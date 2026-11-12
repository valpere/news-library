#!/usr/bin/env bash
# usage: minions/ci-wait.sh PR [TIMEOUT_SECONDS=900]
# Wait until the CI run for the PR's current head commit has completed, then print
# `gh pr checks`. Exit 1 if any check failed or the wait timed out. Read-only.
set -euo pipefail
pr=${1:?usage: ci-wait.sh PR [TIMEOUT_SECONDS]}
limit=${2:-900}
read -r branch sha < <(gh pr view "$pr" --json headRefName,headRefOid -q '.headRefName + " " + .headRefOid')
deadline=$((SECONDS + limit))
until gh run list --branch "$branch" --limit 5 --json headSha,status \
	-q ".[] | select(.headSha==\"$sha\" and .status==\"completed\")" | grep -q .; do
	if ((SECONDS > deadline)); then
		echo "timeout waiting for CI on $branch@${sha:0:7}" >&2
		exit 1
	fi
	sleep 10
done
gh pr checks "$pr"
