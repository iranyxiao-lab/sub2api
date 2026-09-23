package service

import (
	"context"
	"time"
)

// ScheduledTestPlan represents a scheduled test plan domain model.
type ScheduledTestPlan struct {
	ID             int64      `json:"id"`
	AccountID      int64      `json:"account_id"`
	ModelID        string     `json:"model_id"`
	CronExpression string     `json:"cron_expression"`
	Enabled        bool       `json:"enabled"`
	MaxResults     int        `json:"max_results"`
	AutoRecover    bool       `json:"auto_recover"`
	TestKind       string     `json:"test_kind"`
	QuestionIDs    []int64    `json:"question_ids"`
	CustomPrompt   string     `json:"custom_prompt"`
	QuestionCursor int64      `json:"-"`
	LastRunAt      *time.Time `json:"last_run_at"`
	NextRunAt      *time.Time `json:"next_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	ID               int64                 `json:"id"`
	PlanID           int64                 `json:"plan_id"`
	Status           string                `json:"status"`
	ResponseText     string                `json:"response_text"`
	ErrorMessage     string                `json:"error_message"`
	QuestionSnapshot *IntelligenceQuestion `json:"question_snapshot,omitempty"`
	PromptSnapshot   string                `json:"prompt_snapshot,omitempty"`
	ModelSnapshot    string                `json:"model_snapshot,omitempty"`
	LatencyMs        int64                 `json:"latency_ms"`
	StartedAt        time.Time             `json:"started_at"`
	FinishedAt       time.Time             `json:"finished_at"`
	CreatedAt        time.Time             `json:"created_at"`
}

// ScheduledTestPlanRepository defines the data access interface for test plans.
type ScheduledTestPlanRepository interface {
	Create(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error)
	ListDue(ctx context.Context, now time.Time) ([]*ScheduledTestPlan, error)
	Update(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	Delete(ctx context.Context, id int64) error
	UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time, expectedUpdatedAt time.Time) error
	TryClaim(ctx context.Context, id int64, until time.Time, token string, requireDue bool) (bool, error)
	AdvanceQuestion(ctx context.Context, id int64) (int64, error)
	ReleaseClaim(ctx context.Context, id int64, token string) error
	ListQuestions(ctx context.Context) ([]*IntelligenceQuestion, error)
	GetQuestion(ctx context.Context, id int64) (*IntelligenceQuestion, error)
	SaveQuestion(ctx context.Context, question *IntelligenceQuestion) (*IntelligenceQuestion, error)
	DeleteQuestion(ctx context.Context, id int64) error
}

type IntelligenceQuestion struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	BuiltIn bool   `json:"built_in"`
}

// ScheduledTestResultRepository defines the data access interface for test results.
type ScheduledTestResultRepository interface {
	Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error)
	ListByPlanID(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error)
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}
