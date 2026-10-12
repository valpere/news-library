"""Offline browser acceptance with a loopback fake cloud gateway."""
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
markdown = "## Про що матеріал\nСтислий огляд бібліотеки.\n## Ключові тези\n- Збереження тексту.\n## Деталі\nСтійка черга відновлюється.\n## Практичне застосування\nПошук прочитаного.\n## Факти / цитати\nОдин сервіс."
class Gateway(BaseHTTPRequestHandler):
    calls = 0
    fail = False
    def log_message(self, *args):
        pass
    def do_GET(self):
        self.respond({'models': [{'name': 'fixture:cloud', 'remote_model': 'fixture:31b', 'remote_host': 'https://ollama.com'}]})
    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'fixture:cloud'
        Gateway.calls += 1
        time.sleep(1)
        self.respond({'done': True, 'message': {'content': 'invalid' if Gateway.fail else markdown}})
    def respond(self, value):
        body = json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

gateway = ThreadingHTTPServer(('127.0.0.1', 0), Gateway)
threading.Thread(target=gateway.serve_forever, daemon=True).start()
Gateway.fail = True
with tempfile.TemporaryDirectory(dir=root/'tmp', prefix='news-library-batch-retry-') as data:
    process = subprocess.Popen([str(root/'build/news-library'), '-env-file', '', '-data', data,
        '-bind', '127.0.0.1:0', '-knowledge=false', '-model', 'fixture:cloud', '-ollama-url',
        f'http://127.0.0.1:{gateway.server_port}/api/chat'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        line = process.stdout.readline().strip()
        assert line.startswith('news-library: http://'), line
        origin = line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'), args=['--no-sandbox'])
            page = browser.new_page()
            ids = []
            for index in range(2):
                page.goto(origin+'/browse')
                page.locator('input[name=url]').fill(f'http://127.0.0.1/batch-retry-{index}')
                page.get_by_role('button',name='Додати URL',exact=True).click()
                ids.append(page.url.rsplit('/', 1)[1])
                page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
                page.get_by_text('Вставити текст вручну',exact=True).click()
                page.locator('textarea[name=text]').fill(f'Preserved source {index}')
                page.get_by_role('button',name='Зберегти текст',exact=True).click()
                page.get_by_role('heading',name='Дайджест недоступний',exact=True).wait_for(timeout=15000)
            assert Gateway.calls == 2
            page.goto(origin+'/browse')
            page.get_by_role('link',name='Перезапустити невдалі дайджести пачкою',exact=True).click()
            selected = page.locator('input[name=selection]')
            assert selected.count() == 2
            assert all(selected.nth(i).is_checked() for i in range(2))
            Gateway.fail = False
            page.get_by_role('button',name='Поставити вибрані в чергу',exact=True).click()
            assert 'У чергу: 2 · Пропущено: 0' in page.locator('body').inner_text()
            for item_id in ids:
                page.goto(f'{origin}/items/{item_id}')
                page.locator('#digest').wait_for(timeout=15000)
                assert 'Preserved source' in page.locator('pre').all_text_contents()[0]
                assert page.locator('input[name=score]').count() > 0
            assert Gateway.calls == 4
            page.goto(f'{origin}/digest-retries')
            assert page.locator('input[name=selection]').count() == 0
            page.screenshot(path=str(root/'tmp/smoke-digest-batch-retry.png'),full_page=True)
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=15)
        if process.returncode:
            raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS bulk digest retry browser smoke: two failures, checked preview, queued together, both ready, preserved sources; fake cloud only.')
