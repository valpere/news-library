---
name: live-digest-smoke
description: Run an explicitly requested live news-library source and cloud digest integration check in an isolated fixture store, optionally with a synthetic prediction profile.
---

Use only when live integration testing is within the user's authorized task;
ordinary offline validation does not need this skill or live model calls.
Build first if needed. Run from the repository root:

```bash
python3 minions/live_digest_smoke.py --run-cloud
python3 minions/live_digest_smoke.py --run-cloud --prediction
```

Both use the public example URL unless `--url` is supplied. `--prediction`
uses an explicitly synthetic profile, never the user's inferred interests.
Private `.env` is loaded by the application; do not source or display it.
The existing adapter permits cloud generation only. No automatic fallback
to paid providers or local generation. Failure ends the helper; retries
require investigating the cause and existing authorization.

The helper creates an isolated database and primary files under `./tmp/`,
keeps fixture evidence there, and stops only its own subprocess. It never
edits the actual library or Obsidian collections. Report actual model/provider,
result, retained fixture path and limitations. A synthetic prediction smoke
verifies integration, not personal calibration or digest quality.
