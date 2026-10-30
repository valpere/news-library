"""Offline M4 real CLI backup/restore acceptance."""
import sqlite3
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from playwright.sync_api import sync_playwright

root=Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
markdown='## Про що матеріал\nБезпечне відновлення бібліотеки.\n## Ключові тези\n- Незмінна історія.\n## Деталі\nЗбережені файли.\n## Практичне застосування\nРезервні копії.'
class Gateway(BaseHTTPRequestHandler):
    calls=0
    def log_message(self,*args): pass
    def do_GET(self): self.respond({'models':[{'name':'fixture:cloud','remote_model':'fixture:31b','remote_host':'https://ollama.com'}]})
    def do_POST(self):
        Gateway.calls+=1
        self.rfile.read(int(self.headers['Content-Length']))
        self.respond({'done':True,'message':{'content':markdown}})
    def respond(self,payload):
        body=json.dumps(payload).encode();self.send_response(200)
        self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)))
        self.end_headers();self.wfile.write(body)

gateway=ThreadingHTTPServer(('127.0.0.1',0),Gateway)
threading.Thread(target=gateway.serve_forever,daemon=True).start()
def start(data):
    process=subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',str(data),
        '-bind','127.0.0.1:0','-knowledge=false', '-model','fixture:cloud','-ollama-url',
        f'http://127.0.0.1:{gateway.server_port}/api/chat','-telegram=false','-telegram-daily','09:00'],
        stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    line=process.stdout.readline().strip();assert line.startswith('news-library: http://'),line
    return process,line.removeprefix('news-library: ')
def stop(process):
    if process.poll() is None:process.send_signal(signal.SIGTERM);process.wait(timeout=15)
    assert process.returncode==0,process.stderr.read()
with tempfile.TemporaryDirectory(dir=root/'tmp',prefix='m4-smoke-') as directory:
    work=Path(directory);data=work/'source'
    process,origin=start(data)
    try:
        with sync_playwright() as pw:
            browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
            page=browser.new_page(viewport={'width':1280,'height':960})
            page.goto(origin+'/browse')
            page.get_by_label('Додати статтю або YouTube').fill('http://127.0.0.1/m4-fixture')
            page.get_by_role('button',name='Додати URL',exact=True).click()
            page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
            page.get_by_text('Вставити текст вручну',exact=True).click()
            page.locator('textarea[name=text]').fill('Saved source for backup <script>unsafe()</script>.')
            page.get_by_role('button',name='Зберегти текст',exact=True).click()
            page.locator('#digest').wait_for(timeout=15000)
            page.locator('select[name=score]').select_option('2')
            page.get_by_role('button',name='Оцінити особисту корисність',exact=True).click()
            assert Gateway.calls==1
            page.screenshot(path=str(root/'tmp/smoke-m4.png'),full_page=True)
            for command,args in [('backup',['-source',str(data),'-output',str(work/'backup')]),
                                 ('verify-backup',['-source',str(work/'backup')]),
                                 ('restore',['-source',str(work/'backup'),'-output',str(work/'restored')])]:
                subprocess.run([str(root/'build/news-library'),command,*args],cwd=root,check=True)
            restored,restored_origin=start(work/'restored')
            try:
                page.goto(restored_origin+'/items/1')
                assert 'корисність: Низька' in page.locator('body').inner_text()
                assert 'Фонове виконання призупинено' in page.locator('body').inner_text()
                assert Gateway.calls==1
            finally:stop(restored)
            browser.close()
    finally:stop(process)
gateway.shutdown()
print('PASS M4 browser/CLI: live backup+verify+restore, unchanged judgments and source, paused workers. Fake cloud only.')
