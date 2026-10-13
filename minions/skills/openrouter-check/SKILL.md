---
name: openrouter-check
description: Check a privately configured OpenRouter key, free quota and model catalog, or run an explicitly requested isolated free digest fallback test.
---

For key/configuration status at the repository root:

```sh
go run ./minions/openrouter-key-check
```

This loads the private key directly and reports only whitelisted metadata; it
makes no generation requests. Never source .env in a shell, print the key or
copy full authenticated API responses. A valid key/catalog is not proof that a
generation endpoint works.

For an explicitly requested generation test, read
[openrouter-smoke/README.md](../../openrouter-smoke/README.md). It uses a fresh
synthetic fixture under tmp and production free-only checks. Keep concrete free
IDs, zero prices/cost and non-BYOK guards. Account privacy and request
data_collection policy are separate; use the user's existing policy/BYOK decision,
not an inferred change from account controls. Do not enable the reserve, relax
privacy, modify account settings or send real corpus text solely to test a key.
