// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels

// One request per process. No tools, persistent session, instructions, truncation,
// implicit fallback, or diagnostic reflection of the request contents.
@main
struct HollisNative {
    static let version = "0.5.0"
    static let protocolVersion = 2

    struct Request: Decodable {
        let `protocol`: Int
        let operation: String
        let prompt: String?
        let schema: JSONValue?
    }

    static func emit(_ fields: [String: Any], `protocol` effective: Int) throws {
        var event = fields
        event["protocol"] = effective
        event["version"] = version
        event["model"] = "local"
        let data = try JSONSerialization.data(withJSONObject: event, options: [.sortedKeys])
        try FileHandle.standardOutput.write(contentsOf: data + Data([10]))
    }

    static func fail(_ kind: String, `protocol` effective: Int = protocolVersion) {
        try? emit(["event": "error", "kind": kind], protocol: effective)
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
        var effective = protocolVersion
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
            // Foundation's decoder accepts a leading byte-order mark, trailing
            // commas and repeated object keys (keeping the first value). Go
            // rejects all three, and the embedded schema must not be
            // reinterpreted more leniently here than it was measured there.
            // The request envelope is scanned strictly; the schema member is
            // scanned again as a schema so its own rejections match Go's.
            do {
                if let raw = try StrictJSON.schemaMember(of: input) {
                    try StrictJSON.scan(raw, subject: "schema")
                }
            } catch { fail("native_failed"); return }
            var request: Request
            do { request = try JSONDecoder().decode(Request.self, from: input) }
            catch { fail("native_failed"); return }
            // A protocol-2 helper also answers protocol-1 requests so a
            // half-switched managed install fails closed on identity, not on a
            // misinterpreted request. Events are stamped with the request's
            // protocol; schema never reaches a protocol-1 request.
            guard request.`protocol` == 1 || request.`protocol` == protocolVersion else {
                fail("native_failed"); return
            }
            effective = request.`protocol`
            var operations = ["status", "complete", "stream"]
            if effective >= 2 { operations.append("schema") }
            guard operations.contains(request.operation) else {
                fail("native_failed", protocol: effective); return
            }
            if request.operation == "schema", request.schema == nil {
                fail("native_failed", protocol: effective); return
            }
            if request.operation != "schema", request.schema != nil {
                fail("native_failed", protocol: effective); return
            }
            // The subset is parsed and validated before any model access:
            // an unsupported schema must never reach generation.
            var requested: SchemaSubset.Node?
            if let raw = request.schema {
                do { requested = try SchemaSubset.parse(raw) }
                catch { fail("native_failed", protocol: effective); return }
            }
            let model = SystemLanguageModel.default
            var status: [String: Any] = ["event": "status", "available": model.isAvailable,
                                         "reason": String(describing: model.availability)]
            if effective >= 2 {
                // Measured token budget, exposed only on protocol 2 so
                // protocol-1 events stay byte-compatible with old decoders.
                status["context_size"] = model.contextSize
            }
            try emit(status, protocol: effective)
            if request.operation == "status" { return }
            guard model.isAvailable else { return }
            guard let prompt = request.prompt,
                  !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                fail("native_failed", protocol: effective); return
            }
            guard prompt.utf8.count <= 131_072 else {
                fail("context_capacity", protocol: effective); return
            }
            // This is tokenisation, not generation. It rejects known excess;
            // SDK context errors still cover its own transcript overhead.
            guard try await model.tokenCount(for: prompt) < model.contextSize else {
                fail("context_capacity", protocol: effective); return
            }
            let session = LanguageModelSession(model: model)
            if request.operation == "schema" {
                let response = try await session.respond(
                    to: prompt, schema: try SchemaSubset.schema(for: requested!))
                // The SDK decode is necessary but not trusted: validate the
                // emitted object against the requested subset before emitting.
                // The raw text is scanned first, so a repeated key or a
                // malformed document is refused rather than resolved by
                // Foundation's lenient decoder.
                let encoded = response.content.jsonString
                guard let payload = try? SchemaSubset.decodePayload(Data(encoded.utf8)),
                      SchemaSubset.validate(payload, against: requested!) == nil else {
                    fail("native_failed", protocol: effective); return
                }
                try emit(["event": "complete", "text": encoded,
                          "structured": payload.foundationObject,
                          "usage": ["input_tokens": response.usage.input.totalTokenCount,
                                    "output_tokens": response.usage.output.totalTokenCount,
                                    "reasoning_tokens": response.usage.output.reasoningTokenCount]],
                         protocol: effective)
            } else if request.operation == "complete" {
                let response = try await session.respond(to: prompt)
                try emit(["event": "complete", "text": response.content,
                          "usage": ["input_tokens": response.usage.input.totalTokenCount,
                                    "output_tokens": response.usage.output.totalTokenCount,
                                    "reasoning_tokens": response.usage.output.reasoningTokenCount]],
                         protocol: effective)
            } else {
                var last = ""
                for try await snapshot in session.streamResponse(to: prompt) {
                    last = snapshot.content
                    try emit(["event": "snapshot", "text": last], protocol: effective)
                }
                // Streaming usage has not been qualified; omit it.
                try emit(["event": "complete", "text": last], protocol: effective)
            }
        } catch let error as LanguageModelError {
            switch error {
            case .contextSizeExceeded: fail("context_capacity", protocol: effective)
            case .rateLimited: fail("rate_limited", protocol: effective)
            case .refusal, .guardrailViolation: fail("request_declined", protocol: effective)
            default: fail("native_failed", protocol: effective)
            }
        } catch {
            // Never print error descriptions: SDK errors may contain input.
            fail("native_failed", protocol: effective)
        }
    }
}
