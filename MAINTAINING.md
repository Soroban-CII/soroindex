# Maintainer cadence

This is the operating checklist for soroindex. Record evidence and outstanding
work using the report below. “Nothing to report” is a normal, useful outcome.
Do not claim a deployment, release, sync run or verification that was not checked.

## Weekly

### Check lag and forward progress

Use the running deployment's `/v1/health` to check network, indexed ledger,
current RPC tip and lag. Record the response timestamp, network and lag; investigate
RPC errors or lag above the deployment's documented target. A green health
response alone does not prove that the follow process is advancing.

Read the same database's `last_ledger` twice, **60 seconds apart**. The second
value must be greater than the first while the network is producing ledgers.
Run the following from the checkout with the binary on PATH, replacing the
example path with the actual index used by the follow process:

```sh
export MAINTENANCE_DB=/absolute/path/to/testnet.db
export MAINTENANCE_NETWORK=testnet
python3 - <<'PY'
import json
import os
import subprocess
import time

command = ["sep47idx", "stats", "--network", os.environ["MAINTENANCE_NETWORK"],
           "--db", os.environ["MAINTENANCE_DB"], "--json"]
def ledger():
    result = subprocess.run(command, check=True, capture_output=True,
                            text=True, timeout=30)
    value = json.loads(result.stdout)["last_ledger"]
    if not isinstance(value, int) or isinstance(value, bool):
        raise SystemExit("No valid indexed ledger: investigate index initialization")
    return value

first = ledger()
time.sleep(60)
second = ledger()
print(f"last_ledger: {first} -> {second}; delta={second - first}")
if second <= first:
    raise SystemExit("Index did not advance: investigate follow process and RPC")
PY
```

An unchanged value is an investigation trigger, even if health is green. Check
that the service is running `sync --follow`, targets this database and network,
and has no RPC, retention-gap, permission or transaction errors. Compare the
RPC tip between reads to distinguish a stalled writer from a paused network.
A deliberately frozen demo database is not a live-follow success. Never change
or reset its progress marker to make this check pass. A retention gap requires
an operator-reviewed backfill/reseed, not skipping missing ledgers.

### Review the 24-hour testnet soak

The nightly `integration` workflow follows testnet for 30 minutes. That is
too short to show slow lag drift or a slow memory leak, so `soak.yml` runs the
same check — `TestFollowTestnet30Minutes`, with its duration and thresholds
read from `SEP47IDX_SOAK_*` — for 24 hours. It runs on a weekly schedule
(01:11 UTC on Sundays) and from **Actions → soak → Run workflow**. It is
read-only against testnet: it indexes ledgers, submits no transaction, needs
no key and uses no secret.

A job on a GitHub-hosted runner is cancelled at 6 hours, so the soak is a
chain of five segments of 4h48m (24h00m in total), run in order by `needs`.
Each segment downloads the previous segment's SQLite index and every earlier
segment's samples, so the index follows consecutive ledgers across the chain
and the lag series spans the whole day. A failing segment stops the chain; its
samples, report and log are still uploaded.

The nightly `integration` workflow sets none of these variables, so it still
follows for 30 minutes against a throwaway index **with the pass/fail
conditions it had before the soak existed**: the follow must end in
`DeadlineExceeded`, the index must have advanced, and the final lag must be
under 10 ledgers. It does now also record samples and evaluate the soak
thresholds, but only reports them — a violation appears in the log and fails
nothing, because the thresholds apply only when `SEP47IDX_SOAK_ENFORCE` is
set, and the soak action is the only thing that sets it. `integration.yml` is
deliberately left alone. A long local run is the same: setting a 24-hour
duration does not turn enforcement on by itself, so observing a run and
gating on one are separate decisions.

The growth ratio does not fail a run on its own. A segment begins with a cold
process, so the first third of its samples covers warm-up: a 40 MiB resident
set reaching 90 MiB doubles without leaking, and a bare ratio test would fail
healthy nightly runs. The ratio applies only once growth has also passed a
quarter of `max_growth_bytes` — 64 MiB at the default. Lowering
`max_growth_bytes` after a real soak lowers that floor with it.

Workflow inputs, with their defaults:

| Input | Default | Effect |
| --- | --- | --- |
| `segment_duration` | `4h48m` | Follow duration per segment. Five segments make 24 hours. |
| `sample_interval` | `1m` | Shortest gap between recorded samples. |
| `max_lag` | `25` | Fail when a post-catch-up sample is further behind the tip, in ledgers. |
| `max_growth_bytes` | `268435456` | Fail when memory within one segment grows by more bytes. |
| `max_growth_ratio` | `2.0` | Fail when memory within one segment grows by more than this factor, and growth also passed a quarter of `max_growth_bytes`. |
| `test_timeout` | `5h` | `go test -timeout`; keep it above `segment_duration`. |

Check the plumbing cheaply before committing a day of runner time: dispatch it
with `segment_duration=12m` and `test_timeout=20m`. That exercises the whole
chain, including the artifact hand-off, in about an hour.

Artifacts and where the numbers are:

- `soak-samples-<n>`: that segment's timestamped samples
  (`soak/samples/segment-00n.jsonl`, one JSON object per line with
  `at`, `last_ledger`, `latest_ledger`, `lag`, `caught_up`, `memory_bytes`,
  `memory_source`, `rss_bytes`, `heap_in_use_bytes`), its Markdown report and
  its `go test` log. Uploaded whether the segment passed or failed.
- `soak-db-<n>`: the index handed to the next segment. Kept 3 days.
- `soak-series-<run id>`: every segment's samples and reports in one artifact.
- Each segment's job summary holds its report; the `summary` job holds the
  per-segment results and the furthest report.

**What a green run proves, and what it does not.** Lag is judged across the
whole chain, because it measures the index against the network. Memory growth
is judged **inside one segment only**: each segment is a fresh process, so the
resident-set series is discontinuous at every boundary and in-process leak
detection restarts. A green 24-hour run means lag stayed within `max_lag` for
24 hours and each ~4h48m process stayed within the growth limits. It does
**not** prove that a single process is leak-free for 24 hours. GitHub-hosted
runners cannot run one process for 24 hours at all. To cover that, run one
24-hour segment on a self-hosted runner, or locally:

```sh
SEP47IDX_SOAK_DURATION=24h SEP47IDX_SOAK_SAMPLE_INTERVAL=1m \
SEP47IDX_SOAK_ENFORCE=true \
SEP47IDX_SOAK_SAMPLE_DIR=/absolute/path/soak/samples \
SEP47IDX_SOAK_DB=/absolute/path/soak/testnet.db \
SEP47IDX_SOAK_REPORT=/absolute/path/soak/report-001.md \
go test -v -count=1 -tags integration -run '^TestFollowTestnet30Minutes$' \
  -timeout 25h ./internal/sync
```

The growth limits are starting points, not measurements: no soak has reported
a plateau yet. After the first full run, read the per-segment `first` and
`last` memory columns in its report and tighten `max_growth_bytes` to
something a little above the observed spread, in the same change as the
evidence.

**Measured so far (26 November 2026).** Only short, network-free verification
has been run; the 24-hour soak is **outstanding** and needs a maintainer to
dispatch it.

- `go test ./internal/sync -run TestFollowLoopSamplingEndToEnd -v` drives the
  real follow loop against a fake ledger source for 300 ms and records 222
  samples: flat injected RSS gives "every configured threshold was met"; a
  4 MiB-per-sample rise gives `segment 1 memory (rss) grew from 192937984 to
  536870912 bytes (+343932928, x2.78) ... above the limit of 67108864 bytes or
  x2.00`; a frozen index under a moving tip gives `lag exceeded 1 ledgers in 9
  of 10 samples after catch-up`.
- `go test ./internal/sync -run 'TestParseSoakConfig|TestEvaluateSoak|TestSoakReportMarkdown|TestLoadSoakSamples|TestSampler|TestEnforcedErr'`
  covers the duration parsing, the defaults (no variable set keeps the nightly
  30 minutes and leaves enforcement off) and every threshold branch.
- `TestEnforcedErrSeparatesNightlyFromSoak` runs one violating series through
  both modes: nightly reports `segment 1 memory (rss) grew from 343932928 to
  880803840 bytes (+536870912, x2.56) over 11m0s, above the limit of
  268435456 bytes or x2.00` and still passes; a soak run fails on the same
  series with the same message.
- No testnet lag or RSS figure from a long run exists yet. Do not quote one
  until a `soak` run produces it; record the run URL, the span, the median and
  maximum lag and each segment's growth verdict here when it does.

### Check index against RPC

Run the data-quality checker against the same database and network:

```sh
sep47idx check --network testnet --db /absolute/path/to/testnet.db --sample 100 --seed 1
```

The same database and seed always compare the same contracts. Exit 0 means no
differences. Exit 1 means at least one stored contract disagrees with RPC, or
the run failed; investigate before treating the index as healthy. Entries
changed after the index's `last_ledger` are skipped and counted, so run this
after sync has caught up. Record the sample size, seed, checked count and
difference count in the report.

### Triage issues and PRs

Review new reports, reproduce actionable failures, check duplication and label
area/difficulty. Give contributors useful next steps and identify a reviewer.
Record blockers and ownership. Keep security reports private through the route
in SECURITY.md. Merge only after review and required CI checks; acknowledge
when a review will take longer than expected.

## Monthly

1. Read the current SEP-41 and SEP-47 texts in `stellar/stellar-protocol`
   (`ecosystem/sep-0041.md` and `ecosystem/sep-0047.md`). Record the source commit,
   version and review date. Compare required/optional signatures and metadata
   semantics with `rules/sep-0041.json` and parser behavior. If interface rules
   change, bump the ruleset version, update its source provenance, add matching
   and mismatch regression vectors, validate with `make rules-check`, and
   recompute matches with the documented CLI. Do not silently reinterpret old
   results or mix declarations with interface matches.
2. Check `github.com/stellar/go-stellar-sdk` and `modernc.org/sqlite` for patch
   updates using authoritative releases/module metadata. Keep pins exact and
   update one dependency per reviewed change. Run module verification, unit/race
   tests, lint, rules validation, four static targets and both security scans;
   rerun affected XDR/parser or SQLite invariants. Do not automatically move a
   pre-1.0 SDK across minor versions. Record “no update available” when applicable.
3. Regenerate the adoption report using the CLI reference and Hubble export
   procedure. Mainnet requires an explicit RPC URL and the census inputs; retain
   source dates, ledger, coverage, archive counts, denominators and partial-read
   limitations. Review generated `report/ADOPTION.md` before committing it.
   `phase0 --render-only` only renders stored summaries and is not a fresh census.
   The testnet half is automated: `.github/workflows/adoption-report.yml` runs on
   the 1st of each month (and from **Actions → adoption-report → Run workflow**),
   regenerates `report/testnet-*` and `report/ADOPTION.md`, and opens a PR with a
   before/after table of headline numbers. It never pushes to `main`, fails
   without a PR if the RPC is unavailable, and opens no PR when nothing changed.
   Review and merge that PR; mainnet still needs the Hubble export by hand.
4. Reassess the Stage J gate using the newly measured share of live Wasm hashes
   declaring any SEP. The recorded 7 October 2026 measurement is 0.60%; verified
   execution remains deferred below 1%. Reaching the gate requires an explicit
   operator go-ahead, a testnet-only implementation and separately supplied
   credentials. No security audit claim follows from a conformance pass.

5. Run `sep47idx maintenance` on each deployed index with its sync stopped
   (docs/self-hosting.md, "Maintenance"). Record the integrity result, the size
   before and after and the duration. An integrity failure is an incident:
   keep the damaged file, restore the last good backup and resume sync from
   it; never delete or rebuild the file to make the check pass. A refusal
   because the write lock is held means sync was still running; stop it and
   rerun, do not retry in a loop.

See CONTRIBUTING.md for pinned tooling/checks and docs/cli.md for command flags.
If a patch raises the language floor or changes public API/schema semantics,
record the reason and obtain the required operator review before implementation.

## Each Stellar Wave

- Refill the backlog only with demonstrable remaining work. Review
  `scripts/wave-issues.json` against merged code and existing open/closed issues;
  remove solved or duplicate proposals and keep concrete acceptance criteria and
  valid pointers. Run `./scripts/create-issues.sh` (a dry run) before the operator
  publishes the batch. Do not run account mutations from an unattended cadence.
- Review contributor PRs within **48 hours**. If blocked, acknowledge the PR,
  explain the blocker and give a realistic next review time. The target is a
  useful review, not rushed approval or automatic merging.
- Recheck live Wave eligibility and public repo/docs/release/video links before
  an application. The operator handles account installation and submission.

**Hard rule:** never close contributor-facing issues yourself to look active,
and never pad the backlog. Close only for a substantive documented resolution
under the contribution process. Ask: would this be worth doing if nobody were
watching the repository?

## Fixed maintenance report

Use the same format each time. Enter “not run” or “blocked” with a reason for
missing evidence; do not replace an unperformed check with “nothing to report.”
Use dates and time zones explicitly (Africa/Lagos for the maintainer schedule).

```text
Maintenance report: <date/time and timezone>; <weekly / monthly / Wave>
Repository / commit / network / deployment: <actual values>
Sync lag: <timestamp, indexed ledger, RPC tip, lag or exact error>
Forward progress: <first timestamp + ledger; second timestamp + ledger;
                   elapsed >=60 seconds; delta; pass / investigate / not run>
Data quality: <network, sample, seed, checked, changed_after_index, archived, differences; or not run>
Soak: <run URL, segments and results, span, median and max lag, per-segment
       memory growth verdicts; or not due / not run>
Issue and PR triage: <links, concrete actions, review deadlines; or nothing to report>
Standards review: <SEP source SHAs/versions, findings; or not due / not run>
Dependency review: <versions, updates, check outcomes; or not due / not run>
Adoption report: <source date/ledger, coverage, report commit, Stage J decision;
                  or not due / not run>
Index maintenance: <database, integrity result, size before -> after, duration;
                    or not due / not run>
Wave backlog and reviews: <real remaining work and outstanding reviews;
                          or not due / nothing to report>
Risks / blockers: <specific observation, owner, next action; or none observed>
Next review: <date/time and timezone; owner>
```

The report records operations, not activity points. Do not paste keys, tokens,
private reports or full credential-bearing RPC URLs. Link reproducible public
artifacts and retain private evidence in the appropriate restricted location.
