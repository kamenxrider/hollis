"""Native handoff contract tests. No model or session is initialized."""
import copy
import json
import pathlib
import shutil
import tempfile
import unittest

import helper_protocol as protocol


class NativeToolsContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not protocol.can_execute():
            raise unittest.SkipTest("Apple silicon macOS 27 and SDK 27 required")
        folder = pathlib.Path(tempfile.mkdtemp(prefix="hollis-native-tools-", dir="/tmp"))
        cls.addClassCleanup(shutil.rmtree, folder)
        source = folder / "main.swift"
        source.write_text("""
import Foundation
import FoundationModels
do {
    _ = try NativeTools.prepare(FileHandle.standardInput.readDataToEndOfFile())
    print("valid")
} catch {
    print("invalid")
}
""")
        cls.binary = folder / "contract"
        result = protocol.command(
            "xcrun", "swiftc", "-target", "arm64-apple-macosx27.0",
            str(protocol.SUBSET_SOURCE), str(protocol.ROOT / "native/NativeTools.swift"),
            str(source), "-o", str(cls.binary))
        if result.returncode:
            raise RuntimeError(result.stderr)

    def test_shared_contract(self):
        corpus = json.loads((protocol.ROOT / "scripts/native/testdata/native-tools-contract.json").read_text())
        for case in corpus["cases"]:
            with self.subTest(case=case["name"]):
                request = copy.deepcopy(corpus["base"])
                for change in case["changes"]:
                    node = request
                    for key in change[:-2]:
                        node = node[key]
                    key, value = change[-2:]
                    if isinstance(node, list) and key == len(node):
                        node.append(value)
                    else:
                        node[key] = value
                result = protocol.command(str(self.binary), input=json.dumps(request))
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), "valid" if case["valid"] else "invalid")
