// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels

// One request per process. No tools, persistent session, instructions, truncation,
// implicit fallback, or diagnostic reflection of the request contents.
@main
struct HollisNative {
    static let version = "0.6.0"
    static let protocolVersion = 3

    struct Request: Decodable {
        let `protocol`: Int
        let operation: String
        let prompt: String?
        let schema: JSONValue?
        let toolCalling: String?
        let source: Source?
        let pages: [Int]?
        let media: String?

        // Member names are the wire contract; the Swift property names are not.
        enum CodingKeys: String, CodingKey {
            case `protocol`, operation, prompt, schema
            case toolCalling = "tool_calling"
            case source, pages, media
        }
    }

    struct Source: Decodable {
        let path: String
    }

    static let extractCapability: [String: Any] = [
        "pdf": true, "ocr": true, "pages": true,
        "max_pages": Extraction.maxPages, "max_text_bytes": Extraction.maxTextBytes,
    ]

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

    static func statusFields(available: Bool, reason: String, contextSize: Int,
                             vision: Bool, macOSMajor: Int, `protocol` effective: Int) -> [String: Any] {
        var fields: [String: Any] = ["event": "status", "available": available, "reason": reason]
        if effective >= 2 { fields["context_size"] = contextSize }
        if effective >= 3 {
            // Unimplemented capabilities stay absent, not zero-filled.
            fields["capabilities"] = ["vision": vision, "macos_major": macOSMajor,
                                      "extract": extractCapability]
        }
        return fields
    }

    /// Runs one extraction and returns its terminal event. A failure is a fixed
    /// kind with integer fields only: no path, page text or framework
    /// diagnostic reaches the event.
    static func extractEvent(path: String, pages: [Int]?, hint: Extraction.Media?) -> [String: Any] {
        do {
            let document = try Extraction.extract(path: path, pages: pages, mediaHint: hint)
            return ["event": "complete", "media": document.media.rawValue,
                    "page_count": document.pageCount, "text_bytes": document.textBytes,
                    "pages": document.pages.map {
                        ["page": $0.number, "text": $0.text, "method": $0.method.rawValue] as [String: Any]
                    }]
        } catch let failure as Extraction.Failure {
            switch failure {
            case .unsupportedMedia:
                return ["event": "error", "kind": "unsupported_media"]
            case .invalidDocument(let limit, let actual):
                var event: [String: Any] = ["event": "error", "kind": "invalid_document"]
                if let limit, let actual { event["limit"] = limit; event["actual"] = actual }
                return event
            case .mediaMismatch(let detected):
                return ["event": "error", "kind": "extract_media_mismatch", "media": detected.rawValue]
            case .pageOutOfRange(let page, let pageCount):
                return ["event": "error", "kind": "page_out_of_range", "page": page, "page_count": pageCount]
            case .noText:
                return ["event": "error", "kind": "extract_no_text"]
            case .textLimit(let limit, let actual, _):
                return ["event": "error", "kind": "context_capacity", "limit": limit, "actual": actual]
            }
        } catch {
            return ["event": "error", "kind": "invalid_document"]
        }
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
            // Request formats 1, 2 and 3 remain accepted, but every event keeps
            // this build's version. Older CLIs reject that version: only a
            // matched bundle is supported. Era-specific fields stay omitted
            // below their protocol, independently of version identity.
            let accepted = [1, 2, protocolVersion]
            guard accepted.contains(request.`protocol`) else {
                fail("native_failed"); return
            }
            effective = request.`protocol`
            var operations = ["status", "complete", "stream"]
            if effective >= 2 { operations.append("schema") }
            if effective >= 3 { operations.append("extract") }
            guard operations.contains(request.operation) else {
                fail("native_failed", protocol: effective); return
            }
            // Extract members belong to extract alone, and extract carries
            // none of the generation members: a request that mixes them is
            // malformed rather than partly served.
            let extractMembers = request.source != nil || request.pages != nil || request.media != nil
            if request.operation == "extract" {
                guard request.prompt == nil, request.schema == nil, request.toolCalling == nil,
                      let source = request.source, source.path.hasPrefix("/") else {
                    fail("native_failed", protocol: effective); return
                }
                if let pages = request.pages {
                    // Ascending and unique, as the caller pins it; a selection
                    // this helper would have to reorder is not the one sent.
                    guard !pages.isEmpty, pages.count <= Extraction.maxPages,
                          pages.first! >= 1, zip(pages, pages.dropFirst()).allSatisfy({ $0 < $1 }) else {
                        fail("native_failed", protocol: effective); return
                    }
                }
                var hint: Extraction.Media?
                if let media = request.media {
                    guard let known = Extraction.Media(rawValue: media) else {
                        try emit(["event": "error", "kind": "unsupported_media"], protocol: effective)
                        return
                    }
                    hint = known
                }
                let model = SystemLanguageModel.default
                try emit(statusFields(
                    available: model.isAvailable, reason: String(describing: model.availability),
                    contextSize: model.contextSize,
                    vision: model.capabilities.contains(.vision),
                    macOSMajor: ProcessInfo.processInfo.operatingSystemVersion.majorVersion,
                    protocol: effective), protocol: effective)
                // Extraction makes no model call, so it does not wait on the
                // model being available; the generation that follows does.
                try emit(extractEvent(path: source.path, pages: request.pages, hint: hint), protocol: effective)
                return
            }
            if extractMembers {
                fail("native_failed", protocol: effective); return
            }
            if request.operation == "schema", request.schema == nil {
                fail("native_failed", protocol: effective); return
            }
            if request.operation != "schema", request.schema != nil {
                fail("native_failed", protocol: effective); return
            }
            // tool_calling is a known protocol-3 member with no implementation
            // here yet. A helper that ignored it would answer a caller who
            // asked for a tool mode with an ordinary completion, so the whole
            // request is refused instead of silently downgraded.
            if request.toolCalling != nil {
                if effective < 3 { fail("native_failed", protocol: effective); return }
                try emit(["event": "error", "kind": "unsupported_capability",
                          "capability": "tools", "reason": "not_implemented"],
                         protocol: effective)
                return
            }
            // The subset is parsed and validated before any model access:
            // an unsupported schema must never reach generation.
            var requested: SchemaSubset.Node?
            if let raw = request.schema {
                do { requested = try SchemaSubset.parse(raw) }
                catch { fail("native_failed", protocol: effective); return }
            }
            let model = SystemLanguageModel.default
            let status = statusFields(
                available: model.isAvailable, reason: String(describing: model.availability),
                contextSize: effective >= 2 ? model.contextSize : 0,
                vision: effective >= 3 && model.capabilities.contains(.vision),
                macOSMajor: ProcessInfo.processInfo.operatingSystemVersion.majorVersion,
                protocol: effective)
            try emit(status, protocol: effective)
            if request.operation == "status" { return }
            guard let prompt = request.prompt,
                  !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                fail("native_failed", protocol: effective); return
            }
            guard prompt.utf8.count <= 131_072 else {
                fail("context_capacity", protocol: effective); return
            }
            guard model.isAvailable else { return }
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
