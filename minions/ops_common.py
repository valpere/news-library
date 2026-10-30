"""Private operational reports, constrained config and read-only schema access."""
from contextlib import contextmanager
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import shlex
import sqlite3
from urllib.parse import urlsplit
import ipaddress
from urllib.request import HTTPRedirectHandler, ProxyHandler, build_opener

ROOT = Path(__file__).resolve().parents[1]
SCHEMA_VERSION = 1  # keep in step with CurrentSchema in internal/library/backup.go
DEFAULTS = {
    'NEWS_LIBRARY_DATA': 'data', 'NEWS_LIBRARY_BIND': '127.0.0.1:8787',
    'OLLAMA_API_URL': 'http://localhost:11434/api/chat', 'OLLAMA_MODEL': 'gemma4:cloud',
    'NEWS_LIBRARY_NO_WORKER': 'false', 'NEWS_LIBRARY_NO_DIGESTS': 'false',
    'NEWS_LIBRARY_KNOWLEDGE': 'true', 'NEWS_LIBRARY_EMBEDDINGS': 'false',
    'NEWS_LIBRARY_READ_TOKEN': '',
    'NEWS_LIBRARY_OPENROUTER_ENABLED': 'false', 'OPENROUTER_API_KEY': '',
    'NEWS_LIBRARY_OPENROUTER_MODELS': '[]', 'NEWS_LIBRARY_OPENROUTER_DATA_COLLECTION': 'deny',
}


def config(path):
    values = DEFAULTS.copy()
    path = Path(path)
    if path.exists():
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 1 << 20:
            raise ValueError('regular bounded env file required')
        for line in path.read_text().splitlines():
            line = line.strip()
            if line.startswith('export '):
                line = line[7:].strip()
            key, sep, raw = line.partition('=')
            key = key.strip()
            if not sep or key not in values:
                continue
            try:
                words = shlex.split(raw, comments=True, posix=True)
            except ValueError:
                raise ValueError(f'invalid quoting for {key}') from None
            # Deliberately do not evaluate shell code or dotenv interpolation.
            value = ' '.join(words)
            if '$' in value or '\n' in value or '`' in value:
                raise ValueError(f'unsupported interpolation for {key}')
            values[key] = value
    for key in values:
        if key in os.environ:
            values[key] = os.environ[key]
    return values


def no_symlinks(path):
    path = Path(os.path.abspath(path))
    if any(p.is_symlink() for p in (path, *path.parents)):
        raise ValueError('symlink path refused')
    return path


@contextmanager
def database(data):
    path = no_symlinks(Path(data) / 'library.sqlite')
    if not path.is_file():
        raise ValueError('existing database required')
    db = sqlite3.connect(path.as_uri() + '?mode=ro', timeout=5)
    try:
        db.row_factory = sqlite3.Row
        db.execute('PRAGMA query_only=ON')
        db.execute('BEGIN')
        if db.execute('PRAGMA user_version').fetchone()[0] != SCHEMA_VERSION:
            raise ValueError(f'schema v{SCHEMA_VERSION} required; no migration performed')
        yield db
    finally:
        db.close()


def new_output(output):
    tmp = no_symlinks(ROOT / 'tmp')
    path = no_symlinks(output)
    if not path.is_relative_to(tmp) or path == tmp:
        raise ValueError('new subdirectory under project tmp required')
    path.mkdir(parents=True, exist_ok=False, mode=0o700)
    path.chmod(0o700)
    return path


def save(report, output):
    with open(output / 'report.json', 'x', opener=lambda p, f: os.open(p, f, 0o600)) as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
        f.write('\n')


def report(kind):
    return {'schema': f'news-library/{kind}-v1',
            'created_at': datetime.now(timezone.utc).isoformat()}


def loopback_url(raw, allow_query=False):
    u = urlsplit(raw)
    if u.scheme != 'http' or not u.hostname or u.username or u.password or (u.query and not allow_query) or u.fragment:
        raise ValueError('plain loopback HTTP URL required')
    if u.hostname != 'localhost':
        try:
            if not ipaddress.ip_address(u.hostname).is_loopback:
                raise ValueError('loopback required')
        except ValueError:
            raise ValueError('loopback required') from None
    _ = u.port  # Validate without leaking malformed input.
    return u


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def get_json(url, headers=None, timeout=5):
    from urllib.request import Request
    loopback_url(url, allow_query=True)
    opener = build_opener(ProxyHandler({}), NoRedirect())
    with opener.open(Request(url, headers=headers or {}, method='GET'), timeout=timeout) as response:
        body = response.read((2 << 20) + 1)
        if len(body) > 2 << 20:
            raise ValueError('response exceeds limit')
        return json.loads(body)


def data_path(args, settings):
    return args.data or ROOT / settings['NEWS_LIBRARY_DATA']
