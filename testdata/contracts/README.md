# Fixture contracts

Rust sources for the golden Wasm files in `../wasm/` (CLAUDE.md §5.4).
`scripts/build-fixtures.sh` builds them. The `.wasm` outputs are committed, so
Go builds and CI never need Rust.

## Versions used

Recorded on 2026-10-07, when the first committed `.wasm` files were built.
Fixtures added since (2026-11-23) were built with the same versions, and
rebuilding left every earlier hash in `../wasm/SHA256SUMS` unchanged.

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
| `oracle_sep40.wasm` | `oracle_sep40/` | The 7 SEP-40 `PriceFeedTrait` functions with its `PriceData` struct and `Asset` union; `sep=40`. The only fixture whose SEP functions use user-defined types (`udt:Asset`, `Option<udt:PriceData>`, `Option<Vec<udt:PriceData>>`). |
| `nft_sep50.wasm` | `nft_sep50/` | All 11 SEP-50 `NonFungibleToken` functions, `TokenID` and `Balance` as `u32`; `sep=50`. |
| `nft_sep50_partial.wasm` | `nft_sep50_partial/` | `nft_sep50` without `approve_for_all` (10 of 11); `sep=50`. For partial-match tests. |

`common/` holds the token logic the SEP-41 fixtures share. It has no
`contractmeta!`, so every `sep` entry comes from the fixture's own source.

The three token fixtures also export `__constructor(admin, decimals, name,
symbol)`, `mint` and `upgrade`. These are admin-gated and not part of SEP-41;
they exist so the tokens can be deployed, funded and upgraded on testnet
(CLAUDE.md §7 step 28). The matcher ignores functions not in a rule file.

Besides `sep`, every fixture carries the meta that the toolchain adds itself:
`rsver`, `rssdkver`, `rssdk_spec_shaking` and `cliver`.

## SEP sources

Each fixture copies its functions and types from the SEP text in
stellar/stellar-protocol at commit `7a502f4a4ebdc9b6a8c2abf2509e74f8a385b77d`
(read 2026-11-23). The fixture's source comment cites it too.

| Fixture | SEP text |
| --- | --- |
| `oracle_sep40` | `ecosystem/sep-0040.md`, Version 0.1.0, Updated 2023-05-13 |
| `nft_sep50`, `nft_sep50_partial` | `ecosystem/sep-0050.md`, Version 0.1.0, Updated 2025-03-10 |

**SEP-40.** The SEP writes `PriceData` and `Asset` without `pub` or derives.
The fixture adds both so the contract compiles; neither changes the spec
entries. Price reporting is out of scope for SEP-40, so `__constructor` and
an admin-gated `set_price` are extra.

**SEP-50.** `TokenID` and `Balance` are left generic ("should be an unsigned
integer"); the fixture uses `u32`, as OpenZeppelin's Stellar NFT library
does. The contract signatures spell out `u32` because the SDK spec generator
turns a Rust type alias into a reference to an undefined user type (the
build warns "type 'TokenID' ... is not defined in the spec"), and
`TestNFTFixtureHasNoUndefinedTypes` guards this. SEP-50 marks no function
optional. The trait doc for `approve` and the SEP's Events section give
different event layouts; the fixture follows the Events section. Events do
not appear in `Functions` output or affect the matcher. `__constructor` and
`mint` are extra.
