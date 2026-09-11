#!/usr/bin/env python3
"""Pin a published runtime after authenticating every downloaded asset.

Writes only the explicit output path. The release commit/tag must already exist;
local candidate hashes are never represented as published provenance.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request

from package_plugin import authenticate, check_bridges, digest, ROOT, UA


def refresh(version, commit, output):
    if not re.fullmatch(r"\d+\.\d+\.\d+", version) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Supply a release version and full source commit")
    if tuple(map(int, version.split('.'))) < (0, 4, 0):
        raise ValueError("Native packaging requires runtime 0.4.0 or later")
    lock = json.loads((ROOT / 'plugins/hollis/runtime.lock.json').read_text())
    lock.update(schema_version=2, version=version, source_commit=commit,
                source_ref=f'refs/tags/v{version}',
                release_url=f'https://github.com/kamenxrider/hollis/releases/download/v{version}')
    lock['native'] = {'name': 'hollis-native-darwin-arm64', 'protocol_version': 1}
    with tempfile.TemporaryDirectory(prefix='hollis-published-pins-') as temp:
        for key in ('binary', 'bridges', 'native'):
            name = lock[key]['name']
            target = Path(temp) / name
            request = urllib.request.Request(f"{lock['release_url']}/{name}", headers={'User-Agent': UA})
            with urllib.request.urlopen(request, timeout=120) as response:
                if not response.geturl().startswith('https://'):
                    raise ValueError('Insecure release redirect')
                target.write_bytes(response.read())
            authenticate(target, lock)
            lock[key]['sha256'] = digest(target)
        check_bridges(Path(temp) / lock['bridges']['name'], lock['bridge_files'])
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
