# Approved-repository README comparison

Fetched live on 9 October 2026, before refreshing this repository's README. The [Stellar Wave approved list](https://www.drips.network/wave/stellar/repos) embeds its actual API response with `sortBy=stargazersCount`, page 1, 20 rows, 824 total. These are its first three approved rows. Stars are the Wave API's recorded counts, not an independent GitHub star census.

| Repository | Wave stars | GitHub-selected README | Git blob SHA |
| --- | ---: | --- | --- |
| [Emmy123222/Stellar-MarketPay-](https://github.com/Emmy123222/Stellar-MarketPay-) | 59 | `.github/README.md` | `e5817952868228a6550089b1c7f35fa9879411e9` |
| [Emmy123222/Stellar-GreenPay](https://github.com/Emmy123222/Stellar-GreenPay) | 54 | `README.md` | `cfd30cfdf1876c943b198845fc6bfa35e7049824` |
| [OFFER-HUB/offer-hub-monorepo](https://github.com/OFFER-HUB/offer-hub-monorepo) | 54 | `README.md` | `b69d8a174e927ac1f10271da562ca9a3165c1d61` |

GitHub's readme endpoint returned MarketPay's `.github/README.md`, a workflow-focused document: clear operational sections, contributor setup, task commands, troubleshooting and support. GreenPay starts with a name and short user-facing promise, badges and plain-English description, then features, demo, structure, Quick Start, contribution/security, roadmap and license. OFFER-HUB starts with name/badges and description, then features, stack, Quick Start, architecture/docs, contribution/tests, license, support, maintainers and acknowledgments.

The refreshed README uses the observed name/description/badges, practical setup, architecture/docs and contribution/maintainer/license pattern, in the exact order requested by CLAUDE.md Stage I. It includes this project's dated census and trust tiers. Public publication is marked pending; examples from other repositories do not establish this project's test or audit claims.

## Live eligibility search

The public page accepts a base64 JSON `filters` query, as confirmed against Drips' source and actual embedded search response. Read-only curl requests returned HTTP 200:

| Search | Total matching approved repositories |
| --- | ---: |
| `soroindex` | 0 |
| `ciscokwiz/soroindex` | 0 |
| `routedock` (positive control) | 1: `winsznx/routedock` |

This confirms the selected repository was not found in the approved-list search on this date. The operator must recheck immediately before submitting. Direct access to `wave-api.drips.network` remained proxy-blocked; the public page's server-rendered response supplied the evidence. Initial public-page requests were also denied, then later read-only curl requests succeeded. No account login, application submission or mutation was performed.
