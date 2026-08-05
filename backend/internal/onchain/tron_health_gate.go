package onchain

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const TRONRechargeUnavailable = "TRON_RECHARGE_TEMPORARILY_UNAVAILABLE"

type TRONOrderGateError struct {
	Code  string
	Cause error
}

func (e *TRONOrderGateError) Error() string {
	if e.Cause == nil {
		return "TRON recharge is temporarily unavailable"
	}
	return fmt.Sprintf("TRON recharge is temporarily unavailable: %v", e.Cause)
}

func (e *TRONOrderGateError) Unwrap() error { return e.Cause }

type TRONHealthSnapshot struct {
	Checked bool
	Report  TRONHealthReport
	Err     error
}

// TRONHealthGate only gates new order creation. Existing-address scanning and
// recovery must remain schedulable while an unhealthy node is catching up or
// temporarily unavailable.
type TRONHealthGate struct {
	mu        sync.RWMutex
	checked   bool
	report    TRONHealthReport
	healthErr error
}

func NewTRONHealthGate() *TRONHealthGate {
	return &TRONHealthGate{}
}

func (g *TRONHealthGate) Refresh(ctx context.Context, client *JavaTronClient, options TRONHealthCheckOptions) (TRONHealthReport, error) {
	if client == nil {
		err := errors.New("java-tron client is required")
		g.Record(TRONHealthReport{Network: options.Network}, err)
		return TRONHealthReport{Network: options.Network}, err
	}
	report, err := client.CheckTRONHealth(ctx, options)
	g.Record(report, err)
	return report, err
}

func (g *TRONHealthGate) Record(report TRONHealthReport, err error) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.checked = true
	g.report = report
	g.healthErr = err
	g.mu.Unlock()
}

func (g *TRONHealthGate) Snapshot() TRONHealthSnapshot {
	if g == nil {
		return TRONHealthSnapshot{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return TRONHealthSnapshot{Checked: g.checked, Report: g.report, Err: g.healthErr}
}

func (g *TRONHealthGate) NewOrdersAllowed() bool {
	snapshot := g.Snapshot()
	return snapshot.Checked && snapshot.Err == nil && snapshot.Report.Healthy
}

func (g *TRONHealthGate) RequireNewOrder() error {
	snapshot := g.Snapshot()
	if snapshot.Checked && snapshot.Err == nil && snapshot.Report.Healthy {
		return nil
	}
	cause := snapshot.Err
	if cause == nil {
		if !snapshot.Checked {
			cause = errors.New("TRON health has not been checked")
		} else {
			cause = errors.New("latest TRON health report is unhealthy")
		}
	}
	return &TRONOrderGateError{Code: TRONRechargeUnavailable, Cause: cause}
}

func (g *TRONHealthGate) ExistingAddressProcessingAllowed() bool {
	return true
}
