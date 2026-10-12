"""Dialogue home acceptance in an isolated store with a fake cloud planner."""
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
class Gateway(BaseHTTPRequestHandler):
    calls = 0
    release = threading.Event()
    def log_message(self, *args):
        pass
    def respond(self, value):
        body=json.dumps(value).encode()
        self.send_response(200)
        self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        self.respond({'models':[{'name':'fixture:cloud','remote_model':'fixture','remote_host':'https://ollama.com'}]})
    def do_POST(self):
        Gateway.calls+=1
        payload=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model']=='fixture:cloud'
        data=json.loads(payload['messages'][1]['content'])
        assert not data['state']['Results']
        message=data['message']
        if message == 'очікування':
            assert Gateway.release.wait(timeout=15), 'browser did not release pending request'
        if message=='тільки непрочитане':
            command={'action':'refine','criteria':{'read':'0'}}
        elif message=='поясни різницю між тегом і категорією':
            command={'action':'answer','answer':'Категорія описує широку тему, а тег — окреме поняття.'}
        elif message=='дивний запит':
            command={'action':'sql'}
        else:
            command={'action':'search','criteria':{'query':'SQLite','mode':'hybrid','sort':'added','direction':'asc'}}
        self.respond({'done':True,'message':{'content':json.dumps(command)}})
gateway=ThreadingHTTPServer(('127.0.0.1',0),Gateway)
threading.Thread(target=gateway.serve_forever,daemon=True).start()
def start(data):
    p=subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',str(data),'-bind','127.0.0.1:0','-no-worker','-no-digests','-chat-model','fixture:cloud','-ollama-url',f'http://127.0.0.1:{gateway.server_port}/api/chat'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    line=p.stdout.readline().strip()
    assert line.startswith('news-library: http://'),line
    return p,line.removeprefix('news-library: ')
def stop(p):
    if p.poll() is None:
        p.send_signal(signal.SIGTERM)
        p.wait(timeout=15)
    assert p.returncode==0,p.stderr.read()
with tempfile.TemporaryDirectory(dir=root/'tmp',prefix='chat-smoke-') as data:
    p,origin=start(data)
    try:
        with sync_playwright() as pw:
            browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),args=['--no-sandbox'])
            page=browser.new_page(viewport={'width':1440,'height':1000})
            errors=[]
            page.on('pageerror',lambda error: errors.append(str(error)))
            page.goto(origin)
            assert page.locator('.card').count()==0
            assert page.locator('input[name=url],input[name=q]').count()==0
            assert page.get_by_role('heading',name='Що сьогодні почитаємо?').count()==1
            cookie=next(c for c in page.context.cookies() if c['name']=='news_library_chat')
            assert cookie['httpOnly'] and cookie['sameSite']=='Strict'
            page.get_by_role('textbox',name='Повідомлення бібліотеці').fill('SQLite')
            page.get_by_role('textbox',name='Повідомлення бібліотеці').press('Shift+Enter')
            assert page.locator('#composer textarea').input_value()=='SQLite\n'
            assert page.locator('.turn').count()==0
            def assert_chat_visible():
                page.wait_for_function("""() => {
                    const input = document.querySelector('#composer textarea').getBoundingClientRect();
                    const replies = document.querySelectorAll('.assistant-message');
                    const reply = replies[replies.length - 1].getBoundingClientRect();
                    const conversation = document.querySelector('#conversation').getBoundingClientRect();
                    return input.top >= 0 && input.bottom <= innerHeight &&
                        reply.top >= conversation.top - 1 && reply.top < conversation.bottom &&
                        reply.top < input.top;
                }""")
            def send(text, keyboard=True):
                previous=page.locator('.turn').count()
                page.get_by_role('textbox',name='Повідомлення бібліотеці').fill(text)
                if keyboard:
                    page.get_by_role('textbox',name='Повідомлення бібліотеці').press('Enter')
                else:
                    page.get_by_role('button',name='Надіслати ↑').click()
                page.wait_for_function('(n)=>document.querySelectorAll(".turn").length>n',arg=previous)
                assert page.locator('#chat-status').inner_text()==''
                assert_chat_visible()
            page.screenshot(path=str(root/'tmp/smoke-chat-welcome.png'),full_page=True)
            send(' '.join(f'https://example.com/chat/{i}' for i in range(8)))
            assert 'Додано: 8' in page.locator('.turn').last.inner_text()
            assert Gateway.calls==0
            assert page.locator('button[value^="rate:"]').count()==0
            # Seed text through the same supported card endpoint, while workers remain paused.
            csrf=page.locator('#composer input[name=csrf]').input_value()
            for i in range(1,9):
                response=page.request.post(f'{origin}/items/{i}/manual',form={'csrf':csrf,'text':f'SQLite backup: immutable text and queue {i}. <script>alert(1)</script>'},headers={'Origin':origin},max_redirects=0)
                assert response.status==303,response.text()
            response=page.request.post(f'{origin}/items/1/tags',form={'csrf':csrf,'tags':'sqlite, backup'},headers={'Origin':origin},max_redirects=0)
            assert response.status==303
            page.reload()
            assert page.get_by_role('button',name='sqlite',exact=False).count()==1
            send('знайди про резервні копії')
            latest=page.locator('.turn').last
            assert latest.locator('.card').count()==5
            assert 'Знайдено 8' in latest.inner_text()
            assert 'Пошук за змістом недоступний' in latest.inner_text()
            assert page.locator('.card script').count()==0
            send('тільки непрочитане', keyboard=False)
            assert 'Тема: SQLite' in page.locator('.turn').last.inner_text()
            assert Gateway.calls==2
            page.locator('.current-actions summary').click()
            page.locator('.current-actions button[value="read:1"]').click()
            page.locator('.turn').last.get_by_text('Дію з матеріалом виконано.',exact=False).wait_for()
            send('далі')
            assert page.locator('.turn').last.locator('.card').count()==3
            assert '/items/6' in page.locator('.turn').last.inner_html()
            page.locator('.current-actions summary').click()
            page.locator('button[value="rate:2"]').click()
            page.locator('.turn').last.get_by_text('Яку власну оцінку',exact=False).wait_for()
            send('3')
            with sqlite3.connect(Path(data)/'library.sqlite') as db:
                assert db.execute('SELECT item_id,score FROM rating_events').fetchall()==[(7,3)]
            assert Gateway.calls==2
            send('перезапусти')
            assert 'чи продовжити' in page.locator('.turn').last.inner_text()
            send('дайджести')
            assert 'для такого повтору немає' in page.locator('.turn').last.inner_text()
            send('дивний запит')
            assert 'невідома дія чату' in page.locator('.turn').last.inner_text()
            send('стан')
            assert 'Зараз отримання тексту' in page.locator('.turn').last.inner_text()
            send('поясни різницю між тегом і категорією')
            assert 'Категорія описує широку тему' in page.locator('.turn').last.inner_text()
            send('чому аналіз не завершено?')
            assert 'завершилися помилкою' in page.locator('.turn').last.inner_text()
            previous=page.locator('.turn').count()
            page.get_by_role('textbox',name='Повідомлення бібліотеці').fill('очікування')
            page.get_by_role('textbox',name='Повідомлення бібліотеці').press('Enter')
            page.get_by_text('Виконую запит…',exact=True).wait_for()
            assert page.locator('#composer button').is_disabled()
            assert page.evaluate("""() => {
                const input = document.querySelector('#composer textarea').getBoundingClientRect();
                const status = document.querySelector('#chat-status').getBoundingClientRect();
                return input.top >= 0 && input.bottom <= innerHeight &&
                    status.top >= input.bottom && status.bottom <= innerHeight;
            }""")
            Gateway.release.set()
            page.wait_for_function('(n)=>document.querySelectorAll(".turn").length>n',arg=previous)
            assert_chat_visible()
            page.screenshot(path=str(root/'tmp/smoke-chat-desktop.png'),full_page=False)
            page.set_viewport_size({'width':390,'height':844})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
            send('непрочитані вхідні')
            assert page.locator('.turn').last.locator('.card').count()==5
            page.screenshot(path=str(root/'tmp/smoke-chat-mobile.png'),full_page=False)
            assert page.locator('#conversation').evaluate('(el)=>el.scrollHeight > el.clientHeight')
            page.locator('#conversation').evaluate('(el)=>{el.scrollTop=0}')
            assert page.evaluate("""() => {
                const input = document.querySelector('#composer textarea').getBoundingClientRect();
                return input.top >= 0 && input.bottom <= innerHeight &&
                    document.querySelector('#conversation').scrollTop === 0;
            }""")
            send('\n'.join(f'https://example.com/chat/{i}' for i in range(8)))
            assert 'Уже існує: 8' in page.locator('.turn').last.inner_text()
            page.set_viewport_size({'width':390,'height':500})
            send('стан')
            page.screenshot(path=str(root/'tmp/smoke-chat-short-mobile.png'),full_page=False)
            page.set_viewport_size({'width':390,'height':844})
            turns=page.locator('.turn').count()
            stop(p)
            p,origin=start(data)
            page.goto(origin)
            assert page.locator('.turn').count()==turns
            page.get_by_role('button',name='Новий діалог',exact=True).click()
            assert page.locator('.turn').count()==0
            send('https://example.com/chat/0')
            assert 'Додано: 0 · Уже існує: 1' in page.locator('.turn').last.inner_text()
            assert not errors,errors
            browser.close()
    finally:
        Gateway.release.set()
        stop(p)
gateway.shutdown()
print('PASS chat browser: unified URL batch, no premature rating, natural search and refinement, stable paging, explicit rating, contextual clarification, actual queue status, restart history, desktop/mobile Enter/click scrolling with visible composer and latest reply, and safe rendering; fake cloud only.')
