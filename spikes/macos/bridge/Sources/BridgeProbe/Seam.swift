import Foundation
import Dispatch

let maxRequestBytes = 32 * 1024
let maxResponseBytes = 64 * 1024
let maxOutstandingRequests = 64
let helloQueue = DispatchQueue(label: "comuse.spike.hello", qos: .userInitiated)
let supportedOperations: Set<String> = ["doctor", "windows", "a11y"]

struct SpikeRequest: Decodable, Sendable {
    let schema_version: Int
    let request_id: String
    let op: String
}

struct RequestEntry: @unchecked Sendable {
    enum Phase {
        case queued
        case executing
        case terminal
    }

    let requestID: String
    let operation: String
    let requestData: Data
    let runtimeID: UInt64?
    let callbackToken: UInt64
    let callback: @convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void
    var phase: Phase = .queued
    var callbackInFlight = false
    var callbackReturned = false
    var workScheduled = true
    var workReturned = false

    init(
        request: SpikeRequest,
        data: Data,
        runtimeID: UInt64?,
        token: UInt64,
        callback: @escaping @convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void
    ) {
        requestID = request.request_id
        operation = request.op
        requestData = data
        self.runtimeID = runtimeID
        callbackToken = token
        self.callback = callback
    }
}

final class NativeRegistry: @unchecked Sendable {
    let lock = NSLock()
    var nextRequestID: UInt64 = 1
    var nextRuntimeID: UInt64 = 1
    var entries: [UInt64: RequestEntry] = [:]
    var runtime: RuntimeContext?
}

let nativeRegistry = NativeRegistry()

struct TerminalEnvelope: Encodable {
    let schema_version = 1
    let request_id: String
    let status: String
    let result: String?
    let error: String?
}

func encodeTerminal(_ requestID: String, status: String, result: String? = nil, error: String? = nil) -> Data {
    (try? JSONEncoder().encode(TerminalEnvelope(
        request_id: requestID,
        status: status,
        result: result,
        error: error
    ))) ?? Data()
}

private func deliver(_ entry: RequestEntry, handle: UInt64, data: Data) {
    let bytes = UnsafeMutablePointer<UInt8>.allocate(capacity: max(1, data.count))
    data.copyBytes(to: bytes, count: data.count)
    entry.callback(handle, UnsafePointer(bytes), data.count, entry.callbackToken)
    bytes.deallocate()
    nativeRegistry.lock.lock()
    if var current = nativeRegistry.entries[handle] {
        current.callbackInFlight = false
        current.callbackReturned = true
        nativeRegistry.entries[handle] = current
    }
    nativeRegistry.lock.unlock()
}

func finishRequest(_ handle: UInt64, data: Data) {
    nativeRegistry.lock.lock()
    guard var entry = nativeRegistry.entries[handle], entry.phase != .terminal else {
        nativeRegistry.lock.unlock()
        return
    }
    let terminalData = data.count <= maxResponseBytes
        ? data
        : encodeTerminal(entry.requestID, status: "error", error: "response_limit_exceeded")
    entry.phase = .terminal
    entry.callbackInFlight = true
    nativeRegistry.entries[handle] = entry
    nativeRegistry.lock.unlock()
    deliver(entry, handle: handle, data: terminalData)
}

func startRequest(
    _ bytes: UnsafePointer<UInt8>?,
    _ length: Int,
    _ callbackToken: UInt64,
    _ callback: (@convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void)?,
    _ outHandle: UnsafeMutablePointer<UInt64>?
) -> Int32 {
    guard let bytes, let callback, let outHandle, length >= 0 else {
        return 2
    }
    guard length <= maxRequestBytes else {
        return 4
    }
    let requestData = Data(bytes: bytes, count: length)
    guard let request = try? JSONDecoder().decode(SpikeRequest.self, from: requestData),
          request.schema_version == 1,
          !request.request_id.isEmpty,
          request.op == "hello" || supportedOperations.contains(request.op)
    else {
        return 3
    }

    nativeRegistry.lock.lock()
    guard nativeRegistry.entries.count < maxOutstandingRequests else {
        nativeRegistry.lock.unlock()
        return 8
    }
    var runtimeID: UInt64?
    if request.op != "hello" {
        guard let runtime = nativeRegistry.runtime else {
            nativeRegistry.lock.unlock()
            return 10
        }
        guard runtime.acceptingRequests else {
            nativeRegistry.lock.unlock()
            return 11
        }
        runtimeID = runtime.id
    }
    let handle = nativeRegistry.nextRequestID
    nativeRegistry.nextRequestID &+= 1
    nativeRegistry.entries[handle] = RequestEntry(
        request: request,
        data: requestData,
        runtimeID: runtimeID,
        token: callbackToken,
        callback: callback
    )
    nativeRegistry.lock.unlock()
    outHandle.pointee = handle

    if request.op == "hello" {
        helloQueue.async {
            finishRequest(handle, data: encodeTerminal(request.request_id, status: "completed", result: "hello"))
            markScheduledWorkReturned(handle)
        }
    } else {
        DispatchQueue.main.async { @MainActor in
            executeAccessibilityRequest(handle)
            markScheduledWorkReturned(handle)
        }
    }
    return 0
}

func cancelRequest(_ handle: UInt64) -> Int32 {
    nativeRegistry.lock.lock()
    guard var entry = nativeRegistry.entries[handle] else {
        nativeRegistry.lock.unlock()
        return 5
    }
    guard entry.phase == .queued else {
        nativeRegistry.lock.unlock()
        return 0
    }
    let cancelled = encodeTerminal(entry.requestID, status: "cancelled", error: "cancelled")
    entry.phase = .terminal
    entry.callbackInFlight = true
    nativeRegistry.entries[handle] = entry
    nativeRegistry.lock.unlock()
    deliver(entry, handle: handle, data: cancelled)
    return 0
}

func drainRequest(_ handle: UInt64) -> Int32 {
    nativeRegistry.lock.lock()
    defer { nativeRegistry.lock.unlock() }
    guard let entry = nativeRegistry.entries[handle] else {
        return 5
    }
    guard entry.phase == .terminal,
          entry.callbackReturned,
          !entry.callbackInFlight,
          (!entry.workScheduled || entry.workReturned)
    else {
        return 6
    }
    nativeRegistry.entries.removeValue(forKey: handle)
    return 0
}

func markScheduledWorkReturned(_ handle: UInt64) {
    nativeRegistry.lock.lock()
    if var entry = nativeRegistry.entries[handle] {
        entry.workReturned = true
        nativeRegistry.entries[handle] = entry
    }
    nativeRegistry.lock.unlock()
}
