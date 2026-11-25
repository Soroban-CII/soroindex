# SEP-41 partial-threshold calibration

## Census and method

This decision uses the committed mainnet census at
[`report/mainnet-summary.json`](https://github.com/Soroban-CII/soroindex/blob/main/report/mainnet-summary.json), generated on
7 October 2026. Its `ok_histogram` counts unique measured Wasm hashes by the
number of SEP-41 required functions that match. There are 2,176 hashes with a
spec and 2 without one; the latter remain `no_spec` at every threshold.

The calculation applies the current matcher rule: all 10 required functions
means `match`; otherwise, a hash is `partial` when its OK count is at least the
threshold times 10, and `mismatch` below that cutoff. It does not change the
ruleset or the match count.

The counts below were calculated directly from the histogram:

```text
threshold,match,partial,mismatch,no_spec
0.3,120,104,1952,2
0.4,120,91,1965,2
0.5,120,79,1977,2
0.6,120,51,2005,2
0.7,120,48,2008,2
0.8,120,47,2009,2
```

These are counts of unique Wasm hashes, not contracts. Each row totals 2,178
measured hashes, including the two `no_spec` entries.

## Decision

Keep the default threshold at **0.5**. It preserves the documented meaning of
partial as matching at least half the required interface. Lowering it to 0.3
would classify 25 hashes with only 3 or 4 matching functions as partial.
Raising it to 0.8 would move 32 hashes with 5, 6 or 7 matching functions from
partial to mismatch. Neither change affects the 120 full matches, and the
census provides no evidence that either alternative is a better boundary.

The threshold remains configurable for deployments with different needs.
Revisit this decision when the rule's required interface or a later census
changes the measured distribution.
