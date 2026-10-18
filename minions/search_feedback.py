#!/usr/bin/env python3
"""Summarise search relevance judgments and export them as search_eval cases. Read-only."""
import argparse
from collections import defaultdict
import json
from pathlib import Path
import sys

from ops_common import ROOT, database, new_output


def load(data):
    """Events with latest judgment per item; result_ids keep the shown order."""
    with database(data) as db:
        events = [dict(r) for r in db.execute(
            'SELECT id,query,mode,actual_mode,filters,result_ids,created_at,not_found_at FROM search_events ORDER BY id')]
        latest = db.execute('SELECT event_id,item_id,score FROM search_judgments WHERE id IN '
                            '(SELECT max(id) FROM search_judgments GROUP BY event_id,item_id)').fetchall()
    judged = defaultdict(dict)
    for r in latest:
        judged[r['event_id']][r['item_id']] = r['score']
    for e in events:
        e['result_ids'] = json.loads(e['result_ids'])
        e['judgments'] = judged.get(e['id'], {})
    return events


def summary(events):
    """Per requested mode: searches, judged ones, not-found marks and judged-result precision."""
    modes = defaultdict(lambda: {'searches': 0, 'judged_searches': 0, 'not_found': 0, 'results_judged': 0,
                                 'exact': 0, 'partial': 0, 'irrelevant': 0, 'first_exact_ranks': []})
    for e in events:
        m = modes[e['mode']]
        m['searches'] += 1
        m['not_found'] += bool(e['not_found_at'])
        if e['judgments']:
            m['judged_searches'] += 1
        for score in e['judgments'].values():
            m['results_judged'] += 1
            m[('irrelevant', 'partial', 'exact')[score]] += 1
        ranks = [i + 1 for i, item in enumerate(e['result_ids']) if e['judgments'].get(item) == 2]
        if ranks:
            m['first_exact_ranks'].append(ranks[0])
    for m in modes.values():
        ranks = m.pop('first_exact_ranks')
        m['mrr_exact'] = round(sum(1 / r for r in ranks) / m['judged_searches'], 3) if m['judged_searches'] else None
        m['precision_exact'] = round(m['exact'] / m['results_judged'], 3) if m['results_judged'] else None
    return dict(modes)


def cases(events):
    """One search_eval case per judged search; expected = results judged 'exactly'."""
    out = []
    for e in events:
        expected = [i for i, s in e['judgments'].items() if s == 2]
        if expected:
            out.append({'query': e['query'], 'modes': sorted({'fts', e['mode']}), 'expected_ids': expected})
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--data', type=Path, default=ROOT / 'data')
    ap.add_argument('--output', type=Path, required=True, help='new directory under project tmp/')
    args = ap.parse_args()
    try:
        events = load(args.data)
        out = new_output(args.output)
    except (OSError, ValueError) as exc:
        print(f'Failed: {exc}', file=sys.stderr)
        return 1
    report = {'summary': summary(events), 'events': len(events)}
    (out / 'summary.json').write_text(json.dumps(report, ensure_ascii=False, indent=1))
    (out / 'cases.json').write_text(json.dumps(cases(events), ensure_ascii=False, indent=1))
    print(json.dumps(report, ensure_ascii=False))
    return 0


if __name__ == '__main__':
    sys.exit(main())
