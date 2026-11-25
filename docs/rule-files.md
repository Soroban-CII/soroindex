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

- The first type at each position is the type the SEP text names. Later types are accepted for compatibility with earlier versions of the SEP.

[`rules/schema.json`](https://github.com/Soroban-CII/soroindex/blob/main/rules/schema.json) validates every rule file; CI runs it (`make rules-check`).

## Historical versions and `matched_version`

`rules/history/` keeps earlier versions of a SEP's interface, named `sep-NNNN-vX.Y.Z.json`. Only the top-level `sep-NNNN.json` produces the stored status and the inferred tier. History is used for one thing: reporting the newest version of the SEP that a Wasm's interface implements. That appears as `matched_version` on API matches and in the `wasm`, `contract` and `inspect` output.

Each version is checked strictly. Only the first type at each position counts, because that is the type that version's text names. A token with `transfer.to: Address` is `match` against the current SEP-41 file, which still accepts `Address`. Its `matched_version` is `sep41-v0.3.0`, the last version before `MuxedAddress`.

| File | Version | stellar-protocol commit | Interface |
| --- | --- | --- | --- |
| `sep-0041.json` | 0.5.2 | `4db3e802b2929c865a27bd43f2544c979150137d` | 10 functions, `transfer.to: MuxedAddress` (since v0.4.0, `15335f1e6d7cdb1498741aa6038c8f04c08dc2b6`) |
| `history/sep-0041-v0.3.0.json` | 0.3.0 | `1e09d9f6363516a4d717cb8780b580c05bd59407` | 10 functions, `transfer.to: Address`; unchanged since v0.2.0 (`306bd66735abf0401ee075578f7567832578e4bb`) |
| `history/sep-0041-v0.1.0.json` | 0.1.0 | `03a1aed540a231ecaddb12c7171e820e0758e061` | 11 functions, adds `spendable_balance` |

v0.2.0 has no file of its own. Its function list is identical to v0.3.0, so v0.3.0 would always be the newest match. The commits come from `gh api "repos/stellar/stellar-protocol/commits?path=ecosystem/sep-0041.md"`, read on 23 November 2026.

On the golden fixtures, `token_full_sep.wasm` reports `sep41-v0.5.2`, `token_full_legacy.wasm` reports `sep41-v0.3.0`, and `token_partial.wasm` and `not_token.wasm` report `null` (`internal/match/versions_test.go`).

A historical file must describe a SEP that has a current file. It must name exactly one type per input position, have a `ruleset_version` equal to its file name's version, be older than the current file, and have no `requires`. The loader rejects anything else.

## Status thresholds

The threshold remains configurable. The mainnet census calculation and the
decision to retain 0.5 are recorded in
[SEP-41 partial-threshold calibration](threshold-calibration.md).

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
