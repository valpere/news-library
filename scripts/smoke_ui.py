import subprocess
import signal
import os
import tempfile
import time
from pathlib import Path
from playwright.sync_api import sync_playwright

root=Path(__file__).resolve().parents[1]
(root/'tmp').mkdir(exist_ok=True)
smoke_tmp=tempfile.TemporaryDirectory(dir=root/'tmp', prefix="news-library-smoke-")
data=Path(smoke_tmp.name)
data.mkdir(parents=True,exist_ok=True)
def start(no_worker=False):
    command=[str(root/'build/news-library'),'-env-file','','-data',str(data),'-bind','127.0.0.1:0','-no-digests']
    if no_worker: command.append('-no-worker')
    p=subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    line=p.stdout.readline().strip()
    if not line.startswith('news-library: http://127.0.0.1:'): raise RuntimeError(line+p.stderr.read())
    # Exercise the localhost alias, including Chromium form Origin headers.
    return p,line.removeprefix('news-library: ').replace('127.0.0.1','localhost')
def stop(p):
    p.send_signal(signal.SIGTERM)
    p.wait(timeout=15)
    if p.returncode: raise RuntimeError(p.stderr.read())
p,origin=start()
try:
    with sync_playwright() as pw:
        browser=pw.chromium.launch(executable_path=os.environ.get('SMOKE_CHROME','/usr/bin/google-chrome'),headless=True,args=['--no-sandbox'])
        page=browser.new_page(viewport={'width':1280,'height':960})
        page.goto(origin+'/browse')
        page.get_by_label('Додати статтю або YouTube').fill('http://127.0.0.1/private-smoke')
        page.get_by_role('button',name='Додати URL',exact=True).click()
        assert page.locator('select[name=score]').count() == 0
        # The worker may finish before navigation; otherwise the pending card
        # must update itself without a manual page.reload().
        page.get_by_role('heading',name='Текст недоступний',exact=True).wait_for(timeout=15000)
        assert page.locator('select[name=score]').count() == 0
        page.get_by_text('Вставити текст вручну',exact=True).click()
        page.locator('textarea[name=text]').fill('Smoke knowledge: durable queue and useful Go articles. <script>alert(1)</script>')
        page.get_by_role('button',name='Зберегти текст',exact=True).click()
        page.locator('select[name=score]').select_option('3')
        page.get_by_role('button',name='Оцінити особисту корисність',exact=True).click()
        page.locator('input[name=tags]').fill('go, smoke')
        page.get_by_role('button',name='Зберегти теги',exact=True).click()
        assert '<script>alert(1)</script>' in page.locator('pre').text_content()
        assert page.locator('script').count() == 0
        page.goto(origin+'/browse')
        page.locator('input[name=q]').fill('knowledge')
        page.locator('select[name=usefulness]').select_option('3')
        page.locator('input[name=tags]').fill('smoke')
        page.get_by_role('button',name='Знайти',exact=True).click()
        assert page.locator('article').count()==1
        page.screenshot(path=str(root/'tmp/smoke-search.png'),full_page=True)
        page.locator('article h2 a').click()
        assert 'непрочитано' in page.locator('body').inner_text()
        page.locator('select[name=score]').select_option('0')
        page.get_by_role('button',name='Оцінити особисту корисність',exact=True).click()
        page.get_by_role('button',name='Позначити прочитаним',exact=True).click()
        assert 'корисність: Нульова' in page.locator('body').inner_text()
        page.screenshot(path=str(root/'tmp/smoke-detail.png'),full_page=True)
        stop(p)
        p,origin=start()
        page.goto(origin+'/items/1')
        assert 'корисність: Нульова' in page.locator('body').inner_text()
        assert page.get_by_role('button',name='Позначити непрочитаним',exact=True).count()==1
        assert 'durable queue' in page.locator('pre').text_content()
        page.goto(origin+'/browse')
        page.get_by_label('Додати статтю або YouTube').fill('http://127.0.0.1/private-smoke')
        page.get_by_role('button',name='Додати URL',exact=True).click()
        assert page.url.endswith('/items/1')
        # Keep the worker paused to observe queued cards deterministically.
        stop(p)
        p,origin=start(no_worker=True)
        def add_pending(url):
            page.goto(origin+'/browse')
            page.get_by_label('Додати статтю або YouTube').fill(url)
            page.get_by_role('button',name='Додати URL',exact=True).click()
            assert page.get_by_role('heading',name='Посилання збережено',exact=True).count()==1
            assert page.locator('select[name=score]').count()==0
            return page.url.removeprefix(origin),page.locator('input[name=csrf]').first.get_attribute('value')
        path,token=add_pending('https://example.com/pending-ui')
        page.screenshot(path=str(root/'tmp/smoke-pending.png'),full_page=True)
        response=page.request.post(origin+path+'/manual',form={'csrf':token,'text':'Text now available after background processing'},max_redirects=0)
        assert response.status==303
        page.get_by_role('heading',name='Дайджест недоступний',exact=True).wait_for(timeout=15000)
        assert 'Text now available' in page.locator('pre').text_content()
        assert page.locator('select[name=score]').count()==1
        path,token=add_pending('https://example.com/pending-ui-edits')
        page.get_by_text('Вставити текст вручну',exact=True).click()
        page.locator('textarea[name=text]').fill('Unsubmitted text must stay')
        response=page.request.post(origin+path+'/manual',form={'csrf':token,'text':'Snapshot from background processing'},max_redirects=0)
        assert response.status==303
        page.locator('#processing-refresh').wait_for(state='visible',timeout=15000)
        assert page.locator('textarea[name=text]').input_value()=='Unsubmitted text must stay'
        assert page.locator('select[name=score]').count()==0
        assert page.locator('pre').count()==0
        page.locator('#processing-refresh').click()
        assert 'Snapshot from background processing' in page.locator('pre').text_content()
        assert page.locator('select[name=score]').count()==1
        browser.close()
finally:
    if p.poll() is None: stop(p)
    smoke_tmp.cleanup()
print('PASS browser smoke: needs_input, manual text, estimative usefulness Помірна→Нульова, all items retained, tags/FTS, explicit read, escaped HTML, history, restart/duplicates, queued card without ratings, automatic result display and preserved unsent input. Screenshots: tmp/smoke-search.png, tmp/smoke-detail.png, tmp/smoke-pending.png')
