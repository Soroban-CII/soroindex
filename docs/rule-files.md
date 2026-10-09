# Writing a rule file

A rule file lists the functions a SEP requires, by type. The matcher compares a contract's `contractspecv0` functions with it. Rule files live in `rules/` and are named `sep-NNNN.json` after their SEP.

## Format

`rules/sep-0041.json`, derived from SEP-41 v0.5.2 (updated 24 August 2026), first lines:

```json
{
  "sep": 41,
  "ruleset_version": "sep41-v0.5.2",
  "source": "stellar/stellar-protocol ecosystem/sep-0041.md, Version 0.5.2, Updated 2026-08-24; diffed against live text (commit 4db3e802b2) on 2026-10-07",
  "required": [
    {"fn": "allowance", "inputs": [["Address"],["Address"]], "output": ["i128"]},
    {"fn": "transfer",  "inputs": [["Address"],["MuxedAddress","Address"],["i128"]], "output": []}
  ],
  "optional": []
}
```

- Each element of `inputs` is one parameter: the list of types accepted at that position. `transfer.to` accepts `MuxedAddress` (added in SEP-41 v0.4.0) or the older `Address`.
- Types use canonical names: `Address`, `MuxedAddress`, `i128`, `u32`, `String`, `Option<T>`, `Vec<T>`, `Map<K,V>`, `Result<T,E>`, `(A,B)`, `BytesN<32>`, and `udt:<Name>` for user-defined types.
- Only types and their order are compared, never parameter names.
- `ruleset_version` changes whenever the rules change. `sep47idx sync --recompute` then re-matches stored Wasm without fetching anything.

[`rules/schema.json`](https://github.com/ciscokwiz/soroindex/blob/main/rules/schema.json) validates every rule file; CI runs it (`make rules-check`).

## Status thresholds

| Status | Condition |
| --- | --- |
| `match` | Every required function present with accepted types |
| `partial` | At least half of the required functions OK (`--partial-threshold`, default 0.5) |
| `mismatch` | Fewer than half OK |
| `no_spec` | The Wasm has no spec section |

On mainnet (7 October 2026), 34 live Wasm hashes score 8 of 10 for SEP-41. 30 of them lack exactly `burn` and `burn_from`; they are `partial`, not `match`, because SEP-41 v0.5.2 marks no function optional.

## PR checklist for a new rule file

- [ ] Fetched the SEP's current text and listed the commit you read.
- [ ] Every required function and type matches the text; nothing optional marked required.
- [ ] `source` names the SEP file, its version, its updated date, and the date you diffed it.
- [ ] `make rules-check` passes.
- [ ] A test in `internal/match` covers at least one matching and one mismatching spec.
