# Self-hosted USDT signer security review

Status: **approval required**

The implementation must remain disabled in production until Security and Operations record named approvers and evidence for every item below.

## Required evidence

- The `usdt-signer` is built as a separate executable and deployed in a separate process/container on an isolated network segment. The business server has no embedded signer mode or private-key configuration.
- Worker and signer have distinct client/server certificates from the internal CA. Both sides require TLS 1.3 mutual authentication, verify the expected service identity, and reject unauthenticated traffic.
- TRON recharge, Ethereum recharge, and Ethereum gas sponsor keys use three separately authorized TPM 2.0 sealed 256-bit wrapping keys and authenticated encrypted key files. There is no plaintext fallback.
- Wallet fingerprints from offline derivation match the configured business-side xpubs and signer-side encrypted seeds.
- `SweepTRC20`, `FundERC20Gas`, and `SweepERC20` accept only task ID, derivation index, raw amount, and idempotency key. Network, contract, destination, calldata, and raw transaction input are impossible at the protocol boundary.
- Per-operation amount, daily aggregate amount, concurrency, fee, resource, hot-wallet, and replacement-transaction limits are approved. Exhausting these limits pauses funding/sweeping only and cannot stop scanning or user credit settlement.
- `50,000 USDT` hot-wallet warning and `100,000 USDT` cold-transfer approval boundaries are connected to an independent dual-control cold-wallet process.
- Offline disaster recovery requires two named custodians on an isolated host and records actor identities, approval, time, wallet fingerprint, and restored carrier version.
- Firewall evidence shows no public/user/admin access to signer endpoints and no signer access to business user data.

## Approval record

| Role | Name | Evidence reference | Decision | Date |
| --- | --- | --- | --- | --- |
| Security owner |  |  | Pending |  |
| Operations owner |  |  | Pending |  |

Signer production enablement is fail-closed while either decision is pending.
