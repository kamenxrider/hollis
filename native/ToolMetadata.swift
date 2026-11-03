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

    static func prepare(_ root: JSONValue, enabled: Bool, hybridEnabled: Bool = false) throws -> Prepared {
        guard let request = root.fields else { throw Failure.invalid }
        let hasMetadata = request["tool_instructions"] != nil || request["tool_definitions"] != nil
            || request["raw_tool_definitions"] != nil || request["hybrid_tools"] != nil
        guard enabled == hasMetadata else { throw Failure.invalid }
        let hybrid: Bool
        switch request["hybrid_tools"] {
        case .boolean(true)?: hybrid = true
        case nil: hybrid = false
        default: throw Failure.invalid
        }
        guard hybrid == hybridEnabled else { throw Failure.invalid }
        let promptBytes = request["prompt"]?.text?.utf8.count ?? 0
        if !enabled { return Prepared(entries: [], inputBytes: promptBytes) }
        // Unknown top-level members retain protocol 3's forward-compatible
        // ignore rule; recognized incompatible inputs are never dropped.
        guard request["schema"] == nil, request["images"] == nil,
              request["source"] == nil, request["pages"] == nil, request["media"] == nil,
              request["protocol"]?.number == 3,
              request["operation"]?.text == "complete",
              request["tool_calling"]?.text == "allowed",
              let instructions = request["tool_instructions"]?.text, !instructions.isEmpty else { throw Failure.invalid }
        let definitions = try array(request["tool_definitions"])
        let rawDefinitions = try array(request["raw_tool_definitions"])
        let count = definitions.count + rawDefinitions.count
        guard count > 0, count <= (hybrid ? 128 : 32),
              hybrid || request["raw_tool_definitions"] == nil else { throw Failure.invalid }
        var bytes = promptBytes + instructions.utf8.count
        guard bytes <= 131_072 else { throw Failure.capacity }
        var names = Set<String>()
        var tools: [Transcript.ToolDefinition] = []
        var callers = 0
        for definition in definitions {
            guard let fields = definition.fields,
                  Set(fields.keys) == Set(["name", "description", "parameters"]
                    + (fields["source"] == nil ? [] : ["source"])),
                  let name = fields["name"]?.text,
                  !name.isEmpty, name.utf8.count <= 64,
                  name.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0)
                      || (48...57).contains($0) || $0 == 95 || $0 == 45 }),
                  names.insert(name).inserted,
                  let description = fields["description"]?.text,
                  let parameters = fields["parameters"] else { throw Failure.invalid }
            if fields["source"] == nil {
                guard !description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw Failure.invalid }
                callers += 1
            }
            else {
                guard hybrid, fields["source"]?.text == "mcp" else { throw Failure.invalid }
            }
            guard callers <= 32 else { throw Failure.invalid }
            let encoded = try JSONSerialization.data(
                withJSONObject: parameters.foundationObject, options: [.sortedKeys, .fragmentsAllowed])
            bytes += name.utf8.count + description.utf8.count + encoded.count
            guard bytes <= 131_072 else { throw Failure.capacity }
            let node = try SchemaSubset.parse(parameters)
            tools.append(Transcript.ToolDefinition(
                name: name, description: description,
                parameters: try SchemaSubset.schema(for: node, name: name)))
        }
        var rawBytes = 0
        var rawInstructions = ""
        for definition in rawDefinitions {
            guard hybrid, let fields = definition.fields,
                  Set(fields.keys) == Set(["name", "description", "schema", "source"]),
                  fields["source"]?.text == "mcp",
                  let name = fields["name"]?.text, validName(name),
                  names.insert(name).inserted,
                  let description = fields["description"]?.text,
                  let schema = fields["schema"]?.text, schema.utf8.count <= 2048,
                  let data = schema.data(using: .utf8) else { throw Failure.invalid }
            // Keep the original compact schema text. It is not projected into
            // an SDK schema, nor interpreted as trusted instructions.
            try StrictJSON.scan(data, subject: "tool schema")
            guard schema.trimmingCharacters(in: .whitespacesAndNewlines).first == "{",
                  schema.trimmingCharacters(in: .whitespacesAndNewlines).last == "}" else { throw Failure.invalid }
            rawBytes += data.count
            guard rawBytes <= 16_384 else { throw Failure.invalid }
            let text = "\nBEGIN UNTRUSTED SERVER-VALIDATED TOOL\nName: " + name
                + "\nDescription (JSON string): " + (try quoted(description))
                + "\nSchema (JSON string): " + (try quoted(schema))
                + "\nEND UNTRUSTED SERVER-VALIDATED TOOL"
            bytes += text.utf8.count
            guard bytes <= 131_072 else { throw Failure.capacity }
            rawInstructions += text
        }
        return Prepared(entries: [.instructions(Transcript.Instructions(
            segments: [.text(Transcript.TextSegment(content: instructions + rawInstructions))],
            toolDefinitions: tools))], inputBytes: bytes)
    }

    private static func array(_ value: JSONValue?) throws -> [JSONValue] {
        guard let value else { return [] }
        guard let elements = value.elements else { throw Failure.invalid }
        return elements
    }

    private static func validName(_ name: String) -> Bool {
        !name.isEmpty && name.utf8.count <= 64
            && name.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0)
                || (48...57).contains($0) || $0 == 95 || $0 == 45 })
    }

    private static func quoted(_ text: String) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: text,
            options: [.fragmentsAllowed, .withoutEscapingSlashes])
        return String(decoding: data, as: UTF8.self)
            .replacingOccurrences(of: "\u{2028}", with: "\\u2028")
            .replacingOccurrences(of: "\u{2029}", with: "\\u2029")
    }

    // This pending prompt is counted, not installed in the session. respond()
    // adds it exactly once after the metadata-only transcript seeds the session.
    static func preflightEntries(_ entries: [Transcript.Entry], prompt: String,
                                 options: GenerationOptions) -> [Transcript.Entry] {
        entries + [.prompt(Transcript.Prompt(
            segments: [.text(Transcript.TextSegment(content: prompt))], options: options))]
    }
}
