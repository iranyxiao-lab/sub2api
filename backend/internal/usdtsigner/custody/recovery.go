package custody

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type RecoveryAuthorization struct {
	IncidentID              string    `json:"incident_id"`
	ApproverIDs             []string  `json:"approver_ids"`
	Role                    KeyRole   `json:"role"`
	WalletFingerprint       string    `json:"wallet_fingerprint"`
	IsolatedHostAttestation string    `json:"isolated_host_attestation"`
	ApprovedAt              time.Time `json:"approved_at"`
}

type RecoveryRecord struct {
	IncidentID              string    `json:"incident_id"`
	ApproverIDs             []string  `json:"approver_ids"`
	Role                    KeyRole   `json:"role"`
	WalletFingerprint       string    `json:"wallet_fingerprint"`
	IsolatedHostAttestation string    `json:"isolated_host_attestation"`
	RecoveredKeyID          string    `json:"recovered_key_id"`
	EncryptedCarrierDigest  string    `json:"encrypted_carrier_digest"`
	RecoveredAt             time.Time `json:"recovered_at"`
}

func RecoverEncryptedKey(dataKey []byte, authorization RecoveryAuthorization, spec EnvelopeSpec, material []byte, now time.Time) (*EncryptedKey, *RecoveryRecord, error) {
	if err := authorization.Validate(now); err != nil {
		return nil, nil, err
	}
	if authorization.Role != spec.Role {
		return nil, nil, fmt.Errorf("recovery authorization role does not match key carrier role")
	}
	envelope, err := EncryptKeyMaterial(dataKey, spec, material, now)
	if err != nil {
		return nil, nil, err
	}
	if !strings.EqualFold(authorization.WalletFingerprint, envelope.WalletFingerprint) {
		return nil, nil, fmt.Errorf("recovery authorization wallet fingerprint does not match recovered material")
	}
	digest := sha256.Sum256(envelope.Ciphertext)
	record := &RecoveryRecord{
		IncidentID: authorization.IncidentID, ApproverIDs: append([]string(nil), authorization.ApproverIDs...),
		Role: authorization.Role, WalletFingerprint: envelope.WalletFingerprint,
		IsolatedHostAttestation: authorization.IsolatedHostAttestation,
		RecoveredKeyID:          envelope.KeyID, EncryptedCarrierDigest: "sha256:" + hex.EncodeToString(digest[:]),
		RecoveredAt: now.UTC(),
	}
	return envelope, record, nil
}

func (a RecoveryAuthorization) Validate(now time.Time) error {
	if strings.TrimSpace(a.IncidentID) == "" {
		return fmt.Errorf("recovery incident or change reference is required")
	}
	if len(a.ApproverIDs) != 2 {
		return fmt.Errorf("offline recovery requires exactly two approvers")
	}
	first := strings.TrimSpace(a.ApproverIDs[0])
	second := strings.TrimSpace(a.ApproverIDs[1])
	if first == "" || second == "" || strings.EqualFold(first, second) {
		return fmt.Errorf("offline recovery requires two distinct approver identities")
	}
	if a.Role != KeyRoleTRONRecharge && a.Role != KeyRoleEthereumRecharge && a.Role != KeyRoleEthereumGas {
		return fmt.Errorf("offline recovery role is invalid")
	}
	if !validFingerprint(a.WalletFingerprint) {
		return fmt.Errorf("offline recovery wallet fingerprint is invalid")
	}
	if strings.TrimSpace(a.IsolatedHostAttestation) == "" {
		return fmt.Errorf("isolated recovery host attestation is required")
	}
	if a.ApprovedAt.IsZero() || a.ApprovedAt.After(now.Add(time.Minute)) || now.Sub(a.ApprovedAt) > 24*time.Hour {
		return fmt.Errorf("offline recovery approval must be current within 24 hours")
	}
	return nil
}
