---
name: backup-check
description: Verify an existing news-library backup and test restoring it to an isolated paused directory under project tmp.
---

After `make build`, run:

```sh
python3 minions/backup_check.py --source tmp/my-backup --output tmp/backup-check-01
```

Select an existing backup with backup.json, not a live data directory. The helper
delegates hashes, SQLite integrity, foreign keys and referenced-file coverage to
the production verify-backup command. It restores to a fresh private subdirectory
and checks the background pause marker. It never starts a service, sends messages,
resumes queues, replaces live data or copies configuration/secrets.

Report actual step exit statuses; incomplete or failed verification is not a valid
backup. Keep the restored copy and report in tmp for review. If a new backup is
also requested, use the documented production backup command with a fresh tmp
destination before checking it. See [commands and backup scope](../../../README.md).
