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

See remaining M2 integration work and M3 in `docs/BACKLOG.md`.
Rules default to empty; predictions require an explicit current interest profile.
Digest-quality feedback must not alter human usefulness ratings.
Long sources use persisted digest checkpoints; cache cleanup must preserve
primary texts/digests and never follow cache-directory symlinks. See `docs/TRACEABILITY.md`
for passed M1 tests and requirements still awaiting M2–M4. Preserve existing
experiments; do not publish private corpus data automatically.
The shared tool library is documented at `~/wrk/common_codex/ONBOARDING.md`.

## Validation and Development

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
