# For contract authors

On 7 October 2026, 13 of 2,178 live mainnet Wasm hashes declared any SEP. Declaring takes one line.

## Declare what you implement

In your contract crate, using `soroban-sdk`:

```rust
soroban_sdk::contractmeta!(key = "sep", val = "41");
```

For several SEPs, list them, comma-separated, without leading zeros:

```rust
soroban_sdk::contractmeta!(key = "sep", val = "41,40");
```

Separate modules can each add their own line. SEP-47 joins repeated `sep` entries, so this is equivalent:

```rust
mod token  { soroban_sdk::contractmeta!(key = "sep", val = "41"); }
mod oracle { soroban_sdk::contractmeta!(key = "sep", val = "40"); }
```

The key is exactly `sep`, lowercase. Write numbers plainly: `41`, not `041` or `SEP-41`. The index accepts those in lenient mode but records an anomaly.

## Check the result before you deploy

Build with `stellar contract build`, then run `sep47idx inspect` on the file. It
runs the same parser, claim sources and matcher as the indexer, offline, with no
database or RPC. It shows what the index will record once the code is deployed.

Captured on 21 November 2026 from the committed `token_partial.wasm` fixture,
which declares SEP-41 but leaves out `burn` and `burn_from`:

```text
$ sep47idx inspect token_partial.wasm
2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880: ok (parser 1), local file token_partial.wasm, not indexed
Metadata: [{"key":"sep","value":"41"},{"key":"rsver","value":"1.98.1"},{"key":"rssdkver","value":"28.0.0#48d506712f964094d14176e2f0b02afcd1054567"},{"key":"rssdk_spec_shaking","value":"2"},{"key":"cliver","value":"28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"}]
Declared SEP-41 (sep47-meta), token="41", anomaly=none
Inferred SEP-41: partial (sep41-v0.5.2), 8 functions matched
  Missing: burn, burn_from
```

The first line is the Wasm hash the network will use for this code. `Declared`
lines are your `sep` meta. `Anomaly` lines flag tokens such as `041` or `SEP-41`.
Under each `Inferred` line, `Missing` and `Mismatched` name what to fix, with the
expected and found signatures.

To fail a CI build unless the interface matches, add `--require-match`:

```text
$ sep47idx inspect token_partial.wasm --require-match 41
...
sep47idx inspect: required interface does not match: SEP-41 is partial
$ echo $?
1
```

`--json` prints the same fields as `sep47idx wasm <hash> --json`, minus the live
contracts and indexed ledger. After deployment and indexing, `sep47idx wasm <hash>`
shows the same evidence for the stored code. See the [CLI reference](cli.md#inspect).

## Add to CI with the GitHub Action

The `Soroban-CII/soroindex` repository ships a composite action that downloads a pinned `sep47idx` release, verifies its `SHA256SUMS`, and runs `inspect --require-match` for you:

```yaml
# .github/workflows/check-sep.yml
name: check SEP declarations
on: [push, pull_request]
jobs:
  inspect:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: stellar/stellar-contract-action@v1   # builds your contract
      - uses: Soroban-CII/soroindex/.github/actions/inspect-sep@main
        with:
          wasm: target/wasm32-unknown-unknown/release/my_contract.wasm
          require-sep: 41          # fail if SEP-41 interface does not match
```

The action exposes two outputs you can use in later steps:

| Output | Description |
| --- | --- |
| `hash` | SHA-256 hash of the Wasm, as the network will record it |
| `parse-status` | `ok`, `partial`, or `error` |

**Inputs**

| Input | Default | Description |
| --- | --- | --- |
| `wasm` | _(required)_ | Path to the compiled `.wasm` file |
| `require-sep` | `41` | SEP whose inferred interface must be `match` |
| `release-tag` | _(latest)_ | Pin to a specific release, e.g. `v0.1.0` |
| `verify-checksums` | `true` | Verify `SHA256SUMS` after download |
| `bin-path` | _(auto)_ | Use a pre-built binary; skips download |

The action fails (exit 1) when the binary's inferred interface status is not `match`, so the CI job reports failure before the contract is deployed. It succeeds silently when the interface matches.

## Declaring is a claim, not proof

The `declared` tier records what you say. The `inferred` tier checks your function types against the SEP's rule file. If you declare SEP-41 without its 10 functions, the index shows both facts side by side.
