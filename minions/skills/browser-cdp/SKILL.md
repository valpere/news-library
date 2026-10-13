---
name: browser-cdp
description: Inspect a user-selected tab in an existing Chromium session through CDP, including authenticated settings pages and their visible controls.
---

Use the user's running Chromium; default CDP is `http://127.0.0.1:9222`.
From this repository root:

```sh
python3 minions/cdp_inspect.py --url https://openrouter.ai/settings/privacy \
  --wait-for '[aria-label="Allow free endpoints that train on request data"]' \
  --output tmp/openrouter-privacy.json
```

`--list` lists only matching tabs. Match the exact origin and requested path;
refine `--url` if several tabs match. The helper reads headings, links, buttons,
and switch/checkbox states; it does not submit forms, navigate, expose input
values/cookies/storage or close tabs/browser. Output files are new private
files under project `tmp/`; reports may still contain private page labels.

For an authorized browser operation beyond inspection, use Playwright
`connect_over_cdp` in a bounded script and retain the same tab scope. Disconnect
by exiting the Playwright context; never call `browser.close()` on the user's
session. A link outside the viewport need not be force-clicked: for a read-only
page visit use `page.goto` to its verified destination. Wait for the actual
setting's control, not a generic heading or the first switch: authenticated
pages may render headings before settings finish loading.

A working CDP session grants access, not permission to change unrelated account
settings. Apply the user's existing task authorization and choices, including
training/publication preferences, before external mutations; don't ask again
for actions already authorized. Read current control values before deciding
what remains to be done. Never infer successful generation from a valid API
key or a rendered setting; use the relevant live integration test separately.
