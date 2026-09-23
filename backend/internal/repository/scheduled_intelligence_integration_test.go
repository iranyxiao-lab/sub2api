//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIntelligencePlanPromptAndResultRoundTrip(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name: "intelligence-roundtrip", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "fixture"},
	})
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	question, err := plans.SaveQuestion(ctx, &service.IntelligenceQuestion{
		Title: "HTML task", Prompt: "Create a table",
	})
	require.NoError(t, err)
	var kind, answer string
	var choices []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT kind, choices, answer FROM intelligence_questions WHERE id=$1`, question.ID).Scan(&kind, &choices, &answer))
	require.Equal(t, "open", kind)
	require.JSONEq(t, `[]`, string(choices))
	require.Empty(t, answer)
	t.Cleanup(func() {
		_ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(context.Background())
		_ = plans.DeleteQuestion(context.Background(), question.ID)
	})
	next := time.Now().Add(time.Hour)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		AccountID: account.ID, ModelID: "text-model", CronExpression: "0 * * * *", Enabled: true,
		MaxResults: 20, TestKind: "intelligence", QuestionIDs: []int64{question.ID}, NextRunAt: &next,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{question.ID}, plan.QuestionIDs)
	require.Equal(t, "intelligence", plan.TestKind)
	// An in-flight run must not overwrite an administrator's newer schedule.
	changed := *plan
	changed.CronExpression = "0 12 * * *"
	changed.NextRunAt = &next
	_, err = plans.Update(ctx, &changed)
	require.NoError(t, err)
	later := next.Add(time.Hour)
	require.NoError(t, plans.UpdateAfterRun(ctx, plan.ID, time.Now(), later, plan.UpdatedAt))
	current, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, changed.CronExpression, current.CronExpression)
	require.WithinDuration(t, next, *current.NextRunAt, time.Microsecond)
	claimed, err := plans.TryClaim(ctx, plan.ID, time.Now().Add(time.Minute), "owner-1", false)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = plans.TryClaim(ctx, plan.ID, time.Now().Add(time.Minute), "owner-2", false)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, plans.ReleaseClaim(ctx, plan.ID, "owner-2"))
	claimed, err = plans.TryClaim(ctx, plan.ID, time.Now().Add(time.Minute), "owner-2", false)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, plans.ReleaseClaim(ctx, plan.ID, "owner-1"))
	cursor, err := plans.AdvanceQuestion(ctx, plan.ID)
	require.NoError(t, err)
	require.Zero(t, cursor)
	now := time.Now()
	result, err := results.Create(ctx, &service.ScheduledTestResult{
		PlanID: plan.ID, Status: "success", ResponseText: "<html><table></table></html>",
		QuestionSnapshot: question, PromptSnapshot: question.Prompt, ModelSnapshot: plan.ModelID,
		StartedAt: now, FinishedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "Create a table", result.QuestionSnapshot.Prompt)
	require.Equal(t, "<html><table></table></html>", result.ResponseText)
	var score *int
	var grade *string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT score, grade_status FROM scheduled_test_results WHERE id=$1`, result.ID).Scan(&score, &grade))
	require.Nil(t, score)
	require.Nil(t, grade)
	// Existing scored results and choice questions remain in storage, but their
	// obsolete answer and grading fields are not returned by the new API.
	_, err = integrationDB.ExecContext(ctx, `UPDATE scheduled_test_results
		SET score=85, grade_status='reviewed', question_snapshot=$2::jsonb WHERE id=$1`, result.ID,
		`{"id":1,"title":"Legacy","kind":"choice","prompt":"Pick one","choices":["Yes","No"],"answer":"B","built_in":false}`)
	require.NoError(t, err)
	history, err := results.ListByPlanID(ctx, plan.ID, 20)
	require.NoError(t, err)
	require.Len(t, history, 1)
	encoded, err := json.Marshal(history[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "answer")
	require.NotContains(t, string(encoded), "choices")
	require.NotContains(t, string(encoded), "score")
	require.Equal(t, "Pick one", history[0].QuestionSnapshot.Prompt)
	_, err = integrationDB.ExecContext(ctx, `UPDATE intelligence_questions SET kind='choice', choices='["Yes","No"]'::jsonb, answer='B' WHERE id=$1`, question.ID)
	require.NoError(t, err)
	question, err = plans.GetQuestion(ctx, question.ID)
	require.NoError(t, err)
	question.Prompt = "Revised prompt"
	_, err = plans.SaveQuestion(ctx, question)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT kind, choices, answer FROM intelligence_questions WHERE id=$1`, question.ID).Scan(&kind, &choices, &answer))
	require.Equal(t, "open", kind)
	require.JSONEq(t, `[]`, string(choices))
	require.Empty(t, answer)
}
