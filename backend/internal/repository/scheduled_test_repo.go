package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// --- Plan Repository ---

type scheduledTestPlanRepository struct {
	db *sql.DB
}

const planColumns = `id, account_id, model_id, cron_expression, enabled, max_results, auto_recover,
    last_run_at, next_run_at, created_at, updated_at, test_kind, question_ids, custom_prompt, question_cursor`

const resultColumns = `id, plan_id, status, response_text, error_message, latency_ms, started_at,
    finished_at, created_at, question_snapshot, prompt_snapshot, model_snapshot`

func NewScheduledTestPlanRepository(db *sql.DB) service.ScheduledTestPlanRepository {
	return &scheduledTestPlanRepository{db: db}
}

func (r *scheduledTestPlanRepository) Create(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_plans (account_id, model_id, cron_expression, enabled, max_results, auto_recover, next_run_at, test_kind, question_ids, custom_prompt, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		RETURNING `+planColumns+`
	`, plan.AccountID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, plan.TestKind, pq.Array(append([]int64{}, plan.QuestionIDs...)), plan.CustomPrompt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+planColumns+`
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+planColumns+`
		FROM scheduled_test_plans WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) ListDue(ctx context.Context, now time.Time) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+planColumns+`
		FROM scheduled_test_plans
		WHERE enabled = true AND next_run_at <= $1
		ORDER BY next_run_at ASC
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) Update(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE scheduled_test_plans
		SET model_id = $2, cron_expression = $3, enabled = $4, max_results = $5, auto_recover = $6, next_run_at = $7,
		    question_ids = $8, custom_prompt = $9, updated_at = NOW()
		WHERE id = $1
		RETURNING `+planColumns+`
	`, plan.ID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, pq.Array(append([]int64{}, plan.QuestionIDs...)), plan.CustomPrompt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id = $1`, id)
	return err
}

func (r *scheduledTestPlanRepository) UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time, expectedUpdatedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans SET last_run_at = $2, next_run_at = $3, updated_at = NOW()
		WHERE id = $1 AND updated_at = $4
	`, id, lastRunAt, nextRunAt, expectedUpdatedAt)
	return err
}

// --- Result Repository ---

type scheduledTestResultRepository struct {
	db *sql.DB
}

func NewScheduledTestResultRepository(db *sql.DB) service.ScheduledTestResultRepository {
	return &scheduledTestResultRepository{db: db}
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, error_message, latency_ms, started_at, finished_at,
		    question_snapshot, prompt_snapshot, model_snapshot, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		RETURNING `+resultColumns+`
	`, result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt,
		questionJSON(result.QuestionSnapshot), result.PromptSnapshot, result.ModelSnapshot)

	out := &service.ScheduledTestResult{}
	if err := scanResult(row, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+resultColumns+`
		FROM scheduled_test_results
		WHERE plan_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*service.ScheduledTestResult
	for rows.Next() {
		r := &service.ScheduledTestResult{}
		if err := scanResult(rows, r); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (r *scheduledTestResultRepository) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduled_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY created_at DESC) AS rn
				FROM scheduled_test_results
				WHERE plan_id = $1
			) ranked
			WHERE rn > $2
		)
	`, planID, keepCount)
	return err
}

// --- scan helpers ---

type scannable interface {
	Scan(dest ...any) error
}

func scanPlan(row scannable) (*service.ScheduledTestPlan, error) {
	p := &service.ScheduledTestPlan{}
	if err := row.Scan(
		&p.ID, &p.AccountID, &p.ModelID, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt, &p.TestKind, pq.Array(&p.QuestionIDs), &p.CustomPrompt, &p.QuestionCursor,
	); err != nil {
		return nil, err
	}
	return p, nil
}

func scanPlans(rows *sql.Rows) ([]*service.ScheduledTestPlan, error) {
	var plans []*service.ScheduledTestPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func (r *scheduledTestPlanRepository) TryClaim(ctx context.Context, id int64, until time.Time, token string, requireDue bool) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET claimed_until = $2, claim_token = $3
		WHERE id = $1 AND (NOT $4 OR (enabled AND next_run_at <= NOW()))
		AND (claimed_until IS NULL OR claimed_until < NOW())`, id, until, token, requireDue)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *scheduledTestPlanRepository) ReleaseClaim(ctx context.Context, id int64, token string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET claimed_until = NULL, claim_token = NULL WHERE id = $1 AND claim_token = $2`, id, token)
	return err
}

func (r *scheduledTestPlanRepository) AdvanceQuestion(ctx context.Context, id int64) (int64, error) {
	var cursor int64
	err := r.db.QueryRowContext(ctx, `UPDATE scheduled_test_plans SET question_cursor = question_cursor + 1
		WHERE id = $1 RETURNING question_cursor - 1`, id).Scan(&cursor)
	return cursor, err
}

func scanQuestion(row scannable) (*service.IntelligenceQuestion, error) {
	q := &service.IntelligenceQuestion{}
	if err := row.Scan(&q.ID, &q.Title, &q.Prompt, &q.BuiltIn); err != nil {
		return nil, err
	}
	return q, nil
}

const questionColumns = `id, title, prompt, built_in`

func (r *scheduledTestPlanRepository) ListQuestions(ctx context.Context) ([]*service.IntelligenceQuestion, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+questionColumns+` FROM intelligence_questions ORDER BY built_in DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	questions := make([]*service.IntelligenceQuestion, 0)
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

func (r *scheduledTestPlanRepository) GetQuestion(ctx context.Context, id int64) (*service.IntelligenceQuestion, error) {
	return scanQuestion(r.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM intelligence_questions WHERE id = $1`, id))
}

func (r *scheduledTestPlanRepository) SaveQuestion(ctx context.Context, q *service.IntelligenceQuestion) (*service.IntelligenceQuestion, error) {
	if q.ID == 0 {
		return scanQuestion(r.db.QueryRowContext(ctx, `INSERT INTO intelligence_questions (title, kind, prompt)
			VALUES ($1,'open',$2) RETURNING `+questionColumns, q.Title, q.Prompt))
	}
	return scanQuestion(r.db.QueryRowContext(ctx, `UPDATE intelligence_questions
		SET title=$2, kind='open', prompt=$3, choices='[]'::jsonb, answer='', rubric='', updated_at=NOW()
		WHERE id=$1 AND NOT built_in RETURNING `+questionColumns,
		q.ID, q.Title, q.Prompt))
}

func (r *scheduledTestPlanRepository) DeleteQuestion(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM intelligence_questions WHERE id=$1 AND NOT built_in
		AND NOT EXISTS (SELECT 1 FROM scheduled_test_plans WHERE test_kind='intelligence' AND $1=ANY(question_ids))`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func questionJSON(q *service.IntelligenceQuestion) any {
	if q == nil {
		return nil
	}
	b, _ := json.Marshal(q)
	return b
}

func scanResult(row scannable, out *service.ScheduledTestResult) error {
	var snapshot []byte
	err := row.Scan(&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt, &snapshot,
		&out.PromptSnapshot, &out.ModelSnapshot)
	if err != nil {
		return err
	}
	if len(snapshot) > 0 {
		return json.Unmarshal(snapshot, &out.QuestionSnapshot)
	}
	return nil
}
