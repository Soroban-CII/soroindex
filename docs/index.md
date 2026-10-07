# soroindex

soroindex is a public index of Soroban smart contracts. It answers "which contracts are tokens?" and similar questions. For every deployed contract it records:

1. **Declared:** which SEPs the contract claims to implement, read from its SEP-47 meta.
2. **Inferred:** whether its interface spec actually matches those SEPs, from a static check.
3. **Verified:** on testnet only, whether it passed a conformance test suite. Deferred; see [Trust tiers](trust-tiers.md).

It also tracks how all three change when a contract is upgraded.

## The gap it fills

[SEP-47](https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0047.md) lets a contract declare which SEPs it implements, in a `sep` entry of its `contractmetav0` Wasm section. Stellar CLI and Stellar Lab read that declaration for one contract at a time. Nothing scans every deployed contract, so "list every contract that implements SEP-41" has no answer today. SEP-47 leaves claims unverified on purpose. soroindex adds the inferred tier on top of the declared one. The [prior-art survey](prior-art.md) covers what already exists.

## Adoption today

From the Phase 0 census of every mainnet contract on 7 October 2026 (ledger 64,817,831). The full method is in the [adoption report](adoption.md).

| | By unique Wasm hash | By contract |
| --- | ---: | ---: |
| Declares any SEP (`sep` meta) | 13 of 2,178 (0.60%) | 16 of 141,977 (0.01%) |
| Matches the SEP-41 interface | 120 | 1,141 |
| Matches SEP-41, declares nothing | 109 | 1,127 |

Fewer than 1% of mainnet Wasm hashes declare anything. The index therefore leads with what code **matches**, and tracks declarations as adoption grows.

## Status

Pre-release. Built and tested: the parser, the census, seeding an index, the undeclared-gap query and match recomputation. Planned: the incremental sync loop, the HTTP API and the Docker image. The [Stellar Wave application](wave/APPLICATION.md) lists both.
