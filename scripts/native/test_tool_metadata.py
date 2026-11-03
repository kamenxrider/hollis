"""Native metadata construction and rejection tests, without model access.

The harness links only the schema/metadata modules, never the helper main.
It constructs SDK values but never initializes a model/session or tokenizes.
Real-helper cases below are malformed and must fail before a status event.
"""
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import helper_protocol as protocol

HARNESS = r'''
import Foundation
import FoundationModels

let data = FileHandle.standardInput.readDataToEndOfFile()
do {
    try StrictJSON.scan(data, subject: "fixture")
    let root = try JSONDecoder().decode(JSONValue.self, from: data)
    guard let request = root.fields?["request"] else { exit(1) }
    let prepared = try NativeToolMetadata.prepare(
        request, enabled: root.fields?["enabled"]?.foundationObject as? Bool == true)
    let transcript = Transcript(entries: prepared.entries)
    let encoded = try JSONEncoder().encode(transcript)
    let roundTrip = try JSONDecoder().decode(Transcript.self, from: encoded)
    precondition(transcript == roundTrip)
    var names: [String] = []
    var descriptions: [String] = []
    var schemas: [Any] = []
    var instructionText = ""
    if let first = prepared.entries.first {
        guard case .instructions(let instructions) = first else { exit(2) }
        names = instructions.toolDefinitions.map(\.name)
        descriptions = instructions.toolDefinitions.map(\.description)
        for segment in instructions.segments {
            if case .text(let text) = segment { instructionText += text.content }
        }
        for definition in instructions.toolDefinitions {
            schemas.append(try JSONSerialization.jsonObject(
                with: JSONEncoder().encode(definition.parameters)))
        }
    }
    let pending = NativeToolMetadata.preflightEntries(
        prepared.entries, prompt: "pending-once", options: GenerationOptions())
    var promptCount = 0
    for entry in pending { if case .prompt = entry { promptCount += 1 } }
    let result: [String: Any] = [
        "entries": prepared.entries.count, "input_bytes": prepared.inputBytes,
        "names": names, "descriptions": descriptions, "schemas": schemas,
        "instructions": instructionText, "preflight_entries": pending.count,
        "preflight_prompts": promptCount, "round_trip": true]
    print(String(decoding: try JSONSerialization.data(withJSONObject: result), as: UTF8.self))
} catch NativeToolMetadata.Failure.capacity {
    print(#"{"error":"capacity"}"#)
} catch {
    print(#"{"error":"invalid"}"#)
}
'''


def fixture():
    return {
        'protocol': 3, 'operation': 'complete', 'prompt': 'Weather?',
        'tool_calling': 'allowed', 'tool_instructions': 'Fixed format instructions.',
        'tool_definitions': [{
            'name': 'get_weather', 'description': 'Get weather',
            'parameters': {
                'type': 'object', 'properties': {
                    'city': {'type': 'string', 'description': 'City name'},
                    'unit': {'anyOf': [{'type': 'string', 'enum': ['c', 'f']},
                                       {'type': 'null'}]},
                }, 'required': ['city', 'unit'],
            },
        }],
    }


class MetadataConstructionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest('Apple silicon macOS 27 and SDK 27 required')
        folder = Path(tempfile.mkdtemp(prefix='hollis-metadata-'))
        cls.addClassCleanup(shutil.rmtree, folder, True)
        source = folder / 'main.swift'
        source.write_text(HARNESS)
        cls.binary = folder / 'metadata-harness'
        result = protocol.command(
            'xcrun', '--sdk', 'macosx', 'swiftc', '-target', 'arm64-apple-macosx27.0',
            str(protocol.SUBSET_SOURCE), str(protocol.ROOT / 'native/ToolMetadata.swift'),
            str(source), '-o', str(cls.binary))
        if result.returncode:
            raise RuntimeError(result.stderr)

    def run_fixture(self, request, enabled=True):
        result = protocol.command(str(self.binary),
                                  input=json.dumps({'enabled': enabled, 'request': request}))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, '')
        return json.loads(result.stdout)

    def test_native_definition_order_schema_and_single_pending_prompt(self):
        request = fixture()
        for name, properties in (
                ('add', {'a': {'type': 'integer', 'minimum': 0, 'maximum': 10}}),
                ('nested', {'rows': {'type': 'array', 'maxItems': 2, 'items': {
                    'type': 'object', 'properties': {'label': {'type': 'string'}}}}}),
                ('lookup', {'id': {'type': 'integer', 'minimum': 1}})):
            request['tool_definitions'].append({
                'name': name, 'description': name + ' description',
                'parameters': {'type': 'object', 'properties': properties}})
        result = self.run_fixture(request)
        self.assertEqual(result['names'], ['get_weather', 'add', 'nested', 'lookup'])
        self.assertEqual(result['descriptions'], [d['description'] for d in request['tool_definitions']])
        self.assertEqual(result['instructions'], request['tool_instructions'])
        self.assertEqual(result['entries'], 1)
        self.assertEqual(result['preflight_entries'], 2)
        self.assertEqual(result['preflight_prompts'], 1)
        self.assertTrue(result['round_trip'])
        weather = result['schemas'][0]
        self.assertEqual(weather['required'], ['city'])
        self.assertFalse(weather['additionalProperties'])
        self.assertEqual(weather['properties']['city']['description'], 'City name')
        self.assertEqual(weather['properties']['unit']['enum'], ['c', 'f'])

    def test_plain_and_none_have_no_entries(self):
        for fields in ({}, {'tool_calling': 'disallowed'}):
            request = {'protocol': 3, 'operation': 'complete', 'prompt': 'hello', **fields}
            result = self.run_fixture(request, enabled=False)
            self.assertEqual(result['entries'], 0)
            self.assertEqual(result['names'], [])
            self.assertEqual(result['input_bytes'], 5)

    def test_unknown_envelope_members_keep_protocol_three_ignore_rule(self):
        request = fixture()
        request['future_member'] = {'ignored': True}
        self.assertEqual(self.run_fixture(request)['names'], ['get_weather'])

    def test_status_returns_before_tokenization_and_session_creation(self):
        source = protocol.HELPER_SOURCE.read_text()
        stop = source.index('if request.operation == "status" { return }')
        self.assertLess(stop, source.index('model.tokenCount('))
        self.assertLess(stop, source.index('LanguageModelSession('))
        gate = source.index('if metadataMode && !supportsTools')
        self.assertLess(gate, source.index('model.tokenCount('))
        self.assertNotIn('SystemLanguageModel', HARNESS)
        self.assertNotIn('LanguageModelSession', HARNESS)
        self.assertNotIn('.tokenCount(', HARNESS)

    def test_aggregate_bytes_exact_boundary(self):
        request = fixture()
        baseline = self.run_fixture(request)['input_bytes']
        request['prompt'] += 'x' * (131072 - baseline)
        self.assertEqual(self.run_fixture(request)['input_bytes'], 131072)
        request['prompt'] += 'x'
        self.assertEqual(self.run_fixture(request), {'error': 'capacity'})
        request = fixture()
        request['tool_definitions'][0]['description'] = 'x' * 131072
        self.assertEqual(self.run_fixture(request), {'error': 'capacity'})

    def test_invalid_definitions_never_fall_back(self):
        for name, mutate in invalid_cases():
            with self.subTest(name=name):
                request = fixture()
                mutate(request)
                self.assertEqual(self.run_fixture(request), {'error': 'invalid'})
        self.assertEqual(self.run_fixture(fixture(), enabled=False), {'error': 'invalid'})


def invalid_cases():
    return [
        ('none', lambda r: r.update(tool_calling='disallowed')),
        ('stream', lambda r: r.update(operation='stream')),
        ('status', lambda r: r.update(operation='status')),
        ('protocol 2', lambda r: r.update(protocol=2)),
        ('missing instructions', lambda r: r.pop('tool_instructions')),
        ('null instructions', lambda r: r.update(tool_instructions=None)),
        ('empty instructions', lambda r: r.update(tool_instructions='')),
        ('empty definitions', lambda r: r.update(tool_definitions=[])),
        ('null definitions', lambda r: r.update(tool_definitions=None)),
        ('too many', lambda r: r.update(tool_definitions=r['tool_definitions'] * 33)),
        ('duplicate', lambda r: r.update(tool_definitions=r['tool_definitions'] * 2)),
        ('incompatible schema member', lambda r: r.update(schema={})),
        ('incompatible images member', lambda r: r.update(images=[])),
        ('unknown definition', lambda r: r['tool_definitions'][0].update(invented=True)),
        ('newline name', lambda r: r['tool_definitions'][0].update(name='weather\n')),
        ('long name', lambda r: r['tool_definitions'][0].update(name='x' * 65)),
        ('empty description', lambda r: r['tool_definitions'][0].update(description=' ')),
        ('unsupported schema', lambda r: r['tool_definitions'][0]['parameters'].update(additionalProperties=True)),
        ('null schema', lambda r: r['tool_definitions'][0].update(parameters=None)),
    ]


class MetadataHelperRejectionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.helper = protocol.configured_helper()
        if cls.helper is None:
            if not protocol.can_execute():
                raise unittest.SkipTest('Apple silicon macOS 27 and SDK 27 required')
            folder = Path(tempfile.mkdtemp(prefix='hollis-metadata-helper-'))
            cls.addClassCleanup(shutil.rmtree, folder, True)
            cls.helper = protocol.build_helper(folder)

    def test_invalid_metadata_fails_before_model_status(self):
        version, _ = protocol.swift_pins()
        for name, mutate in invalid_cases():
            with self.subTest(name=name):
                request = fixture()
                mutate(request)
                result = protocol.run(self.helper, ['--native-definitions'], json.dumps(request).encode())
                self.assertEqual(result.returncode, 0)
                self.assertEqual(result.stderr, b'')
                self.assertEqual(protocol.events(result.stdout), [{
                    'event': 'error', 'kind': 'native_failed', 'model': 'local',
                    'protocol': request['protocol'], 'version': version}])

    def test_metadata_without_argv_gate_and_argv_without_metadata_refuse(self):
        for args, request in (
                ([], fixture()),
                (['--native-definitions'], {'protocol': 3, 'operation': 'status'})):
            result = protocol.run(self.helper, args, json.dumps(request).encode())
            events = protocol.events(result.stdout)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, b'')
            self.assertEqual(len(events), 1)
            self.assertEqual(events[0]['kind'], 'native_failed')

    def test_aggregate_capacity_fails_before_model_status(self):
        request = fixture()
        request['tool_definitions'][0]['description'] = 'x' * 131072
        result = protocol.run(self.helper, ['--native-definitions'], json.dumps(request).encode())
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stderr, b'')
        events = protocol.events(result.stdout)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]['kind'], 'context_capacity')
