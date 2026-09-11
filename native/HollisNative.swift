// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels

// One request per process. No tools, persistent session, instructions, truncation,
// implicit fallback, or diagnostic reflection of the request contents.
@main
struct HollisNative {
    static let version = "0.4.0"
    static let protocolVersion = 1

    struct Request: Decodable {
        let `protocol`: Int
        let operation: String
        let prompt: String?
    }

    static func emit(_ fields: [String: Any]) throws {
        var event = fields
        event["protocol"] = protocolVersion
        event["version"] = version
        event["model"] = "local"
        let data = try JSONSerialization.data(withJSONObject: event, options: [.sortedKeys])
        try FileHandle.standardOutput.write(contentsOf: data + Data([10]))
    }

    static func fail(_ kind: String) {
        try? emit(["event": "error", "kind": kind])
    }

    @MainActor
    static func main() async {
        // These metadata commands do not initialise a model or session.
        if CommandLine.arguments == [CommandLine.arguments[0], "--version"] {
            print("hollis-native \(version)")
            return
        }
        if CommandLine.arguments == [CommandLine.arguments[0], "--protocol-version"] {
            print(protocolVersion)
            return
        }
        guard CommandLine.arguments.count == 1 else { fail("native_failed"); return }
        do {
            // The maximum encoded request allows worst-case JSON escaping of
            // the 128 KiB prompt. The Go parent closes stdin after one request.
            let data = try FileHandle.standardInput.read(upToCount: 1_048_577) ?? Data()
            guard data.count <= 1_048_576 else { fail("context_capacity"); return }
            // A pipe read may be short; consume remaining bytes within the bound.
            var input = data
            while let rest = try FileHandle.standardInput.read(upToCount: 1_048_577 - input.count), !rest.isEmpty {
                input.append(rest)
                if input.count > 1_048_576 { fail("context_capacity"); return }
            }
            let request = try JSONDecoder().decode(Request.self, from: input)
            guard request.protocol == protocolVersion,
                  ["status", "complete", "stream"].contains(request.operation) else {
                fail("native_failed"); return
            }
            let model = SystemLanguageModel.default
            try emit(["event": "status", "available": model.isAvailable,
                      "reason": String(describing: model.availability)])
            if request.operation == "status" { return }
            guard model.isAvailable else { return }
            guard let prompt = request.prompt,
                  !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                fail("native_failed"); return
            }
            guard prompt.utf8.count <= 131_072 else { fail("context_capacity"); return }
            // This is tokenisation, not generation. It rejects known excess;
            // SDK context errors still cover its own transcript overhead.
            guard try await model.tokenCount(for: prompt) < model.contextSize else {
                fail("context_capacity"); return
            }
            let session = LanguageModelSession(model: model)
            if request.operation == "complete" {
                let response = try await session.respond(to: prompt)
                try emit(["event": "complete", "text": response.content,
                          "usage": ["input_tokens": response.usage.input.totalTokenCount,
                                    "output_tokens": response.usage.output.totalTokenCount,
                                    "reasoning_tokens": response.usage.output.reasoningTokenCount]])
            } else {
                var last = ""
                for try await snapshot in session.streamResponse(to: prompt) {
                    last = snapshot.content
                    try emit(["event": "snapshot", "text": last])
                }
                // Streaming usage has not been qualified; omit it.
                try emit(["event": "complete", "text": last])
            }
        } catch let error as LanguageModelError {
            switch error {
            case .contextSizeExceeded: fail("context_capacity")
            case .rateLimited: fail("rate_limited")
            case .refusal, .guardrailViolation: fail("request_declined")
            default: fail("native_failed")
            }
        } catch {
            // Never print error descriptions: SDK errors may contain input.
            fail("native_failed")
        }
    }
}
