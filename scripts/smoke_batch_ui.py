"""Batch browser acceptance in an isolated persistent fixture; no external calls."""
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import tempfile
from playwright.sync_api import sync_playwright

root=Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
def start(data):
    process=subprocess.Popen([str(root/'build/news-library'),'-env-file','','-data',str(data),
        '-bind','127.0.0.1:0','-no-worker','-no-digests','-embeddings=false','-telegram=false'],
        stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    line=process.stdout.readline().strip()
    assert line.startswith('news-library: http://'),(line,process.stderr.read())
    return process,line.removeprefix('news-library: ')
def stop(process):
    if process.poll() is None:process.send_signal(signal.SIGTERM);process.wait(timeout=15)
    assert process.returncode==0,process.stderr.read()
with tempfile.TemporaryDirectory(dir=root/'tmp',prefix='batch-smoke-') as directory:
    data=Path(directory);process,origin=start(data)
    try:
        with sync_playwright() as pw:
            browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
            page=browser.new_page(viewport={'width':1280,'height':960})
            page.goto(origin)
            page.get_by_text('Додати посилання пачкою',exact=True).click()
            page.get_by_label('Посилання — по одному на рядок').fill('https://example.com/one\nnot-a-url\nhttps://example.com/two\nhttps://example.com/one?utm_source=mail')
            page.get_by_role('button',name='Додати пачку',exact=True).click()
            assert 'Нових: 2 · Уже були: 1 · Не додано: 1' in page.locator('body').inner_text()
            assert page.get_by_label('Посилання — по одному на рядок').input_value()=='not-a-url'
            page.get_by_label('Посилання — по одному на рядок').fill('https://example.com/three')
            page.get_by_role('button',name='Додати пачку',exact=True).click()
            assert 'Нових: 1' in page.locator('body').inner_text()
            page.goto(origin)
            page.get_by_text('Додати посилання пачкою',exact=True).click()
            urls='\n'.join(f'https://example.com/batch/{i}' for i in range(180))
            page.get_by_label('Посилання — по одному на рядок').fill(urls+'\nhttps://example.com/batch/0')
            page.get_by_role('button',name='Додати пачку',exact=True).click()
            assert 'Нових: 180 · Уже були: 1 · Не додано: 0' in page.locator('body').inner_text()
            page.screenshot(path=str(root/'tmp/smoke-batch.png'),full_page=False)
            page.get_by_role('link',name='Переглянути непрочитані вхідні',exact=True).click()
            assert 'placement=inbox' in page.url and 'read=0' in page.url
            page.goto(origin+'/items/1')
            assert 'непрочитано' in page.locator('body').inner_text()
            stop(process)
            process,origin=start(data)
            page.goto(origin+'/items/1')
            assert 'непрочитано' in page.locator('body').inner_text()
            page.goto(origin)
            assert page.locator('article').count()>0
            browser.close()
    finally:stop(process)
    with sqlite3.connect(data/'library.sqlite') as db:
        assert db.execute('select count(*) from items').fetchone()[0]==183
        assert db.execute("select count(*) from jobs where stage='fetch' and state='queued'").fetchone()[0]==183
        assert db.execute('select sum(is_read) from items').fetchone()[0]==0
print('PASS batch browser: mixed input, canonical duplicate, correction, 180-URL batch, unread inbox and restart-persistent queue. No external network/model calls.')
