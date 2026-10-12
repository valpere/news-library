"""Resumable batch backfill of existing digests, isolated fake cloud only."""
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
    calls = {'digest': 0, 'metadata': 0, 'links': 0}
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
def start(data,knowledge):
    process = subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',data,'-bind','127.0.0.1:0',f'-knowledge={str(knowledge).lower()}','-model','fixture:cloud','-ollama-url',f'http://127.0.0.1:{gateway.server_port}/api/chat'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    line = process.stdout.readline().strip()
    assert line.startswith('news-library: http://'),line
    return process,line.removeprefix('news-library: ')
def stop(process):
    if process.poll() is None:
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=15)
    if process.returncode:
        raise RuntimeError(process.stderr.read())
with tempfile.TemporaryDirectory(dir=root/'tmp',prefix='news-library-knowledge-batch-') as data:
    process,origin = start(data,False)
    try:
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),args=['--no-sandbox'])
            page = browser.new_page()
            ids=[]
            for index in range(3):
                page.goto(origin)
                page.locator('input[name=url]').fill(f'http://127.0.0.1/batch-knowledge-{index}')
                page.get_by_role('button',name='Додати URL',exact=True).click()
                ids.append(page.url.rsplit('/',1)[1])
                page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
                page.get_by_text('Вставити текст вручну',exact=True).click()
                page.locator('textarea[name=text]').fill(f'SQLite backup persistent source {index}')
                page.get_by_role('button',name='Зберегти текст',exact=True).click()
                page.locator('#digest').wait_for(timeout=15000)
            assert Gateway.calls == {'digest':3,'metadata':0,'links':0},Gateway.calls
            stop(process)
            process,origin=start(data,True)
            page.goto(origin)
            page.get_by_role('link',name='Дообробити статті пачкою',exact=True).click()
            assert 'Ще не почато: 3' in page.locator('body').inner_text()
            token=page.locator('input[name=request_token]').input_value()
            csrf=page.locator('input[name=csrf]').input_value()
            page.get_by_role('button',name='Дообробити наступні 5',exact=True).click()
            assert 'Нових задач: 3' in page.locator('body').inner_text()
            for item_id in ids:
                page.goto(f'{origin}/items/{item_id}')
                page.locator('#knowledge').get_by_text('Аналіз завершено.',exact=False).wait_for(timeout=20000)
            assert Gateway.calls['digest']==3 and Gateway.calls['metadata']==3,Gateway.calls
            calls=dict(Gateway.calls)
            # Replaying the accepted form remains a no-op even after completion.
            replay=page.request.post(f'{origin}/knowledge-batch',form={'csrf':csrf,'request_token':token,'limit':'5','include_retry':'1'},headers={'Origin':origin})
            assert replay.status==200 and 'Цей пакет уже був прийнятий' in replay.text()
            page.goto(f'{origin}/knowledge-batch')
            assert 'Завершено: 3' in page.locator('body').inner_text()
            page.get_by_role('button',name='Дообробити решту (до 500)',exact=True).click()
            assert 'Нових задач: 0 · Продовжено: 0' in page.locator('body').inner_text()
            assert Gateway.calls==calls,Gateway.calls
            page.screenshot(path=str(root/'tmp/smoke-knowledge-batch.png'),full_page=True)
            browser.close()
    finally:
        stop(process)
gateway.shutdown()
print('PASS knowledge batch: existing digests -> complete metadata/notes; no digest regeneration; replay and next click skip completed work; fake cloud only.')
