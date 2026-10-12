#!/usr/bin/env python3
"""Explicit live cloud dialogue acceptance; synthetic store and paused workers."""
import argparse
import http.cookiejar
import ipaddress
import json
from pathlib import Path
import re
import selectors
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def cloud_endpoint(value):
    u = urllib.parse.urlsplit(value)
    try:
        local = u.hostname == 'localhost' or ipaddress.ip_address(u.hostname).is_loopback
    except ValueError:
        local = False
    if not local or u.scheme != 'http' or u.path != '/api/chat' or u.query or u.fragment or u.username or u.password:
        raise ValueError('use a loopback HTTP Ollama /api/chat endpoint')
    return value


def field(html, name):
    match = re.search(r'name="'+name+r'" value="([^"]*)"', html)
    if not match or (name != "csrf" and not match.group(1)):
        raise ValueError('fixture chat field missing: '+name)
    return match.group(1)


class Recorder:
    def __init__(self, endpoint, timeout):
        self.endpoint, self.timeout = endpoint, timeout
        self.calls = []
        self.models = {}

    def handler(self):
        recorder = self
        class Gateway(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_GET(self):
                self.forward('GET', '/api/tags')
            def do_POST(self):
                self.forward('POST', '/api/chat')
            def forward(self, method, path):
                if self.path != path:
                    self.send_error(404)
                    return
                body = None
                headers = {}
                if method == 'POST':
                    length = int(self.headers.get('Content-Length', '0'))
                    if length > 1 << 20:
                        self.send_error(413)
                        return
                    body = self.rfile.read(length)
                    headers['Content-Type'] = 'application/json'
                # Forward authentication only to the fixed local endpoint; never log headers.
                if self.headers.get('Authorization'):
                    headers['Authorization'] = self.headers['Authorization']
                target = recorder.endpoint.removesuffix('/api/chat')+path
                started = time.monotonic()
                call = None
                try:
                    if method == 'POST':
                        payload = json.loads(body)
                        identity = recorder.models.get(payload['model'], {})
                        call = {'model':payload['model'], 'remote_model':identity.get('remote_model'), 'remote_host':identity.get('remote_host'), 'status':'running'}
                        recorder.calls.append(call)
                    with urllib.request.urlopen(urllib.request.Request(target, data=body, headers=headers, method=method), timeout=recorder.timeout) as response:
                        result = response.read((2 << 20)+1)
                    if len(result) > 2 << 20:
                        raise ValueError('upstream response too large')
                    if method == 'GET':
                        recorder.models = {m['name']: m for m in json.loads(result).get('models', []) if 'name' in m}
                    if method == 'POST':
                        payload, reply = json.loads(body), json.loads(result)
                        content = reply.get('message', {}).get('content', '')
                        try:
                            command = json.loads(content)
                        except json.JSONDecodeError:
                            command = None
                        call.update({'actual_model': reply.get('model'), 'seconds':round(time.monotonic()-started,3),
                                     'command':command, 'content':content, 'status':'done'})
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(result)))
                    self.end_headers()
                    self.wfile.write(result)
                except (OSError, ValueError) as error:
                    if call is not None:
                        call.update({'error':type(error).__name__, 'status':'failed', 'seconds':round(time.monotonic()-started,3)})
                    self.send_error(502, 'local Ollama request failed; no fallback attempted')
        return Gateway


def check(condition, message):
    if not condition:
        raise ValueError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-cloud', action='store_true')
    parser.add_argument('--model', default='gemma4:31b-cloud')
    parser.add_argument('--ollama-url', default='http://127.0.0.1:11434/api/chat', type=cloud_endpoint)
    parser.add_argument('--timeout', type=int, default=90)
    args = parser.parse_args()
    if not args.run_cloud:
        parser.error('live calls require --run-cloud; excluded from routine checks')
    if not args.model.endswith(('-cloud', ':cloud')) or not 1 <= args.timeout <= 300:
        parser.error('a cloud model and timeout 1..300 are required')
    scratch = ROOT/'tmp'
    scratch.mkdir(exist_ok=True)
    data = Path(tempfile.mkdtemp(prefix='live-chat-', dir=scratch))
    recorder = Recorder(args.ollama_url, args.timeout)
    gateway = ThreadingHTTPServer(('127.0.0.1', 0), recorder.handler())
    threading.Thread(target=gateway.serve_forever, daemon=True).start()
    report = {'data': str(data), 'requested_model': args.model, 'cases': [], 'cloud_calls': [], 'status': 'incomplete'}
    def save():
        report['cloud_calls'] = recorder.calls
        (data/'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    command = [str(ROOT/'build/news-library'), '-data', str(data), '-bind', '127.0.0.1:0',
               '-no-worker', '-no-digests=false', '-knowledge=true', '-embeddings=false', '-telegram=false',
               '-filter-rules', '[]', '-interests', '', '-chat=true', '-chat-model', args.model,
               '-chat-timeout', str(args.timeout)+'s', '-ollama-url', f'http://127.0.0.1:{gateway.server_port}/api/chat']
    print('Fixture: '+str(data), flush=True)
    # The application reads private .env itself; no sourcing or copying secrets.
    with (data/'service.log').open('w') as log:
        process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=log, text=True)
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                check(selector.select(timeout=15), 'fixture service startup timed out')
                line = process.stdout.readline().strip()
            check(line.startswith('news-library: http://127.0.0.1:'), 'fixture startup failed; inspect service.log')
            origin = line.removeprefix('news-library: ')
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            def get():
                with opener.open(origin, timeout=15) as r:
                    return r.read().decode()
            def post(path, values, ajax=False):
                headers = {'Origin': origin}
                if ajax:
                    headers['X-Chat-Request'] = '1'
                with opener.open(urllib.request.Request(origin+path, data=urllib.parse.urlencode(values).encode(), headers=headers), timeout=args.timeout+15) as r:
                    return r.read()
            csrf = field(get(), 'csrf')
            # Seed only synthetic primary texts and human tags via supported application routes.
            for i in range(10):
                topic = 'SQLite' if i < 6 else 'Rust'
                post('/items', {'csrf': csrf, 'url': f'https://example.invalid/chat-fixture/{i}'})
                post(f'/items/{i+1}/manual', {'csrf': csrf, 'text': f'{topic}: резервні копії backup, відновлення бази даних і надійність черги. Синтетичний тестовий матеріал {i}.'})
                post(f'/items/{i+1}/tags', {'csrf': csrf, 'tags': 'backup, sqlite' if i < 6 else 'rust'})
            post('/items/1/read', {'csrf': csrf, 'read': '1'})
            with sqlite3.connect(f'file:{data}/library.sqlite?mode=ro', uri=True) as db:
                def send(text):
                    html = get()
                    values = {name: field(html, name) for name in ('csrf', 'token', 'revision')}
                    values['message'] = text
                    before = len(recorder.calls)
                    started = time.monotonic()
                    post('/chat', values, ajax=True)
                    row = db.execute('SELECT session_id,response_json FROM chat_turns ORDER BY id DESC LIMIT 1').fetchone()
                    reply = json.loads(row[1])
                    state = json.loads(db.execute('SELECT state_json FROM chat_sessions WHERE id=?', (row[0],)).fetchone()[0])
                    result = {'input': text, 'seconds': round(time.monotonic()-started, 3), 'reply': reply,
                              'state': state, 'calls': len(recorder.calls)-before}
                    report['cases'].append(result)
                    save()
                    print(f'{len(report["cases"]):02d}: {result["seconds"]:.2f}s / cloud {result["calls"]} / {text}', flush=True)
                    check(reply['Kind'] != 'error', 'chat returned an error: '+reply['Message'])
                    return state, reply
                state, reply = send('Підбери мені матеріали про SQLite')
                check(state['HasSearch'] and state['Filters']['Query'] and len(state['Cards']) > 1, 'topic search was not executed')
                query = state['Filters']['Query']
                state, _ = send('Залиши з цього тільки те, що я ще не прочитав')
                check(state['Filters']['Read'] == '0' and state['Filters']['Query'] == query, 'unread refinement lost the topic')
                state, _ = send('Додай до цього пошуку тег backup')
                check('backup' in state['Filters']['Tags'] and state['Filters']['Query'] == query, 'tag was not added to the active search')
                state, _ = send('Прибери обмеження за тегом, тему і непрочитане залиш')
                check(not state['Filters']['Tags'] and state['Filters']['Read'] == '0' and state['Filters']['Query'] == query, 'removing a tag lost other criteria')
                second = state['Cards'][1]['Item']['ID']
                state, _ = send('Збережи другий матеріал до моєї бібліотеки')
                check(db.execute('SELECT placement FROM items WHERE id=?', (second,)).fetchone()[0] == 'library', 'second card was not saved')
                first = state['Cards'][0]['Item']['ID']
                state, reply = send('Оціни перший матеріал')
                check(reply['Kind'] == 'question' and db.execute('SELECT count(*) FROM rating_events').fetchone()[0] == 0, 'missing score was invented')
                state, _ = send('3')
                check(db.execute('SELECT item_id,score FROM rating_events').fetchall() == [(first, 3)], 'short score reply did not complete the previous question')
                state, _ = send('Перший уже прочитав, познач це')
                check(db.execute('SELECT is_read FROM items WHERE id=?', (first,)).fetchone()[0] == 1, 'shown card was not marked read')
                state, reply = send('Повтори обробку')
                check(reply['Kind'] == 'question', 'ambiguous processing retry did not ask a question')
                state, reply = send('дайджести')
                check('Дайджестів для такого повтору немає' in reply['Message'], 'short retry-kind answer did not choose digest retries')
                state, reply = send('Дооброби старі статті, почни з п’яти')
                check('нових задач 0' in reply['Message'], 'knowledge batch intent was not executed')
                ratings = db.execute('SELECT count(*) FROM rating_events').fetchone()[0]
                state, _ = send('Знайди статті з власною оцінкою 3')
                check(state['Filters']['Rating'] == '3' and db.execute('SELECT count(*) FROM rating_events').fetchone()[0] == ratings, 'search score became a human rating')
                state, _ = send('https://example.invalid/new-a https://example.invalid/new-b')
                check(report['cases'][-1]['calls'] == 0, 'bare URL batch called the planner')
                check(db.execute('SELECT count(*) FROM generated_artifacts').fetchone()[0] == 0, 'paused fixture generated a digest')
                check(db.execute('SELECT count(*) FROM deliveries').fetchone()[0] == 0, 'fixture created a delivery')
            actual = {call.get('model') for call in recorder.calls}
            check(actual and all(model and model.endswith(('-cloud', ':cloud')) for model in actual), 'unexpected model or local generation')
            check(all(call.get('remote_model') and urllib.parse.urlsplit(call.get('remote_host', '')).hostname == 'ollama.com' for call in recorder.calls), 'cloud identity not verified')
            report['status'] = 'passed'
            report['actual_aliases'] = sorted(actual)
            save()
            print('PASS: '+str(data/'report.json'), flush=True)
        except Exception as error:
            report['error'] = str(error)
            save()
            raise
        finally:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            process.stdout.close()
            gateway.shutdown()
            gateway.server_close()


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, sqlite3.Error) as error:
        raise SystemExit('live-chat-smoke: '+str(error))
