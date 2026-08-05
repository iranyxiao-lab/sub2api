package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SettlementPreparation struct {
	IntentID           int64
	Status             IntentStatus
	ExpectedAmountRaw  string
	ReceivedAmountRaw  string
	PendingAmountRaw   string
	Ready              bool
	ReviewRequired     bool
	SettlementAttempts int
}

type SettlementStore interface {
	ListSettlementCandidates(ctx context.Context, network Network, limit int) ([]int64, error)
	PrepareIntentSettlement(ctx context.Context, intentID int64) (SettlementPreparation, error)
	RecordSettlementFailure(ctx context.Context, failure SettlementFailure) error
}

type DepositConfirmer interface {
	ConfirmOnchainDeposit(ctx context.Context, intentID int64) error
}

type SettlementWorkerOptions struct {
	Network         Network
	BatchSize       int
	PollInterval    time.Duration
	MinRetryBackoff time.Duration
	MaxRetryBackoff time.Duration
	ClassifyError   SettlementErrorClassifier
}

type SettlementFailureClass struct {
	Code      string
	Retryable bool
}

type SettlementErrorClassifier func(error) SettlementFailureClass

type SettlementFailure struct {
	IntentID    int64
	Code        string
	Message     string
	Retryable   bool
	NextRetryAt time.Time
}

type SettlementBatchResult struct {
	Candidates int
	Skipped    int
	Underpaid  int
	Review     int
	Ready      int
	Confirmed  int
	Failed     int
}

type SettlementWorker struct {
	store        SettlementStore
	confirmer    DepositConfirmer
	network      Network
	batchSize    int
	pollInterval time.Duration
	minBackoff   time.Duration
	maxBackoff   time.Duration
	classify     SettlementErrorClassifier
}

func NewSettlementWorker(store SettlementStore, confirmer DepositConfirmer, options SettlementWorkerOptions) (*SettlementWorker, error) {
	if store == nil || confirmer == nil {
		return nil, fmt.Errorf("settlement store and deposit confirmer are required")
	}
	if strings.TrimSpace(string(options.Network)) == "" {
		return nil, fmt.Errorf("settlement network is required")
	}
	if options.BatchSize <= 0 {
		return nil, fmt.Errorf("settlement batch size must be positive")
	}
	if options.PollInterval <= 0 {
		return nil, fmt.Errorf("settlement poll interval must be positive")
	}
	if options.MinRetryBackoff <= 0 {
		options.MinRetryBackoff = time.Second
	}
	if options.MaxRetryBackoff < options.MinRetryBackoff {
		options.MaxRetryBackoff = time.Minute
	}
	if options.ClassifyError == nil {
		options.ClassifyError = defaultSettlementErrorClass
	}
	return &SettlementWorker{
		store: store, confirmer: confirmer, network: options.Network,
		batchSize: options.BatchSize, pollInterval: options.PollInterval,
		minBackoff: options.MinRetryBackoff, maxBackoff: options.MaxRetryBackoff,
		classify: options.ClassifyError,
	}, nil
}

func (w *SettlementWorker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		result, _ := w.RunOnce(ctx)
		if result.Candidates == 0 && !waitSettlementWorker(ctx, w.pollInterval) {
			break
		}
	}
	return nil
}

// RunOnce isolates every payment intent so one failed fulfillment does not
// prevent the remaining candidates from being prepared or confirmed.
func (w *SettlementWorker) RunOnce(ctx context.Context) (SettlementBatchResult, error) {
	ids, err := w.store.ListSettlementCandidates(ctx, w.network, w.batchSize)
	if err != nil {
		return SettlementBatchResult{}, fmt.Errorf("list settlement candidates: %w", err)
	}
	result := SettlementBatchResult{Candidates: len(ids)}
	var batchErr error
	for _, intentID := range ids {
		prepared, prepareErr := w.store.PrepareIntentSettlement(ctx, intentID)
		if prepareErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("prepare intent %d: %w", intentID, prepareErr))
			continue
		}
		if prepared.IntentID == 0 {
			result.Skipped++
			continue
		}
		if prepared.ReviewRequired {
			result.Review++
			continue
		}
		if !prepared.Ready {
			result.Underpaid++
			continue
		}
		result.Ready++
		if confirmErr := w.confirmer.ConfirmOnchainDeposit(ctx, intentID); confirmErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("confirm intent %d: %w", intentID, confirmErr))
			class := w.classify(confirmErr)
			delay := settlementRetryBackoff(w.minBackoff, w.maxBackoff, prepared.SettlementAttempts+1)
			recordErr := w.store.RecordSettlementFailure(ctx, SettlementFailure{
				IntentID: intentID, Code: class.Code, Message: confirmErr.Error(), Retryable: class.Retryable,
				NextRetryAt: time.Now().UTC().Add(delay),
			})
			if recordErr != nil {
				batchErr = errors.Join(batchErr, fmt.Errorf("record failure for intent %d: %w", intentID, recordErr))
			}
			continue
		}
		result.Confirmed++
	}
	return result, batchErr
}

func defaultSettlementErrorClass(err error) SettlementFailureClass {
	if err == nil {
		return SettlementFailureClass{}
	}
	return SettlementFailureClass{Code: "SETTLEMENT_TRANSIENT", Retryable: true}
}

func settlementRetryBackoff(minimum, maximum time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := minimum
	for attempt := 1; attempt < attempts && delay < maximum; attempt++ {
		if delay >= maximum-delay {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func waitSettlementWorker(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
