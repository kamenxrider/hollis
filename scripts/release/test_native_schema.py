"""Model-free tests for the native JSON Schema subset and helper protocol.

The shipped parser (native/SchemaSubset.swift) is compiled together with a
test-only harness that never creates a session or touches a model. Acceptance
cases assemble a real GenerationSchema; rejection cases exercise the pre-call
subset rules from the design. The real helper is also built and its metadata
commands are checked.

Nothing here calls Apple services, initializes a model, or generates text.
"""
import json
import platform
import subprocess
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SUBSET = ROOT / 'native/SchemaSubset.swift'
HELPER = ROOT / 'native/HollisNative.swift'
BUILD = ROOT / 'scripts/build-native.sh'

HARNESS = '''import Foundation

struct Harness: Decodable {
    let mode: String
    let schema: JSONValue?
    let value: JSONValue?
}

let input = FileHandle.standardInput.readDataToEndOfFile()
let request: Harness
do { request = try JSONDecoder().decode(Harness.self, from: input) }
catch { print("fail decode"); exit(0) }
do {
    switch request.mode {
    case "parse", "assemble":
        guard let schema = request.schema else { print("fail missing schema"); exit(0) }
        let node = try SchemaSubset.parse(schema)
        if request.mode == "assemble" { _ = try SchemaSubset.schema(for: node) }
        print("ok")
    case "validate":
        guard let schema = request.schema, let value = request.value else {
            print("fail missing"); exit(0)
        }
        let node = try SchemaSubset.parse(schema)
        if let bad = SchemaSubset.validate(value, against: node) {
            print("mismatch \\(bad)")
        } else { print("ok") }
    default:
        print("fail unknown mode")
    }
} catch let failure as SchemaSubset.Failure {
    print("fail \\(failure.pointer) \\(failure.reason)")
} catch {
    print("fail \\(error)")
}
'''


def command(*args, **kwargs):
    return subprocess.run(args, capture_output=True, text=True, timeout=kwargs.pop('timeout', 600), **kwargs)


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


def can_execute_target():
    return platform.system() == 'Darwin' and platform.machine() == 'arm64' and (host_major() or 0) >= 27


class HarnessMixin:
    @classmethod
    def build(cls):
        sdk = sdk_version()
        if platform.system() != 'Darwin' or platform.machine() != 'arm64':
            raise unittest.SkipTest('Apple silicon macOS is required for the native subset harness')
        if sdk is None or int(sdk.split('.')[0]) < 27:
            raise unittest.SkipTest(f'macOS SDK 27 is required for the native subset harness (found {sdk})')
        if not can_execute_target():
            raise unittest.SkipTest('the built harness requires macOS 27 to execute')
        folder = Path(tempfile.mkdtemp(prefix='hollis-schema-'))
        cls.addClassCleanup(lambda: __import__('shutil').rmtree(folder, ignore_errors=True))
        (folder / 'main.swift').write_text(HARNESS)
        binary = folder / 'harness'
        sdk_path = command('xcrun', '--sdk', 'macosx', '--show-sdk-path').stdout.strip()
        result = command(
            'xcrun', 'swiftc', '-sdk', sdk_path, '-target', 'arm64-apple-macosx27.0',
            str(SUBSET), str(folder / 'main.swift'), '-o', str(binary))
        if result.returncode != 0:
            raise RuntimeError(f'harness build failed: {result.stderr}')
        cls.binary = binary

    def run_mode(self, mode, schema, value=None):
        payload = {'mode': mode, 'schema': schema}
        if value is not None:
            payload['value'] = value
        result = command(str(self.binary), input=json.dumps(payload))
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def accept(self, schema):
        output = self.run_mode('assemble', schema)
        self.assertEqual(output, 'ok')

    def reject(self, schema, needle):
        output = self.run_mode('assemble', schema)
        self.assertTrue(output.startswith('fail'), output)
        self.assertIn(needle, output)


def root(properties=None, required=None):
    schema = {'type': 'object', 'additionalProperties': False}
    if properties is not None:
        schema['properties'] = properties
    if required is not None:
        schema['required'] = required
    return schema


class SchemaSubsetTests(HarnessMixin, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build()

    def test_primitive_fields_required_and_numeric_bounds_assemble(self):
        self.accept(root({'name': {'type': 'string'}, 'count': {'type': 'integer', 'minimum': 0, 'maximum': 10},
                          'ratio': {'type': 'number', 'minimum': 0, 'maximum': 1},
                          'enabled': {'type': 'boolean'}}, required=['name', 'count']))

    def test_string_enum_assembles_and_integer_or_other_enums_do_not(self):
        self.accept(root({'kind': {'type': 'string', 'enum': ['alpha', 'beta']}}))
        self.reject(root({'kind': {'type': 'integer', 'enum': [1, 2]}}), 'enums must be strings')
        self.reject(root({'kind': {'type': 'string', 'enum': [1, 2]}}), 'enum choices must be strings')
        self.reject(root({'kind': {'type': 'string', 'enum': []}}), 'enum must not be empty')
        self.reject(root({'kind': {'type': 'string', 'enum': ['a', 'a']}}), 'enum choices must be unique')

    def test_arrays_with_items_and_bounds_assemble(self):
        self.accept(root({'tags': {'type': 'array', 'items': {'type': 'string'}, 'minItems': 1, 'maxItems': 5}}))
        self.accept(root({'rows': {'type': 'array', 'items': {
            'type': 'object', 'properties': {'cell': {'type': 'string'}}, 'additionalProperties': False}}}))
        self.reject(root({'tags': {'type': 'array', 'minItems': 1}}), 'array items are required')
        self.reject(root({'tags': {'type': 'array', 'items': {'type': 'string'},
                                   'minItems': 3, 'maxItems': 2}}), 'smaller than minItems')
        self.reject(root({'tags': {'type': 'array', 'items': {'type': 'string'},
                                   'maxItems': 101}}), 'whole numbers from 0 to 100')

    def test_nested_objects_to_depth_three_assemble(self):
        leaf = root({'leaf': {'type': 'string'}})
        self.accept(root({'child': root({'grand': leaf})}))
        self.reject(root({'a': root({'b': root({'c': leaf})})}),
                    'object nesting exceeds depth 3')

    def test_unsupported_keywords_and_roots_are_rejected(self):
        self.reject({'type': 'array', 'items': {'type': 'string'}, 'additionalProperties': False},
                    'root schema must be an object')
        self.reject('not a schema', 'root schema must be an object')
        self.reject(root({'value': 'not a schema'}), 'schema node must be an object')
        self.reject(root({'a': {'type': 'object', 'properties': {}, 'additionalProperties': True}}),
                    'additionalProperties:false is required')
        self.reject({'type': 'object', 'properties': {}}, 'additionalProperties:false is required')
        for keyword, node in (
                ('$ref', {'type': 'string', '$ref': '#/definitions/x'}),
                ('anyOf', {'type': 'string', 'anyOf': [{'type': 'string'}]}),
                ('pattern', {'type': 'string', 'pattern': '^a'}),
                ('format', {'type': 'string', 'format': 'uuid'}),
                ('default', {'type': 'string', 'default': 'x'}),
                ('minLength', {'type': 'string', 'minLength': 1})):
            with self.subTest(keyword=keyword):
                self.reject(root({'value': node}), 'unsupported schema keyword')

    def test_contradictions_and_declaration_errors_are_rejected(self):
        self.reject(root({'n': {'type': 'integer', 'minimum': 5, 'maximum': 1}}),
                    'maximum is smaller than minimum')
        self.reject(root({'d': {'type': 'number', 'minimum': 1.5, 'maximum': 1.0}}),
                    'maximum is smaller than minimum')
        self.reject(root({'n': {'type': 'integer', 'minimum': 1.5}}), 'integer bounds must be whole numbers')
        self.reject(root({'name': {'type': 'string'}}, required=['absent']),
                    'required names a property that is not declared')
        self.reject(root({'name': {'type': 'string'}}, required=['name', 'name']),
                    'required names must be unique')
        self.reject(root({'name': {'type': 'string'}}, required='name'),
                    'required must be an array of names')
        self.reject(root({'n': {'type': 'some-unknown-type'}}), 'unsupported schema type')

    def test_property_count_and_encoded_size_are_bounded(self):
        many = {f'f{index}': {'type': 'string'} for index in range(33)}
        self.reject(root(many), 'more than 32 properties')
        self.accept(root({f'f{index}': {'type': 'string'} for index in range(32)}))
        huge_enum = ['v' + str(index) + 'x' * 40 for index in range(2500)]
        self.reject(root({'kind': {'type': 'string', 'enum': huge_enum}}), 'larger than 64 KiB')

    def test_returned_payload_validation_accepts_conforming_objects(self):
        schema = root({'name': {'type': 'string'}, 'count': {'type': 'integer', 'minimum': 0, 'maximum': 10},
                       'kind': {'type': 'string', 'enum': ['alpha', 'beta']},
                       'tags': {'type': 'array', 'items': {'type': 'string'}, 'minItems': 1, 'maxItems': 3}},
                      required=['name', 'count'])
        self.assertEqual(self.run_mode('validate', schema, {
            'name': 'fixture', 'count': 3, 'kind': 'beta', 'tags': ['a', 'b']}), 'ok')
        self.assertEqual(self.run_mode('validate', schema, {'name': 'fixture', 'count': 0}), 'ok')

    def test_returned_payload_validation_rejects_every_mismatch(self):
        schema = root({'name': {'type': 'string'}, 'count': {'type': 'integer', 'minimum': 0, 'maximum': 10},
                       'kind': {'type': 'string', 'enum': ['alpha', 'beta']},
                       'tags': {'type': 'array', 'items': {'type': 'string'}, 'minItems': 1, 'maxItems': 3}},
                      required=['name', 'count'])
        for value in (
                {'count': 1},                                  # missing required
                {'name': 'x', 'count': 'one'},                 # wrong type
                {'name': 'x', 'count': 1.5},                   # integer vs number
                {'name': 'x', 'count': 11},                    # numeric maximum
                {'name': 'x', 'count': -1},                    # numeric minimum
                {'name': 'x', 'count': 1, 'kind': 'gamma'},    # enum membership
                {'name': 'x', 'count': 1, 'tags': []},         # array minimum
                {'name': 'x', 'count': 1, 'tags': ['a', 'b', 'c', 'd']},  # array maximum
                {'name': 'x', 'count': 1, 'tags': [1]},        # array item type
                {'name': 'x', 'count': 1, 'extra': True}):     # additional property
            with self.subTest(value=value):
                self.assertTrue(self.run_mode('validate', schema, value).startswith('mismatch'))


class HelperProtocolTests(unittest.TestCase):
    def test_source_pins_protocol_two_and_helper_version(self):
        source = HELPER.read_text()
        self.assertIn('static let protocolVersion = 2', source)
        self.assertIn('static let version = "0.5.0"', source)
        self.assertIn('request.operation == "schema"', source)

    def test_built_helper_metadata_and_model_free_rejections(self):
        if platform.system() != 'Darwin' or platform.machine() != 'arm64':
            raise unittest.SkipTest('the native helper requires Apple silicon macOS')
        sdk = sdk_version()
        if sdk is None or int(sdk.split('.')[0]) < 27:
            raise unittest.SkipTest(f'macOS SDK 27 is required to build the native helper (found {sdk})')
        with tempfile.TemporaryDirectory(prefix='hollis-native-') as folder:
            helper = Path(folder) / 'hollis-native'
            result = command('bash', str(BUILD), str(helper))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(command(str(helper), '--version').stdout.strip(), 'hollis-native 0.5.0')
            self.assertEqual(command(str(helper), '--protocol-version').stdout.strip(), '2')
            if not can_execute_target():
                raise unittest.SkipTest('the built helper requires macOS 27 to execute')
            # An unsupported schema is rejected before any model access, so the
            # error event is the only output and no status event appears.
            for request in (
                    {'protocol': 2, 'operation': 'schema', 'prompt': 'fixture',
                     'schema': {'type': 'array', 'items': {'type': 'string'}}},
                    {'protocol': 2, 'operation': 'schema', 'prompt': 'fixture',
                     'schema': root({'kind': {'type': 'integer', 'enum': [1]}})},
                    {'protocol': 3, 'operation': 'status'},
                    {'protocol': 2, 'operation': 'stream', 'prompt': 'fixture',
                     'schema': root({'name': {'type': 'string'}})},
                    {'protocol': 1, 'operation': 'complete', 'prompt': 'fixture',
                     'schema': root({'name': {'type': 'string'}})}):
                with self.subTest(request=request):
                    result = command(str(helper), input=json.dumps(request))
                    self.assertEqual(result.returncode, 0, result.stderr)
                    events = [json.loads(line) for line in result.stdout.splitlines() if line]
                    self.assertEqual(len(events), 1, result.stdout)
                    self.assertEqual(events[0]['event'], 'error')
                    self.assertEqual(events[0]['kind'], 'native_failed')


if __name__ == '__main__':
    unittest.main()
