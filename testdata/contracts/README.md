# Fixture contracts

Rust sources for the golden Wasm files in `../wasm/` (CLAUDE.md §5.4).
`scripts/build-fixtures.sh` builds them. The `.wasm` outputs are committed, so
Go builds and CI never need Rust.

## Versions used

Recorded on 2026-10-07, when the committed `.wasm` files were built:

| Tool | Version | Pinned in |
| --- | --- | --- |
| Rust | 1.98.1 (target `wasm32v1-none`) | `rust-toolchain.toml` |
| soroban-sdk | 28.0.0 (latest stable on crates.io) | `Cargo.toml` (`=28.0.0`) |
| soroban-token-sdk | 28.0.0 | `Cargo.toml` (`=28.0.0`) |
| stellar-cli | 28.0.0 (`300aaf69`) | `scripts/build-fixtures.sh` |

soroban-sdk 28 refuses a plain `cargo build`; it must be built with
`stellar contract build` (stellar-cli v25.2.0 or later). The CLI writes its
own version into each Wasm's meta as `cliver`. A different CLI therefore
changes the bytes, so the script refuses to run with any other version.

Building twice with these versions gives identical bytes; `../wasm/SHA256SUMS`
lists the expected hashes.

## Fixtures

| Wasm | Source | Contents |
| --- | --- | --- |
| `token_full_sep.wasm` | `token_full_sep/` | All 10 SEP-41 functions, `transfer.to: MuxedAddress`; `sep=41`. A working token for testnet and soroban-guard. |
| `token_full_legacy.wasm` | `token_full_legacy/` | All 10 SEP-41 functions, `transfer.to: Address`; no `sep` meta. |
| `token_partial.wasm` | `token_partial/` | No `burn` or `burn_from`; `sep=41`. Uses the same storage as `token_full_sep`, so a deployed `token_full_sep` can upgrade to it. |
| `not_token.wasm` | `not_token/` | `hello`, `add`, `version`; `sep=41` (a false claim). |
| `multi_sep.wasm` | `multi_sep/` | `contractmeta!` in two modules: `sep=41` and `sep=40`, as two meta entries. |
| `no_meta.wasm` | `no_meta/` | Source declares `sep=41`; the script then removes the whole `contractmetav0` section. Spec intact (`ping`). |
| `truncated.wasm` | derived | `token_full_sep.wasm` cut halfway through its `contractmetav0` payload. |

`common/` holds the token logic the three token fixtures share. It has no
`contractmeta!`, so every `sep` entry comes from the fixture's own source.

The three token fixtures also export `__constructor(admin, decimals, name,
symbol)`, `mint` and `upgrade`. These are admin-gated and not part of SEP-41;
they exist so the tokens can be deployed, funded and upgraded on testnet
(CLAUDE.md §7 step 28). The matcher ignores functions not in a rule file.

Besides `sep`, every fixture carries the meta that the toolchain adds itself:
`rsver`, `rssdkver`, `rssdk_spec_shaking` and `cliver`.
