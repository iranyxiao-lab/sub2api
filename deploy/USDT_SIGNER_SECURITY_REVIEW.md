# USDT Signer Security Review

Status: technically approved with mandatory production activation gates.

This review defines the security boundary for the independently deployed
`usdt-signer`. It does not authorize a production rollout by itself. Production
activation still requires named operations and security approvers to attest the
deployment evidence listed below.

## Trust boundary

- The signer runs as a standalone process or container on an isolated host. The
  business server cannot load signer packages, seed material, extended private
  keys, or child private keys.
- The listener binds only to a loopback or private literal IP. Host firewall
  rules allow connections only from the sweep worker network segment.
- Every request uses mutual TLS. The server authorizes an explicit SPIFFE URI
  allowlist for sweep workers; a valid certificate without an authorized URI is
  rejected.
- The signer exposes only versioned `SweepTRC20`, `FundERC20Gas`, and
  `SweepERC20` operations. It has no raw-signing, arbitrary calldata,
  destination, contract, refund, user, public, or general administration API.

## Key custody decision

Production uses three independent key carriers:

| Role | Material | Allowed operation |
| --- | --- | --- |
| TRON recharge wallet | TRON HD seed or account extended private key | `SweepTRC20` |
| Ethereum recharge wallet | Ethereum HD seed or account extended private key | `SweepERC20` |
| Ethereum gas sponsor | One Ethereum private key | `FundERC20Gas` |

Each carrier is encrypted with AES-256-GCM using a distinct 256-bit data key.
Each data key is sealed independently by TPM 2.0 and bound to the approved PCR
policy. Envelope metadata authenticates the role, network, key ID, creation
time, and expected public wallet fingerprint. Startup fails if a carrier is
missing, duplicated across roles, damaged, assigned to another role/network, or
does not reproduce its configured fingerprint.

The production process has no plaintext-key environment variable or config
fallback. TPM or integrity failure keeps all spend operations unavailable.
Development mock and one-time seed modes are separate and are rejected when the
environment is `production`.

Offline recovery material is encrypted and held under two-person control. A
recovery record must contain two distinct authorized operator identities, the
change or incident reference, recovered carrier role, wallet fingerprint,
timestamp, and isolated-host attestation. Recovery material and plaintext keys
must never enter the business database, application logs, or ticket body.

## Policy limits

The production baseline is fail-closed and may only be lowered without a new
security review:

| Operation | Single-operation limit | UTC-day limit | Concurrent limit |
| --- | ---: | ---: | ---: |
| TRC20 sweep | 100,000 USDT | 500,000 USDT | 4 |
| ERC20 sweep | 100,000 USDT | 500,000 USDT | 4 |
| ERC20 gas funding | 0.006 ETH | 1 ETH | 1 |

The signer also fixes chain ID, token contract, derivation path, and collection
wallet per operation. An increase to a limit, a wallet fingerprint change, a
contract change, or a client identity change requires two distinct approvers
from operations and security, a versioned configuration change, and a signer
restart. Emergency suspension may be performed by either function and does not
require raising a limit.

These are outgoing hot-wallet protection limits, not user recharge limits.
Exhausting a signer limit pauses new gas funding and sweeps only. It must not
stop address scanning, deposit confirmation, balance crediting, or creation of
otherwise valid USDT recharge orders. User daily recharge amount and count
remain unlimited; the existing per-order range and pending-order safeguards
remain in force.

## Manual approval boundary

The automatic signer cannot transfer from the collection wallet to cold
storage. At 50,000 USDT the collection wallet raises an alert. At 100,000 USDT
it pauses new automatic collection into that wallet and creates a cold-transfer
approval request. Cold transfers and user refunds use an independent controlled
wallet and signing process with two-person approval.

P0 events include a wallet fingerprint mismatch, key-role crossover, policy
bypass attempt, destination or contract mismatch, and finalized/solidified hash
conflict. They stop the affected automatic operation immediately and require a
15-minute response. Unexplained nonce or balance state is P1. Ordinary failed
sweeps and late-payment review are P2.

## Production activation evidence

Named operations and security approvers must verify all of the following before
enabling spend operations:

- isolated host, private listener, firewall rule, and no public route;
- server and client certificate chains, authorized SPIFFE URIs, and rotation;
- three distinct encrypted carriers, TPM sealed-key IDs, PCR policy, and wallet
  fingerprint matches;
- a witnessed two-person offline recovery drill on an isolated host;
- policy rejection tests for wrong role, network, contract, destination,
  amount, daily total, concurrency, replay, raw transaction, and plaintext
  fallback;
- evidence that a paused signer does not interrupt scanning or user crediting;
- alerts for unseal failure, policy denial, repeated failure, signer health,
  resource shortage, and collection-wallet thresholds.

