import Foundation
import FoundationModels

enum NativeTools {
    enum Failure: Error { case invalid, capacity, partialBatch }

    struct GenerationFailure: Error {
        let underlying: Error
        let callbackCaptured: Bool
    }

    actor CallbackState {
        private(set) var captured = false
        func record() { captured = true }
    }

    static func failureEvent(_ error: Error, callbackCaptured: Bool) -> [String: Any] {
        var current = error
        for _ in 0..<4 {
            let ns = current as NSError
            let type = String(reflecting: Swift.type(of: current))
            var kind = "native_failed"
            var classified = false
            if let typed = current as? LanguageModelError {
                classified = true
                switch typed {
                case .contextSizeExceeded: kind = "context_capacity"
                case .rateLimited: kind = "rate_limited"
                case .refusal, .guardrailViolation: kind = "request_declined"
                default: break
                }
            } else if let typed = current as? LanguageModelSession.GenerationError {
                classified = true
                switch typed {
                case .exceededContextWindowSize: kind = "context_capacity"
                case .rateLimited: kind = "rate_limited"
                case .refusal, .guardrailViolation: kind = "request_declined"
                default: break
                }
            }
            let manager = ns.domain == "ModelManagerError" || ns.domain.hasSuffix(".ModelManagerError")
                || type == "ModelManagerError" || type.hasSuffix(".ModelManagerError")
            if !classified && manager && ns.code == 1012 { kind = "model_busy" }
            if !classified && kind != "model_busy",
               let underlying = ns.userInfo[NSUnderlyingErrorKey] as? Error {
                current = underlying
                continue
            }
            return ["event": "error", "kind": kind, "error_info": [
                "swift_type": errorIdentifier(type), "domain": errorIdentifier(ns.domain),
                "code": ns.code, "callback_captured": callbackCaptured]]
        }
        return ["event": "error", "kind": "native_failed"]
    }

    private static func errorIdentifier(_ value: String) -> String {
        guard !value.isEmpty, value.utf8.count <= 192,
              value.utf8.allSatisfy({
                  (65...90).contains($0) || (97...122).contains($0)
                    || (48...57).contains($0) || $0 == 95 || $0 == 46
              }) else { return "unknown" }
        return value
    }

    struct Call: Codable {
        let id: String
        let name: String
        let arguments: String
    }

    struct History: Decodable {
        let type: String
        let text: String?
        let id: String?
        let name: String?
        let calls: [Call]?
        let isError: Bool?

        enum CodingKeys: String, CodingKey {
            case type, text, id, name, calls
            case isError = "is_error"
        }
    }

    struct Definition: Decodable {
        let name: String
        let description: String
        let parameters: JSONValue
        let source: String
        let validation: String
    }

    struct Choice: Decodable {
        let type: String
        let name: String?
    }

    struct Options: Decodable {
        let temperature: Double?
        let topP: Double?
        let maximumTokens: Int?
        let seed: UInt64?

        enum CodingKeys: String, CodingKey {
            case temperature, seed
            case topP = "top_p"
            case maximumTokens = "max_tokens"
        }

        func generationOptions() throws -> GenerationOptions {
            if let temperature {
                guard temperature.isFinite, (0...2).contains(temperature) else { throw Failure.invalid }
            }
            if let topP {
                guard topP.isFinite, topP > 0, topP <= 1 else { throw Failure.invalid }
            }
            if let maximumTokens { guard maximumTokens > 0 else { throw Failure.invalid } }
            guard temperature != 0 || (topP == nil && seed == nil) else { throw Failure.invalid }
            var options = GenerationOptions()
            options.temperature = temperature
            options.maximumResponseTokens = maximumTokens
            if temperature == 0 {
                options.samplingMode = .greedy
            } else if topP != nil || seed != nil {
                options.samplingMode = .random(probabilityThreshold: topP ?? 1, seed: seed)
            }
            return options
        }
    }

    struct Request: Decodable {
        let operation: String
        let requestID: String
        let step: Int
        let remainingSteps: Int
        let toolChoice: Choice
        let instructions: String
        let tools: [Definition]
        let history: [History]
        let generationOptions: Options?

        enum CodingKeys: String, CodingKey {
            case operation, step, instructions, tools, history
            case requestID = "request_id"
            case remainingSteps = "remaining_steps"
            case toolChoice = "tool_choice"
            case generationOptions = "generation_options"
        }
    }

    struct Prepared {
        let request: Request
        let definitions: [(Definition, GenerationSchema)]
        let history: [Transcript.Entry]
        let options: GenerationOptions
        let prompt: String
    }

    private struct Handoff: Error {}

    private struct HandoffTool: Tool {
        typealias Arguments = GeneratedContent
        let name: String
        let description: String
        let parameters: GenerationSchema
        let callbacks: CallbackState

        func call(arguments: GeneratedContent) async throws -> String {
            await callbacks.record()
            throw Handoff()
        }
    }

    private struct Profile: LanguageModelSession.DynamicProfile {
        let tools: [any Tool]
        let instructions: String
        let options: GenerationOptions

        var body: some LanguageModelSession.DynamicProfile {
            LanguageModelSession.Profile {
                Instructions(instructions)
                tools
            }
            .model(SystemLanguageModel.default)
            .transcriptErrorHandlingPolicy(.preserveTranscript)
            .toolCallingMode(options.toolCallingMode)
            .temperature(options.temperature)
            .samplingMode(options.samplingMode)
            .maximumResponseTokens(options.maximumResponseTokens)
        }
    }

    static func capture(_ entries: [Transcript.Entry], allowedNames: Set<String>) throws -> [Call] {
        var batch: Transcript.ToolCalls?
        var prompts = 0
        for entry in entries {
            switch entry {
            case .prompt:
                prompts += 1
                guard prompts == 1, batch == nil else { throw Failure.partialBatch }
            case .toolCalls(let calls):
                guard batch == nil, prompts == 1, !calls.isEmpty, calls.count <= 16
                else { throw Failure.partialBatch }
                batch = calls
            case .reasoning:
                guard batch == nil else { throw Failure.partialBatch }
            default: throw Failure.partialBatch
            }
        }
        guard let batch else { throw Failure.partialBatch }
        var ids = Set<String>()
        return try batch.map { call in
            guard validID(call.id), ids.insert(call.id).inserted,
                  allowedNames.contains(call.toolName), call.arguments.isComplete
            else { throw Failure.partialBatch }
            let arguments = call.arguments.jsonString
            guard arguments.utf8.count <= 65_536 else { throw Failure.partialBatch }
            try StrictJSON.scan(Data(arguments.utf8), subject: "native arguments")
            guard try JSONDecoder().decode(JSONValue.self, from: Data(arguments.utf8)).fields != nil
            else { throw Failure.partialBatch }
            return Call(id: call.id, name: call.toolName, arguments: arguments)
        }
    }

    static func run(_ prepared: Prepared,
                    budget: (([String: Int]) throws -> Void)? = nil) async throws -> [String: Any] {
        let request = prepared.request
        var fulfilled = false
        for entry in request.history {
            if entry.type == "user" { fulfilled = false }
            if entry.type == "calls", let calls = entry.calls {
                if request.toolChoice.type == "required" {
                    fulfilled = true
                } else if request.toolChoice.type == "function" {
                    fulfilled = fulfilled || calls.contains { $0.name == request.toolChoice.name }
                }
            }
        }
        var options = prepared.options
        switch request.toolChoice.type {
        case "none": options.toolCallingMode = .disallowed
        case "required": options.toolCallingMode = fulfilled ? .allowed : .required
        case "function": options.toolCallingMode = fulfilled ? .disallowed : .required
        default: options.toolCallingMode = .allowed
        }
        let callbacks = CallbackState()
        let tools: [any Tool] = prepared.definitions.compactMap { definition, schema in
            if request.toolChoice.type == "none" { return nil }
            if request.toolChoice.type == "function",
               fulfilled || definition.name != request.toolChoice.name { return nil }
            return HandoffTool(name: definition.name, description: definition.description,
                               parameters: schema, callbacks: callbacks)
        }
        let session = LanguageModelSession(profile: Profile(
            tools: tools, instructions: request.instructions, options: options), history: prepared.history)
        let startCount = session.transcript.count
        let entries = Array(session.transcript) + [
            .prompt(Transcript.Prompt(segments: [segment(prepared.prompt)], options: options))]
        let tokens = try await SystemLanguageModel.default.tokenCount(for: entries)
        if let budget {
            // Incremental counts in installation order add up to the actual
            // preflight. No prompt, schema, identifier or result text is emitted.
            let model = SystemLanguageModel.default
            let base = try await model.tokenCount(for: Array(LanguageModelSession(model: model).transcript))
            let instructed = try await model.tokenCount(for: Array(
                LanguageModelSession(model: model, instructions: request.instructions).transcript))
            let installed = Array(session.transcript.prefix(startCount - prepared.history.count))
            let definitions = try await model.tokenCount(for: installed)
            let withoutResults = entries.filter {
                if case .toolOutput = $0 { return false }
                return true
            }
            let conversation = try await model.tokenCount(for: withoutResults)
            let toolBytes = request.tools.reduce(0) {
                $0 + $1.name.utf8.count + $1.description.utf8.count
                    + ((try? JSONSerialization.data(withJSONObject: $1.parameters.foundationObject).count) ?? 0)
            }
            let historyBytes = request.history.filter { $0.type != "output" }.reduce(0) {
                $0 + ($1.text?.utf8.count ?? 0) + ($1.calls ?? []).reduce(0) {
                    $0 + $1.id.utf8.count + $1.name.utf8.count + $1.arguments.utf8.count
                }
            }
            let resultBytes = request.history.filter { $0.type == "output" }.reduce(0) {
                $0 + ($1.text?.utf8.count ?? 0) + ($1.id?.utf8.count ?? 0) + ($1.name?.utf8.count ?? 0)
            }
            try budget([
                "base_tokens": base, "instructions_tokens": instructed - base,
                "tools_tokens": definitions - instructed,
                "conversation_tokens": conversation - definitions,
                "results_framing_tokens": tokens - conversation,
                "input_tokens": tokens, "answer_reserved_tokens": options.maximumResponseTokens ?? 512,
                "context_tokens": model.contextSize, "tools_bytes": toolBytes,
                "instructions_bytes": request.instructions.utf8.count,
                "conversation_bytes": historyBytes, "results_framing_bytes": resultBytes,
            ])
        }
        guard (options.maximumResponseTokens ?? 512) <= SystemLanguageModel.default.contextSize - tokens
        else { throw Failure.capacity }
        if request.operation == "tool_check" {
            return ["event": "checked", "input_tokens": tokens]
        }
        do {
            let response = try await session.respond(to: prepared.prompt)
            return ["event": "complete", "text": response.content,
                    "usage": ["input_tokens": response.usage.input.totalTokenCount,
                              "output_tokens": response.usage.output.totalTokenCount,
                              "reasoning_tokens": response.usage.output.reasoningTokenCount]]
        } catch is Handoff {
        } catch let error as LanguageModelSession.ToolCallError
            where error.underlyingError is Handoff {
        } catch {
            let invoked = await callbacks.captured
            let recorded = session.transcript.dropFirst(startCount).contains {
                if case .toolCalls = $0 { return true }
                return false
            }
            throw GenerationFailure(underlying: error, callbackCaptured: invoked || recorded)
        }
        guard options.toolCallingMode != .disallowed else { throw Failure.partialBatch }
        let calls = try capture(Array(session.transcript).dropFirst(startCount).map { $0 },
                                allowedNames: Set(tools.map(\.name)))
        let encoded = try JSONEncoder().encode(calls)
        return ["event": "tool_calls", "step": request.step,
                "calls": try JSONSerialization.jsonObject(with: encoded)]
    }

    static func validID(_ id: String) -> Bool {
        !id.isEmpty && id.utf8.count <= 128 && !id.unicodeScalars.contains {
            CharacterSet.controlCharacters.contains($0)
        }
    }

    static func segment(_ text: String) -> Transcript.Segment {
        .text(Transcript.TextSegment(content: text))
    }

    static func prepare(_ data: Data) throws -> Prepared {
        try StrictJSON.scan(data, subject: "native tools")
        let root = try JSONDecoder().decode(JSONValue.self, from: data)
        guard let fields = root.fields,
              Set(fields.keys).isSubset(of: [
                "protocol", "operation", "request_id", "step", "remaining_steps",
                "tool_choice", "instructions", "tools", "history", "generation_options"]),
              fields["protocol"]?.number == 4,
              ["tool_step", "tool_check"].contains(fields["operation"]?.text ?? "") else { throw Failure.invalid }
        let request = try JSONDecoder().decode(Request.self, from: data)
        guard validID(request.requestID), request.step > 0, request.remainingSteps > 0,
              request.step <= 128, request.remainingSteps <= 128,
              request.tools.count <= 128, !request.history.isEmpty else { throw Failure.invalid }
        var bytes = request.instructions.utf8.count
        var names = Set<String>()
        var callers = 0
        let definitions = try request.tools.map { definition in
            guard !definition.name.isEmpty, definition.name.utf8.count <= 64,
                  definition.name.utf8.allSatisfy({
                      (65...90).contains($0) || (97...122).contains($0)
                        || (48...57).contains($0) || $0 == 95 || $0 == 45
                  }), names.insert(definition.name).inserted,
                  ["caller", "mcp"].contains(definition.source),
                  ["strict", "server"].contains(definition.validation),
                  definition.validation != "server" || definition.source == "mcp"
            else { throw Failure.invalid }
            if definition.source == "caller" {
                callers += 1
                guard !definition.description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                else { throw Failure.invalid }
            }
            let encoded = try JSONSerialization.data(withJSONObject: definition.parameters.foundationObject)
            bytes += definition.name.utf8.count + definition.description.utf8.count + encoded.count
            return (definition, try SchemaSubset.schema(
                for: SchemaSubset.parse(definition.parameters), name: definition.name))
        }
        guard callers <= 32 else { throw Failure.invalid }
        switch request.toolChoice.type {
        case "auto", "none":
            guard request.toolChoice.name == nil else { throw Failure.invalid }
        case "required":
            guard request.toolChoice.name == nil, !names.isEmpty else { throw Failure.invalid }
        case "function":
            guard let name = request.toolChoice.name, names.contains(name) else { throw Failure.invalid }
        default: throw Failure.invalid
        }
        var history: [Transcript.Entry] = []
        var used = Set<String>()
        var outstanding: [String: String] = [:]
        for entry in request.history {
            switch entry.type {
            case "user", "assistant":
                guard outstanding.isEmpty, let text = entry.text,
                      entry.id == nil, entry.name == nil, entry.calls == nil, entry.isError == nil
                else { throw Failure.invalid }
                bytes += text.utf8.count
                if entry.type == "user" {
                    history.append(.prompt(Transcript.Prompt(segments: [segment(text)])))
                } else {
                    history.append(.response(Transcript.Response(segments: [segment(text)])))
                }
            case "calls":
                guard outstanding.isEmpty, let calls = entry.calls, !calls.isEmpty,
                      calls.count <= 16, entry.text == nil, entry.id == nil,
                      entry.name == nil, entry.isError == nil else { throw Failure.invalid }
                let sdk = try calls.map { call in
                    guard validID(call.id), used.insert(call.id).inserted,
                          names.contains(call.name), call.arguments.utf8.count <= 65_536
                    else { throw Failure.invalid }
                    let data = Data(call.arguments.utf8)
                    try StrictJSON.scan(data, subject: "tool arguments")
                    guard try JSONDecoder().decode(JSONValue.self, from: data).fields != nil
                    else { throw Failure.invalid }
                    outstanding[call.id] = call.name
                    bytes += call.id.utf8.count + call.name.utf8.count + data.count
                    return Transcript.ToolCall(id: call.id, toolName: call.name,
                                               arguments: try GeneratedContent(json: call.arguments))
                }
                history.append(.toolCalls(Transcript.ToolCalls(sdk)))
            case "output":
                guard let id = entry.id, let name = entry.name, let text = entry.text,
                      outstanding.removeValue(forKey: id) == name, entry.calls == nil
                else { throw Failure.invalid }
                bytes += id.utf8.count + name.utf8.count + text.utf8.count
                history.append(.toolOutput(Transcript.ToolOutput(
                    id: id, toolName: name, segments: [segment(text)])))
            default: throw Failure.invalid
            }
        }
        guard outstanding.isEmpty else { throw Failure.invalid }
        guard bytes <= 131_072 else { throw Failure.capacity }
        let options = try request.generationOptions?.generationOptions() ?? GenerationOptions()
        var prompt = ""
        if case .prompt(let last)? = history.last {
            prompt = last.segments.compactMap {
                if case .text(let text) = $0 { return text.content }
                return nil
            }.joined()
            history.removeLast()
        }
        return Prepared(request: request, definitions: definitions, history: history,
                        options: options, prompt: prompt)
    }
}
