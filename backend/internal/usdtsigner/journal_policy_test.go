package usdtsigner

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOperationJournalPersistsIdempotencyDailyAmountAndHashChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	journal, err := OpenOperationJournal(path)
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0)
	input := OperationInput{
		Operation: OperationSweepTRC20, TaskID: "sweep-1", DerivationIndex: 7,
		RawAmount: "50000000", IdempotencyKey: "idem-1", RequestDigest: "sha256:request-1", Now: now,
	}
	record, replay, err := journal.Begin(input)
	require.NoError(t, err)
	require.False(t, replay)
	require.NotEmpty(t, record.AuditID)
	completed, err := journal.Finish(input.IdempotencyKey, OperationStatusBroadcast, "abcd", "", now)
	require.NoError(t, err)
	require.Equal(t, "abcd", completed.TransactionID)
	require.NoError(t, journal.Close())

	journal, err = OpenOperationJournal(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.Close()) })
	replayed, replay, err := journal.Begin(input)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, completed.AuditID, replayed.AuditID)
	total, err := journal.DailyAmount(OperationSweepTRC20, now)
	require.NoError(t, err)
	require.Equal(t, "50000000", total.String())

	input.RequestDigest = "sha256:different"
	_, _, err = journal.Begin(input)
	require.ErrorIs(t, err, ErrIdempotencyReplay)
}

func TestOperationJournalRejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.jsonl")
	journal, err := OpenOperationJournal(path)
	require.NoError(t, err)
	_, _, err = journal.Begin(OperationInput{
		Operation: OperationSweepERC20, TaskID: "sweep-1", RawAmount: "1",
		IdempotencyKey: "idem-1", RequestDigest: "sha256:request", Now: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, journal.Close())
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString("{\"tampered\":true}\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = OpenOperationJournal(path)
	require.Error(t, err)
}

func TestPolicyEngineEnforcesSingleDailyAndConcurrencyLimits(t *testing.T) {
	journal, err := OpenOperationJournal(filepath.Join(t.TempDir(), "operations.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.Close()) })
	engine, err := NewPolicyEngine(PolicyConfig{
		SweepTRC20:   OperationLimit{SingleAmount: big.NewInt(100), DailyAmount: big.NewInt(150), Concurrency: 1},
		FundERC20Gas: OperationLimit{SingleAmount: big.NewInt(10), DailyAmount: big.NewInt(20), Concurrency: 1},
		SweepERC20:   OperationLimit{SingleAmount: big.NewInt(100), DailyAmount: big.NewInt(150), Concurrency: 1},
	}, journal)
	require.NoError(t, err)
	now := time.Now().UTC()
	permit, err := engine.Authorize(OperationSweepTRC20, big.NewInt(100), now)
	require.NoError(t, err)
	_, err = engine.Authorize(OperationSweepTRC20, big.NewInt(1), now)
	require.ErrorContains(t, err, "concurrency")
	permit.Release()
	_, err = engine.Authorize(OperationSweepTRC20, big.NewInt(101), now)
	require.ErrorContains(t, err, "single-operation")

	_, err = engine.Authorize(OperationFundERC20Gas, big.NewInt(11), now)
	require.ErrorContains(t, err, "single-operation")
	gasInput := OperationInput{
		Operation: OperationFundERC20Gas, TaskID: "gas-1", RawAmount: "15",
		IdempotencyKey: "gas-idem-1", RequestDigest: "sha256:gas-1", Now: now,
	}
	_, _, err = journal.Begin(gasInput)
	require.NoError(t, err)
	_, err = journal.Finish(gasInput.IdempotencyKey, OperationStatusBroadcast, "0xgas", "", now)
	require.NoError(t, err)
	_, err = engine.Authorize(OperationFundERC20Gas, big.NewInt(6), now)
	require.ErrorContains(t, err, "UTC-day limit")
}
