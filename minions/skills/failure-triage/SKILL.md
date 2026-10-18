---
name: failure-triage
description: Explain current news-library failures and missing processing stages, distinguishing possible retries from source, format, limit and configuration problems.
---

```sh
python3 minions/failure_triage.py --output tmp/triage-01
```

Run at the repository root; choose a new private output directory. The plan uses
active items and selected source/digest, with corresponding metadata/runs.
Completed stages supersede historical failures. Waiting or running work should
not be duplicated. Check report.json for the requested IDs; do not publish
corpus data. `--data PATH` selects a different existing schema-v16 store.

A temporary retry candidate is a suggestion, not an automatic retry or a promise
of success. Format/configuration/limit/source failures need their indicated
correction. Use existing source-recovery or knowledge-recovery helpers for an
explicitly authorized bounded selection, preserving completed checkpoints and
rating history. A request to explain failures does not authorize mutations or
cloud calls. Existing recovery authorization in the session remains applicable.
See [helper contract](../../README.md).
