#!/usr/bin/env python3
"""Run offline checks and validate only committed public files in ./tmp/."""
import argparse
import io
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(argv, cwd=ROOT, env=None):
    print("+ " + " ".join(map(str, argv)), flush=True)
    subprocess.run(argv, cwd=cwd, env=env, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--public-only", action="store_true",
                        help="Skip local make check; validate committed public docs/spec only")
    parser.add_argument("--ref", default="HEAD", help="Committed tree to archive (default HEAD)")
    args = parser.parse_args()
    if not args.public_only:
        run(["make", "check"])
    # Resolve the ref to an immutable commit before archiving. Never include
    # untracked research, runtime data or private .env in the public fixture.
    commit = subprocess.check_output(
        ["git", "rev-parse", "--verify", "--end-of-options", args.ref + "^{commit}"],
        cwd=ROOT, text=True).strip()
    binary = os.environ.get("TUMANOMIR") or str(ROOT / ".build/tumanomir")
    if not Path(binary).is_file():
        binary = shutil.which(binary) or shutil.which("tumanomir")
    if not binary:
        raise ValueError("tumanomir missing; see docs/TRACEABILITY.md")
    env = os.environ.copy()
    env["TUMANOMIR"] = str(Path(binary).resolve())
    scratch = ROOT / "tmp"
    scratch.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="public-check-", dir=scratch) as directory:
        archive = subprocess.check_output(["git", "archive", commit], cwd=ROOT)
        with tarfile.open(fileobj=io.BytesIO(archive)) as contents:
            contents.extractall(directory, filter="data")
        for script in ("scripts/check_project.py", "scripts/check_spec.py"):
            run([sys.executable, script], cwd=directory, env=env)
    print(f"PASS: local/public checks requested; public commit {commit}")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(error.returncode)
    except (ValueError, OSError, tarfile.TarError) as error:
        raise SystemExit(f"project-check: {error}")
