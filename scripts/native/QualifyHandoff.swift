import Foundation
import FoundationModels

private struct Handoff: Error {}

private actor Observations {
    var callbacks: [String] = []
    func record(_ name: String) { callbacks.append(name) }
}

private struct HandoffTool: Tool {
    typealias Arguments = GeneratedContent
    let name: String
    let description: String
    let parameters: GenerationSchema
    let observations: Observations

    func call(arguments: GeneratedContent) async throws -> String {
        await observations.record(name)
        throw Handoff()
    }
}

private struct HandoffProfile: LanguageModelSession.DynamicProfile {
    let tools: [any Tool]
    let instructions: String
    let seed: UInt64
    let required: Bool

    var body: some LanguageModelSession.DynamicProfile {
        LanguageModelSession.Profile {
            Instructions(instructions)
            tools
        }
        .model(SystemLanguageModel.default)
        .transcriptErrorHandlingPolicy(.preserveTranscript)
        .toolCallingMode(required ? .required : .allowed)
        .samplingMode(.random(probabilityThreshold: 0.9, seed: seed))
        .temperature(0.7)
        .maximumResponseTokens(512)
    }
}

private struct Request: Decodable {
    struct Definition: Decodable {
        let name: String
        let description: String
        let parameters: JSONValue
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
    }
    let instructions: String
    let prompt: String
    let tools: [Definition]
    let history: [History]
    let seed: UInt64
    let required: Bool
}

private func emit(_ value: [String: Any]) throws {
    let data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    FileHandle.standardOutput.write(data + Data([10]))
}

private func segment(_ text: String) -> Transcript.Segment {
    .text(Transcript.TextSegment(content: text))
}

@main private struct QualifyHandoff {
    static func main() async {
        do {
            let data = FileHandle.standardInput.readDataToEndOfFile()
            try StrictJSON.scan(data, subject: "qualification request")
            let request = try JSONDecoder().decode(Request.self, from: data)
            let observations = Observations()
            let tools: [any Tool] = try request.tools.map {
                HandoffTool(name: $0.name, description: $0.description,
                    parameters: try SchemaSubset.schema(
                        for: SchemaSubset.parse($0.parameters), name: $0.name),
                    observations: observations)
            }
            var history: [Transcript.Entry] = []
            for entry in request.history {
                switch entry.type {
                case "user":
                    history.append(.prompt(Transcript.Prompt(segments: [segment(entry.text!)])))
                case "assistant":
                    history.append(.response(Transcript.Response(
                        segments: [segment(entry.text!)])))
                case "calls":
                    let calls = try entry.calls!.map {
                        Transcript.ToolCall(id: $0.id, toolName: $0.name,
                            arguments: try GeneratedContent(json: $0.arguments))
                    }
                    history.append(.toolCalls(Transcript.ToolCalls(calls)))
                case "output":
                    history.append(.toolOutput(Transcript.ToolOutput(
                        id: entry.id!, toolName: entry.name!, segments: [segment(entry.text!)])))
                default:
                    throw CocoaError(.coderReadCorrupt)
                }
            }
            let session = LanguageModelSession(profile: HandoffProfile(
                tools: tools, instructions: request.instructions, seed: request.seed,
                required: request.required), history: history)
            let startCount = session.transcript.count
            let inputTokens = try await SystemLanguageModel.default.tokenCount(
                for: Array(session.transcript) + [
                    .prompt(Transcript.Prompt(segments: [segment(request.prompt)]))])
            try emit(["event": "preflight", "input_tokens": inputTokens,
                      "context_size": SystemLanguageModel.default.contextSize])
            let start = Date()
            var sentinel = false
            do {
                let response = try await session.respond(to: request.prompt)
                try emit(["event": "complete", "text": response.content,
                          "usage_input": response.usage.input.totalTokenCount,
                          "usage_output": response.usage.output.totalTokenCount,
                          "latency_s": Date().timeIntervalSince(start)])
            } catch is Handoff {
                sentinel = true
            } catch let error as LanguageModelSession.ToolCallError
                where error.underlyingError is Handoff {
                sentinel = true
            }
            let entries = Array(session.transcript).dropFirst(startCount)
            var batches: [[Request.Call]] = []
            var kinds: [String] = []
            for entry in entries {
                switch entry {
                case .toolCalls(let calls):
                    kinds.append("calls")
                    batches.append(calls.map {
                        Request.Call(id: $0.id, name: $0.toolName,
                                     arguments: $0.arguments.jsonString)
                    })
                case .prompt: kinds.append("prompt")
                case .response: kinds.append("response")
                case .toolOutput: kinds.append("output")
                case .reasoning: kinds.append("reasoning")
                default: kinds.append("other")
                }
            }
            let encoded = try JSONEncoder().encode(batches)
            try emit(["event": "transcript", "sentinel": sentinel,
                      "batches": try JSONSerialization.jsonObject(with: encoded),
                      "entry_kinds": kinds, "callbacks": await observations.callbacks,
                      "latency_s": Date().timeIntervalSince(start)])
        } catch {
            try? emit(["event": "error", "message": String(describing: error)])
            exit(1)
        }
    }
}
