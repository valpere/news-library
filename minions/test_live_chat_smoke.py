"""Offline boundaries of the explicit live dialogue helper; fake HTTP only."""
import json
import subprocess
import sys
import threading
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import live_chat_smoke as smoke


class LiveChatSmokeTest(unittest.TestCase):
    def test_explicit_flag_is_required_before_fixture_or_process(self):
        result = subprocess.run([sys.executable, str(Path(smoke.__file__))], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn('live calls require --run-cloud', result.stderr)
        self.assertNotIn('Fixture:', result.stdout)

    def test_cloud_endpoint_cannot_forward_to_remote_or_credentials(self):
        for endpoint in ['https://ollama.com/api/chat', 'http://example.com/api/chat',
                         'http://user:secret@127.0.0.1/api/chat', 'http://127.0.0.1/api/generate',
                         'http://localhost/api/chat?key=secret', 'file:///api/chat']:
            with self.subTest(endpoint=endpoint), self.assertRaises(ValueError):
                smoke.cloud_endpoint(endpoint)
        for endpoint in ['http://127.0.0.1:11434/api/chat', 'http://localhost:11434/api/chat', 'http://[::1]:11434/api/chat']:
            self.assertEqual(smoke.cloud_endpoint(endpoint), endpoint)

    def test_proxy_records_cloud_identity_without_authentication_headers(self):
        auth = []
        class Fake(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def respond(self, body):
                data = json.dumps(body).encode()
                self.send_response(200)
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            def do_GET(self):
                self.respond({'models': [{'name':'fixture:cloud', 'remote_model':'fixture', 'remote_host':'https://ollama.com'}]})
            def do_POST(self):
                payload=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if payload['model']=='fail:cloud':
                    self.send_error(503)
                    return
                auth.append(self.headers.get('Authorization'))
                self.respond({'done': True, 'model':'fixture:cloud', 'message': {'content':'{"action":"queue"}'}})
        upstream = ThreadingHTTPServer(('127.0.0.1',0), Fake)
        recorder = smoke.Recorder(f'http://127.0.0.1:{upstream.server_port}/api/chat',2)
        proxy = ThreadingHTTPServer(('127.0.0.1',0), recorder.handler())
        for server in (upstream,proxy):
            threading.Thread(target=server.serve_forever,daemon=True).start()
        origin=f'http://127.0.0.1:{proxy.server_port}'
        try:
            with urllib.request.urlopen(origin+'/api/tags',timeout=3) as response:
                self.assertIn('models',json.load(response))
            body=json.dumps({'model':'fixture:cloud'}).encode()
            with urllib.request.urlopen(urllib.request.Request(origin+'/api/chat',data=body,headers={'Authorization':'Bearer fixture-private-auth'}),timeout=3) as response:
                self.assertTrue(json.load(response)['done'])
            self.assertEqual(auth,['Bearer fixture-private-auth'])
            self.assertEqual(recorder.calls[0]['command'],{'action':'queue'})
            self.assertEqual(recorder.calls[0]['remote_model'],'fixture')
            self.assertNotIn('fixture-private-auth',json.dumps(recorder.calls))
            with self.assertRaises(urllib.error.HTTPError) as rejected:
                urllib.request.urlopen(origin+'/api/generate',timeout=3)
            self.assertEqual(rejected.exception.code,404)
            self.assertEqual(len(recorder.calls),1)
            with self.assertRaises(urllib.error.HTTPError):
                urllib.request.urlopen(urllib.request.Request(origin+'/api/chat',data=b'{"model":"fail:cloud"}'),timeout=3)
            self.assertEqual(len(recorder.calls),2)
            self.assertEqual(recorder.calls[1]['model'],'fail:cloud')
            self.assertEqual(recorder.calls[1]['status'],'failed')
        finally:
            for server in (proxy,upstream):
                server.shutdown()
                server.server_close()
