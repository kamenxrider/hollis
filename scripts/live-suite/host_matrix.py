#!/usr/bin/env python3
"""Run one authorized manifest case through the plugin; journal before dispatch.

Developer test harness, not an installation dependency. No automatic retries.
Share one manifest between hosts to serialize and pace the entire experiment.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import time


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def timestamp():
    return datetime.now(timezone.utc).isoformat()


def write_json(path, value):
    temporary = path.with_suffix('.pending')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def run_case(manifest, case_id):
    case = next(c for c in manifest['cases'] if c['id'] == case_id)
    root = Path(manifest['output']); root.mkdir(parents=True, exist_ok=True)
    receipt = root / (case_id + '.json')
    if receipt.exists():
        raise RuntimeError('A receipt already exists. Inspect it; this case cannot be dispatched again.')
    lock = root / '.active'
    lock.mkdir()  # An abandoned lock requires investigation, never automatic deletion.
    try:
        (lock / 'pid').write_text(str(os.getpid()))
        # A competing host may have completed after our first receipt check.
        if receipt.exists():
            raise RuntimeError('A receipt already exists. This case cannot be dispatched again.')
        state_path = root / 'pacing.json'
        state = json.loads(state_path.read_text()) if state_path.exists() else {'ended': 0, 'blocked': {}}
        if case['sequence'] in state['blocked']:
            raise RuntimeError('Sequence stopped: ' + state['blocked'][case['sequence']])
        if digest(manifest['binary']) != manifest['binary_sha256']:
            raise RuntimeError('Candidate binary no longer matches the reviewed manifest')
        env = dict(os.environ, **manifest['env'])
        helper = str(Path(manifest['kit']) / 'scripts/run.sh')
        resolved = subprocess.run(['/bin/bash', str(Path(manifest['kit']) / 'scripts/setup.sh'), 'path'], env=env, capture_output=True, text=True, check=True)
        if resolved.stdout.strip() != manifest['binary']:
            raise RuntimeError('Plugin resolved an unexpected runtime')
        delay = 45 if case.get('model') == 'cloud-pro' else 10
        time.sleep(max(0, state['ended'] + delay - time.time()))
        record = dict(case, started_at=timestamp(), started_epoch=time.time(), exit_code=None,
                      runtime_sha256=manifest['binary_sha256'], outcome='uncertain')
        record['input_hashes'] = {p: digest(p) for p in case.get('inputs', [])}
        write_json(receipt, record)
        before = time.monotonic()
        try:
            result = subprocess.run(['/bin/bash', helper, *case['arguments']], env=env, text=True, capture_output=True, timeout=180)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            # Hollis has its own 120s process-group deadline. Preserve uncertainty.
            state['blocked'][case['sequence']] = case_id + ': uncertain completion'
            state['ended'] = time.time(); write_json(state_path, state)
            raise
        record.update(ended_at=timestamp(), ended_epoch=time.time(), elapsed_seconds=round(time.monotonic()-before, 3),
                      exit_code=result.returncode, stdout=result.stdout, stderr=result.stderr)
        try:
            payload = json.loads(result.stdout)
        except ValueError:
            payload = {}
        if not isinstance(payload, dict):
            payload = {}
        error = payload.get('error', {})
        if not isinstance(error, dict):
            error = {}
        record['outcome'] = ('success' if 'results' in payload else 'invalid_response') if result.returncode == 0 else error.get('code', 'uncertain')
        if record['outcome'] == 'success' and case.get('image_output'):
            try:
                image = Path(case['image_output']); data = image.read_bytes()
                if len(data) < 24 or data[:8] != b'\x89PNG\r\n\x1a\n':
                    raise ValueError('Invalid PNG header')
                record['image'] = {'path': str(image), 'sha256': digest(image), 'bytes': len(data),
                                   'width': struct.unpack('>I', data[16:20])[0], 'height': struct.unpack('>I', data[20:24])[0]}
            except (OSError, ValueError, struct.error):
                record['outcome'] = 'invalid_image'
        try:
            record['inputs_unchanged'] = all(digest(p) == h for p, h in record['input_hashes'].items())
        except OSError:
            record['inputs_unchanged'] = False
        write_json(receipt, record)
        state['ended'] = record['ended_epoch']
        diagnostic = (result.stdout + result.stderr).lower()
        if not record['inputs_unchanged'] or record['outcome'] not in ('success', 'request_declined') or any(s in diagnostic for s in ('too many incoming requests', 'rate limit', 'rate-limit')):
            state['blocked'][case['sequence']] = case_id + ': ' + record['outcome']
        write_json(state_path, state)
        print(json.dumps(record, indent=2))
        return 0
    finally:
        (lock / 'pid').unlink(missing_ok=True)
        lock.rmdir()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--case', required=True)
    parser.add_argument('--live', action='store_true', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    raise SystemExit(run_case(json.loads(args.manifest.read_text()), args.case))


if __name__ == '__main__':
    main()
