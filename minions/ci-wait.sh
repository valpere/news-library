#!/usr/bin/env bash
# usage: minions/ci-wait.sh PR [TIMEOUT_SECONDS=900]
# Wait until the CI run for the PR's current head commit has completed, then print
# `gh pr checks`. Exit non-zero if any check failed (gh pr checks code) or 1 on timeout. Read-only.
set -euo pipefail
pr=${1:?usage: ci-wait.sh PR [TIMEOUT_SECONDS]}
limit=${2:-900}
read -r branch sha < <(gh pr view "$pr" --json headRefName,headRefOid -q '.headRefName + " " + .headRefOid')
deadline=$((SECONDS + limit))
# done = at least one run exists for the head commit and none of them is still going
runs_done() {
	local out
	out=$(gh run list --branch "$branch" --limit 20 --json headSha,status \
		-q "[.[] | select(.headSha==\"$sha\") | .status] | if length==0 then \"none\" elif all(.==\"completed\") then \"done\" else \"busy\" end")
	[[ $out == done ]]
}
until runs_done; do
	if ((SECONDS > deadline)); then
		echo "timeout waiting for CI on $branch@${sha:0:7}" >&2
		exit 1
	fi
	sleep 10
done
gh pr checks "$pr"
