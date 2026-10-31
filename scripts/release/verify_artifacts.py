#!/usr/bin/env python3
"""Verify local release bytes against release-manifest.json and SHA256SUMS.

This check is intentionally offline and does not authenticate provenance.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re

import manifest as release_manifest

HASH_RE = re.compile(r'[0-9a-f]{64}\Z')
VERSION_RE = re.compile(r'\d+\.\d+\.\d+\Z')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def expected_asset_names(version):
    return set(release_manifest.ASSET_FIXED) | {f'hollis-{version}-darwin-arm64.zip'}


def parse_checksums(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError('Missing or unsafe SHA256SUMS')
    result = {}
    for line in path.read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9._-]+)', line)
        if not match or match.group(2) in result:
            raise ValueError('Malformed or duplicate SHA256SUMS entry')
        result[match.group(2)] = match.group(1)
    return result


def verify_release_dir(folder, expected_ref=None, expected_commit=None):
    manifest_path = folder / 'release-manifest.json'
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise ValueError('Missing or unsafe release-manifest.json')
    try:
        data = json.loads(manifest_path.read_bytes())
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError('Invalid release-manifest.json') from exc
    required = {'schema_version', 'identity', 'helper', 'platform', 'assets', 'macho', 'bridges', 'signing'}
    if (not isinstance(data, dict) or set(data) != required
            or type(data['schema_version']) is not int or data['schema_version'] != 1):
        raise ValueError('Unsupported release manifest schema')
    identity = data['identity']
    if set(identity) != {'repository', 'source_ref', 'source_commit', 'runtime_version'}:
        raise ValueError('Invalid release identity fields')
    version = identity['runtime_version']
    if (identity['repository'] != release_manifest.REPOSITORY or not isinstance(version, str)
            or not VERSION_RE.fullmatch(version) or identity['source_ref'] != f'refs/tags/v{version}'
            or not re.fullmatch(r'[0-9a-f]{40}', identity['source_commit'])):
        raise ValueError('Invalid release identity')
    if expected_ref is not None and identity['source_ref'] != expected_ref:
        raise ValueError('Release source ref does not match the caller pin')
    if expected_commit is not None and identity['source_commit'] != expected_commit:
        raise ValueError('Release source commit does not match the caller pin')
    if (data['helper'] != {'version': release_manifest.HELPER_VERSION,
                           'protocol_version': release_manifest.HELPER_PROTOCOL}
            or type(data['helper']['protocol_version']) is not int):
        raise ValueError('Helper identity or protocol mismatch')
    if data['platform'] != {'os': 'darwin', 'architecture': 'arm64', 'minimum_macos': '27.0'}:
        raise ValueError('Unsupported release platform')
    expected_names = expected_asset_names(version)
    assets = data['assets']
    if not isinstance(assets, dict) or set(assets) != expected_names:
        raise ValueError('Unexpected release asset manifest membership')
    for name, record in assets.items():
        if not isinstance(record, dict) or set(record) != {'sha256', 'size_bytes'}:
            raise ValueError(f'Invalid asset record: {name}')
        if not isinstance(record['sha256'], str) or not HASH_RE.fullmatch(record['sha256']):
            raise ValueError(f'Invalid asset hash: {name}')
        if type(record['size_bytes']) is not int or record['size_bytes'] <= 0:
            raise ValueError(f'Invalid asset size: {name}')
        path = folder / name
        raw = release_manifest.regular_bytes(path)
        if len(raw) != record['size_bytes'] or digest(raw) != record['sha256']:
            raise ValueError(f'Release asset mismatch: {name}')
    expected_macho = {
        'hollis-darwin-arm64': 'arm64',
        'hollis-darwin-amd64': 'x86_64',
        'hollis-native-darwin-arm64': 'arm64',
    }
    if not isinstance(data['macho'], dict) or set(data['macho']) != set(expected_macho):
        raise ValueError('Mach-O metadata does not cover each CLI and helper artifact')
    for name, arch in expected_macho.items():
        record = data['macho'][name]
        if (set(record) != {'architecture', 'deployment_target', 'sdk_version'}
                or record['architecture'] != arch
                or not isinstance(record['deployment_target'], str)
                or not isinstance(record['sdk_version'], str)
                or not re.fullmatch(r'\d+(?:\.\d+){1,2}', record['deployment_target'])
                or not re.fullmatch(r'\d+(?:\.\d+){1,2}', record['sdk_version'])):
            raise ValueError(f'Invalid Mach-O metadata: {name}')
    bridges = data['bridges']
    bridge_hash = assets['hollis-bridges.zip']['sha256']
    if (set(bridges) != {'archive', 'sha256', 'members'} or bridges['archive'] != 'hollis-bridges.zip'
            or bridges['sha256'] != bridge_hash):
        raise ValueError('Bridge archive identity/hash mismatch')
    actual_members = release_manifest.inspect_bridges((folder / bridges['archive']).read_bytes())
    if bridges['members'] != actual_members:
        raise ValueError('Bridge archive members disagree with the manifest')
    bundle_name = f'hollis-{version}-darwin-arm64.zip'
    release_manifest.inspect_bundle(
        (folder / bundle_name).read_bytes(), version,
        {name: (folder / name).read_bytes() for name in expected_names})
    signing = data['signing']
    if (set(signing) != {'artifacts', 'developer_id', 'notarized'}
            or signing['developer_id'] is not False or signing['notarized'] is not False
            or not isinstance(signing['artifacts'], dict)
            or set(signing['artifacts']) != set(expected_macho)
            or signing['artifacts']['hollis-native-darwin-arm64'] != 'ad_hoc'
            or any(signing['artifacts'][name] not in ('unsigned', 'ad_hoc')
                   for name in ('hollis-darwin-arm64', 'hollis-darwin-amd64'))):
        raise ValueError('Signing claims changed or are not factual')
    sums = parse_checksums(folder / 'SHA256SUMS')
    expected_sum_names = expected_names | {'release-manifest.json'}
    if set(sums) != expected_sum_names:
        raise ValueError('SHA256SUMS membership does not match the release assets')
    for name, expected_hash in sums.items():
        raw = (folder / name).read_bytes()
        if digest(raw) != expected_hash:
            raise ValueError(f'SHA256SUMS mismatch: {name}')
    directory_names = {p.name for p in folder.iterdir()}
    if directory_names != expected_sum_names | {'SHA256SUMS'}:
        raise ValueError('Unexpected or missing release directory entries')
    return data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dist', type=Path)
    parser.add_argument('--source-ref')
    parser.add_argument('--source-commit')
    args = parser.parse_args()
    verify_release_dir(args.dist, args.source_ref, args.source_commit)
    print('Local release bytes and manifest verified; provenance was not checked.')


if __name__ == '__main__':
    main()
