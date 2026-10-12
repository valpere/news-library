"""Offline browser smoke for multipart generation and primary files under cache GC."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from playwright.sync_api import sync_playwright,expect

root=Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
markdown="## Про що матеріал\nСтислий огляд джерела.\n## Ключові тези\n- Збереження фактів.\n## Деталі\nЧерга обробляє частини.\n## Практичне застосування\nПерегляд довгих матеріалів.\n## Факти / цитати\nОдне джерело."
class Gateway(BaseHTTPRequestHandler):
    calls=0
    def log_message(self,*args):pass
    def do_GET(self):self.respond({'models':[{'name':'fixture:cloud','remote_model':'fixture','remote_host':'https://ollama.com'}]})
    def do_POST(self):
        payload=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model']=='fixture:cloud'
        Gateway.calls+=1
        self.respond({'done':True,'message':{'content':markdown},'prompt_eval_count':10,'eval_count':5})
    def respond(self,value):
        body=json.dumps(value).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
gateway=ThreadingHTTPServer(('127.0.0.1',0),Gateway);threading.Thread(target=gateway.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(dir=root/'tmp', prefix='news-library-long-smoke-') as data:
    process=subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',data,'-bind','127.0.0.1:0','-knowledge=false', '-model','fixture:cloud','-ollama-url',f'http://127.0.0.1:{gateway.server_port}/api/chat','-digest-max-bytes','1024','-cache-cleanup-interval','1s','-cache-max-bytes','1024'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line=process.stdout.readline().strip();assert line.startswith('news-library: http://'),line
        origin=line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
            page=browser.new_page()
            page.goto(origin+'/browse')
            page.get_by_label('Додати статтю або YouTube').fill('http://127.0.0.1/long-fixture')
            page.get_by_role('button',name='Додати URL',exact=True).click()
            page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
            source='Довгий матеріал із фактами та прикладами.\n'*100
            page.get_by_text('Вставити текст вручну',exact=True).click()
            page.locator('textarea[name=text]').fill(source)
            page.get_by_role('button',name='Зберегти текст',exact=True).click()
            page.locator('#digest').wait_for(timeout=15000)
            assert 'Оброблено частинами:' in page.locator('#digest').inner_text()
            expect(page.locator('details pre').first).to_have_text(source.strip())
            assert Gateway.calls>2
            page.get_by_role('button',name='Оцінити 2',exact=True).click()
            with page.expect_download() as info:page.get_by_role('link',name='Markdown',exact=True).click()
            assert 'частин:' in Path(info.value.path()).read_text()
            page.screenshot(path=str(root/'tmp/smoke-long-digest.png'),full_page=True)
            # Several periodic cleanups must leave every primary result intact.
            page.wait_for_timeout(2200);page.reload()
            assert 'Оброблено частинами:' in page.locator('#digest').inner_text()
            assert 'оцінка: 2' in page.locator('body').inner_text()
            browser.close()
    finally:
        if process.poll() is None:process.send_signal(signal.SIGTERM);process.wait(timeout=15)
        if process.returncode:raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS long digest browser smoke: all source retained, multipart result and exports, human rating and primary results survive periodic cache cleanup. Fake cloud only.')
