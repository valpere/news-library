#!/usr/bin/env python3
"""Read-only final PR gate for an already reviewed immutable head/base."""
import argparse
import json
import re
import subprocess


def blockers(pr, head, base, required):
    problems = []
    if pr.get("state") != "OPEN" or pr.get("isDraft") is not False:
        problems.append("PR must be open and not a draft")
    for field, expected in (("headRefOid", head), ("baseRefOid", base)):
        if pr.get(field) != expected:
            problems.append(f"{field} differs from reviewed SHA")
    if pr.get("mergeable") != "MERGEABLE" or pr.get("mergeStateStatus") != "CLEAN":
        problems.append("GitHub merge state must be MERGEABLE/CLEAN")
    checks = pr.get("statusCheckRollup") or []
    for name in required:
        if not any(check.get("name", check.get("context")) == name for check in checks):
            problems.append(f"missing required check: {name}")
    for check in checks:
        kind = check.get("__typename")
        passed = (kind == "CheckRun" and check.get("status") == "COMPLETED"
                  and check.get("conclusion") == "SUCCESS") or (
                      kind == "StatusContext" and check.get("state") == "SUCCESS")
        if not passed:
            problems.append("check not successful: " + str(check.get("name", check.get("context", "unknown"))))
    return problems


def sha(value):
    if not re.fullmatch(r"[0-9a-f]{40}", value):
        raise argparse.ArgumentTypeError("use the complete 40-character reviewed SHA")
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("pr", type=int)
    parser.add_argument("--repo", default="valpere/news-library")
    parser.add_argument("--head", required=True, type=sha)
    parser.add_argument("--base", required=True, type=sha)
    parser.add_argument("--required-check", action="append", default=[])
    args = parser.parse_args()
    fields = "url,state,isDraft,headRefOid,baseRefOid,mergeable,mergeStateStatus,statusCheckRollup"
    pr = json.loads(subprocess.check_output(
        ["gh", "pr", "view", str(args.pr), "--repo", args.repo, "--json", fields], text=True))
    problems = blockers(pr, args.head, args.base, args.required_check or ["news-library-check"])
    output = {"ready": not problems, "url": pr.get("url"),
              "head": pr.get("headRefOid"), "base": pr.get("baseRefOid"), "blockers": problems,
              "scope": "Remote state only; review approval and local validation must be verified separately."}
    if not problems:
        output["merge_argv"] = ["gh", "pr", "merge", str(args.pr), "--repo", args.repo,
                                "--squash", "--match-head-commit", args.head]
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return 1 if problems else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except subprocess.CalledProcessError as error:
        raise SystemExit(error.returncode)
    except (ValueError, OSError) as error:
        raise SystemExit(f"pr-gate: {error}")
