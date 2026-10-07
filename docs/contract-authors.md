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

## Check the result

Build with `stellar contract build`, then read the meta:

```text
$ stellar contract info meta --wasm target/wasm32v1-none/release/your_contract.wasm --output json
[{"sc_meta_v0":{"key":"sep","val":"41"}},{"sc_meta_v0":{"key":"rsver","val":"1.98.1"}}, ...]
```

That output shape is from the `token_full_sep` fixture, built with stellar-cli 28.0.0. A `sep47idx wasm <hash>` command that also shows the anomaly and interface verdict is planned.

## Declaring is a claim, not proof

The `declared` tier records what you say. The `inferred` tier checks your function types against the SEP's rule file. If you declare SEP-41 without its 10 functions, the index shows both facts side by side.
