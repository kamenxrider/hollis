#!/usr/bin/env python3
"""Verify the local MCP SDK replacement against its pinned upstream and patch."""
import base64
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE = 'github.com/modelcontextprotocol/go-sdk'
VERSION = 'v1.8.0'
COMMIT = '3f3b699b2b67e1ed033a63d6651671dab53c2d32'
MODULE_SUM = 'h1:KIvahhYqwtbeniWVPs3TcXEA7b8jEtwfBpOTAI+Urx4='
PROVENANCE = 'third_party/go-sdk.provenance.json'
PATCH = 'third_party/go-sdk-structured-content.patch'
SOURCE = 'third_party/go-sdk'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def source_hashes(directory):
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError('SDK source is not a regular directory')
    result = {}
    for path in directory.rglob('*'):
        if path.is_symlink():
            raise ValueError('SDK source contains a symlink')
        if path.is_file():
            result[path.relative_to(directory).as_posix()] = digest(path.read_bytes())
    return result


def verify(root=ROOT):
    provenance = json.loads((root / PROVENANCE).read_bytes())
    if (provenance['module'], provenance['version'], provenance['commit'], provenance['module_sum']) != (
            MODULE, VERSION, COMMIT, MODULE_SUM):
        raise ValueError('SDK upstream identity changed')
    upstream = provenance['upstream_files']
    lines = ''.join(f'{sha}  {MODULE}@{VERSION}/{name}\n' for name, sha in sorted(upstream.items()))
    module_sum = 'h1:' + base64.b64encode(hashlib.sha256(lines.encode()).digest()).decode()
    if module_sum != MODULE_SUM:
        raise ValueError('SDK upstream source hashes do not match the official module sum')
    if f'{MODULE} {VERSION} {MODULE_SUM}' not in (root / 'go.sum').read_text().splitlines():
        raise ValueError('SDK upstream checksum is missing from go.sum')
    mod = (root / 'go.mod').read_text()
    if f'{MODULE} {VERSION}' not in mod or f'replace {MODULE} => ./third_party/go-sdk' not in mod:
        raise ValueError('SDK requirement or local replacement changed')
    patch = root / PATCH
    if digest(patch.read_bytes()) != provenance['patch_sha256']:
        raise ValueError('SDK patch checksum changed')
    expected = dict(upstream)
    expected.update(provenance['modified_files'])
    expected.update(provenance['added_files'])
    if source_hashes(root / SOURCE) != expected:
        raise ValueError('SDK replacement contents differ from the reviewed source hashes')
    with tempfile.TemporaryDirectory(prefix='hollis-sdk-check-') as temp:
        restored = Path(temp) / 'sdk'
        shutil.copytree(root / SOURCE, restored)
        result = subprocess.run(['git', 'apply', '--reverse', str(patch.resolve())],
                                cwd=restored, capture_output=True, text=True)
        if result.returncode:
            raise ValueError('SDK patch does not reverse cleanly')
        if source_hashes(restored) != upstream:
            raise ValueError('Reversing the SDK patch did not restore exact upstream sources')
    return len(expected)


if __name__ == '__main__':
    print(f'Verified {verify()} MCP SDK files against the pinned upstream module and patch.')
