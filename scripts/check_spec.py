#!/usr/bin/env python3
"""Offline trace coverage and tumanomir check; no generation calls."""
from collections import Counter
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def unique(values, label):
    duplicates = [value for value, count in Counter(values).items() if count > 1]
    if duplicates:
        raise ValueError(f"duplicate {label}: {', '.join(duplicates)}")
    return set(values)


def validate_contract(requirements, hld, trace):
    rows = re.findall(r"^\| \[REQ-(FR-\d+)\] \|(.+)$", requirements, re.M)
    if not rows:
        raise ValueError("no requirement definitions found")
    ids = unique([ident for ident, _ in rows], "requirements")
    all_tags = re.findall(r"\[REQ-([^\]]+)\]", requirements)
    if Counter(all_tags) != Counter(ident for ident, _ in rows):
        raise ValueError("requirement tags must occur once in their definition rows")
    components = unique(re.findall(r"^### (FUN-NL-[A-Z-]+)$", hld, re.M), "components")
    for ident, row in rows:
        edges = re.findall(r"-> \[(FUN-NL-[A-Z-]+)\]\(HLD\.md#([a-z-]+)\)", row)
        if not edges:
            raise ValueError(f"{ident}: missing HLD trace")
        for component, anchor in edges:
            if component not in components or anchor != component.lower():
                raise ValueError(f"{ident}: unknown HLD target {component}#{anchor}")
    use_cases = unique(re.findall(r"^\| (UC-\d+) \|", requirements, re.M), "use cases")
    mappings = re.findall(r"^\| (FR-\d+) \| ([^|]+) \| ([^|]+) \| (AT-\d+) \| ([^|]+) \|$", trace, re.M)
    mapped = unique([row[0] for row in mappings], "trace rows")
    if mapped != ids:
        raise ValueError(f"trace coverage mismatch: missing={sorted(ids-mapped)}, extra={sorted(mapped-ids)}")
    unique([row[3] for row in mappings], "acceptance IDs")
    covered = set()
    for ident, cases, milestones, acceptance, status in mappings:
        referenced = set(cases.strip().split(", "))
        if not referenced <= use_cases:
            raise ValueError(f"{ident}: unknown use cases {sorted(referenced-use_cases)}")
        if not set(milestones.strip().split(", ")) <= {"M1", "M2", "M3", "M4"}:
            raise ValueError(f"{ident}: unknown milestone")
        if acceptance != ident.replace("FR-", "AT-"):
            raise ValueError(f"{ident}: acceptance ID must identify its requirement")
        covered |= referenced
    if covered != use_cases:
        raise ValueError(f"use cases without requirements: {sorted(use_cases-covered)}")
    return len(ids)


def validate_measurement(payload, count, returncode):
    result = payload["result"]
    kd = result["k_drift"]
    if returncode != 0:
        raise ValueError(f"tumanomir failed (exit {returncode})")
    if kd["requirements"] != count or result["k_drift_verdict"] != "ok":
        raise ValueError("tumanomir did not validate every requirement (n/a is not success)")
    if kd["hanging"] != 0 or kd["value"] != 0:
        raise ValueError("untraced requirements")


def main():
    count = validate_contract(
        (ROOT / "docs/REQUIREMENTS.md").read_text(),
        (ROOT / "docs/HLD.md").read_text(),
        (ROOT / "docs/TRACEABILITY.md").read_text(),
    )
    local = ROOT / ".build/tumanomir"
    binary = os.environ.get("TUMANOMIR") or (str(local) if local.is_file() else shutil.which("tumanomir"))
    if not binary:
        raise ValueError("tumanomir missing; see docs/TRACEABILITY.md")
    run = subprocess.run(
        [binary, "check", "--format", "json", "--k-drift-max", "0", "docs/REQUIREMENTS.md"],
        cwd=ROOT, text=True, capture_output=True, timeout=30,
    )
    if run.returncode not in (0, 1):
        raise ValueError(f"tumanomir execution failed: {run.stderr.strip()}")
    payload = json.loads(run.stdout)
    validate_measurement(payload, count, run.returncode)
    dc = payload["result"]["d_const"]["value"]
    print(f"OK: {count} requirements traced to HLD/use cases/acceptance; K_drift=0; D_const={dc:.4f} (advisory).")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit(f"spec-check: {error}")
