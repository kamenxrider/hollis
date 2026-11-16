// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels
import CoreGraphics

// One request per process. Tool callbacks only hand off to the parent.
@main
struct HollisNative {
    static let version = "0.7.1"
    static let protocolVersion = 4

    struct Request: Decodable {
        let `protocol`: Int
        let operation: String
        let prompt: String?
        let schema: JSONValue?
        let toolCalling: String?
        let source: Source?
        let pages: [Int]?
        let media: String?
        let images: [Source]?
        let generationOptions: NativeTools.Options?
        let instructions: String?
        let history: [Message]?
        let permissive: Bool?

        // Member names are the wire contract; the Swift property names are not.
        enum CodingKeys: String, CodingKey {
            case `protocol`, operation, prompt, schema
            case toolCalling = "tool_calling"
            case source, pages, media, images
            case generationOptions = "generation_options"
            case instructions, history, permissive
        }
    }

    struct Message: Decodable {
        let role: String
        let content: String
    }

    struct Source: Decodable {
        let path: String
    }

    static let extractCapability: [String: Any] = [
        "pdf": true, "ocr": true, "pages": true,
        "max_pages": Extraction.maxPages, "max_text_bytes": Extraction.maxTextBytes,
    ]

    static func imageCapability(vision: Bool, macOSMajor: Int) -> [String: Any] {
        guard macOSMajor >= 27 else { return ["supported": false, "reason": "macos_26"] }
        guard vision else { return ["supported": false, "reason": "no_vision"] }
        return ["supported": true, "max_images": 1, "formats": ["png", "jpeg"],
                "max_side_px": ImageInput.maxSidePixels]
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

    static func statusFields(available: Bool, reason: String, contextSize: Int,
                             vision: Bool, tools: Bool, macOSMajor: Int, `protocol` effective: Int) -> [String: Any] {
        var fields: [String: Any] = ["event": "status", "available": available, "reason": reason]
        if effective >= 2 && contextSize > 0 { fields["context_size"] = contextSize }
        if effective >= 3 {
            // Unimplemented capabilities stay absent, not zero-filled.
            fields["capabilities"] = ["vision": vision, "macos_major": macOSMajor,
                                      "extract": extractCapability,
                                      "images": imageCapability(vision: vision, macOSMajor: macOSMajor),
                                      "tools": ["supported": tools, "tool_calling_mode": true,
                                                "native_definitions": tools, "hybrid_tools": tools,
                                                "executable": tools, "turn_handoff": tools]]
            if effective >= 4 {
                var capabilities = fields["capabilities"] as! [String: Any]
                capabilities["stream_token_usage"] = true
                fields["capabilities"] = capabilities
            }
        }
        return fields
    }

    // Availability metadata only: no session, prompt, entitlement or PCC request.
    static func quotaFields() -> [String: Any] {
        let quota = PrivateCloudComputeLanguageModel().quotaUsage
        let status: String
        switch quota.status {
        case .belowLimit(let below): status = below.isApproachingLimit ? "approaching" : "available"
        case .limitReached: status = "limit_reached"
        @unknown default: status = "unknown"
        }
        var fields: [String: Any] = ["status": status]
        if let reset = quota.resetDate {
            fields["reset_date"] = ISO8601DateFormatter().string(from: reset)
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
        let hybridMode = CommandLine.arguments == [CommandLine.arguments[0], "--hybrid-tools"]
        let metadataMode = hybridMode || CommandLine.arguments == [CommandLine.arguments[0], "--native-definitions"]
        let nativeMode = CommandLine.arguments == [CommandLine.arguments[0], "--native-tools"]
        guard CommandLine.arguments.count == 1 || metadataMode || nativeMode else { fail("native_failed"); return }
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
            if nativeMode {
                let prepared: NativeTools.Prepared
                do { prepared = try NativeTools.prepare(input) }
                catch NativeTools.Failure.capacity { fail("context_capacity"); return }
                catch { fail("native_protocol"); return }
                func send(_ fields: [String: Any]) throws {
                    var fields = fields
                    fields["request_id"] = prepared.request.requestID
                    try emit(fields, protocol: protocolVersion)
                }
                let model = SystemLanguageModel.default
                try send(statusFields(
                    available: model.isAvailable, reason: String(describing: model.availability),
                    contextSize: model.contextSize, vision: model.capabilities.contains(.vision),
                    tools: model.isAvailable && model.capabilities.contains(.toolCalling),
                    macOSMajor: ProcessInfo.processInfo.operatingSystemVersion.majorVersion,
                    protocol: protocolVersion))
                guard model.isAvailable else { return }
                guard model.contextSize > 0 else {
                    try send(["event": "error", "kind": "local_unavailable"])
                    return
                }
                guard model.capabilities.contains(.toolCalling) else {
                    try send(["event": "error", "kind": "unsupported_capability",
                              "capability": "tools", "reason": "no_tool_calling"])
                    return
                }
                do {
                    let diagnostics = ProcessInfo.processInfo.environment["HOLLIS_DEBUG_BUDGET"] == "1"
                    try send(await NativeTools.run(prepared, budget: diagnostics ? { counts in
                        try send(["event": "budget", "budget": counts])
                    } : nil))
                }
                catch NativeTools.Failure.capacity {
                    try send(["event": "error", "kind": "context_capacity"])
                } catch NativeTools.Failure.partialBatch {
                    try send(["event": "error", "kind": "native_protocol"])
                } catch let failure as NativeTools.GenerationFailure {
                    try send(NativeTools.failureEvent(
                        failure.underlying, callbackCaptured: failure.callbackCaptured))
                } catch {
                    try send(NativeTools.failureEvent(error, callbackCaptured: false))
                }
                return
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
            let accepted = [1, 2, 3, protocolVersion]
            guard accepted.contains(request.`protocol`) else {
                fail("native_failed"); return
            }
            effective = request.`protocol`
            let metadata: NativeToolMetadata.Prepared
            do {
                metadata = try NativeToolMetadata.prepare(
                    JSONDecoder().decode(JSONValue.self, from: input), enabled: metadataMode, hybridEnabled: hybridMode)
            } catch NativeToolMetadata.Failure.capacity {
                fail("context_capacity", protocol: effective); return
            } catch { fail("native_failed", protocol: effective); return }
            var operations = ["status", "complete", "stream", "count_tokens"]
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
                guard request.prompt == nil, request.schema == nil, request.toolCalling == nil, request.images == nil,
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
                    tools: model.isAvailable && model.capabilities.contains(.toolCalling),
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
            if let images = request.images {
                guard effective >= 3, request.toolCalling == nil,
                      request.operation == "complete" || request.operation == "stream",
                      images.count == 1, images[0].path.hasPrefix("/") else {
                    fail("native_failed", protocol: effective); return
                }
            }
            if request.operation == "schema", request.schema == nil {
                fail("native_failed", protocol: effective); return
            }
            if request.operation != "schema", request.schema != nil {
                fail("native_failed", protocol: effective); return
            }
            // tool_calling is a protocol-3 member of plain generation. Only the
            // two measured modes exist: the SDK's required mode throws on this
            // OS build. Any other value, era or operation is refused rather
            // than served as a completion that silently dropped the mode.
            var options = try request.generationOptions?.generationOptions() ?? GenerationOptions()
            if request.generationOptions != nil {
                guard effective == 4,
                      request.operation == "complete" || request.operation == "stream" || request.operation == "schema"
                else { fail("native_failed", protocol: effective); return }
            }
            if let mode = request.toolCalling {
                guard effective >= 3,
                      request.operation == "complete" || request.operation == "stream" else {
                    fail("native_failed", protocol: effective); return
                }
                switch mode {
                case "allowed": options.toolCallingMode = .allowed
                case "disallowed": options.toolCallingMode = .disallowed
                default: fail("native_failed", protocol: effective); return
                }
            }
            // The subset is parsed and validated before any model access:
            // an unsupported schema must never reach generation.
            var requested: SchemaSubset.Node?
            if let raw = request.schema {
                do { requested = try SchemaSubset.parse(raw) }
                catch { fail("native_failed", protocol: effective); return }
            }
            let model = request.permissive == true
                ? SystemLanguageModel(guardrails: .permissiveContentTransformations)
                : SystemLanguageModel.default
            let supportsTools = model.isAvailable && model.capabilities.contains(.toolCalling)
            var status = statusFields(
                available: model.isAvailable, reason: String(describing: model.availability),
                contextSize: effective >= 2 ? model.contextSize : 0,
                vision: effective >= 3 && model.capabilities.contains(.vision),
                tools: supportsTools,
                macOSMajor: ProcessInfo.processInfo.operatingSystemVersion.majorVersion,
                protocol: effective)
            if request.operation == "status", effective >= 4 {
                var capabilities = status["capabilities"] as! [String: Any]
                capabilities["pcc_quota"] = quotaFields()
                status["capabilities"] = capabilities
            }
            try emit(status, protocol: effective)
            if request.operation == "status" { return }
            guard let prompt = request.prompt,
                  !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                fail("native_failed", protocol: effective); return
            }
            let historyBytes = (request.history ?? []).reduce(0) { $0 + $1.content.utf8.count }
            guard metadata.inputBytes + (request.instructions?.utf8.count ?? 0) + historyBytes <= 131_072 else {
                fail("context_capacity", protocol: effective); return
            }
            var image: CGImage?
            if let images = request.images {
                guard #available(macOS 27, *) else {
                    fail("unsupported_os", protocol: effective); return
                }
                guard model.capabilities.contains(.vision) else {
                    try emit(["event": "error", "kind": "unsupported_capability",
                              "capability": "images", "reason": "no_vision"], protocol: effective)
                    return
                }
                image = try ImageInput.load(path: images[0].path)
            }
            guard model.isAvailable else { return }
            guard model.contextSize > 0 else {
                fail("local_unavailable", protocol: effective); return
            }
            if metadataMode && !supportsTools {
                try emit(["event": "error", "kind": "unsupported_capability",
                          "capability": "tools", "reason": "no_tool_calling"], protocol: effective)
                return
            }
            var entries = metadata.entries
            if let instructions = request.instructions {
                entries.append(.instructions(Transcript.Instructions(
                    segments: [NativeTools.segment(instructions)], toolDefinitions: [])))
            }
            for message in request.history ?? [] {
                guard !message.content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                    fail("native_failed", protocol: effective); return
                }
                switch message.role {
                case "user": entries.append(.prompt(Transcript.Prompt(segments: [NativeTools.segment(message.content)])))
                case "assistant": entries.append(.response(Transcript.Response(segments: [NativeTools.segment(message.content)])))
                default: fail("native_failed", protocol: effective); return
                }
            }
            let count: Int
            if metadataMode {
                count = try await model.tokenCount(for: NativeToolMetadata.preflightEntries(
                    metadata.entries, prompt: prompt, options: options))
            } else if !entries.isEmpty || request.operation == "count_tokens" {
                let seeded = entries.isEmpty ? Array(LanguageModelSession(model: model).transcript) : entries
                count = try await model.tokenCount(for: NativeToolMetadata.preflightEntries(seeded, prompt: prompt, options: options))
            } else {
                count = try await model.tokenCount(for: prompt)
            }
            if request.operation == "count_tokens" {
                guard request.images == nil, request.schema == nil, request.toolCalling == nil else {
                    fail("native_failed", protocol: effective); return
                }
                try emit(["event": "checked", "input_tokens": count], protocol: effective)
                return
            }
            guard count < model.contextSize else {
                fail("context_capacity", protocol: effective); return
            }
            let session = !entries.isEmpty
                ? LanguageModelSession(model: model, tools: [], transcript: Transcript(entries: entries))
                : LanguageModelSession(model: model)
            let generationPrompt = Prompt {
                prompt
                if let image {
                    Attachment<ImageAttachmentContent>(image)
                }
            }
            if request.operation == "schema" {
                let response = try await session.respond(
                    to: prompt, schema: try SchemaSubset.schema(for: requested!), options: options)
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
                let response = try await session.respond(to: generationPrompt, options: options)
                try emit(["event": "complete", "text": response.content,
                          "usage": ["input_tokens": response.usage.input.totalTokenCount,
                                    "output_tokens": response.usage.output.totalTokenCount,
                                    "reasoning_tokens": response.usage.output.reasoningTokenCount]],
                         protocol: effective)
            } else {
                var last = ""
                for try await snapshot in session.streamResponse(to: generationPrompt, options: options) {
                    last = snapshot.content
                    try emit(["event": "snapshot", "text": last], protocol: effective)
                }
                // Counts are used only for the best-effort token-limit signal.
                // Public streaming usage remains unqualified and omitted.
                var terminal: [String: Any] = ["event": "complete", "text": last]
                if effective >= 4 {
                    terminal["usage"] = ["input_tokens": session.usage.input.totalTokenCount,
                                         "output_tokens": session.usage.output.totalTokenCount,
                                         "reasoning_tokens": session.usage.output.reasoningTokenCount]
                }
                try emit(terminal, protocol: effective)
            }
        } catch ImageInput.Failure.unsupportedMedia {
            fail("unsupported_media", protocol: effective)
        } catch ImageInput.Failure.invalidImage {
            fail("invalid_image", protocol: effective)
        } catch {
            // Older protocols have no error-classification payload.
            if effective == 4 {
                try? emit(NativeTools.failureEvent(error, callbackCaptured: true), protocol: effective)
            } else {
                fail("native_failed", protocol: effective)
            }
        }
    }
}
