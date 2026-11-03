// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels

// Model-free validation and transcript construction. No executable Tool is
// registered: definitions only describe calls Go must independently validate.
enum NativeToolMetadata {
    enum Failure: Error { case invalid, capacity }

    struct Prepared {
        let entries: [Transcript.Entry]
        let inputBytes: Int
    }

    static func prepare(_ root: JSONValue, enabled: Bool) throws -> Prepared {
        guard let request = root.fields else { throw Failure.invalid }
        let hasMetadata = request["tool_instructions"] != nil || request["tool_definitions"] != nil
        guard enabled == hasMetadata else { throw Failure.invalid }
        let promptBytes = request["prompt"]?.text?.utf8.count ?? 0
        if !enabled { return Prepared(entries: [], inputBytes: promptBytes) }
        // Unknown top-level members retain protocol 3's forward-compatible
        // ignore rule; recognized incompatible inputs are never dropped.
        guard request["schema"] == nil, request["images"] == nil,
              request["source"] == nil, request["pages"] == nil, request["media"] == nil,
              request["protocol"]?.number == 3,
              request["operation"]?.text == "complete",
              request["tool_calling"]?.text == "allowed",
              let instructions = request["tool_instructions"]?.text, !instructions.isEmpty,
              let definitions = request["tool_definitions"]?.elements,
              !definitions.isEmpty, definitions.count <= 32 else { throw Failure.invalid }
        var bytes = promptBytes + instructions.utf8.count
        var names = Set<String>()
        var tools: [Transcript.ToolDefinition] = []
        for definition in definitions {
            guard let fields = definition.fields,
                  Set(fields.keys) == Set(["name", "description", "parameters"]),
                  let name = fields["name"]?.text,
                  !name.isEmpty, name.utf8.count <= 64,
                  name.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0)
                      || (48...57).contains($0) || $0 == 95 || $0 == 45 }),
                  names.insert(name).inserted,
                  let description = fields["description"]?.text,
                  !description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                  let parameters = fields["parameters"] else { throw Failure.invalid }
            let encoded = try JSONSerialization.data(
                withJSONObject: parameters.foundationObject, options: [.sortedKeys, .fragmentsAllowed])
            bytes += name.utf8.count + description.utf8.count + encoded.count
            guard bytes <= 131_072 else { throw Failure.capacity }
            let node = try SchemaSubset.parse(parameters)
            tools.append(Transcript.ToolDefinition(
                name: name, description: description,
                parameters: try SchemaSubset.schema(for: node, name: name)))
        }
        return Prepared(entries: [.instructions(Transcript.Instructions(
            segments: [.text(Transcript.TextSegment(content: instructions))],
            toolDefinitions: tools))], inputBytes: bytes)
    }

    // This pending prompt is counted, not installed in the session. respond()
    // adds it exactly once after the metadata-only transcript seeds the session.
    static func preflightEntries(_ entries: [Transcript.Entry], prompt: String,
                                 options: GenerationOptions) -> [Transcript.Entry] {
        entries + [.prompt(Transcript.Prompt(
            segments: [.text(Transcript.TextSegment(content: prompt))], options: options))]
    }
}
