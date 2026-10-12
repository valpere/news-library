"""Offline M2 browser acceptance; every cloud response is a local fixture."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
markdown = "## Про що матеріал\nСтислий огляд новин.\n## Ключові тези\n- Доступний текст.\n## Деталі\nВерсії залишаються.\n## Практичне застосування\nПошук прочитаного.\n## Факти / цитати\nОдин сервіс."
class Gateway(BaseHTTPRequestHandler):
    calls = 0
    def log_message(self, *args): pass
    def do_GET(self):
        self.respond({'models': [{'name':'fixture:cloud','remote_model':'fixture','remote_host':'https://ollama.com'}]})
    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'fixture:cloud'
        assert 'Fixture interests' in payload['messages'][1]['content']
        Gateway.calls += 1
        time.sleep(1)
        content = json.dumps({'markdown':markdown,'prediction':{'probability':0.73,'reason':'Матеріал пов’язаний із темами тестового профілю.'}},ensure_ascii=False)
        self.respond({'done':True,'message':{'content':content}})
    def respond(self, value):
        body = json.dumps(value).encode()
        self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)

gateway = ThreadingHTTPServer(('127.0.0.1',0),Gateway)
threading.Thread(target=gateway.serve_forever,daemon=True).start()
rules = json.dumps([{'id':'url-commercial','reason':'commercial','field':'url','contains':'/blocked-fixture','probability':.95},
    {'id':'text-commercial','reason':'commercial','field':'text','contains':'advertisement','probability':.95}])
with tempfile.TemporaryDirectory(dir=root/'tmp', prefix='news-library-m2-smoke-') as data:
    process = subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',data,'-bind','127.0.0.1:0',
        '-knowledge=false', '-model','fixture:cloud','-ollama-url',f'http://127.0.0.1:{gateway.server_port}/api/chat',
        '-interests','Fixture interests: libraries and queues','-filter-rules',rules],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line = process.stdout.readline().strip()
        assert line.startswith('news-library: http://'), line
        origin = line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
            page = browser.new_page(viewport={'width':1280,'height':960})
            page.goto(origin+'/browse')
            page.get_by_label('Додати статтю або YouTube').fill('https://example.com/blocked-fixture')
            page.get_by_role('button',name='Додати URL',exact=True).click()
            page.get_by_role('heading',name='Відсіяно правилами',exact=True).wait_for()
            assert Gateway.calls == 0
            assert page.locator('select[name=score]').count() == 0
            page.get_by_text('Рішення правил',exact=True).click()
            assert 'commercial: 95%' in page.locator('body').inner_text()
            # Manual input is saved and indexed even while rejected.
            page.get_by_text('Вставити текст вручну',exact=True).click()
            page.locator('textarea[name=text]').fill('Searchable advertisement source')
            page.get_by_role('button',name='Зберегти текст',exact=True).click()
            assert Gateway.calls == 0
            page.goto(origin+'/browse')
            page.locator('input[name=q]').fill('Searchable')
            page.locator('select[name=filter]').select_option('rejected')
            page.get_by_role('button',name='Знайти',exact=True).click()
            assert page.locator('article').count() == 1
            page.locator('article h2 a').click()
            page.get_by_role('button',name='Повернути до обробки',exact=True).click()
            page.locator('#digest').wait_for(timeout=15000)
            assert Gateway.calls == 1
            assert 'Confidence 73%' in page.locator('#interest-prediction').inner_text()
            page.locator('select[name=score]').select_option('2')
            page.get_by_role('button',name='Оцінити особисту корисність',exact=True).click()
            page.get_by_text('Якість дайджесту',exact=True).click()
            page.locator('select[name=quality]').select_option('inaccurate')
            page.locator('textarea[name=note]').fill('Fixture feedback: useful source, flawed digest')
            first_digest = page.locator('input[name=digest]').first.input_value()
            page.get_by_role('button',name='Зберегти відгук',exact=True).click()
            assert 'корисність: Низька' in page.locator('body').inner_text()
            assert Gateway.calls == 1
            page.get_by_role('button',name='Створити нову версію дайджесту',exact=True).click()
            page.get_by_text('Оновлюється дайджест.',exact=False).wait_for()
            page.locator('#processing[data-pending="false"]').wait_for(state='attached',timeout=15000)
            next_digest = page.locator('input[name=digest]').first.input_value()
            assert next_digest != first_digest
            assert Gateway.calls == 2
            page.get_by_text('Історія оцінок (1)',exact=True).click()
            assert f'дайджест: #{first_digest}' in page.locator('body').inner_text()
            page.get_by_text('Версії дайджесту (2)',exact=True).click()
            page.locator(f'a[href$="&digest={first_digest}"]').click()
            page.get_by_text('Якість дайджесту',exact=True).click()
            assert 'Fixture feedback' in page.locator('body').inner_text()
            assert page.locator('input[name=digest]').first.input_value() == first_digest
            page.screenshot(path=str(root/'tmp/smoke-m2.png'),full_page=True)
            page.goto(origin+'/browse')
            page.locator('input[name=confidence_min]').fill('70')
            page.locator('input[name=confidence_max]').fill('80')
            page.locator('select[name=sort]').select_option('confidence')
            page.get_by_role('button',name='Знайти',exact=True).click()
            assert page.locator('article').count() == 1
            assert Gateway.calls == 2
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM);process.wait(timeout=15)
        if process.returncode: raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS M2 browser smoke: explained zero-call rejection, retained FTS source, explicit override, Confidence, separate feedback, versioned regeneration, unchanged human rating context and confidence search. Fake cloud only.')
