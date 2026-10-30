"""Offline M3 browser/API acceptance; fake local embedding model only."""
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
token = 'fixture-read-token-' + 'r'*32

class Gateway(BaseHTTPRequestHandler):
    fail = False
    calls = 0
    def log_message(self, *args):
        pass
    def do_GET(self):
        self.respond({'models':[{'name':'bge-m3:latest','digest':'fixture-v1','capabilities':['embedding']}]})
    def do_POST(self):
        assert self.path == '/api/embed', 'Unexpected generation request'
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'bge-m3:latest' and payload['truncate'] is False
        Gateway.calls += 1
        if Gateway.fail:
            self.send_response(503); self.end_headers(); return
        vector = [0.0]*1024
        vector[0 if any(word in payload['input'] for word in ('database','storage')) else 1] = 1
        self.respond({'model':'bge-m3:latest','embeddings':[vector]})
    def respond(self, payload):
        body=json.dumps(payload).encode()
        self.send_response(200);self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)

gateway=ThreadingHTTPServer(('127.0.0.1',0),Gateway)
threading.Thread(target=gateway.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='news-library-m3-smoke-',dir=root/'tmp') as data:
    process=subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',data,
        '-bind','127.0.0.1:0','-no-digests','-embeddings','-embed-url',
        f'http://127.0.0.1:{gateway.server_port}/api/embed','-read-token',token],
        stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line=process.stdout.readline().strip();assert line.startswith('news-library: http://'),line
        origin=line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
            page=browser.new_page(viewport={'width':1280,'height':960})
            page.goto(origin+'/browse')
            page.get_by_label('Додати статтю або YouTube').fill('http://127.0.0.1/m3-fixture')
            page.get_by_role('button',name='Додати URL',exact=True).click()
            page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
            page.get_by_text('Вставити текст вручну',exact=True).click()
            page.locator('textarea[name=text]').fill('A database keeps information after a restart. <script>unsafe()</script>')
            page.get_by_role('button',name='Зберегти текст',exact=True).click()
            with sqlite3.connect(Path(data)/'library.sqlite') as db:
                deadline=time.monotonic()+15
                while time.monotonic()<deadline:
                    if db.execute("SELECT count(*) FROM embedding_runs WHERE state='done'").fetchone()[0]==1: break
                    time.sleep(.1)
                else: raise AssertionError('index did not complete')
            page.goto(origin+'/browse')
            page.locator('input[name=q]').fill('persistent storage')
            page.locator('select[name=mode]').select_option('vector')
            page.goto(origin+'/browse?q=persistent+storage&mode=vector')
            page.get_by_role('button',name='Знайти',exact=True).click()
            assert page.locator('article').count()==1
            assert 'A database' in page.locator('article').inner_text()
            assert page.locator('article script').count()==0
            page.screenshot(path=str(root/'tmp/smoke-m3.png'),full_page=True)
            auth={'Authorization':'Bearer '+token}
            assert page.request.get(origin+'/api/read/items/1/context').status==401
            before_read=Gateway.calls
            context=page.request.get(origin+'/api/read/items/1/context',headers=auth)
            assert context.status==200
            assert context.json()['context_kind']=='source_excerpt'
            assert Gateway.calls==before_read
            assert page.request.post(origin+'/items/1/read',headers=auth,data={'read':'1'}).status==403
            search=page.request.get(origin+'/api/read/search?q=persistent+storage&mode=hybrid',headers=auth)
            assert search.status==200 and len(search.json()['items'])==1
            Gateway.fail=True
            page.goto(origin+'/?q=database&mode=hybrid')
            assert 'Пошук за змістом недоступний' in page.locator('body').inner_text()
            assert page.locator('article').count()==1
            with sqlite3.connect(Path(data)/'library.sqlite') as db:
                assert db.execute('SELECT is_read FROM items').fetchone()==(0,)
                assert db.execute('SELECT count(*) FROM items WHERE rating IS NOT NULL').fetchone()[0]==0
                assert db.execute('SELECT count(*) FROM digests').fetchone()[0]==0
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM);process.wait(timeout=15)
        if process.returncode: raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS M3 browser/API: synonym retrieval, deferred filter, escaped source fragment, read authentication, denied mutation, FTS fallback and unchanged human state. Fake embeddings only.')
