# Self-hosted USDT-TRC20 deployment

This guide deploys the TRON FullNode/SolidityNode pair and `usdt-signer`
without exposing either service publicly. Read
`USDT_SIGNER_SECURITY_REVIEW.md` before enabling any funds operation.

## Build artifacts

Build the business service with the repository's normal image flow. Build the
signer independently from the backend context:

```sh
docker build \
  -f deploy/Dockerfile.usdt-signer \
  --build-arg VERSION="$(git describe --always --dirty)" \
  -t registry.internal/sub2api/usdt-signer:REPLACE_VERSION \
  backend
docker inspect --format '{{index .RepoDigests 0}}' \
  registry.internal/sub2api/usdt-signer:REPLACE_VERSION
```

Promote only a digest-pinned signer image. Build Java-Tron from its reviewed
upstream release or use an internally mirrored, digest-pinned image. FullNode
and SolidityNode must use the same approved network genesis and compatible
release, but separate data volumes and role-specific configuration.

## Network topology

`docker-compose.usdt-tron.example.yml` is an overlay for the standard Compose
deployment. It creates three internal networks:

- `tron-node`: Java-Tron nodes and the TLS RPC proxy;
- `tron-observer`: the business service, signer, and TLS RPC proxy;
- `signer-control`: only the business service and signer.

No Java-Tron or signer port is published on the host. In production, enforce
the same paths with host firewall or CNI policy: the business service may reach
FullNode/SolidityNode HTTPS and signer TCP 9443; the signer may reach FullNode
HTTPS; all other ingress to signer is denied. Java-Tron P2P ports may be
published only on the node hosts and must not expose RPC ports.

Prepare `deploy/tron/fullnode.conf` and `deploy/tron/soliditynode.conf` from the
approved Java-Tron release. Set `JAVA_TRON_IMAGE`,
`JAVA_TRON_FULLNODE_COMMAND`, `JAVA_TRON_SOLIDITY_COMMAND`, and `TPM_GROUP_ID`
(the numeric group owning `/dev/tpmrm0`) in `.env`, then
validate the merged topology before starting it:

```sh
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.usdt-tron.example.yml \
  config > /tmp/sub2api-usdt-tron.rendered.yml
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.usdt-tron.example.yml \
  up -d tron-fullnode tron-soliditynode tron-rpc
```

The mainnet application requires HTTPS node URLs. The example terminates TLS
at Caddy and expects certificates for `tron-fullnode-rpc` and
`tron-solidity-rpc`. `internal-ca-bundle.crt` must contain the normal OS trust
bundle plus the issuing internal CA; do not replace it with only the private
CA certificate.

## Mutual TLS identities

Issue certificates from an offline or enterprise CA. Required SAN identities:

| Certificate | Required SAN | Extended key usage |
| --- | --- | --- |
| signer server | `spiffe://sub2api.internal/usdt-signer` | server auth |
| sweep worker client | `spiffe://sub2api.internal/workload/usdt-sweep-worker` | client auth |

Store the signer server key only on the signer host. Store the client key only
with the business service. Mount read-only files with owner-only permissions
and configure:

```yaml
onchain:
  tron:
    full_node_url: "https://tron-fullnode-rpc:8443"
    solidity_node_url: "https://tron-solidity-rpc:8444"
    signer_url: "https://172.31.21.10:9443"
    signer_client_cert_file: "/run/secrets/sub2api-signer-client/client.crt"
    signer_client_key_file: "/run/secrets/sub2api-signer-client/client.key"
    signer_server_ca_file: "/run/secrets/sub2api-signer-client/server-ca.crt"
    signer_server_identity_uri: "spiffe://sub2api.internal/usdt-signer"
```

The signer environment must authorize only the client URI above. Test both a
valid client and a CA-valid certificate with an unauthorized URI; the second
request must be rejected before production activation.

## Encrypted key carriers and backup

Create the three encrypted carriers and TPM-sealed data keys on an isolated
provisioning host. Never place plaintext seed, mnemonic, xprv, child key, gas
sponsor key, or recovery material in `.env`, Compose, the business database,
logs, or tickets.

Back up these encrypted operational artifacts after every approved carrier
rotation:

- the three encrypted wallet carrier files;
- the three TPM sealed-key metadata files;
- `operations.jsonl`, preserving permissions and its hash chain;
- the signer image digest, configuration version, wallet fingerprints, TPM
  PCR policy, and public certificates.

The TPM-sealed files alone are not disaster recovery material. Keep a separate
offline encrypted recovery package under two-person control. A restore drill
must use an isolated host and record both operator identities, approval/change
reference, carrier role, wallet fingerprint, time, and host attestation. Verify
derived public fingerprints before the restored signer is allowed network
access. Destroy temporary plaintext material before leaving the isolated host.

## Activation order

1. Start and fully synchronize FullNode and SolidityNode.
2. Verify network identity, solidified height, official USDT contract, and six
   decimals from the internal HTTPS endpoints.
3. Start signer with funds operations isolated; verify TPM unseal, all three
   wallet fingerprints, journal integrity, mTLS authorization, and `/health`.
4. Start the business service with all TRON feature switches disabled and
   verify node, signer, resource, and hot-wallet metrics.
5. Enable scanner, then settlement. Confirm the durable cursor catches the
   SolidityNode head and periodic balance reconciliation reports zero
   differences.
6. Enable sweeper, then order creation. Spend limits protect outgoing funds;
   they do not impose user daily recharge limits.

## Upgrade and rollback

Before an upgrade, disable new order creation and new sweep claiming while
leaving scanning, existing-order settlement, solidification tracking, and
manual review active. Back up PostgreSQL, signer state, node configuration, and
the artifact manifest described above.

Upgrade in this order: SolidityNode/FullNode one role at a time, signer, then
business service. For each step verify health, network identity, solidified
height/hash continuity, signer wallet fingerprints, journal integrity, and
reconciliation status before continuing. Never initialize a new node data
volume or overwrite a scan cursor to hide a hash conflict.

Rollback uses the previous digest-pinned binaries and unchanged persistent
state. Database migrations must be proven backward compatible before rollout;
otherwise restore the coordinated database backup rather than mixing old code
with a newer schema. A signer rollback must retain the current operation
journal and key carriers to preserve replay protection. Re-enable new sweeps
and orders only after a complete health and reconciliation pass.
