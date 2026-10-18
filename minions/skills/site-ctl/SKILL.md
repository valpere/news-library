---
name: site-ctl
description: Start, stop or check the local news-library site only when the user explicitly asks; graceful stop, no data access.
---

Run from the news-library root (after `make build`):

```sh
node minions/site_ctl.mjs status
node minions/site_ctl.mjs start [--resume-restored]
node minions/site_ctl.mjs stop
```

Never start the site on your own initiative: other helpers treat a stopped
site as a valid state. The pid and log live in `tmp/site/` (`agent.log`).
Stop sends SIGTERM only, after verifying the pid runs `build/news-library`;
it never kills. `--resume-restored` is for a restored data directory the user
chose to resume. The helper does not read or modify library data.
