# USDT-ERC20 operations runbook

This runbook covers incidents in the self-hosted Ethereum deposit and fund
sweeping path. Production crediting evidence comes only from the approved
self-hosted execution/consensus stacks: Geth + Lighthouse and Nethermind +
Teku. Retain all timestamps, endpoint identities, block heights/hashes,
transaction hashes, nonces, receipt fields, logs, signer audit IDs and config
versions in the incident record.

## Safety rules

- Never replace `finalized` with `latest`, a fixed confirmation count, a block
  explorer, hosted RPC, or third-party index as the crediting boundary.
- Never delete, rewrite or manually advance a scan cursor, deposit, gas
  funding, nonce state, transaction version, sweep or balance audit to clear an
  alert.
- Never resolve a nonce incident by sending a business-supplied raw
  transaction. Only replay the original high-level signer request. The signer
  owns nonce selection, semantic checks, EIP-1559 fees and replacement policy.
- A gas-funding or sweep failure must not reverse an already credited user
  balance. Automatic on-chain refunds remain prohibited.
- Disable new orders before maintenance that can make node identity, finality,
  scanner state or contract identity uncertain.
- Disabling new sweep claims must not stop tracking transactions already in
  `BROADCAST`, `CONFIRMING` or a same-nonce replacement family.

## Operator queries and signals

The endpoints require the deployment's normal administrator authentication.
Do not put real credentials in an incident ticket.

```sh
export SUB2API_ADMIN_URL="https://sub2api.internal.example"
export SUB2API_ADMIN_TOKEN="REPLACE_IN_SHELL_ONLY"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/health"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/deposits?network=ethereum-mainnet&transaction_id=TX_HASH&log_index=0"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/onchain/reviews?network=ethereum-mainnet&page=1&page_size=50"

curl -fsS -H "Authorization: Bearer ${SUB2API_ADMIN_TOKEN}" \
  "${SUB2API_ADMIN_URL}/api/v1/admin/payment/orders/ORDER_ID"
```

The health response exposes primary/backup latest and finalized heights,
common finalized height, finalized and scan lag, RPC errors, pending nonce and
nonce conflicts, pending transaction age, replacement count, gas sponsor ETH,
unswept USDT and finalized gas cost. Order detail joins the payment order with
the intent, ERC20 logs and receipt outcome, balance audits, gas-funding and
sweep transaction versions, and related nonce states.

## Feature-switch matrix

Use validated deployment configuration and the normal rollout process. Do not
set `onchain.ethereum.enabled=false` during routine rollback because scanners
and trackers need the network configuration.

| Mode | `enabled` | `order_creation_enabled` | `scanner_enabled` | `settlement_enabled` | `sweeper_enabled` |
| --- | --- | --- | --- | --- | --- |
| Normal | `true` | `true` | `true` | `true` | `true` |
| Signer, fee, gas or nonce incident | `true` | `false` | `true` | `true` | `false` |
| Node divergence or finalized-history doubt | `true` | `false` | `true` | `false` | `false` |

The safe funds mode keeps scanning, existing-order settlement and transaction
tracking active. The finality incident mode also stops settlement until the
recorded chain facts are verified.

## Primary and backup node divergence

`NODE_DIVERGENCE`, `HASH_CONFLICT` or different hashes at the common finalized
height is P0.

1. Immediately disable new orders, settlement and new sweep claims. Preserve
   both node data directories, client logs, versions, peer state and config.
2. Record primary/backup latest and finalized heights/hashes, common finalized
   height/hash, cursor height/hash and all deposits in the safety window.
3. Verify chain ID, genesis/network configuration, execution-to-consensus JWT
   pairing, system clocks, disk health and whether either stack was restored
   from an incompatible snapshot.
4. Compare the two approved stacks independently. Do not choose the result
   that matches an explorer, overwrite the stored finalized hash or rewind the
   application cursor manually.
5. Resume only after both stacks agree at a monotonically advancing common
   finalized boundary and every recorded deposit/audit in the affected range
   has an approved reconciliation disposition.

## Finalized unavailable or delayed

Signals include `FINALIZED_DELAY`, `SCAN_STALLED`, a missing finalized block,
or a disconnected consensus client/Engine API.

1. Disable new orders. Keep the scanner enabled so it remains fault-closed and
   observable; no deposit may be credited from `latest`.
2. Inspect both consensus clients, Engine API authentication/JWT permissions,
   execution sync state, peer count, checkpoint status, disk latency and clock.
3. Confirm whether only one stack is affected. A healthy stack may serve
   reads, but common-finalized consistency must still pass before scanning or
   settlement advances.
4. After repair, require stable finalized advancement, common hash agreement,
   safety-window rescan without new conflicts, and scan-lag recovery before
   re-enabling orders.

## RPC range and response errors

Signals include `RPC_ERRORS`, response-size failures, timeouts, `eth_getLogs`
range errors or repeated failover.

1. Disable new orders if both endpoints are impaired. Keep already persisted
   deposits and balances unchanged.
2. Capture endpoint, method, requested range, response/error class, retry
   count, cursor height and node/proxy logs. Check TLS routing and RPC limits.
3. Allow the scanner's deterministic range-halving logic to retry. Do not skip
   the failing block range or manually advance the cursor.
4. If an endpoint is unhealthy, restore the approved alternate self-hosted
   stack. Do not add a hosted provider as a temporary crediting source.
5. Recovery requires the original range to scan completely, deterministic log
   ordering, receipt validation and common-finalized hash agreement.

## Nonce blocked, gap or conflict

`NONCE_CONFLICT` is P0; a growing `PENDING_NONCE` backlog is P2 and escalates
to P1 when funds operations stop progressing.

1. Stop new gas funding and sweep claims for the affected sender. Continue
   receipt/finalized tracking for every known transaction version.
2. Query the order trace and nonce state. Record sender, chain ID, stored next
   nonce, node pending nonce, lease owner/expiry, all same-nonce hashes and
   signer audit IDs.
3. Check both nodes for every known hash and receipt. Distinguish an active
   lease, response loss, a mined older version, an external transaction and a
   genuine nonce gap.
4. Let an expired lease be taken over by the normal repository path. Do not
   edit `next_nonce`, reuse a lower nonce, cancel with a zero-value transfer, or
   submit a raw replacement from the business service.
5. For an unknown broadcast, replay only the identical high-level request.
   The signer must preserve nonce and semantics and may raise both EIP-1559 fee
   caps within policy.
6. Resume allocation only after the node pending nonce and durable state agree
   and the conflict state has passed normal reconciliation.

## Gas sponsor ETH shortage

Signals include `ETH_LOW`, an unavailable balance check, or gas-funding signer
errors reporting insufficient ETH.

1. Stop new sweep claims; keep deposits, settlement and transaction tracking
   running.
2. Verify the configured/recorded sponsor address on both approved nodes and
   compare its balance with pending funding value plus worst-case EIP-1559
   fees. Review pending nonces before adding funds.
3. Replenish only through the approved treasury and dual-control process. Do
   not expose the sponsor key, add an unapproved faucet/service, or send ETH
   directly to arbitrary user-supplied addresses.
4. Require a fresh balance check and process the oldest eligible gas-funding
   task first. Confirm it reaches canonical finalized before its ERC20 sweep
   is allowed to broadcast.

## Fee spike or gas budget exceeded

Signals include fee estimates above `0.006 ETH`, `GAS_COST_HIGH`, repeated
replacement attempts or uneconomic sweep decisions.

1. Stop new gas funding and sweeps. Deposits and user credits continue; funds
   remain at their derived addresses.
2. Record base fee, suggested priority fee, gas estimate, 120% buffered cost,
   signer caps, replacement count and accumulated finalized gas cost.
3. Do not raise signer caps ad hoc, remove the 120% buffer, lower the 200 USDT
   economic threshold or split a request to bypass policy.
4. Wait for fees to normalize or obtain the required security/operations
   approval for a reviewed configuration change. Validate the new limits in a
   non-production environment first.
5. Re-enable gradually and confirm the first funding and sweep stay within
   both per-transaction and cumulative policies.

## Signer unavailable or replacement stuck

Signals include signer mTLS/TPM/journal errors, `TRANSACTION_STUCK` or repeated
`TRANSACTION_REPLACEMENTS`.

1. Enter safe funds mode. Isolate signer network access if compromise is
   suspected, but keep chain tracking and user settlement active.
2. Check mTLS identities, certificate validity, TPM unseal permissions,
   encrypted key carriers, wallet fingerprints, policy configuration and audit
   journal continuity. Never enable plaintext or embedded-key fallback.
3. For each transaction family, query every hash. Any same-nonce version may
   win; once one succeeds, mark it finalized and stop replacing siblings.
4. A failed receipt is not success. ERC20 sweep completion additionally
   requires exactly one matching `Transfer(source, destination, amount)` log in
   a canonical finalized block.
5. Re-enable funds operations only after a controlled health request succeeds,
   replay protection is intact and every pending family has an explained chain
   outcome or approved review disposition.

## Recovery checklist

- both execution/consensus stacks have the expected identity and agree on the
  common finalized hash;
- finalized and scanner lag are within configured thresholds;
- cursor health is healthy and no range was skipped or manually rewritten;
- pending settlements are draining and each deposit event is credited at most
  once;
- sponsor ETH is above threshold and fee estimates are within policy;
- durable next nonce matches the node pending nonce with no conflict;
- every broadcast/replacement family has one explained winner or remains under
  tracked review;
- unswept USDT and finalized gas cost are within operational thresholds;
- signer identity, wallet separation, journal and policy checks pass.

Restore in this order: node finality, scanner, settlement, nonce reconciliation,
gas funding, ERC20 sweeps, then new order creation. Monitor one approved small
internal test through finalized deposit credit, gas funding and ERC20 sweep
before returning to normal traffic.
