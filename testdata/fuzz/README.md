# Fuzz corpus seeds

Each subdirectory holds file-based seed corpus entries for one fuzz target in
`pkg/sepmeta/fuzz_test.go`. These run on every `go test` invocation and are
additive to the in-code `f.Add()` seeds (which are seeded from the golden Wasm
fixtures in `testdata/wasm/`).

## Provenance

Seed construction date: **2026-11-20**.

### FuzzDecodeMeta

Seeds encode XDR `contractmetav0` section bytes as Go fuzz corpus entries
(`go test fuzz v1 / []byte("...")`). The XDR structure follows the
`pkg/sepmeta.DecodeMeta` specification:

- Each entry is an `ScMetaEntry` with discriminant 0 (`ScMetaKindScMetaV0`).
- Strings are big-endian 4-byte length + data + zero-padding to a 4-byte boundary.

Encoding patterns were validated against the following mainnet Wasm hashes from
`report/mainnet-raw.csv` (all with `has_meta=true`, `parse_status=ok`,
extracted on 2026-11-20):

| Hash (prefix) | size_bytes | Encoding pattern |
|---------------|-----------|------------------|
| `00258cacff57` | 6029 | rsver + spec, no sep key |
| `003710b383f9` | 90138 | rsver + spec, no sep key |
| `003d85432305` | 13061 | rsver + spec, no sep key |
| `006a2161fc23` | 14097 | rsver + spec, no sep key |
| `00949b550bb9` | 3865 | rsver + spec, no sep key |

The seed bytes were constructed by hand following the XDR specification, not by
fetching and parsing real Wasm (the report contains hashes only, not raw bytes).
They are equivalent to what `sepmeta.DecodeMeta` would read from a live
contract with these patterns.

### FuzzDecodeSpec

Seeds encode XDR `contractspecv0` section bytes, extracted from the golden Wasm
fixtures in `testdata/wasm/` using a Go extractor at
`scripts/extract_fuzz_corpus.go`. Each fixture was built by the Soroban Rust SDK
and represents actual on-chain contract spec encoding.

### FuzzReadCustomSections

Seeds are minimal Wasm binaries with a `contractmetav0` custom section, covering:
- A complete minimal Wasm with meta section (`seed001`)
- Wasm header only, no sections (`seed002`)
- Empty bytes (`seed003`)

### FuzzParseSEP47

Seeds cover the token classification cases in CLAUDE.md §5.2:
canonical numbers, comma-lists, leading zeros, `SEP-` prefixes, oversized
values, two-entry inputs, and empty strings.

## Regression inputs

Any crash found during a `go test -fuzz=…` run is committed here as a file
named `crash_<target>_<date>_<brief-description>`, together with a fix in the
same commit (CLAUDE.md §5.4).
