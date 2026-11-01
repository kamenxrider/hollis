#!/usr/bin/env python3
"""Pin a published runtime after authenticating every downloaded asset.

Writes only the explicit output path. The release commit/tag must already exist;
local candidate hashes are never represented as published provenance.
"""
import argparse
import json
from pathlib import Path
import re
import sys
import tempfile
import urllib.request

from package_plugin import authenticate, check_bridges, ROOT, UA
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'release'))
from verify_artifacts import expected_asset_names, verify_release_dir


def download(lock, name, target):
    request = urllib.request.Request(f"{lock['release_url']}/{name}", headers={'User-Agent': UA})
    with urllib.request.urlopen(request, timeout=120) as response:
        if not response.geturl().startswith('https://'):
            raise ValueError('Insecure release redirect')
        target.write_bytes(response.read())


def refresh(version, commit, output):
    if not re.fullmatch(r"\d+\.\d+\.\d+", version) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Supply a release version and full source commit")
    if tuple(map(int, version.split('.'))) < (0, 4, 0):
        raise ValueError("Native packaging requires runtime 0.4.0 or later")
    lock = json.loads((ROOT / 'plugins/hollis/runtime.lock.json').read_text())
    lock.update(schema_version=2, version=version, source_commit=commit,
                source_ref=f'refs/tags/v{version}',
                release_url=f'https://github.com/kamenxrider/hollis/releases/download/v{version}')
    lock['native'] = {'name': 'hollis-native-darwin-arm64', 'protocol_version': 2}
    with tempfile.TemporaryDirectory(prefix='hollis-published-pins-') as temp:
        folder = Path(temp)
        # Authenticate the common authority against caller-selected source pins
        # before using any hash it contains. Asset names never come from JSON.
        sidecar = folder / 'release-manifest.json'
        download(lock, sidecar.name, sidecar)
        authenticate(sidecar, lock)
        for name in sorted(expected_asset_names(version) | {'SHA256SUMS'}):
            target = Path(temp) / name
            download(lock, name, target)
            authenticate(target, lock)
        record = verify_release_dir(folder, lock['source_ref'], commit)
        for key in ('binary', 'bridges', 'native'):
            lock[key]['sha256'] = record['assets'][lock[key]['name']]['sha256']
        lock['native']['version'] = record['helper']['version']
        lock['native']['protocol_version'] = record['helper']['protocol_version']
        lock['bridge_files'] = [item['name'] for item in record['bridges']['members']]
        check_bridges(folder / lock['bridges']['name'], lock['bridge_files'])
    # Never overwrite a lock by accident. Review and copy the authenticated result.
    with output.open('x') as stream:
        json.dump(lock, stream, indent=2)
        stream.write('\n')
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    print(refresh(args.version, args.source_commit, args.output))
