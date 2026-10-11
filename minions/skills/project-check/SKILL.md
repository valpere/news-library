---
name: project-check
description: Run news-library offline checks and validate its committed public files before a PR, excluding local research and runtime data.
---

Run from the news-library repository root:

```bash
python3 minions/project_check.py
```

This runs `make check`, then archives the resolved HEAD commit under `./tmp/`
and checks public documents/spec separately. If local checks already passed
and only the committed public composition needs checking, use `--public-only`.
Use `--ref <commit>` to check a specific committed tree. Working-tree edits
are included only in local checks; archive checks cover committed files only.

Report the tested commit, actual exit status and what was covered. An error
is not a successful check. Do not run pilot generation commands. Run relevant
browser fixtures separately when the change affects their behavior.
The script does not commit, publish, migrate user data or start the app.

Keep one-off helper scripts and generated evidence under `./tmp/`.
Promote reused helpers into `./minions/` instead of regenerating inline code.
