---
name: library-doctor
description: Diagnose news-library configuration, current processing progress and optional local service availability without starting services or changing data.
---

Run from the news-library root:

```sh
python3 minions/library_doctor.py --output tmp/doctor-01
```

Use a fresh output directory. The helper reads schema v17 without migration,
uses private `.env` plus process overrides, and never prints secret values.
Use `--data PATH` for an explicitly selected store. Command flags of an existing
server are unknown; do not mistake env settings for confirmed runtime settings.

Add `--probe-local` when local availability is relevant: this checks only the
web socket and Ollama model listing, never generation. An intentionally stopped
site is a valid finding, not authorization to start it. A listed model does not
prove cloud generation works. Explain config, missing stages and availability
separately; use failure-triage for per-item details. See [helper contract](../../README.md).
