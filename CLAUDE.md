# CLAUDE.md — Soroban Contract Interface Index

You are a senior Go engineer building a network-wide index of Soroban smart contracts. It records three things for every deployed contract:

1. **Declared**: which SEPs the contract claims to implement, read from its SEP-47 meta.
2. **Inferred**: whether its interface spec actually matches those SEPs, from a static check.
3. **Verified**: on testnet only, whether it passed a conformance test suite.

It also tracks how all three change when a contract is upgraded. You take this repository from an empty folder to a tagged `v0.1.0` release, with repo hygiene, docs, issues, and an application package ready for the Stellar Drips Wave program.

Work to a production standard. No placeholders, no stubs, no "TODO later." A unit of work is finished when it is committed and pushed with its tests.

**Tie-break rule.** Where this document is ambiguous, choose the interpretation that **claims less trust**. An index that understates what a contract does is a minor inconvenience. An index that overstates it can lead someone to integrate an unsafe token. Say which interpretation you chose in the commit message.

**If a requirement in this document is wrong — it cannot work, contradicts itself, contradicts the current text of a SEP, or creates a real risk — stop and say so rather than building it anyway or quietly routing around it.** This document was written on 7 October 2026 from sources checked that day. Standards and libraries move. When the code and this document disagree with a primary source, the primary source wins, and you report the disagreement.

You do not have permission to change, without operator approval:

- the trust-tier definitions (§5.6),
- the public API paths and response shapes (§5.11),
- the database schema after its first migration ships.

If you believe one of them is wrong, say so and wait.

---

## 0. How to work with the operator

The operator is the repo owner. They review your work at the **STOP** points in §7. At each STOP:

1. Stop. Do not start the next stage.
2. Post a report in exactly this shape:

```
## STOP <id> report
Commits: <list of short SHAs + subjects since the last STOP>
Commands run (real output, trimmed only with "..."):
  $ <command>
  <output>
Tests added: <file paths and test names>
Claims that need evidence: <each claim + the test or output that proves it>
Contradictions found in CLAUDE.md or upstream sources: <none | list>
Next stage: <id and first commit you plan>
```

3. Wait for the operator's go-ahead.

Rules for reports:

- "Tests pass" is not evidence. Paste the `go test` output, and name the test that proves each claim.
- If a fix "worked" after you changed more than one thing, you have not found the cause. Isolate it before you report it: test each change alone and both together, then say which variable predicts the outcome.
- Every number in a report or doc must come from a command you ran. Never estimate a figure and present it as measured.

Between STOPs, commit and push as you go (§6). Don't batch work.

---

## 1. What this is, and what it is not

**Plain-English description** (use this in the README and the Wave application):

> A public index of Soroban smart contracts that answers "which contracts are tokens?" and similar questions. For every deployed contract it records what the contract says it implements, what its code interface actually matches, and, on testnet, what it was shown to do when tested. It tracks how those answers change when a contract is upgraded. Anyone can query it through an HTTP API or a command-line tool.

**The gap it fills.** SEP-47 lets a contract declare which SEPs it implements. Stellar CLI and Stellar Lab can read that declaration for one contract at a time. Nothing scans every deployed contract, so a question like "list every contract that implements SEP-41" has no answer today. SEP-47 deliberately leaves claims unverified and puts verification out of scope. This project adds the inferred and verified tiers on top of the declared one.

### Non-goals: do not build these

- Source or build verification (SEP-55 style). Other Wave projects cover it.
- An on-chain registry, self-registration, or admin approval of contracts. The data already exists in each contract's meta.
- Executing contract Wasm in any form inside the indexer. Wasm is untrusted input that is only parsed.
- Event indexing, balances, or token-metadata enrichment. SoroTrail, Stardex, Sierpe and others do this.
- A web frontend. The deliverables are an HTTP API, a CLI, and a static adoption page generated from data.
- ZK or privacy features.
- A `home_domain()` claim source beyond an empty stub with tests (§5.5).
- Your own SEP-41 conformance suite. Phase 2 shells out to soroban-guard; do not reimplement it.

---

## 2. Repository structure

Derive the module path from the git remote. Run `git remote get-url origin` and turn `git@github.com:<owner>/<repo>.git` or `https://github.com/<owner>/<repo>` into `github.com/<owner>/<repo>`. If there is no remote, stop and ask the operator. The project's display name is `<repo>`. The binary is always `sep47idx`.

```
.
├── CLAUDE.md                     # this file
├── README.md
├── CONTRIBUTING.md
├── SECURITY.md
├── CODE_OF_CONDUCT.md            # Contributor Covenant 2.1, unmodified
├── MAINTAINING.md                # maintainer cadence (§7 stage K)
├── CHANGELOG.md                  # Keep a Changelog format
├── LICENSE                       # Apache-2.0
├── Dockerfile
├── Makefile                      # test, lint, fuzz, build, fixtures, docs
├── go.mod / go.sum
├── .github/
│   ├── workflows/ci.yml          # job names: lint, test, fuzz-smoke, build (exactly these)
│   ├── workflows/integration.yml # optional testnet job, manual + nightly
│   ├── workflows/docs.yml        # builds docs site to GitHub Pages
│   ├── ISSUE_TEMPLATE/{bug.yml,feature.yml,rule-file.yml,config.yml}
│   └── pull_request_template.md
├── cmd/sep47idx/
│   ├── main.go                   # subcommand dispatch only
│   └── cmd_*.go                  # one file per subcommand
├── pkg/sepmeta/                  # PUBLIC, importable by others
│   ├── wasm.go                   # bounded custom-section reader
│   ├── meta.go                   # contractmetav0 decode + SEP-47 parse
│   ├── spec.go                   # contractspecv0 decode
│   ├── doc.go                    # package docs with usage example
│   └── *_test.go, fuzz_test.go
├── internal/
│   ├── config/                   # flags + env, one place
│   ├── rpc/                      # JSON-RPC client: getLedgers, getLedgerEntries, getLatestLedger, getNetwork
│   ├── ingest/                   # LedgerSource, BackfillSource, seed import, change extraction
│   ├── claims/                   # ClaimSource interface, SEP47MetaSource, HomeDomainSource stub
│   ├── match/                    # rule loading + Matcher
│   ├── store/                    # SQLite, migrations, queries
│   ├── sync/                     # incremental sync loop
│   ├── api/                      # net/http handlers
│   ├── verify/                   # Verifier interface + soroban-guard adapter
│   └── phase0/                   # adoption report generator
├── rules/
│   ├── schema.json               # JSON Schema for rule files
│   └── sep-0041.json
├── migrations/                   # 0001_init.sql, ... embedded with go:embed
├── testdata/
│   ├── vectors/                  # meta value vectors (.txt in, .json expected)
│   ├── wasm/                     # golden .wasm fixtures, committed
│   └── contracts/                # Rust sources that build the golden fixtures
├── scripts/
│   ├── build-fixtures.sh         # rebuilds testdata/wasm from testdata/contracts
│   ├── hubble-export.sql         # Phase 0 / seed queries
│   ├── create-issues.sh          # gh CLI, creates all planned issues
│   └── repo-settings.sh          # gh CLI, branch protection + topics
├── docs/                         # docs site source (§5.14)
│   └── wave/                     # application package (§7 stage I)
└── report/                       # Phase 0 output, committed
```

---

## 3. Stack and exact versions

All versions below were checked on 7 October 2026. Before you pin each one, check it again against its registry. If one has moved, pin the newer patch release, record it in the table in `CONTRIBUTING.md`, and mention it in your STOP report.

| Component | Pin | Notes |
| --- | --- | --- |
| Go toolchain (build pin) | `go1.27.1` | Put `toolchain go1.27.1` in go.mod. CI uses exactly this. |
| Go language floor (`go` directive) | `1.26` | **Different number on purpose.** The floor is the oldest Go you claim to support, and it can be lower than the build pin. If a dependency's own go.mod needs higher, raise the floor to the **highest** requirement in the whole dependency tree, and report which module forced it. Don't fix only the first error you see. |
| `github.com/stellar/go-stellar-sdk` | `v0.7.3` | Use only the `xdr` package (and `strkey` for contract IDs). It is pre-1.0, so pin it exactly; an upgrade is its own commit with a test run. Confirm `xdr.ScMetaEntry`, `xdr.ScSpecEntry`, `xdr.LedgerCloseMeta`, and the `SC_SPEC_TYPE_MUXED_ADDRESS` spec type exist at this version. If MuxedAddress is missing, stop and report it. |
| `modernc.org/sqlite` | `v1.60.1` | CGO-free driver. Builds must work with `CGO_ENABLED=0`. |
| HTTP | Go standard library `net/http` (1.22+ routing patterns) | No web framework. |
| CLI | Standard library `flag` | No CLI framework. |
| Logging | `log/slog`, JSON handler | — |
| Rust (fixtures only) | Current stable `soroban-sdk`, checked on crates.io at build time | Used only by `scripts/build-fixtures.sh`. Built `.wasm` files are committed, so normal builds and CI never need Rust. Record the exact versions used in `testdata/contracts/README.md`. |
| soroban-guard (Phase 2 only) | `npx soroban-guard@<exact version>` | Needs Node 24. Pin the version you tested. |
| Docs site | MkDocs with Material theme, versions pinned in `docs/requirements.txt` | Check current versions on PyPI and pin exactly. |
| Linter | `golangci-lint`, version pinned in CI | `govet`, `staticcheck`, `errcheck`, `gosec`, `revive`. |

Allowed direct dependencies: the four Go modules above, plus the standard library. Adding any other dependency needs a stated reason in the commit body and a mention in the next STOP report.

---

## 4. Patterns used throughout

- **Errors.** Wrap with context: `fmt.Errorf("fetch wasm %s: %w", hash, err)`. Define sentinel errors in each package (`ErrNotFound`, `ErrRetentionGap`, ...). Never compare error strings.
- **Contexts.** Every I/O function takes `ctx context.Context` first. Every RPC call has a timeout from config. No `context.Background()` outside `main` and tests.
- **No panics** outside `main` setup and tests. Malformed input of any kind (Wasm, XDR, meta values, API params) becomes a recorded error, never a crash.
- **Untrusted bytes.** Wasm bytes and XDR from the network are hostile. Every length read from them is checked against the bytes remaining *and* against a configured maximum *before* anything is allocated (§5.1).
- **Database writes.** One transaction per sync batch. Every write is idempotent (`INSERT ... ON CONFLICT DO UPDATE`, or `DO NOTHING` where history must not change). Use parameterized SQL only; never build SQL with string concatenation.
- **Time.** Store UTC, as RFC 3339 strings. Ledger numbers are the primary clock; wall-clock time is informational only.
- **Tests.** Prefer table-driven tests with `t.Run` names that state the expected behavior. Golden files live under `testdata/`, and a `-update` flag regenerates them. Integration tests that need the network use the build tag `integration`.
- **Versioning visible to users.** `sepmeta.ParserVersion` (string constant) and each rule file's `ruleset_version` appear in every API response that carries derived data.

---

## 5. Specification

### 5.1 `pkg/sepmeta`: Wasm section reader (untrusted input)

`ReadCustomSections(wasm []byte, names []string, lim Limits) (map[string][]byte, error)`

Steps, in this order:

1. Require the magic bytes `\0asm` and version `1`. Otherwise return `ErrNotWasm`.
2. Walk the sections. Each section is a 1-byte id, then a LEB128 u32 size, then the payload.
   - Bounds-check every LEB128 value (at most 5 bytes for a u32) and every size against the bytes remaining.
3. For custom sections (id 0), read the name: a LEB128 length, then UTF-8 bytes. Skip sections whose names you don't need, without copying them.
4. If the same name appears more than once, **concatenate** the payloads in file order. The Soroban toolchain can emit several.
5. Enforce `Limits`: total Wasm size (default 1 MiB; configurable), size per section (default 256 KiB), and a section count cap (default 10,000).
   - Exceeding any limit returns `ErrLimit` together with the limit's name.
6. Non-custom sections are skipped. Never decode code sections.

### 5.2 `pkg/sepmeta`: meta and the SEP-47 parse

`DecodeMeta(section []byte, lim Limits) ([]MetaEntry, error)` decodes the payload as a back-to-back stream of XDR `SCMetaEntry` values until the bytes run out, using the Go SDK decoder with a max depth. A trailing partial entry is `ErrTruncated`. Return the entries decoded so far, plus the error.

`ParseSEP47(entries []MetaEntry, mode Mode) SEPClaims`. The rules come from SEP-47 v0.1.0. Its key text, verified on 7 Oct 2026:

- **Key.** Consider entries whose key is exactly `sep`. Keys are case-sensitive.
- **Several `sep` entries are normal.** SEP-47 explicitly allows repeated entries and says to treat them as joined with commas, because separate Rust modules each emit their own. **Union them. Never flag this as an anomaly.** Record `EntryCount`.
- Split each value on `,` and trim ASCII whitespace from each token.
- Each token is a decimal SEP number with no leading zeros. Classify as follows:

| Raw token | Strict mode | Lenient mode (default) | Anomaly recorded |
| --- | --- | --- | --- |
| `41` | 41 | 41 | — |
| `041` | dropped | 41 | `leading_zero` |
| `SEP-41`, `sep41` | dropped | 41 | `prefixed_token` |
| `abc`, `4.1`, `-1` | dropped | dropped | `non_numeric` |
| empty (from `41,,46` or a trailing comma) | dropped | dropped | `empty_token` |
| more than 6 digits, or a value over 4 KiB | dropped | dropped | `oversized` |

- Deduplicate SEP numbers, keeping first-seen order.
- Every token keeps its `RawToken`.
- Output: `SEPClaims{SEPs []int, Tokens []Token{Raw string; SEP int; Anomaly string; Accepted bool}, EntryCount int}`.

The default mode is lenient, because the declared tier is informative and anomalies are surfaced alongside. The API exposes both the normalized SEP and the anomaly.

### 5.3 `pkg/sepmeta`: spec decode

`DecodeSpec(section []byte, lim Limits) ([]xdr.ScSpecEntry, error)` follows the same streaming rules as `DecodeMeta`.

`Functions(spec []xdr.ScSpecEntry) []FnSig` returns, for each function: its name, input type names in order, and output type names. Use the canonical names `Address`, `MuxedAddress`, `i128`, `u32`, `String`, and so on. Nested types render as `Option<T>`, `Vec<T>`, `Map<K,V>`, `Result<T,E>`. A user-defined type renders as `udt:<Name>`.

### 5.4 Test vectors and fixtures

**Vectors** in `testdata/vectors/`, each with an expected JSON output in both modes: `41`, `41,46`, ` 41 , 46 `, `041`, `SEP-41`, empty string, `abc`, `41,,46`, `41,41`, two separate entries `41` and `46`, a 5,000-character value, and a 7-digit number.

**Golden Wasm** in `testdata/wasm/`, built by `scripts/build-fixtures.sh` from `testdata/contracts/`:

| Fixture | Contents |
| --- | --- |
| `token_full_sep.wasm` | All 10 SEP-41 functions, `transfer.to: MuxedAddress`; `contractmeta!(key="sep", val="41")` |
| `token_full_legacy.wasm` | All 10 SEP-41 functions, `transfer.to: Address`; no `sep` meta |
| `token_partial.wasm` | Missing `burn` and `burn_from`; declares `sep=41` |
| `not_token.wasm` | 3 unrelated functions; `sep=41` (a false claim) |
| `multi_sep.wasm` | Two `contractmeta!` lines in separate modules: `41` and `40` |
| `no_meta.wasm` | Built with meta stripped |
| `truncated.wasm` | `token_full_sep.wasm` cut mid-section, by script |

**Fuzzing**: `FuzzReadCustomSections`, `FuzzDecodeMeta`, `FuzzDecodeSpec`, `FuzzParseSEP47`. Seed each corpus from the fixtures and vectors. The CI `fuzz-smoke` job runs each for 30 seconds. Any crash found is committed to `testdata/fuzz/` as a regression case, with a fix in the same commit.

### 5.5 Claim sources

```go
type ClaimSource interface {
    Name() string
    NeedsInvocation() bool
    Discover(ctx context.Context, w WasmArtifact, c ContractRef) ([]Claim, error)
}
type Claim struct {
    SEP      int
    RawToken string
    Source   string // e.g. "sep47-meta"
    Anomaly  string // "" or one of §5.2's anomaly codes
}
```

- `SEP47MetaSource` reads meta through `pkg/sepmeta`. It never invokes anything.
- `HomeDomainSource` is a stub that would call `home_domain()` by simulation. `NeedsInvocation()` returns `true`, and `Discover` returns `ErrNotImplemented`. It is **not** registered by default.
  - Ship tests proving that a registered invocation-needing source is skipped unless `--allow-invocation` is set.
  - The flag exists, but no shipped source uses it.

### 5.6 Trust tiers (fixed surface)

| Tier | Meaning | Source | Networks |
| --- | --- | --- | --- |
| `declared` | The contract's meta lists the SEP | §5.2 | all |
| `inferred` | Its spec section matches the rule file | §5.7 | all |
| `verified` | It declares the SEP **and** soroban-guard exited 0 on this exact Wasm hash | §5.13 | testnet only |
| `protocol` | A Stellar Asset Contract; implements SEP-41 by protocol definition | contract kind | all |

Rules:

- **Never mix tiers.** A SAC is never `declared`. An inferred match is never presented as a claim.
- **Verification belongs to a Wasm hash.** After an upgrade, earlier verifications show as `stale: true` and are not counted.
- **The "undeclared gap"** is a Wasm whose inferred status is `match` and that declares nothing. It is always labelled inferred.

### 5.7 Rule files and the Matcher

`rules/sep-0041.json`, derived from SEP-41 v0.5.2 (updated 24 Aug 2026). The current text lists 10 trait functions and marks none optional. Mint and clawback are events, not functions.

```json
{
  "sep": 41,
  "ruleset_version": "sep41-v0.5.2",
  "source": "stellar/stellar-protocol ecosystem/sep-0041.md, Version 0.5.2, Updated 2026-08-24",
  "required": [
    {"fn": "allowance",     "inputs": [["Address"],["Address"]],                          "output": ["i128"]},
    {"fn": "approve",       "inputs": [["Address"],["Address"],["i128"],["u32"]],         "output": []},
    {"fn": "balance",       "inputs": [["Address"]],                                      "output": ["i128"]},
    {"fn": "transfer",      "inputs": [["Address"],["MuxedAddress","Address"],["i128"]],  "output": []},
    {"fn": "transfer_from", "inputs": [["Address"],["Address"],["Address"],["i128"]],     "output": []},
    {"fn": "burn",          "inputs": [["Address"],["i128"]],                             "output": []},
    {"fn": "burn_from",     "inputs": [["Address"],["Address"],["i128"]],                 "output": []},
    {"fn": "decimals",      "inputs": [],                                                 "output": ["u32"]},
    {"fn": "name",          "inputs": [],                                                 "output": ["String"]},
    {"fn": "symbol",        "inputs": [],                                                 "output": ["String"]}
  ],
  "optional": []
}
```

- Each `inputs` element lists the types accepted at that position. `transfer.to` accepts `MuxedAddress`, added in SEP-41 v0.4.0, **or** the older `Address`.
- Before committing this file, fetch the current SEP-41 text and diff it against this list. If anything differs, stop and report.
- `rules/schema.json` validates every rule file. CI validates all of them.

`Match(fns []FnSig, r RuleFile) MatchResult` compares the **types and their order**, never parameter names, because the spec section records names but the interface is defined by types. A function present with the right types is ok. A function present with different types is `mismatched`. An absent function is `missing`.

| Status | Condition |
| --- | --- |
| `no_spec` | The Wasm has no `contractspecv0` section |
| `match` | Every required function present with accepted types |
| `partial` | At least half of the required functions OK, but not all |
| `mismatch` | Fewer than half OK |

- The "half" cutoff is configurable as `match.partial_threshold` (default 0.5). Report the Phase 0 distribution of OK counts so the operator can tune it.
- `MatchResult` records `missing`, `mismatched` (expected vs actual), and `matched_variants` (e.g. `{"transfer.to":"MuxedAddress"}`).

### 5.8 Data model (SQLite, one file per network)

The schema lives in numbered migrations under `migrations/`, embedded with `go:embed`. On start:

- Apply migrations in a transaction.
- Refuse to start if `sync_state.schema_version` is newer than the binary knows.
- Refuse to start if `network_passphrase` differs from the configured network.

```sql
CREATE TABLE contracts (
  contract_id       TEXT PRIMARY KEY,          -- C... strkey
  kind              TEXT NOT NULL CHECK (kind IN ('wasm','sac')),
  current_wasm_hash TEXT,                      -- NULL for sac
  sac_asset         TEXT,                      -- 'native' | 'CODE:ISSUER', NULL for wasm
  created_ledger    INTEGER,
  updated_ledger    INTEGER,
  archived          INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE wasm (
  hash              TEXT PRIMARY KEY,          -- hex
  size_bytes        INTEGER,
  first_seen_ledger INTEGER,
  has_meta          INTEGER NOT NULL DEFAULT 0,
  has_spec          INTEGER NOT NULL DEFAULT 0,
  sep_entry_count   INTEGER NOT NULL DEFAULT 0,
  meta_json         TEXT,                      -- all raw key/value pairs
  parse_status      TEXT NOT NULL CHECK (parse_status IN ('ok','partial','error','archived')),
  parse_error       TEXT,
  parser_version    TEXT NOT NULL
);
CREATE TABLE contract_versions (
  contract_id TEXT NOT NULL,
  wasm_hash   TEXT,                            -- NULL for sac
  from_ledger INTEGER NOT NULL,
  to_ledger   INTEGER,                         -- NULL while current
  PRIMARY KEY (contract_id, from_ledger)
);
CREATE TABLE wasm_claims (
  wasm_hash TEXT NOT NULL,
  sep       INTEGER NOT NULL,
  source    TEXT NOT NULL,
  raw_token TEXT NOT NULL,
  anomaly   TEXT,
  PRIMARY KEY (wasm_hash, sep, source)
);
CREATE TABLE parse_anomalies (                 -- tokens that produced no SEP
  wasm_hash TEXT NOT NULL,
  raw_token TEXT NOT NULL,
  anomaly   TEXT NOT NULL,
  PRIMARY KEY (wasm_hash, raw_token)
);
CREATE TABLE interface_matches (
  wasm_hash             TEXT NOT NULL,
  sep                   INTEGER NOT NULL,
  ruleset_version       TEXT NOT NULL,
  status                TEXT NOT NULL CHECK (status IN ('match','partial','mismatch','no_spec')),
  ok_count              INTEGER NOT NULL,
  missing_json          TEXT NOT NULL,
  mismatched_json       TEXT NOT NULL,
  matched_variants_json TEXT NOT NULL,
  computed_at           TEXT NOT NULL,
  PRIMARY KEY (wasm_hash, sep, ruleset_version)
);
CREATE TABLE verifications (
  contract_id  TEXT NOT NULL,
  wasm_hash    TEXT NOT NULL,
  sep          INTEGER NOT NULL,
  tool         TEXT NOT NULL,
  tool_version TEXT NOT NULL,
  verdict      TEXT NOT NULL CHECK (verdict IN ('pass','fail','unverifiable')),
  passed       INTEGER NOT NULL,               -- 1 only when verdict = 'pass'
  clauses_json TEXT NOT NULL,
  run_at       TEXT NOT NULL,
  PRIMARY KEY (contract_id, wasm_hash, sep, tool, run_at)
);
CREATE TABLE sync_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE INDEX idx_claims_sep       ON wasm_claims (sep, wasm_hash);
CREATE INDEX idx_contracts_hash   ON contracts (current_wasm_hash);
CREATE INDEX idx_matches_sep      ON interface_matches (sep, status);
CREATE INDEX idx_versions_current ON contract_versions (contract_id) WHERE to_ledger IS NULL;
CREATE VIEW claims AS
  SELECT v.contract_id, v.wasm_hash, c.sep, c.source, c.raw_token, c.anomaly,
         (v.to_ledger IS NULL) AS current
  FROM contract_versions v JOIN wasm_claims c ON c.wasm_hash = v.wasm_hash;
```

`sync_state` keys: `last_ledger`, `network_passphrase`, `schema_version`, `rpc_url`, `ruleset_versions`.

**Invariants, enforced in SQL, not only in Go:**

- **One current version per contract.** A trigger rejects inserting a second `contract_versions` row with `to_ledger IS NULL` for the same contract.
- **Only a pass is passed.** A `CHECK` rejects `passed = 1` unless `verdict = 'pass'`.
- Write a test for each invariant that runs **raw SQL** against the database and attempts the violation directly, not only through Go code.

The API process opens the database read-only (`mode=ro`).

### 5.9 Ingestion, seeding, and sync

**RPC client** (`internal/rpc`): JSON-RPC 2.0 over HTTPS.

- Methods: `getNetwork`, `getLatestLedger`, `getLedgers` (`startLedger` or `cursor`; `limit` 1 to 200; default 200), `getLedgerEntries`.
- Before writing the client, read the current method docs on developers.stellar.org and match field names exactly.
- Configurable concurrency (default 4). Retry with exponential backoff and jitter on 429/5xx and timeouts (max 6 attempts). No retry on 4xx other than 429.

**Change extraction** (`internal/ingest`): decode each ledger's `metadataXdr` as `xdr.LedgerCloseMeta`. Walk every ledger-entry change in transaction meta and upgrade meta, and collect:

- `CONTRACT_CODE` created → new Wasm hash.
- `CONTRACT_DATA` with key `ScvLedgerKeyContractInstance` and persistent durability, created or updated → read the instance executable. It is either a Wasm hash or the Stellar Asset executable.
  - On create, insert the contract.
  - On update, compare with the stored hash. A different hash is an **upgrade**: close the old `contract_versions` row at this ledger and open a new one.
- SAC asset identity → `contracts.sac_asset`, where the instance storage exposes it.
  - If it can't be derived reliably, leave it NULL and report this at the STOP. Don't guess.

**Wasm fetch.** Fetch each new hash once with `getLedgerEntries` (`ContractCode` key). Run it through sepmeta, the claim sources, and the matcher.

- If an entry is missing or archived (`liveUntilLedgerSeq` below the latest ledger, or absent), set `parse_status='archived'` or `contracts.archived=1`.
- **Never delete claims** because an entry became archived. Keep the last known data.

**Seed and backfill** (`BackfillSource`):

- `SeedFileSource` reads a CSV of `contract_id[,wasm_hash]` (the Hubble export from §5.10 or any list). For each contract, it reads the instance entry with `getLedgerEntries`.
- A data-lake source is **out of scope for v0.1.0**. Leave the interface and an issue for it.

**Sync loop** (`sep47idx sync`):

1. Open the database and check the passphrase and schema. Read `last_ledger`. On first run, require `--seed <file>` or `--start-ledger <n>`, otherwise exit 1 with a message.
2. Call `getLedgers` once to learn `oldestLedger`. If `last_ledger + 1 < oldestLedger`, exit with `ErrRetentionGap` and a message telling the operator to seed or backfill. **Never skip ledgers silently.**
3. Fetch batches of up to 200 ledgers. For each batch, write every change and the new `last_ledger` **in one transaction**.
4. Reprocessing any ledger must be idempotent. Stellar ledgers are final, so no reorg handling is needed.
5. When caught up, re-check `getLatestLedger` before reporting `caught_up`. With `--follow`, poll every `--interval` (default 5s).
6. `--recompute` re-runs the matcher for every Wasm whose stored `ruleset_version` differs from the loaded rules. It doesn't re-fetch Wasm.
7. Lag (`latest - last_ledger`) is exposed in `/v1/health` and `sep47idx stats`.

**Required tests** (each named in the STOP F report):

- Replaying the same ledgers twice produces identical database contents. Compare a table dump.
- A crash mid-batch, simulated by cancelling the context after N writes, leaves `last_ledger` at the previous batch. A rerun completes it.
- An instance update with a new hash creates exactly one new version row and closes the old one.
- An archived code entry keeps its claims.
- The retention-gap condition returns `ErrRetentionGap` and writes nothing.

### 5.10 Phase 0: the adoption report

Phase 0 is the first real use of `pkg/sepmeta`, and **the gate for the rest of the project**.

**Mainnet (census, not a sample):**

1. The operator runs `scripts/hubble-export.sql` against Stellar's public BigQuery dataset, Hubble (project `crypto-stellar`).
   - Before writing the SQL, open the Hubble data dictionary on developers.stellar.org and confirm the exact table and column names: `contract_code` (`contract_code_hash`, `deleted`) and `snapshots.contract_data_snapshot` (`contract_id`, `contract_key_type`, ...).
   - Query 1: distinct non-deleted code hashes.
   - Query 2: current contract-instance rows (contract ID, plus the executable or Wasm hash if a column exposes it).
   - Use column pruning and a partition or date bound. These tables are billed per byte scanned.
2. If no column exposes the instance's Wasm hash, the command reads each instance with `getLedgerEntries`.

**Testnet (sample):** Hubble does not cover testnet. Scan `getLedgers` across the retention window. Collect contract instance creates and updates, stratified across the window. Target 2,000 contracts, or all of them if there are fewer.

**Command:**

```
sep47idx phase0 --network mainnet --hashes report/mainnet_hashes.csv --instances report/mainnet_instances.csv --out report/
sep47idx phase0 --network testnet --sample 2000 --out report/
```

**Outputs:** `report/<network>-raw.csv` (one row per Wasm hash) and `report/ADOPTION.md`, containing:

- contracts counted, % SAC, % Wasm, unique Wasm hashes, archived count;
- % of Wasm declaring `sep`, weighted **by unique hash** and **by contract** (both, side by side, because factory deployments skew the per-contract number);
- the count for each SEP number;
- anomalies, by type, with examples;
- % with a spec section;
- the SEP-41 matcher distribution (`match`, `partial`, `mismatch`, `no_spec`) and the OK-count histogram;
- the undeclared-gap count;
- method, limits, and, for testnet, a 95% Wilson confidence interval on the declare rate.

**Decision rule** (state the applicable row in ADOPTION.md):

| Unique mainnet Wasm hashes declaring `sep` | README headline | Phase 2 |
| --- | --- | --- |
| ≥ 5% | Declared index | Proceed after stage G |
| 1% to < 5% | Inferred tier + declared-vs-inferred gap | Proceed after stage G |
| < 1% | Interface index + adoption tracker; README "How to help" lists outreach (adding `contractmeta!(key="sep", ...)` to open-source contracts) | Deferred |

### 5.11 HTTP API (read-only, `/v1`)

General rules:

- Standard library `net/http`. JSON only.
- UTC RFC 3339 timestamps.
- Keyset pagination with an opaque, base64url `cursor`.
- `limit`: default 50, max 500.
- CORS enabled for GET.
- An optional per-IP rate limit (`--rate-limit` requests/minute, 0 = off).
- Contract IDs are validated as `C...` strkeys before any query. Wasm hashes are validated as 64 lowercase hex characters.

| Endpoint | Returns |
| --- | --- |
| `GET /v1/contracts` | Contracts currently satisfying the filters. Params: `implements` (comma list, AND), `implements_any` (OR), `tier` (`declared` \| `inferred` \| `verified` \| `protocol`, default `declared`), `kind` (`wasm` \| `sac`), `limit`, `cursor`. `/contracts` is an alias. |
| `GET /v1/contracts/{id}` | Detail: claims grouped by tier, version history, archived flag |
| `GET /v1/contracts/{id}/history` | Every Wasm version with ledger range and the claims at each |
| `GET /v1/wasm/{hash}` | Parsed meta, claims, anomalies, interface matches, contracts using it |
| `GET /v1/seps` | Counts per SEP number by tier |
| `GET /v1/stats` | Adoption numbers, the undeclared gap, anomalies, last sync |
| `GET /v1/health` | `{status, network, last_ledger, latest_ledger, lag}` |

Contract detail shape (fixed surface):

```json
{
  "contract_id": "C...",
  "network": "mainnet",
  "kind": "wasm",
  "wasm_hash": "ab12...",
  "archived": false,
  "claims": [
    {"sep": 41,
     "declared": true,
     "inferred": {"status": "match", "ruleset_version": "sep41-v0.5.2", "missing": [], "mismatched": []},
     "verified": null,
     "protocol": false,
     "source": "sep47-meta",
     "anomaly": null}
  ],
  "history": [{"wasm_hash": "ab12...", "from_ledger": 123, "to_ledger": null}],
  "parser_version": "1",
  "as_of_ledger": 456
}
```

- `verified` is `null` when not run. Otherwise it is `{"verdict", "tool", "tool_version", "run_at", "stale"}`.
- Errors always use this shape: `{"error":{"code":"not_found|bad_request|rate_limited|internal","message":"..."}}`, with the matching HTTP status.
- Every response that carries `verified` data includes, in the docs, the caveat that **a pass means the suite's checks passed; it is not a security audit.**

### 5.12 CLI

```
sep47idx phase0 ...                       (§5.10)
sep47idx sync    --network <n> [--seed f] [--start-ledger n] [--follow] [--recompute]
sep47idx serve   --network <n> --addr :8080 [--rate-limit n]
sep47idx query   --implements 41 [--implements-any ...] [--tier declared] [--kind wasm] [--json] [--csv]
sep47idx contract <C...> [--history] [--json]
sep47idx wasm <hash> [--json]
sep47idx stats [--json]
sep47idx gap --sep 41 [--json]            # undeclared gap, labelled "inferred"
sep47idx verify <C...> --sep 41           # Phase 2, testnet only
sep47idx version
```

Common flags: `--db` (default `./data/<network>.db`), `--rpc-url`, `--network` (`mainnet` | `testnet`), `--json`. Each flag also has an env var, prefixed `SEP47IDX_` (`SEP47IDX_RPC_URL`, ...); flags override env.

Exit codes: 0 = ok, 1 = error, 2 = not found.

### 5.13 Phase 2: verified tier (optional, stage J)

`internal/verify`:

```go
type Verifier interface {
    Name() string
    Verify(ctx context.Context, contractID string, sep int) (Verification, error)
}
```

The adapter runs `npx soroban-guard@<pinned> <contractID> --format json`, with a timeout of 10 minutes.

- Before writing the adapter, read soroban-guard's current README and a real JSON report. Confirm the output shape and exit codes.
- Exit-code mapping: 0 → `pass`, 1 → `fail`, 2 → `unverifiable`. Store every clause.
- **Refuse to run unless the configured network passphrase is the testnet passphrase.** Never pass `--allow-non-testnet-write`; grep the code in CI to prove it never appears.
- `OWNER_SECRET` and `SPENDER_SECRET` are passed through from the operator's environment to the child process only. Never log them, write them to disk, or store them in the database.
- The verifier only runs when an operator calls the `verify` command; nothing triggers it automatically. Document that native XLM cannot reach `pass`, because the SAC blocks `burn`.

### 5.14 Docs site

MkDocs Material lives in `docs/` and is published to GitHub Pages by `docs.yml`. Pages:

1. **Introduction**: the gap, the three tiers, the Phase 0 numbers with their date.
2. **Trust tiers**: exact definitions from §5.6, worked examples from the golden fixtures.
3. **How it works**: the data flow, sync, upgrades, archival, the retention gap.
4. **API reference**: every endpoint with a real request/response captured from a real run.
5. **CLI reference**: every command, with real output.
6. **For contract authors**: how to add `contractmeta!(key="sep", val="41")`, and how to check the result with `sep47idx wasm`.
7. **Writing a rule file**: schema, review rules, PR checklist.
8. **Self-hosting**: Docker, env vars, seeding, backups.
9. **Adoption report**: generated from `report/ADOPTION.md`.
10. **Contributing**: links to CONTRIBUTING.md.

Writing style:

- Short direct sentences. No filler words ("seamless", "robust", "powerful", "leverage").
- Every number comes from a real run, with a date.
- Every command shown was run.

---

## 6. Git workflow — non-negotiable

1. The first commit is the scaffold (`chore: scaffold repository`). After it, **never `git add .` or `git add -A`**; stage named files only.
2. One commit per logical unit: one function with its tests, one migration, one endpoint, one doc page.
3. **Push immediately after every commit.** Never batch local history.
4. Conventional commits: `type(scope): description`, lowercase and imperative. Types: `feat fix test docs chore ci refactor perf build`. Scopes: `sepmeta rpc ingest claims match store sync api cli verify phase0 docs repo`.
5. Never force-push or rewrite pushed history.
6. Never commit secrets, `.env` files, `data/*.db`, or BigQuery credentials. `.gitignore` covers all of these from the scaffold commit.
7. A correction to something already pushed is its own commit, with the evidence (what was tested and the result) in the body. Never fold it into unrelated work.

---

## 7. Build sequence

Each bullet is at least one commit. The **STOP** points are the operator's checkpoints.

- **Tight** (report in full): stages B, C, F, J.
- **Loose** (one batch report): stages D, E, G, H, I, K.

### Stage A: scaffold and prior art

1. `chore: scaffold repository`: tree from §2 (empty dirs with `.gitkeep`), `.gitignore`, LICENSE, go.mod with the pins from §3, Makefile skeleton.
2. `ci: add lint test fuzz-smoke build jobs`.
3. `docs(repo): record prior-art search`. Search GitHub and the Wave issue board for: `SEP-47`, `contractmetav0`, `"contract meta"`, `"interface discovery"`, `implements=`, `soroban indexer`. Record what each result does and how it differs, in `docs/prior-art.md`.
   - Must cover StellarFoundry/stellar-contract-platform, SoroTrail, soroban-guard, Stardex, Sierpe, and the Python stellar-sdk `ContractMeta.supported_seps()`.
   - **If anything already does network-wide SEP-47 indexing, stop and report it immediately.**

### Stage B: `pkg/sepmeta` — tight

4. Wasm section reader + limits + tests.
5. Fixture contracts + `scripts/build-fixtures.sh` + committed `.wasm` files.
6. `DecodeMeta` + tests against fixtures.
7. `ParseSEP47` + every vector from §5.4.
8. `DecodeSpec` + `Functions` + tests.
9. Four fuzz targets + seeds; run each for at least 10 minutes locally and paste the summary.
10. `doc.go` with a runnable `Example` test.

**STOP B.** Show the fuzz run summaries, the vector table output, and the result of reading `multi_sep.wasm` (it must yield `[41 40]` with `EntryCount 2` and no anomaly).

### Stage C: Phase 0 — tight, project gate

11. `internal/rpc`: `getLedgerEntries`, `getLedgers`, `getLatestLedger`, `getNetwork` + tests against recorded responses.
12. `internal/match` + `rules/sep-0041.json` + `rules/schema.json`. Phase 0 needs the matcher. Diff the rule file against the live SEP-41 text first.
13. `scripts/hubble-export.sql`, with the confirmed table and column names.
14. `internal/phase0` + the `phase0` subcommand.
15. **Operator action:** run the Hubble export and supply the CSVs. Ask for this when you reach step 15.
16. Run Phase 0 on mainnet and testnet. Commit `report/`.

**STOP C — GATE.** Report the ADOPTION.md numbers and the decision row. The operator confirms the headline before stage D. If the numbers disagree with Hubble's own row counts by more than 1%, explain why before anything else.

### Stage D: store and seeded ingest — loose

17. Migrations + store package + invariant triggers + raw-SQL invariant tests.
18. `ClaimSource`, `SEP47MetaSource`, `HomeDomainSource` stub + tests.
19. `SeedFileSource` + `sync --seed` (seed-only path) writing contracts, wasm, claims and matches.
20. A check that the stored totals equal the Phase 0 numbers for the same seed. Paste the output.

### Stage E: inferred tier — loose

21. Undeclared-gap query + `gap` command.
22. `--recompute`, with a test that bumping `ruleset_version` recomputes only the stale rows.

**STOP D/E** (one report).

### Stage F: incremental sync — tight

23. `LedgerSource` over `getLedgers` + change extraction + tests on recorded ledger meta.
24. Sync loop + batching + idempotency + crash resume + retention-gap error.
25. Upgrade detection + archival handling.
26. All five tests from §5.9.
27. Integration test (build tag `integration`): follow testnet for 30 minutes from the current tip with no errors and lag under 10 ledgers at the end.
28. **Operator action:** deploy a fixture token to testnet, then upgrade it to `token_partial.wasm`. Show the new version row and the changed inferred status.

**STOP F.** Read in full: paste the five §5.9 test names and their source, plus the step 28 evidence.

### Stage G: API and CLI — loose

29. Each endpoint in §5.11, one commit per endpoint, with handler tests (pagination, AND vs OR, tier filters, 404, bad input).
30. Each CLI command in §5.12.
31. Benchmark: a synthetic database with 1M claim rows. Record `implements=41` p50/p95 query time in `docs/`. Materialize the `claims` view only if p95 exceeds 200 ms, and show the before/after numbers.

### Stage H: hardening and release prep — loose

32. Dockerfile: multi-stage build, distroless, non-root, `CGO_ENABLED=0`.
33. `gosec` clean; dependency review; `govulncheck` clean.
34. `integration.yml` (manual and nightly testnet).

**STOP G/H.**

### Stage I: Wave readiness — loose, then STOP

Before writing the README, fetch three of the most-starred repos in the Wave's approved-repos list (drips.network/wave/stellar/repos). Match their README pattern; don't assume it.

35. `README.md`, in this order:
    - name + one-line description;
    - badges (CI, license, Go Reference, release);
    - the plain-English description from §1;
    - the three questions the index answers;
    - Phase 0 numbers with date;
    - a quick start (Docker + curl `implements=41`);
    - architecture in five lines plus a link to the docs;
    - the trust-tier table;
    - prior art and credits (SEP-47/48 authors, SoroTrail, soroban-guard);
    - a "How to help";
    - contributing;
    - a maintainer table (GitHub `@ciscokwiz`, Discord `ciscokwiz`);
    - contributors image (`contrib.rocks`);
    - license.
36. `CONTRIBUTING.md`: setup, `make` targets, commit rules from §6, how Wave issues are labelled and assigned, the rule-file PR checklist, the versions table.
37. `SECURITY.md`:
    - the private reporting route (GitHub private vulnerability reporting);
    - scope: parser, API and sync;
    - a statement that the project is unaudited;
    - the statement that the verified tier is not a security audit.
38. `CODE_OF_CONDUCT.md`, issue templates, PR template.
39. Docs site, one commit per page (§5.14), deployed by `docs.yml`.
40. `scripts/repo-settings.sh`, which the operator runs:
    - branch protection on `main`: PRs required, 1 approval, required checks `lint`, `test`, `fuzz-smoke`, `build` (match the job names in ci.yml exactly), no force-push;
    - topics: `stellar`, `soroban`, `sep-47`, `sep-41`, `indexer`, `smart-contracts`, `go`, `drips-wave`.
41. `scripts/create-issues.sh`: one `gh issue create` per planned issue, created in a single run.
    - Labels: `Stellar Wave`, plus one of `difficulty/trivial`, `difficulty/medium`, `difficulty/high`, plus an area label.
    - Body sections: Summary, Why, Acceptance Criteria (checkboxes), Tech Stack, Pointers (file paths).
    - At least 25 issues, every one real remaining work: rule files for SEP-50, SEP-56, SEP-40 (each reviewed against its SEP text), parser vectors, fuzz seeds, API filters, CSV output, an OpenAPI document, a data-lake `BackfillSource`, RPC failover, adoption-page charts, docs fixes.
    - **No busywork issues and no issues you already solved.**
42. Release `v0.1.0`:
    - CHANGELOG entry;
    - a tag;
    - a GitHub release body with the Phase 0 numbers, binaries for linux/darwin amd64/arm64, and a Docker image tag.
43. `docs/wave/APPLICATION.md`, the submission package:
    - the one-paragraph project description;
    - the "planned issues" summary grouped by area, generated from the real `create-issues.sh` list;
    - a links checklist: repo, docs site, release, Docker image, demo video (operator records), live API URL if hosted;
    - a statement of what is built vs planned, accurate on the day.
44. `docs/wave/DEMO.md`: a demo script of real commands. The flow:
    - `phase0` summary;
    - `query --implements 41 --tier declared` vs `--tier inferred`;
    - `gap --sep 41`;
    - `contract <id> --history` on the step 28 upgraded contract;
    - `curl /v1/stats`.
    - Every output in the script is pasted from a real run.

**STOP I.** The operator checks:

- every link opens, in a logged-out browser;
- the docs site renders;
- the demo commands reproduce;
- the project is **not already** in the approved-repos list (search it live).

The operator then runs `repo-settings.sh` and `create-issues.sh`, records the demo, installs the Drips Wave GitHub App on the org, and submits. **You never submit the application yourself, and never act in the operator's Drips or GitHub account beyond the scripts they run.**

### Stage J: verified tier — tight, only if Phase 0 is ≥ 1%

45. Verifier interface + soroban-guard adapter + the testnet-only guard + secrets handling + tests with a fake child process.
46. Stale handling after upgrade + API and CLI output.
47. **Operator action:** a keyed run against `token_full_sep.wasm` deployed on testnet. Store the result; show the API output.

**STOP J.** Read in full: the passphrase guard code and test, the secret-handling code, and the grep proving `--allow-non-testnet-write` never appears.

### Stage K: maintainer cadence

48. `MAINTAINING.md`. It must cover:
    - **Weekly:**
      - sync lag check;
      - a check that the follow process is actually advancing: `last_ledger` must change between two reads 60 seconds apart. A green `/v1/health` alone does not prove this; the process can be stuck while health reports ok.
      - triage new issues and PRs.
    - **Monthly:**
      - re-check SEP-41 and SEP-47 text for changes, and bump the rule file if needed;
      - bump the go-stellar-sdk and modernc sqlite patch versions, with tests;
      - regenerate the adoption report.
    - **Each Wave:**
      - refill issues only with real work;
      - review PRs within 48 hours.
    - **Hard rule:** never close contributor-facing issues yourself to look active, and never pad the backlog. Ask: would this be worth doing if nobody were watching the repo?
    - A fixed report format, where "nothing to report" is a normal, frequent outcome.

---

## 8. Coding standards

- `gofmt` and `golangci-lint` clean. No `//nolint` without a reason on the same line.
- No `panic` outside `main` setup and tests. No ignored errors (`_ = f()` only with a comment saying why).
- Every exported identifier has a doc comment that says **why**, not just what.
- Every I/O call takes a context and has a timeout.
- Every length read from untrusted bytes is bounds-checked before allocation.
- SQL is parameterized only. The API opens the database read-only.
- Logs are structured (`slog`). Never log Wasm bodies, secrets, or full RPC responses. Log hashes and IDs.
- Ledger numbers are `uint32` in Go and `INTEGER` in SQLite. Convert at the store boundary only.
- Floats only for reported percentages and confidence intervals, never for ledger numbers, counts, or IDs.
- Tests don't touch the network unless they carry the `integration` build tag.

---

## 9. Constraints checklist

Run through this before declaring any stage done, and in full before STOP I. Each item must be independently checkable.

- [ ] `go build` works with `CGO_ENABLED=0` on linux/darwin amd64/arm64.
- [ ] `go.mod`'s `go` directive is the floor and `toolchain` is the build pin; the two differ for a stated reason.
- [ ] Every fixture and vector in §5.4 has a test, and `multi_sep.wasm` produces no anomaly.
- [ ] All four fuzz targets ran at least 10 minutes with no crash; regression cases are committed.
- [ ] No code path executes Wasm or simulates a contract call (grep for `simulateTransaction`; it appears only in the `HomeDomainSource` stub comment).
- [ ] Both SQL invariants have raw-SQL violation tests.
- [ ] All five §5.9 sync tests exist and pass.
- [ ] The retention-gap case exits non-zero and writes nothing.
- [ ] No tier is mixed: a test asserts a SAC never appears under `tier=declared`.
- [ ] `rules/sep-0041.json` was diffed against the live SEP-41 text on the day it was committed (date recorded in its `source` field).
- [ ] Every API endpoint has tests for pagination, bad input and 404.
- [ ] `--allow-non-testnet-write` appears nowhere in the codebase.
- [ ] No secret, `.env`, database file or credential is in git history (`git log -p | grep -i secret` is reviewed).
- [ ] Every number in README, docs and APPLICATION.md comes from a committed report or a pasted run, with a date.
- [ ] Every command in DEMO.md and the docs was run and its output pasted.
- [ ] Branch-protection required checks match the ci.yml job names exactly.
- [ ] Every issue in `create-issues.sh` is real, unsolved work with acceptance criteria.
- [ ] README states what is built vs planned, accurately, on the release date.
