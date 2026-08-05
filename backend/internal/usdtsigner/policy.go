package usdtsigner

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

type OperationKind string

const (
	OperationSweepTRC20   OperationKind = "sweep_trc20"
	OperationFundERC20Gas OperationKind = "fund_erc20_gas"
	OperationSweepERC20   OperationKind = "sweep_erc20"
)

var ErrPolicyPaused = errors.New("signer funds operation is paused by safety policy")

type OperationLimit struct {
	SingleAmount *big.Int
	DailyAmount  *big.Int
	Concurrency  int
}

type PolicyConfig struct {
	SweepTRC20   OperationLimit
	FundERC20Gas OperationLimit
	SweepERC20   OperationLimit
}

func (c PolicyConfig) Validate() error {
	for operation, limit := range map[OperationKind]OperationLimit{
		OperationSweepTRC20: c.SweepTRC20, OperationFundERC20Gas: c.FundERC20Gas, OperationSweepERC20: c.SweepERC20,
	} {
		if limit.SingleAmount == nil || limit.SingleAmount.Sign() <= 0 || limit.DailyAmount == nil || limit.DailyAmount.Sign() <= 0 {
			return fmt.Errorf("signer policy amounts for %s must be positive", operation)
		}
		if limit.DailyAmount.Cmp(limit.SingleAmount) < 0 {
			return fmt.Errorf("signer policy daily amount for %s is below its single amount", operation)
		}
		if limit.Concurrency < 1 || limit.Concurrency > 64 {
			return fmt.Errorf("signer policy concurrency for %s must be between 1 and 64", operation)
		}
	}
	return nil
}

type DailyAmountSource interface {
	DailyAmount(OperationKind, time.Time) (*big.Int, error)
}

type PolicyEngine struct {
	mu       sync.Mutex
	config   PolicyConfig
	source   DailyAmountSource
	inFlight map[OperationKind]int
	reserved map[string]*big.Int
}

type PolicyPermit struct {
	engine    *PolicyEngine
	operation OperationKind
	day       string
	amount    *big.Int
	finished  bool
}

func NewPolicyEngine(config PolicyConfig, source DailyAmountSource) (*PolicyEngine, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("signer policy requires a persistent daily amount source")
	}
	return &PolicyEngine{
		config: config, source: source, inFlight: make(map[OperationKind]int, 3), reserved: make(map[string]*big.Int),
	}, nil
}

func (e *PolicyEngine) Authorize(operation OperationKind, amount *big.Int, now time.Time) (*PolicyPermit, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("signer policy amount must be positive")
	}
	limit, err := e.limit(operation)
	if err != nil {
		return nil, err
	}
	if amount.Cmp(limit.SingleAmount) > 0 {
		return nil, fmt.Errorf("%w: %s single-operation limit exceeded", ErrPolicyPaused, operation)
	}
	day := now.UTC().Format("2006-01-02")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inFlight[operation] >= limit.Concurrency {
		return nil, fmt.Errorf("%w: %s concurrency limit reached", ErrPolicyPaused, operation)
	}
	persisted, err := e.source.DailyAmount(operation, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("load signer daily policy amount: %w", err)
	}
	reservationKey := string(operation) + ":" + day
	reserved := e.reserved[reservationKey]
	if reserved == nil {
		reserved = new(big.Int)
	}
	projected := new(big.Int).Add(persisted, reserved)
	projected.Add(projected, amount)
	if projected.Cmp(limit.DailyAmount) > 0 {
		return nil, fmt.Errorf("%w: %s UTC-day limit exceeded", ErrPolicyPaused, operation)
	}
	e.inFlight[operation]++
	e.reserved[reservationKey] = new(big.Int).Add(reserved, amount)
	return &PolicyPermit{engine: e, operation: operation, day: day, amount: new(big.Int).Set(amount)}, nil
}

func (p *PolicyPermit) Commit() {
	if p == nil || p.finished {
		return
	}
	p.finish()
}

func (p *PolicyPermit) Release() {
	if p == nil || p.finished {
		return
	}
	p.finish()
}

func (p *PolicyPermit) finish() {
	p.engine.mu.Lock()
	defer p.engine.mu.Unlock()
	p.finished = true
	p.engine.inFlight[p.operation]--
	key := string(p.operation) + ":" + p.day
	remaining := new(big.Int).Sub(p.engine.reserved[key], p.amount)
	if remaining.Sign() == 0 {
		delete(p.engine.reserved, key)
	} else {
		p.engine.reserved[key] = remaining
	}
}

func (e *PolicyEngine) limit(operation OperationKind) (OperationLimit, error) {
	switch operation {
	case OperationSweepTRC20:
		return e.config.SweepTRC20, nil
	case OperationFundERC20Gas:
		return e.config.FundERC20Gas, nil
	case OperationSweepERC20:
		return e.config.SweepERC20, nil
	default:
		return OperationLimit{}, fmt.Errorf("unsupported signer operation %q", operation)
	}
}
