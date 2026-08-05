package usdtsigner

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type ServiceOptions struct {
	Keys           *custody.KeySet
	KeyConfig      custody.KeySetConfig
	TRONConfig     TRONOperationConfig
	EthereumConfig EthereumOperationConfig
	TRONNode       TRONNode
	EthereumNode   EthereumNode
	Policy         *PolicyEngine
	Journal        *OperationJournal
	Clock          func() time.Time
}

type Service struct {
	keys           *custody.KeySet
	keyConfig      custody.KeySetConfig
	tronConfig     TRONOperationConfig
	ethereumConfig EthereumOperationConfig
	tronNode       TRONNode
	ethereumNode   EthereumNode
	policy         *PolicyEngine
	journal        *OperationJournal
	clock          func() time.Time
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Keys == nil || options.TRONNode == nil || options.EthereumNode == nil || options.Policy == nil || options.Journal == nil {
		return nil, fmt.Errorf("signer service requires custody, fixed nodes, policy, and persistent journal")
	}
	if err := options.KeyConfig.Validate(); err != nil {
		return nil, err
	}
	if err := options.TRONConfig.Validate(); err != nil {
		return nil, err
	}
	if err := options.EthereumConfig.Validate(); err != nil {
		return nil, err
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &Service{
		keys: options.Keys, keyConfig: options.KeyConfig, tronConfig: options.TRONConfig,
		ethereumConfig: options.EthereumConfig, tronNode: options.TRONNode, ethereumNode: options.EthereumNode,
		policy: options.Policy, journal: options.Journal, clock: options.Clock,
	}, nil
}

func (s *Service) SweepTRC20(ctx context.Context, request signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return s.execute(ctx, OperationSweepTRC20, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey,
		func(amount *big.Int) (string, error) {
			material, err := s.keys.Material(custody.KeyRoleTRONRecharge)
			if err != nil {
				return "", err
			}
			defer zeroBytes(material)
			privateKey, err := custody.DeriveRechargePrivateKey(
				custody.KeyRoleTRONRecharge, s.keyConfig.TRONRecharge.MaterialType, material, request.DerivationIndex,
			)
			if err != nil {
				return "", err
			}
			defer zeroPrivateKey(privateKey)
			return ExecuteTRC20Sweep(ctx, s.tronConfig, s.tronNode, privateKey, amount)
		})
}

func (s *Service) FundERC20Gas(ctx context.Context, request signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return s.executeEthereum(ctx, OperationFundERC20Gas, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey,
		func(amount *big.Int, replacement *EthereumTransactionVersion) (EthereumBroadcast, error) {
			rechargeMaterial, err := s.keys.Material(custody.KeyRoleEthereumRecharge)
			if err != nil {
				return EthereumBroadcast{}, err
			}
			defer zeroBytes(rechargeMaterial)
			targetKey, err := custody.DeriveRechargePrivateKey(
				custody.KeyRoleEthereumRecharge, s.keyConfig.EthereumRecharge.MaterialType,
				rechargeMaterial, request.DerivationIndex,
			)
			if err != nil {
				return EthereumBroadcast{}, err
			}
			target := crypto.PubkeyToAddress(targetKey.PublicKey)
			zeroPrivateKey(targetKey)

			gasMaterial, err := s.keys.Material(custody.KeyRoleEthereumGas)
			if err != nil {
				return EthereumBroadcast{}, err
			}
			defer zeroBytes(gasMaterial)
			sponsorKey, err := crypto.ToECDSA(gasMaterial)
			if err != nil {
				return EthereumBroadcast{}, fmt.Errorf("load Ethereum gas sponsor private key: %w", err)
			}
			defer zeroPrivateKey(sponsorKey)
			return executeERC20GasFunding(ctx, s.ethereumConfig, s.ethereumNode, sponsorKey, target, amount, replacement)
		})
}

func (s *Service) SweepERC20(ctx context.Context, request signerv1.SweepERC20Request) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return s.executeEthereum(ctx, OperationSweepERC20, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey,
		func(amount *big.Int, replacement *EthereumTransactionVersion) (EthereumBroadcast, error) {
			material, err := s.keys.Material(custody.KeyRoleEthereumRecharge)
			if err != nil {
				return EthereumBroadcast{}, err
			}
			defer zeroBytes(material)
			privateKey, err := custody.DeriveRechargePrivateKey(
				custody.KeyRoleEthereumRecharge, s.keyConfig.EthereumRecharge.MaterialType,
				material, request.DerivationIndex,
			)
			if err != nil {
				return EthereumBroadcast{}, err
			}
			defer zeroPrivateKey(privateKey)
			return executeERC20Sweep(ctx, s.ethereumConfig, s.ethereumNode, privateKey, amount, replacement)
		})
}

func (s *Service) executeEthereum(ctx context.Context, operation OperationKind, taskID string, derivationIndex uint32, rawAmount, idempotencyKey string, executor func(*big.Int, *EthereumTransactionVersion) (EthereumBroadcast, error)) (*signerv1.OperationResponse, error) {
	now := s.clock().UTC()
	digest, err := signerRequestDigest(operation, taskID, derivationIndex, rawAmount, idempotencyKey)
	if err != nil {
		return nil, err
	}
	record, replay, err := s.journal.Begin(OperationInput{Operation: operation, TaskID: taskID, DerivationIndex: derivationIndex, RawAmount: rawAmount, IdempotencyKey: idempotencyKey, RequestDigest: digest, Now: now})
	if err != nil {
		if errors.Is(err, ErrIdempotencyReplay) {
			return nil, fmt.Errorf("%w: idempotency key content mismatch", ErrOperationUnavailable)
		}
		return nil, err
	}
	amount, ok := new(big.Int).SetString(rawAmount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid signer raw amount")
	}
	var previous *EthereumTransactionVersion
	var permit *PolicyPermit
	if replay {
		if record.Status != OperationStatusBroadcast || len(record.EthereumVersions) == 0 {
			return nil, fmt.Errorf("%w: existing operation status is %s", ErrOperationUnavailable, record.Status)
		}
		last := record.EthereumVersions[len(record.EthereumVersions)-1]
		if now.Sub(last.BroadcastAt) < s.ethereumConfig.PendingThreshold {
			return operationResponse(record), nil
		}
		if len(record.EthereumVersions) >= s.ethereumConfig.MaxReplacementAttempts+1 {
			return nil, fmt.Errorf("%w: Ethereum replacement attempt limit reached", ErrOperationUnavailable)
		}
		receipt, receiptErr := s.ethereumNode.TransactionReceipt(ctx, common.HexToHash(last.TransactionID))
		if receiptErr == nil && receipt != nil {
			return operationResponse(record), nil
		}
		if receiptErr != nil && !errors.Is(receiptErr, ethereum.NotFound) {
			return nil, fmt.Errorf("query signer Ethereum receipt before replacement: %w", receiptErr)
		}
		transaction, pending, transactionErr := s.ethereumNode.TransactionByHash(ctx, common.HexToHash(last.TransactionID))
		if transactionErr != nil && !errors.Is(transactionErr, ethereum.NotFound) {
			return nil, fmt.Errorf("query signer Ethereum transaction before replacement: %w", transactionErr)
		}
		if transactionErr == nil && (transaction == nil || !pending) {
			return operationResponse(record), nil
		}
		previous = &last
	} else {
		var authorizeErr error
		permit, authorizeErr = s.policy.Authorize(operation, amount, now)
		if authorizeErr != nil {
			_, finishErr := s.journal.Finish(idempotencyKey, OperationStatusPolicyDenied, "", "POLICY_DENIED", s.clock())
			if finishErr != nil {
				return nil, finishErr
			}
			return nil, fmt.Errorf("%w: %v", ErrOperationUnavailable, authorizeErr)
		}
	}
	broadcast, executeErr := executor(amount, previous)
	if executeErr != nil {
		if permit != nil {
			permit.Release()
			_, _ = s.journal.Finish(idempotencyKey, OperationStatusFailed, "", "EXECUTION_FAILED", s.clock())
		}
		return nil, executeErr
	}
	version := EthereumTransactionVersion{TransactionID: broadcast.TransactionID, SemanticDigest: broadcast.SemanticDigest, Nonce: broadcast.Nonce, GasLimit: broadcast.GasLimit, MaxFeePerGasWei: broadcast.FeeCap.String(), PriorityFeeWei: broadcast.TipCap.String(), BroadcastAt: s.clock().UTC()}
	if previous != nil {
		version.ReplacementOf = previous.TransactionID
	}
	completed, err := s.journal.RecordEthereumBroadcast(idempotencyKey, version, s.clock())
	if err != nil {
		if permit != nil {
			permit.Release()
		}
		return nil, fmt.Errorf("persist signer Ethereum broadcast result: %w", err)
	}
	if permit != nil {
		permit.Commit()
	}
	return operationResponse(completed), nil
}

func (s *Service) execute(
	ctx context.Context,
	operation OperationKind,
	taskID string,
	derivationIndex uint32,
	rawAmount string,
	idempotencyKey string,
	executor func(*big.Int) (string, error),
) (*signerv1.OperationResponse, error) {
	now := s.clock().UTC()
	digest, err := signerRequestDigest(operation, taskID, derivationIndex, rawAmount, idempotencyKey)
	if err != nil {
		return nil, err
	}
	record, replay, err := s.journal.Begin(OperationInput{
		Operation: operation, TaskID: taskID, DerivationIndex: derivationIndex,
		RawAmount: rawAmount, IdempotencyKey: idempotencyKey, RequestDigest: digest, Now: now,
	})
	if err != nil {
		if errors.Is(err, ErrIdempotencyReplay) {
			return nil, fmt.Errorf("%w: idempotency key content mismatch", ErrOperationUnavailable)
		}
		return nil, err
	}
	if replay {
		if record.Status == OperationStatusBroadcast {
			return operationResponse(record), nil
		}
		return nil, fmt.Errorf("%w: existing operation status is %s", ErrOperationUnavailable, record.Status)
	}
	amount, ok := new(big.Int).SetString(rawAmount, 10)
	if !ok || amount.Sign() <= 0 {
		_, _ = s.journal.Finish(idempotencyKey, OperationStatusFailed, "", "INVALID_AMOUNT", s.clock())
		return nil, fmt.Errorf("invalid signer raw amount")
	}
	permit, err := s.policy.Authorize(operation, amount, now)
	if err != nil {
		_, finishErr := s.journal.Finish(idempotencyKey, OperationStatusPolicyDenied, "", "POLICY_DENIED", s.clock())
		if finishErr != nil {
			return nil, finishErr
		}
		return nil, fmt.Errorf("%w: %v", ErrOperationUnavailable, err)
	}
	transactionID, executeErr := executor(amount)
	if executeErr != nil {
		permit.Release()
		_, finishErr := s.journal.Finish(idempotencyKey, OperationStatusFailed, "", "EXECUTION_FAILED", s.clock())
		if finishErr != nil {
			return nil, errors.Join(executeErr, finishErr)
		}
		return nil, executeErr
	}
	completed, err := s.journal.Finish(idempotencyKey, OperationStatusBroadcast, transactionID, "", s.clock())
	if err != nil {
		permit.Release()
		return nil, fmt.Errorf("persist signer broadcast result: %w", err)
	}
	permit.Commit()
	return operationResponse(completed), nil
}

func signerRequestDigest(operation OperationKind, taskID string, derivationIndex uint32, rawAmount, idempotencyKey string) (string, error) {
	payload, err := json.Marshal(struct {
		Operation       OperationKind `json:"operation"`
		TaskID          string        `json:"task_id"`
		DerivationIndex uint32        `json:"derivation_index"`
		RawAmount       string        `json:"raw_amount"`
		IdempotencyKey  string        `json:"idempotency_key"`
	}{operation, taskID, derivationIndex, rawAmount, idempotencyKey})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func operationResponse(record OperationRecord) *signerv1.OperationResponse {
	response := &signerv1.OperationResponse{
		TaskID: record.TaskID, IdempotencyKey: record.IdempotencyKey,
		Status: string(record.Status), TransactionID: record.TransactionID, AuditID: record.AuditID,
	}
	if len(record.EthereumVersions) > 0 {
		latest := record.EthereumVersions[len(record.EthereumVersions)-1]
		response.ReplacementOfTransactionID = latest.ReplacementOf
		response.TransactionVersion = len(record.EthereumVersions)
		response.Nonce = latest.Nonce
		response.GasLimit = latest.GasLimit
		response.MaxFeePerGasWei = latest.MaxFeePerGasWei
		response.MaxPriorityFeePerGasWei = latest.PriorityFeeWei
	}
	return response
}

func zeroPrivateKey(privateKey *ecdsa.PrivateKey) {
	if privateKey == nil || privateKey.D == nil {
		return
	}
	privateKey.D.SetInt64(0)
	privateKey.PublicKey.X = new(big.Int)
	privateKey.PublicKey.Y = new(big.Int)
}

var _ TRC20Sweeper = (*Service)(nil)
var _ ERC20GasFunder = (*Service)(nil)
var _ ERC20Sweeper = (*Service)(nil)
