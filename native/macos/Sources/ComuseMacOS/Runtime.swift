import AppKit
import ApplicationServices
import ComuseABI
import CoreFoundation
import Darwin
import Foundation

let comuseMaximumRequestBytes = 32 * 1024
let comuseMaximumResponseBytes = 64 * 1024
let comuseMaximumScopeProcesses = 32

typealias Completion = @convention(c) (UInt64, UInt64, Int32, UnsafePointer<UInt8>?, Int) -> Void

struct NativeProcess: Codable, Sendable, Equatable {
    var pid: Int32
    var bundleID: String
    var launchID: String
    enum CodingKeys: String, CodingKey { case pid; case bundleID = "bundle_id"; case launchID = "launch_id" }
}

struct NativeScope: Codable, Sendable {
    var processes: [NativeProcess]
    var expiresAtUnixMilli: Int64
    enum CodingKeys: String, CodingKey { case processes; case expiresAtUnixMilli = "expires_at_unix_milli" }
}

struct NativeConfig: Decodable, Sendable {
    var schemaVersion: Int
    var scope: NativeScope
    var allowValues: Bool
    enum CodingKeys: String, CodingKey { case schemaVersion = "schema_version"; case scope; case allowValues = "allow_values" }
}

struct NativeBudget: Decodable, Sendable {
    var maxDepth: Int
    var maxNodes: Int
    var maxBytes: Int
    var timeoutNanoseconds: Int64
    enum CodingKeys: String, CodingKey {
        case maxDepth = "max_depth"; case maxNodes = "max_nodes"; case maxBytes = "max_bytes"
        case timeoutNanoseconds = "timeout"
    }
}

struct NativeAction: Decodable, Sendable {
    var id: String
    var windowRef: String
    var elementRef: String
    var stateID: String
    var kind: String
    var text: String
    enum CodingKeys: String, CodingKey {
        case id; case windowRef = "window_ref"; case elementRef = "element_ref"
        case stateID = "state_id"; case kind; case text
    }

    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(String.self, forKey: .id)
        windowRef = try values.decode(String.self, forKey: .windowRef)
        elementRef = try values.decode(String.self, forKey: .elementRef)
        stateID = try values.decode(String.self, forKey: .stateID)
        kind = try values.decode(String.self, forKey: .kind)
        text = try values.decodeIfPresent(String.self, forKey: .text) ?? ""
    }
}

struct NativeRequest: Decodable, Sendable {
    var schemaVersion: Int
    var requestID: String
    var operation: String
    var windowRef: String?
    var elementRef: String?
    var stateID: String?
    var budget: NativeBudget?
    var action: NativeAction?
    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"; case requestID = "request_id"; case operation
        case windowRef = "window_ref"; case elementRef = "element_ref"; case stateID = "state_id"
        case budget; case action
    }
}

struct NativeEnvelope: Encodable, Sendable {
    var schemaVersion: Int = 1
    var requestID: String
    var status: String
    var result: Data?
    var error: String?

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"; case requestID = "request_id"; case status; case result; case error
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(schemaVersion, forKey: .schemaVersion)
        try container.encode(requestID, forKey: .requestID)
        try container.encode(status, forKey: .status)
        if let error { try container.encode(error, forKey: .error) }
        else if let result {
            let object = try JSONSerialization.jsonObject(with: result, options: [.fragmentsAllowed])
            try container.encode(AnyCodable(object), forKey: .result)
        }
    }
}

private struct AnyCodable: Encodable {
    let value: Any
    init(_ value: Any) { self.value = value }
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case is NSNull: try container.encodeNil()
        case let value as String: try container.encode(value)
        case let value as NSNumber:
            if CFGetTypeID(value) == CFBooleanGetTypeID() { try container.encode(value.boolValue) }
            else if CFNumberIsFloatType(value as! CFNumber) { try container.encode(value.doubleValue) }
            else { try container.encode(value.int64Value) }
        case let value as [Any]: try container.encode(value.map(AnyCodable.init))
        case let value as [String: Any]: try container.encode(value.mapValues(AnyCodable.init))
        default: throw EncodingError.invalidValue(value, .init(codingPath: encoder.codingPath, debugDescription: "unsupported JSON value"))
        }
    }
}

private let idLock = NSLock()
nonisolated(unsafe) private var nextNativeID: UInt64 = 1
nonisolated(unsafe) private var cancelledIDs = Set<UInt64>()
private let cancellationLock = NSLock()

private func allocateID() -> UInt64 {
    idLock.lock(); defer { idLock.unlock() }
    let value = nextNativeID
    nextNativeID &+= 1
    if nextNativeID == 0 { nextNativeID = 1 }
    return value
}

private func markCancelled(_ requestID: UInt64) {
    cancellationLock.lock(); cancelledIDs.insert(requestID); cancellationLock.unlock()
}

func isCancelled(_ requestID: UInt64) -> Bool {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    return cancelledIDs.contains(requestID)
}

func clearCancelled(_ requestID: UInt64) {
    cancellationLock.lock(); cancelledIDs.remove(requestID); cancellationLock.unlock()
}

@MainActor private var nativeRuntimes: [UInt64: NativeRuntime] = [:]

@MainActor
final class NativeRuntime {
    let id: UInt64
    let config: NativeConfig
    let processes: [NativeProcess]
    var requestTasks: [UInt64: Task<Void, Never>] = [:]
    var lastProjectionDigest: String?

    init(id: UInt64, config: NativeConfig, processes: [NativeProcess]) {
        self.id = id
        self.config = config
        self.processes = processes
    }

    func dispatch(requestID: UInt64, callbackID: UInt64, completion: Completion, data: Data) {
        guard requestTasks[requestID] == nil else {
            finish(callbackID: callbackID, requestID: requestID, completion: completion,
                   bytes: envelope(requestID: extractRequestID(from: data), error: error("invalid_request")))
            return
        }
        let task = Task { @MainActor [weak self] in
            guard let self else { return }
            let response = self.handle(data, nativeRequestID: requestID)
            self.finish(callbackID: callbackID, requestID: requestID, completion: completion, bytes: response)
            self.requestTasks.removeValue(forKey: requestID)
            clearCancelled(requestID)
        }
        requestTasks[requestID] = task
    }

    func cancel(_ requestID: UInt64) {
        markCancelled(requestID)
        requestTasks[requestID]?.cancel()
    }

    func close() -> Int32 {
        guard requestTasks.isEmpty else { return 5 }
        ReferenceStore.shared.remove(runtimeID: id)
        SnapshotStore.shared.remove(runtimeID: id)
        nativeRuntimes.removeValue(forKey: id)
        return 0
    }

    private func handle(_ data: Data, nativeRequestID: UInt64) -> Data {
        do {
            let decoder = JSONDecoder()
            let request = try decoder.decode(NativeRequest.self, from: data)
            guard request.schemaVersion == 1, validRequestID(request.requestID),
                  !isCancelled(nativeRequestID),
                  config.scope.expiresAtUnixMilli > Int64(Date().timeIntervalSince1970 * 1000) else {
                throw ProbeFailure(code: isCancelled(nativeRequestID) ? "cancelled" : "invalid_request")
            }
            let result: Any
            switch request.operation {
            case "doctor": result = doctor()
            case "windows": result = try windows(request, requestID: nativeRequestID)
            case "observe": result = try observe(request, requestID: nativeRequestID)
            case "read_element": result = try readElement(request, requestID: nativeRequestID)
            case "execute": result = try execute(request, requestID: nativeRequestID)
            default: throw ProbeFailure(code: "unsupported")
            }
            let encoded = try JSONSerialization.data(withJSONObject: result, options: [.fragmentsAllowed, .sortedKeys])
            guard encoded.count <= comuseMaximumResponseBytes else { throw ProbeFailure(code: "budget_exceeded") }
            return envelope(requestID: request.requestID, result: encoded)
        } catch let failure as ProbeFailure {
            return envelope(requestID: extractRequestID(from: data), error: error(failure.code))
        } catch {
            return envelope(requestID: extractRequestID(from: data), error: error("invalid_request"))
        }
    }

    private func doctor() -> [String: Any] {
        let accessibility = AXIsProcessTrusted()
        return [
            "capabilities": [
                "accessibility": accessibility,
                "input": false,
                "qualified_input": false,
                "screen_capture": false,
                "reasons": accessibility ? ["input_unqualified", "screen_capture_unavailable"] : ["accessibility_permission_denied", "input_unqualified", "screen_capture_unavailable"]
            ],
            "permissions": [
                "accessibility": accessibility ? "granted" : "denied",
                "event_posting": CGPreflightPostEventAccess() ? "granted" : "denied"
            ]
        ]
    }

    private func windows(_ request: NativeRequest, requestID: UInt64) throws -> [[String: Any]] {
        try checkPermission()
        var result: [[String: Any]] = []
        let budget = boundedBudget(request.budget)
        let deadline = ProcessInfo.processInfo.systemUptime + budget.timeout
        for process in processes {
            try checkDeadline(requestID, deadline: deadline)
            let app = try application(process)
            let applicationElement = AXUIElementCreateApplication(process.pid)
            guard AXUIElementSetMessagingTimeout(applicationElement, Float(max(0.05, min(budget.timeout, 1.0)))) == .success else {
                throw ProbeFailure(code: "backend_unavailable")
            }
            var raw: CFTypeRef?
            guard AXUIElementCopyAttributeValue(applicationElement, kAXWindowsAttribute as CFString, &raw) == .success,
                  let values = raw as? [AXUIElement] else { throw ProbeFailure(code: "backend_unavailable") }
            for window in values.prefix(128) {
                try checkDeadline(requestID, deadline: deadline)
                let ref = try retain(window, process: app.identity, windowRef: nil, kind: .window)
                let title = stringAttribute(window, kAXTitleAttribute).map { String($0.prefix(1024)) } ?? ""
                result.append(["ref": ref, "process": processJSON(app.identity), "title": title])
            }
            guard try application(process).identity == app.identity else { throw ProbeFailure(code: "element_stale") }
        }
        return result
    }

    // AX implementation is defined in Accessibility.swift.
    func observe(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        try observeWindow(request, requestID: requestID)
    }
    func readElement(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        try readScopedElement(request, requestID: requestID)
    }
    func execute(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        try executeScopedAction(request, requestID: requestID)
    }

    func finish(callbackID: UInt64, requestID: UInt64, completion: Completion, bytes: Data) {
        bytes.withUnsafeBytes { raw in
            let pointer = raw.bindMemory(to: UInt8.self).baseAddress
            completion(callbackID, requestID, 0, pointer, raw.count)
        }
    }

    private func checkPermission() throws {
        guard AXIsProcessTrusted() else { throw ProbeFailure(code: "permission_denied") }
    }

    func checkDeadline(_ requestID: UInt64, deadline: TimeInterval) throws {
        if isCancelled(requestID) { throw ProbeFailure(code: "cancelled") }
        if ProcessInfo.processInfo.systemUptime > deadline { throw ProbeFailure(code: "budget_exceeded") }
    }

    func application(_ expected: NativeProcess) throws -> (application: NSRunningApplication, identity: NativeProcess) {
        guard let app = NSRunningApplication(processIdentifier: expected.pid), !app.isTerminated,
              app.bundleIdentifier == expected.bundleID,
              let identity = processIdentity(pid: expected.pid, bundleID: expected.bundleID) else {
            throw ProbeFailure(code: "element_stale")
        }
        if !expected.launchID.isEmpty && identity.launchID != expected.launchID {
            throw ProbeFailure(code: "element_stale")
        }
        return (app, identity)
    }

    func process(for pid: Int32) throws -> NativeProcess {
        guard let expected = processes.first(where: { $0.pid == pid }) else { throw ProbeFailure(code: "policy_refused") }
        return try application(expected).identity
    }

    func matchingProcess(_ identity: NativeProcess) throws -> NativeProcess {
        guard let expected = processes.first(where: { $0.pid == identity.pid && $0.bundleID == identity.bundleID }) else {
            throw ProbeFailure(code: "policy_refused")
        }
        let current = try application(expected).identity
        guard current == identity else { throw ProbeFailure(code: "element_stale") }
        return current
    }
}

struct ProbeFailure: Error { var code: String }

private func validRequestID(_ id: String) -> Bool {
    guard !id.isEmpty, id.utf8.count <= 128 else { return false }
    return id.utf8.allSatisfy { byte in
        (48...57).contains(byte) || (65...90).contains(byte) || (97...122).contains(byte) || byte == 45 || byte == 46 || byte == 95
    }
}

func extractRequestID(from data: Data) -> String {
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let value = object["request_id"] as? String, validRequestID(value) else { return "" }
    return value
}

private func error(_ code: String) -> String { code }

private func envelope(requestID: String, result: Data? = nil, error: String? = nil) -> Data {
    let response: NativeEnvelope
    if let error {
        response = NativeEnvelope(requestID: requestID, status: "error", result: nil, error: error)
    } else if let result {
        response = NativeEnvelope(requestID: requestID, status: "ok", result: result, error: nil)
    } else {
        response = NativeEnvelope(requestID: requestID, status: "error", result: nil, error: "internal_error")
    }
    let encoder = JSONEncoder()
    return (try? encoder.encode(response)) ?? Data("{}".utf8)
}

private func processIdentity(pid: Int32, bundleID: String) -> NativeProcess? {
    var info = proc_bsdinfo()
    let length = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, Int32(MemoryLayout<proc_bsdinfo>.size))
    guard length == Int32(MemoryLayout<proc_bsdinfo>.size) else { return nil }
    let launchID = "\(info.pbi_start_tvsec).\(String(format: "%06d", info.pbi_start_tvusec))"
    return NativeProcess(pid: pid, bundleID: bundleID, launchID: launchID)
}

private func processJSON(_ process: NativeProcess) -> [String: Any] {
    ["pid": process.pid, "bundle_id": process.bundleID, "launch_id": process.launchID]
}

@_cdecl("comuse_abi_version")
public func comuseABIVersion(_ versionOut: UnsafeMutablePointer<UInt32>?) -> Int32 {
    guard let versionOut else { return 1 }
    versionOut.pointee = COMUSE_ABI_VERSION
    return 0
}

@_cdecl("comuse_runtime_open")
public func comuseRuntimeOpen(_ configBytes: UnsafePointer<UInt8>?, _ configLength: Int,
                              _ runtimeOut: UnsafeMutablePointer<UInt64>?,
                              _ resolvedScopeOut: UnsafeMutablePointer<UInt8>?,
                              _ resolvedScopeCapacity: Int,
                              _ resolvedScopeLengthOut: UnsafeMutablePointer<Int>?) -> Int32 {
    guard pthread_main_np() != 0, let configBytes, configLength > 0, configLength <= comuseMaximumRequestBytes,
          let runtimeOut, let resolvedScopeOut, let resolvedScopeLengthOut,
          resolvedScopeCapacity > 0 else { return 1 }
    return MainActor.assumeIsolated {
        do {
            let decoder = JSONDecoder()
            let config = try decoder.decode(NativeConfig.self, from: Data(bytes: configBytes, count: configLength))
            guard config.schemaVersion == 1, !config.scope.processes.isEmpty,
                  config.scope.processes.count <= comuseMaximumScopeProcesses,
                  config.scope.expiresAtUnixMilli > Int64(Date().timeIntervalSince1970 * 1000),
                  Set(config.scope.processes.map(\.pid)).count == config.scope.processes.count else { return 1 }
            var resolved: [NativeProcess] = []
            for process in config.scope.processes {
                guard process.pid > 0, !process.bundleID.isEmpty, process.bundleID.utf8.count <= 255,
                      let app = NSRunningApplication(processIdentifier: process.pid), !app.isTerminated,
                      app.bundleIdentifier == process.bundleID,
                      let current = processIdentity(pid: process.pid, bundleID: process.bundleID),
                      process.launchID.isEmpty || process.launchID == current.launchID else { return 1 }
                resolved.append(current)
            }
            let boundScope = NativeScope(processes: resolved, expiresAtUnixMilli: config.scope.expiresAtUnixMilli)
            let encoder = JSONEncoder(); encoder.keyEncodingStrategy = .convertToSnakeCase
            let data = try encoder.encode(boundScope)
            guard data.count <= resolvedScopeCapacity else { return 2 }
            let runtimeID = allocateID()
            nativeRuntimes[runtimeID] = NativeRuntime(id: runtimeID, config: config, processes: resolved)
            data.copyBytes(to: resolvedScopeOut, count: data.count)
            resolvedScopeLengthOut.pointee = data.count
            runtimeOut.pointee = runtimeID
            return 0
        } catch { return 1 }
    }
}

@_cdecl("comuse_runtime_pump")
public func comuseRuntimePump(_ runtimeID: UInt64, _ timeoutMilliseconds: UInt32) -> Int32 {
    guard pthread_main_np() != 0, MainActor.assumeIsolated({ nativeRuntimes[runtimeID] != nil }) else { return 3 }
    let bounded = min(timeoutMilliseconds, 50)
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, TimeInterval(bounded) / 1000.0, true)
    return 0
}

@_cdecl("comuse_runtime_close")
public func comuseRuntimeClose(_ runtimeID: UInt64) -> Int32 {
    guard pthread_main_np() != 0 else { return 4 }
    return MainActor.assumeIsolated {
        guard let runtime = nativeRuntimes[runtimeID] else { return 3 }
        return runtime.close()
    }
}

@_cdecl("comuse_request_start")
public func comuseRequestStart(_ runtimeID: UInt64, _ requestBytes: UnsafePointer<UInt8>?,
                               _ requestLength: Int, _ callbackID: UInt64,
                               _ completion: Completion?, _ requestOut: UnsafeMutablePointer<UInt64>?) -> Int32 {
    guard let requestBytes, requestLength > 0, requestLength <= comuseMaximumRequestBytes,
          let completion, let requestOut else { return 1 }
    let nativeID = allocateID()
    requestOut.pointee = nativeID
    let data = Data(bytes: requestBytes, count: requestLength)
    Task { @MainActor in
        guard let runtime = nativeRuntimes[runtimeID] else {
            let bytes = envelope(requestID: extractRequestID(from: data), error: error("backend_unavailable"))
            bytes.withUnsafeBytes { raw in completion(callbackID, nativeID, 0, raw.bindMemory(to: UInt8.self).baseAddress, raw.count) }
            return
        }
        runtime.dispatch(requestID: nativeID, callbackID: callbackID, completion: completion, data: data)
    }
    return 0
}

@_cdecl("comuse_request_cancel")
public func comuseRequestCancel(_ runtimeID: UInt64, _ requestID: UInt64) -> Int32 {
    markCancelled(requestID)
    Task { @MainActor in nativeRuntimes[runtimeID]?.cancel(requestID) }
    return 0
}
