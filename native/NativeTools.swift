import Foundation
import FoundationModels

enum NativeTools {
    enum Failure: Error { case invalid, capacity, partialBatch }

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
        let requestID: String
        let step: Int
        let remainingSteps: Int
        let toolChoice: Choice
        let instructions: String
        let tools: [Definition]
        let history: [History]
        let generationOptions: Options?

        enum CodingKeys: String, CodingKey {
            case step, instructions, tools, history
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
              fields["operation"]?.text == "tool_step" else { throw Failure.invalid }
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
