# Trust tiers

Each fact the index records belongs to exactly one tier. Tiers are never mixed.

| Tier | Meaning | Source | Networks |
| --- | --- | --- | --- |
| `declared` | The contract's meta lists the SEP | SEP-47 `sep` meta entries | all |
| `inferred` | Its spec section matches the SEP's rule file | `contractspecv0` + [rule file](rule-files.md) | all |
| `verified` | It declares the SEP **and** soroban-guard exited 0 on this exact Wasm hash | soroban-guard run | testnet only |
| `protocol` | A Stellar Asset Contract; implements SEP-41 by protocol definition | contract kind | all |

Rules:

- A Stellar Asset Contract is never `declared`. An inferred match is never presented as a claim.
- Verification belongs to a Wasm hash. After an upgrade, earlier verifications are stale and not counted.
- The **undeclared gap** is a Wasm whose inferred status is `match` and that declares nothing at all. It is always labelled inferred.
- The `verified` tier is **deferred**: with under 1% of mainnet Wasm declaring anything (7 October 2026), there is little to verify yet.

None of the tiers is a security audit. A `verified` pass means soroban-guard's checks passed on one Wasm hash.

## Worked examples

These come from the golden fixtures in `testdata/wasm`, built from the Rust sources in `testdata/contracts`. Every row is asserted by a test.

| Fixture | Declared | Inferred (SEP-41) | Why |
| --- | --- | --- | --- |
| `token_full_sep.wasm` | SEP-41 | `match` 10/10 | All 10 functions, `transfer.to: MuxedAddress` |
| `token_full_legacy.wasm` | none | `match` 10/10 | Same interface with the older `transfer.to: Address`; this is the undeclared gap |
| `token_partial.wasm` | SEP-41 | `partial` 8/10 | Missing `burn` and `burn_from` |
| `not_token.wasm` | SEP-41 | `mismatch` 0/10 | A false claim: three unrelated functions |
| `multi_sep.wasm` | SEP-41, SEP-40 | `mismatch` 1/10 | Two `contractmeta!` lines in separate modules; SEP-47 joins them |
| `no_meta.wasm` | none | `mismatch` 0/10 | Meta section stripped; spec intact |
