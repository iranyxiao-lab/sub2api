package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIntelligenceRunActive = errors.New("an intelligence test is already active")
	ErrIntelligenceNotDue    = errors.New("intelligence plan is not due")
	ErrIntelligenceLeaseLost = errors.New("intelligence run lease lost")
)

func (s *ScheduledTestService) EnqueueRun(ctx context.Context, plan *ScheduledTestPlan, key, trigger string) (*IntelligenceRun, error) {
	if plan.TestKind != "intelligence" {
		return nil, fmt.Errorf("not an intelligence plan")
	}
	if key == "" || len(key) > 100 {
		return nil, fmt.Errorf("invalid idempotency key")
	}
	var next *time.Time
	if trigger == "scheduled" {
		n, err := computeNextRun(plan.CronExpression, time.Now())
		if err != nil {
			return nil, err
		}
		next = &n
	}
	return s.resultRepo.EnqueueIntelligenceRun(ctx, plan.ID, key, trigger, next)
}

func (s *ScheduledTestService) ListRuns(ctx context.Context, planID int64, status string, page, pageSize int) (*IntelligenceRunPage, error) {
	switch status {
	case "", "queued", "running", "success", "failed", "interrupted":
	default:
		return nil, fmt.Errorf("invalid run status")
	}
	if page < 1 || page > 100000 || pageSize < 1 || pageSize > 20 {
		return nil, fmt.Errorf("invalid pagination")
	}
	return s.resultRepo.ListIntelligenceRuns(ctx, planID, status, page, pageSize)
}

func (s *ScheduledTestService) GetRun(ctx context.Context, planID, runID int64) (*IntelligenceRun, error) {
	return s.resultRepo.GetIntelligenceRun(ctx, planID, runID)
}

func (s *ScheduledTestService) waitForRun(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestResult, error) {
	run, err := s.EnqueueRun(ctx, plan, uuid.NewString(), "manual")
	if err != nil {
		return nil, err
	}
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			run, err = s.GetRun(ctx, plan.ID, run.ID)
			if err != nil {
				return nil, err
			}
			if run.Status != "queued" && run.Status != "running" {
				return &run.ScheduledTestResult, nil
			}
		}
	}
}

// Called by the runner lifecycle, never by a browser request goroutine.
func (s *ScheduledTestService) runIntelligenceWorker(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			run, err := s.resultRepo.ClaimIntelligenceRun(claimCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[IntelligenceWorker] claim failed: %v", err)
				}
				continue
			}
			if run != nil {
				s.executeIntelligenceRun(ctx, run)
			}
		}
	}
}

func (s *ScheduledTestService) executeIntelligenceRun(parent context.Context, run *IntelligenceRun) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	started := time.Now()
	result := &ScheduledTestResult{Status: "interrupted", ErrorCode: "worker_interrupted", ErrorMessage: "Test worker was interrupted"}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Status = "interrupted"
			result.ErrorCode = "worker_interrupted"
			result.ErrorMessage = "Test worker failed unexpectedly"
			log.Printf("[IntelligenceWorker] run=%d panic contained", run.ID)
		}
		result.LatencyMs = time.Since(started).Milliseconds()
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer saveCancel()
		if err := s.resultRepo.CompleteIntelligenceRun(saveCtx, run, result); err != nil {
			log.Printf("[IntelligenceWorker] run=%d completion failed: %v", run.ID, err)
			return
		}
		if err := s.resultRepo.PruneOldResults(saveCtx, run.PlanID, max(1, run.MaxResults)); err != nil {
			log.Printf("[IntelligenceWorker] run=%d pruning failed: %v", run.ID, err)
		}
	}()
	output, err := s.accountTest.RunTestBackground(ctx, run.AccountID, run.ModelSnapshot, run.PromptSnapshot)
	if output != nil {
		result = output
	}
	if err != nil {
		result.Status = "failed"
		result.ErrorMessage = err.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Status = "failed"
		result.ErrorCode = "execution_timeout"
	} else if ctx.Err() != nil {
		result.Status = "interrupted"
		result.ErrorCode = "worker_interrupted"
	} else if result.Status == "failed" {
		result.ErrorCode = "upstream_error"
	}
	// Preserve technical details and any partial text. Do not mistake cancellation
	// after partial output for successful protocol completion.
	if result.Status != "success" && strings.TrimSpace(result.ErrorMessage) == "" {
		result.ErrorMessage = "Test did not complete"
	}
}
