#!/usr/bin/env python3
"""Explicit live cloud digest smoke using a separate store under ./tmp/."""
import argparse
import json
from pathlib import Path
import re
import selectors
import signal
import sqlite3
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
EXAMPLE = "https://ollama.com/blog/ollama-now-supports-jev-style-decision-models"


def inspect(db, data, prediction):
    row = db.execute("SELECT state,error FROM items WHERE id=1").fetchone()
    if not row:
        return None
    if row[0] in ("needs_input", "failed", "digest_failed"):
        raise ValueError(f"fixture processing failed: {row[0]}")
    result = db.execute("SELECT item_id,markdown_path,actual_model,provider,prompt_version FROM digests WHERE item_id=1").fetchone()
    if row[0] != "ready" or not result:
        return None
    markdown = (data / result[1]).read_text()
    output = {"state": row[0], "actual_model": result[2], "provider": result[3],
              "prompt_version": result[4], "data": str(data), "words": len(markdown.split()),
              "sections": [line for line in markdown.splitlines() if line.startswith("## ")]}
    if prediction:
        predicted = db.execute("SELECT probability FROM interest_predictions WHERE item_id=?", (result[0],)).fetchone()
        if not predicted:
            raise ValueError("ready digest lacks the requested fixture prediction")
        output["fixture_confidence"] = predicted[0]
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-cloud", action="store_true", help="Explicitly run live source/cloud requests")
    parser.add_argument("--prediction", action="store_true", help="Use a synthetic test interest profile")
    parser.add_argument("--url", default=EXAMPLE)
    parser.add_argument("--timeout", type=int, default=420)
    args = parser.parse_args()
    if not args.run_cloud:
        parser.error("live requests require --run-cloud; excluded from make check")
    if args.timeout < 1:
        parser.error("timeout must be positive")
    scratch = ROOT / "tmp"
    scratch.mkdir(exist_ok=True)
    # Keep evidence for inspection, rather than deleting the generated fixture.
    data = Path(tempfile.mkdtemp(prefix="live-digest-", dir=scratch))
    interests = "ТЕСТОВИЙ ПРОФІЛЬ: API Ollama та моделі прийняття рішень." if args.prediction else ""
    command = [str(ROOT / "build/news-library"), "-data", str(data), "-bind", "127.0.0.1:0",
               "-embeddings=false", "-filter-rules", "[]", "-interests", interests, "-no-worker=false", "-no-digests=false"]
    # Application loads private .env itself; never source or print credentials.
    with (data / "service.log").open("w") as log:
        process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=log, text=True)
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                if not selector.select(timeout=15):
                    raise ValueError("fixture service did not start within 15 seconds")
                line = process.stdout.readline().strip()
            if not line.startswith("news-library: http://127.0.0.1:"):
                raise ValueError("fixture service startup failed; inspect its local service.log")
            origin = line.removeprefix("news-library: ")
            with urllib.request.urlopen(origin, timeout=15) as response:
                html = response.read().decode()
            token = re.search(r'name="csrf" value="([^"]+)"', html)
            if not token:
                raise ValueError("fixture UI lacks CSRF token")
            payload = urllib.parse.urlencode({"csrf": token.group(1), "url": args.url}).encode()
            with urllib.request.urlopen(urllib.request.Request(origin + "/items", data=payload), timeout=15) as response:
                response.read()
            deadline = time.monotonic() + args.timeout
            with sqlite3.connect(data / "library.sqlite") as db:
                while time.monotonic() < deadline:
                    result = inspect(db, data, args.prediction)
                    if result:
                        print(json.dumps(result, ensure_ascii=False, indent=2))
                        return
                    if process.poll() is not None:
                        raise ValueError("fixture service stopped unexpectedly")
                    time.sleep(2)
            raise ValueError("live digest smoke timed out; fixture retained under ./tmp/")
        finally:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            process.stdout.close()


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, sqlite3.Error) as error:
        raise SystemExit(f"live-digest-smoke: {error}")
