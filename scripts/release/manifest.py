#!/usr/bin/env python3
"""Create the canonical, byte-matched Hollis release manifest.

This records local build facts only. It does not authenticate a release or
assert that candidate bytes have been published.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import zipfile

REPOSITORY = 'kamenxrider/hollis'
HELPER_VERSION = '0.4.0'
HELPER_PROTOCOL = 1
BRIDGE_MEMBERS = {
    'AFM Bridge - Cloud.shortcut',
    'AFM Bridge - Cloud Pro.shortcut',
    'AFM Bridge - On-Device.shortcut',
    'AFM Bridge - ChatGPT.shortcut',
    'Hollis Image - Reference Input v2.shortcut',
}
BUNDLE_MEMBERS = {'hollis', 'hollis-native', 'hollis-bridges.zip', 'LICENSE', 'manifest.json'}
ASSET_FIXED = ('hollis-darwin-arm64', 'hollis-darwin-amd64',
               'hollis-native-darwin-arm64', 'hollis-bridges.zip', 'hollis.spdx.json')
VERSION_RE = re.compile(r'\d+\.\d+\.\d+\Z')
HASH_RE = re.compile(r'[0-9a-f]{40}\Z')


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def regular_bytes(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f'Missing or unsafe release artifact: {path.name}')
    data = path.read_bytes()
    if not data:
        raise ValueError(f'Empty release artifact: {path.name}')
    return data


def inspect_macho(path, expected_arch):
    arch_result = subprocess.run(['xcrun', 'lipo', '-archs', str(path)],
                                 check=True, capture_output=True, text=True)
    architectures = arch_result.stdout.split()
    if architectures != [expected_arch]:
        raise ValueError(f'Unexpected Mach-O architecture for {path.name}')
    build_result = subprocess.run(['xcrun', 'vtool', '-show-build', str(path)],
                                  check=True, capture_output=True, text=True)
    minos = re.search(r'\bminos\s+([0-9]+(?:\.[0-9]+){1,2})\b', build_result.stdout)
    sdk = re.search(r'\bsdk\s+([0-9]+(?:\.[0-9]+){1,2})\b', build_result.stdout)
    if not minos or not sdk:
        raise ValueError(f'Missing Mach-O deployment target or SDK stamp for {path.name}')
    return {
        'architecture': expected_arch,
        'deployment_target': minos.group(1),
        'sdk_version': sdk.group(1),
    }


def inspect_signature(path):
    result = subprocess.run(['codesign', '-dvv', str(path)],
                            check=False, capture_output=True, text=True)
    output = result.stdout + result.stderr
    if re.search(r'\bSignature=adhoc\b', output, re.IGNORECASE):
        return 'ad_hoc'
    if 'code object is not signed' in output.lower() or 'code object is not signed at all' in output.lower():
        return 'unsigned'
    if result.returncode == 0 and ('Authority=' in output or 'TeamIdentifier=' in output):
        return 'signed'
    raise ValueError(f'Unable to determine signing status for {path.name}')


def inspect_bridges(data):
    import io
    members = []
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        infos = archive.infolist()
        names = [item.filename for item in infos]
        if len(names) != len(set(names)) or set(names) != BRIDGE_MEMBERS:
            raise ValueError('Bridge archive must contain exactly the five pinned bridges')
        for item in infos:
            mode = (item.external_attr >> 16) & 0o170000
            if mode not in (0, 0o100000) or item.is_dir():
                raise ValueError(f'Unsafe bridge archive member: {item.filename}')
            if item.filename.startswith('/') or '..' in Path(item.filename).parts or '/' in item.filename:
                raise ValueError(f'Unsafe bridge archive member: {item.filename}')
            members.append({'name': item.filename, 'sha256': sha256(archive.read(item))})
    return sorted(members, key=lambda item: item['name'])


def inspect_bundle(data, version, expected_contents=None):
    import io
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        infos = archive.infolist()
        names = [item.filename for item in infos]
        if len(names) != len(set(names)) or set(names) != BUNDLE_MEMBERS:
            raise ValueError('Runtime bundle must preserve its exact five members')
        payload = {}
        for item in infos:
            mode = (item.external_attr >> 16) & 0o170000
            if item.is_dir() or mode not in (0, 0o100000):
                raise ValueError(f'Unsafe runtime bundle member: {item.filename}')
            payload[item.filename] = archive.read(item)
    try:
        inner = json.loads(payload['manifest.json'])
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError('Invalid runtime bundle manifest') from exc
    expected_fields = {'version', 'native_protocol_version', 'native_minimum_macos', 'architecture', 'sha256'}
    if set(inner) != expected_fields or inner['version'] != version:
        raise ValueError('Runtime bundle manifest identity or fields changed')
    if (type(inner['native_protocol_version']) is not int or inner['native_protocol_version'] != 1
            or inner['native_minimum_macos'] != '27.0'
            or inner['architecture'] != 'arm64'):
        raise ValueError('Runtime bundle compatibility fields changed')
    expected_hashes = {name: sha256(payload[name]) for name in BUNDLE_MEMBERS - {'manifest.json'}}
    if inner['sha256'] != expected_hashes:
        raise ValueError('Runtime bundle member hashes disagree with its manifest')
    if expected_contents is not None:
        for member, asset in (('hollis', 'hollis-darwin-arm64'),
                              ('hollis-native', 'hollis-native-darwin-arm64'),
                              ('hollis-bridges.zip', 'hollis-bridges.zip')):
            if payload[member] != expected_contents[asset]:
                raise ValueError(f'Runtime bundle differs from final release asset: {asset}')
    return inner


def create_manifest(dist, version, source_ref, source_commit,
                    macho_inspector=inspect_macho, signature_inspector=inspect_signature):
    if not VERSION_RE.fullmatch(version):
        raise ValueError('Invalid runtime version')
    if source_ref != f'refs/tags/v{version}' or not HASH_RE.fullmatch(source_commit):
        raise ValueError('Release source identity must be a tag and full commit')
    bundle_name = f'hollis-{version}-darwin-arm64.zip'
    names = set(ASSET_FIXED) | {bundle_name}
    contents = {name: regular_bytes(dist / name) for name in names}
    inspect_bundle(contents[bundle_name], version, contents)
    bridge_members = inspect_bridges(contents['hollis-bridges.zip'])
    expected_arches = {
        'hollis-darwin-arm64': 'arm64',
        'hollis-darwin-amd64': 'x86_64',
        'hollis-native-darwin-arm64': 'arm64',
    }
    macho = {name: macho_inspector(dist / name, arch) for name, arch in expected_arches.items()}
    signing = {name: signature_inspector(dist / name) for name in expected_arches}
    if signing['hollis-native-darwin-arm64'] != 'ad_hoc':
        raise ValueError('The native helper must retain its factual ad-hoc signature')
    if any(signing[name] not in ('unsigned', 'ad_hoc')
           for name in ('hollis-darwin-arm64', 'hollis-darwin-amd64')):
        raise ValueError('CLI signing changed; review the release contract before packaging')
    assets = {name: {'sha256': sha256(data), 'size_bytes': len(data)}
              for name, data in sorted(contents.items())}
    return {
        'schema_version': 1,
        'identity': {
            'repository': REPOSITORY,
            'source_ref': source_ref,
            'source_commit': source_commit,
            'runtime_version': version,
        },
        'helper': {'version': HELPER_VERSION, 'protocol_version': HELPER_PROTOCOL},
        'platform': {'os': 'darwin', 'architecture': 'arm64', 'minimum_macos': '27.0'},
        'assets': assets,
        'macho': macho,
        'bridges': {
            'archive': 'hollis-bridges.zip',
            'sha256': assets['hollis-bridges.zip']['sha256'],
            'members': bridge_members,
        },
        'signing': {
            'artifacts': signing,
            'developer_id': False,
            'notarized': False,
        },
    }


def write_manifest(dist, version, source_ref, source_commit):
    manifest = create_manifest(dist, version, source_ref, source_commit)
    path = dist / 'release-manifest.json'
    if path.exists() or path.is_symlink():
        raise FileExistsError(f'Refusing to overwrite {path.name}')
    with path.open('x', encoding='utf-8') as stream:
        stream.write(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dist', type=Path)
    parser.add_argument('--version', required=True)
    parser.add_argument('--source-ref', required=True)
    parser.add_argument('--source-commit', required=True)
    args = parser.parse_args()
    print(write_manifest(args.dist, args.version, args.source_ref, args.source_commit))


if __name__ == '__main__':
    main()
