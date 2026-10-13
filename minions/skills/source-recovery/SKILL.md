---
name: source-recovery
description: Inspect or complete an explicitly selected news-library source recovery batch while retaining extracted texts, history and completed downstream stages.
---

Read [the helper's README](../../source-recovery/README.md) for current flags,
preconditions and stopping conditions. Start with its read-only plan for the
requested item IDs. Missing language tracks, unavailable pages, challenge/footer
responses and generation failures require different handling; do not retry
digests when the source itself is unusable.

Apply mode and cloud processing require the user's applicable authorization for
that concrete selection. Preserve completed stages and immutable history; keep
reports under tmp. Do not broaden the batch, start the site, resume restored
workers or modify Obsidian/Wiki collections. Existing explicit session
authorization may be used without requesting it again.
