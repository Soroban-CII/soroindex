# Threat model

soroindex makes strong safety claims: it never executes contract Wasm, it bounds every length it reads from
untrusted bytes, and it never mixes trust tiers. This page checks each claim against the code and names the
function or test that enforces it. The scope of this page is limited to controls and gaps suitable for public review.

## Method

- Reviewed against the source at commit `305de6d` on **26 November 2026**. The first pass read `98a6c41` on
  25 November 2026; this pass re-read every entry affected by `#208`, which landed in between and changed the
  parser: a second entry point (`ReadCustomSectionsAt`), two structure-aware fuzz targets, and over-depth XDR
  reclassified from `ErrMalformed` to `ErrLimit`. The affected entries are T1.2, T1.3, T1.4 and T1.5. The review
  read the code, not `CLAUDE.md`: where the design document or another docs page describes a control the code
  does not implement, that is recorded as a gap.
- Existing mitigations cite code or tests. The review ran `go test ./...` on 26 November 2026 at `305de6d`.
- Confidential findings are pending private handling. Tracking for some public gaps is still pending.
- Report a vulnerability through the private route in
  [SECURITY.md](https://github.com/Soroban-CII/soroindex/blob/main/SECURITY.md). **The project is unaudited.**

### What each entry says

| Field | Meaning |
| --- | --- |
| Attacker | Who can do this, and what they must already control. |
| Impact | What goes wrong if it works. |
| Mitigation | What the code does today. Nothing here is aspirational. |
| Enforced by | The file and identifier that implements or tests it. |
| Residual risk | What is still possible with the mitigation in place. |

### Assets

1. **The index's integrity**: the claims, matches and version history it reports. The project's whole value is
   that a reader can trust the tier label.
2. **Availability of the read-only API** to its consumers.
3. **The operator's host**: the sync process parses hostile bytes from the network on it.
4. **Release artifacts**: binaries and the container image that operators run.
5. **Operator secrets**: RPC credentials and, when the verified tier is built, test-account keys.

### Out of scope

- The security of the contracts the index describes. The index reports declarations and interface shape; neither
  is a safety statement. See [Trust tiers](trust-tiers.md).
- Source or build verification of contracts (SEP-55 style), an explicit non-goal.
- Third-party RPC providers' own security. Their output is treated as hostile; see
  [RPC provider trust](#2-rpc-provider-trust).
- The deployed public instance's infrastructure. No instance is hosted yet.

---

## 1. Untrusted Wasm and XDR

`pkg/sepmeta` and `internal/ingest` parse bytes that any account on the network can publish for a few XLM. The
parser is the largest attack surface in the project.

### T1.1 Contract Wasm executed or interpreted

- **Attacker**: anyone who can deploy a contract.
- **Impact**: arbitrary code execution on the indexer host. This is the worst case for the project.
- **Mitigation**: nothing in the codebase executes, interprets or simulates Wasm. `ReadCustomSections` walks
  section headers and copies only the custom sections it was asked for; `if id != customSectionID { continue }`
  skips every other section, including `code`, without decoding it. No Wasm runtime is in the dependency graph,
  and `simulateTransaction` appears in exactly one place: a comment on the unregistered `HomeDomainSource` stub.
- **Enforced by**: `pkg/sepmeta/wasm.go` (`ReadCustomSections`, `customSectionID`);
  `internal/claims/claims.go` (`HomeDomainSource.NeedsInvocation`, `Registry.Active`);
  `internal/claims/claims_test.go` (`TestHomeDomainSourceIsAStub`, `TestInvocationGuard`).
  `go list -deps ./...` names no Wasm runtime (checked 25 November 2026).
- **Residual risk**: the `--allow-invocation` flag exists in `cmd/sep47idx/cmd_sync.go` for a future source that
  would simulate a call. No shipped source sets `NeedsInvocation() == true` except the stub, which returns
  `ErrNotImplemented`, so the flag enables nothing today. The guard is tested, not merely documented.

### T1.2 Oversized module or section exhausts memory

- **Attacker**: anyone who can deploy a contract, or an RPC provider returning a larger body than the network
  would accept.
- **Impact**: the sync process is killed by the OOM killer; the index stops advancing.
- **Mitigation**: `ReadCustomSections` checks `len(wasm)` against `MaxWasmBytes` (1 MiB) before it looks at the
  header, caps each requested section's concatenated payload at `MaxSectionBytes` (256 KiB), and caps the number
  of sections at `MaxSections` (10,000). A zero field means "use the default", never "unlimited". Exceeding a
  limit returns a `*LimitError` naming the field. `ReadCustomSectionsAt`, the `io.ReaderAt` entry point added by
  `#208`, applies the **same** `Limits` through the same `withDefaults`: it rejects a `size` over
  `MaxWasmBytes` before reading a byte, counts sections against `MaxSections`, and checks the running
  concatenated total against `MaxSectionBytes`. It never holds the module, so its own overhead does not scale
  with module size.
- **Enforced by**: `pkg/sepmeta/wasm.go` (`Limits`, `DefaultLimits`, `withDefaults`, `LimitError`);
  `pkg/sepmeta/wasm_test.go` (`TestReadCustomSectionsLimits`, including the subtests
  `module over MaxWasmBytes is rejected before parsing`, `repeated sections are limited by their concatenated
  size`, `section count over MaxSections is rejected` and `zero Limits fields take their defaults instead of
  allowing nothing`); `pkg/sepmeta/wasm_at.go` (the `size > int64(lim.MaxWasmBytes)`, `count >= lim.MaxSections`
  and `total > lim.MaxSectionBytes` checks) and `pkg/sepmeta/wasm_at_test.go`
  (`TestReadCustomSectionsAtLimits`, `TestReadCustomSectionsAtAllocationBound`,
  `TestReadCustomSectionsAtSkipsUnrequestedBytes`). `make bench-budget` holds every
  `pkg/sepmeta` benchmark to a committed allocation budget (`pkg/sepmeta/testdata/alloc-budgets.txt`,
  84 benchmarks), which is the project's regression detector for an allocation that grows with input size.
- **Residual risk**: the limits are **not configurable** at the command line. `DefaultLimits()` is hard-coded at
  every call site (`cmd_sync.go`, `cmd_phase0.go`, `cmd_inspect.go`). `CLAUDE.md` §5.1 says the total-size limit
  is "configurable (default 1 MiB)"; it is not, at this commit. An operator who meets a legitimate module above
  1 MiB must rebuild. Tracked as
  [#139](https://github.com/Soroban-CII/soroindex/issues/139) (open, not implemented at `305de6d`).
  Separately, neither entry point validates the `Limits` it is handed — `withDefaults` fills a zero field but
  nothing rejects an absurdly large one. No in-tree caller passes anything but `DefaultLimits()`, so this is a
  hazard for a future caller rather than a live weakness, and `#139` is where the validation belongs.

### T1.3 A declared length larger than the input

- **Attacker**: anyone who can deploy a contract.
- **Impact**: a `make([]byte, n)` on an attacker-chosen `n` is an instant OOM, or a slice out of range is a panic.
- **Mitigation**: `reader.sized` reads the LEB128 length, compares it with `r.remaining()` as `int64`, and only
  then returns a **subslice** of the input. It never allocates. `reader.u32` rejects a LEB128 longer than five
  bytes and a fifth byte whose high nibble is set, so the length cannot exceed 32 bits.
- **Enforced by**: `pkg/sepmeta/wasm.go` (`reader.sized`, `reader.u32`, `reader.remaining`);
  `pkg/sepmeta/wasm_test.go` (`TestReadCustomSections` subtests `max u32 section size on short input is
  truncated, not allocated`, `LEB128 with a fifth byte over 32 bits is malformed`, `section size past end is
  truncated`) and `TestLEB128u32`. The `io.ReaderAt` path cannot subslice an input it does not hold, so it
  reads through a bounded `streamReader` (`streamReader.readAt`) and allocates a payload only after its
  declared length has been checked against both the remaining size and `MaxSectionBytes`:
  `pkg/sepmeta/wasm_at_test.go` (`TestReadCustomSectionsAtShortRead`,
  `TestReadCustomSectionsAtAllocationBound` — on a 1 MiB module it allocates the requested payloads plus under
  16 KiB).
- **Residual risk**: none known for this path. It is covered by `FuzzReadCustomSections`, which since `#208`
  also checks the two entry points against each other on every input, so a bounds bug in either that the other
  does not share is a fuzz failure.

### T1.4 Repeated custom sections used to multiply work

- **Attacker**: anyone who can deploy a contract.
- **Impact**: a module with thousands of `contractmetav0` sections could grow one buffer without bound, if
  concatenation were unbounded. SEP-47 requires concatenation, so it cannot simply be refused.
- **Mitigation**: repeated sections are appended in file order, and the **running total** is checked against
  `MaxSectionBytes` before each append. Sections whose names were not requested are skipped without being
  copied, and do not count against the limit.
- **Enforced by**: `pkg/sepmeta/wasm.go` (the `total := len(out[name]) + len(body)` check);
  `pkg/sepmeta/wasm_test.go` (`repeated sections are limited by their concatenated size`, `unrequested sections
  do not count against MaxSectionBytes`); `pkg/sepmeta/wasm_at.go` (the same running total) and
  `pkg/sepmeta/wasm_at_test.go` (`TestReadCustomSectionsAtMatchesReferenceOnFixtures`,
  `TestReadCustomSectionsAtMatchesReferenceOnCraftedModules`), which pin the two readers to the same
  concatenation order and the same limit.
- **Residual risk**: none known.

### T1.5 Malformed or deeply nested XDR in a meta or spec section

- **Attacker**: anyone who can deploy a contract.
- **Impact**: stack exhaustion from recursive XDR, an unbounded allocation, or a panic that stops sync.
- **Mitigation**: `decodeStream` decodes back-to-back XDR values with
  `xdr.DecodeOptions{MaxDepth: lim.MaxXDRDepth, MaxInputLen: inputLen}`, so nesting is bounded at 500 and every
  declared length is bounded by the bytes that remain. A failure returns the entries decoded so far plus an
  error, recorded as a parse status and never a crash. Since `#208` the error distinguishes the two causes: an
  entry nested deeper than `MaxXDRDepth` is `ErrLimit` with a `*LimitError` naming `MaxXDRDepth`, while a
  cut or corrupt entry stays `ErrTruncated` or `ErrMalformed`. That matters because the two call for different
  responses — a limit says the input exceeded a configured bound, malformed says the bytes are wrong — and
  before `#208` a cut entry under a small `MaxXDRDepth` was misreported as malformed.
- **Enforced by**: `pkg/sepmeta/meta.go` (`decodeStream`, `decodeOptions`, `completesWithPadding`, and the
  `LimitError{Limit: "MaxXDRDepth"}` branch); `pkg/sepmeta/meta_test.go` (`TestDecodeMeta`,
  `TestDecodeMetaEveryPrefix`, `TestDecodeMetaLimits`); `pkg/sepmeta/spec_test.go`
  (`TestDecodeSpecEveryPrefix`, `TestDecodeSpecErrors`). `FuzzDecodeMeta`, `FuzzDecodeSpec` and, since `#208`,
  the structure-aware `FuzzDecodeMetaStructured` and `FuzzDecodeSpecStructured` each run 30 s in the CI
  `fuzz-smoke` job (`make fuzz-smoke`); the structured targets build valid streams from fuzzer choices and
  check decode, re-encode, `Functions` and truncation invariants, which is how the misreported-truncation bug
  above was found. One regression case is committed
  (`pkg/sepmeta/testdata/fuzz/FuzzDecodeSpecStructured/7248f90c35232d6b`).
- **Residual risk**: the depth and length bounds are enforced by the pinned `go-stellar-sdk` decoder. An SDK
  regression would weaken them; the pin and `govulncheck` (`.github/workflows/security.yml`) are the only
  defence. `TestDecodeMetaEveryPrefix` and `TestDecodeSpecEveryPrefix` decode every prefix of a fixture, and the
  structured fuzz targets re-encode what they decode, which together are the cheapest regression detectors the
  project has for this.

### T1.6 Hostile meta values copied into every row and response

- **Attacker**: anyone who can deploy a contract with a 4 KiB `sep` value or a 7-digit token.
- **Impact**: database and API amplification: a long raw token stored on every claim row and echoed in every
  response.
- **Mitigation**: a `sep` value over 4,096 bytes is not split at all; it becomes one token with the `oversized`
  anomaly, and its raw text is capped at 64 bytes plus an ellipsis, on a rune boundary. Tokens over six digits
  are `oversized` and produce no SEP.
- **Enforced by**: `pkg/sepmeta/sep47.go` (`maxValueBytes`, `maxRawBytes`, `maxDigits`, `capRaw`, `classify`);
  `pkg/sepmeta/sep47_test.go` (`TestOversizedRawIsCapped`, subtests `raw text is capped at 64 bytes plus an
  ellipsis` and `cap does not split a multi-byte character`), `TestParseSEP47Rules`, `TestVectors`.
- **Residual risk**: a contract can still produce many distinct short anomalous tokens, one row each in
  `parse_anomalies`. The table is keyed `(wasm_hash, raw_token)`, so the per-Wasm row count is bounded by the
  distinct tokens in a 4 KiB value — a few hundred, not unbounded.

### T1.7 Non-UTF-8 or confusable section names

- **Attacker**: anyone who can deploy a contract.
- **Impact**: a name that decodes differently in Go than in the Soroban toolchain could make the index read the
  wrong section, or a non-UTF-8 name could propagate into JSON.
- **Mitigation**: a custom section's name is validated with `utf8.Valid` before use; an invalid name is
  `ErrMalformed` for the whole module. Names are matched by exact equality against the requested list, so a name
  that merely starts with `contractmetav0` does not match.
- **Enforced by**: `pkg/sepmeta/wasm.go` (`utf8.Valid(nameBytes)`, `want[string(nameBytes)]`);
  `pkg/sepmeta/wasm_test.go` (`non-UTF-8 custom name is malformed`, `section named by prefix only does not
  match`).
- **Residual risk**: meta **values** are not required to be UTF-8 (`MetaEntry` documents this). They reach
  `wasm.meta_json`, where `encoding/json` would replace invalid bytes with U+FFFD. That alters what is reported
  but cannot corrupt a claim: SEP numbers come from an ASCII-digit check (`isASCIIDigits`), not from the
  rendered JSON.

### T1.8 Hostile ledger metadata and ledger entries

- **Attacker**: an RPC provider, or anyone whose transaction appears in a ledger the indexer reads.
- **Impact**: an unbounded base64 string or a deeply nested XDR value in `metadataXdr` could exhaust memory
  during sync.
- **Mitigation**: `UnmarshalBase64` checks the base64 string length against `MaxBase64Len` **before** decoding,
  then calls `xdr.SafeUnmarshalBase64WithOptions`, which overrides `MaxInputLen` to the input size and bounds
  nesting with `MaxDepth` (default 500), and which rejects trailing bytes. Each call site sets a limit sized to
  its entry type: 2 MiB of base64 for contract code (`codeLimits`), 16 MiB for instances, 64 MiB for ledger
  metadata.
- **Enforced by**: `internal/ingest/instance.go` (`XDRLimits`, `withDefaults`, `UnmarshalBase64`,
  `DecodeLedgerEntryDataBase64`); `internal/ingest/fetch.go` (`codeLimits`); `internal/ingest/ledgers.go`
  (`LedgerSource.metaLimits`); `internal/ingest/instance_test.go` (`TestUnmarshalBase64Limits`);
  `internal/ingest/fuzz_test.go` (`FuzzExtractContractFacts`).
- **Residual risk**: 64 MiB of base64 per ledger is a large allowance, chosen to accept real mainnet ledgers.
  With the RPC client's default concurrency of 4, several of those can be in flight at once. Memory use under a
  hostile provider is bounded but not small; this is the same bound as T2.1 and is not configurable per
  deployment (see [#139](https://github.com/Soroban-CII/soroindex/issues/139)).

### T1.9 XDR from a future protocol

- **Attacker**: nobody — a protocol upgrade, not an adversary. Listed because the failure mode matters.
- **Impact**: silently mis-decoded ledger data would put wrong facts in the index, which is worse than stopping.
- **Mitigation**: when the pinned SDK cannot decode a ledger, sync stops with `ErrUnknownXDR` and a message
  naming the ledger, the protocol it closed under (read from the raw header bytes) and the pinned SDK version.
  Nothing from that batch is written.
- **Enforced by**: `internal/ingest/protocol.go`; `internal/ingest/protocol_test.go`
  (`TestUnknownXDRNamesLedgerProtocolAndSDK`), which patches a recorded testnet ledger's `StellarValueType`.
- **Residual risk**: the index stops advancing until an operator upgrades the SDK. That is the intended
  trade-off: a stale index that says so beats a wrong one.

---

## 2. RPC provider trust

The indexer has exactly one source of truth: the configured JSON-RPC endpoint. A hostile or compromised provider
is the strongest realistic attacker against the index's integrity.

### T2.1 Oversized response body

- **Attacker**: the configured RPC provider, or a network attacker who can MITM the connection.
- **Impact**: memory exhaustion of the sync or serve process.
- **Mitigation**: a declared `Content-Length` over `MaxResponseBytes` (256 MiB) fails before any of the body is
  read; a chunked or mis-declared body is stopped by a `LimitReader` at the cap plus one byte. An over-size
  response is **not** retried, because the same request would get the same answer. At most
  `Concurrency × (MaxResponseBytes + 1)` bytes of body are held at once.
- **Enforced by**: `internal/rpc/client.go` (`Client.attempt`, `ErrResponseTooLarge`, `Config.MaxResponseBytes`);
  `internal/rpc/hostile_test.go` (`TestHugeDeclaredBodyFailsBeforeRead`, `TestHugeChunkedBodyIsCapped`).
- **Residual risk**: 256 MiB × 4 concurrent attempts is about 1 GiB of worst-case body memory. That is the
  documented bound, not a comfortable one for a small host. See
  [#139](https://github.com/Soroban-CII/soroindex/issues/139).

### T2.2 Deeply nested JSON

- **Attacker**: the RPC provider.
- **Impact**: stack exhaustion in `encoding/json`, which recurses per nesting level.
- **Mitigation**: `checkDepth` scans the raw bytes once, without allocating, skipping string contents, and
  rejects nesting deeper than `MaxJSONDepth` (64) **before** `json.Unmarshal` runs.
- **Enforced by**: `internal/rpc/client.go` (`checkDepth`, `ErrProtocol`); `internal/rpc/hostile_test.go`
  (`TestDeepNestingRejected`, `TestCheckDepth`, `TestBracketsInStringsAreNotNesting`).
- **Residual risk**: none known.

### T2.3 Invalid UTF-8 in a response

- **Attacker**: the RPC provider.
- **Impact**: `encoding/json` silently replaces invalid UTF-8 with U+FFFD, so a mangled string could be stored as
  if the network had contained it.
- **Mitigation**: the whole body is checked with `utf8.Valid` and rejected as `ErrProtocol` before decoding.
- **Enforced by**: `internal/rpc/client.go` (`utf8.Valid(raw)`); `internal/rpc/hostile_test.go`
  (`TestInvalidUTF8Rejected`).
- **Residual risk**: none known.

### T2.4 Slow trickle (a response that never ends)

- **Attacker**: the RPC provider.
- **Impact**: sync hangs indefinitely while holding a connection and a partial buffer.
- **Mitigation**: every attempt runs under `context.WithTimeout(ctx, c.cfg.Timeout)` (default 30 s), which covers
  reading the body, not only the headers. The attempt fails and is retried up to `MaxAttempts` (6) with
  exponential backoff and full jitter.
- **Enforced by**: `internal/rpc/client.go` (`Client.attempt`, `Client.backoff`, `sleepCtx`);
  `internal/rpc/hostile_test.go` (`TestSlowTrickleTimesOut`).
- **Residual risk**: a provider that is merely slow, inside the timeout, can hold sync back indefinitely without
  erroring. Lag is visible in `/v1/health` and `sep47idx stats`; detecting a stuck follower is a documented
  weekly check in `MAINTAINING.md`, not an automatic alarm.

### T2.5 Wrong network

- **Attacker**: the provider, a DNS or MITM attacker, or an operator misconfiguration.
- **Impact**: testnet facts written into a mainnet index, which would silently corrupt every answer.
- **Mitigation**: three independent checks. `sep47idx sync` and `serve` call `getNetwork` at start-up and exit if
  the passphrase differs from the configured network. `store.Open` records the passphrase in `sync_state` and
  refuses a database that holds a different one (`ErrNetworkMismatch`). Each network lives in its own file.
- **Enforced by**: `cmd/sep47idx/cmd_sync.go` and `cmd/sep47idx/cmd_serve.go` (the `n.Passphrase !=
  cfg.Network.Passphrase` checks, also in `multiNetworkHandler`); `internal/store/store.go` (`Open`,
  `ErrNetworkMismatch`, `KeyNetworkPassphrase`); `internal/config/config.go` (`NetworkByName`, the passphrase
  constants, checked against developers.stellar.org on 2026-10-24).
- **Residual risk**: `getNetwork` is checked once at start-up. A provider that switches networks mid-run is
  caught later, and only indirectly, by the reset detection in T2.7.

### T2.6 Response/request confusion

- **Attacker**: the provider, or a MITM.
- **Impact**: the answer to one query attributed to another, mixing ledgers or hashes.
- **Mitigation**: each request carries a monotonic id and the response id must match exactly; a mismatch is
  `ErrProtocol`. A reply with neither `result` nor `error`, or a `null` result, is also `ErrProtocol`.
- **Enforced by**: `internal/rpc/client.go` (`Client.nextID`, the `string(r.ID) != strconv.FormatUint(id, 10)`
  check); `internal/rpc/client_test.go`.
- **Residual risk**: envelope checks establish that the reply corresponds to the request. Ledger history
  consistency is covered in T2.7.

### T2.7 Withheld, skipped or rewritten history

- **Attacker**: the provider.
- **Impact**: contracts missing from the index, or an upgrade never recorded, with no sign that anything is
  wrong. For an index whose purpose is completeness, a silent gap is the worst integrity failure after a wrong
  tier label.
- **Mitigation**: the loop never skips ledgers. A batch that is not the consecutive run requested is
  `ErrLedgerGap`. A resume point older than the node's retention window is `ErrRetentionGap`, and nothing is
  written. A stored `last_ledger` more than 17,280 ledgers ahead of the node's tip, or a stored
  `ledger_checkpoint` hash that no longer matches the node's history, is `ErrNetworkReset`, and sync refuses
  before writing. `getLedgers` is re-checked before `caught_up` is reported.
- **Enforced by**: `internal/ingest/ledgers.go` (`ErrLedgerGap`, `LedgerSource.Window`, `LedgerHash`);
  `internal/sync/loop.go` (`ErrRetentionGap`, `ErrNetworkReset`, `DefaultResetThreshold`);
  `internal/sync/loop_test.go` (`TestRetentionGapWritesNothing`, `TestRetentionGapLeavesEveryTableUnchanged`,
  `TestCaughtUpRechecksTip`, `TestCrashMidBatchRollsBackAndResumes`); `internal/sync/reset_test.go`
  (`TestNetworkResetDetected`, `TestSmallTipLagIsNotAReset`, `TestMatchingCheckpointContinues`);
  `internal/ingest/ledgers_test.go` (`TestLedgerSourceNeverSkips`).
- **Residual risk**: the index relies on one provider for ledger history. Independent confirmation and failover
  are tracked in
  [#91](https://github.com/Soroban-CII/soroindex/issues/91) (cross-provider ledger-hash verification) and
  [#29](https://github.com/Soroban-CII/soroindex/issues/29) (RPC failover).

### T2.8 Plain-HTTP or credentialed RPC URL

- **Attacker**: a network attacker on the path to the provider; or anyone who can read the operator's process
  list, shell history or logs.
- **Impact**: plain HTTP provides no transport integrity. Credentials embedded in a URL may be exposed.
- **Mitigation as it exists**: `rpc.New` requires a non-empty URL and nothing else. The default endpoints for
  testnet and futurenet are HTTPS; mainnet has no default, so the operator must supply one. The client never logs
  response bodies: retry logs carry the method, attempt, wait and error only.
- **Enforced by**: `internal/rpc/client.go` (`New`, the `"rpc retry"` log call);
  `internal/config/config.go` (`Testnet.DefaultRPCURL`, `Futurenet.DefaultRPCURL`).
- **The gap**: no code rejects an `http://` URL, and no code redacts a userinfo component or query token in an
  RPC URL. `sync_state.rpc_url` stores the configured URL in the database. Authenticated providers are not
  supported at all; tracked as [#95](https://github.com/Soroban-CII/soroindex/issues/95)
  (open, "authenticated RPC providers without logging secrets").
- **Status**: credential handling is tracked in [#95](https://github.com/Soroban-CII/soroindex/issues/95).
  Separate tracking for the HTTPS default is pending.

### T2.9 Retry storm amplifying a provider outage

- **Attacker**: none; a failure mode worth bounding.
- **Impact**: a failing provider is hammered, and the indexer's own memory use multiplies across retries.
- **Mitigation**: at most 6 attempts, full-jitter exponential backoff capped at 10 s, a `Retry-After` header
  honoured up to that cap, a semaphore of `Concurrency` in-flight requests, and no retry on 4xx other than 429 or
  on a JSON-RPC error. One attempt's body is released before the next starts.
- **Enforced by**: `internal/rpc/client.go` (`Client.call`, `Client.backoff`, `parseRetryAfter`, `Client.sem`);
  `internal/rpc/client_test.go`.
- **Residual risk**: `MaxAttempts`, `BaseBackoff` and `MaxBackoff` are not exposed as flags; only `--rpc-timeout`
  and `--rpc-concurrency` are.

---

## 3. The public read-only API

`sep47idx serve` is meant to face the internet. It serves GET requests only, from a database opened read-only.

### T3.1 SQL injection through a query parameter

- **Attacker**: any API client.
- **Impact**: arbitrary reads from, or writes to, the index.
- **Mitigation**: every public value is bound as a parameter. The catalog query assembles **fixed** SQL fragments
  chosen by a validated tier, and appends only placeholders; no request string is ever concatenated into SQL.
  Identifiers are validated before any query: contract IDs through `strkey.Decode(strkey.VersionByteContract, …)`
  with a 32-byte length check, Wasm hashes as exactly 64 lowercase hex characters, SEP lists as positive decimal
  integers with no leading zeros and at most 100 entries. Unknown, repeated or empty query parameters are
  rejected with `bad_request` before anything runs.
- **Enforced by**: `internal/store/catalog.go` (`Store.Contracts` `predicate`, `ValidateContractID`, `ParseSEPs`,
  `ParseContractIDs`, `MaxLookupIDs`); `internal/store/wasm.go` (`ValidateWasmHash`); `internal/api/wasm.go`
  (`parseQuery`); `internal/api/catalog.go` (`contracts`, `lookup`); `internal/api/lookup_test.go`
  (`TestBulkLookupRejectsInvalidInputBeforeQuerying`); `internal/api/wasm_test.go`
  (`TestWasmEvidenceAndBadInput`); `internal/api/details_test.go` (`TestContractFixedShapeAndHistory`, which
  asserts 400 for `/v1/contracts/invalid` and for an unexpected parameter).
- **Residual risk**: the parsers themselves are not fuzzed. Tracked as
  [#106](https://github.com/Soroban-CII/soroindex/issues/106) (open, "fuzz every query-parameter parser").

### T3.2 Writes through the API

- **Attacker**: any API client, or a bug in a handler.
- **Impact**: a public endpoint that could migrate or modify the index.
- **Mitigation**: `api.New` refuses to start unless every backend's store reports `IsReadOnly()`. The store opens
  with `mode=ro` and applies no migrations in that mode. Only GET and OPTIONS reach the mux; anything else gets
  405 with an `Allow` header.
- **Enforced by**: `internal/api/server.go` (`validateBackend`, `Server.ServeHTTP` method check);
  `internal/store/store.go` (`Open` with `Options.ReadOnly`, `IsReadOnly`, `ErrReadOnlyUninitialized`,
  `ErrSchemaTooOld`); `internal/api/multinet_test.go` (`TestNewRejectsInvalidBackends`);
  `internal/api/server_test.go` (`TestHealthReportsFreshTipAndReadOnlyStore`, `TestServerRoutingAndMissingProgress`).
- **Residual risk**: file-system permissions still matter. A serve process that can write the file could be made
  to by another route; run it as a user with read-only access to the database, as
  [Self-hosting](self-hosting.md) describes.

### T3.3 Expensive aggregate endpoints

- **Attacker**: any unauthenticated client.
- **Impact**: aggregate requests can add significant load as the index grows. See the benchmark and decision
  record linked below.
- **Mitigation**: each request is bounded by a 15-second context, the store read runs in a `ReadOnly`
  transaction, and the per-IP rate limit (when enabled) applies.
- **Enforced by**: `internal/api/server.go` (`Server.ServeHTTP`, `context.WithTimeout(r.Context(),
  15*time.Second)`); `internal/store/statistics.go` (`Store.Stats`, which opens `sql.TxOptions{ReadOnly: true}`);
  `internal/api/stats_test.go` (`TestStatsAdoptionAndGap`).
- **Measured cost**: on the committed benchmark (Apple M4, warm cache, `docs/query-benchmark.md`), `/v1/stats`
  p95 is 1,350 ms at 1M claim rows and 118,467 ms at 10M; `/v1/seps` p95 is 4,327 ms and 89,520 ms. By
  comparison, `/v1/contracts?implements=41` is 23 ms p95 at 10M. The benchmark's own conclusion is that these two
  endpoints "should be treated as unsuitable for public traffic on an index approaching this size".
- **Residual risk**: real. Today's mainnet index is far below 1M claim rows, and the measured decision record in
  closed issue [#99](https://github.com/Soroban-CII/soroindex/issues/99) says the fix is summary tables
  maintained in the sync transaction — a schema change that needs maintainer approval and is not built. Until
  then, an operator
  exposing these endpoints publicly should set `--rate-limit` and cache them at a reverse proxy.

### T3.4 Unbounded result sets

- **Attacker**: any client.
- **Impact**: one request that returns the whole index.
- **Mitigation**: `limit` defaults to 50 and must be 1..500, validated both in the handler and again in the
  store. Paging is keyset-based on `contract_id`, so a deep page costs no more than a shallow one. Bulk lookup is
  capped at `MaxLookupIDs` (100) and cannot be combined with filters or paging.
- **Enforced by**: `internal/api/catalog.go` (the `limit must be 1..500` check); `internal/store/catalog.go`
  (`Store.Contracts` limit check); `internal/store/pagination.go` (`contractPageInput`);
  `internal/api/catalog_test.go` (`TestContractsFiltersPaginationAndAlias`); `internal/api/lookup_test.go`
  (`TestBulkLookupReportsUnknownAndKeepsOrder`).
- **Residual risk**: pagination bounds response size. Query cost still depends on filter selectivity, as shown in
  the [query benchmark](query-benchmark.md).

### T3.5 Cursor tampering

- **Attacker**: any client.
- **Impact**: injection or an out-of-range read through the opaque continuation token.
- **Mitigation**: a cursor is base64url JSON, capped at 256 characters, and must decode to `{"v":1,"id":"C…"}`
  with a valid contract strkey. Anything else is `ErrBadQuery` → 400. The decoded ID is used only as a bound
  parameter.
- **Enforced by**: `internal/store/pagination.go` (`contractPageInput`, `encodeContractCursor`);
  `internal/store/catalog.go` (the inline cursor check in `Store.Contracts`); `internal/api/catalog_test.go`,
  `internal/api/history_test.go` (`TestHistoryPaginationAndStaleVerification`).
- **Residual risk**: cursors are not authenticated, so a client can forge a valid-looking cursor and start paging
  anywhere. That reveals nothing the catalog does not already serve. Cursors also carry no schema or ruleset
  version, so one reused across an upgrade can produce confusing results; tracked as
  [#107](https://github.com/Soroban-CII/soroindex/issues/107) and
  [#177](https://github.com/Soroban-CII/soroindex/issues/177).

### T3.6 Rate-limit evasion

- **Attacker**: any client, or a botnet.
- **Impact**: sustained traffic can reduce API availability.
- **Mitigation**: an optional fixed-window per-IP limit, charged from the peer address of the TCP connection.
  `X-Forwarded-For` and similar headers are deliberately **ignored**, so a client cannot spoof its bucket. Bulk
  lookups are charged one unit per ten requested IDs. The window map is swept each minute and is capped at 50,000
  entries, so the limiter itself cannot be used to exhaust memory.
- **Enforced by**: `internal/api/limit.go` (`ipLimiter.allowN`, the 50,000-entry cap);
  `internal/api/catalog.go` (`lookupCost`, `idsPerCharge`); `internal/api/limit_test.go`
  (`TestPerIPRateLimitIgnoresForwardedHeaders`); `internal/api/lookup_test.go`
  (`TestBulkLookupChargesRateLimitPerTenIDs`).
- **Residual risk**: three, all real. The limit is **off by default** (`--rate-limit 0`). Because forwarded
  headers are ignored, a deployment behind a reverse proxy sees one IP for every client, so enabling the limit
  there throttles everyone together. And once the cap is reached the limiter returns 429 for a **new** IP rather
  than evicting, which is fail-closed for new peers. No response headers advertise the limit;
  [#151](https://github.com/Soroban-CII/soroindex/issues/151) is open for that.

### T3.7 CORS allows every origin

- **Attacker**: any website a user visits.
- **Impact**: any page can read the API from the user's browser.
- **Mitigation**: the API is read-only, unauthenticated and carries no cookies or credentials, so there is no
  user-specific data to steal and no state-changing request to forge. `Access-Control-Allow-Origin: *` with
  `GET, OPTIONS` is the intended behaviour for a public index.
- **Enforced by**: `internal/api/server.go` (the two `Access-Control-*` headers and the OPTIONS short-circuit);
  `internal/api/server_test.go` (`TestServerRoutingAndMissingProgress`, which asserts 204 for OPTIONS).
- **Residual risk**: an operator who puts the API behind an authenticating proxy would be exposing it to every
  origin. Configurable origins are tracked as [#148](https://github.com/Soroban-CII/soroindex/issues/148).

### T3.8 Information disclosure through errors

- **Attacker**: any client.
- **Impact**: internal paths, SQL text or raw input echoed back, useful for probing.
- **Mitigation**: every error response is the fixed `{"error":{"code","message"}}` shape with one of four codes
  and a fixed message string. Internal errors become `"cannot query contracts"` or `"cannot read index"`; the
  underlying error is never serialized. Bulk-lookup messages name a 1-based position and the cap, never the
  offending input.
- **Enforced by**: `internal/api/server.go` (`writeError`); `internal/api/details.go` (`detailError`);
  `internal/store/catalog.go` (`ParseContractIDs`, whose message names a position only);
  `internal/api/snapshot_test.go` (`TestSnapshotEveryEndpoint`, `TestSnapshotDocsMatch`).
- **Residual risk**: the API does not log requests at all, which is also a gap for incident response:
  [#105](https://github.com/Soroban-CII/soroindex/issues/105) is open for request IDs and access logs.

### T3.9 Slowloris and header floods

- **Attacker**: any client.
- **Impact**: connection and memory exhaustion of the server process.
- **Mitigation**: `http.Server` is configured with `ReadHeaderTimeout` 5 s, `ReadTimeout` 20 s, `WriteTimeout`
  20 s, `IdleTimeout` 60 s and `MaxHeaderBytes` 16 KiB, each overridable by a flag or `SEP47IDX_*` variable.
  Negative values are rejected at start-up. Shutdown waits 10 s for in-flight requests.
- **Enforced by**: `cmd/sep47idx/cmd_serve.go` (`serveHTTP`, `runServe` validation);
  `cmd/sep47idx/cmd_serve_test.go` (`TestServeSlowClientClosed`, `TestServeOversizedHeadersClosed`,
  `TestServeGracefulShutdownCompletesInFlight`).
- **Residual risk**: operators should set connection limits at the reverse proxy for public deployments.

---

## 4. Release pipeline and artifact integrity

Operators run the binaries and the container image this pipeline produces. A compromise here reaches every
operator.

### T4.1 A third-party GitHub Action is backdoored

- **Attacker**: whoever controls an action's repository or a mutable tag on it.
- **Impact**: arbitrary code in CI, with the release job's `contents: write` and `packages: write` tokens.
- **Mitigation**: every `uses:` in every workflow is pinned to a 40-character commit SHA with the version in a
  trailing comment. `grep -rn "uses:" .github/workflows/ | grep -v "@[0-9a-f]\{40\}"` returned no matches over
  all 31 `uses:` lines on 25 November 2026.
- **Enforced by**: `.github/workflows/ci.yml`, `release.yml`, `security.yml`, `docs.yml`, `integration.yml`,
  `adoption-report.yml`.
- **Residual risk**: nothing in CI enforces the pinning, so a future pull request could add a tag-pinned action
  and only review would catch it. An OpenSSF Scorecard check is tracked as
  [#136](https://github.com/Soroban-CII/soroindex/issues/136).

### T4.2 Over-broad workflow permissions

- **Attacker**: anyone who can get code into a workflow run, including a pull-request author.
- **Impact**: a token that can push code, publish releases or publish images.
- **Mitigation**: every workflow declares `permissions: contents: read` at the top level. Write scopes are
  granted per job and narrowly: `contents: write` plus `packages: write` only in `release.yml`'s `release` job,
  `contents: write` plus `pull-requests: write` only in `adoption-report.yml`'s job (which pushes a report branch
  and opens a PR, never `main`), and `pages: write` plus `id-token: write` only in `docs.yml`'s deploy job. No
  workflow uses `pull_request_target`, so a fork's code never runs with a privileged token.
- **Enforced by**: the `permissions:` blocks in `.github/workflows/*.yml`.
- **Residual risk**: `adoption-report.yml` runs on a schedule with write access and executes repository code
  against a live RPC endpoint. Branch protection, which the operator applies with `scripts/repo-settings.sh`,
  is what keeps that out of `main`.

### T4.3 A substituted or non-reproducible release artifact

- **Attacker**: someone who can influence one CI runner, or who can replace an asset after the fact.
- **Impact**: operators run a binary that is not the tagged source.
- **Mitigation**: the tag must match `^v[0-9]+\.[0-9]+\.[0-9]+$`. Two independent jobs (`build-a`, `build-b`)
  build the same tag on fresh runners with no cache, and `compare-release.sh` fails the pipeline unless their
  `SHA256SUMS` are byte-identical; it prints the differing files and the `go version -m` difference. Only then
  does the release job publish, and it publishes build-a's archives as a **draft** for a maintainer to review.
  If the release already exists, its reviewed assets are never replaced; a rebuild that differs is reported as a
  warning. Builds use `-trimpath` and `CGO_ENABLED=0`, and `BUILD.json` records the version, source commit,
  whether the tree was dirty, and the compiler.
- **Enforced by**: `.github/workflows/release.yml` (the `tag is vN.N.N` step, the `build` matrix, the
  `reproducible` job, the `release` job); `scripts/build-release.sh`; `scripts/compare-release.sh`;
  `scripts/package_release.py` (the `manifest` written as `BUILD.json`); `scripts/publish-release.sh`, which
  refuses a dirty tree, a non-`main` branch, an origin that does not match, and an existing tag pointing
  elsewhere.
- **Residual risk**: reproducibility is checked between two runners of the same provider at the same moment, not
  against an independent rebuilder.

### T4.4 A compromised dependency

- **Attacker**: whoever controls an upstream module or the module proxy.
- **Impact**: malicious code compiled into every binary.
- **Mitigation**: four direct modules only, all pinned exactly; `go.sum` is enforced and `go mod verify` runs in
  the release build, in the Docker build and in both `security.yml` jobs. `go mod tidy -diff` fails CI if the
  files drift. `govulncheck ./...` runs on every push and pull request, and `govulncheck -C cmd/sep47idx
  -scan=module -show=verbose` fails a pull request on any affected module even when the vulnerable code is
  unreachable. The versions table in `CONTRIBUTING.md` records each bump and why (for example moving off
  `klauspost/compress` 1.18 for GO-2026-6865).
- **Enforced by**: `.github/workflows/security.yml`; `.github/workflows/ci.yml` (`go mod tidy -diff`); `go.mod`;
  `Dockerfile` (`go mod download && go mod verify`); `scripts/build-release.sh` (`go mod verify`);
  [dependency review](dependency-review.md).
- **Residual risk**: `docs/requirements.txt` pins MkDocs and Material by version but not by hash, so the docs
  build trusts PyPI. That reaches the published site, not the binaries.

### T4.5 No signatures, SBOM or provenance attestation

- **Attacker**: anyone who can persuade a user to download the wrong file, or who can tamper with a mirror.
- **Impact**: an operator cannot verify cryptographically that an archive or image came from this pipeline.
  `SHA256SUMS` proves integrity only to someone who already has an authentic copy of the checksums.
- **Mitigation as it exists**: `SHA256SUMS` and the unsigned `BUILD.json` manifest, plus the two-runner
  reproducibility check (T4.3).
- **Enforced by**: `scripts/package_release.py`; `.github/workflows/release.yml`.
- **The gap**: no artifact signing (cosign or Sigstore), no SLSA provenance, no SBOM. `grep -rn "cosign\|sigstore\|attest\|sbom"` over
  `scripts/` and `.github/` matches only the issue text in `scripts/wave-issues.json` (checked 25 November 2026).
  The GHCR image is referenced by tag, not digest.
- **Status**: tracked as [#49](https://github.com/Soroban-CII/soroindex/issues/49) (open, "produce release SBOM
  and signed provenance").

### T4.6 A compromised container image at runtime

- **Attacker**: whoever reaches the running container.
- **Impact**: escalation from the API process to the host.
- **Mitigation**: multi-stage build on a digest-pinned `golang:1.27.4-bookworm`, shipping into a digest-pinned
  `gcr.io/distroless/static-debian13:nonroot`. The final image has no shell and no package manager, runs as UID
  65532, and the binary is static (`CGO_ENABLED=0`). The release job smoke-tests it with `docker run --rm
  --read-only`. An optional CA bundle is mounted only for the dependency download step and is never copied into
  the runtime image.
- **Enforced by**: `Dockerfile`; `.github/workflows/release.yml` (the `--read-only` smoke test);
  [Self-hosting](self-hosting.md#docker-validation).
- **Residual risk**: `/app/data` is writable by UID 65532 so a container can hold a database. Only the linux
  image is built for one architecture in the release job; arm64 images are tracked as
  [#50](https://github.com/Soroban-CII/soroindex/issues/50).

---

## 5. The verified tier's subprocess

**The verifier does not exist at this commit.** `internal/verify/` contains only `.gitkeep`. Nothing in the
codebase runs `npx`, `soroban-guard` or any child process. The `verified` tier is deferred, as
[Trust tiers](trust-tiers.md) and the README both say, because under 1% of mainnet Wasm declares anything.

What **does** exist is the surface the verifier would write to: the `verifications` table, the `tier=verified`
filter, and the `verified` object in the contract detail shape. Those are covered in
[T6.3](#t63-a-stale-verification-presented-as-current) and
[T6.4](#t64-a-failed-verification-counted-as-a-pass).

This section records the threats the adapter must address **before** it ships, each with the control
`CLAUDE.md` §5.13 requires and its status today.

| Threat | Required control | Status at `305de6d` |
| --- | --- | --- |
| The verifier submits transactions on mainnet, spending real funds and writing real state | Refuse to run unless the configured passphrase is the testnet passphrase | **Not built.** No passphrase guard exists because no verifier exists. `config.Testnet.Passphrase` is available for it. |
| `OWNER_SECRET` / `SPENDER_SECRET` leak into logs, the database or an error message | Pass them through to the child's environment only; never log, store or echo them | **Not built.** No code reads either variable (`grep -rn "OWNER_SECRET\|SPENDER_SECRET"` matches nothing outside `CLAUDE.md`). |
| A contract ID or SEP number from the index is interpolated into a shell command | Build the argument list with `exec.CommandContext`, never a shell; validate the contract ID as a strkey first | **Not built.** `store.ValidateContractID` already exists and must be used. |
| A hostile or hanging child process never returns | A 10-minute timeout on the child, with the process group killed | **Not built.** |
| The verifier receives `--allow-non-testnet-write` | CI must prohibit the flag in executable code and configuration | The flag appears in no Go, SQL, workflow or script file at `305de6d`. A CI check is pending. |
| A pass is read as a security audit | Every response and doc carrying `verified` states that it is not an audit | **Built for the docs.** `SECURITY.md`, `docs/trust-tiers.md` and `docs/api.md` all say so. |

**Status**: public tracking for the deferred verifier and the CI guard is pending.

---

## 6. Trust-tier boundaries

The index exists to tell a reader how much to trust a claim. Mixing tiers is the project's defining failure mode:
it is worse than being incomplete, because it misleads rather than disappoints.

### T6.1 A Stellar Asset Contract presented as declaring a SEP

- **Attacker**: none. A modelling error, which is why it is tested.
- **Impact**: a SAC would appear as a contract that *claims* SEP-41. SACs implement SEP-41 by protocol
  definition, and they declare nothing; conflating the two would make the declared count meaningless.
- **Mitigation**: every non-protocol tier predicate begins `c.kind <> 'sac'`; the `protocol` predicate is
  `c.kind = 'sac'` and answers only for SEP 41 or an unfiltered query. A SQL `CHECK` rejects a SAC row with a
  Wasm hash, so a SAC can never acquire claims through a hash.
- **Enforced by**: `internal/store/catalog.go` (`Store.Contracts` `predicate`); `migrations/0001_init.sql`;
  `internal/api/catalog_test.go` (`TestContractsFiltersPaginationAndAlias`, case
  `/v1/contracts?tier=declared&kind=sac` expecting 0); `internal/api/details_test.go`
  (`TestContractFixedShapeAndHistory`, which fails with "SAC mixed into declared tier" if a SAC's detail carries
  `"declared":true`); `internal/store/invariants_test.go` (`invariantViolations` case `a SAC with a Wasm hash`,
  run by `TestInvariantViolationsAreRejectedOnFreshDatabase`).
- **Residual risk**: none known for SEP-41. A future rule file for a SEP that SACs also satisfy would need the
  same treatment.

### T6.2 A declared claim read as a safety statement

- **Attacker**: anyone who deploys a contract with `contractmeta!(key="sep", val="41")` and no SEP-41 interface.
- **Impact**: a consumer integrates an unsafe or non-conforming token because the index said "SEP-41".
- **Mitigation**: a declaration is always reported as a declaration. Every API response carrying derived data
  includes `parser_version` and, for inferred results, `ruleset_version`; the contract detail reports `declared`,
  `inferred`, `verified` and `protocol` as separate fields, so a declaration that the interface contradicts is
  visible side by side. `not_token.wasm` is a committed fixture of exactly this case: it declares SEP-41 and
  matches 0 of 10 functions. The default tier for `/v1/contracts` is `declared`, which is the weakest, and the
  docs say in three places that no tier is a security audit.
- **Enforced by**: `internal/store/details.go` (the per-tier claim fields); `internal/api/details_test.go`
  (`TestContractFixedShapeAndHistory`, which asserts the exact 10-key detail shape); `testdata/wasm/not_token.wasm`
  and the fixture table in [Trust tiers](trust-tiers.md#worked-examples); `SECURITY.md`.
- **Residual risk**: this is a presentation control, not a technical one. Nothing stops a downstream consumer
  from reading `tier=declared` and showing a checkmark. The project's mitigation is to keep the weakest tier the
  default and to label everything; the risk cannot be removed.

### T6.3 A stale verification presented as current

- **Attacker**: a contract owner who upgrades after passing verification, including by changing a CAP-85
  executable reference that the contract's own instance entry never mentions.
- **Impact**: a "verified" badge that describes code the contract no longer runs — a direct path to over-trust.
- **Mitigation**: a verification belongs to a Wasm hash. Detail and history mark a verification `stale` whenever
  the contract's current hash differs from the verified one, and the `tier=verified` filter matches only when
  `v.wasm_hash = c.current_wasm_hash`, the verdict is `pass`, a matching declaration exists, and no later run for
  the same `(contract, hash, sep)` exists.
- **Enforced by**: `internal/store/details.go` (`Verification.Stale`, set from `currentHash != hash`);
  `internal/store/catalog.go` (the `verified` branch of `predicate`); `internal/api/history_test.go`
  (`TestHistoryPaginationAndStaleVerification`); `internal/ingest/apply_test.go`
  (`TestReferenceUpgradeOnlyReferencingContracts`, which proves a reference update upgrades exactly the
  referencing contracts).
- **Residual risk**: none known in the query. It is untested against real verifier output, because there is no
  verifier (§5).

### T6.4 A failed verification counted as a pass

- **Attacker**: a bug, or anyone with write access to the database file.
- **Impact**: the strongest tier label on a contract that failed.
- **Mitigation**: a SQL `CHECK` rejects `passed = 1` unless `verdict = 'pass'`, so Go code cannot write the
  contradiction even by accident. The filter requires both `v.passed = 1` and `v.verdict = 'pass'`, and prefers
  the latest run per `(contract, hash, sep)`.
- **Enforced by**: `migrations/0001_init.sql`; `internal/store/invariants_test.go` (`invariantViolations` cases
  `a failed verification marked passed` and `an unverifiable verification marked passed`, attempted as **raw
  SQL** inside a rolled-back transaction by `assertInvariantsHold`); `internal/api/verified_test.go`
  (`TestLatestFailedVerificationCannotMatchOlderPass`).
- **Residual risk**: anyone who can write the database file can write anything. The API opens it read-only; the
  database is the operator's trust boundary.

### T6.5 An inferred match from a rule file that has since changed

- **Attacker**: none; a correctness and provenance problem.
- **Impact**: a match attributed to a version of a SEP's rules that no longer says what it said.
- **Mitigation**: every `interface_matches` row records `ruleset_version` **and** the SHA-256 of the rule file
  that produced it (migration 0003). `tier=inferred` matches only rows whose `ruleset_version` equals the
  currently loaded version for that SEP, bound as JSON and compared in SQL. `--recompute` re-runs the matcher for
  rows whose recorded file hash differs from the loaded file, and rows written before migration 0003 carry
  `'unknown'` and are always recomputed. Responses carrying inferred data include `ruleset_versions`.
- **Enforced by**: `migrations/0003_rule_file_sha256.sql`; `internal/ingest/recompute.go` (the
  `rule_file_sha256 != ? OR rule_file_sha256 = 'unknown'` query); `internal/store/catalog.go` (the `inferred`
  branch of `predicate`); `internal/api/catalog_test.go` (case `/v1/contracts?tier=inferred&implements=40`
  expecting 0); `make rules-check`, which validates every rule file against `rules/schema.json` in CI.
- **Residual risk**: a Wasm analysed before migration 0002 has no stored `functions_json` and cannot be
  recomputed without re-fetching; by design it is reported as needing a re-fetch rather than silently skipped.

### T6.6 A lenient parse inflating the declared tier

- **Attacker**: anyone writing `SEP-41` or `041` in their meta, deliberately or not.
- **Impact**: the declared count includes tokens SEP-47 does not define, overstating adoption.
- **Mitigation**: lenient mode is the default, but nothing is hidden: every token is kept with its raw text and
  its anomaly code, `Accepted` records whether it contributed, non-numeric and empty tokens are dropped in both
  modes, and the API exposes both the normalised SEP and the anomaly. `/v1/stats` reports anomalies by type. The
  adoption report counts declarations both by unique Wasm hash and by contract, because factory deployments skew
  the per-contract number.
- **Enforced by**: `pkg/sepmeta/sep47.go` (`Mode`, `classify`, `Token.Accepted`, the `Anomaly*` constants);
  `pkg/sepmeta/sep47_test.go` (`TestVectors` over the committed vectors in `testdata/vectors`,
  `TestParseSEP47TokenFields`); `internal/store/statistics.go` (`Statistics.Anomalies`);
  `internal/api/stats_test.go` (`TestStatsAdoptionAndGap`).
- **Residual risk**: a consumer reading only `seps` sees the lenient number. The anomaly is one field away, not
  in the same field.

### T6.7 An unresolved executable reference silently keeping old claims

- **Attacker**: a CAP-85 owner contract that archives or removes a reference entry.
- **Impact**: the index would keep reporting claims for code the contract can no longer be shown to run.
- **Mitigation**: a `wasm_ref` contract whose reference is missing or archived has `current_wasm_hash = NULL` and
  therefore no current claims or matches, while remaining counted and flagged `archived`. A SQL `CHECK` requires
  `exec_ref_owner` and `exec_ref_tag` to be non-NULL exactly when `kind = 'wasm_ref'`, so a reference contract can
  never lose the record of what it references. Archival never deletes claim history.
- **Enforced by**: `migrations/0001_init.sql`; `internal/ingest/apply_test.go`
  (`TestUnresolvedReferenceClaimsNothing`, `TestReferenceArchivalClaimsNothingUntilRestored`,
  `TestArchivedCodeKeepsClaimsAndRestores`); `internal/store/invariants_test.go` (`invariantViolations` cases
  `a wasm_ref without its reference`, `a wasm_ref with only an owner`, `a wasm contract naming a reference`).
- **Residual risk**: none known.

---

## Gap register

This register lists public gaps and links to existing issues. Entries marked **Tracking pending** still need
a public tracking decision.

| # | Gap | Status |
| --- | --- | --- |
| [T1.2](#t12-oversized-module-or-section-exhausts-memory), [T1.8](#t18-hostile-ledger-metadata-and-ledger-entries), [T2.1](#t21-oversized-response-body) | Parser and RPC limits are hard-coded, not configurable. `CLAUDE.md` §5.1 says otherwise. | Open issue [#139](https://github.com/Soroban-CII/soroindex/issues/139) |
| [T2.7](#t27-withheld-skipped-or-rewritten-history) | One RPC provider, no cross-check of ledger hashes. | Open issues [#91](https://github.com/Soroban-CII/soroindex/issues/91), [#29](https://github.com/Soroban-CII/soroindex/issues/29) |
| [T2.8](#t28-plain-http-or-credentialed-rpc-url) | Remote RPC URLs can use plain HTTP. | **Tracking pending** |
| [T2.8](#t28-plain-http-or-credentialed-rpc-url) | No support for, or redaction of, RPC credentials. | Open issue [#95](https://github.com/Soroban-CII/soroindex/issues/95) |
| [T3.1](#t31-sql-injection-through-a-query-parameter) | Query-parameter parsers are not fuzzed. | Open issue [#106](https://github.com/Soroban-CII/soroindex/issues/106) |
| [T3.3](#t33-expensive-aggregate-endpoints) | `/v1/stats` and `/v1/seps` aggregate the whole index per request. | **No open issue.** The measured decision record is in closed [#99](https://github.com/Soroban-CII/soroindex/issues/99); the summary tables it recommends are a schema change, which needs maintainer approval before an issue proposes it (`CLAUDE.md` §5.8). |
| [T3.5](#t35-cursor-tampering) | Cursors carry no schema or ruleset version. | Open issues [#107](https://github.com/Soroban-CII/soroindex/issues/107), [#177](https://github.com/Soroban-CII/soroindex/issues/177) |
| [T3.6](#t36-rate-limit-evasion) | Rate limiting is off by default and has no response headers. | Open issue [#151](https://github.com/Soroban-CII/soroindex/issues/151) |
| [T3.7](#t37-cors-allows-every-origin) | CORS is `*` with no way to narrow it. | Open issue [#148](https://github.com/Soroban-CII/soroindex/issues/148) |
| [T3.8](#t38-information-disclosure-through-errors) | No request logs or request IDs for incident response. | Open issue [#105](https://github.com/Soroban-CII/soroindex/issues/105) |
| [T4.1](#t41-a-third-party-github-action-is-backdoored) | Action SHA pinning is practice, not an enforced check. | Open issue [#136](https://github.com/Soroban-CII/soroindex/issues/136) |
| [T4.5](#t45-no-signatures-sbom-or-provenance-attestation) | No signing, SBOM or provenance attestation. | Open issue [#49](https://github.com/Soroban-CII/soroindex/issues/49) |
| [§5](#5-the-verified-tiers-subprocess) | The verifier, its testnet guard, its secret handling and its timeout do not exist. | **Tracking pending** |
| [§5](#5-the-verified-tiers-subprocess) | No CI check enforces the restriction on `--allow-non-testnet-write`. | **Tracking pending** |

## Where this model is weakest

1. **The index depends on a single provider for ledger history.** Cross-provider verification is tracked in [#91](https://github.com/Soroban-CII/soroindex/issues/91).
2. **Availability of the aggregate endpoints is a measured problem, not a theoretical one** (T3.3). The fix is a
   schema change awaiting approval.
3. **The verified tier is a documented surface with no implementation** (§5). Every control it needs is listed
   here and none is built.
4. **The project is unaudited.** This page records a review of the source tree.
