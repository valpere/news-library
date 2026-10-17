#!/usr/bin/env python3
"""List, protect and (explicitly) prune data snapshots in snapshots/. Never touches data/."""
import argparse
import json
import re
from datetime import datetime, timezone
import os
from pathlib import Path
import shutil
import sqlite3
import sys

from ops_common import ROOT, config

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


def candidates(base=DIR, older_than=30, keep_last=5, now=None):
    """Unkept snapshots outside the newest keep_last and at least older_than days old."""
    rows = sorted((describe(n, base, now) for n in names(base)), key=lambda d: d['age_days'])
    return [d['name'] for i, d in enumerate(rows)
            if i >= keep_last and d['age_days'] >= older_than and not d['kept']]


def link_tree(src, dst):
    """Hardlink an immutable file tree (copy where linking is impossible); symlinks are skipped."""
    for f in src.rglob('*'):
        if f.is_symlink():
            continue
        target = dst / f.relative_to(src)
        if f.is_dir():
            target.mkdir(parents=True, exist_ok=True)
        elif f.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            try:
                os.link(f, target)
            except OSError:
                shutil.copy2(f, target)


def create(purpose, data, base=DIR, now=None):
    """Consistent SQLite copy plus hardlinked immutable texts/digests. Never writes to data."""
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,60}', purpose):
        raise ValueError('purpose: lowercase letters, digits and dashes')
    db = data / 'library.sqlite'
    if not db.is_file():
        raise ValueError(f'no library.sqlite in {data}')
    stamp = (now or datetime.now(timezone.utc)).strftime('%Y%m%d-%H%M%S')
    dst = base / f'{purpose}-{stamp}'
    base.mkdir(mode=0o700, exist_ok=True)
    dst.mkdir(mode=0o700)  # fails if it exists; never overwrites
    try:
        with sqlite3.connect(f'file:{db}?mode=ro', uri=True) as src, sqlite3.connect(dst / 'library.sqlite') as out:
            src.backup(out)
        for sub in ('texts', 'digests'):
            if (data / sub).is_dir():
                link_tree(data / sub, dst / sub)
        files = sum(1 for f in dst.rglob('*') if f.is_file())
        (dst / 'snapshot.json').write_text(json.dumps(
            {'purpose': purpose, 'created': stamp, 'schema': schema(dst), 'files': files}, indent=1) + '\n')
    except BaseException:
        shutil.rmtree(dst, ignore_errors=True)
        raise
    return dst.name


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest='cmd', required=True)
    ls = sub.add_parser('list')
    ls.add_argument('--older-than', type=int, default=30, help='days; candidate minimum age')
    ls.add_argument('--keep-last', type=int, default=5, help='newest N are never candidates')
    cr = sub.add_parser('create', help='new snapshot of the data dir (read-only on data)')
    cr.add_argument('purpose', help='e.g. before-migration-v16')
    cr.add_argument('--data', type=Path, help='default: NEWS_LIBRARY_DATA from .env')
    for c in ('keep', 'unkeep'):
        sub.add_parser(c).add_argument('name')
    pr = sub.add_parser('prune')
    pr.add_argument('names', nargs='+')
    pr.add_argument('--apply', action='store_true', help='actually delete (default: dry run)')
    args = ap.parse_args(argv)
    if args.cmd == 'list':
        cand = candidates(DIR, args.older_than, args.keep_last)
        for name in names():
            d = describe(name)
            flag = 'KEPT' if d['kept'] else ('candidate' if name in cand else '')
            print(f"{d['name']}\t{d['mb']}MB\t{d['age_days']}d\tschema={d['schema']}\t{flag}")
    elif args.cmd == 'create':
        data = args.data or ROOT / config(ROOT / '.env')['NEWS_LIBRARY_DATA']
        print('created ' + create(args.purpose, Path(data)))
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
