#!/usr/bin/env python3
"""Offline inventory and review signals. Never open Store or run models/workers."""
import argparse
from collections import Counter, defaultdict
from contextlib import closing
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import sqlite3
import sys

from ops_common import SCHEMA_VERSION

ROOT = Path(__file__).resolve().parents[1]


def local_path(root, relative):
    """Reject escapes and symlinks, including intermediate directories."""
    path = Path(relative)
    if path.is_absolute() or not path.parts or '..' in path.parts:
        raise ValueError('invalid relative path')
    target = root
    for part in path.parts:
        target = target / part
        if target.is_symlink():
            raise ValueError('symlink refused')
    return target


def audit(data):
    data = Path(data).absolute()
    if any(p.is_symlink() for p in (data, *data.parents)):
        raise ValueError('data directory symlink refused')
    database = local_path(data, 'library.sqlite')
    if not database.is_file():
        raise ValueError('existing database required')
    problems, signals, items, notes = [], [], [], []
    text_cache = {}
    with closing(sqlite3.connect(database.as_uri() + '?mode=ro')) as db:
        db.row_factory = sqlite3.Row
        db.execute('PRAGMA query_only=ON')
        db.execute('BEGIN')
        if db.execute('PRAGMA user_version').fetchone()[0] != SCHEMA_VERSION:
            raise ValueError(f'schema v{SCHEMA_VERSION} required; audit never migrates')
        integrity = [r[0] for r in db.execute('PRAGMA integrity_check')]
        if integrity != ['ok']:
            problems.append({'kind': 'sqlite_integrity', 'details': integrity})
        for row in db.execute('PRAGMA foreign_key_check'):
            problems.append({'kind': 'foreign_key', 'details': list(row)})
        files = 0
        for table, column, prefix in (
            ('texts', 'text_path', 'texts'),
            ('digests', 'markdown_path', 'digests'),
            ('article_knowledge', 'markdown_path', 'digests'),
            ('connection_notes', 'markdown_path', 'digests'),
        ):
            key = 'item_id' if table in ('texts', 'digests') else 'id'
            for row in db.execute(f'SELECT {key},{column},hash FROM {table}'):
                ref = {'table': table, 'id': row[key], 'path': row[column]}
                try:
                    if Path(row[column]).parts[0] != prefix:
                        raise ValueError('unexpected primary directory')
                    path = local_path(data, row[column])
                    if not path.is_file() or path.stat().st_size > 64 << 20:
                        raise ValueError('missing or oversized primary file')
                    content = path.read_bytes()
                    if hashlib.sha256(content).hexdigest() != row['hash']:
                        raise ValueError('hash mismatch')
                    text_cache[(table, row[key])] = content.decode('utf-8')
                    files += 1
                except (OSError, ValueError, IndexError) as exc:
                    problems.append({'kind': 'primary_file', **ref, 'details': str(exc)})
        sources = {r['item_id']: dict(r) for r in db.execute('SELECT * FROM texts')}
        digests = {r['item_id']: dict(r) for r in db.execute('SELECT * FROM digests')}
        for item_id in digests:
            if item_id not in sources:
                problems.append({'kind': 'digest_without_text', 'item_id': item_id})
        active = {r['id']: dict(r) for r in db.execute('SELECT * FROM active_items')}
        completeness = Counter()
        for item in active.values():
            iid = item['id']
            source = sources.get(iid)
            if not source:
                problems.append({'kind': 'selected_source', 'item_id': iid})
                continue
            completeness[source['completeness']] += 1
            digest = digests.get(iid)
            if digest is None:
                signals.append({'kind': 'missing_digest', 'item_id': iid})
            src = text_cache.get(('texts', iid), '')
            md = text_cache.get(('digests', iid), '') if digest else ''
            sw, dw = len(src.split()), len(md.split())
            record = {'id': iid, 'title': item['title'],
                      'source_path': source['text_path'], 'digest_path': digest['markdown_path'] if digest else None,
                      'completeness': source['completeness'], 'warning': source['warning'],
                      'source_words': sw, 'digest_words': dw}
            items.append(record)
            if sw < 80:
                signals.append({'kind': 'short_source', 'item_id': iid, 'words': sw})
            if sw and dw > sw:
                signals.append({'kind': 'digest_longer_than_source', 'item_id': iid, 'ratio': round(dw / sw, 2)})
            metadata = db.execute('SELECT * FROM article_knowledge WHERE item_id=? ORDER BY id DESC LIMIT 1', (iid,)).fetchone()
            if metadata is None:
                signals.append({'kind': 'missing_current_metadata', 'item_id': iid})
        current = list(db.execute('SELECT * FROM connection_notes ORDER BY id'))
        current_ids = {r['id'] for r in current}
        members = defaultdict(list)
        for row in db.execute('SELECT * FROM connection_members ORDER BY note_id,item_id'):
            m = dict(row)
            members[m['note_id']].append(m)
            if m['item_id'] not in sources:
                problems.append({'kind': 'connection_ownership', **m})
            if m['note_id'] in current_ids and m['item_id'] not in active:
                signals.append({'kind': 'connection_inactive_member', **m})
        title_groups = defaultdict(list)
        for row in current:
            nid = row['id']
            if not 2 <= len(members[nid]) <= 20:
                problems.append({'kind': 'connection_member_count', 'note_id': nid})
            title_groups[row['title'].strip().casefold()].append(nid)
            notes.append({'note_id': nid, 'title': row['title'],
                          'path': row['markdown_path'], 'members': members[nid]})
        for ids in title_groups.values():
            if len(ids) > 1:
                signals.append({'kind': 'repeated_connection_title', 'note_ids': ids})
        return {'schema': 'news-library/corpus-audit-v1', 'created_at': datetime.now(timezone.utc).isoformat(),
                'scope': 'offline read-only; signals are not quality or usefulness ratings',
                'summary': {'active_items': len(active), 'current_connections': len(current),
                            'verified_primary_references': files, 'source_completeness': dict(completeness),
                            'integrity_problems': len(problems), 'review_signal_counts': dict(Counter(s['kind'] for s in signals))},
                'problems': problems, 'signals': signals, 'items': items, 'connections': notes}


def save_report(report, output):
    # Corpus titles/references stay private. Refuse existing output and symlink parents.
    tmp = ROOT / 'tmp'
    if tmp.is_symlink():
        raise ValueError('tmp symlink refused')
    output = Path(output).absolute()
    relative = output.relative_to(tmp)
    if not relative.parts:
        raise ValueError('use a new subdirectory of tmp')
    output = local_path(tmp, relative)
    output.mkdir(parents=True, exist_ok=False)
    output.chmod(0o700)
    (output / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    lines = ['# Аудит збережених матеріалів', '',
             'Лише читання, без моделей. Сигнали потребують перегляду; це не оцінки якості або корисності.', '',
             '```json', json.dumps(report['summary'], ensure_ascii=False, indent=2), '```', '',
             '## Проблеми цілісності', '', '```json', json.dumps(report['problems'], ensure_ascii=False, indent=2), '```', '',
             '## Сигнали для перегляду', '']
    for signal in report['signals']:
        lines.append('- ' + json.dumps(signal, ensure_ascii=False))
    lines += ['', '## Найкоротші джерела', '', '| Item | Source words | Digest words |', '|---|---:|---:|']
    for item in sorted(report['items'], key=lambda i: i['source_words'])[:20]:
        lines.append(f"| {item['id']} | {item['source_words']} | {item['digest_words']} |")
    (output / 'report.md').write_text('\n'.join(lines) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data', type=Path, default=ROOT / 'data')
    parser.add_argument('--output', type=Path, required=True, help='new directory under project tmp/')
    args = parser.parse_args()
    try:
        report = audit(args.data)
        save_report(report, args.output)
    except (OSError, ValueError, sqlite3.Error) as exc:
        print(f'Audit failed: {exc}', file=sys.stderr)
        return 1
    print(json.dumps(report['summary'], ensure_ascii=False))
    return 1 if report['problems'] else 0


if __name__ == '__main__':
    sys.exit(main())
