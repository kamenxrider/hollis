"""Drive the native helper's JSON protocol without generating text.

This module builds `hollis-native` from the checked-in sources with the same
script the release uses and runs it against fixtures. Every case here is
model-free: requests are rejected before a session exists, or they ask for the
non-generative status event. Nothing in this module, and no test that imports
it, calls the on-device model.
"""
import json
import platform
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
HELPER_SOURCE = ROOT / 'native/HollisNative.swift'
SUBSET_SOURCE = ROOT / 'native/SchemaSubset.swift'
BUILD = ROOT / 'scripts/build-native.sh'
EXTRACTION_SOURCE = ROOT / 'native/Extraction.swift'
HARNESS_SOURCE = ROOT / 'scripts/native/testdata/harness/main.swift'
GO_NATIVE = ROOT / 'internal/runner/native.go'
GOLDENS = Path(__file__).resolve().parent / 'testdata/protocol'


def command(*args, **kwargs):
    timeout = kwargs.pop('timeout', 600)
    return subprocess.run(args, capture_output=True, text=True,
                          timeout=timeout, **kwargs)


def sdk_version():
    result = command('xcrun', '--sdk', 'macosx', '--show-sdk-version')
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def host_major():
    result = command('sw_vers', '-productVersion')
    if result.returncode != 0:
        return None
    return int(result.stdout.strip().split('.')[0])


def can_build():
    """True where the checked-in helper sources can be compiled."""
    if platform.system() != 'Darwin' or platform.machine() != 'arm64':
        return False
    sdk = sdk_version()
    return sdk is not None and int(sdk.split('.')[0]) >= 27


def can_execute():
    """True where a helper built for macOS 27 can run.

    The release image compiles the helper on a macOS 26 host, so building and
    executing are separate questions. Tests that only compile are allowed to
    run anywhere `can_build()` holds.
    """
    return can_build() and (host_major() or 0) >= 27


def build_helper(folder):
    """Build the helper into `folder` and return its path."""
    helper = Path(folder) / 'hollis-native'
    result = command('bash', str(BUILD), str(helper))
    if result.returncode != 0:
        raise RuntimeError(f'helper build failed: {result.stderr}')
    return helper


def build_extraction_harness(folder):
    """Build the extraction test driver into `folder` and return its path."""
    harness = Path(folder) / 'harness'
    sdk_path = command('xcrun', '--sdk', 'macosx', '--show-sdk-path').stdout.strip()
    result = command(
        'xcrun', 'swiftc', '-sdk', sdk_path, '-target', 'arm64-apple-macosx27.0',
        str(EXTRACTION_SOURCE), str(HARNESS_SOURCE), '-o', str(harness))
    if result.returncode != 0:
        raise RuntimeError(f'extraction harness build failed: {result.stderr}')
    return harness


def harness(executable, *args, timeout=600):
    """Run the extraction driver and decode its single JSON answer."""
    result = subprocess.run([str(executable), *args], capture_output=True,
                            text=True, timeout=timeout)
    if result.returncode != 0:
        raise AssertionError(f'harness exited {result.returncode}: {result.stderr}')
    return json.loads(result.stdout)


def run(helper, args=(), stdin=b'', timeout=120):
    """Run the helper once with an optional request on stdin.

    Returns the completed process with byte stdout/stderr so a fixture can
    assert the exact events, including absence of diagnostics.
    """
    process = subprocess.run([str(helper), *args], input=stdin,
                             capture_output=True, timeout=timeout)
    return process


def request_bytes(protocol, operation, **fields):
    payload = {'protocol': protocol, 'operation': operation}
    payload.update(fields)
    return json.dumps(payload).encode()


def events(stdout):
    """Decode an NDJSON event stream.

    A blank stream yields an empty list. Any non-object line is a protocol
    violation and fails the calling test with the offending line.
    """
    parsed = []
    for raw in stdout.splitlines():
        if not raw:
            continue
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise AssertionError(f'event is not an object: {raw!r}')
        parsed.append(value)
    return parsed


def load_goldens():
    cases = []
    for path in sorted(GOLDENS.glob('*.json')):
        cases.append((path, json.loads(path.read_text())))
    return cases


def golden_stdin(case):
    if 'stdin' in case:
        return case['stdin'].encode()
    if 'stdin_repeat' in case:
        repeat = case['stdin_repeat']
        return (repeat['text'] * repeat['count']).encode()
    return b''


SWIFT_VERSION = re.compile(r'static let version = "([^"]+)"')
SWIFT_PROTOCOL = re.compile(r'static let protocolVersion = (\d+)')
GO_PROTOCOL = re.compile(r'const NativeProtocol = (\d+)')
GO_VERSION = re.compile(r'const NativeVersion = "([^"]+)"')
SWIFT_EVENT = re.compile(r'"event": "([a-z_]+)"')
SWIFT_FAILURE = re.compile(r'fail\("([a-z_]+)"')


def swift_pins():
    source = HELPER_SOURCE.read_text()
    version = SWIFT_VERSION.search(source)
    protocol = SWIFT_PROTOCOL.search(source)
    if not version or not protocol:
        raise AssertionError('helper source no longer states its version pins')
    return version.group(1), int(protocol.group(1))


def go_pins():
    source = GO_NATIVE.read_text()
    protocol = GO_PROTOCOL.search(source)
    version = GO_VERSION.search(source)
    if not protocol or not version:
        raise AssertionError('Go runner no longer states the helper pins')
    return version.group(1), int(protocol.group(1))
