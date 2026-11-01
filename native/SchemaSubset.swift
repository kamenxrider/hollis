// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
import Foundation
import FoundationModels

/// A generic JSON tree used to carry the requested schema and the generated
/// object. Neither is reinterpreted through application types: the subset is
/// validated here and the object is validated here before it is emitted.
indirect enum JSONValue: Decodable {
    case object([String: JSONValue])
    case array([JSONValue])
    case string(String)
    case number(Double)
    case boolean(Bool)
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null; return }
        if let value = try? container.decode(Bool.self) { self = .boolean(value); return }
        if let value = try? container.decode(Double.self) { self = .number(value); return }
        if let value = try? container.decode(String.self) { self = .string(value); return }
        if let value = try? container.decode([JSONValue].self) { self = .array(value); return }
        if let value = try? container.decode([String: JSONValue].self) { self = .object(value); return }
        throw DecodingError.dataCorruptedError(
            in: container, debugDescription: "unsupported JSON value")
    }

    var fields: [String: JSONValue]? { if case .object(let value) = self { return value }; return nil }
    var elements: [JSONValue]? { if case .array(let value) = self { return value }; return nil }
    var text: String? { if case .string(let value) = self { return value }; return nil }
    var number: Double? { if case .number(let value) = self { return value }; return nil }

    /// Re-encode for emission. Booleans stay booleans for JSONSerialization.
    var foundationObject: Any {
        switch self {
        case .object(let value): return value.mapValues { $0.foundationObject }
        case .array(let value): return value.map { $0.foundationObject }
        case .string(let value): return value
        case .number(let value): return value
        case .boolean(let value): return value
        case .null: return NSNull()
        }
    }
}

/// The supported JSON Schema subset, enforced model-free. Parsing never
/// touches a model or session; `schema(for:)` only builds SDK schema values.
///
/// The bounds are the design's: root object, depth 3 (the root counts as
/// depth 1, and every child schema node adds a level), 32 declared properties
/// across the whole tree, 64 KiB of normalized encoding, array bounds no
/// greater than 100, and at most 8 schema nodes on one path counting the root.
/// Anything outside the subset is rejected, never guessed.
enum SchemaSubset {
    static let maxEncodedBytes = 64 * 1024
    static let maxDepth = 3
    static let maxProperties = 32
    static let maxItems = 100
    /// Total nodes on one path, root included. Object depth bounds objects
    /// more tightly; this bound also covers array chains, whose `items` can be
    /// another array. Kept equal to the Go consumer's SchemaMaxNesting so both
    /// sides reject the same documents with the same pointer.
    static let maxNesting = 8
    static let rootName = "HollisResponse"

    indirect enum Node {
        case string([String]?)
        case integer(Int?, Int?)
        case number(Double?, Double?)
        case boolean
        case object([(name: String, node: Node, required: Bool)])
        case array(Node, Int?, Int?)
    }

    /// A rejection carries only a JSON pointer and a fixed reason: schema
    /// values are caller input and never appear in emitted diagnostics.
    struct Failure: Error {
        let pointer: String
        let reason: String
    }

    private struct Budget { var properties = 0 }

    static func parse(_ value: JSONValue) throws -> Node {
        // fragmentsAllowed: a bare string or number schema is rejected below,
        // and re-encoding it must not raise. Only objects are valid roots.
        guard let data = try? JSONSerialization.data(
            withJSONObject: value.foundationObject, options: [.sortedKeys, .fragmentsAllowed]),
            data.count <= maxEncodedBytes else {
            throw Failure(pointer: "", reason: "schema is larger than 64 KiB")
        }
        var budget = Budget()
        guard value.fields?["type"]?.text == "object" else {
            throw Failure(pointer: "", reason: "root schema must be an object")
        }
        let node = try parseNode(value, path: "", objectDepth: 1, nesting: 1, budget: &budget)
        guard case .object = node else {
            throw Failure(pointer: "", reason: "root schema must be an object")
        }
        return node
    }

    private static func escape(_ name: String) -> String {
        name.replacingOccurrences(of: "~", with: "~0").replacingOccurrences(of: "/", with: "~1")
    }

    private static func pointer(_ path: String, _ component: String) -> String {
        path + "/" + escape(component)
    }

    private static func parseNode(
        _ value: JSONValue, path: String, objectDepth: Int, nesting: Int, budget: inout Budget
    ) throws -> Node {
        // Checked first, for every node kind. Object depth alone leaves array
        // chains unbounded, and a deeply nested document fails in this file's
        // decoder instead of as a clean rejection.
        guard nesting <= maxNesting else {
            throw Failure(pointer: path, reason: "schema nesting exceeds depth 8")
        }
        guard let fields = value.fields else {
            throw Failure(pointer: path, reason: "schema node must be an object")
        }
        guard let kind = fields["type"]?.text,
              ["object", "array", "string", "integer", "number", "boolean"].contains(kind) else {
            throw Failure(pointer: pointer(path, "type"), reason: "unsupported schema type")
        }
        let allowed: Set<String>
        switch kind {
        case "object": allowed = ["type", "properties", "required", "additionalProperties"]
        case "array": allowed = ["type", "items", "minItems", "maxItems"]
        case "string": allowed = ["type", "enum"]
        case "integer", "number": allowed = ["type", "minimum", "maximum"]
        default: allowed = ["type"]
        }
        for key in fields.keys where !allowed.contains(key) {
            let reason = key == "enum" ? "enums must be strings" : "unsupported schema keyword"
            throw Failure(pointer: pointer(path, key), reason: reason)
        }
        switch kind {
        case "object":
            guard objectDepth <= maxDepth else {
                throw Failure(pointer: path, reason: "object nesting exceeds depth 3")
            }
            guard case .boolean(false)? = fields["additionalProperties"] else {
                throw Failure(pointer: pointer(path, "additionalProperties"),
                              reason: "additionalProperties:false is required")
            }
            var declared: [String: SchemaSubset.Node] = [:]
            var order: [String] = []
            if let rawProperties = fields["properties"] {
                guard let propertyFields = rawProperties.fields else {
                    throw Failure(pointer: pointer(path, "properties"),
                                  reason: "properties must be an object")
                }
                for name in propertyFields.keys.sorted() {
                    guard !name.isEmpty else {
                        throw Failure(pointer: pointer(path, "properties"),
                                      reason: "property names must not be empty")
                    }
                    // Byte-distinct names that are canonically equivalent are
                    // one name to Foundation and two to Go. Printable ASCII
                    // removes the difference: no two ASCII strings are
                    // canonically equivalent unless they are identical, and no
                    // non-ASCII string is canonically equivalent to an ASCII
                    // one.
                    guard printableASCII(name) else {
                        throw Failure(pointer: pointer(path, "properties"),
                                      reason: "property names must be printable ASCII")
                    }
                    budget.properties += 1
                    guard budget.properties <= maxProperties else {
                        throw Failure(pointer: pointer(path, "properties"),
                                      reason: "schema declares more than 32 properties")
                    }
                    declared[name] = try parseNode(
                        propertyFields[name]!, path: pointer(pointer(path, "properties"), name),
                        objectDepth: objectDepth + 1, nesting: nesting + 1, budget: &budget)
                    order.append(name)
                }
            }
            var required: Set<String> = []
            if let rawRequired = fields["required"] {
                guard let names = rawRequired.elements else {
                    throw Failure(pointer: pointer(path, "required"),
                                  reason: "required must be an array of names")
                }
                for (index, name) in names.enumerated() {
                    guard let key = name.text else {
                        throw Failure(pointer: path + "/required/\(index)",
                                      reason: "required entries must be strings")
                    }
                    guard printableASCII(key) else {
                        throw Failure(pointer: path + "/required/\(index)",
                                      reason: "required names must be printable ASCII")
                    }
                    guard required.insert(key).inserted else {
                        throw Failure(pointer: path + "/required/\(index)",
                                      reason: "required names must be unique")
                    }
                    guard declared[key] != nil else {
                        throw Failure(pointer: pointer(path, "required"),
                                      reason: "required names a property that is not declared")
                    }
                }
            }
            return .object(order.map { (name: $0, node: declared[$0]!, required: required.contains($0)) })
        case "array":
            guard let rawItems = fields["items"] else {
                throw Failure(pointer: pointer(path, "items"), reason: "array items are required")
            }
            // Arrays add no object level: an object inside an array is nested
            // one level below the object that declares the array property.
            let items = try parseNode(rawItems, path: pointer(path, "items"),
                                      objectDepth: objectDepth, nesting: nesting + 1, budget: &budget)
            let minimum = try itemBound(fields["minItems"], path: pointer(path, "minItems"))
            let maximum = try itemBound(fields["maxItems"], path: pointer(path, "maxItems"))
            if let low = minimum, let high = maximum, low > high {
                throw Failure(pointer: path, reason: "maxItems is smaller than minItems")
            }
            return .array(items, minimum, maximum)
        case "string":
            var choices: [String]?
            if let rawEnum = fields["enum"] {
                guard let values = rawEnum.elements, !values.isEmpty else {
                    throw Failure(pointer: pointer(path, "enum"), reason: "enum must not be empty")
                }
                var seen: Set<String> = []
                choices = try values.map { value in
                    guard let choice = value.text else {
                        throw Failure(pointer: pointer(path, "enum"),
                                      reason: "enum choices must be strings")
                    }
                    // Foundation compares strings by canonical equivalence, so
                    // a precomposed choice would accept a decomposed value the
                    // Go consumer rejects. Printable ASCII choices make the two
                    // comparisons agree.
                    guard printableASCII(choice) else {
                        throw Failure(pointer: pointer(path, "enum"),
                                      reason: "enum choices must be printable ASCII")
                    }
                    guard seen.insert(choice).inserted else {
                        throw Failure(pointer: pointer(path, "enum"),
                                      reason: "enum choices must be unique")
                    }
                    return choice
                }
            }
            return .string(choices)
        case "integer":
            let low = try integerBound(fields["minimum"], path: pointer(path, "minimum"))
            let high = try integerBound(fields["maximum"], path: pointer(path, "maximum"))
            if let minimum = low, let maximum = high, minimum > maximum {
                throw Failure(pointer: path, reason: "maximum is smaller than minimum")
            }
            return .integer(low, high)
        case "number":
            let low = try numberBound(fields["minimum"], path: pointer(path, "minimum"))
            let high = try numberBound(fields["maximum"], path: pointer(path, "maximum"))
            if let minimum = low, let maximum = high, minimum > maximum {
                throw Failure(pointer: path, reason: "maximum is smaller than minimum")
            }
            return .number(low, high)
        default:
            return .boolean
        }
    }

    /// Printable ASCII only: byte identity and canonical equivalence are then
    /// the same relation for schema-authoring names and enum choices. Control
    /// characters are excluded too, which keeps a name out of a terminal escape
    /// sequence.
    static func printableASCII(_ name: String) -> Bool {
        name.unicodeScalars.allSatisfy { $0.isASCII && $0.value >= 0x20 && $0.value <= 0x7e }
    }

    private static func itemBound(_ value: JSONValue?, path: String) throws -> Int? {
        guard let value else { return nil }
        guard let number = value.number, number.rounded() == number,
              number >= 0, number <= Double(maxItems) else {
            throw Failure(pointer: path, reason: "array bounds must be whole numbers from 0 to 100")
        }
        return Int(number)
    }

    private static func integerBound(_ value: JSONValue?, path: String) throws -> Int? {
        guard let value else { return nil }
        guard let number = value.number, number.rounded() == number,
              number.magnitude <= 9_007_199_254_740_992 else {
            throw Failure(pointer: path, reason: "integer bounds must be whole numbers")
        }
        return Int(number)
    }

    private static func numberBound(_ value: JSONValue?, path: String) throws -> Double? {
        guard let value else { return nil }
        guard let number = value.number, number.isFinite else {
            throw Failure(pointer: path, reason: "number bounds must be finite numbers")
        }
        return number
    }

    /// Assemble the SDK schema. Inline definitions are accepted by
    /// GenerationSchema, so no dependency table is needed for this subset
    /// (references and recursive schemas are rejected before this point).
    static func schema(for node: Node, name: String = rootName) throws -> GenerationSchema {
        let root = try dynamic(node, name: name)
        return try GenerationSchema(root: root, dependencies: [])
    }

    private static func dynamic(_ node: Node, name: String) throws -> DynamicGenerationSchema {
        switch node {
        case .boolean:
            return DynamicGenerationSchema(type: Bool.self)
        case .string(let choices):
            if let choices { return DynamicGenerationSchema(name: name, anyOf: choices) }
            return DynamicGenerationSchema(type: String.self)
        case .integer(let low, let high):
            return DynamicGenerationSchema(type: Int.self, guides: integerGuides(low, high))
        case .number(let low, let high):
            return DynamicGenerationSchema(type: Double.self, guides: numberGuides(low, high))
        case .array(let item, let low, let high):
            return DynamicGenerationSchema(
                arrayOf: try dynamic(item, name: name + "Item"),
                minimumElements: low, maximumElements: high)
        case .object(let properties):
            return DynamicGenerationSchema(name: name, properties: try properties.map { property in
                DynamicGenerationSchema.Property(
                    name: property.name,
                    schema: try dynamic(property.node, name: name + "." + property.name),
                    isOptional: !property.required)
            })
        }
    }

    private static func integerGuides(_ low: Int?, _ high: Int?) -> [GenerationGuide<Int>] {
        switch (low, high) {
        case let (low?, high?): return [.range(low...high)]
        case let (low?, nil): return [.minimum(low)]
        case let (nil, high?): return [.maximum(high)]
        case (nil, nil): return []
        }
    }

    private static func numberGuides(_ low: Double?, _ high: Double?) -> [GenerationGuide<Double>] {
        switch (low, high) {
        case let (low?, high?): return [.range(low...high)]
        case let (low?, nil): return [.minimum(low)]
        case let (nil, high?): return [.maximum(high)]
        case (nil, nil): return []
        }
    }

    /// Independent validation of generated content against the same subset.
    /// Returns the first offending pointer, or nil when the object conforms.
    static func validate(_ value: JSONValue, against node: Node, path: String = "") -> String? {
        switch node {
        case .object(let properties):
            guard let fields = value.fields else { return path.isEmpty ? "/" : path }
            var declared: [String: (node: Node, required: Bool)] = [:]
            for property in properties { declared[property.name] = (property.node, property.required) }
            for key in fields.keys where declared[key] == nil {
                return pointer(path, key)
            }
            for property in properties where property.required && fields[property.name] == nil {
                return pointer(path, property.name)
            }
            for property in properties {
                if let child = fields[property.name],
                   let bad = validate(child, against: property.node,
                                      path: pointer(path, property.name)) {
                    return bad
                }
            }
            return nil
        case .array(let item, let low, let high):
            guard let values = value.elements else { return path.isEmpty ? "/" : path }
            if let low, values.count < low { return path.isEmpty ? "/" : path }
            if let high, values.count > high { return path.isEmpty ? "/" : path }
            for (index, child) in values.enumerated() {
                if let bad = validate(child, against: item, path: path + "/\(index)") { return bad }
            }
            return nil
        case .string(let choices):
            guard let text = value.text else { return path.isEmpty ? "/" : path }
            if let choices, !choices.contains(text) { return path.isEmpty ? "/" : path }
            return nil
        case .integer(let low, let high):
            guard let number = value.number, number.rounded() == number else {
                return path.isEmpty ? "/" : path
            }
            if let low, number < Double(low) { return path.isEmpty ? "/" : path }
            if let high, number > Double(high) { return path.isEmpty ? "/" : path }
            return nil
        case .number(let low, let high):
            guard let number = value.number, number.isFinite else { return path.isEmpty ? "/" : path }
            if let low, number < low { return path.isEmpty ? "/" : path }
            if let high, number > high { return path.isEmpty ? "/" : path }
            return nil
        case .boolean:
            if case .boolean = value { return nil }
            return path.isEmpty ? "/" : path
        }
    }
}
