#!/usr/bin/env python3
"""Render an unsubmitted Homebrew formula from a verified local release set."""
import argparse
from pathlib import Path
import re

import verify_artifacts

ROOT = Path(__file__).resolve().parents[2]
TEMPLATE = ROOT / 'packaging/homebrew/hollis.rb.in'


def render_installer(dist, output, template=ROOT / 'scripts/install.sh'):
    """Render pins from verified bytes; callers must authenticate provenance first."""
    record = verify_artifacts.verify_release_dir(dist)
    identity = record['identity']
    version = identity['runtime_version']
    pins = {
        'version': version,
        'protocol': str(record['helper']['protocol_version']),
        'source_commit': identity['source_commit'],
        'asset_sha256': record['assets'][f'hollis-{version}-darwin-arm64.zip']['sha256'],
    }
    source = template.read_text(encoding='utf-8')
    for key, value in pins.items():
        source, count = re.subn(rf'^{key}=.*$', f'{key}={value}', source, flags=re.MULTILINE)
        if count != 1:
            raise ValueError(f'Expected one installer pin: {key}')
    output = Path(output)
    with output.open('x', encoding='utf-8') as stream:
        stream.write(source)
    return output


def render_formula(dist, output, template=TEMPLATE):
    manifest = verify_artifacts.verify_release_dir(dist)
    identity = manifest['identity']
    bundle_name = f"hollis-{identity['runtime_version']}-darwin-arm64.zip"
    bundle_hash = manifest['assets'][bundle_name]['sha256']
    source = template.read_text(encoding='utf-8')
    replacements = {
        '{{VERSION}}': identity['runtime_version'],
        '{{BUNDLE_NAME}}': bundle_name,
        '{{BUNDLE_SHA256}}': bundle_hash,
        '{{HELPER_PROTOCOL}}': str(manifest['helper']['protocol_version']),
        '{{HELPER_VERSION}}': manifest['helper']['version'],
    }
    for marker, value in replacements.items():
        source = source.replace(marker, value)
    if '{{' in source or '}}' in source:
        raise ValueError('Formula template contains an unresolved placeholder')
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists() or output.is_symlink():
        raise FileExistsError(f'Refusing to overwrite formula: {output}')
    with output.open('x', encoding='utf-8') as stream:
        stream.write(source)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dist', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--template', type=Path, default=TEMPLATE)
    args = parser.parse_args()
    print(render_formula(args.dist, args.output, args.template))
    print('Prepared locally only; no tap or package-manager state was changed.')


if __name__ == '__main__':
    main()
