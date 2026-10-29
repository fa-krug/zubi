import Foundation

/// A strict JSON Schema validator for the subset openapi.json uses. A keyword it does not know is an
/// error rather than ignored, so the spec cannot start relying on something this never checks.
/// A class so one instance can collect coverage across a test's calls; those calls run one at a time.
final class SchemaValidator: @unchecked Sendable {
    typealias Schema = [String: Any]

    private static let checked: Set<String> = [
        "type", "properties", "required", "additionalProperties", "items", "$ref", "enum", "const",
        "pattern", "minimum", "maximum",
    ]
    /// Annotations: they document, they do not constrain.
    private static let annotations: Set<String> = ["description", "format", "default", "example", "title"]

    let components: [String: Schema]
    /// Per component, the optional properties seen present and seen absent.
    private(set) var present: [String: Set<String>] = [:]
    private(set) var absent: [String: Set<String>] = [:]

    init(spec: [String: Any]) {
        let schemas = (spec["components"] as? [String: Any])?["schemas"] as? [String: Any] ?? [:]
        components = schemas.compactMapValues { $0 as? Schema }
    }

    /// Every way `value` breaks `schema`, as "path: problem". Empty when it validates.
    func errors(_ value: Any, against schema: Schema, at path: String = "$") -> [String] {
        validate(value, schema, path, component: nil)
    }

    private func validate(_ value: Any, _ schema: Schema, _ path: String, component: String?) -> [String] {
        var errors = schema.keys
            .filter { !Self.checked.contains($0) && !Self.annotations.contains($0) }
            .map { "\(path): unsupported schema keyword '\($0)'" }

        if let ref = schema["$ref"] {
            let prefix = "#/components/schemas/"
            guard let ref = ref as? String, ref.hasPrefix(prefix),
                  let target = components[String(ref.dropFirst(prefix.count))]
            else { return errors + ["\(path): unresolvable $ref \(ref)"] }
            let name = String(ref.dropFirst(prefix.count))
            errors += validate(value, target, path, component: name)
        }

        if let type = schema["type"] {
            let types = (type as? [String]) ?? [(type as? String) ?? "?"]
            if !types.contains(where: { Self.matches(value, $0) }) {
                return errors + ["\(path): expected \(types.joined(separator: " or ")), got \(Self.describe(value))"]
            }
        }

        if let options = schema["enum"] as? [Any], !options.contains(where: { Self.equal($0, value) }) {
            errors.append("\(path): \(Self.describe(value)) is not one of \(options)")
        }
        if let constant = schema["const"], !Self.equal(constant, value) {
            errors.append("\(path): \(Self.describe(value)) is not \(constant)")
        }
        if let pattern = schema["pattern"] as? String, let string = value as? String {
            if string.range(of: pattern, options: .regularExpression) == nil {
                errors.append("\(path): '\(string)' does not match \(pattern)")
            }
        }
        if let number = Self.number(value) {
            if let minimum = schema["minimum"].flatMap(Self.number), number < minimum {
                errors.append("\(path): \(number) is below \(minimum)")
            }
            if let maximum = schema["maximum"].flatMap(Self.number), number > maximum {
                errors.append("\(path): \(number) is above \(maximum)")
            }
        }

        if let object = value as? [String: Any] {
            let properties = schema["properties"] as? [String: Any] ?? [:]
            let required = Set(schema["required"] as? [String] ?? [])
            for key in required where object[key] == nil {
                errors.append("\(path): missing required '\(key)'")
            }
            if let additional = schema["additionalProperties"] {
                guard additional as? Bool == false else {
                    return errors + ["\(path): only additionalProperties: false is supported"]
                }
                for key in object.keys where properties[key] == nil {
                    errors.append("\(path): undocumented property '\(key)'")
                }
            }
            for (key, propertySchema) in properties {
                guard let propertySchema = propertySchema as? Schema else {
                    errors.append("\(path).\(key): schema is not an object")
                    continue
                }
                if let component, !required.contains(key) {
                    if object[key] == nil { absent[component, default: []].insert(key) }
                    else { present[component, default: []].insert(key) }
                }
                if let property = object[key] {
                    errors += validate(property, propertySchema, "\(path).\(key)", component: nil)
                }
            }
        }

        if let array = value as? [Any], let items = schema["items"] {
            guard let items = items as? Schema else { return errors + ["\(path): items is not a schema"] }
            for (index, element) in array.enumerated() {
                errors += validate(element, items, "\(path)[\(index)]", component: nil)
            }
        }
        return errors
    }

    // MARK: JSONSerialization's types

    private static func isBool(_ value: Any) -> Bool {
        guard let number = value as? NSNumber else { return false }
        return CFGetTypeID(number) == CFBooleanGetTypeID()
    }

    private static func number(_ value: Any) -> Double? {
        guard let number = value as? NSNumber, !isBool(number) else { return nil }
        return number.doubleValue
    }

    private static func matches(_ value: Any, _ type: String) -> Bool {
        switch type {
        case "object": value is [String: Any]
        case "array": value is [Any]
        case "string": value is String
        case "boolean": isBool(value)
        case "number": number(value) != nil
        case "integer": number(value).map { $0 == $0.rounded() } ?? false
        case "null": value is NSNull
        default: false
        }
    }

    private static func equal(_ lhs: Any, _ rhs: Any) -> Bool {
        if isBool(lhs) || isBool(rhs) { return isBool(lhs) && isBool(rhs) && (lhs as? Bool) == (rhs as? Bool) }
        if let a = number(lhs), let b = number(rhs) { return a == b }
        if let a = lhs as? String, let b = rhs as? String { return a == b }
        return lhs is NSNull && rhs is NSNull
    }

    private static func describe(_ value: Any) -> String {
        if value is NSNull { return "null" }
        if isBool(value) { return "boolean \(value)" }
        if value is [String: Any] { return "an object" }
        if value is [Any] { return "an array" }
        if let string = value as? String { return "string '\(string)'" }
        return "\(value)"
    }
}
