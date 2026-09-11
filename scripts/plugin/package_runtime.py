#!/usr/bin/env python3
"""Package an explicit set of runtime files; never walk the checkout."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import stat
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def package(dist, version):
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        raise ValueError('Invalid release version')
    files = {
        'hollis': dist / 'hollis-darwin-arm64',
        'hollis-native': dist / 'hollis-native-darwin-arm64',
        'hollis-bridges.zip': dist / 'hollis-bridges.zip',
        'LICENSE': ROOT / 'LICENSE',
    }
    data = {}
    for name, path in files.items():
        if path.is_symlink() or not path.is_file():
            raise ValueError(f'Missing or unsafe bundle member: {name}')
        data[name] = path.read_bytes()
    manifest = {'version': version, 'native_protocol_version': 1,
                'native_minimum_macos': '27.0', 'architecture': 'arm64',
                'sha256': {k: hashlib.sha256(v).hexdigest() for k, v in data.items()}}
    data['manifest.json'] = (json.dumps(manifest, indent=2, sort_keys=True) + '\n').encode()
    output = dist / f'hollis-{version}-darwin-arm64.zip'
    with zipfile.ZipFile(output, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, content in data.items():
            info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (stat.S_IFREG | (0o755 if name in ('hollis', 'hollis-native') else 0o644)) << 16
            archive.writestr(info, content)
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dist', type=Path)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    print(package(args.dist, args.version))
