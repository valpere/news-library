#!/usr/bin/env python3
"""Plan current incomplete stages without retries, models or database writes."""
import argparse
from collections import Counter
import json
from pathlib import Path
import sqlite3
import sys
import ops_common as ops


ADVICE = {
    'temporary': 'Можливий явний повтор після перевірки доступності сервісу.',
    'format': 'Перевірити збережену відповідь; повтор без виправлення може нічого не змінити.',
    'configuration': 'Виправити конфігурацію перед повтором.',
    'limit': 'Перевірити ліміти або обробку частинами перед повтором.',
    'source': 'Потрібен придатний текст або нове отримання джерела.',
    'unknown': 'Переглянути діагностику; автоматичний повтор не рекомендований.',
}


def inspect(data):
    rows = []
    with ops.database(data) as db:
        for item in db.execute('SELECT id,state,filter_state,selected_snapshot FROM active_items ORDER BY id'):
            iid, sid = item['id'], item['selected_snapshot']
            source = db.execute('SELECT id FROM source_versions WHERE id=? AND item_id=?', (sid, iid)).fetchone()
            digest = db.execute('SELECT id FROM generated_artifacts WHERE item_id=? AND snapshot_id=? ORDER BY id DESC LIMIT 1', (iid, sid)).fetchone()
            did = digest['id'] if digest else None
            metadata = db.execute('SELECT id FROM article_knowledge WHERE item_id=? AND snapshot_id=? AND digest_id=? ORDER BY id DESC LIMIT 1', (iid, sid, did)).fetchone()
            analysis = db.execute("SELECT id FROM knowledge_runs WHERE item_id=? AND snapshot_id=? AND digest_id=? AND state='done' LIMIT 1", (iid, sid, did)).fetchone()
            completed = {'source': bool(source), 'digest': bool(digest), 'metadata': bool(metadata), 'analysis': bool(analysis)}
            entry = {'item_id': iid, 'state': item['state'], 'snapshot_id': sid,
                     'digest_id': did, 'completed': completed, 'issues': []}
            rows.append(entry)
            if item['filter_state'] == 'rejected':
                entry['excluded_by_filter'] = True
                continue
            stage = 'fetch' if not source or item['state'] == 'needs_input' else 'digest' if not digest else 'knowledge'
            latest = db.execute('SELECT stage,state FROM jobs WHERE item_id=? ORDER BY id DESC LIMIT 1', (iid,)).fetchone()
            if latest and latest['stage'] == 'fetch' and (latest['state'] in ('queued', 'running', 'retry_wait') or
                    latest['state'] == 'failed' and item['state'] == 'failed'):
                stage = 'fetch'
            if stage == 'knowledge':
                run = db.execute('SELECT id,state,kind FROM knowledge_runs WHERE item_id=? AND snapshot_id=? AND digest_id=? ORDER BY id DESC LIMIT 1', (iid, sid, did)).fetchone()
                if analysis:
                    continue
            else:
                query = 'SELECT id,state FROM jobs WHERE item_id=? AND stage=?'
                params = [iid, stage]
                if stage == 'digest':
                    query += ' AND snapshot_id=?'
                    params.append(sid)
                run = db.execute(query + ' ORDER BY id DESC LIMIT 1', params).fetchone()
            if run and run['state'] in ('queued', 'running', 'retry_wait'):
                issue = {'stage': stage, 'status': run['state'], 'action': 'Дочекатися або перевірити worker; не створювати дубль.'}
            elif stage == 'fetch' and item['state'] == 'needs_input':
                issue = {'stage': stage, 'status': 'needs_input', 'kind': 'source', 'action': ADVICE['source']}
            elif run and run['state'] == 'failed':
                if stage == 'knowledge':
                    kind = run['kind'] if run['kind'] in ADVICE else 'unknown'
                    retryable = kind == 'temporary'
                else:
                    failure = db.execute('SELECT kind,retryable FROM job_failures WHERE job_id=? ORDER BY id DESC LIMIT 1', (run['id'],)).fetchone()
                    kind = failure['kind'] if failure and failure['kind'] in ADVICE else 'unknown'
                    retryable = bool(failure and failure['retryable'])
                issue = {'stage': stage, 'status': 'failed', 'run_id': run['id'], 'kind': kind,
                         'retry_candidate': kind == 'temporary' and retryable, 'action': ADVICE[kind]}
            else:
                issue = {'stage': stage, 'status': 'incomplete', 'action': 'Перевірити або явно дообробити лише відсутній етап.'}
            entry['issues'].append(issue)
    result = ops.report('failure-triage')
    issues = [issue for row in rows for issue in row['issues']]
    result.update({'scope': 'current active selections; read-only; historical failures are not retried',
                   'summary': {'active_items': len(rows), 'items_needing_attention': sum(bool(r['issues']) for r in rows),
                               'completed_stages': {stage: sum(r['completed'][stage] for r in rows) for stage in ('source', 'digest', 'metadata', 'analysis')},
                               'issues_by_stage': dict(Counter(i['stage'] for i in issues)),
                               'retry_candidates': sum(i.get('retry_candidate', False) for i in issues)},
                   'items': rows})
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--data', type=Path)
    p.add_argument('--env', type=Path, default=ops.ROOT / '.env')
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    try:
        settings = ops.config(args.env)
        result = inspect(ops.data_path(args, settings))
        ops.save(result, ops.new_output(args.output))
    except (OSError, ValueError, sqlite3.Error) as exc:
        print(f'Triage failed ({type(exc).__name__}); no changes applied.', file=sys.stderr)
        return 1
    print(json.dumps(result['summary'], ensure_ascii=False))
    return 0


if __name__ == '__main__':
    sys.exit(main())
