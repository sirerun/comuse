import AppKit
import ApplicationServices
import ComuseABI
import CoreFoundation
import Darwin
import Foundation

let comuseMaximumRequestBytes = 32 * 1024
let comuseMaximumResponseBytes = 64 * 1024
let comuseMaximumScopeProcesses = 32

func nativeWindowTitleEvidence(_ value: Any?) -> String? { value as? String }

public typealias Completion = @convention(c) (UInt64, UInt64, Int32, UnsafePointer<UInt8>?, Int) -> Void

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
    var direction: String
    var amount: String
    var x: Double
    var y: Double
    var endX: Double
    var endY: Double
    var button: String
    var count: Int
    var holdMS: Int
    var delayMS: Int
    var keys: String
    var dx: Int
    var dy: Int
    var steps: Int
    var durationMS: Int
    enum CodingKeys: String, CodingKey {
        case id; case windowRef = "window_ref"; case elementRef = "element_ref"
        case stateID = "state_id"; case kind; case text; case direction; case amount
        case x; case y; case endX = "end_x"; case endY = "end_y"; case button
        case count; case holdMS = "hold_ms"; case delayMS = "delay_ms"; case keys
        case dx; case dy; case steps; case durationMS = "duration_ms"
    }

    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        id = try values.decode(String.self, forKey: .id)
        windowRef = try values.decode(String.self, forKey: .windowRef)
        elementRef = try values.decodeIfPresent(String.self, forKey: .elementRef) ?? ""
        stateID = try values.decodeIfPresent(String.self, forKey: .stateID) ?? ""
        kind = try values.decode(String.self, forKey: .kind)
        text = try values.decodeIfPresent(String.self, forKey: .text) ?? ""
        direction = try values.decodeIfPresent(String.self, forKey: .direction) ?? ""
        amount = try values.decodeIfPresent(String.self, forKey: .amount) ?? ""
        x = try values.decodeIfPresent(Double.self, forKey: .x) ?? 0
        y = try values.decodeIfPresent(Double.self, forKey: .y) ?? 0
        endX = try values.decodeIfPresent(Double.self, forKey: .endX) ?? 0
        endY = try values.decodeIfPresent(Double.self, forKey: .endY) ?? 0
        button = try values.decodeIfPresent(String.self, forKey: .button) ?? ""
        count = try values.decodeIfPresent(Int.self, forKey: .count) ?? 0
        holdMS = try values.decodeIfPresent(Int.self, forKey: .holdMS) ?? 0
        delayMS = try values.decodeIfPresent(Int.self, forKey: .delayMS) ?? 0
        keys = try values.decodeIfPresent(String.self, forKey: .keys) ?? ""
        dx = try values.decodeIfPresent(Int.self, forKey: .dx) ?? 0
        dy = try values.decodeIfPresent(Int.self, forKey: .dy) ?? 0
        steps = try values.decodeIfPresent(Int.self, forKey: .steps) ?? 0
        durationMS = try values.decodeIfPresent(Int.self, forKey: .durationMS) ?? 0
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
            else if CFNumberIsFloatType(value as CFNumber) { try container.encode(value.doubleValue) }
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
nonisolated(unsafe) private var activeNativeRequests: [UInt64: UInt64] = [:]
private let cancellationLock = NSLock()

private func allocateID() -> UInt64 {
    idLock.lock(); defer { idLock.unlock() }
    let value = nextNativeID
    nextNativeID &+= 1
    if nextNativeID == 0 { nextNativeID = 1 }
    return value
}

private func registerNativeRequest(_ runtimeID: UInt64, _ requestID: UInt64) -> Bool {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    guard activeNativeRequests.count < 32, activeNativeRequests[requestID] == nil else { return false }
    activeNativeRequests[requestID] = runtimeID
    return true
}

private func cancelNativeRequest(_ runtimeID: UInt64, _ requestID: UInt64) -> Bool {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    guard activeNativeRequests[requestID] == runtimeID else { return false }
    cancelledIDs.insert(requestID)
    return true
}

func isCancelled(_ requestID: UInt64) -> Bool {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    return cancelledIDs.contains(requestID)
}

func clearCancelled(_ requestID: UInt64) {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    cancelledIDs.remove(requestID)
    activeNativeRequests.removeValue(forKey: requestID)
}

private func clearRuntimeRequests(_ runtimeID: UInt64) {
    cancellationLock.lock(); defer { cancellationLock.unlock() }
    let requestIDs = activeNativeRequests.compactMap { $0.value == runtimeID ? $0.key : nil }
    for requestID in requestIDs {
        activeNativeRequests.removeValue(forKey: requestID)
        cancelledIDs.remove(requestID)
    }
}

@MainActor private var nativeRuntimes: [UInt64: NativeRuntime] = [:]

@MainActor
final class NativeRuntime {
    let id: UInt64
    let config: NativeConfig
    let processes: [NativeProcess]
    var requestTasks: [UInt64: Task<Void, Never>] = [:]
    var lastProjectionDigest: String?
    var desktopTracker: DesktopContextTracker

    init(id: UInt64, config: NativeConfig, processes: [NativeProcess]) {
        self.id = id
        self.config = config
        self.processes = processes
        self.desktopTracker = DesktopContextTracker(displayID: UUID().uuidString.lowercased())
    }

    func dispatch(requestID: UInt64, callbackID: UInt64, completion: Completion, data: Data) {
        guard requestTasks[requestID] == nil else {
            finish(callbackID: callbackID, requestID: requestID, completion: completion,
                   bytes: envelope(requestID: extractRequestID(from: data), error: nativeErrorCode("invalid_request")))
            clearCancelled(requestID)
            return
        }
        let task = Task { @MainActor [weak self] in
            guard let self else {
                let bytes = envelope(requestID: extractRequestID(from: data), error: nativeErrorCode("backend_unavailable"))
                bytes.withUnsafeBytes { raw in
                    completion(callbackID, requestID, 0, raw.bindMemory(to: UInt8.self).baseAddress, raw.count)
                }
                clearCancelled(requestID)
                return
            }
            let response = self.handle(data, nativeRequestID: requestID)
            self.finish(callbackID: callbackID, requestID: requestID, completion: completion, bytes: response)
            self.requestTasks.removeValue(forKey: requestID)
            clearCancelled(requestID)
        }
        requestTasks[requestID] = task
    }

    func cancel(_ requestID: UInt64) {
        requestTasks[requestID]?.cancel()
    }

    func close() -> Int32 {
        guard requestTasks.isEmpty else { return 5 }
        ReferenceStore.shared.remove(runtimeID: id)
        SnapshotStore.shared.remove(runtimeID: id)
        clearRuntimeRequests(id)
        nativeRuntimes.removeValue(forKey: id)
        return 0
    }

    private func handle(_ data: Data, nativeRequestID: UInt64) -> Data {
        do {
            let request = try decodeNativeRequest(data)
            guard request.schemaVersion == 1, validRequestID(request.requestID) else {
                throw ProbeFailure(code: "invalid_request")
            }
            guard !isCancelled(nativeRequestID) else { throw ProbeFailure(code: "cancelled") }
            guard config.scope.expiresAtUnixMilli > Int64(Date().timeIntervalSince1970 * 1000) else {
                invalidateAccessibilityState()
                desktopTracker.invalidate()
                throw ProbeFailure(code: "invalid_request")
            }
            let result: Any
            switch request.operation {
            case "doctor": result = try doctor(requestID: nativeRequestID)
            case "windows": result = try windows(request, requestID: nativeRequestID)
            case "observe": result = try observe(request, requestID: nativeRequestID)
            case "read_element": result = try readElement(request, requestID: nativeRequestID)
            case "execute": result = try execute(request, requestID: nativeRequestID)
            default: throw ProbeFailure(code: "unsupported")
            }
            let encoded = try JSONSerialization.data(withJSONObject: result, options: [.fragmentsAllowed, .sortedKeys])
            let response = envelope(requestID: request.requestID, result: encoded)
            guard response.count <= comuseMaximumResponseBytes else { throw ProbeFailure(code: "budget_exceeded") }
            return response
        } catch {
            return nativeErrorEnvelope(requestID: extractRequestID(from: data), error: error)
        }
    }

    private func doctor(requestID: UInt64) throws -> [String: Any] {
        let deadline = ProcessInfo.processInfo.systemUptime + 1.0
        try checkDeadline(requestID, deadline: deadline)
        let accessibility = AXIsProcessTrusted()
        if !accessibility { invalidateAccessibilityState() }
        var result: [String: Any] = [
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
        if let context = try desktopContextJSON(requestID: requestID, deadline: deadline) { result["desktop_context"] = context }
        return result
    }

    private func windows(_ request: NativeRequest, requestID: UInt64) throws -> [[String: Any]] {
        try checkPermission()
        let desktopGeneration = try refreshDesktopGeneration()
        var result: [[String: Any]] = []
        let budget = try boundedBudget(request.budget)
        let deadline = ProcessInfo.processInfo.systemUptime + budget.timeout
        var totalBytes = 0
        for process in processes {
            try checkDeadline(requestID, deadline: deadline)
            let app = try application(process)
            let applicationElement = AXUIElementCreateApplication(process.pid)
            var windowCount: CFIndex = 0
            guard (try nativeAXDeadlineIPC(applicationElement, deadline: deadline) {
                AXUIElementGetAttributeValueCount(applicationElement, kAXWindowsAttribute as CFString, &windowCount)
            }) == .success,
                  windowCount >= 0 else { throw ProbeFailure(code: "backend_unavailable") }
            let remaining = min(budget.maxNodes - result.count, 128 - result.count)
            let bounds = boundedChildCount(Int(windowCount), limit: remaining)
            // Windows have no per-row coverage envelope; fail explicitly rather
            // than return a complete-looking subset when the list is too large.
            guard !bounds.truncated else { throw ProbeFailure(code: "budget_exceeded") }
            var rawWindows: CFArray?
            if bounds.count > 0 {
                guard (try nativeAXDeadlineIPC(applicationElement, deadline: deadline) {
                    AXUIElementCopyAttributeValues(applicationElement, kAXWindowsAttribute as CFString, 0, CFIndex(bounds.count), &rawWindows)
                }) == .success else {
                    throw ProbeFailure(code: "backend_unavailable")
                }
            }
            let values: [AXUIElement]
            if bounds.count == 0 {
                values = []
            } else if let copied = rawWindows as? [AXUIElement] {
                values = copied
            } else {
                throw ProbeFailure(code: "backend_unavailable")
            }
            for window in values {
                try checkDeadline(requestID, deadline: deadline)
                guard result.count < min(budget.maxNodes, 128) else { throw ProbeFailure(code: "budget_exceeded") }
                let ref = try retain(window, process: app.identity, windowRef: nil, kind: .window, deadline: deadline)
                guard let inspectedTitle = nativeWindowTitleEvidence(copyAttribute(window, kAXTitleAttribute as String,
                                                                                    deadline: deadline)) else {
                    invalidateAccessibilityState()
                    throw ProbeFailure(code: "backend_unavailable")
                }
                let title = boundedUTF8Prefix(inspectedTitle, byteLimit: 1024)
                guard !title.truncated else { throw ProbeFailure(code: "budget_exceeded") }
                let row: [String: Any] = ["ref": ref, "process": processJSON(app.identity), "title": title.text]
                let bytes = try JSONSerialization.data(withJSONObject: row, options: [.sortedKeys]).count
                guard totalBytes + bytes <= budget.maxBytes else { throw ProbeFailure(code: "budget_exceeded") }
                totalBytes += bytes
                result.append(row)
            }
            guard try application(process).identity == app.identity else { throw ProbeFailure(code: "element_stale") }
        }
        guard try refreshDesktopGeneration() == desktopGeneration else {
            invalidateAccessibilityState()
            throw ProbeFailure(code: "state_expired")
        }
        return result
    }

    // AX implementation is defined in Accessibility.swift.
    func observe(_ request: NativeRequest, requestID: UInt64) throws -> [String: Any] {
        let budget = try boundedBudget(request.budget)
        return try observeWindow(request, requestID: requestID,
                                 deadline: ProcessInfo.processInfo.systemUptime + budget.timeout)
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

    func checkPermission() throws {
        try requireAccessibilityPermission(AXIsProcessTrusted())
    }

    func requireAccessibilityPermission(_ trusted: Bool) throws {
        guard trusted else {
            invalidateAccessibilityState()
            throw ProbeFailure(code: "permission_denied")
        }
    }

    func invalidateAccessibilityState() {
        ReferenceStore.shared.remove(runtimeID: id)
        SnapshotStore.shared.remove(runtimeID: id)
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

private let nativeErrorCodes: Set<String> = [
    "invalid_request", "policy_refused", "element_stale", "state_expired", "permission_denied",
    "unsupported", "backend_unavailable", "budget_exceeded", "cancelled", "unknown_outcome", "internal_error"
]

private func nativeErrorCode(_ code: String) -> String {
    nativeErrorCodes.contains(code) ? code : "internal_error"
}

func nativeErrorCode(for error: Error) -> String {
    if let failure = error as? ProbeFailure {
        return nativeErrorCodes.contains(failure.code) ? failure.code : "internal_error"
    }
    if error is DecodingError { return "invalid_request" }
    return "internal_error"
}

func nativeErrorEnvelope(requestID: String, error: Error) -> Data {
    envelope(requestID: requestID, error: nativeErrorCode(for: error))
}

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

private enum NativeOpenResult: Sendable {
    case success(runtimeID: UInt64, scopeData: Data)
    case failure(status: Int32)
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
    do {
        let config = try decodeNativeConfig(Data(bytes: configBytes, count: configLength))
        guard config.schemaVersion == 1, !config.scope.processes.isEmpty,
              config.scope.processes.count <= comuseMaximumScopeProcesses,
              config.scope.expiresAtUnixMilli > Int64(Date().timeIntervalSince1970 * 1000),
              Set(config.scope.processes.map(\.pid)).count == config.scope.processes.count else { return 1 }

        // Keep C pointers out of the actor-isolated closure. The C caller owns
        // their storage for this synchronous ABI call; only Sendable values
        // cross the MainActor boundary.
        let opened: NativeOpenResult = MainActor.assumeIsolated {
            var resolved: [NativeProcess] = []
            for process in config.scope.processes {
                guard process.pid > 0, !process.bundleID.isEmpty, process.bundleID.utf8.count <= 255,
                      let app = NSRunningApplication(processIdentifier: process.pid), !app.isTerminated,
                      app.bundleIdentifier == process.bundleID,
                      let current = processIdentity(pid: process.pid, bundleID: process.bundleID),
                      process.launchID.isEmpty || process.launchID == current.launchID else { return .failure(status: 1) }
                resolved.append(current)
            }
            let boundScope = NativeScope(processes: resolved, expiresAtUnixMilli: config.scope.expiresAtUnixMilli)
            let scopeData: Data
            do {
                let encoder = JSONEncoder(); encoder.keyEncodingStrategy = .convertToSnakeCase
                scopeData = try encoder.encode(boundScope)
            } catch {
                return .failure(status: 1)
            }
            guard scopeData.count <= resolvedScopeCapacity else { return .failure(status: 2) }
            let runtimeID = allocateID()
            nativeRuntimes[runtimeID] = NativeRuntime(id: runtimeID, config: config, processes: resolved)
            return .success(runtimeID: runtimeID, scopeData: scopeData)
        }
        switch opened {
        case .failure(let status):
            return status
        case .success(let runtimeID, let scopeData):
            scopeData.copyBytes(to: resolvedScopeOut, count: scopeData.count)
            resolvedScopeLengthOut.pointee = scopeData.count
            runtimeOut.pointee = runtimeID
            return 0
        }
    } catch {
        return 1
    }
}

@_cdecl("comuse_runtime_pump")
public func comuseRuntimePump(_ runtimeID: UInt64, _ timeoutMilliseconds: UInt32) -> Int32 {
    guard pthread_main_np() != 0, MainActor.assumeIsolated({ nativeRuntimes[runtimeID] != nil }) else { return 3 }
    let bounded = min(timeoutMilliseconds, 50)
    CFRunLoopRunInMode(CFRunLoopMode.defaultMode, TimeInterval(bounded) / 1000.0, true)
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
    guard registerNativeRequest(runtimeID, nativeID) else { return 6 }
    requestOut.pointee = nativeID
    let data = Data(bytes: requestBytes, count: requestLength)
    Task { @MainActor in
        guard let runtime = nativeRuntimes[runtimeID] else {
            let bytes = envelope(requestID: extractRequestID(from: data), error: nativeErrorCode("backend_unavailable"))
            bytes.withUnsafeBytes { raw in completion(callbackID, nativeID, 0, raw.bindMemory(to: UInt8.self).baseAddress, raw.count) }
            clearCancelled(nativeID)
            return
        }
        runtime.dispatch(requestID: nativeID, callbackID: callbackID, completion: completion, data: data)
    }
    return 0
}

@_cdecl("comuse_request_cancel")
public func comuseRequestCancel(_ runtimeID: UInt64, _ requestID: UInt64) -> Int32 {
    guard cancelNativeRequest(runtimeID, requestID) else { return 3 }
    Task { @MainActor in nativeRuntimes[runtimeID]?.cancel(requestID) }
    return 0
}
