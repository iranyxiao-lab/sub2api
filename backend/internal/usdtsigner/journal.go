package usdtsigner

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	journalVersion      = 1
	maxJournalEntrySize = 1 << 20
)

var ErrIdempotencyReplay = errors.New("signer idempotency key replayed with different request content")

type OperationStatus string

const (
	OperationStatusReceived     OperationStatus = "received"
	OperationStatusBroadcast    OperationStatus = "broadcast"
	OperationStatusPolicyDenied OperationStatus = "policy_denied"
	OperationStatusFailed       OperationStatus = "failed"
)

type OperationRecord struct {
	Version           int                          `json:"version"`
	AuditID           string                       `json:"audit_id"`
	Operation         OperationKind                `json:"operation"`
	TaskID            string                       `json:"task_id"`
	DerivationIndex   uint32                       `json:"derivation_index"`
	RawAmount         string                       `json:"raw_amount"`
	IdempotencyKey    string                       `json:"idempotency_key"`
	RequestDigest     string                       `json:"request_digest"`
	Status            OperationStatus              `json:"status"`
	TransactionID     string                       `json:"transaction_id,omitempty"`
	EthereumVersions  []EthereumTransactionVersion `json:"ethereum_versions,omitempty"`
	FailureCode       string                       `json:"failure_code,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
	PreviousEntryHash string                       `json:"previous_entry_hash,omitempty"`
	EntryHash         string                       `json:"entry_hash"`
}

type EthereumTransactionVersion struct {
	TransactionID   string    `json:"transaction_id"`
	SemanticDigest  string    `json:"semantic_digest"`
	Nonce           uint64    `json:"nonce"`
	GasLimit        uint64    `json:"gas_limit"`
	MaxFeePerGasWei string    `json:"max_fee_per_gas_wei"`
	PriorityFeeWei  string    `json:"max_priority_fee_per_gas_wei"`
	ReplacementOf   string    `json:"replacement_of,omitempty"`
	BroadcastAt     time.Time `json:"broadcast_at"`
}

type OperationInput struct {
	Operation       OperationKind
	TaskID          string
	DerivationIndex uint32
	RawAmount       string
	IdempotencyKey  string
	RequestDigest   string
	Now             time.Time
}

type OperationJournal struct {
	mu       sync.Mutex
	file     *os.File
	records  map[string]OperationRecord
	lastHash string
}

func OpenOperationJournal(path string) (*OperationJournal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("signer operation journal path is required")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		file.Close()
		return nil, fmt.Errorf("signer operation journal must be a private regular file")
	}
	journal := &OperationJournal{file: file, records: make(map[string]OperationRecord)}
	if err := journal.load(); err != nil {
		file.Close()
		return nil, err
	}
	return journal, nil
}

func (j *OperationJournal) Begin(input OperationInput) (OperationRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if existing, ok := j.records[input.IdempotencyKey]; ok {
		if existing.RequestDigest != input.RequestDigest {
			return OperationRecord{}, false, ErrIdempotencyReplay
		}
		return existing, true, nil
	}
	auditBytes := make([]byte, 16)
	if _, err := rand.Read(auditBytes); err != nil {
		return OperationRecord{}, false, err
	}
	record := OperationRecord{
		Version: journalVersion, AuditID: hex.EncodeToString(auditBytes), Operation: input.Operation,
		TaskID: input.TaskID, DerivationIndex: input.DerivationIndex, RawAmount: input.RawAmount,
		IdempotencyKey: input.IdempotencyKey, RequestDigest: input.RequestDigest,
		Status: OperationStatusReceived, CreatedAt: input.Now.UTC(), UpdatedAt: input.Now.UTC(),
	}
	if err := j.append(&record); err != nil {
		return OperationRecord{}, false, err
	}
	return record, false, nil
}

func (j *OperationJournal) Finish(idempotencyKey string, status OperationStatus, transactionID, failureCode string, now time.Time) (OperationRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.records[idempotencyKey]
	if !ok {
		return OperationRecord{}, fmt.Errorf("signer operation journal record not found")
	}
	if status != OperationStatusBroadcast && status != OperationStatusPolicyDenied && status != OperationStatusFailed {
		return OperationRecord{}, fmt.Errorf("invalid signer terminal operation status %q", status)
	}
	record.Status = status
	record.TransactionID = strings.TrimSpace(transactionID)
	record.FailureCode = strings.TrimSpace(failureCode)
	record.UpdatedAt = now.UTC()
	if err := j.append(&record); err != nil {
		return OperationRecord{}, err
	}
	return record, nil
}

func (j *OperationJournal) RecordEthereumBroadcast(idempotencyKey string, version EthereumTransactionVersion, now time.Time) (OperationRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.records[idempotencyKey]
	if !ok {
		return OperationRecord{}, fmt.Errorf("signer operation journal record not found")
	}
	if record.Operation != OperationFundERC20Gas && record.Operation != OperationSweepERC20 {
		return OperationRecord{}, fmt.Errorf("signer operation is not Ethereum")
	}
	if err := validateEthereumTransactionVersion(record, version); err != nil {
		return OperationRecord{}, err
	}
	record.Status = OperationStatusBroadcast
	record.TransactionID = version.TransactionID
	record.EthereumVersions = append(append([]EthereumTransactionVersion(nil), record.EthereumVersions...), version)
	record.FailureCode = ""
	record.UpdatedAt = now.UTC()
	if err := j.append(&record); err != nil {
		return OperationRecord{}, err
	}
	return record, nil
}

func validateEthereumTransactionVersion(record OperationRecord, version EthereumTransactionVersion) error {
	transactionID := strings.TrimSpace(version.TransactionID)
	semanticDigest := strings.TrimSpace(version.SemanticDigest)
	_, transactionHashErr := hex.DecodeString(strings.TrimPrefix(transactionID, "0x"))
	_, semanticHashErr := hex.DecodeString(strings.TrimPrefix(semanticDigest, "0x"))
	if !strings.HasPrefix(transactionID, "0x") || len(transactionID) != 66 || transactionHashErr != nil ||
		!strings.HasPrefix(semanticDigest, "0x") || len(semanticDigest) != 66 || semanticHashErr != nil || version.GasLimit < 21_000 || version.BroadcastAt.IsZero() {
		return fmt.Errorf("invalid signer Ethereum transaction version")
	}
	fee, feeOK := new(big.Int).SetString(version.MaxFeePerGasWei, 10)
	tip, tipOK := new(big.Int).SetString(version.PriorityFeeWei, 10)
	if !feeOK || !tipOK || fee.Sign() <= 0 || tip.Sign() <= 0 || tip.Cmp(fee) > 0 {
		return fmt.Errorf("invalid signer Ethereum transaction fees")
	}
	if len(record.EthereumVersions) == 0 {
		if strings.TrimSpace(version.ReplacementOf) != "" {
			return fmt.Errorf("initial Ethereum transaction cannot replace another version")
		}
		return nil
	}
	previous := record.EthereumVersions[len(record.EthereumVersions)-1]
	if !strings.EqualFold(strings.TrimSpace(version.ReplacementOf), previous.TransactionID) ||
		version.SemanticDigest != previous.SemanticDigest || version.Nonce != previous.Nonce || version.GasLimit != previous.GasLimit ||
		fee.Cmp(mustJournalInteger(previous.MaxFeePerGasWei)) <= 0 || tip.Cmp(mustJournalInteger(previous.PriorityFeeWei)) <= 0 {
		return fmt.Errorf("replacement transaction must preserve semantics and raise both fees")
	}
	return nil
}

func mustJournalInteger(raw string) *big.Int {
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return new(big.Int)
	}
	return value
}

func (j *OperationJournal) DailyAmount(operation OperationKind, day time.Time) (*big.Int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	total := new(big.Int)
	date := day.UTC().Format("2006-01-02")
	for _, record := range j.records {
		if record.Operation != operation || record.Status != OperationStatusBroadcast || record.CreatedAt.UTC().Format("2006-01-02") != date {
			continue
		}
		amount, ok := new(big.Int).SetString(record.RawAmount, 10)
		if !ok {
			return nil, fmt.Errorf("operation journal contains invalid raw amount")
		}
		total.Add(total, amount)
	}
	return total, nil
}

func (j *OperationJournal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	return j.file.Close()
}

func (j *OperationJournal) load() error {
	if _, err := j.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(j.file)
	scanner.Buffer(make([]byte, 64*1024), maxJournalEntrySize)
	previous := ""
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var record OperationRecord
		decoder := json.NewDecoder(strings.NewReader(string(line)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return fmt.Errorf("decode signer operation journal: %w", err)
		}
		if record.Version != journalVersion || record.PreviousEntryHash != previous {
			return fmt.Errorf("signer operation journal chain is invalid")
		}
		expected, err := operationRecordHash(record)
		if err != nil || !strings.EqualFold(expected, record.EntryHash) {
			return fmt.Errorf("signer operation journal entry authentication failed")
		}
		j.records[record.IdempotencyKey] = record
		previous = record.EntryHash
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	j.lastHash = previous
	_, err := j.file.Seek(0, io.SeekEnd)
	return err
}

func (j *OperationJournal) append(record *OperationRecord) error {
	record.PreviousEntryHash = j.lastHash
	record.EntryHash = ""
	hash, err := operationRecordHash(*record)
	if err != nil {
		return err
	}
	record.EntryHash = hash
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(payload) > maxJournalEntrySize-1 {
		return fmt.Errorf("signer operation journal entry is too large")
	}
	payload = append(payload, '\n')
	if _, err := j.file.Write(payload); err != nil {
		return err
	}
	if err := j.file.Sync(); err != nil {
		return err
	}
	j.lastHash = record.EntryHash
	j.records[record.IdempotencyKey] = *record
	return nil
}

func operationRecordHash(record OperationRecord) (string, error) {
	record.EntryHash = ""
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
