package custody

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
)

const (
	SealedKeyVersion = 1
	DataKeySize      = 32
)

type SealedKey struct {
	Version     int       `json:"version"`
	KeyID       string    `json:"key_id"`
	PCRs        []int     `json:"pcrs"`
	PCRDigest   []byte    `json:"pcr_digest"`
	PublicBlob  []byte    `json:"public_blob"`
	PrivateBlob []byte    `json:"private_blob"`
	CreatedAt   time.Time `json:"created_at"`
}

func SealNewDataKey(t transport.TPM, pcrs []int, now time.Time) ([]byte, *SealedKey, error) {
	dataKey := make([]byte, DataKeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, nil, fmt.Errorf("generate data key: %w", err)
	}
	sealed, err := SealDataKey(t, pcrs, dataKey, now)
	if err != nil {
		zero(dataKey)
		return nil, nil, err
	}
	return dataKey, sealed, nil
}

func SealDataKey(t transport.TPM, pcrs []int, dataKey []byte, now time.Time) (*SealedKey, error) {
	if t == nil {
		return nil, fmt.Errorf("TPM transport is required")
	}
	if len(dataKey) != DataKeySize {
		return nil, fmt.Errorf("TPM sealed data key must be exactly %d bytes", DataKeySize)
	}
	selection, normalizedPCRs, err := pcrSelection(pcrs)
	if err != nil {
		return nil, err
	}
	pcrDigest, err := readPCRDigest(t, selection, len(normalizedPCRs))
	if err != nil {
		return nil, err
	}
	policyDigest, err := calculatePCRPolicyDigest(selection, pcrDigest)
	if err != nil {
		return nil, err
	}

	primary, err := (tpm2.CreatePrimary{
		PrimaryHandle: tpm2.TPMRHOwner,
		InPublic:      tpm2.New2B(tpm2.ECCSRKTemplate),
	}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("create TPM storage root: %w", err)
	}
	defer flush(t, primary.ObjectHandle)

	create, err := (tpm2.Create{
		ParentHandle: tpm2.AuthHandle{
			Handle: primary.ObjectHandle,
			Name:   primary.Name,
			Auth: tpm2.HMAC(tpm2.TPMAlgSHA256, 16,
				tpm2.AESEncryption(128, tpm2.EncryptIn)),
		},
		InSensitive: tpm2.TPM2BSensitiveCreate{
			Sensitive: &tpm2.TPMSSensitiveCreate{
				Data: tpm2.NewTPMUSensitiveCreate(&tpm2.TPM2BSensitiveData{Buffer: append([]byte(nil), dataKey...)}),
			},
		},
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgKeyedHash,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:        true,
				FixedParent:     true,
				AdminWithPolicy: true,
				NoDA:            true,
			},
			AuthPolicy: tpm2.TPM2BDigest{Buffer: policyDigest},
		}),
	}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("seal data key in TPM: %w", err)
	}

	keyIDBytes := make([]byte, 16)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return nil, fmt.Errorf("generate sealed key ID: %w", err)
	}
	return &SealedKey{
		Version:     SealedKeyVersion,
		KeyID:       hex.EncodeToString(keyIDBytes),
		PCRs:        normalizedPCRs,
		PCRDigest:   append([]byte(nil), pcrDigest...),
		PublicBlob:  append([]byte(nil), create.OutPublic.Bytes()...),
		PrivateBlob: append([]byte(nil), create.OutPrivate.Buffer...),
		CreatedAt:   now.UTC(),
	}, nil
}

func UnsealDataKey(t transport.TPM, sealed SealedKey) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("TPM transport is required")
	}
	if err := sealed.Validate(); err != nil {
		return nil, err
	}
	selection, _, err := pcrSelection(sealed.PCRs)
	if err != nil {
		return nil, err
	}
	currentDigest, err := readPCRDigest(t, selection, len(sealed.PCRs))
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(currentDigest, sealed.PCRDigest) != 1 {
		return nil, fmt.Errorf("current TPM PCR state does not match sealed key policy")
	}

	primary, err := (tpm2.CreatePrimary{
		PrimaryHandle: tpm2.TPMRHOwner,
		InPublic:      tpm2.New2B(tpm2.ECCSRKTemplate),
	}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("create TPM storage root: %w", err)
	}
	defer flush(t, primary.ObjectHandle)

	loaded, err := (tpm2.Load{
		ParentHandle: tpm2.AuthHandle{
			Handle: primary.ObjectHandle,
			Name:   primary.Name,
			Auth:   tpm2.HMAC(tpm2.TPMAlgSHA256, 16),
		},
		InPrivate: tpm2.TPM2BPrivate{Buffer: append([]byte(nil), sealed.PrivateBlob...)},
		InPublic:  tpm2.BytesAs2B[tpm2.TPMTPublic, *tpm2.TPMTPublic](append([]byte(nil), sealed.PublicBlob...)),
	}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("load TPM sealed key: %w", err)
	}
	defer flush(t, loaded.ObjectHandle)

	policy, cleanup, err := tpm2.PolicySession(t, tpm2.TPMAlgSHA256, 16,
		tpm2.AESEncryption(128, tpm2.EncryptOut))
	if err != nil {
		return nil, fmt.Errorf("start TPM policy session: %w", err)
	}
	defer cleanup()
	if _, err := (tpm2.PolicyPCR{
		PolicySession: policy.Handle(),
		PcrDigest:     tpm2.TPM2BDigest{Buffer: append([]byte(nil), sealed.PCRDigest...)},
		Pcrs:          selection,
	}).Execute(t); err != nil {
		return nil, fmt.Errorf("authorize TPM PCR policy: %w", err)
	}

	unsealed, err := (tpm2.Unseal{
		ItemHandle: tpm2.AuthHandle{
			Handle: loaded.ObjectHandle,
			Name:   loaded.Name,
			Auth:   policy,
		},
	}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("unseal TPM data key: %w", err)
	}
	if len(unsealed.OutData.Buffer) != DataKeySize {
		zero(unsealed.OutData.Buffer)
		return nil, fmt.Errorf("TPM returned invalid data key length")
	}
	return append([]byte(nil), unsealed.OutData.Buffer...), nil
}

func (s SealedKey) Validate() error {
	if s.Version != SealedKeyVersion {
		return fmt.Errorf("unsupported TPM sealed key version %d", s.Version)
	}
	if len(s.KeyID) != 32 {
		return fmt.Errorf("TPM sealed key ID is invalid")
	}
	if _, err := hex.DecodeString(s.KeyID); err != nil {
		return fmt.Errorf("TPM sealed key ID is invalid")
	}
	if _, _, err := pcrSelection(s.PCRs); err != nil {
		return err
	}
	if len(s.PCRDigest) != sha256.Size {
		return fmt.Errorf("TPM PCR digest must be SHA-256")
	}
	if len(s.PublicBlob) == 0 || len(s.PrivateBlob) == 0 {
		return fmt.Errorf("TPM sealed key blobs are required")
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("TPM sealed key creation time is required")
	}
	return nil
}

func pcrSelection(pcrs []int) (tpm2.TPMLPCRSelection, []int, error) {
	if len(pcrs) == 0 || len(pcrs) > 24 {
		return tpm2.TPMLPCRSelection{}, nil, fmt.Errorf("TPM PCR policy must select between 1 and 24 PCRs")
	}
	normalized := append([]int(nil), pcrs...)
	sort.Ints(normalized)
	for i, pcr := range normalized {
		if pcr < 0 || pcr > 23 {
			return tpm2.TPMLPCRSelection{}, nil, fmt.Errorf("TPM PCR index %d is outside 0..23", pcr)
		}
		if i > 0 && pcr == normalized[i-1] {
			return tpm2.TPMLPCRSelection{}, nil, fmt.Errorf("TPM PCR index %d is duplicated", pcr)
		}
	}
	selected := make([]uint, len(normalized))
	for i, pcr := range normalized {
		selected[i] = uint(pcr)
	}
	return tpm2.TPMLPCRSelection{PCRSelections: []tpm2.TPMSPCRSelection{{
		Hash:      tpm2.TPMAlgSHA256,
		PCRSelect: tpm2.PCClientCompatible.PCRs(selected...),
	}}}, normalized, nil
}

func readPCRDigest(t transport.TPM, selection tpm2.TPMLPCRSelection, expectedCount int) ([]byte, error) {
	response, err := (tpm2.PCRRead{PCRSelectionIn: selection}).Execute(t)
	if err != nil {
		return nil, fmt.Errorf("read TPM PCRs: %w", err)
	}
	if len(response.PCRValues.Digests) != expectedCount {
		return nil, fmt.Errorf("TPM returned %d PCR values, expected %d", len(response.PCRValues.Digests), expectedCount)
	}
	hash := sha256.New()
	for _, digest := range response.PCRValues.Digests {
		if len(digest.Buffer) != sha256.Size {
			return nil, fmt.Errorf("TPM returned non-SHA-256 PCR value")
		}
		_, _ = hash.Write(digest.Buffer)
	}
	return hash.Sum(nil), nil
}

func calculatePCRPolicyDigest(selection tpm2.TPMLPCRSelection, pcrDigest []byte) ([]byte, error) {
	calculator, err := tpm2.NewPolicyCalculator(tpm2.TPMAlgSHA256)
	if err != nil {
		return nil, fmt.Errorf("create TPM policy calculator: %w", err)
	}
	command := tpm2.PolicyPCR{
		PcrDigest: tpm2.TPM2BDigest{Buffer: append([]byte(nil), pcrDigest...)},
		Pcrs:      selection,
	}
	if err := command.Update(calculator); err != nil {
		return nil, fmt.Errorf("calculate TPM PCR policy: %w", err)
	}
	return append([]byte(nil), calculator.Hash().Digest...), nil
}

func flush(t transport.TPM, handle tpm2.TPMHandle) {
	_, _ = (tpm2.FlushContext{FlushHandle: handle}).Execute(t)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
