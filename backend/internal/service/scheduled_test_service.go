package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo    ScheduledTestPlanRepository
	resultRepo  ScheduledTestResultRepository
	accountTest *AccountTestService
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
	accountTest *AccountTestService,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:    planRepo,
		resultRepo:  resultRepo,
		accountTest: accountTest,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := s.validatePlan(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := s.validatePlan(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

func (s *ScheduledTestService) validatePlan(ctx context.Context, plan *ScheduledTestPlan) error {
	if plan.TestKind == "" {
		plan.TestKind = "connectivity"
	}
	if plan.TestKind != "connectivity" && plan.TestKind != "intelligence" {
		return fmt.Errorf("invalid test kind")
	}
	if len(plan.ModelID) > 100 || len(plan.CustomPrompt) > 4000 {
		return fmt.Errorf("model or prompt too long")
	}
	if plan.TestKind == "connectivity" {
		plan.QuestionIDs = nil
		plan.CustomPrompt = ""
		return nil
	}
	if plan.MaxResults < 0 || plan.MaxResults > 200 {
		return fmt.Errorf("max_results must be between 1 and 200")
	}
	if plan.ModelID == "" || len(plan.QuestionIDs) == 0 || len(plan.QuestionIDs) > 50 {
		return fmt.Errorf("select a model and 1-50 questions")
	}
	if isOpenAIImageModel(plan.ModelID) || isGrokImageGenerationModel(plan.ModelID) || isGrokVideoGenerationModel(plan.ModelID) || isImageGenerationModel(plan.ModelID) {
		return fmt.Errorf("select a text model for intelligence tests")
	}
	account, err := s.accountTest.accountRepo.GetByID(ctx, plan.AccountID)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}
	if account.IsSyntheticUITest() {
		return fmt.Errorf("synthetic accounts cannot run intelligence tests")
	}
	if plan.AutoRecover {
		return fmt.Errorf("intelligence tests cannot auto-recover accounts")
	}
	seen := make(map[int64]bool)
	for _, id := range plan.QuestionIDs {
		if id < 1 || seen[id] {
			return fmt.Errorf("invalid or duplicate question")
		}
		seen[id] = true
		if _, err := s.planRepo.GetQuestion(ctx, id); err != nil {
			return fmt.Errorf("question %d not found: %w", id, err)
		}
	}
	sched, err := scheduledTestCronParser.Parse(plan.CronExpression)
	if err != nil {
		return err
	}
	next := sched.Next(time.Now())
	if sched.Next(next).Sub(next) < 15*time.Minute {
		return fmt.Errorf("intelligence tests require at least 15 minutes between runs")
	}
	return nil
}

func (s *ScheduledTestService) ListQuestions(ctx context.Context) ([]*IntelligenceQuestion, error) {
	return s.planRepo.ListQuestions(ctx)
}

func (s *ScheduledTestService) SaveQuestion(ctx context.Context, q *IntelligenceQuestion) (*IntelligenceQuestion, error) {
	q.Title, q.Prompt = strings.TrimSpace(q.Title), strings.TrimSpace(q.Prompt)
	if q.BuiltIn || q.Title == "" || q.Prompt == "" || len(q.Title) > 160 || len(q.Prompt) > 8000 {
		return nil, fmt.Errorf("invalid question fields")
	}
	return s.planRepo.SaveQuestion(ctx, q)
}

func (s *ScheduledTestService) DeleteQuestion(ctx context.Context, id int64) error {
	return s.planRepo.DeleteQuestion(ctx, id)
}

func (s *ScheduledTestService) RunIntelligence(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestResult, error) {
	return s.waitForRun(ctx, plan)
}

func (s *ScheduledTestService) RunIntelligenceNow(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestResult, error) {
	return s.waitForRun(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
