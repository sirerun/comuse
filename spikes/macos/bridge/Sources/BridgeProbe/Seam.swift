import Foundation
import Dispatch

private let maxRequestBytes = 32 * 1024
private let maxResponseBytes = 64 * 1024
private let maxOutstandingRequests = 64
private let workQueue = DispatchQueue(label: "comuse.spike.hello", qos: .userInitiated)

private struct Request: Decodable, Sendable {
    let schema_version: Int
    let request_id: String
    let op: String
}
private struct Response: Encodable {
    let schema_version = 1
    let request_id: String
    let status: String
    let result: String?
    let error: String?
}
private final class Entry: @unchecked Sendable {
    let requestID: String
    let token: UInt64
    let callback: @convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void
    var terminal = false
    var callbackInFlight = false
    var callbackReturned = false
    init(requestID: String, token: UInt64, callback: @escaping @convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void) {
        self.requestID = requestID
        self.token = token
        self.callback = callback
    }
}
private final class Registry: @unchecked Sendable {
    let lock = NSLock()
    var nextHandle: UInt64 = 1
    var entries: [UInt64: Entry] = [:]
}
private let registry = Registry()

private func encode(_ requestID: String, _ status: String, _ result: String? = nil, _ error: String? = nil) -> Data {
    struct Envelope: Encodable {
        let schema_version = 1
        let request_id: String
        let status: String
        let result: String?
        let error: String?
    }
    return (try? JSONEncoder().encode(Envelope(request_id: requestID, status: status, result: result, error: error))) ?? Data()
}
private func finish(_ handle: UInt64, _ data: Data) {
    registry.lock.lock()
    guard let entry = registry.entries[handle], !entry.terminal else { registry.lock.unlock(); return }
    let terminalData = data.count <= maxResponseBytes ? data : encode("", "error", nil, "response_limit_exceeded")
    entry.terminal = true
    entry.callbackInFlight = true
    registry.lock.unlock()
    let bytes = UnsafeMutablePointer<UInt8>.allocate(capacity: max(1, data.count))
    terminalData.copyBytes(to: bytes, count: terminalData.count)
    entry.callback(handle, UnsafePointer(bytes), terminalData.count, entry.token)
    bytes.deallocate()
    registry.lock.lock()
    entry.callbackInFlight = false
    entry.callbackReturned = true
    registry.lock.unlock()
}

@_cdecl("comuse_spike_abi_version")
public func comuse_spike_abi_version() -> UInt32 { 1 }

@_cdecl("comuse_spike_request_start")
public func comuse_spike_request_start(_ bytes: UnsafePointer<UInt8>?, _ length: Int, _ token: UInt64, _ callback: (@convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void)?, _ outHandle: UnsafeMutablePointer<UInt64>?) -> Int32 {
    guard let bytes, let callback, let outHandle, length >= 0 else { return 2 }
    guard length <= maxRequestBytes else { return 4 }
    let data = Data(bytes: bytes, count: length)
    guard let request = try? JSONDecoder().decode(Request.self, from: data), request.schema_version == 1, !request.request_id.isEmpty, request.op == "hello" else { return 3 }
    registry.lock.lock()
    guard registry.entries.count < maxOutstandingRequests else { registry.lock.unlock(); return 8 }
    let handle = registry.nextHandle
    registry.nextHandle &+= 1
    let entry = Entry(requestID: request.request_id, token: token, callback: callback)
    registry.entries[handle] = entry
    registry.lock.unlock()
    outHandle.pointee = handle
    workQueue.async { finish(handle, encode(request.request_id, "completed", "hello")) }
    return 0
}

@_cdecl("comuse_spike_request_cancel")
public func comuse_spike_request_cancel(_ handle: UInt64) -> Int32 {
    registry.lock.lock()
    guard let entry = registry.entries[handle] else { registry.lock.unlock(); return 5 }
    registry.lock.unlock()
    finish(handle, encode(entry.requestID, "cancelled", nil, "cancelled"))
    return 0
}

@_cdecl("comuse_spike_request_drain")
public func comuse_spike_request_drain(_ handle: UInt64) -> Int32 {
    registry.lock.lock()
    defer { registry.lock.unlock() }
    guard let entry = registry.entries[handle] else { return 5 }
    guard entry.terminal && entry.callbackReturned && !entry.callbackInFlight else { return 6 }
    registry.entries.removeValue(forKey: handle)
    return 0
}
