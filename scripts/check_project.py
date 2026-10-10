#!/usr/bin/env python3
"""Offline checks for documentation, Python syntax and saved pilot records."""
import ast
import json
from pathlib import Path
import re


ROOT = Path(__file__).resolve().parents[1]


def main():
    failures = []
    for path in sorted((ROOT/'scripts').glob('*.py')):
        try:
            ast.parse(path.read_text(), filename=str(path))
        except SyntaxError as error:
            failures.append(str(error))
    docs = [ROOT/n for n in ['README.md','START_HERE.md','AGENTS.md']]
    docs += sorted((ROOT/'docs').glob('*.md'))
    for path in docs:
        for target in re.findall(r'\]\(([^)]+)\)', path.read_text()):
            if target.startswith(('http:', 'https:', '/', '~/', '#', 'mailto:')):
                continue
            target = target.split('#', 1)[0]
            if not (path.parent/target).exists():
                failures.append(f'{path.relative_to(ROOT)}: missing {target}')
    checked = 0
    for path in (ROOT/'experiments').rglob('manifest.json'):
        from news_library_pilot import digest, validate
        manifest = json.loads(path.read_text())
        samples = {s['id']: s for s in manifest['sample']}
        if len(samples) != len(manifest['sample']):
            failures.append(f'{path.relative_to(ROOT)}: duplicate sample IDs')
        if manifest['system_sha256'] != digest(manifest['system_prompt']):
            failures.append(f'{path.relative_to(ROOT)}: prompt hash mismatch')
        for result_path in (path.parent/'results').glob('*.json'):
            result = json.loads(result_path.read_text())
            sample = samples.get(result['id'])
            if sample is None or result['text_sha256'] != sample['text_sha256']:
                failures.append(f'{result_path.relative_to(ROOT)}: sample mismatch')
            if result['prompt_sha256'] != manifest['system_sha256']:
                failures.append(f'{result_path.relative_to(ROOT)}: prompt mismatch')
            if result['status'] == 'ok':
                try:
                    validate(result['answer'])
                except ValueError as error:
                    failures.append(f'{result_path.relative_to(ROOT)}: {error}')
            checked += 1
    if failures:
        raise SystemExit('\n'.join(failures))
    print(f'OK: Python syntax, {len(docs)} documents, {checked} saved model responses; no network calls.')


if __name__ == '__main__':
    main()
