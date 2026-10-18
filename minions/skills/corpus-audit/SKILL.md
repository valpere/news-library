---
name: corpus-audit
description: Audit news-library primary-file integrity, current connection references and quality review signals without altering the corpus or calling models.
---

```sh
python3 minions/corpus_audit.py --data data --output tmp/corpus-audit-01
```

Use an existing schema-v16 store and a new private tmp directory. The audit checks
source/digest/metadata/connection hashes and reference ownership, not all backup
files. Use backup-check for diagnostic/checkpoint coverage.

Short sources, longer digests and repeated connection titles are review signals,
not automatic defects or usefulness judgments. Exact quotes prove textual
grounding, not that a connection explanation is supported. Inspect relevant
saved source/digest versions before deciding. Repairs need the user's applicable
authorization and new immutable versions; never rewrite history or frozen Wiki.
