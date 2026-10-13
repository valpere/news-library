#!/usr/bin/env python3
"""Read only a selected Chromium CDP tab; leave the user's browser running."""
import argparse
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[1]


def checked_url(value):
    parts = urlsplit(value)
    if parts.scheme not in ('http', 'https') or not parts.hostname or parts.username or parts.password:
        raise ValueError('use an HTTP(S) page URL without credentials')
    if parts.query or parts.fragment:
        raise ValueError('select a page by origin/path, without query or fragment')
    return parts


def matching_url(candidate, target):
    wanted = checked_url(target)
    actual = urlsplit(candidate)
    # Exact origin, not a string prefix that could match a different host.
    defaults = {'http': 80, 'https': 443}
    if (actual.scheme, actual.hostname, actual.port or defaults.get(actual.scheme)) != (wanted.scheme, wanted.hostname, wanted.port or defaults[wanted.scheme]):
        return False
    path = wanted.path or '/'
    return actual.path == path or (path.endswith('/') and actual.path.startswith(path))


def public_url(value):
    parts = urlsplit(value)
    if parts.scheme not in ('http', 'https'):
        return None
    # Links and tab URLs do not retain query secrets or fragments.
    return urlunsplit((parts.scheme, parts.netloc.split('@')[-1], parts.path, '', ''))


def clean_text(value):
    return re.sub(r'sk-(?:or-)?[A-Za-z0-9_-]+', '[redacted]', value)[:250]


def write_report(output, report):
    tmp = ROOT / 'tmp'
    path = Path(output).absolute()
    relative = path.relative_to(tmp)
    if not relative.parts or '..' in relative.parts or tmp.is_symlink():
        raise ValueError('output must be a new file under project tmp/')
    current = tmp
    for part in relative.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError('output symlink refused')
    path.parent.mkdir(parents=True, exist_ok=True)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write('\n')


def inspect_page(page):
    report = page.evaluate('''() => {
        const visible = e => !!e.getClientRects().length && getComputedStyle(e).visibility !== 'hidden';
        const label = e => e.getAttribute('aria-label') ||
            (e.getAttribute('aria-labelledby') || '').split(/\\s+/).map(id => document.getElementById(id)?.innerText || '').join(' ').trim() ||
            (e.id ? Array.from(document.querySelectorAll('label')).find(l => l.htmlFor === e.id)?.innerText : '') ||
            e.closest('label')?.innerText || e.innerText || '';
        return {
            url: location.href,
            title: document.title,
            headings: Array.from(document.querySelectorAll('h1,h2,h3')).filter(visible).map(e => e.innerText),
            links: Array.from(document.querySelectorAll('a[href]')).filter(visible).map(e => ({text:label(e),url:e.href})),
            buttons: Array.from(document.querySelectorAll('button')).filter(visible).map(e => label(e)),
            controls: Array.from(document.querySelectorAll('[role="switch"],[role="checkbox"],input[type="checkbox"]')).filter(visible).map(e => ({label:label(e),checked:e.hasAttribute('aria-checked') ? e.getAttribute('aria-checked') === 'true' : e.checked,disabled:e.disabled === true}))
        };
    }''')
    report['url'] = public_url(report['url'])
    report['title'] = clean_text(report['title'])
    report['headings'] = [clean_text(s) for s in report['headings']]
    report['buttons'] = [clean_text(s) for s in report['buttons']]
    report['links'] = [{'text': clean_text(s['text']), 'url': public_url(s['url'])} for s in report['links'] if public_url(s['url'])]
    for control in report['controls']:
        control['label'] = clean_text(control['label'])
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--port', type=int, default=9222)
    parser.add_argument('--url', required=True, help='exact page URL; trailing / permits a path prefix')
    parser.add_argument('--list', action='store_true', help='list matching tabs only')
    parser.add_argument('--wait-for', help='wait for this visible CSS selector before reading')
    parser.add_argument('--timeout-ms', type=int, default=15000)
    parser.add_argument('--output', type=Path, help='new JSON file under project tmp/')
    args = parser.parse_args()
    try:
        checked_url(args.url)
        if not 1 <= args.port <= 65535 or not 1 <= args.timeout_ms <= 30000:
            raise ValueError('invalid port or timeout (max 30000 ms)')
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            browser = p.chromium.connect_over_cdp(f'http://127.0.0.1:{args.port}', timeout=args.timeout_ms)
            pages = [page for context in browser.contexts for page in context.pages if matching_url(page.url, args.url)]
            if args.list:
                report = {'tabs': [{'url': public_url(page.url), 'title': clean_text(page.title())} for page in pages]}
            else:
                if len(pages) != 1:
                    raise ValueError(f'expected one matching tab, found {len(pages)}; refine --url or use --list')
                page = pages[0]
                if args.wait_for:
                    page.locator(args.wait_for).first.wait_for(state='visible', timeout=args.timeout_ms)
                if not matching_url(page.url, args.url):
                    raise ValueError('selected tab navigated outside requested scope')
                report = inspect_page(page)
                if not matching_url(report['url'], args.url):
                    raise ValueError('snapshot is outside requested tab scope')
        if args.output:
            write_report(args.output, report)
            print(f'Saved read-only CDP report: {args.output}')
        else:
            print(json.dumps(report, ensure_ascii=False, indent=2))
    except Exception as exc:
        # Browser exceptions can include DOM content; retain only the error type.
        if isinstance(exc, (ValueError, FileExistsError)):
            print(f'CDP inspection failed: {exc}', file=sys.stderr)
        else:
            print(f'CDP inspection failed: {type(exc).__name__}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
