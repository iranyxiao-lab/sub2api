package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const runColumns = resultColumns + `, trigger_type, COALESCE(run_token,'')`

func decodeRunQuestion(snapshot []byte, result *service.ScheduledTestResult) error {
	if len(snapshot) == 0 {
		return nil
	}
	return json.Unmarshal(snapshot, &result.QuestionSnapshot)
}

func scanIntelligenceRun(row scannable) (*service.IntelligenceRun, error) {
	r := &service.IntelligenceRun{}
	var snapshot []byte
	b := &r.ScheduledTestResult
	err := row.Scan(&b.ID, &b.PlanID, &b.Status, &b.ResponseText, &b.ErrorMessage, &b.LatencyMs, &b.StartedAt, &b.FinishedAt, &b.CreatedAt, &snapshot, &b.PromptSnapshot, &b.ModelSnapshot, &b.ErrorCode, &b.OutputTruncated, &r.TriggerType, &r.RunToken)
	if err != nil {
		return nil, err
	}
	if err := decodeRunQuestion(snapshot, b); err != nil {
		return nil, err
	}
	r.QueuedAt = b.CreatedAt
	if b.Status != "queued" && b.ErrorCode != "queue_timeout" {
		r.StartedAt = &b.StartedAt
	}
	if b.Status != "queued" && b.Status != "running" {
		r.FinishedAt = &b.FinishedAt
	}
	return r, nil
}

// This short transaction lock serializes global admission/claim decisions, not
// external requests. All processes share one execution slot.
func lockRunAdmission(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(72420421)`)
	return err
}

func (r *scheduledTestResultRepository) EnqueueIntelligenceRun(ctx context.Context, planID int64, requestKey, trigger string, nextRun *time.Time) (*service.IntelligenceRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockRunAdmission(ctx, tx); err != nil {
		return nil, err
	}
	plan, err := scanPlan(tx.QueryRowContext(ctx, `SELECT `+planColumns+` FROM scheduled_test_plans WHERE id=$1 FOR UPDATE`, planID))
	if err != nil {
		return nil, err
	}
	if plan.TestKind != "intelligence" || len(plan.QuestionIDs) == 0 {
		return nil, fmt.Errorf("invalid intelligence plan")
	}
	existing, err := scanIntelligenceRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM scheduled_test_results WHERE plan_id=$1 AND (request_key=$2 OR status IN ('queued','running')) ORDER BY (request_key=$2) DESC, id DESC LIMIT 1`, planID, requestKey))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if trigger == "scheduled" && (!plan.Enabled || plan.NextRunAt == nil || plan.NextRunAt.After(time.Now())) {
		return nil, service.ErrIntelligenceNotDue
	}
	token := uuid.NewString()
	res, err := tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET claimed_until=NOW()+INTERVAL '13 minutes', claim_token=$2 WHERE id=$1 AND (claimed_until IS NULL OR claimed_until<NOW())`, planID, token)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, service.ErrIntelligenceRunActive
	}
	q, err := scanQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM intelligence_questions WHERE id=$1`, plan.QuestionIDs[plan.QuestionCursor%int64(len(plan.QuestionIDs))]))
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(plan.CustomPrompt + "\n\n" + q.Prompt)
	run, err := scanIntelligenceRun(tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_results(plan_id,status,question_snapshot,prompt_snapshot,model_snapshot,request_key,trigger_type,run_token) VALUES($1,'queued',$2,$3,$4,$5,$6,$7) RETURNING `+runColumns, planID, questionJSON(q), prompt, plan.ModelID, requestKey, trigger, token))
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET question_cursor=question_cursor+1, next_run_at=COALESCE($2,next_run_at) WHERE id=$1`, planID, nextRun)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *scheduledTestResultRepository) ClaimIntelligenceRun(ctx context.Context) (*service.IntelligenceRun, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockRunAdmission(ctx, tx); err != nil {
		return nil, err
	}
	// Never repeat an upstream request after lease expiry. Release only its own plan lease.
	_, err = tx.ExecContext(ctx, `WITH expired AS (
 UPDATE scheduled_test_results SET status=CASE WHEN status='queued' THEN 'failed' ELSE 'interrupted' END,
 error_code=CASE WHEN status='queued' THEN 'queue_timeout' ELSE 'worker_interrupted' END,
 error_message=CASE WHEN status='queued' THEN 'Queue wait exceeded 10 minutes' ELSE 'Execution lease expired; retry manually' END,
 finished_at=NOW(), lease_until=NULL
 WHERE (status='running' AND lease_until<NOW()) OR (status='queued' AND created_at<NOW()-INTERVAL '10 minutes') RETURNING plan_id,run_token
 ) UPDATE scheduled_test_plans p SET claimed_until=NULL,claim_token=NULL FROM expired e WHERE p.id=e.plan_id AND p.claim_token=e.run_token`)
	if err != nil {
		return nil, err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_results WHERE status='running')`).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, tx.Commit()
	}
	run, err := scanIntelligenceRun(tx.QueryRowContext(ctx, `UPDATE scheduled_test_results SET status='running', started_at=NOW(), lease_until=NOW()+INTERVAL '150 seconds'
 WHERE id=(SELECT id FROM scheduled_test_results WHERE status='queued' ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING `+runColumns))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT account_id,max_results FROM scheduled_test_plans WHERE id=$1`, run.PlanID).Scan(&run.AccountID, &run.MaxResults)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *scheduledTestResultRepository) CompleteIntelligenceRun(ctx context.Context, run *service.IntelligenceRun, result *service.ScheduledTestResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE scheduled_test_results SET status=$3,response_text=$4,error_message=$5,error_code=$6,output_truncated=$7,latency_ms=$8,finished_at=NOW(),lease_until=NULL WHERE id=$1 AND run_token=$2 AND status='running' AND lease_until>NOW()`, run.ID, run.RunToken, result.Status, result.ResponseText, result.ErrorMessage, result.ErrorCode, result.OutputTruncated, result.LatencyMs)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrIntelligenceLeaseLost
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET claimed_until=NULL,claim_token=NULL,last_run_at=NOW() WHERE id=$1 AND claim_token=$2`, run.PlanID, run.RunToken)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scheduledTestResultRepository) GetIntelligenceRun(ctx context.Context, planID, runID int64) (*service.IntelligenceRun, error) {
	return scanIntelligenceRun(r.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM scheduled_test_results WHERE plan_id=$1 AND id=$2`, planID, runID))
}

func (r *scheduledTestResultRepository) ListIntelligenceRuns(ctx context.Context, planID int64, status string, page, pageSize int) (*service.IntelligenceRunPage, error) {
	out := &service.IntelligenceRunPage{Items: []*service.IntelligenceRun{}, Page: page, PageSize: pageSize}
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE $2='' OR status=$2),count(*) FILTER(WHERE status IN ('queued','running')) FROM scheduled_test_results WHERE plan_id=$1`, planID, status).Scan(&out.Total, &out.ActiveCount)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+runColumns+` FROM scheduled_test_results WHERE plan_id=$1 AND ($2='' OR status=$2) ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, planID, status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanIntelligenceRun(rows)
		if err != nil {
			return nil, err
		}
		run.PromptSnapshot = ""
		out.Items = append(out.Items, run)
	}
	return out, rows.Err()
}
