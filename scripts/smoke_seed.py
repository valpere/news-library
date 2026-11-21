"""Shared helper for browser smokes: put synthetic text into a throwaway store before the service starts."""
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parents[1]


def seed(data, url, text, rules=None):
    """Add URL and complete its fetch with the given text (no network); returns the item id.

    `rules` is the service's -filter-rules JSON. Run it while no service uses `data`, and add items one at a time (minions/seed-text).
    """
    Path(data).mkdir(parents=True, exist_ok=True)
    command = ['go', 'run', './minions/seed-text', '-data', str(data), '-url', url, '-text', text]
    if rules:
        command += ['-filter-rules', rules]
    out = subprocess.run(command,
                         cwd=root, capture_output=True, text=True)
    if out.returncode:
        raise RuntimeError('seed-text failed: ' + out.stderr.strip())
    return int(out.stdout.strip())
