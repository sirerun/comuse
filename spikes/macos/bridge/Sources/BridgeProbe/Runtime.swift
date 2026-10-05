import Foundation
import Darwin

final class RuntimeContext: @unchecked Sendable {
    let id: UInt64
    let ownerThreadID: UInt64
    var acceptingRequests = true

    init(id: UInt64, ownerThreadID: UInt64) {
        self.id = id
        self.ownerThreadID = ownerThreadID
    }
}

private func currentThreadID() -> UInt64 {
    var threadID: UInt64 = 0
    pthread_threadid_np(pthread_self(), &threadID)
    return threadID
}

private func ownerThreadMatches(_ runtime: RuntimeContext) -> Bool {
    pthread_main_np() != 0 && currentThreadID() == runtime.ownerThreadID
}

func openRuntime(_ outRuntime: UnsafeMutablePointer<UInt64>?) -> Int32 {
    guard let outRuntime else {
        return 2
    }
    guard pthread_main_np() != 0 else {
        return 9
    }
    let threadID = currentThreadID()
    nativeRegistry.lock.lock()
    guard nativeRegistry.runtime == nil else {
        nativeRegistry.lock.unlock()
        return 8
    }
    let id = nativeRegistry.nextRuntimeID
    nativeRegistry.nextRuntimeID &+= 1
    nativeRegistry.runtime = RuntimeContext(id: id, ownerThreadID: threadID)
    nativeRegistry.lock.unlock()
    outRuntime.pointee = id
    return 0
}

func pumpRuntime(_ runtimeID: UInt64, timeoutMilliseconds: UInt32) -> Int32 {
    guard pthread_main_np() != 0 else {
        return 9
    }
    nativeRegistry.lock.lock()
    guard let runtime = nativeRegistry.runtime, runtime.id == runtimeID else {
        nativeRegistry.lock.unlock()
        return 5
    }
    let sameThread = ownerThreadMatches(runtime)
    nativeRegistry.lock.unlock()
    guard sameThread else {
        return 9
    }
    guard timeoutMilliseconds <= 250 else {
        return 4
    }
    let deadline = Date(timeIntervalSinceNow: TimeInterval(timeoutMilliseconds) / 1_000)
    _ = RunLoop.main.run(mode: .default, before: deadline)
    return 0
}

func closeRuntime(_ runtimeID: UInt64) -> Int32 {
    guard pthread_main_np() != 0 else {
        return 9
    }
    nativeRegistry.lock.lock()
    guard let runtime = nativeRegistry.runtime, runtime.id == runtimeID else {
        nativeRegistry.lock.unlock()
        return 5
    }
    guard ownerThreadMatches(runtime) else {
        nativeRegistry.lock.unlock()
        return 9
    }
    runtime.acceptingRequests = false
    let queuedHandles = nativeRegistry.entries.compactMap { handle, entry in
        entry.runtimeID == runtimeID && entry.phase == .queued ? handle : nil
    }
    nativeRegistry.lock.unlock()

    for handle in queuedHandles {
        _ = cancelRequest(handle)
    }

    nativeRegistry.lock.lock()
    let hasRetainedEntries = nativeRegistry.entries.values.contains { $0.runtimeID == runtimeID }
    if !hasRetainedEntries {
        nativeRegistry.runtime = nil
    }
    nativeRegistry.lock.unlock()
    return hasRetainedEntries ? 6 : 0
}

@MainActor
func executeAccessibilityRequest(_ handle: UInt64) {
    nativeRegistry.lock.lock()
    guard var entry = nativeRegistry.entries[handle], entry.phase == .queued else {
        nativeRegistry.lock.unlock()
        return
    }
    guard pthread_main_np() != 0 else {
        let requestID = entry.requestID
        nativeRegistry.lock.unlock()
        finishRequest(handle, data: makeProbeError(requestID: requestID, code: "wrong_thread", message: "AX probe was not dispatched on the process main thread"))
        return
    }
    guard
          let runtime = nativeRegistry.runtime,
          runtime.id == entry.runtimeID,
          runtime.acceptingRequests,
          ownerThreadMatches(runtime)
    else {
        nativeRegistry.lock.unlock()
        return
    }
    entry.phase = .executing
    nativeRegistry.entries[handle] = entry
    nativeRegistry.lock.unlock()

    let response = handleAccessibilityProbe(entry.requestData)
    guard validProbeEnvelope(response, requestID: entry.requestID) else {
        finishRequest(handle, data: makeProbeError(requestID: entry.requestID, code: "invalid_native_response", message: "AX probe returned an invalid versioned response envelope"))
        return
    }
    finishRequest(handle, data: response)
}

private func validProbeEnvelope(_ data: Data, requestID: String) -> Bool {
    guard data.count <= maxResponseBytes,
          let value = try? JSONSerialization.jsonObject(with: data),
          let object = value as? [String: Any],
          object["schema_version"] as? Int == 1,
          object["request_id"] as? String == requestID,
          object["status"] is String,
          object.keys.contains("error"),
          object.keys.contains("result")
    else {
        return false
    }
    return true
}

private func makeProbeError(requestID: String, code: String, message: String) -> Data {
    let envelope: [String: Any] = [
        "schema_version": 1,
        "request_id": requestID,
        "status": "error",
        "error": ["code": code, "message": message],
        "result": NSNull()
    ]
    return (try? JSONSerialization.data(withJSONObject: envelope, options: [.sortedKeys])) ?? Data()
}
