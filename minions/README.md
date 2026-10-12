# Reusable project helpers

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
