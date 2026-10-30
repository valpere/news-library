"""Automatic article metadata and connection notes, isolated fake cloud only."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
markdown = "## Про що матеріал\nСтислий огляд бібліотеки.\n## Ключові тези\n- Збереження тексту.\n## Деталі\nСтійка черга відновлюється.\n## Практичне застосування\nПошук прочитаного.\n## Факти / цитати\nОдин сервіс."
class Gateway(BaseHTTPRequestHandler):
    calls = {'digest': 0, 'metadata': 0, 'links': 0, 'support': 0}
    def log_message(self, *args):
        pass
    def respond(self, value):
        body = json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        self.respond({'models':[{'name':'fixture:cloud','remote_model':'fixture','remote_host':'https://ollama.com'}]})
    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'fixture:cloud'
        instruction = payload['messages'][0]['content']
        content = markdown
        if 'Визнач категорію' in instruction:
            Gateway.calls['metadata'] += 1
            content = json.dumps({'category':'Зберігання даних','tags':['sqlite','backup'],'questions':['Як відновити чергу без втрат?']})
        elif 'Окрема перевірка connection-support-v1' in instruction:
            Gateway.calls['support'] += 1
            data = json.loads(payload['messages'][1]['content'])
            content = json.dumps({'version':'connection-support-v1','decisions':[{'item_id':link['item_id'],'verdict':'supported','reason':'Both provided contexts describe persistent queue recovery.'} for link in data['links']]})
        elif 'Перевір кандидатів' in instruction:
            Gateway.calls['links'] += 1
            data = json.loads(payload['messages'][1]['content'])
            other = data['candidates'][0]
            content = json.dumps({'links':[{'item_id':other['ItemID'],'title':'Збереження і відновлення черги','body':'Обидва матеріали пояснюють збереження тексту під час відновлення черги. Це спільний механізм надійності.','category':'Зберігання даних','tags':['sqlite','backup'],'questions':['Як відновити чергу без втрат?'],'current_role':'Збереження','other_role':'Відновлення','current_evidence':'Стійка черга відновлюється.','other_evidence':'Стійка черга відновлюється.'}]})
        else:
            Gateway.calls['digest'] += 1
        self.respond({'done':True,'message':{'content':content}})

gateway = ThreadingHTTPServer(('127.0.0.1',0),Gateway)
threading.Thread(target=gateway.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(dir=root/'tmp',prefix='news-library-knowledge-smoke-') as data:
    process = subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',data,'-bind','127.0.0.1:0','-model','fixture:cloud','-ollama-url',f'http://127.0.0.1:{gateway.server_port}/api/chat'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line = process.stdout.readline().strip()
        assert line.startswith('news-library: http://'),line
        origin = line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),args=['--no-sandbox'])
            page = browser.new_page()
            ids = []
            for index in range(2):
                page.goto(origin+'/browse')
                page.locator('input[name=url]').fill(f'http://127.0.0.1/knowledge-{index}')
                page.get_by_role('button',name='Додати URL',exact=True).click()
                ids.append(page.url.rsplit('/',1)[1])
                page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
                page.get_by_text('Вставити текст вручну',exact=True).click()
                page.locator('textarea[name=text]').fill(f'SQLite backup persistent source {index}')
                page.get_by_role('button',name='Зберегти текст',exact=True).click()
                page.locator('#knowledge').get_by_text('Аналіз завершено.',exact=False).wait_for(timeout=20000)
                assert 'Теги моделі: sqlite' in page.locator('#knowledge').inner_text()
                assert 'Як відновити чергу без втрат?' in page.locator('#knowledge').inner_text()
                if index == 0:
                    page.locator('select[name=score]').select_option('2')
                    page.get_by_role('button',name='Оцінити особисту корисність',exact=True).click()
            assert Gateway.calls == {'digest':2,'metadata':2,'links':1,'support':1},Gateway.calls
            for item_id in ids:
                page.goto(f'{origin}/items/{item_id}')
                page.locator('#connection-backlinks summary').click()
                page.get_by_role('link',name='Збереження і відновлення черги',exact=True).click()
                assert 'автор: модель fixture:cloud' in page.locator('body').inner_text()
                assert 'Підстава зв’язку' in page.locator('.connection-body').inner_text()
            page.goto(f'{origin}/items/{ids[1]}')
            with page.expect_download() as info:
                page.get_by_role('link',name='Markdown із метаданими',exact=True).click()
            exported = Path(info.value.path()).read_text()
            assert 'aliases:' in exported and 'author: model' in exported and 'digest_id:' in exported
            page.goto(f'{origin}/?q=Як%20відновити%20чергу%20без%20втрат?')
            assert page.locator('article a[href^="/items/"]').count() == 2
            page.screenshot(path=str(root/'tmp/smoke-knowledge.png'),full_page=True)
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=15)
        if process.returncode:
            raise RuntimeError(process.stderr.read())
gateway.shutdown()
print('PASS automatic knowledge: digest, category/tags/questions, grounded note, backlinks on both articles, model provenance, Markdown, search; fake cloud only.')
