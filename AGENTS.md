# Repository Guidelines

## Purpose and Authority

Build a personal news library for one user. The user is the sole authority
on personal usefulness at the time of assessment. Model ratings are predictions;
they must not overwrite human judgments. Interests and knowledge change, so
retain dated rating history. Unknown historical rating dates stay unknown.

Read `START_HERE.md`, then `docs/REQUIREMENTS.md`, `docs/HLD.md`, and
`docs/BACKLOG.md`. These documents describe the current implementation contract.
The `docs/news-library-*` files preserve discovery and experimental context;
some older proposals were superseded. Do not treat proposals as user decisions.

## Project Structure

M1 and the core M2 workflow are implemented in `cmd/news-library/` and `internal/library/`: one Go
service, a web interface, SQLite metadata, separate immutable text/digest files,
a shared HTTP cache, and a persistent background queue. Runtime settings live
in private `.env`; `.env.example` documents defaults. Backup needs DB + primary files. Python
validation and optional browser smoke live in `scripts/`; private evaluation
scripts and pilot artifacts remain local. The upstream license is preserved.

M4 backup/restore and Telegram preview/delivery code are implemented. Restored
workers stay paused; imported unfinished deliveries are quarantined as unknown.
Telegram remains disabled until private bot/destination setup; user selected
09:00 Europe/Kyiv, up to 5 items. Do not send live messages during routine tests.
See remaining optional M2 integration work in `docs/BACKLOG.md`.
M3 uses local bge-m3 with versioned persistent derived indexes and read-only
agent API. FTS/context need no inference; semantic queries never generate text.
Rules default to empty; predictions require an explicit current interest profile.
Digest-quality feedback must not alter human usefulness ratings.
Long sources use persisted digest checkpoints; cache cleanup must preserve
primary texts/digests and never follow cache-directory symlinks. See `docs/TRACEABILITY.md`
for passed M1 tests and requirements still awaiting M2–M4. Preserve existing
experiments; do not publish private corpus data automatically.
The shared tool library is documented at `~/wrk/common_codex/ONBOARDING.md`.

Connection notes use immutable revisions and exact source references; current
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
Keep secrets and runtime data out of source control. Do not publish or push
local preparation changes without user instructions.
