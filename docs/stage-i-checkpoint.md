# Stage I checkpoint

## STOP I report

Prepared 9 October 2026 for **ciscokwiz/soroindex**, branch `work`. Source preparation is complete and pushed. Public Wave submission readiness remains conditional on the operator actions below. No release/tag, registry push, main merge, account setting, issue publication or Drips submission was performed by the agent.

## Commits

Each logical unit was committed and immediately pushed; documentation changes were committed per page.

```text
d05df1a chore(repo): curate 26 remaining stellar wave issues
19d2dff fix(repo): target selected repository in operator settings script
ba54990 docs(repo): refresh security and contribution templates for wave
9adadf0 docs(contributing): document wave backlog and release review
a110c8b fix(repo): show setting payloads in dry-run preview
5278cc4 build(release): package four static binaries with license notices
e87005c chore(release): prepare v0.1.0 changelog and operator publication
2213b06 docs(wave): prepare factual v0.1.0 release body
f08183d docs(wave): capture real release candidate demo outputs
a6415d5 docs(wave): refresh application with built features and real backlog
77f0445 docs(authors): point to implemented wasm inspection command
189fb1e docs(architecture): clarify completed follow check and fixture waiver
443e361 docs(hosting): use selected repository in setup links
53f0b94 docs(rules): link schema in selected repository
6cd734d docs(site): configure selected repository and pages URL
3f3c0a4 docs(wave): correct server address flag in recording guidance
544539a docs(ci): identify selected Pages deployment
bc09b5a docs(wave): record live approved README comparison and eligibility
d51b813 docs(readme): align wave presentation with approved repositories
466df38 docs(wave): record successful live eligibility and publication checks
```

This report is the final source documentation commit after those listed above.

## Commands and outcomes

- `DRY_RUN=1 ./scripts/create-issues.sh`: 26 validated remaining issues. Areas: api 4, cli 3, docs 4, infra 4, parser 3, rules 3, sync 5. All file pointers resolve. No already-completed API, CLI, Docker or incremental-sync tasks remain in the backlog.
- Mocked execution of the issue creator: two existing titles skipped; 24 calls supplied complete body files and all required labels. No real GitHub request was made by this check.
- `DRY_RUN=1 ./scripts/repo-settings.sh`: correct selected repository; protection payload requires one approval and lint/test/fuzz-smoke/build, includes admins, and disables force-push/deletion. The preview now displays write payloads without executing them.
- `bash -n scripts/repo-settings.sh scripts/build-release.sh scripts/publish-release.sh` and Python compilation passed. The publication script's default preview performs no publication; `--publish` requires clean reviewed main and matching origin.
- `.venv/bin/mkdocs build --strict`: passed after all page/config changes. Pages publishing workflow is present and targets main; this is not evidence of a hosted deployment.
- `./scripts/build-release.sh`: built static linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 archives. `sha256sum --check SHA256SUMS` passed for all four. Archive inspections confirmed executable mode, project/Go/dependency notices and consistent BUILD.json. A clean-tree build recorded source commit 544539abeef6797ae1d30f0d3f93544126bec6af; final candidate can be rebuilt from this checkpoint without creating a tag.
- Local Docker build with VERSION=v0.1.0 completed. `docker run --rm --read-only soroindex:v0.1.0 version` returned `sep47idx v0.1.0 (go1.27.2, linux/amd64)`. Nonroot read-only stats reported 200 stored contracts. A read-only API/container request returned one real inferred SEP-41 match, and `/app/licenses` contained Go redistribution notices.
- Container API startup first failed because container DNS could not reach the public RPC. Supplying named proxy variables, the proxy hostname mapping and a read-only trusted CA mount resolved it; TLS stayed enabled. Ordinary Internet hosts may use the README command directly.
- The [demo](wave/DEMO.md) records real candidate commands/output: dated Phase 0 rendering, separate declared/inferred queries, gap (27 contracts), actual contract history, and curl stats at ledger 5097816. Rendering did not rerun the mainnet census. Absolute paths identify the actual run; fresh samples can differ.

Stage I changes no Go application logic. Existing G/H unit, race, lint, fuzz, benchmark and security validation is recorded in [the G/H checkpoint](stage-gh-checkpoint.md); it was not relabelled as a fresh Stage I run. Stage I functional validation covers the new packaging and operator utilities. No implementation-mirroring tests were added.

## Claims and live evidence

- Before writing README, fetched and read the GitHub-selected READMEs of the three leading approved Wave rows (59, 54, 54 Wave-recorded stars). Their source hashes and observed patterns are in [README comparison](wave/README-COMPARISON.md).
- Public approved-list searches returned zero for `soroindex` and `ciscokwiz/soroindex`; control `routedock` returned one. Initial proxy 403s were followed by successful HTTP 200 public-page curl reads. The direct Wave API hostname still failed proxy CONNECT. The page's server-rendered JSON supplied the actual live search/list evidence.
- Anonymous HTTP checks: repository **200**, CI workflow page **200**, documentation site **404**, intended v0.1.0 release page **404**. These are read-only HTTP results, not logged-out browser rendering checks. External badges/prior-art links still require operator browser review.
- Candidate release binaries and Docker image exist locally. No public image/release, docs deployment, demo video or hosted API is claimed. The application link table states what remains absent.
- Mainnet adoption figures remain the 7 October census: 156,173 contracts; 13/2,178 live hashes declare any SEP (0.60%); 120 hashes match SEP-41; 109 matching hashes declare nothing. This is dated evidence, not a new scan.

## Waivers and constraints

The operator explicitly waived Stage F step 28 deployed-fixture upgrade validation. Stage I's history demo therefore uses a real observed version and does not invent an upgraded contract or claim that waived test passed. Stage J remains deferred because measured adoption is below 1%.

CLAUDE.md Stage I says: **“The operator then runs `repo-settings.sh` and `create-issues.sh`, records the demo, installs the Drips Wave GitHub App on the org, and submits. You never submit the application yourself, and never act in the operator's Drips or GitHub account beyond the scripts they run.”** Publication utilities are concrete and reviewable but remain unexecuted.

The cloud setup draft preserves the tested install script and now records Stage I startup/validation instructions. Network additions include the public Wave hosts and `wave-api.drips.network`, GitHub API, docs, badge and contributor hosts. Draft persistence was confirmed; review/save and publication are required to apply it. No fresh-task restoration or active-network update is claimed.

## Operator handoff

1. Review/merge `work` into main through a PR. Review the README, application, changelog and candidate release body.
2. Preview the settings and issue scripts above, then run them. Confirm protected-main checks, private vulnerability reporting, topics, Pages deployment and 26 published remaining issues.
3. From clean reviewed main, preview `./scripts/publish-release.sh`, then publish with the operator's registry access. It rebuilds archives, verifies checksums, smoke-tests the image, creates/pushes the non-overwritten v0.1.0 tag and publishes release assets/image.
4. Open every link in a logged-out browser; confirm the docs site renders, release assets/image are public, and demo commands reproduce. Record the video and add its public URL. A hosted API is optional; no placeholder URL is supplied.
5. Recheck the approved list immediately before submission, install the Drips Wave GitHub App for the selected repository/account, and submit from the operator's account.

Next stage: finish these Stage I operator publication checks. Do not start Stage J under the current adoption gate. Stage K cadence starts only after the STOP I review/go-ahead.
