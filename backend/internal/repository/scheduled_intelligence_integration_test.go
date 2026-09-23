//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestIntelligencePlanQuestionResultAndReviewRoundTrip(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name: "intelligence-roundtrip", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "fixture"},
	})
	plans := NewScheduledTestPlanRepository(integrationDB)
	results := NewScheduledTestResultRepository(integrationDB)
	question, err := plans.SaveQuestion(ctx, &service.IntelligenceQuestion{
		Title: "HTML task", Kind: "open", Prompt: "Create a table", Choices: []string{}, Rubric: "Readable",
	})
	require.NoError(t, err)
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
		GradeStatus: "pending", StartedAt: now, FinishedAt: now,
	})
	require.NoError(t, err)
	result, err = results.Review(ctx, plan.ID, result.ID, account.ID, 85, "Readable table")
	require.NoError(t, err)
	require.Equal(t, "reviewed", result.GradeStatus)
	require.Equal(t, 85, *result.Score)
	require.Equal(t, "Create a table", result.QuestionSnapshot.Prompt)
	choice := &service.IntelligenceQuestion{ID: question.ID, Title: "Ambiguous choice", Kind: "choice", Prompt: "Pick A or B", Choices: []string{"first", "second"}, Answer: "A"}
	pending, err := results.Create(ctx, &service.ScheduledTestResult{
		PlanID: plan.ID, Status: "success", ResponseText: "I think A", QuestionSnapshot: choice,
		GradeStatus: "pending", StartedAt: now, FinishedAt: now,
	})
	require.NoError(t, err)
	pending, err = results.Review(ctx, plan.ID, pending.ID, account.ID, 100, "Accepted after review")
	require.NoError(t, err)
	require.Equal(t, "reviewed", pending.GradeStatus)
	_, err = results.Review(ctx, plan.ID, pending.ID, account.ID, 0, "Cannot override")
	require.Error(t, err)
}
