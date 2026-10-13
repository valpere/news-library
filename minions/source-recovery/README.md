# Source recovery

Read-only, offline inventory of active items without a selected source:

```bash
go run ./minions/source-recovery -data data
```

With the site stopped and a verified backup, explicitly recover a bounded batch:

```bash
go run ./minions/source-recovery -data data -apply -run-cloud \
  -items 1,2 -output tmp/source-recovery.json
```

Omit `-items` to select up to `-limit` (default 30, maximum 500) active items
without source. `-apply` permits live fetches and primary/DB writes;
`-run-cloud` additionally permits the configured cloud digest and knowledge
pipeline. Without it, only text fetching runs. Private settings are read from
`.env` (`-env-file` can choose another file); process environment overrides them.
Secrets are never printed or shell-sourced. No Telegram or embedding worker runs.

Existing accepted source/digest stages are reused. A recognized saved browser
challenge is fetched again rather than digested. At most one fetch/digest attempt
per item is made; chunk checkpoints and immutable diagnostics remain in the store.
Knowledge work is scoped to the selected item. The report is saved after each
item, and human ratings/read/placement state are checked for exact preservation.
Read each item's result: successful process exit does not mean every remote
source or generation succeeded. Resolve diagnosed failures before explicit retry.

The helper refuses paused restores, an occupied configured site port, or an
already active processing queue. Keep reports in `./tmp/`; do not publish private
URLs, transcripts or digests. Routine `make check` never runs recovery.
