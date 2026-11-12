#!/usr/bin/env bash
# usage: minions/merge-deploy.sh PR
# Squash-merge a PR whose checks are all green (after review!), sync main, rebuild
# build/news-library, restart the user systemd unit and check that / answers 200.
# Refuses when any check is not passing. Writes: merges and deletes the remote branch.
set -euo pipefail
pr=${1:?usage: merge-deploy.sh PR}
cd "$(git rev-parse --show-toplevel)"
if ! gh pr checks "$pr" >/dev/null; then
	echo "checks are not all green for PR $pr; not merging" >&2
	exit 1
fi
gh pr merge "$pr" --squash --delete-branch
git checkout -q main
git pull -q --ff-only
make build >/dev/null
systemctl --user restart news-library
sleep 3
code=$(curl -s -o /dev/null -w '%{http_code}' "http://${NEWS_LIBRARY_ADDR:-127.0.0.1:8790}/")
echo "main@$(git rev-parse --short HEAD) deployed, GET / -> $code"
[[ $code == 200 ]]
