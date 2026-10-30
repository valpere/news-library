import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import threading
import unittest
from unittest.mock import patch

import backup_check
import failure_triage
import library_doctor
import ops_common as ops
import search_eval


class OperationsTests(unittest.TestCase):
    def setUp(self):
        (ops.ROOT / 'tmp').mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=ops.ROOT / 'tmp')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = self.root / 'data'
        self.data.mkdir()
        self.path = self.data / 'library.sqlite'
        with sqlite3.connect(self.path) as db:
            db.executescript('''
                PRAGMA user_version=1;
                CREATE TABLE items(id,state,filter_state,selected_snapshot);
                CREATE VIEW active_items AS SELECT * FROM items WHERE id!=99;
                CREATE TABLE source_versions(id,item_id);
                CREATE TABLE generated_artifacts(id,item_id,snapshot_id);
                CREATE TABLE article_knowledge(id,item_id,snapshot_id,digest_id);
                CREATE TABLE jobs(id,item_id,stage,snapshot_id,state);
                CREATE TABLE job_failures(id,job_id,kind,retryable);
                CREATE TABLE knowledge_runs(id,item_id,snapshot_id,digest_id,state,kind);
                INSERT INTO items VALUES(1,'ready','passed',11),(2,'failed','passed',22),
                  (3,'failed','passed',33),(4,'needs_input','passed',44),(99,'failed','passed',NULL);
                INSERT INTO source_versions VALUES(11,1),(22,2),(33,3),(44,4);
                INSERT INTO generated_artifacts VALUES(1,1,11),(2,2,22);
                INSERT INTO article_knowledge VALUES(1,1,11,1),(2,2,22,2);
                INSERT INTO jobs VALUES(1,1,'digest',11,'failed'),(2,3,'digest',3,'failed'),(3,3,'digest',33,'failed');
                INSERT INTO job_failures VALUES(1,1,'format',0),(2,2,'format',0),(3,3,'temporary',1);
                INSERT INTO knowledge_runs VALUES(1,1,11,1,'done',''),(2,2,22,2,'failed','format');
            ''')

    def test_completed_stages_old_snapshot_and_trash_not_retried(self):
        before = hashlib.sha256(self.path.read_bytes()).hexdigest()
        r = failure_triage.inspect(self.data)
        items = {i['item_id']: i for i in r['items']}
        self.assertEqual({1, 2, 3, 4}, set(items))
        self.assertEqual([], items[1]['issues'])
        self.assertTrue(items[2]['completed']['metadata'])
        self.assertEqual('knowledge', items[2]['issues'][0]['stage'])
        self.assertFalse(items[2]['issues'][0]['retry_candidate'])
        self.assertTrue(items[3]['issues'][0]['retry_candidate'])
        self.assertEqual('source', items[4]['issues'][0]['kind'])
        self.assertEqual(before, hashlib.sha256(self.path.read_bytes()).hexdigest())

    def test_waiting_and_rejected_do_not_offer_retry(self):
        with sqlite3.connect(self.path) as db:
            db.execute("UPDATE jobs SET state='retry_wait' WHERE id=3")
            db.execute("UPDATE items SET filter_state='rejected' WHERE id=4")
        r = failure_triage.inspect(self.data)
        self.assertEqual(0, r['summary']['retry_candidates'])
        self.assertEqual([], r['items'][3]['issues'])
        self.assertEqual('retry_wait', r['items'][2]['issues'][0]['status'])

    def test_missing_database_and_wrong_schema_are_not_created_or_migrated(self):
        with self.assertRaises(ValueError):
            failure_triage.inspect(self.root / 'missing')
        self.assertFalse((self.root / 'missing').exists())
        with sqlite3.connect(self.path) as db:
            db.execute('PRAGMA user_version=14')
        with self.assertRaises(ValueError):
            failure_triage.inspect(self.data)
        with sqlite3.connect(self.path) as db:
            self.assertEqual(14, db.execute('PRAGMA user_version').fetchone()[0])

    def test_config_secrets_do_not_appear_in_doctor_no_network_default(self):
        env = self.root / '.env'
        env.write_text('OPENROUTER_API_KEY="synthetic-key-do-not-emit"\n'
                       "NEWS_LIBRARY_OPENROUTER_MODELS='[\"example/test:free\"]'\n"
                       'NEWS_LIBRARY_OPENROUTER_ENABLED=true\n')
        with patch.dict(os.environ, {'NEWS_LIBRARY_NO_WORKER': 'true'}, clear=True):
            settings = ops.config(env)
        with patch.object(ops, 'get_json', side_effect=AssertionError('network forbidden')):
            r = library_doctor.diagnose(settings, self.data)
        self.assertNotIn('synthetic-key-do-not-emit', json.dumps(r))
        self.assertTrue(r['configuration']['openrouter_key_present'])
        self.assertTrue(r['configuration']['openrouter_models_valid'])
        self.assertEqual('true', r['configuration']['booleans']['NEWS_LIBRARY_NO_WORKER'])
        env.write_text('NEWS_LIBRARY_DATA=$(touch tmp/unwanted)\n')
        with self.assertRaises(ValueError):
            ops.config(env)

    def test_private_output_and_symlinks(self):
        out = ops.new_output(self.root / 'report')
        ops.save({'private': 'fixture'}, out)
        self.assertEqual(0o700, out.stat().st_mode & 0o777)
        self.assertEqual(0o600, (out / 'report.json').stat().st_mode & 0o777)
        with self.assertRaises(FileExistsError):
            ops.new_output(out)
        with self.assertRaises(ValueError):
            ops.new_output(ops.ROOT / 'outside-tmp')
        link = self.root / 'link'
        link.symlink_to(self.data, target_is_directory=True)
        with self.assertRaises(ValueError):
            failure_triage.inspect(link)

    def server(self, handler):
        server = ThreadingHTTPServer(('127.0.0.1', 0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return f'http://127.0.0.1:{server.server_port}'

    def test_search_get_auth_metrics_fallback_failure_and_redirect(self):
        requests = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                requests.append((self.command, self.path, self.headers.get('Authorization')))
                if 'q=redirect' in self.path:
                    self.send_response(302)
                    self.send_header('Location', '/would-leak-token')
                    self.end_headers()
                    return
                if 'q=fail' in self.path:
                    self.send_response(503)
                    self.end_headers()
                    self.wfile.write(b'synthetic-private-key-in-error')
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps({'items': [{'id': 4}, {'id': 2}], 'mode': 'fts',
                                            'warning': 'semantic unavailable', 'indexed': 1, 'eligible': 2, 'truncated': False}).encode())
        base = self.server(Handler)
        rows = [{'query': 'cache', 'modes': ['hybrid'], 'expected_ids': [2, 9]},
                {'query': 'unjudged'}, {'query': 'fail'}, {'query': 'redirect'}]
        token = 'synthetic-read-token-' + 'x' * 32
        r = search_eval.evaluate(rows, base, token)
        self.assertFalse(r['passed'])
        first = r['results'][0]
        self.assertEqual(.5, first['recall_at_limit'])
        self.assertEqual(.5, first['reciprocal_rank'])
        self.assertEqual('fts', first['actual_mode'])
        self.assertIsNone(r['results'][1]['recall_at_limit'])
        self.assertEqual('HTTP 503', r['results'][2]['error'])
        self.assertEqual('HTTP 302', r['results'][3]['error'])
        self.assertNotIn(token, json.dumps(r))
        self.assertNotIn('synthetic-private-key-in-error', json.dumps(r))
        self.assertEqual(4, len(requests))
        self.assertTrue(all(req[0] == 'GET' and req[1].startswith('/api/read/search?') and req[2] == 'Bearer ' + token for req in requests))
        with self.assertRaises(ValueError):
            search_eval.evaluate(rows, 'http://example.org', token)

    def test_cloud_alias_registered_without_generation_and_bad_search_response(self):
        settings = ops.DEFAULTS.copy()
        settings['OLLAMA_MODEL'] = 'gemma4:31b-cloud'
        models = {'models': [{'name': 'gemma4:cloud', 'remote_model': 'gemma4:31b', 'remote_host': 'https://ollama.com'}]}
        with patch.object(ops, 'get_json', return_value=models) as get:
            result = library_doctor.diagnose(settings, self.data, probe=True)
            self.assertTrue(result['probes']['primary_cloud_model_registered'])
            self.assertEqual('http://localhost:11434/api/tags', get.call_args.args[0])
        with patch.object(ops, 'get_json', return_value={'items': [{'id': 1}]}):
            result = search_eval.evaluate([{'query': 'x'}], 'http://localhost:8787', 'x' * 32)
            self.assertFalse(result['passed'])

    def test_failed_refresh_preserves_completed_stages_in_plan(self):
        with sqlite3.connect(self.path) as db:
            db.execute("UPDATE items SET state='failed' WHERE id=1")
            db.execute("INSERT INTO jobs VALUES(4,1,'fetch',NULL,'failed')")
            db.execute("INSERT INTO job_failures VALUES(4,4,'source',0)")
        item = failure_triage.inspect(self.data)['items'][0]
        self.assertTrue(all(item['completed'].values()))
        self.assertEqual('fetch', item['issues'][0]['stage'])
        self.assertFalse(item['issues'][0]['retry_candidate'])

    def test_invalid_cases_no_silent_defaults(self):
        file = self.root / 'cases.json'
        for bad in [[{'query': 'x', 'modes': ['chat']}], [{'query': 'x', 'expected_ids': [True]}],
                    [{'query': 'x', 'filters': {'token': 'no'}}], []]:
            file.write_text(json.dumps(bad))
            with self.assertRaises(ValueError):
                search_eval.cases(file)

    def test_backup_failure_stops_restore_and_never_starts_service(self):
        source = self.root / 'backup'
        source.mkdir()
        (source / 'backup.json').write_text('{}')
        binary = self.root / 'binary'
        binary.write_text('fixture')
        with patch('backup_check.subprocess.run') as run:
            run.return_value.returncode = 1
            r = backup_check.check(source, self.root / 'check-failed', binary)
            self.assertFalse(r['passed'])
            self.assertEqual(1, run.call_count)
            self.assertEqual('verify-backup', run.call_args.args[0][1])
        def success(argv, **kwargs):
            if argv[1] == 'restore':
                restored = Path(argv[-1])
                restored.mkdir()
                (restored / '.restore-paused').touch()
                (restored / 'library.sqlite').touch()
            class Result:
                returncode = 0
            return Result()
        with patch('backup_check.subprocess.run', side_effect=success) as run:
            r = backup_check.check(source, self.root / 'check-passed', binary)
            self.assertTrue(r['passed'])
            self.assertEqual(['verify-backup', 'restore'], [c.args[0][1] for c in run.call_args_list])


if __name__ == '__main__':
    unittest.main()
