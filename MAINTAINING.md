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
4. Reassess the Stage J gate using the newly measured share of live Wasm hashes
   declaring any SEP. The recorded 7 October 2026 measurement is 0.60%; verified
   execution remains deferred below 1%. Reaching the gate requires an explicit
   operator go-ahead, a testnet-only implementation and separately supplied
   credentials. No security audit claim follows from a conformance pass.

See CONTRIBUTING.md for pinned tooling/checks and docs/cli.md for command flags.
If a patch raises the language floor or changes public API/schema semantics,
record the reason and obtain the required operator review before implementation.

## Each Stellar Wave

- Refill the backlog only with demonstrable remaining work. Review
  `scripts/wave-issues.json` against merged code and existing open/closed issues;
  remove solved or duplicate proposals and keep concrete acceptance criteria and
  valid pointers. Preview `DRY_RUN=1 ./scripts/create-issues.sh` before the operator
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
Issue and PR triage: <links, concrete actions, review deadlines; or nothing to report>
Standards review: <SEP source SHAs/versions, findings; or not due / not run>
Dependency review: <versions, updates, check outcomes; or not due / not run>
Adoption report: <source date/ledger, coverage, report commit, Stage J decision;
                  or not due / not run>
Wave backlog and reviews: <real remaining work and outstanding reviews;
                          or not due / nothing to report>
Risks / blockers: <specific observation, owner, next action; or none observed>
Next review: <date/time and timezone; owner>
```

The report records operations, not activity points. Do not paste keys, tokens,
private reports or full credential-bearing RPC URLs. Link reproducible public
artifacts and retain private evidence in the appropriate restricted location.
