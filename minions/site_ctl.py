#!/usr/bin/env python3
"""Explicit start/stop/status of the local news-library site (user-requested only)."""
import argparse
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

from ops_common import ROOT, config

STATE = ROOT / 'tmp' / 'site'
BINARY = ROOT / 'build' / 'news-library'


def read_pid(state=STATE):
    try:
        return int((state / 'pid').read_text().strip())
    except (OSError, ValueError):
        return None


def is_ours(pid, binary=BINARY):
    """True only if pid is alive and runs our binary (guards against pid reuse)."""
    try:
        return os.path.realpath(f'/proc/{pid}/exe') == os.path.realpath(binary)
    except OSError:
        return False


def status(state=STATE, binary=BINARY):
    pid = read_pid(state)
    return pid if pid and is_ours(pid, binary) else None


def start(extra, state=STATE, binary=BINARY):
    if status(state, binary):
        return 'already running'
    if not Path(binary).is_file():
        return 'binary missing: run make build'
    state.mkdir(parents=True, exist_ok=True)
    with open(state / 'agent.log', 'ab') as log:
        proc = subprocess.Popen([str(binary), *extra], cwd=ROOT, stdout=log, stderr=log,
                                stdin=subprocess.DEVNULL, start_new_session=True)
    (state / 'pid').write_text(f'{proc.pid}\n')
    return f'started pid {proc.pid}'


def stop(state=STATE, binary=BINARY, timeout=30):
    pid = status(state, binary)
    if not pid:
        return 'not running'
    os.kill(pid, signal.SIGTERM)  # graceful only; never SIGKILL
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not is_ours(pid, binary):
            (state / 'pid').unlink(missing_ok=True)
            return f'stopped pid {pid}'
        time.sleep(0.2)
    return f'pid {pid} still running after {timeout}s; not killed'


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('action', choices=['status', 'start', 'stop'])
    ap.add_argument('--resume-restored', action='store_true',
                    help='pass -resume-restored (start only)')
    args = ap.parse_args(argv)
    if args.action == 'start':
        msg = start(['-resume-restored'] if args.resume_restored else [])
    elif args.action == 'stop':
        msg = stop()
    else:
        pid = status()
        msg = f'running pid {pid}, bind {config(ROOT / ".env")["NEWS_LIBRARY_BIND"]}' if pid else 'not running'
    print(msg)
    return 1 if msg.startswith('binary missing') or 'still running' in msg else 0


if __name__ == '__main__':
    sys.exit(main())
