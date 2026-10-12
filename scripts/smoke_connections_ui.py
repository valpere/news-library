"""Offline browser acceptance for versioned thematic notes and backlinks."""
import os
from pathlib import Path
import signal
import subprocess
import tempfile
from playwright.sync_api import sync_playwright

root = Path(__file__).resolve().parents[1]
(root / 'tmp').mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(dir=root / 'tmp', prefix='connections-smoke-') as directory:
    process = subprocess.Popen([
        str(root / 'build/news-library'), '-env-file', '', '-data', directory,
        '-bind', '127.0.0.1:0', '-no-worker', '-no-digests', '-embeddings=false',
        '-telegram=false', '-read-token', 'fixture-read-token-1234567890123456',
    ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        line = process.stdout.readline().strip()
        assert line.startswith('news-library: http://'), line
        origin = line.removeprefix('news-library: ')
        with sync_playwright() as pw:
            browser = pw.chromium.launch(
                executable_path=os.environ.get('SMOKE_CHROME', '/usr/bin/google-chrome'),
                headless=True, args=['--no-sandbox'])
            page = browser.new_page(viewport={'width':1280, 'height':960})
            for name in ['wal', 'snapshot', 'limits']:
                page.goto(origin+'/browse')
                page.get_by_label('Додати статтю або YouTube').fill('https://example.com/' + name)
                page.get_by_role('button', name='Додати URL', exact=True).click()
                page.get_by_text('Вставити текст вручну', exact=True).click()
                page.locator('textarea[name=text]').fill('Saved source: ' + name)
                page.get_by_role('button', name='Зберегти текст', exact=True).click()
            page.goto(origin + '/connections/new')
            page.get_by_label('Назва', exact=True).fill('SQLite: узгодженість резервної копії')
            page.get_by_label('Category', exact=True).fill('Зберігання даних')
            page.get_by_label('Tags через кому').fill('sqlite, backup')
            page.get_by_label('Questions — одне питання на рядок').fill('Як відновити базу без втрат?')
            page.get_by_label('Зміст Markdown').fill('## Спільне\nWAL і знімок бази пояснюють узгодженість.')
            page.locator('select[name=item]').nth(0).select_option('1')
            page.locator('select[name=item]').nth(1).select_option('2')
            page.locator('input[name=role]').nth(0).fill('Пояснення WAL')
            page.locator('input[name=role]').nth(1).fill('Приклад знімка')
            page.get_by_role('button', name='Зберегти нову версію').click()
            assert page.url == origin + '/connections/1'
            assert 'версія 1' in page.locator('body').inner_text()
            first = page.request.get(origin + '/connections/1/markdown').text()
            assert 'type: connection' in first and 'questions:' in first
            for item in [1,2]:
                page.goto(origin + f'/items/{item}')
                page.locator('#connection-backlinks summary').click()
                assert page.locator('#connection-backlinks a[href="/connections/1"]').count() == 1
            page.goto(origin + '/connections/1/edit')
            page.get_by_label('Зміст Markdown').fill('## Доповнення\nТретя стаття уточнює межі.')
            page.locator('select[name=item]').nth(0).select_option('3')
            # Two existing source rows precede the new choices.
            page.locator('input[name=role]').nth(2).fill('Уточнення меж')
            page.get_by_role('button', name='Зберегти нову версію').click()
            assert 'версія 2' in page.locator('body').inner_text()
            assert page.locator('article a[href^="/items/3?"]').count() == 1
            page.locator('summary').filter(has_text='Історія версій').click()
            old = page.get_by_role('link', name='Версія 1', exact=False)
            old_url = old.get_attribute('href')
            page.goto(origin + old_url)
            assert 'Спільне' in page.locator('.connection-body').inner_text()
            assert page.request.get(origin + old_url.replace('?', '/markdown?')).text() == first
            page.goto(origin + '/connections?q=як%20відновити')
            assert page.get_by_role('link', name='SQLite: узгодженість резервної копії', exact=True).count() == 1
            assert page.request.get(origin+'/api/read/connections').status == 401
            result = page.request.get(origin+'/api/read/connections/1',headers={'Authorization':'Bearer fixture-read-token-1234567890123456'})
            assert result.status == 200 and len(result.json()['Members']) == 3
            page.goto(origin+'/connections/1')
            page.screenshot(path=str(root/'tmp/smoke-connections.png'),full_page=True)
            browser.close()
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            process.wait(timeout=15)
        assert process.returncode == 0, process.stderr.read()
print('PASS connections browser: create, reciprocal navigation, third article, immutable historical Markdown, Ukrainian question search and authenticated read API. No external network or model calls.')
