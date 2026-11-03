"""Opt-in tests for generation events, the only part that needs the model.

Nothing here runs in CI. The tests are skipped unless HOLLIS_NATIVE_LIVE is
"1", and each one makes exactly one on-device generation call with a synthetic
prompt, so a run of this file makes three calls in total. Apple's local model is
shared machine-wide: the operator must hold the machine-wide serialization lock
and keep the spacing and per-minute budget that apply to every local call
before setting the variable. The file deliberately does not retry, warm up, or
fall back.

What is asserted is protocol behavior, not answer quality: a complete event
with measured usage and non-empty text, append-only stream snapshots matching
their final text and carrying no usage, and a guided object that satisfies the
requested schema. The shared fixture answers are deliberately trivial so a
guardrail refusal is easy to recognize; a refusal is reported as a coded
`request_declined` event and fails these tests rather than being retried.
"""
import json
import os
import time
import unittest

import helper_protocol as protocol

LIVE = os.environ.get('HOLLIS_NATIVE_LIVE') == '1'
MINIMUM_SPACING_SECONDS = 3.0


def schema():
    return {'type': 'object', 'additionalProperties': False,
            'properties': {'city': {'type': 'string'}},
            'required': ['city']}


def event_stream(process):
    if process.returncode != 0:
        raise AssertionError(f'helper exited {process.returncode}')
    return protocol.events(process.stdout)


@unittest.skipUnless(LIVE, 'set HOLLIS_NATIVE_LIVE=1 to make model calls')
class LiveGenerationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest('execution requires Apple silicon macOS 27')
        import shutil
        import tempfile
        cls.folder = tempfile.mkdtemp(prefix='hollis-protocol-live-')
        cls.addClassCleanup(shutil.rmtree, cls.folder, True)
        cls.helper = protocol.build_helper(cls.folder)
        cls.last_call = 0.0

    @classmethod
    def call(cls, payload):
        wait = MINIMUM_SPACING_SECONDS - (time.monotonic() - cls.last_call)
        if wait > 0:
            time.sleep(wait)
        cls.last_call = time.monotonic()
        return event_stream(protocol.run(cls.helper, stdin=payload))

    def test_complete_reports_text_and_measured_usage(self):
        events = self.call(protocol.request_bytes(
            2, 'complete', prompt='Reply with the single word: ready.'))
        self.assertEqual([event['event'] for event in events],
                         ['status', 'complete'])
        self.assertTrue(events[1]['text'].strip())
        usage = events[1]['usage']
        self.assertGreaterEqual(usage['input_tokens'], 0)
        self.assertGreaterEqual(usage['output_tokens'], 0)
        self.assertLessEqual(usage['reasoning_tokens'], usage['output_tokens'])

    def test_stream_snapshots_are_append_only_and_unpriced(self):
        events = self.call(protocol.request_bytes(
            2, 'stream', prompt='Count from one to three.'))
        self.assertEqual(events[0]['event'], 'status')
        snapshots = [event['text'] for event in events
                     if event['event'] == 'snapshot']
        final = events[-1]
        self.assertEqual(final['event'], 'complete')
        self.assertNotIn('usage', final)
        for earlier, later in zip(snapshots, snapshots[1:]):
            self.assertTrue(later.startswith(earlier))
        if snapshots:
            self.assertEqual(final['text'], snapshots[-1])

    def test_schema_returns_an_object_string_and_usage(self):
        events = self.call(protocol.request_bytes(
            2, 'schema', prompt='Name one city.', schema=schema()))
        self.assertEqual([event['event'] for event in events],
                         ['status', 'complete'])
        final = events[1]
        self.assertEqual(set(final['structured']), {'city'})
        self.assertIsInstance(final['structured']['city'], str)
        self.assertEqual(json.loads(final['text']), final['structured'])
        self.assertIn('usage', final)


if __name__ == '__main__':
    unittest.main()
