---
name: snapshots
description: List, protect and explicitly prune full data snapshots (rollback copies) in snapshots/; never touches live data/.
---

Run from the news-library root:

```sh
python3 minions/snapshots.py create PURPOSE          # e.g. before-migration-v16
python3 minions/snapshots.py list [--older-than 30] [--keep-last 5]
python3 minions/snapshots.py keep|unkeep NAME
python3 minions/snapshots.py prune NAME... [--apply]
```

`snapshots/` (gitignored, private) holds full copies of the library taken before
risky operations (migration, repack, recovery). Name new ones
`<purpose>-<before|after>-<YYYYMMDD>` and move them here instead of `tmp/`.
Before any risky operation to the live store, make a verified backup first and
store it here. `list` flags unkept snapshots older than N days as candidates
only; pruning is never automatic and deletes only names the user approved.
`create` copies the SQLite DB with the backup API (read-only on `data/`) and hardlinks the
immutable `texts/`/`digests/`, so a snapshot costs about the DB size; it never overwrites.
Candidates = unkept, outside the newest `--keep-last`, and older than `--older-than`.
`keep` protects a rollback point; `prune` refuses kept snapshots and dry-runs
unless `--apply`. `data/` is the live server data: never modify it.
