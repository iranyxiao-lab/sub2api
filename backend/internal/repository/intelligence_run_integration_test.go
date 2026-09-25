//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIntelligenceRunDurabilityAndRecovery(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "durable-intelligence", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}})
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	q, err := plans.SaveQuestion(ctx, &service.IntelligenceQuestion{Title: "HTML", Prompt: "Build a table"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)
		_ = plans.DeleteQuestion(ctx, q.ID)
	})
	next := time.Now().Add(time.Hour)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: "test-model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 10, TestKind: "intelligence", QuestionIDs: []int64{q.ID}, CustomPrompt: "Accessible HTML", NextRunAt: &next})
	require.NoError(t, err)
	var wg sync.WaitGroup
	ids := make(chan int64, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "request-1", "manual", nil, 300)
			if err != nil {
				errs <- err
				return
			}
			ids <- run.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var id int64
	for value := range ids {
		if id == 0 {
			id = value
		}
		require.Equal(t, id, value)
	}
	require.NotZero(t, id)
	queued, err := results.GetIntelligenceRun(ctx, plan.ID, id)
	require.NoError(t, err)
	require.Equal(t, "queued", queued.Status)
	require.Equal(t, 300, queued.ExecutionTimeoutSeconds)
	var planLockSeconds float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM claimed_until-NOW()) FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&planLockSeconds))
	require.InDelta(t, 930, planLockSeconds, 5)
	require.Nil(t, queued.StartedAt)
	require.Nil(t, queued.FinishedAt)
	raw, err := json.Marshal(queued)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"started_at":null`)
	require.Equal(t, "Accessible HTML\n\nBuild a table", queued.PromptSnapshot)
	other, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "request-2", "manual", nil, 300)
	require.NoError(t, err)
	require.Equal(t, id, other.ID)
	require.ErrorIs(t, plans.Delete(ctx, plan.ID), service.ErrIntelligenceRunActive)
	legacy, err := results.ListByPlanID(ctx, plan.ID, 20)
	require.NoError(t, err)
	require.Empty(t, legacy)
	claimed, err := results.ClaimIntelligenceRun(ctx)
	require.NoError(t, err)
	require.Equal(t, id, claimed.ID)
	var leaseSeconds float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM lease_until-started_at) FROM scheduled_test_results WHERE id=$1`, id).Scan(&leaseSeconds))
	require.Equal(t, float64(330), leaseSeconds)
	// Completion after the former 150-second lease remains valid under the new snapshot.
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET started_at=NOW()-INTERVAL '160 seconds', lease_until=NOW()+INTERVAL '170 seconds' WHERE id=$1`, id)
	require.NoError(t, err)
	none, err := results.ClaimIntelligenceRun(ctx)
	require.NoError(t, err)
	require.Nil(t, none)
	require.NoError(t, results.PruneOldResults(ctx, plan.ID, 0))
	require.NoError(t, results.CompleteIntelligenceRun(ctx, claimed, &service.ScheduledTestResult{Status: "failed", ResponseText: "<h1>Partial</h1>", ErrorMessage: "context deadline exceeded", ErrorCode: "execution_timeout", OutputTruncated: true, LatencyMs: 120000}))
	finished, err := results.GetIntelligenceRun(ctx, plan.ID, id)
	require.NoError(t, err)
	require.Equal(t, "failed", finished.Status)
	require.Equal(t, "<h1>Partial</h1>", finished.ResponseText)
	require.True(t, finished.OutputTruncated)
	require.Equal(t, 300, finished.ExecutionTimeoutSeconds)
	var released bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT claimed_until IS NULL AND claim_token IS NULL FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&released))
	require.True(t, released)
	require.NotNil(t, finished.FinishedAt)
	same, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "request-1", "manual", nil, 600)
	require.NoError(t, err)
	require.Equal(t, id, same.ID)
	require.Equal(t, 300, same.ExecutionTimeoutSeconds, "retry must preserve the original snapshot")
	page, err := results.ListIntelligenceRuns(ctx, plan.ID, "failed", 1, 10)
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Zero(t, page.ActiveCount)
	require.Empty(t, page.Items[0].PromptSnapshot)
	second, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "request-3", "manual", nil, 300)
	require.NoError(t, err)
	second, err = results.ClaimIntelligenceRun(ctx)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, second.ID)
	require.NoError(t, err)
	none, err = results.ClaimIntelligenceRun(ctx)
	require.NoError(t, err)
	require.Nil(t, none)
	interrupted, err := results.GetIntelligenceRun(ctx, plan.ID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", interrupted.Status)
	require.ErrorIs(t, results.CompleteIntelligenceRun(ctx, second, &service.ScheduledTestResult{Status: "success"}), service.ErrIntelligenceLeaseLost)
	third, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "request-4", "manual", nil, 300)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results SET created_at=NOW()-INTERVAL '11 minutes' WHERE id=$1`, third.ID)
	require.NoError(t, err)
	none, err = results.ClaimIntelligenceRun(ctx)
	require.NoError(t, err)
	require.Nil(t, none)
	expired, err := results.GetIntelligenceRun(ctx, plan.ID, third.ID)
	require.NoError(t, err)
	require.Equal(t, "queue_timeout", expired.ErrorCode)
	// Stale scheduled submissions must not enqueue after a manual run moved the plan forward.
	_, err = results.EnqueueIntelligenceRun(ctx, plan.ID, "schedule-early", "scheduled", &next, 300)
	require.ErrorIs(t, err, service.ErrIntelligenceNotDue)
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	scheduled, err := results.EnqueueIntelligenceRun(ctx, plan.ID, "schedule-due", "scheduled", &next, 300)
	require.NoError(t, err)
	require.Equal(t, "scheduled", scheduled.TriggerType)
	current, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, next, *current.NextRunAt, time.Millisecond)
	// Old application writers omit the new column and must remain compatible.
	var legacyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO scheduled_test_results(plan_id,status) VALUES($1,'success') RETURNING id`, plan.ID).Scan(&legacyID))
	oldRun, err := results.GetIntelligenceRun(ctx, plan.ID, legacyID)
	require.NoError(t, err)
	require.Equal(t, 120, oldRun.ExecutionTimeoutSeconds)
}
