"""Contract tests for the native helper's JSON protocol, runnable anywhere.

These tests read the helper and Go runner sources and compare the two halves of
one contract: version and protocol pins, accepted operations and events, event
field names, error kinds, and the rule that prompt text only ever travels on
stdin. They need no Apple SDK, no model, and no network, so they run on every
CI host and catch a one-sided edit before it ships.

They do not prove the helper behaves as documented; the golden suite in
test_protocol_events.py does that where the built helper can execute.
"""
import os
import re
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import helper_protocol as protocol

# Event fields the Go decoder accepts. Kept here as the reviewed contract; the
# tests below fail if either source grows a field without the other.
GO_REQUEST_FIELDS = {'protocol', 'operation', 'prompt', 'schema', 'tool_calling'}
GO_EVENT_FIELDS = {'protocol', 'version', 'model', 'event', 'available',
                   'reason', 'text', 'usage', 'kind', 'context_size',
                   'structured', 'capability', 'capabilities'}
GO_CAPABILITY_FIELDS = {'images', 'tools', 'vision', 'macos_major'}
GO_USAGE_FIELDS = {'input_tokens', 'output_tokens', 'reasoning_tokens'}
PROTOCOL_TWO_OPERATIONS = {'status', 'complete', 'stream', 'schema'}
PROTOCOL_EVENTS = {'status', 'snapshot', 'complete', 'error'}


def go_struct_tags(source, name):
    match = re.search(r'type ' + name + r' struct \{(.*?)\n\}', source, re.S)
    if not match:
        raise AssertionError(f'Go struct {name} is missing')
    return set(re.findall(r'`json:"([a-z_]+)', match.group(1)))


def go_kind_values():
    source = protocol.ROOT.joinpath('internal/runner/runner.go').read_text()
    return {name: value for name, value in
            re.findall(r'(Kind\w+)\s+Kind\s*=\s*"([a-z_]+)"', source)}


def go_accepted_event_error_kinds():
    source = protocol.GO_NATIVE.read_text()
    section = source.split('case "error":', 1)
    if len(section) != 2:
        raise AssertionError('the native event switch no longer has an error case')
    section = section[1].split('default:', 1)[0]
    names = re.findall(r'Kind\w+', section)
    values = go_kind_values()
    missing = [name for name in names if name not in values]
    if missing:
        raise AssertionError(f'unknown error kind constants: {missing}')
    return {values[name] for name in names}


class ConfiguredHelperTests(unittest.TestCase):
    def test_default_leaves_building_to_the_suite(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertIsNone(protocol.configured_helper())

    def test_configured_helper_requires_execution_support(self):
        with mock.patch.dict(os.environ, {'HOLLIS_NATIVE_HELPER': 'helper'}):
            with mock.patch.object(protocol, 'can_execute', return_value=False):
                with self.assertRaisesRegex(RuntimeError, 'requires Apple silicon'):
                    protocol.configured_helper()

    def test_configured_helper_requires_an_executable_file(self):
        with tempfile.TemporaryDirectory() as folder:
            helper = Path(folder) / 'helper'
            with mock.patch.dict(os.environ, {'HOLLIS_NATIVE_HELPER': str(helper)}):
                with mock.patch.object(protocol, 'can_execute', return_value=True):
                    with self.assertRaisesRegex(RuntimeError, 'built executable'):
                        protocol.configured_helper()
                    helper.write_text('#!/bin/sh\nexit 0\n')
                    with self.assertRaisesRegex(RuntimeError, 'built executable'):
                        protocol.configured_helper()
                    helper.chmod(0o700)
                    self.assertEqual(protocol.configured_helper(), helper.resolve())


class PinTests(unittest.TestCase):
    def test_helper_and_runner_agree_on_version_and_protocol(self):
        swift_version, swift_protocol = protocol.swift_pins()
        go_version, go_protocol = protocol.go_pins()
        self.assertEqual(swift_version, go_version)
        self.assertEqual(swift_protocol, go_protocol)

    def test_docs_state_the_matched_pins(self):
        pins = f'{protocol.swift_pins()[0]}'
        protocol_word = f'protocol {protocol.swift_pins()[1]}'
        for path in (protocol.ROOT / 'native/README.md',
                     protocol.ROOT / 'docs/native-local.md'):
            text = path.read_text()
            with self.subTest(path=str(path)):
                self.assertIn(pins, text)
                self.assertIn(protocol_word, text)


class MessageShapeTests(unittest.TestCase):
    def setUp(self):
        self.swift = protocol.HELPER_SOURCE.read_text()
        self.go = protocol.GO_NATIVE.read_text()

    def test_request_fields_match_both_decoders(self):
        self.assertEqual(go_struct_tags(self.go, 'nativeRequest'), GO_REQUEST_FIELDS)
        match = re.search(r'struct Request: Decodable \{(.*?)\n    \}', self.swift, re.S)
        self.assertIsNotNone(match, 'helper Request struct is missing')
        swift_fields = set(re.findall(r'let `?([A-Za-z_]+)`?:', match.group(1)))
        coding_keys = re.search(r'enum CodingKeys: String, CodingKey \{(.*?)\n        \}',
                                match.group(1), re.S)
        self.assertIsNotNone(coding_keys, 'helper request CodingKeys are missing')
        swift_keys = {}
        for case in re.findall(r'case ([^\n]+)', coding_keys.group(1)):
            for member in case.split(','):
                parts = member.strip().split(' = ')
                swift_keys[parts[0].strip('`')] = parts[-1].strip('`"')
        self.assertEqual(set(swift_keys), swift_fields)
        self.assertEqual(set(swift_keys.values()), GO_REQUEST_FIELDS)

    def test_helper_emits_only_fields_the_runner_decodes(self):
        emitted = set(re.findall(r'"([a-z_]+)":', self.swift))
        emitted.update(re.findall(r'\b(?:event|fields)\["([a-z_]+)"\] =',
                                  self.swift))
        # The helper also builds status, usage and capability dictionaries;
        # each is covered by the corresponding Go decoder's tags.
        self.assertEqual(go_struct_tags(self.go, 'nativeEvent'), GO_EVENT_FIELDS)
        self.assertEqual(go_struct_tags(self.go, 'nativeUsage'), GO_USAGE_FIELDS)
        self.assertEqual(go_struct_tags(self.go, 'Capabilities'),
                         GO_CAPABILITY_FIELDS)
        self.assertLessEqual(emitted, GO_EVENT_FIELDS | GO_USAGE_FIELDS |
                             GO_CAPABILITY_FIELDS)

    def test_helper_emits_only_events_the_runner_handles(self):
        self.assertEqual(set(protocol.SWIFT_EVENT.findall(self.swift)),
                         PROTOCOL_EVENTS)
        for event in PROTOCOL_EVENTS:
            self.assertIn(f'case "{event}":', self.go)

    def test_helper_failure_kinds_are_accepted_by_the_runner(self):
        emitted = set(protocol.SWIFT_FAILURE.findall(self.swift))
        emitted.update(re.findall(r'"kind": "([a-z_]+)"', self.swift))
        self.assertTrue(emitted, 'helper emits no failure kinds')
        self.assertLessEqual(emitted, go_accepted_event_error_kinds())

    def test_helper_accepts_exactly_protocols_one_two_and_three(self):
        match = re.search(r'let accepted = \[([^\]]+)\]', self.swift)
        self.assertIsNotNone(match, 'helper accepted protocol set is missing')
        accepted = [protocol.swift_pins()[1] if value.strip() == 'protocolVersion'
                    else int(value.strip()) for value in match.group(1).split(',')]
        self.assertEqual(sorted(accepted), [1, 2, 3])
        self.assertIn('guard accepted.contains(request.`protocol`) else {',
                      self.swift)

    def test_operations_are_pinned_on_both_sides(self):
        match = re.search(r'var operations = \[(.*?)\](.*?)guard operations.contains',
                          self.swift, re.S)
        self.assertIsNotNone(match, 'helper operation guard is missing')
        swift_operations = set(re.findall(r'"([a-z_]+)"', match.group(1)))
        swift_operations.update(re.findall(r'operations.append\("([a-z_]+)"\)',
                                           match.group(2)))
        go_operations = set(re.findall(
            r'r\.invoke\(ctx,\s*(?:model|ModelLocal),\s*"([a-z_]+)"', self.go))
        self.assertEqual(swift_operations, PROTOCOL_TWO_OPERATIONS)
        self.assertEqual(go_operations, PROTOCOL_TWO_OPERATIONS)
        self.assertEqual(swift_operations, go_operations)

    def test_runner_rejects_unknown_event_fields(self):
        self.assertIn('decoder.DisallowUnknownFields()', self.go)


class PrivacyOfTheChannelTests(unittest.TestCase):
    """Prompt text must travel on stdin and errors must stay content-free."""

    def setUp(self):
        self.swift = protocol.HELPER_SOURCE.read_text()

    def test_prompt_never_travels_in_process_arguments(self):
        # Two metadata comparisons (two references each) and one guard: no
        # other argument inspection exists, so a request cannot arrive that way.
        occurrences = self.swift.count('CommandLine.arguments')
        self.assertEqual(occurrences, 5, 'unexpected argument handling')
        self.assertNotIn('CommandLine.arguments[1]', self.swift)
        self.assertIn('FileHandle.standardInput.read(upToCount:', self.swift)

    def test_helper_writes_no_diagnostics(self):
        self.assertNotIn('standardError', self.swift)
        self.assertNotIn('FileHandle.standardError', self.swift)

    def test_helper_prints_only_metadata(self):
        prints = [line.strip() for line in self.swift.splitlines()
                  if re.search(r'\bprint\(', line)]
        self.assertEqual(len(prints), 2, prints)
        self.assertIn('hollis-native', prints[0])
        self.assertEqual(prints[1], 'print(protocolVersion)')

    def test_error_events_carry_no_free_text(self):
        # fail() takes a kind only; an error description could contain input.
        match = re.search(r'static func fail\((.*?)\n    \}', self.swift, re.S)
        self.assertIsNotNone(match)
        self.assertNotIn('error)', match.group(1))
        self.assertIn('"event": "error", "kind": kind', self.swift)


if __name__ == '__main__':
    unittest.main()
