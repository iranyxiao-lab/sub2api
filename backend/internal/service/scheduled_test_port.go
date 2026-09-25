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
	ErrorCode        string                `json:"error_code,omitempty"`
	OutputTruncated  bool                  `json:"output_truncated"`
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
	EnqueueIntelligenceRun(ctx context.Context, planID int64, requestKey, trigger string, nextRun *time.Time, timeoutSeconds int) (*IntelligenceRun, error)
	ClaimIntelligenceRun(ctx context.Context) (*IntelligenceRun, error)
	CompleteIntelligenceRun(ctx context.Context, run *IntelligenceRun, result *ScheduledTestResult) error
	GetIntelligenceRun(ctx context.Context, planID, runID int64) (*IntelligenceRun, error)
	ListIntelligenceRuns(ctx context.Context, planID int64, status string, page, pageSize int) (*IntelligenceRunPage, error)
}

// Nullable lifecycle times avoid presenting queued work as already completed.
type IntelligenceRun struct {
	ScheduledTestResult
	ExecutionTimeoutSeconds int        `json:"execution_timeout_seconds"`
	QueuedAt                time.Time  `json:"queued_at"`
	StartedAt               *time.Time `json:"started_at"`
	FinishedAt              *time.Time `json:"finished_at"`
	TriggerType             string     `json:"trigger_type"`
	RunToken                string     `json:"-"`
	AccountID               int64      `json:"-"`
	MaxResults              int        `json:"-"`
}

type IntelligenceRunPage struct {
	Items       []*IntelligenceRun `json:"items"`
	Total       int                `json:"total"`
	ActiveCount int                `json:"active_count"`
	Page        int                `json:"page"`
	PageSize    int                `json:"page_size"`
}
