package onchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

type TRONSweepCandidate struct {
	IntentID        int64
	Network         Network
	ChainID         int64
	SourceAddress   string
	DerivationIndex int64
	FundingMarker   int64
	WaitingTask     *TRONSweepTask
}

type TRONSweepCreate struct {
	IntentID           int64
	Network            Network
	ChainID            int64
	SourceAddress      string
	DestinationAddress string
	BalanceSnapshotRaw string
	AmountRaw          string
	IdempotencyKey     string
	Status             TransferStatus
}

type TRONSweepTask struct {
	ID             int64
	IdempotencyKey string
	AmountRaw      string
	Status         TransferStatus
	Version        int
}

type TRONSweepStore interface {
	ListTRONSweepCandidates(ctx context.Context, network Network, limit int) ([]TRONSweepCandidate, error)
	EnsureTRONSweep(ctx context.Context, input TRONSweepCreate) (TRONSweepTask, bool, error)
	MarkTRONSweepResourcesReady(ctx context.Context, taskID int64, version int) error
}

type TRONSweepBalanceSource interface {
	TRC20Balance(ctx context.Context, contract, address string) (*big.Int, error)
	TRONAccountState(ctx context.Context, address string) (TRONAccountState, error)
}

type TRONSweepPlannerOptions struct {
	Network              Network
	ContractAddress      string
	DestinationAddress   string
	MinimumAmountRaw     string
	RequiredEnergy       int64
	RequiredBandwidth    int64
	MinimumTRXBalanceSun int64
	BatchSize            int
}

type TRONSweepPlanResult struct {
	Candidates   int
	BelowMinimum int
	Prepared     int
	ResourceWait int
	Existing     int
	Failed       int
}

type TRONSweepPlanner struct {
	store              TRONSweepStore
	source             TRONSweepBalanceSource
	network            Network
	contractAddress    string
	destinationAddress string
	minimumAmount      *big.Int
	requiredEnergy     int64
	requiredBandwidth  int64
	minimumTRXSun      int64
	batchSize          int
}

func NewTRONSweepPlanner(store TRONSweepStore, source TRONSweepBalanceSource, options TRONSweepPlannerOptions) (*TRONSweepPlanner, error) {
	if store == nil || source == nil {
		return nil, fmt.Errorf("TRON sweep store and balance source are required")
	}
	if options.Network != NetworkTronMainnet && options.Network != NetworkTronNile {
		return nil, fmt.Errorf("TRON sweep planner does not support network %q", options.Network)
	}
	if err := ValidateAddress(options.Network, options.ContractAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON sweep contract: %w", err)
	}
	if err := ValidateAddress(options.Network, options.DestinationAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON sweep destination: %w", err)
	}
	minimumAmount, ok := new(big.Int).SetString(strings.TrimSpace(options.MinimumAmountRaw), 10)
	if !ok || minimumAmount.Sign() <= 0 || minimumAmount.BitLen() > 256 || minimumAmount.String() != strings.TrimSpace(options.MinimumAmountRaw) {
		return nil, fmt.Errorf("TRON minimum sweep amount must be a canonical positive uint256")
	}
	if options.RequiredEnergy < 0 || options.RequiredBandwidth < 0 || options.MinimumTRXBalanceSun < 0 {
		return nil, fmt.Errorf("TRON sweep resource thresholds must be non-negative")
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, fmt.Errorf("TRON sweep batch size must be between 1 and 1000")
	}
	return &TRONSweepPlanner{
		store: store, source: source, network: options.Network,
		contractAddress:    strings.TrimSpace(options.ContractAddress),
		destinationAddress: strings.TrimSpace(options.DestinationAddress), minimumAmount: minimumAmount,
		requiredEnergy: options.RequiredEnergy, requiredBandwidth: options.RequiredBandwidth,
		minimumTRXSun: options.MinimumTRXBalanceSun, batchSize: options.BatchSize,
	}, nil
}

func (p *TRONSweepPlanner) PlanBatch(ctx context.Context) (TRONSweepPlanResult, error) {
	candidates, err := p.store.ListTRONSweepCandidates(ctx, p.network, p.batchSize)
	if err != nil {
		return TRONSweepPlanResult{}, fmt.Errorf("list TRON sweep candidates: %w", err)
	}
	result := TRONSweepPlanResult{Candidates: len(candidates)}
	var batchErr error
	for _, candidate := range candidates {
		if candidate.Network != p.network || candidate.IntentID <= 0 || candidate.FundingMarker <= 0 || candidate.DerivationIndex < 0 {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("invalid TRON sweep candidate for intent %d", candidate.IntentID))
			continue
		}
		balance, balanceErr := p.source.TRC20Balance(ctx, p.contractAddress, candidate.SourceAddress)
		if balanceErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("query TRC20 balance for intent %d: %w", candidate.IntentID, balanceErr))
			continue
		}
		if balance == nil || balance.Sign() < 0 || balance.BitLen() > 256 {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("invalid TRC20 balance for intent %d", candidate.IntentID))
			continue
		}
		if balance.Cmp(p.minimumAmount) < 0 {
			result.BelowMinimum++
			continue
		}
		account, accountErr := p.source.TRONAccountState(ctx, candidate.SourceAddress)
		if accountErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("query TRON resources for intent %d: %w", candidate.IntentID, accountErr))
			continue
		}
		status := TransferResourceWait
		if p.resourcesReady(account) {
			status = TransferPrepared
		}
		if candidate.WaitingTask != nil {
			waitingAmount, ok := new(big.Int).SetString(candidate.WaitingTask.AmountRaw, 10)
			if !ok || waitingAmount.Sign() <= 0 || waitingAmount.BitLen() > 256 {
				result.Failed++
				batchErr = errors.Join(batchErr, fmt.Errorf("invalid waiting TRON sweep amount for intent %d", candidate.IntentID))
				continue
			}
			if status != TransferPrepared || balance.Cmp(waitingAmount) < 0 {
				result.ResourceWait++
				continue
			}
			if readyErr := p.store.MarkTRONSweepResourcesReady(ctx, candidate.WaitingTask.ID, candidate.WaitingTask.Version); readyErr != nil {
				result.Failed++
				batchErr = errors.Join(batchErr, fmt.Errorf("release TRON resource wait for intent %d: %w", candidate.IntentID, readyErr))
				continue
			}
			result.Prepared++
			continue
		}
		idempotencyKey := tronSweepIdempotencyKey(candidate, balance)
		_, created, createErr := p.store.EnsureTRONSweep(ctx, TRONSweepCreate{
			IntentID: candidate.IntentID, Network: candidate.Network, ChainID: candidate.ChainID,
			SourceAddress: candidate.SourceAddress, DestinationAddress: p.destinationAddress,
			BalanceSnapshotRaw: balance.String(), AmountRaw: balance.String(),
			IdempotencyKey: idempotencyKey, Status: status,
		})
		if createErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("create TRON sweep for intent %d: %w", candidate.IntentID, createErr))
			continue
		}
		if !created {
			result.Existing++
			continue
		}
		if status == TransferPrepared {
			result.Prepared++
		} else {
			result.ResourceWait++
		}
	}
	return result, batchErr
}

func (p *TRONSweepPlanner) resourcesReady(account TRONAccountState) bool {
	delegatedResourcesReady := account.AvailableEnergy() >= p.requiredEnergy &&
		account.AvailableBandwidth() >= p.requiredBandwidth
	if delegatedResourcesReady {
		return true
	}
	return p.minimumTRXSun > 0 && account.TRXBalanceSun >= p.minimumTRXSun
}

func tronSweepIdempotencyKey(candidate TRONSweepCandidate, balance *big.Int) string {
	payload := fmt.Sprintf("v1|%s|%d|%d|%s", candidate.Network, candidate.IntentID, candidate.FundingMarker, balance.String())
	digest := sha256.Sum256([]byte(payload))
	return "tron-sweep-v1:" + hex.EncodeToString(digest[:])
}
