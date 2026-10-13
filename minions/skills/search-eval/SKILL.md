---
name: search-eval
description: Evaluate news-library search results and latency through its read API using selected queries and expected material IDs.
---

Prepare a private cases file under tmp using the [case format](../../README.md).
Queries and expected IDs should reflect the user's search intent. If no expected
IDs are supplied, results are unjudged; do not invent usefulness labels.

```sh
python3 minions/search_eval.py --cases tmp/search-cases.json --output tmp/search-eval-01
```

The helper requires an already running loopback site and its private read token.
It does not enable the API or start the site. Default FTS needs no inference;
explicit vector/hybrid cases use approved local embeddings, never generation.
Only GET requests are made. Do not substitute cloud embeddings or build indexes.

Compare actual returned mode, index coverage and warnings as well as hit recall,
reciprocal rank and measured latency. FTS fallback is not a successful semantic
test. Request failures are recorded and produce a nonzero exit; a zero exit only
means requests succeeded, not that relevance is satisfactory. Keep queries and
results private. No human ratings or library records are changed.
