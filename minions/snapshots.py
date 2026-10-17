#!/usr/bin/env python3
"""List, protect and (explicitly) prune data snapshots in snapshots/. Never touches data/."""
import argparse
from datetime import datetime, timezone
import os
from pathlib import Path
import shutil
import sqlite3
import sys

from ops_common import ROOT

DIR = ROOT / 'snapshots'
KEEP = '.keep'


def names(base=DIR):
    """Snapshot directories only: real dirs, no symlinks, direct children."""
    if not base.is_dir():
        return []
    return sorted(p.name for p in base.iterdir() if p.is_dir() and not p.is_symlink())


def kept(name, base=DIR):
    return (base / (name + KEEP)).exists()


def size_mb(path):
    return sum(f.stat().st_size for f in path.rglob('*') if f.is_file() and not f.is_symlink()) / 1e6


def schema(path):
    db = path / 'library.sqlite'
    if not db.is_file():
        return None
    try:
        with sqlite3.connect(f'file:{db}?mode=ro', uri=True) as con:
            return con.execute('PRAGMA user_version').fetchone()[0]
    except sqlite3.Error:
        return None


def describe(name, base=DIR, now=None):
    path = base / name
    now = now or datetime.now(timezone.utc).timestamp()
    return {'name': name, 'mb': round(size_mb(path)), 'age_days': int((now - path.stat().st_mtime) / 86400),
            'schema': schema(path), 'kept': kept(name, base)}


def set_keep(name, on, base=DIR):
    if name not in names(base):
        raise ValueError(f'unknown snapshot {name}')
    marker = base / (name + KEEP)
    if on:
        marker.touch()
    else:
        marker.unlink(missing_ok=True)


def prune(selected, apply, base=DIR):
    """Dry-run by default. Kept or unknown names are refused; nothing is chosen automatically."""
    out = []
    for name in selected:
        if name not in names(base):
            out.append(f'refused {name}: not a snapshot directory')
        elif kept(name, base):
            out.append(f'refused {name}: kept (run unkeep first)')
        elif apply:
            shutil.rmtree(base / name)
            out.append(f'removed {name}')
        else:
            out.append(f'would remove {name}')
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest='cmd', required=True)
    ls = sub.add_parser('list')
    ls.add_argument('--older-than', type=int, default=30, help='days; marks unkept ones as candidates')
    for c in ('keep', 'unkeep'):
        sub.add_parser(c).add_argument('name')
    pr = sub.add_parser('prune')
    pr.add_argument('names', nargs='+')
    pr.add_argument('--apply', action='store_true', help='actually delete (default: dry run)')
    args = ap.parse_args(argv)
    if args.cmd == 'list':
        for name in names():
            d = describe(name)
            flag = 'KEPT' if d['kept'] else ('candidate' if d['age_days'] >= args.older_than else '')
            print(f"{d['name']}\t{d['mb']}MB\t{d['age_days']}d\tschema={d['schema']}\t{flag}")
    elif args.cmd in ('keep', 'unkeep'):
        set_keep(args.name, args.cmd == 'keep')
        print(f'{args.cmd} {args.name}')
    else:
        lines = prune(args.names, args.apply)
        print('\n'.join(lines))
        return 1 if any(l.startswith('refused') for l in lines) else 0
    return 0


if __name__ == '__main__':
    sys.exit(main())
