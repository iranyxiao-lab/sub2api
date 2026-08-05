# USDT-TRC20 operations runbook

This runbook covers incidents in the self-hosted TRON deposit path. It assumes
the deployment and identities in `USDT_TRC20_DEPLOYMENT.md` are already in
place. All timestamps, transaction IDs, log indexes, block heights and hashes
collected during an incident must be retained in the incident record.

## Safety rules

- Never delete or rewrite an on-chain deposit, sweep, balance audit, or scan
  cursor to make an alert disappear.
- Never credit an underpaid or reused address by editing the database. Never
  issue an automatic on-chain refund. Use the approved manual review and
  separately controlled signing process.
- A funds-operation problem must not reverse an already credited user balance.
- Disable new order creation before maintenance that can make node, scanner, or
  contract health uncertain.
- Disabling new sweep work must still allow already broadcast or confirming
  transactions to be tracked to a solidified outcome.

## Operator queries

The endpoints below require an authenticated administrator session. Examples
use a bearer token only as a placeholder; use the deployment's normal admin
authentication mechanism and do not put credentials in an incident ticket.

```sh
export SUB2API_ADMIN_URL="https://sub2api.internal.example"
export SUB2API_ADMIN_TOKEN="REPLACE_IN_SHELL_ONLY"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/health"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/deposits?network=tron-mainnet&transaction_id=TXID&log_index=0"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/reviews?network=tron-mainnet&page=1&page_size=50"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/orders/ORDER_ID"
```

The deposit and review queries also accept `address`, `order_id`, `user_id`,
`status`, and `transaction_id`. Order detail returns `onchain_trace`, including
the intent, deposits, balance audits, sweeps, and gas-funding records.

## Feature-switch matrix

The switches are startup configuration. Change the deployment configuration,
use the normal validated rollout/restart process, and confirm the effective
state after restart. Do not set `onchain.tron.enabled` to `false` during a
routine rollback because all TRON runtimes depend on the network remaining
configured.

| Mode | `enabled` | `order_creation_enabled` | `scanner_enabled` | `settlement_enabled` | `sweeper_enabled` |
| --- | --- | --- | --- | --- | --- |
| Normal | `true` | `true` | `true` | `true` | `true` |
| Safe rollback / signer or resource incident | `true` | `false` | `true` | `true` | `false` |
| Node data or solidified hash conflict | `true` | `false` | `true` | `false` | `false` |

In safe rollback mode, scanning, existing-order settlement, reconciliation,
manual review, and tracking of previously broadcast sweeps remain active. In a
hash conflict, settlement is also stopped until recorded solidified facts are
verified because the chain history itself is in doubt.

## Node failure or lag

Signals include `NODE_UNSYNCED`, `SCAN_STALLED`, `node_healthy=false`, a growing
`node_block_lag`, or a growing `scan_lag_blocks` from the health endpoint.

1. Disable new order creation. If both node paths are unreliable, also disable
   new sweep work; keep scanner and settlement enabled so they resume from
   durable state when the node returns.
2. Capture FullNode height, SolidityNode solidified height, cursor height/hash,
   `last_success_at`, `last_error_code`, and node/proxy logs.
3. Check node disk, peer count, clock, TLS proxy, network identity, configured
   USDT contract, and SolidityNode synchronization. Do not substitute a hosted
   RPC or block explorer as a crediting source.
4. Restore the approved primary or fallback self-hosted endpoint. Do not create
   a fresh database cursor or point an existing mainnet deployment at a
   different network.
5. Require stable node health, monotonically increasing solidified height,
   cursor catch-up, zero unexplained reconciliation differences, and no hash
   conflict before re-enabling sweeps and then order creation.

## Cursor recovery and hash conflict

`HASH_CONFLICT`, `SOLIDIFIED_HASH_CONFLICT`, or cursor health
`HASH_CONFLICT` is P0. Treat a cursor that stopped after a crash but has no hash
conflict as recovery, not as permission to edit it.

1. Immediately disable new orders, settlement, and new sweep work. Leave the
   scanner enabled so its fault-closed state and evidence remain observable.
2. Record the saved cursor height/hash, current SolidityNode hash at that
   height, node image/configuration digests, and all deposits in the configured
   safety window. Preserve database and node snapshots.
3. For an ordinary worker crash or expired lease, restart a healthy scanner
   instance and let lease expiry/takeover and the safety-window rescan recover
   from the last committed cursor. Confirm duplicate event keys do not change
   totals.
4. For a hash mismatch, compare both approved self-hosted node data sets and
   investigate node corruption, wrong-network configuration, or an invalid
   restore. Do not overwrite the saved hash, rewind the cursor manually,
   delete deposits, or reverse credited balances.
5. Resume settlement only after the incident owner has reconciled the saved
   event set and balance audits against a healthy solidified chain and recorded
   the disposition. Resume sweeps and orders last.

## Underpayment and late payment

1. Query by order ID and deposit address. Verify the expected, received,
   credited, and overpaid raw amounts in `onchain_trace.intent`, then enumerate
   every `(transaction_id, log_index)` deposit.
2. A non-expired underpayment remains `PARTIALLY_PAID`; do not credit it. The
   user may send the exact remaining amount to the same address.
3. An expired underpayment is `REVIEW_REQUIRED`. Preserve the funds and review
   it within the P2 SLA; do not silently discard it, attach it to another
   order, or refund it automatically.
4. A late payment that brings an unfulfilled order to the expected amount is
   allowed to settle once automatically. Confirm the order audit marks the
   late recovery and the credit idempotency key appears only once.
5. If totals, order state, and balance audits disagree, stop settlement and
   escalate as a reconciliation incident rather than applying a manual credit.

## Completed-address reuse

Any new valid transfer to an address whose order is already fulfilled must be
`REVIEW_REQUIRED` and must not fulfill the original order again.

1. Query deposits by address and open the original order's `onchain_trace`.
2. Confirm the new transaction/log index is distinct and the original intent
   has a single settlement idempotency key and settled timestamp.
3. Keep the new deposit in manual review. Do not reassign the address, mutate
   the original order, merge the deposit into another order, or ask the normal
   sweep signer to refund an arbitrary address.
4. Resolve ownership and any refund or credit through the approved dual-control
   process, retaining the review decision and external transaction reference.

## Signer unavailable

Signals include `SIGNER_UNAVAILABLE`, mTLS errors, failed TPM unseal, wallet
fingerprint mismatch, or an invalid signer audit journal.

1. Enter safe rollback mode: stop new orders and new sweep claiming, but keep
   scanner, existing-order settlement, reconciliation, and manual review
   active. Already credited balances remain valid.
2. Continue tracking sweeps already in `BROADCAST` or `CONFIRMING`; do not
   submit replacement sweep requests merely because the signer response was
   lost.
3. Check signer health, certificate validity/SPIFFE identities, TPM access,
   encrypted carrier integrity, configured wallet fingerprint, and hash-chain
   journal continuity. Never enable a plaintext or embedded-key fallback.
4. If broadcast outcome is uncertain, query the approved FullNode by known
   transaction ID and compare the source balance before any retry.
5. Re-enable new sweep work only after signer identity, wallet fingerprints,
   replay protection, policy limits, and a controlled health request pass.

## TRON resource shortage

Signals include `RESOURCE_INSUFFICIENT`, `RESOURCE_WAIT` sweeps, low Energy or
Bandwidth, or insufficient TRX for the configured fallback policy.

1. Disable new sweep claiming while keeping deposits and settlement running.
2. Inspect `available_energy`, `available_bandwidth`, `trx_balance_sun`,
   `resource_wait_sweeps`, and the affected sweep records in order traces.
3. Restore only through the approved self-owned resource account: stake or
   delegate Energy/Bandwidth, or apply the controlled minimum TRX policy. Do
   not repeatedly broadcast transactions known to lack resources, and do not
   make an unreviewed third-party resource service a correctness dependency.
4. After replenishment, require a fresh resource check and process the oldest
   waiting task first. Verify actual resource consumption before gradually
   restoring normal sweep concurrency.

## Emergency disable and recovery

Use safe rollback mode for application/signer upgrades, suspected signer
compromise, resource exhaustion, or unexplained sweep behavior. Use the stricter
hash-conflict mode whenever solidified history or recorded deposits are in
doubt. If signer policy bypass or key compromise is suspected, also isolate
signer network access and follow the security incident/key-rotation procedure;
configuration switches alone are not containment.

Before recovery, confirm all of the following:

- node network/contract identity and solidified height/hash continuity pass;
- cursor health is healthy and scan lag has returned within threshold;
- pending settlements are draining and no order has multiple credits;
- every broadcast/confirming sweep has an explained chain outcome;
- reconciliation mismatch count is zero or each mismatch has an approved
  review disposition;
- signer identity, wallet fingerprints, journal and policy checks pass;
- Energy, Bandwidth, TRX and hot-wallet thresholds are within policy.

Restore in this order: scanner health, settlement, reconciliation, sweep work,
then new order creation. Monitor the health, reviews, and a small internal test
deposit through solidification and settlement before returning to normal
traffic.
