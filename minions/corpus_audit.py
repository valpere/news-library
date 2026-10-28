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
            ('source_versions', 'text_path', 'texts'),
            ('generated_artifacts', 'markdown_path', 'digests'),
            ('article_knowledge', 'markdown_path', 'digests'),
            ('connection_versions', 'markdown_path', 'digests'),
        ):
            for row in db.execute(f'SELECT id,{column},hash FROM {table}'):
                ref = {'table': table, 'id': row['id'], 'path': row[column]}
                try:
                    if Path(row[column]).parts[0] != prefix:
                        raise ValueError('unexpected primary directory')
                    path = local_path(data, row[column])
                    if not path.is_file() or path.stat().st_size > 64 << 20:
                        raise ValueError('missing or oversized primary file')
                    content = path.read_bytes()
                    if hashlib.sha256(content).hexdigest() != row['hash']:
                        raise ValueError('hash mismatch')
                    text_cache[(table, row['id'])] = content.decode('utf-8')
                    files += 1
                except (OSError, ValueError, IndexError) as exc:
                    problems.append({'kind': 'primary_file', **ref, 'details': str(exc)})
        sources = {r['id']: dict(r) for r in db.execute('SELECT * FROM source_versions')}
        digests = {r['id']: dict(r) for r in db.execute('SELECT * FROM generated_artifacts')}
        for d in digests.values():
            s = sources.get(d['snapshot_id'])
            if not s or s['item_id'] != d['item_id']:
                problems.append({'kind': 'digest_ownership', 'digest_id': d['id']})
        for k in db.execute('SELECT * FROM article_knowledge'):
            s, d = sources.get(k['snapshot_id']), digests.get(k['digest_id'])
            if not s or s['item_id'] != k['item_id'] or not d or d['item_id'] != k['item_id'] or d['snapshot_id'] != k['snapshot_id']:
                problems.append({'kind': 'metadata_ownership', 'metadata_id': k['id']})
        active = {r['id']: dict(r) for r in db.execute('SELECT * FROM active_items')}
        completeness = Counter()
        for item in active.values():
            iid = item['id']
            source = sources.get(item['selected_snapshot'])
            if not source or source['item_id'] != iid:
                problems.append({'kind': 'selected_source', 'item_id': iid})
                continue
            completeness[source['completeness']] += 1
            choices = [d for d in digests.values() if d['snapshot_id'] == source['id'] and d['item_id'] == iid]
            digest = max(choices, key=lambda d: d['id']) if choices else None
            if digest is None:
                signals.append({'kind': 'missing_digest', 'item_id': iid})
            src = text_cache.get(('source_versions', source['id']), '')
            md = text_cache.get(('generated_artifacts', digest['id']), '') if digest else ''
            sw, dw = len(src.split()), len(md.split())
            record = {'id': iid, 'title': item['title'], 'snapshot_id': source['id'],
                      'digest_id': digest['id'] if digest else None,
                      'source_path': source['text_path'], 'digest_path': digest['markdown_path'] if digest else None,
                      'completeness': source['completeness'], 'warning': source['warning'],
                      'source_words': sw, 'digest_words': dw}
            items.append(record)
            if sw < 80:
                signals.append({'kind': 'short_source', 'item_id': iid, 'words': sw})
            if sw and dw > sw:
                signals.append({'kind': 'digest_longer_than_source', 'item_id': iid, 'ratio': round(dw / sw, 2)})
            metadata = db.execute('SELECT * FROM article_knowledge WHERE item_id=? AND snapshot_id=? AND digest_id=? ORDER BY id DESC LIMIT 1',
                                  (iid, source['id'], digest['id'] if digest else -1)).fetchone()
            if metadata is None:
                signals.append({'kind': 'missing_current_metadata', 'item_id': iid})
        current = list(db.execute('SELECT v.* FROM connection_versions v WHERE v.id=(SELECT v2.id FROM connection_versions v2 WHERE v2.note_id=v.note_id ORDER BY revision DESC LIMIT 1) ORDER BY v.id'))
        current_ids = {r['id'] for r in current}
        members = defaultdict(list)
        for row in db.execute('SELECT * FROM connection_members ORDER BY version_id,item_id'):
            m = dict(row)
            members[m['version_id']].append(m)
            s, d = sources.get(m['snapshot_id']), digests.get(m['digest_id'])
            if not s or s['item_id'] != m['item_id'] or (m['digest_id'] is not None and
                    (not d or d['snapshot_id'] != m['snapshot_id'] or d['item_id'] != m['item_id'])):
                problems.append({'kind': 'connection_ownership', **m})
            if m['version_id'] in current_ids:
                if m['item_id'] not in active:
                    signals.append({'kind': 'connection_inactive_member', **m})
                elif active[m['item_id']]['selected_snapshot'] != m['snapshot_id']:
                    signals.append({'kind': 'connection_historical_source', **m})
        title_groups = defaultdict(list)
        for row in current:
            vid = row['id']
            if not 2 <= len(members[vid]) <= 20:
                problems.append({'kind': 'connection_member_count', 'version_id': vid})
            title_groups[row['title'].strip().casefold()].append(vid)
            notes.append({'note_id': row['note_id'], 'version_id': vid, 'title': row['title'],
                          'path': row['markdown_path'], 'members': members[vid]})
        for ids in title_groups.values():
            if len(ids) > 1:
                signals.append({'kind': 'repeated_connection_title', 'version_ids': ids})
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
