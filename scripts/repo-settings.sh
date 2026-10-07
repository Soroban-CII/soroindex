#!/usr/bin/env bash
# Repository settings for soroindex (CLAUDE.md §7 step 40). Run by a
# maintainer with admin rights:
#   ./scripts/repo-settings.sh
#   REPO=owner/name ./scripts/repo-settings.sh
#
# 1. GitHub Pages from GitHub Actions (the docs workflow publishes the site).
# 2. Branch protection on main: pull requests with 1 approval, required checks
#    lint, test, fuzz-smoke and build (the exact check names CI reports), no
#    force-push, no deletion. Admins are not exempt from checks on PRs but may
#    still push directly (enforce_admins false), so a maintainer can fix main.
# 3. Repository topics.
set -euo pipefail

REPO="${REPO:-Soroban-CII/soroindex}"
command -v gh >/dev/null || { echo "gh CLI is required" >&2; exit 1; }

echo "== Pages (source: GitHub Actions)"
if gh api "repos/$REPO/pages" >/dev/null 2>&1; then
  gh api -X PUT "repos/$REPO/pages" -f build_type=workflow >/dev/null
else
  gh api -X POST "repos/$REPO/pages" -f build_type=workflow >/dev/null
fi
gh api "repos/$REPO/pages" --jq '"site: " + .html_url'

echo "== Branch protection on main"
gh api -X PUT "repos/$REPO/branches/main/protection" \
  -H "Accept: application/vnd.github+json" --input - >/dev/null <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["lint", "test", "fuzz-smoke", "build"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": true
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_linear_history": false
}
EOF
gh api "repos/$REPO/branches/main/protection" \
  --jq '"required checks: " + (.required_status_checks.contexts | join(", ")) + "; approvals: " + (.required_pull_request_reviews.required_approving_review_count | tostring) + "; force-push allowed: " + (.allow_force_pushes.enabled | tostring)'

echo "== Topics"
gh repo edit "$REPO" \
  --add-topic stellar --add-topic soroban --add-topic sep-47 --add-topic sep-41 \
  --add-topic indexer --add-topic smart-contracts --add-topic go --add-topic drips-wave
gh repo view "$REPO" --json repositoryTopics --jq '"topics: " + ([.repositoryTopics[].name] | join(", "))'

echo "== Private vulnerability reporting (SECURITY.md relies on it)"
gh api -X PUT "repos/$REPO/private-vulnerability-reporting" >/dev/null && echo "enabled"
