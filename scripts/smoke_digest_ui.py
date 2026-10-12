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
with tempfile.TemporaryDirectory(dir=root/'tmp', prefix='news-library-digest-smoke-') as data:
    process = subprocess.Popen([str(root/'build/news-library'), '-env-file', '', '-data', data,
        '-bind', '127.0.0.1:0', '-knowledge=false', '-model', 'fixture:cloud', '-ollama-url',
        f'http://127.0.0.1:{gateway.server_port}/api/chat'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        line = process.stdout.readline().strip()
        assert line.startswith('news-library: http://'), line
        origin = line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'), headless=True,args=['--no-sandbox'])
            context = browser.new_context(permissions=['clipboard-read','clipboard-write'],viewport={'width':1280,'height':960})
            page = context.new_page()
            def submit_source(url, text):
                page.goto(origin+'/')
                page.get_by_label('Додати статтю або YouTube').fill(url)
                page.get_by_role('button',name='Додати URL',exact=True).click()
                page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
                page.get_by_text('Вставити текст вручну',exact=True).click()
                page.locator('textarea[name=text]').fill(text)
                page.get_by_role('button',name='Зберегти текст',exact=True).click()
                assert page.locator('input[name=score]').count() == 0
                assert page.locator('#processing').get_attribute('data-pending') == 'true'
            submit_source('http://127.0.0.1/digest-smoke', 'Full source text <script>alert(1)</script>')
            page.locator('#digest').wait_for(timeout=15000)
            assert Gateway.calls == 1
            assert page.locator('details pre').count() == 1
            assert not page.locator('details pre').is_visible()
            assert page.get_by_role('tabpanel').count() == 1
            page.get_by_role('tab',name='Ключові тези',exact=True).click()
            assert 'Збереження тексту' in page.get_by_role('tabpanel').inner_text()
            page.get_by_role('button',name='Копіювати Markdown').click()
            page.get_by_text('Скопійовано',exact=True).wait_for()
            assert page.evaluate('navigator.clipboard.readText()') == markdown
            for label, suffix in [('Markdown','.md'),('HTML','.html')]:
                with page.expect_download() as info:
                    page.get_by_role('link',name=label,exact=True).click()
                download=info.value
                assert download.suggested_filename.endswith(suffix)
                assert 'Ключові тези' in Path(download.path()).read_text()
                if suffix == '.html':
                    artifact = Path(data)/'export.html'
                    download.save_as(artifact)
                    exported = context.new_page()
                    exported.goto(artifact.as_uri())
                    assert exported.get_by_role('tabpanel').count() == 1
                    exported.get_by_role('tab',name='Деталі',exact=True).click()
                    assert 'Стійка черга' in exported.get_by_role('tabpanel').inner_text()
                    assert exported.get_by_role('link',name='Markdown',exact=True).count() == 0
                    exported.close()
            digest_id=page.locator('input[name=digest]').first.input_value()
            page.get_by_role('button',name='Оцінити 2',exact=True).click()
            page.get_by_text('Історія оцінок (1)',exact=True).click()
            assert f'дайджест: #{digest_id}' in page.locator('body').inner_text()
            page.screenshot(path=str(root/'tmp/smoke-digest.png'),full_page=True)
            Gateway.fail=True
            submit_source('http://127.0.0.1/digest-failure','Preserved source after model failure')
            page.get_by_role('heading',name='Дайджест недоступний',exact=True).wait_for(timeout=15000)
            assert page.locator('input[name=score]').count() == 0
            assert 'Preserved source' in page.locator('pre').filter(has_text='Preserved source').first.text_content(), (page.url, page.locator('pre').all_text_contents(), page.locator('body').inner_text())
            assert page.locator('#digest-failure').get_attribute('data-kind') == 'format'
            assert 'Автоматичного повтору немає' in page.locator('#digest-failure').inner_text()
            assert Gateway.calls == 2
            Gateway.fail=False
            page.get_by_role('button',name='Запросити інший варіант дайджесту',exact=True).click()
            page.locator('#digest').wait_for(timeout=15000)
            assert Gateway.calls == 3
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=15)
        if process.returncode:
            raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS digest browser smoke: queued generation without rating, automatic digest, tabs, clipboard, Markdown/HTML downloads, rating provenance, preserved source and explicit generation retry. Fake cloud only.')
