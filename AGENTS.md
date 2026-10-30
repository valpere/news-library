# Repository Guidelines

## Purpose and Authority

Build a personal news library for one user. The user is the sole authority
on personal usefulness at the time of assessment. Model ratings are predictions;
they must not overwrite human judgments. A human rating is one current value with the date it was given; a new
rating replaces it. When a material's text changes its ratings and links are erased and the
material is queued for post-processing. There are no text or digest versions and no history
tables: only the current state is stored (see `docs/REDESIGN_DECISIONS.md`).

Read `START_HERE.md`, then `docs/REQUIREMENTS.md`, `docs/HLD.md`, and
`docs/BACKLOG.md`. These documents describe the current implementation contract.
`docs/EVALUATION.md` and `docs/RESEARCH_NOTES.md` preserve evaluation data, pilot
results and unadopted ideas; they do not override the contract. Do not treat
proposals as user decisions.

## Project Structure

M1 and the core M2 workflow are implemented in `cmd/news-library/` and `internal/library/`: one Go
service, a web interface, SQLite metadata, separate text/digest files (one current text and digest per material),
a shared HTTP cache, and a persistent background queue. Runtime settings live
in private `.env`; `.env.example` documents defaults. Backup needs DB + primary files. Python
validation and optional browser smoke live in `scripts/`; private evaluation
scripts and pilot artifacts remain local. The upstream license is preserved.

M4 backup/restore is implemented. Telegram digests on a schedule are dropped (the daily
schedule is off); the planned replacement is a bot that accepts new links and sends the
digest once, as Markdown with buttons (see `docs/REDESIGN_DECISIONS.md`). Restored workers
stay paused. Do not send live messages during routine tests.
See remaining optional M2 integration work in `docs/BACKLOG.md`.
M3 uses local bge-m3 with versioned persistent derived indexes and read-only
agent API. FTS/context need no inference; semantic queries never generate text.
Rules default to empty; predictions require an explicit current interest profile.
Digest-quality feedback must not alter human usefulness ratings.
Generation failures have typed reasons (only the last one is kept per material); rejected replies
are diagnostics, never accepted digests. Format/configuration/limit/source
failures must not retry automatically; diagnostic files belong in backups.
Long sources use persisted digest checkpoints; cache cleanup must preserve
primary texts/digests and never follow cache-directory symlinks. See `docs/TRACEABILITY.md`
for passed M1 tests and requirements still awaiting M2–M4. Preserve existing
experiments; do not publish private corpus data automatically.
The shared tool library is documented at `~/wrk/common_codex/ONBOARDING.md`.

Connection notes are single current records without revisions; current
backlinks are derived, never added by rewriting article files. The existing
`~/doc/llm-wiki/wiki` is frozen and its import is deferred.

## Validation and Development

Store temporary agent scripts, fixtures and review evidence under `./tmp/`.
Keep recurring helper scripts in `./minions/`; promote reusable temporary
scripts there. Project skills are in `minions/skills/`, with local discovery
links under `.agents/skills/`. Use `project_check.py` for local/public checks
and `pr_gate.py` for read-only final remote gates. Live digest smoke is explicit
and excluded from routine checks. See README for commands.

Run `make check` for offline validation of this preparation package. Pilot
`run` commands make cloud requests; do not include them in routine checks.
Use `gofmt` and meaningful Go tests when implementing application code, and add
working build/run/test commands to the README and Makefile at that point.

Generation uses cloud models through local Ollama; OpenRouter free is the
optional fallback. Local generative models are currently excluded. Local
`bge-m3` is approved for embeddings. Filters that reject an item before AI
processing must cause zero generation and embedding calls for that item.

## Data and Changes

Keep extracted text immediately; saving to the library is a separate user
action. Model failures must not lose a submitted URL or existing text.
Agent access is read-only. Never modify source Obsidian/LLM Wiki collections.

Do not invent commit conventions: upstream currently has only its initial
commit. Describe concrete changes and validation in commits or pull requests.
Full-data rollback copies live in gitignored `snapshots/` (see `minions/snapshots.py`);
live `data/` and snapshots are never modified or deleted without the user's explicit instruction.
Keep secrets and runtime data out of source control. Do not publish or push
local preparation changes without user instructions.
