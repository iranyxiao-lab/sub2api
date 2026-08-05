package onchain

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const EthereumRechargeUnavailable = "ETHEREUM_RECHARGE_TEMPORARILY_UNAVAILABLE"

type EthereumOrderGateError struct {
	Code  string
	Cause error
}

func (e *EthereumOrderGateError) Error() string {
	if e.Cause == nil {
		return "Ethereum recharge is temporarily unavailable"
	}
	return fmt.Sprintf("Ethereum recharge is temporarily unavailable: %v", e.Cause)
}

func (e *EthereumOrderGateError) Unwrap() error { return e.Cause }

type EthereumHealthSnapshot struct {
	Checked bool
	Report  EthereumStartupReport
	Err     error
}

type EthereumHealthGate struct {
	mu        sync.RWMutex
	checked   bool
	report    EthereumStartupReport
	healthErr error
}

func NewEthereumHealthGate() *EthereumHealthGate { return &EthereumHealthGate{} }

func (g *EthereumHealthGate) Refresh(ctx context.Context, primary, backup EthereumStartupSource, options EthereumStartupOptions) (EthereumStartupReport, error) {
	report, err := ValidateEthereumStartup(ctx, primary, backup, options)
	g.Record(report, err)
	return report, err
}

func (g *EthereumHealthGate) Record(report EthereumStartupReport, err error) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.checked = true
	g.report = report
	g.healthErr = err
	g.mu.Unlock()
}

func (g *EthereumHealthGate) Snapshot() EthereumHealthSnapshot {
	if g == nil {
		return EthereumHealthSnapshot{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return EthereumHealthSnapshot{Checked: g.checked, Report: g.report, Err: g.healthErr}
}

func (g *EthereumHealthGate) NewOrdersAllowed() bool {
	snapshot := g.Snapshot()
	return snapshot.Checked && snapshot.Err == nil
}

func (g *EthereumHealthGate) RequireNewOrder() error {
	snapshot := g.Snapshot()
	if snapshot.Checked && snapshot.Err == nil {
		return nil
	}
	cause := snapshot.Err
	if cause == nil {
		cause = errors.New("Ethereum health has not been checked")
	}
	return &EthereumOrderGateError{Code: EthereumRechargeUnavailable, Cause: cause}
}

func (g *EthereumHealthGate) ExistingAddressProcessingAllowed() bool {
	return !errors.Is(g.Snapshot().Err, ErrEthereumFinalizedDivergence)
}
