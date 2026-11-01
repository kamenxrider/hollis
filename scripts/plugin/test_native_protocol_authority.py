"""Receipt-authority fixtures for the pinned native helper protocol.

The receipt pins the protocol; the observed helper must match it. These tests
never call Apple services: the helper and CLI are synthetic scripts and every
runtime lives in a temporary directory.
"""
import json
from pathlib import Path
import subprocess
import unittest

import package_plugin
import test_native_package

COMMON = Path(package_plugin.__file__).resolve().parents[2] / 'plugins/hollis/scripts/common.sh'


class ProtocolAuthorityTests(unittest.TestCase):
    def setUp(self):
        self.fixture = test_native_package.NativeInstallerTests('runTest')
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)

    def pin(self, protocol):
        self.fixture.lock['native']['protocol_version'] = protocol
        self.fixture.relock()

    def helper(self, protocol, version='0.3.0'):
        self.fixture.helper.write_text(
            '#!/bin/bash\ncase "$1" in\n'
            f'--version) echo "hollis-native {version}";;\n'
            f'--protocol-version) echo {protocol};;\n'
            '*) exit 88;;\nesac\n')
        self.fixture.lock['native']['sha256'] = package_plugin.digest(self.fixture.helper)
        self.fixture.relock()

    def record(self):
        result = self.fixture.call('runtime-record')
        # The record is always one complete, valid JSON document, whatever the
        # receipt says; the exit code carries the outcome.
        self.assertTrue(result.stdout.endswith('\n'), result.stdout)
        self.assertEqual(result.stdout.count('\n'), 1, result.stdout)
        payload = json.loads(result.stdout)
        self.assertEqual(payload['contract_version'], 1)
        self.assertEqual(result.returncode, 0 if payload['state'] == 'verified' else 10)
        return payload, result.stdout

    def test_protocol_two_receipt_and_matching_helper_verify(self):
        self.helper(2)
        self.pin(2)
        installed = self.fixture.install()
        self.assertTrue((installed / 'hollis-native').exists())
        payload, raw = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['verification'], 'verified')
        self.assertEqual(payload['helper']['protocol'], 2)
        self.assertIsInstance(payload['helper']['protocol'], int)
        self.assertIn('"protocol":2', raw)

    def test_protocol_one_receipt_remains_valid(self):
        # An existing 0.4-era receipt keeps working exactly as before: the pin
        # is data, not a hard-coded era.
        self.helper(1)
        self.pin(1)
        self.fixture.install()
        payload, raw = self.record()
        self.assertEqual(payload['state'], 'verified')
        self.assertEqual(payload['helper']['protocol'], 1)
        self.assertIn('"protocol":1', raw)

    def test_corrupt_pins_fail_closed_with_valid_json(self):
        for protocol in ('one', 2.0, 0, 100, -1, '1;rm -rf /', {'a': 1}, True, 10 ** 30,
                         '01', '09', '00', ' 1', '1 ', '+1', '1.0'):
            with self.subTest(protocol=protocol):
                self.helper(1)
                self.pin(protocol)
                payload, raw = self.record()
                self.assertNotEqual(payload['state'], 'verified')
                self.assertNotIn('protocol', payload['helper'])
                self.assertNotIn('"protocol"', raw)

    def test_leading_zero_pin_never_emits_invalid_json(self):
        # A JSON string "01" reaches the producer verbatim through plutil. It
        # must never be interpolated unquoted: `"protocol":01` is invalid JSON
        # and the strict Go decoder would drop the whole record after the
        # install had already reported verified.
        installed = self.fixture.install()
        receipt_path = installed / 'runtime.lock.json'
        receipt = json.loads(receipt_path.read_text())
        for raw_pin in ('01', '09'):
            with self.subTest(pin=raw_pin):
                receipt['native']['protocol_version'] = raw_pin
                receipt_path.write_text(json.dumps(receipt))
                payload, raw = self.record()
                # Fail-closed: not verified, no protocol field, and the one
                # emitted document parses (record() would raise otherwise).
                self.assertNotEqual(payload['state'], 'verified')
                self.assertNotIn('protocol', payload['helper'])
                self.assertNotIn('"protocol":', raw)
                self.assertNotIn('"protocol",', raw)

    def test_valid_protocol_accepts_only_canonical_decimal(self):
        accepted = ('1', '2', '9', '10', '42', '99')
        rejected = ('0', '00', '01', '09', '010', '100', '-1', '+1', '1.0', '2.0',
                    '', 'one', '1;rm -rf /', ' 1', '1 ', '٠١')
        script = ('set -uo pipefail; . "$1"; shift; '
                  'for value in "$@"; do if valid_protocol "$value"; '
                  'then printf \'ok\\n\'; else printf \'no\\n\'; fi; done')
        result = subprocess.run(
            ['/bin/bash', '-c', script, '_', str(COMMON), *accepted, *rejected],
            capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode, 0, result.stderr)
        answers = result.stdout.split()
        self.assertEqual(len(answers), len(accepted) + len(rejected), result.stdout)
        self.assertEqual(answers[:len(accepted)], ['ok'] * len(accepted))
        for raw_pin, answer in zip(rejected, answers[len(accepted):]):
            self.assertEqual(answer, 'no', f'{raw_pin!r} must be rejected')

    def test_install_rejects_corrupt_pin_before_switching_pointer(self):
        self.helper(1)
        for protocol in ('one', 100, {'a': 1}, '1;rm -rf /', '01', '09'):
            with self.subTest(protocol=protocol):
                self.pin(protocol)
                result = self.fixture.call('install')
                self.assertNotEqual(result.returncode, 0, result.stderr)
                self.assertIn('Invalid native helper protocol receipt.', result.stderr)
                self.assertFalse((self.fixture.home / 'current').exists())
                self.assertFalse(list((self.fixture.home / 'versions').glob('.stage.*')))

    def test_pinned_protocol_outside_bounds_refuses_matching_helper(self):
        # Even a helper that agrees with an out-of-range pin cannot be
        # installed: the pin itself is invalid.
        self.helper(100)
        self.pin(100)
        result = self.fixture.call('install')
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertIn('Invalid native helper protocol receipt.', result.stderr)
        self.assertFalse((self.fixture.home / 'current').exists())


if __name__ == '__main__':
    unittest.main()
