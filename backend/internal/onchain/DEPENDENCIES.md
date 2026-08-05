# On-chain dependency and identity baseline

This baseline is part of the `add-self-hosted-usdt-trc20-recharge` security boundary.

## Network and token identities

| Network | Network identity | Chain ID | USDT contract | Decimals | Trust classification |
| --- | --- | ---: | --- | ---: | --- |
| TRON mainnet | `tron-mainnet` / Java-Tron mainnet genesis | n/a | `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t` | 6 | Production Tether allowlist |
| Ethereum mainnet | `ethereum-mainnet` | 1 | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 6 | Production Tether allowlist |
| TRON Nile | `tron-nile` | n/a | Deployment-owned test token, required explicitly | 6 | Non-production only |
| Ethereum Sepolia | `ethereum-sepolia` | 11155111 | Deployment-owned test token, required explicitly | 6 | Non-production only |

TRON has no Ethereum-style numeric chain ID. The signer and scanner must verify the configured Java-Tron network/genesis identity. Production contract identities are immutable in code. A test token is never promoted into the production allowlist and must be pinned explicitly by each non-production deployment.

## Go dependencies

| Purpose | Module/version | Source and rationale |
| --- | --- | --- |
| Ethereum address, ABI, log, receipt, RPC, and EIP-1559 types | `github.com/ethereum/go-ethereum v1.17.5` | Upstream Geth repository and Go module; use `rpc.Client` for the literal `finalized` block tag and typed `ethclient`/`core/types` primitives elsewhere. |
| BIP32 public child derivation and Base58Check | `github.com/btcsuite/btcd/btcutil v1.2.0` | btcsuite upstream module; `hdkeychain` derives only non-hardened children after the account-level xpub, and `base58` validates TRON addresses. |
| Java-Tron node access | Go standard library HTTP client against the documented Java-Tron FullNode/SolidityNode HTTP API | Avoids an additional third-party TRON SDK in the trusted scanner. Requests use fixed endpoints, bounded bodies, timeouts, and normalized local models. |

Both Go modules are pinned in `go.mod` and verified by `go.sum`. Dependency upgrades require reviewing release notes, running fixed address vectors and scanner fixtures, and running `govulncheck` before merge.

## Derivation boundary

The business process receives account-level extended public keys only. Hardened path components are derived offline:

- TRON account xpub: `m/44'/195'/0'`; business-side suffix: `/0/index`.
- Ethereum account xpub: `m/44'/60'/0'`; business-side suffix: `/0/index`.

The signer independently derives the same path from the network-specific encrypted seed. TRON and Ethereum roots, xpubs, indexes, and destination wallets must never be reused across networks.
