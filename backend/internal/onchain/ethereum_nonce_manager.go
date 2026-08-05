package onchain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrEthereumNonceLeaseUnavailable = errors.New("Ethereum nonce lease is unavailable")
	ErrEthereumNonceConflict         = errors.New("Ethereum nonce state conflicts with the node pending nonce")
)

type EthereumNonceReservation struct {
	StateID       int64
	ChainID       int64
	SenderAddress string
	Nonce         uint64
	Owner         string
	Version       int
	LeaseUntil    time.Time
}

type EthereumNonceReserveInput struct {
	ChainID         int64
	SenderAddress   string
	Owner           string
	ObservedPending uint64
	Now             time.Time
	LeaseUntil      time.Time
}

type EthereumNonceStore interface {
	ReserveEthereumNonce(ctx context.Context, input EthereumNonceReserveInput) (EthereumNonceReservation, bool, error)
	CommitEthereumNonce(ctx context.Context, reservation EthereumNonceReservation, observedPending uint64, reconciledAt time.Time) error
	MarkEthereumNonceConflict(ctx context.Context, reservation EthereumNonceReservation, observedPending uint64, code, message string, reconciledAt time.Time) error
}

type EthereumPendingNonceSource interface {
	PendingNonce(ctx context.Context, senderAddress string) (uint64, error)
}

type EthereumNonceManagerOptions struct {
	ChainID       uint64
	SenderAddress string
	Owner         string
	LeaseDuration time.Duration
}

type EthereumNonceManager struct {
	store         EthereumNonceStore
	source        EthereumPendingNonceSource
	chainID       uint64
	senderAddress string
	owner         string
	leaseDuration time.Duration
	now           func() time.Time
}

func NewEthereumNonceManager(store EthereumNonceStore, source EthereumPendingNonceSource, options EthereumNonceManagerOptions) (*EthereumNonceManager, error) {
	if store == nil || source == nil {
		return nil, fmt.Errorf("Ethereum nonce store and pending nonce source are required")
	}
	if options.ChainID == 0 || options.ChainID > math.MaxInt64 {
		return nil, fmt.Errorf("Ethereum nonce chain ID is invalid")
	}
	sender := common.HexToAddress(strings.TrimSpace(options.SenderAddress))
	if !common.IsHexAddress(options.SenderAddress) || sender == (common.Address{}) {
		return nil, fmt.Errorf("Ethereum nonce sender address is invalid")
	}
	owner := strings.TrimSpace(options.Owner)
	if owner == "" || len(owner) > 128 {
		return nil, fmt.Errorf("Ethereum nonce lease owner is invalid")
	}
	if options.LeaseDuration < time.Second || options.LeaseDuration > 5*time.Minute {
		return nil, fmt.Errorf("Ethereum nonce lease duration must be between one second and five minutes")
	}
	return &EthereumNonceManager{
		store: store, source: source, chainID: options.ChainID,
		senderAddress: sender.Hex(), owner: owner, leaseDuration: options.LeaseDuration,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (m *EthereumNonceManager) Reserve(ctx context.Context) (EthereumNonceReservation, error) {
	observed, err := m.source.PendingNonce(ctx, m.senderAddress)
	if err != nil {
		return EthereumNonceReservation{}, fmt.Errorf("query Ethereum pending nonce before reservation: %w", err)
	}
	if observed > math.MaxInt64 {
		return EthereumNonceReservation{}, fmt.Errorf("Ethereum pending nonce exceeds persistent range")
	}
	now := m.now()
	reservation, acquired, err := m.store.ReserveEthereumNonce(ctx, EthereumNonceReserveInput{
		ChainID: int64(m.chainID), SenderAddress: m.senderAddress, Owner: m.owner,
		ObservedPending: observed, Now: now, LeaseUntil: now.Add(m.leaseDuration),
	})
	if err != nil {
		return EthereumNonceReservation{}, err
	}
	if !acquired {
		return EthereumNonceReservation{}, ErrEthereumNonceLeaseUnavailable
	}
	return reservation, nil
}

func (m *EthereumNonceManager) Commit(ctx context.Context, reservation EthereumNonceReservation) error {
	if err := m.validateReservation(reservation); err != nil {
		return err
	}
	observed, err := m.source.PendingNonce(ctx, m.senderAddress)
	if err != nil {
		return fmt.Errorf("query Ethereum pending nonce after broadcast: %w", err)
	}
	expected := reservation.Nonce + 1
	if reservation.Nonce == math.MaxUint64 || observed != expected {
		message := fmt.Sprintf("node pending nonce is %d after reserving %d; expected %d", observed, reservation.Nonce, expected)
		if markErr := m.store.MarkEthereumNonceConflict(ctx, reservation, observed, "PENDING_NONCE_CONFLICT", message, m.now()); markErr != nil {
			return errors.Join(ErrEthereumNonceConflict, markErr)
		}
		return fmt.Errorf("%w: %s", ErrEthereumNonceConflict, message)
	}
	if err := m.store.CommitEthereumNonce(ctx, reservation, observed, m.now()); err != nil {
		return fmt.Errorf("commit Ethereum nonce reservation: %w", err)
	}
	return nil
}

func (m *EthereumNonceManager) validateReservation(reservation EthereumNonceReservation) error {
	if reservation.StateID <= 0 || reservation.ChainID != int64(m.chainID) ||
		!strings.EqualFold(reservation.SenderAddress, m.senderAddress) || reservation.Owner != m.owner || reservation.Version < 0 {
		return fmt.Errorf("Ethereum nonce reservation identity is invalid")
	}
	return nil
}
