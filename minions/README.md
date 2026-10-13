# Reusable project helpers

## Operational helpers

`python3 minions/library_doctor.py --output tmp/doctor-01` reads private `.env`
and schema-v15 SQLite without opening the application Store. It reports only
whitelisted configuration checks, aggregate progress and secret presence.
Optional `--probe-local` checks the configured loopback web socket and Ollama
`GET /api/tags`; it never starts services or performs inference. Environment
overrides are honored; command-line settings of a running service are unknown.

`python3 minions/failure_triage.py --output tmp/triage-01` produces a private
per-item plan based on the selected source and latest corresponding digest,
metadata and runs. Completed stages supersede old failure history. A temporary
failure is only a possible explicit retry; other failures require inspection,
configuration correction or source text. No retries or database writes occur.
Both helpers accept `--data PATH` and `--env PATH` (default `.env`); reports
are new directories under project `tmp/`, with private file permissions.

`python3 minions/backup_check.py --source tmp/my-backup --output tmp/backup-check-01`
uses `build/news-library verify-backup`, then restores to a fresh private
`tmp/.../restored` directory and checks the pause marker. It verifies an existing
backup, not a live data directory, and never starts or resumes restored workers.
Run `make build` first. A failure is recorded and exits nonzero.

`python3 minions/search_eval.py --cases tmp/search-cases.json --output tmp/search-eval-01`
queries the authenticated read API of an already running loopback site. Each
case has `query`, optional `modes` (default `["fts"]`), `expected_ids`, `limit`
(1–100), and `filters` (category/tags/read/state/usefulness/filter/sort).
Example: `[{"query":"HTTP cache","modes":["fts","hybrid"],"expected_ids":[1,2]}]`.
It records returned IDs, latency, actual search mode, index coverage, warnings,
expected-hit recall and reciprocal rank. Failed cases remain failures; an empty
expected list means relevance is unjudged. Vector/hybrid queries use approved
local embeddings; no text generation, indexing or ratings are performed. Default
base URL follows `NEWS_LIBRARY_BIND`; override with `--base-url`. Read token is
loaded privately, never passed on the command line. The helper does not start
the site, which may intentionally be stopped. Reports may contain private queries.

Skills are under `minions/skills/`, with local discovery links in `.agents/skills/`:
`library-doctor`, `failure-triage`, `backup-check`, `search-eval`, `corpus-audit`,
`source-recovery`, `knowledge-recovery`, and `openrouter-check`. Existing checks,
cloud digest smoke and browser-CDP skills remain available. Temporary evidence
is never part of the public project archive.

Read-only URL list audit (no import/fetch, uses the application canonicalizer):
`go run ./minions/url-list-check -file '/path/to/url-list'` prints counts,
invalid/duplicate line numbers and whether the list fits one batch. It does
not print URLs or modify the source. Exit 1 reports invalid input or read errors.


Explicit read-only source smoke: `go run ./minions/source-smoke -fetch-url URL`.
Fetches one source live with a 30s context, prints extractor/completeness/text
size/warnings, does not write a database or invoke models. Not routine CI.

Offline digest check: `go run ./minions/validate-digest -file PATH` or add
`-ollama-response` for a captured `/api/chat` JSON envelope. Read-only, no
network/model calls, uses the application's validator and prints only result.


Консиліум документів без Codex: `python3 minions/consilium/run.py --dry-run`
готує знімок; `--run-agents` явно запускає незалежні CLI-рецензенти.
Коміт і повний склад фіксуються; звіти та помилки зберігаються в `tmp/consilium/`.
Інструкції: [consilium/README.md](consilium/README.md).
