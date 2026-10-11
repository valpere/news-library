---
name: pr-gate
description: Verify the final news-library PR head/base and successful GitHub checks after review and before an authorized merge.
---

Read `docs/REVIEW.md`. This skill checks remote readiness; it does not perform
review or grant publication/merge permission. Use the recorded, fully reviewed
head and base SHAs, not freshly fetched SHAs presented as reviewed.

Run from the repository root:

```bash
python3 minions/pr_gate.py <PR> --head <reviewed-head> --base <reviewed-base>
```

The script is read-only. It requires OPEN/non-draft, exact head/base,
MERGEABLE/CLEAN and successful checks, including `news-library-check`.
Missing, skipped, pending, unknown or failed results block readiness.
For another repository supply `--repo` and its `--required-check` names.

Before using the returned merge argv, verify applicable authorization,
completed review with no unresolved blockers, successful relevant local
checks, and that no local edit or changed base made the review stale.
Record evidence under `./tmp/`. Recheck immediately before an authorized
merge; execute argv directly, never shell-evaluate generated JSON. Use the
reviewed head with `--match-head-commit`; never use admin/bypass. Verify the
actual remote MERGED state and merge SHA after execution.

If pending, wait in intervals that allow progress updates; a failed or changed
target requires investigation/review, not an automatic retry or weaker gate.
