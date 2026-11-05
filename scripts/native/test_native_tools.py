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
    let data = FileHandle.standardInput.readDataToEndOfFile()
    if String(decoding: data, as: UTF8.self) == "capture" {
        let prompt = Transcript.Entry.prompt(Transcript.Prompt(
            segments: [NativeTools.segment("Weather?")]))
        let call = Transcript.ToolCall(id: "native:id/1", toolName: "weather",
            arguments: try GeneratedContent(json: "{\\"city\\":\\"Zürich\\"}"))
        let batch = Transcript.Entry.toolCalls(Transcript.ToolCalls([call]))
        let output = Transcript.Entry.toolOutput(Transcript.ToolOutput(
            id: call.id, toolName: call.toolName, segments: [NativeTools.segment("")]))
        let response = Transcript.Entry.response(Transcript.Response(
            segments: [NativeTools.segment("unfinished")]))
        let accepted = try NativeTools.capture([prompt, batch], allowedNames: ["weather"])
        precondition(accepted.count == 1 && accepted[0].id == call.id)
        let rejected: [[Transcript.Entry]] = [
            [], [prompt], [batch], [prompt, batch, batch],
            [prompt, batch, output], [prompt, response, batch],
            [prompt, .toolCalls(Transcript.ToolCalls([call, call]))],
            [prompt, .toolCalls(Transcript.ToolCalls([]))],
        ]
        for entries in rejected {
            do {
                _ = try NativeTools.capture(entries, allowedNames: ["weather"])
                fatalError("partial batch accepted")
            } catch {}
        }
        do {
            _ = try NativeTools.capture([prompt, batch], allowedNames: [])
            fatalError("unknown tool accepted")
        } catch {}
        print("capture-ok")
    } else if String(decoding: data, as: UTF8.self) == "errors" {
        let busy = NSError(domain: "ModelManagerError", code: 1012,
            userInfo: [NSLocalizedDescriptionKey: "PRIVATE SDK DESCRIPTION"])
        let wrapped = NSError(domain: "Wrapper", code: 1,
            userInfo: [NSUnderlyingErrorKey: busy])
        let context = LanguageModelSession.GenerationError.Context(debugDescription: "PRIVATE")
        let cases: [(Error, String)] = [
            (busy, "model_busy"), (wrapped, "model_busy"),
            (NSError(domain: "ModelManagerError", code: 1000), "native_failed"),
            (NSError(domain: "Other", code: 1012), "native_failed"),
            (LanguageModelSession.GenerationError.guardrailViolation(context), "request_declined"),
            (LanguageModelSession.GenerationError.exceededContextWindowSize(context), "context_capacity"),
            (LanguageModelSession.GenerationError.decodingFailure(context), "native_failed"),
        ]
        for (error, expected) in cases {
            let event = NativeTools.failureEvent(error, callbackCaptured: false)
            precondition(event["kind"] as? String == expected)
            let encoded = try JSONSerialization.data(withJSONObject: event)
            precondition(!String(decoding: encoded, as: UTF8.self).contains("PRIVATE"))
        }
        let event = NativeTools.failureEvent(busy, callbackCaptured: true)
        let info = event["error_info"] as! [String: Any]
        precondition(info["callback_captured"] as? Bool == true)
        precondition(info["domain"] as? String == "ModelManagerError")
        precondition(info["code"] as? Int == 1012)
        print("errors-ok")
    } else {
        _ = try NativeTools.prepare(data)
        print("valid")
    }
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

    def test_complete_batch_only(self):
        result = protocol.command(str(self.binary), input="capture")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "capture-ok")

    def test_error_classification_is_content_free(self):
        result = protocol.command(str(self.binary), input="errors")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "errors-ok")
