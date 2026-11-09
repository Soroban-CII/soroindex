#!/usr/bin/env python3
"""Validate the Wave backlog before any operator-authorized GitHub mutation."""
import argparse
import collections
import json
import pathlib
import re
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
AREAS = {"parser", "sync", "api", "cli", "rules", "docs", "infra"}
DIFFICULTIES = {"trivial", "medium", "high"}


def load_issues():
    issues = json.loads((ROOT / "scripts/wave-issues.json").read_text())
    assert len(issues) >= 25, "Wave needs at least 25 real remaining issues"
    titles = set()
    for item in issues:
        assert item["title"] not in titles, "duplicate issue title"
        titles.add(item["title"])
        assert item["area"] in AREAS and item["difficulty"] in DIFFICULTIES
        for field in ("summary", "why", "tech_stack"):
            assert item[field].strip(), f"missing {field}"
        assert len(item["acceptance_criteria"]) >= 2
        # existing_issue: the open GitHub issue already tracking this work, so the
        # creator skips it instead of opening a duplicate under another title.
        assert isinstance(item.get("existing_issue", 0), int) and item.get("existing_issue", 0) >= 0
        for pointer in item["pointers"]:
            path = (ROOT / pointer).resolve()
            assert path.is_relative_to(ROOT) and path.exists(), f"unknown pointer: {pointer}"
    return issues


def body(item):
    sections = ["## Summary\n" + item["summary"], "## Why\n" + item["why"],
                "## Acceptance Criteria\n" + "\n".join("- [ ] " + x for x in item["acceptance_criteria"]),
                "## Tech Stack\n" + item["tech_stack"],
                "## Pointers\n" + "\n".join("- `" + x + "`" for x in item["pointers"])]
    return "\n\n".join(sections) + "\n"


def gh(*args, capture=False):
    return subprocess.run(["gh", *args], check=True, text=True,
                          stdout=subprocess.PIPE if capture else None)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default="Soroban-CII/soroindex")
    parser.add_argument("--create", action="store_true", help="operator authorization to create labels/issues")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repo):
        parser.error("repo must be owner/name")
    issues = load_issues()  # Complete validation precedes every mutation.
    counts = collections.Counter(x["area"] for x in issues)
    tracked = [x for x in issues if x.get("existing_issue")]
    if not args.create:
        for item in issues:
            if item.get("existing_issue"):
                print(f"tracked by #{item['existing_issue']} (not created): {item['title']}")
                continue
            print(f"would create [{item['difficulty']}/{item['area']}]: {item['title']}\n{body(item)}")
        print(f"validated {len(issues)} remaining issues ({len(tracked)} already open on GitHub); "
              f"areas: {dict(sorted(counts.items()))}")
        return
    open_numbers = set(gh("api", "--paginate", f"repos/{args.repo}/issues?state=open&per_page=100",
                          "--jq", ".[] | select(.pull_request == null) | .number", capture=True).stdout.split())
    stale = [x for x in tracked if str(x["existing_issue"]) not in open_numbers]
    if stale:  # Validation precedes every mutation: a closed tracker means the list is out of date.
        raise SystemExit("existing_issue not open: " + ", ".join(f"#{x['existing_issue']} {x['title']}" for x in stale))
    existing = set(gh("api", "--paginate", f"repos/{args.repo}/issues?state=all&per_page=100",
                      "--jq", ".[] | select(.pull_request == null) | .title", capture=True).stdout.splitlines())
    labels = [("Stellar Wave", "1D76DB"), ("bug", "D73A4A"), ("enhancement", "A2EEEF")]
    labels += [(f"difficulty/{x}", "FBCA04") for x in sorted(DIFFICULTIES)]
    labels += [(f"area/{x}", "5319E7") for x in sorted(AREAS)]
    for label, color in labels:
        gh("label", "create", label, "--repo", args.repo, "--color", color, "--force")
    created = skipped = 0
    for item in issues:
        if item.get("existing_issue"):
            print(f"skip (tracked by #{item['existing_issue']}): " + item["title"])
            skipped += 1
            continue
        if item["title"] in existing:
            print("skip (exists): " + item["title"])
            skipped += 1
            continue
        with tempfile.TemporaryDirectory(prefix="soroindex-issue-") as directory:
            path = pathlib.Path(directory) / "body.md"
            path.write_text(body(item))
            gh("issue", "create", "--repo", args.repo, "--title", item["title"],
               "--label", "Stellar Wave", "--label", "difficulty/" + item["difficulty"],
               "--label", "area/" + item["area"], "--body-file", str(path))
        created += 1
    print(f"created {created}; skipped {skipped}")


if __name__ == "__main__":
    main()
