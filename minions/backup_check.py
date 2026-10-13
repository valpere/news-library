#!/usr/bin/env python3
"""Verify an existing backup and test restore under tmp; never start restored services."""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import ops_common as ops


def check(source, output, binary):
    source, binary = ops.no_symlinks(source), ops.no_symlinks(binary)
    if not (source / 'backup.json').is_file() or not binary.is_file():
        raise ValueError('existing backup and built application required')
    output = ops.new_output(output)
    result = ops.report('backup-check')
    result['steps'] = []
    result['passed'] = False
    for command, extra in [('verify-backup', []), ('restore', ['-output', str(output / 'restored')])]:
        try:
            proc = subprocess.run([str(binary), command, '-source', str(source), *extra],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=1800, check=False)
            status = proc.returncode
        except subprocess.TimeoutExpired:
            status = 'timeout'
        result['steps'].append({'command': command, 'exit_status': status})
        if status != 0:
            ops.save(result, output)
            return result
    result['workers_paused'] = (output / 'restored/.restore-paused').is_file()
    result['restored_database_present'] = (output / 'restored/library.sqlite').is_file()
    result['passed'] = result['workers_paused'] and result['restored_database_present']
    ops.save(result, output)
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--source', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--binary', type=Path, default=ops.ROOT / 'build/news-library')
    args = p.parse_args()
    try:
        result = check(args.source, args.output, args.binary)
    except (OSError, ValueError) as exc:
        print(f'Backup check failed ({type(exc).__name__}); no service started.', file=sys.stderr)
        return 1
    print(json.dumps(result))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
