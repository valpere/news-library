#!/usr/bin/env python3
"""Evaluate an existing loopback read API with private queries and expected item IDs."""
import argparse
import json
from pathlib import Path
import sys
import time
from urllib.parse import urlencode
from urllib.error import HTTPError
import ops_common as ops

FILTERS = {'category', 'tags', 'read', 'state', 'usefulness', 'filter', 'sort'}


def cases(path):
    path = ops.no_symlinks(path)
    if not path.is_file() or path.stat().st_size > 1 << 20:
        raise ValueError('bounded regular cases file required')
    rows = json.loads(path.read_text())
    if not isinstance(rows, list) or not 1 <= len(rows) <= 100:
        raise ValueError('1..100 cases required')
    for row in rows:
        if not isinstance(row, dict) or set(row) - {'query', 'modes', 'expected_ids', 'limit', 'filters'}:
            raise ValueError('invalid case fields')
        q, modes, expected = row.get('query'), row.get('modes', ['fts']), row.get('expected_ids', [])
        limit, filters = row.get('limit', 20), row.get('filters', {})
        if not isinstance(q, str) or not q.strip() or len(q.encode()) > 4000:
            raise ValueError('bounded nonempty query required')
        if not isinstance(modes, list) or not 1 <= len(modes) <= 3 or any(m not in ('fts', 'vector', 'hybrid') for m in modes):
            raise ValueError('invalid search modes')
        if not isinstance(expected, list) or any(type(i) is not int or i <= 0 for i in expected):
            raise ValueError('positive expected IDs required')
        if type(limit) is not int or not 1 <= limit <= 100:
            raise ValueError('limit must be 1..100')
        if not isinstance(filters, dict) or set(filters) - FILTERS or any(not isinstance(v, str) or len(v) > 1000 for v in filters.values()):
            raise ValueError('invalid filters')
    return rows


def evaluate(rows, base_url, token):
    base = ops.loopback_url(base_url)
    if base.path not in ('', '/'):
        raise ValueError('base URL must have no path')
    if not 32 <= len(token.encode()) <= 256 or any(c.isspace() or ord(c) < 32 for c in token):
        raise ValueError('configured valid read token required')
    results = []
    for number, row in enumerate(rows, 1):
        for mode in row.get('modes', ['fts']):
            start = time.monotonic()
            entry = {'case': number, 'query': row['query'], 'requested_mode': mode,
                     'filters': row.get('filters', {}), 'limit': row.get('limit', 20),
                     'expected_ids': sorted(set(row.get('expected_ids', [])))}
            try:
                params = {'q': row['query'], 'mode': mode, 'limit': row.get('limit', 20), **row.get('filters', {})}
                payload = ops.get_json(f'http://{base.netloc}/api/read/search?' + urlencode(params),
                                      {'Authorization': 'Bearer ' + token}, timeout=60)
                items = payload.get('items')
                if not isinstance(items, list) or len(items) > params['limit'] or any(not isinstance(i, dict) or type(i.get('id')) is not int or i['id'] <= 0 for i in items):
                    raise ValueError('invalid search response')
                ids = [i['id'] for i in items]
                if len(set(ids)) != len(ids) or payload.get('mode') not in ('fts', 'vector', 'hybrid') or not isinstance(payload.get('warning'), str) or any(
                    type(payload.get(key)) is not int or payload[key] < 0 for key in ('indexed', 'eligible')) or type(payload.get('truncated')) is not bool:
                    raise ValueError('invalid search metadata')
                expected = set(row.get('expected_ids', []))
                ranks = [rank for rank, iid in enumerate(ids, 1) if iid in expected]
                entry.update({'status': 'ok', 'ids': ids, 'actual_mode': payload.get('mode'),
                              'warning': payload.get('warning'), 'indexed': payload.get('indexed'),
                              'eligible': payload.get('eligible'), 'truncated': payload.get('truncated'),
                              'recall_at_limit': len(expected.intersection(ids)) / len(expected) if expected else None,
                              'reciprocal_rank': 1 / min(ranks) if ranks else 0 if expected else None})
            except HTTPError as exc:
                entry.update({'status': 'failed', 'error': f'HTTP {exc.code}'})
            except (OSError, ValueError, TypeError, AttributeError) as exc:
                # Do not copy response bodies, URLs, tokens or exception strings into evidence.
                entry.update({'status': 'failed', 'error': type(exc).__name__})
            entry['duration_ms'] = round((time.monotonic() - start) * 1000, 2)
            results.append(entry)
    result = ops.report('search-eval')
    result.update({'results': results, 'passed': all(r['status'] == 'ok' for r in results),
                   'scope': 'GET read API; FTS no inference; vector/hybrid local embeddings only; relevance needs user-selected expected IDs'})
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cases', type=Path, required=True)
    p.add_argument('--env', type=Path, default=ops.ROOT / '.env')
    p.add_argument('--base-url')
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    try:
        settings = ops.config(args.env)
        rows = cases(args.cases)
        output = ops.new_output(args.output)
        result = evaluate(rows, args.base_url or 'http://' + settings['NEWS_LIBRARY_BIND'], settings['NEWS_LIBRARY_READ_TOKEN'])
        ops.save(result, output)
    except (OSError, ValueError) as exc:
        print(f'Search evaluation failed ({type(exc).__name__}); site not started.', file=sys.stderr)
        return 1
    print(json.dumps({'requests': len(result['results']), 'passed': result['passed']}))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
