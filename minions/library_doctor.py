#!/usr/bin/env python3
"""Read-only current progress and whitelisted config diagnostics; optional local probes."""
import argparse
import json
from pathlib import Path
import re
import socket
import sqlite3
import sys
from urllib.parse import urlsplit
import ops_common as ops
from failure_triage import inspect


def diagnose(settings, data, probe=False):
    result = ops.report('library-doctor')
    checks = {}
    for key in ('NEWS_LIBRARY_NO_WORKER', 'NEWS_LIBRARY_NO_DIGESTS', 'NEWS_LIBRARY_KNOWLEDGE',
                'NEWS_LIBRARY_EMBEDDINGS', 'NEWS_LIBRARY_TELEGRAM_ENABLED', 'NEWS_LIBRARY_OPENROUTER_ENABLED'):
        value = settings[key].lower()
        checks[key] = value if value in ('true', 'false', '1', '0', 't', 'f') else 'invalid_boolean'
    enabled = settings['NEWS_LIBRARY_OPENROUTER_ENABLED'].lower() in ('true', '1', 't')
    try:
        models = json.loads(settings['NEWS_LIBRARY_OPENROUTER_MODELS'])
        valid = isinstance(models, list) and len(models) <= 3 and all(
            isinstance(m, str) and len(m) <= 256 and re.fullmatch(r'[a-zA-Z0-9_-]+/[a-zA-Z0-9][a-zA-Z0-9._-]*:free', m)
            and not m.lower().startswith('openrouter/') for m in models) and len(set(models)) == len(models)
    except (ValueError, TypeError):
        valid, models = False, []
    policy = settings['NEWS_LIBRARY_OPENROUTER_DATA_COLLECTION']
    result['configuration'] = {'booleans': checks,
        'read_token_present': bool(settings['NEWS_LIBRARY_READ_TOKEN']),
        'openrouter_key_present': bool(settings['OPENROUTER_API_KEY']),
        'openrouter_models_valid': valid and (not enabled or bool(models)),
        'openrouter_policy': policy if policy in ('deny', 'allow') else 'invalid',
        'scope': 'env/default checks only; runtime command flags and cloud availability unknown'}
    current = inspect(data)
    result['progress'] = current['summary']
    with ops.database(data) as db:
        result['run_history_counts'] = {table: {r['state']: r['n'] for r in db.execute(
            f'SELECT r.state,count(*) AS n FROM {table} r JOIN active_items i ON i.id=r.item_id GROUP BY r.state')}
            for table in ('jobs', 'knowledge_runs')}
    result['restored_workers_paused'] = (Path(data) / '.restore-paused').exists()
    if probe:
        probes = {}
        try:
            bind = ops.loopback_url('http://' + settings['NEWS_LIBRARY_BIND'])
            with socket.create_connection((bind.hostname, bind.port or 80), timeout=2):
                probes['web_socket'] = 'reachable'
        except (OSError, ValueError):
            probes['web_socket'] = 'unavailable_or_invalid_loopback_bind'
        try:
            u = ops.loopback_url(settings['OLLAMA_API_URL'])
            models = ops.get_json(f'http://{u.netloc}/api/tags').get('models', [])
            probes['ollama_tags'] = 'reachable'
            requested = settings['OLLAMA_MODEL']
            target = requested.removesuffix('-cloud')
            registered = False
            for model in models:
                if not isinstance(model, dict):
                    continue
                host = urlsplit(model.get('remote_host', ''))
                name = model.get('name', '')
                if host.scheme == 'https' and host.hostname == 'ollama.com' and not host.username and model.get('remote_model') and (
                    name == requested or (model.get('remote_model') == target and name.endswith(('-cloud', ':cloud')))):
                    registered = True
            probes['primary_cloud_model_registered'] = registered and requested.endswith(('-cloud', ':cloud'))
            # Listing is not evidence of cloud generation availability.
        except (OSError, ValueError, TypeError, AttributeError):
            probes['ollama_tags'] = 'unavailable_or_invalid_response'
        result['probes'] = probes
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--data', type=Path)
    p.add_argument('--env', type=Path, default=ops.ROOT / '.env')
    p.add_argument('--probe-local', action='store_true')
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    try:
        settings = ops.config(args.env)
        result = diagnose(settings, ops.data_path(args, settings), args.probe_local)
        ops.save(result, ops.new_output(args.output))
    except (OSError, ValueError, sqlite3.Error) as exc:
        print(f'Diagnosis failed ({type(exc).__name__}); no service started.', file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False))
    return 0


if __name__ == '__main__':
    sys.exit(main())
