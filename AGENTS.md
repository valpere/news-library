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

This repository currently contains documentation, Python evaluation scripts in
`scripts/`, and pilot artifacts in `experiments/news-library/`. The upstream
template provided README, license, and Go-oriented ignore rules; application
code has not been implemented. The accepted application architecture is one
Go service, a web interface, SQLite, and a persistent background queue.

Start with milestone M1 in `docs/BACKLOG.md`. Add application directories as
they become necessary. Preserve the upstream license and existing experiments.
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
