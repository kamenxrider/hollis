"""Golden tests for the native helper's JSON protocol.

The helper is built with the release script and driven exactly as the Go runner
drives it: one NDJSON request on stdin, an event stream on stdout. Every case
here fails before a generation session exists, or asks only for the
non-generative status event, so no test in this file calls the on-device model.

Cases run where the built helper can execute (Apple silicon, macOS 27). The
release image compiles on a macOS 26 host, so building without executing is
covered by BuildTests, and a build-only host skips the event cases instead of
failing them. Generation behavior lives in test_protocol_live.py, which is
opt-in and never runs in CI.
"""
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import helper_protocol as protocol


def fresh_folder(prefix):
    folder = Path(tempfile.mkdtemp(prefix=prefix))
    return folder


class BuildTests(unittest.TestCase):
    """The checked-in sources compile with the release build script."""

    def test_helper_builds(self):
        if not protocol.can_build():
            self.skipTest('macOS SDK 27 on Apple silicon is required to build')
        folder = fresh_folder('hollis-protocol-build-')
        self.addCleanup(shutil.rmtree, folder, True)
        helper = protocol.build_helper(folder)
        self.assertTrue(helper.is_file())
        self.assertTrue(helper.stat().st_mode & 0o111)


class HelperEventTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest(
                'the built helper requires Apple silicon macOS 27 to execute')
        cls.folder = fresh_folder('hollis-protocol-')
        cls.addClassCleanup(shutil.rmtree, cls.folder, True)
        cls.helper = protocol.build_helper(cls.folder)

    def assert_clean(self, process):
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(process.stderr, b'', process.stderr)

    def test_metadata_commands_do_not_read_a_request(self):
        version = protocol.run(self.helper, ['--version'])
        self.assert_clean(version)
        swift_version, swift_protocol = protocol.swift_pins()
        self.assertEqual(version.stdout.decode().strip(),
                         f'hollis-native {swift_version}')
        reported = protocol.run(self.helper, ['--protocol-version'])
        self.assert_clean(reported)
        self.assertEqual(reported.stdout.decode().strip(), str(swift_protocol))

    def test_golden_request_cases(self):
        cases = protocol.load_goldens()
        self.assertTrue(cases, 'no golden cases found')
        for path, case in cases:
            with self.subTest(case=case.get('name', path.stem)):
                process = protocol.run(self.helper, case.get('args', []),
                                       protocol.golden_stdin(case))
                self.assert_clean(process)
                self.assertEqual(protocol.events(process.stdout),
                                 case['events'], path.name)

    def test_status_event_shape(self):
        version, _ = protocol.swift_pins()
        for number, expected_keys in (
                (2, {'event', 'available', 'reason', 'context_size',
                     'protocol', 'version', 'model'}),
                (1, {'event', 'available', 'reason',
                     'protocol', 'version', 'model'})):
            with self.subTest(protocol=number):
                process = protocol.run(
                    self.helper, stdin=protocol.request_bytes(number, 'status'))
                self.assert_clean(process)
                events = protocol.events(process.stdout)
                self.assertEqual(len(events), 1, process.stdout)
                status = events[0]
                self.assertEqual(set(status), expected_keys)
                self.assertEqual(status['event'], 'status')
                self.assertEqual(status['protocol'], number)
                self.assertEqual(status['version'], version)
                self.assertIsInstance(status['available'], bool)
                self.assertIsInstance(status['reason'], str)
                if number == 2:
                    self.assertIsInstance(status['context_size'], int)
                    self.assertGreater(status['context_size'], 0)

    def test_prompt_guards_run_before_generation(self):
        status = protocol.events(protocol.run(
            self.helper,
            stdin=protocol.request_bytes(2, 'status')).stdout)
        if not status or not status[0]['available']:
            self.skipTest('the local model is not available on this host')
        cases = (
            ('empty prompt', '', 'native_failed'),
            ('whitespace prompt', ' \n\t ', 'native_failed'),
            ('prompt over 128 KiB', 'x' * (128 * 1024 + 1),
             'context_capacity'),
        )
        for name, prompt, kind in cases:
            with self.subTest(case=name):
                process = protocol.run(
                    self.helper,
                    stdin=protocol.request_bytes(2, 'complete', prompt=prompt))
                self.assert_clean(process)
                events = protocol.events(process.stdout)
                self.assertEqual(len(events), 2, process.stdout)
                self.assertEqual(events[0]['event'], 'status')
                self.assertEqual(events[1]['event'], 'error')
                self.assertEqual(events[1]['kind'], kind)


if __name__ == '__main__':
    unittest.main()
