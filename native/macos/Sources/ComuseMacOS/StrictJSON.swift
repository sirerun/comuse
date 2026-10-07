import Foundation

private struct JSONStructureScanner {
    let bytes: [UInt8]
    var index = 0

    mutating func scan() throws {
        try value(depth: 0)
        whitespace()
        guard index == bytes.count else { throw ProbeFailure(code: "invalid_request") }
    }

    private mutating func value(depth: Int) throws {
        guard depth <= 64 else { throw ProbeFailure(code: "invalid_request") }
        whitespace()
        guard index < bytes.count else { throw ProbeFailure(code: "invalid_request") }
        switch bytes[index] {
        case 0x7b: try object(depth: depth + 1)
        case 0x5b: try array(depth: depth + 1)
        case 0x22: _ = try string()
        default:
            let start = index
            while index < bytes.count && ![0x20, 0x09, 0x0a, 0x0d, 0x2c, 0x5d, 0x7d].contains(bytes[index]) { index += 1 }
            guard index > start else { throw ProbeFailure(code: "invalid_request") }
        }
    }

    private mutating func object(depth: Int) throws {
        index += 1; whitespace()
        if take(0x7d) { return }
        var keys = Set<String>()
        while true {
            whitespace()
            let key = try string()
            guard keys.insert(key).inserted else { throw ProbeFailure(code: "invalid_request") }
            whitespace(); guard take(0x3a) else { throw ProbeFailure(code: "invalid_request") }
            try value(depth: depth)
            whitespace()
            if take(0x7d) { return }
            guard take(0x2c) else { throw ProbeFailure(code: "invalid_request") }
        }
    }

    private mutating func array(depth: Int) throws {
        index += 1; whitespace()
        if take(0x5d) { return }
        while true {
            try value(depth: depth)
            whitespace()
            if take(0x5d) { return }
            guard take(0x2c) else { throw ProbeFailure(code: "invalid_request") }
        }
    }

    private mutating func string() throws -> String {
        whitespace()
        let start = index
        guard take(0x22) else { throw ProbeFailure(code: "invalid_request") }
        var escaped = false
        while index < bytes.count {
            let byte = bytes[index]; index += 1
            if escaped { escaped = false; continue }
            if byte == 0x5c { escaped = true; continue }
            if byte == 0x22 {
                let data = Data(bytes[start..<index])
                guard let result = try? JSONDecoder().decode(String.self, from: data) else { throw ProbeFailure(code: "invalid_request") }
                return result
            }
        }
        throw ProbeFailure(code: "invalid_request")
    }

    private mutating func whitespace() {
        while index < bytes.count && [0x20, 0x09, 0x0a, 0x0d].contains(bytes[index]) { index += 1 }
    }

    private mutating func take(_ byte: UInt8) -> Bool {
        guard index < bytes.count, bytes[index] == byte else { return false }
        index += 1; return true
    }
}

private func strictJSONObject(_ data: Data) throws -> [String: Any] {
    var scanner = JSONStructureScanner(bytes: Array(data))
    try scanner.scan()
    guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
        throw ProbeFailure(code: "invalid_request")
    }
    return object
}

private func strictObjectShape(_ object: [String: Any], required: Set<String>, optional: Set<String> = []) -> Bool {
    let keys = Set(object.keys)
    return required.isSubset(of: keys) && keys.isSubset(of: required.union(optional)) &&
        object.values.allSatisfy { !($0 is NSNull) }
}

func decodeNativeRequest(_ data: Data) throws -> NativeRequest {
    guard !data.isEmpty, data.count <= comuseMaximumRequestBytes else { throw ProbeFailure(code: "invalid_request") }
    let object = try strictJSONObject(data)
    guard strictObjectShape(object, required: ["schema_version", "request_id", "operation"],
                            optional: ["window_ref", "element_ref", "state_id", "budget", "action"]),
          let operation = object["operation"] as? String else { throw ProbeFailure(code: "invalid_request") }
    let base: Set<String> = ["schema_version", "request_id", "operation"]
    let shape: Set<String>
    let required: Set<String>
    switch operation {
    case "doctor": shape = base; required = base
    case "windows": shape = base.union(["budget"]); required = base.union(["budget"])
    case "observe": shape = base.union(["window_ref", "budget"]); required = base.union(["window_ref", "budget"])
    case "read_element":
        shape = base.union(["window_ref", "element_ref", "state_id", "budget"])
        required = base.union(["window_ref", "element_ref", "state_id", "budget"])
    case "execute":
        shape = base.union(["window_ref", "element_ref", "state_id", "action"])
        required = base.union(["action"])
    default: throw ProbeFailure(code: "invalid_request")
    }
    let keys = Set(object.keys)
    guard keys.isSubset(of: shape), required.isSubset(of: keys) else {
        throw ProbeFailure(code: "invalid_request")
    }
    if let budget = object["budget"] {
        guard let values = budget as? [String: Any], strictObjectShape(values,
            required: ["max_depth", "max_nodes", "max_bytes", "timeout"]) else { throw ProbeFailure(code: "invalid_request") }
    }
    if let action = object["action"] {
        guard let values = action as? [String: Any] else { throw ProbeFailure(code: "invalid_request") }
        try validateNativeActionObject(values)
    }
    return try JSONDecoder().decode(NativeRequest.self, from: data)
}

private func validateNativeActionObject(_ object: [String: Any]) throws {
    let common: Set<String> = ["id", "window_ref", "element_ref", "state_id", "kind"]
    guard common.isSubset(of: Set(object.keys)), let kind = object["kind"] as? String else {
        throw ProbeFailure(code: "invalid_request")
    }
    let fields: Set<String>
    switch kind {
    case "press", "pick", "focus", "focus_window": fields = []
    case "replace", "insert": fields = ["text"]
    case "type_text": fields = ["text", "delay_ms"]
    case "scroll": fields = ["direction", "amount"]
    case "click": fields = ["x", "y", "button", "count", "hold_ms"]
    case "press_key": fields = ["keys", "hold_ms"]
    case "coordinate_scroll": fields = ["x", "y", "dx", "dy"]
    case "drag": fields = ["x", "y", "end_x", "end_y", "steps", "duration_ms"]
    default: throw ProbeFailure(code: "invalid_request")
    }
    // The Darwin transport emits every required field, including zero values
    // and empty replacement text; omission must not fabricate those facts.
    guard strictObjectShape(object, required: common.union(fields)) else {
        throw ProbeFailure(code: "invalid_request")
    }
}

func decodeNativeConfig(_ data: Data) throws -> NativeConfig {
    guard !data.isEmpty, data.count <= comuseMaximumRequestBytes else { throw ProbeFailure(code: "invalid_request") }
    let object = try strictJSONObject(data)
    guard strictObjectShape(object, required: ["schema_version", "scope", "allow_values"]),
          let scope = object["scope"] as? [String: Any],
          strictObjectShape(scope, required: ["processes", "expires_at_unix_milli"]),
          let processes = scope["processes"] as? [[String: Any]],
          processes.allSatisfy({ strictObjectShape($0, required: ["pid", "bundle_id", "launch_id"]) }) else {
        throw ProbeFailure(code: "invalid_request")
    }
    return try JSONDecoder().decode(NativeConfig.self, from: data)
}
