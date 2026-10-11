# Reusable project helpers

Read-only URL list audit (no import/fetch, uses the application canonicalizer):
`go run ./minions/url-list-check -file '/path/to/url-list'` prints counts,
invalid/duplicate line numbers and whether the list fits one batch. It does
not print URLs or modify the source. Exit 1 reports invalid input or read errors.
