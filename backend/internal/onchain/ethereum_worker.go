package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type EthereumBatchScanner interface {
	ScanBatch(ctx context.Context, leaseOwner string) (EthereumScanBatchResult, error)
}

type EthereumScanLeaseStore interface {
	AcquireCursorLease(ctx context.Context, network, owner string, now, leaseUntil time.Time) (bool, error)
	RenewCursorLease(ctx context.Context, network, owner string, now, leaseUntil time.Time) (bool, error)
	ReleaseCursorLease(ctx context.Context, network, owner string) (bool, error)
}

type EthereumScanWorkerOptions struct {
	Network       Network
	Owner         string
	LeaseDuration time.Duration
	RenewInterval time.Duration
	PollInterval  time.Duration
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
}

type EthereumScanWorker struct {
	scanner       EthereumBatchScanner
	leases        EthereumScanLeaseStore
	network       Network
	owner         string
	leaseDuration time.Duration
	renewInterval time.Duration
	pollInterval  time.Duration
	minBackoff    time.Duration
	maxBackoff    time.Duration
}

func NewEthereumScanWorker(scanner EthereumBatchScanner, leases EthereumScanLeaseStore, options EthereumScanWorkerOptions) (*EthereumScanWorker, error) {
	if scanner == nil || leases == nil {
		return nil, fmt.Errorf("Ethereum scanner and lease store are required")
	}
	if options.Network != NetworkEthereumMainnet && options.Network != NetworkEthereumSepolia {
		return nil, fmt.Errorf("Ethereum scan worker does not support network %q", options.Network)
	}
	options.Owner = strings.TrimSpace(options.Owner)
	if options.Owner == "" || len(options.Owner) > 128 {
		return nil, fmt.Errorf("Ethereum scan worker owner must contain 1 to 128 characters")
	}
	if options.LeaseDuration <= 0 || options.RenewInterval <= 0 || options.RenewInterval >= options.LeaseDuration {
		return nil, fmt.Errorf("Ethereum scan worker renew interval must be shorter than its lease duration")
	}
	if options.PollInterval <= 0 || options.MinBackoff <= 0 || options.MaxBackoff < options.MinBackoff {
		return nil, fmt.Errorf("Ethereum scan worker polling and backoff durations are invalid")
	}
	return &EthereumScanWorker{
		scanner: scanner, leases: leases, network: options.Network, owner: options.Owner,
		leaseDuration: options.LeaseDuration, renewInterval: options.RenewInterval,
		pollInterval: options.PollInterval, minBackoff: options.MinBackoff, maxBackoff: options.MaxBackoff,
	}, nil
}

func (w *EthereumScanWorker) Run(ctx context.Context) error {
	backoff := w.minBackoff
	for ctx.Err() == nil {
		now := time.Now().UTC()
		acquired, err := w.leases.AcquireCursorLease(ctx, string(w.network), w.owner, now, now.Add(w.leaseDuration))
		if err != nil || !acquired {
			if !waitTRONWorker(ctx, backoff) {
				break
			}
			backoff = nextTRONBackoff(backoff, w.maxBackoff)
			continue
		}
		backoff = w.minBackoff
		err = w.runLease(ctx)
		if errors.Is(err, ErrEthereumScanHashConflict) {
			return err
		}
		if ctx.Err() != nil {
			break
		}
		if !waitTRONWorker(ctx, backoff) {
			break
		}
		backoff = nextTRONBackoff(backoff, w.maxBackoff)
	}
	return nil
}

func (w *EthereumScanWorker) runLease(parent context.Context) error {
	leaseCtx, cancel := context.WithCancelCause(parent)
	renewDone := make(chan struct{})
	go w.renewLease(leaseCtx, cancel, renewDone)
	defer func() {
		cancel(nil)
		<-renewDone
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), min(5*time.Second, w.renewInterval))
		defer releaseCancel()
		_, _ = w.leases.ReleaseCursorLease(releaseCtx, string(w.network), w.owner)
	}()

	backoff := w.minBackoff
	for leaseCtx.Err() == nil {
		result, err := w.scanner.ScanBatch(leaseCtx, w.owner)
		if err != nil {
			if errors.Is(err, ErrEthereumScanHashConflict) || errors.Is(err, ErrEthereumScanLeaseLost) {
				return err
			}
			if !waitTRONWorker(leaseCtx, backoff) {
				break
			}
			backoff = nextTRONBackoff(backoff, w.maxBackoff)
			continue
		}
		backoff = w.minBackoff
		if result.BlocksScanned == 0 && !waitTRONWorker(leaseCtx, w.pollInterval) {
			break
		}
	}
	return context.Cause(leaseCtx)
}

func (w *EthereumScanWorker) renewLease(ctx context.Context, cancel context.CancelCauseFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(w.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			now = now.UTC()
			renewed, err := w.leases.RenewCursorLease(ctx, string(w.network), w.owner, now, now.Add(w.leaseDuration))
			if err != nil || !renewed {
				if err == nil {
					err = ErrEthereumScanLeaseLost
				}
				cancel(fmt.Errorf("renew Ethereum scan lease: %w", err))
				return
			}
		}
	}
}
