# Minions (посіпаки)

Private helpers for the agent only: they speed up repeated work and are never
called by the service, hooks, timers or CI. Anything the service needs is a utility
(`cmd/`, `internal/`, `scripts/`). Details of older helpers: [README.md](README.md).

## Agent workflow

- `minions/ci-wait.sh PR [TIMEOUT_SECONDS]` — waits until CI has finished for the PR's current head commit, then prints `gh pr checks`; exit 1 on failure or timeout.
- `minions/merge-deploy.sh PR` — refuses unless all checks are green, then squash-merges and deletes the branch, syncs `main`, runs `make build`, restarts the `news-library` user unit and checks that `/` returns 200.
- `minions/page-text.sh PATH [MAX_CHARS]` — prints the visible text of a page of the running local service (e.g. `/items/169`), scripts and tags stripped; read-only GET.

## Data and checks (see README.md for details)

- `python3 minions/snapshots.py create PURPOSE|list|keep|unkeep|prune` — rollback copies of live data in `snapshots/`.
- `python3 minions/pr_gate.py` — read-only final remote gates for a PR.
- `python3 minions/project_check.py` — local/public project checks.
- `python3 minions/library_doctor.py`, `failure_triage.py`, `backup_check.py` — read-only operational reports into `tmp/`.
- `python3 minions/search_eval.py`, `search_feedback.py` — search quality evaluation and recorded relevance judgments.
- `go run ./minions/url-list-check`, `source-smoke`, `validate-digest` — offline URL list audit, one-source live smoke, digest validator.
- `node minions/site_ctl.mjs status|start|stop` — explicit site control, only when the user asks.
- `python3 minions/consilium/run.py` — document consilium with independent CLI reviewers.

## Audits, recovery and live smokes (explicit runs only; some call cloud models)

- `python3 minions/corpus_audit.py --data PATH --output tmp/DIR` — offline inventory and review signals; never opens the Store or runs models/workers.
- `python3 minions/cdp_inspect.py --url URL [--port 9222] [--list] [--wait-for SELECTOR] [--output FILE]` — reads one selected Chromium CDP tab and leaves the browser running.
- `go run ./minions/source-recovery` / `go run ./minions/knowledge-recovery` — bounded recovery of failed source/analysis; read-only and offline by default (see their README.md).
- `go run ./minions/openrouter-key-check` — checks the OpenRouter key against `https://openrouter.ai/api/v1/models` (process env vs `.env` key); cloud request.
- `go run ./minions/openrouter-smoke -run-cloud -no-byok -model MODEL` — OpenRouter fallback smoke; cloud request (see its README.md).
- `python3 minions/live_digest_smoke.py`, `live_chat_smoke.py` — explicit live cloud digest and dialogue smokes on a separate store under `tmp/`; never routine checks.
- `go run minions/live_semantic_smoke.go` — explicit local bge-m3 integration smoke; fixtures under `tmp/`.
