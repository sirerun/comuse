import Foundation
import Testing
@testable import BridgeProbe

private final class Capture: @unchecked Sendable {
    private let lock = NSLock()
    private var payload: Data?
    private var callbackCount = 0
    private let blocksCallback: Bool
    let done = DispatchSemaphore(value: 0)
    let releaseCallback = DispatchSemaphore(value: 0)
    init(blocksCallback: Bool = false) {
        self.blocksCallback = blocksCallback
    }
    func set(_ value: Data) -> Bool {
        lock.lock()
        payload = value
        callbackCount += 1
        lock.unlock()
        done.signal()
        return blocksCallback
    }
    func get() -> Data? {
        lock.lock()
        defer { lock.unlock() }
        return payload
    }
    func count() -> Int {
        lock.lock()
        defer { lock.unlock() }
        return callbackCount
    }
}
nonisolated(unsafe) private var capture = Capture()

@_cdecl("seam_test_completion")
private func seamTestCompletion(_ handle: UInt64, _ bytes: UnsafePointer<UInt8>?, _ length: Int, _ token: UInt64) {
    guard handle > 0, token == 42, let bytes, length >= 0 else { return }
    let shouldBlock = capture.set(Data(bytes: bytes, count: length))
    if shouldBlock {
        capture.releaseCallback.wait()
    }
}

@Suite(.serialized)
struct SeamTests {
    @Test
    func readonlyRequestStartRejectsInputAndHostEntryRequiresMatchingRuntime() {
        capture = Capture()
        let request = Data(#"{"schema_version":1,"request_id":"host-only","op":"replace","action_id":"a","scope":{}}"#.utf8)
        var handle: UInt64 = 0
        let readonlyStatus = request.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        #expect(readonlyStatus == 3)
        #expect(handle == 0)
        let hostStatus = request.withUnsafeBytes { raw in
            comuse_spike_input_request_start(1, raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        #expect(hostStatus == 10)
        #expect(handle == 0)
        #expect(capture.count() == 0)
    }

    @Test
    func versionedHelloCompletesAndDrains() throws {
        #expect(comuse_spike_abi_version() == 1)
        capture = Capture()
        let request = Data(#"{"schema_version":1,"request_id":"swift-test","op":"hello"}"#.utf8)
        var handle: UInt64 = 0
        let status = request.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        #expect(status == 0)
        #expect(handle > 0)
        #expect(capture.done.wait(timeout: .now() + 2) == .success)
        let data = try #require(capture.get())
        let object = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
        #expect(object["schema_version"] as? Int == 1)
        #expect(object["request_id"] as? String == "swift-test")
        #expect(object["status"] as? String == "completed")
        #expect(object["result"] as? String == "hello")
        #expect(comuse_spike_request_drain(handle) == 0)
        #expect(comuse_spike_request_drain(handle) == 5)
    }

    @Test
    func malformedRequestFailsBeforeCreatingAHandle() {
        var handle: UInt64 = 0
        let bad = Data("{}".utf8)
        #expect(bad.withUnsafeBytes { comuse_spike_request_start($0.bindMemory(to: UInt8.self).baseAddress, $0.count, 42, seamTestCompletion, &handle) } == 3)
        let oversizedRequestLength: Int = 32 * 1024 + 1
        #expect(comuse_spike_request_start(nil, oversizedRequestLength, 42, seamTestCompletion, &handle) == 2)
    }

    @Test
    func requestByteLimitRejectsAboveCapAndAcceptsExactCap() throws {
        capture = Capture()
        let overLimit = Data(repeating: 0x20, count: (32 * 1024) + 1)
        var rejectedHandle: UInt64 = 0
        let rejectedStatus = overLimit.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &rejectedHandle)
        }
        #expect(rejectedStatus == 4)
        #expect(rejectedHandle == 0)
        #expect(capture.count() == 0)

        var exactLimit = Data(#"{"schema_version":1,"request_id":"swift-boundary","op":"hello"}"#.utf8)
        exactLimit.append(Data(repeating: 0x20, count: (32 * 1024) - exactLimit.count))
        #expect(exactLimit.count == 32 * 1024)
        var handle: UInt64 = 0
        let acceptedStatus = exactLimit.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        #expect(acceptedStatus == 0)
        #expect(handle > 0)
        #expect(capture.done.wait(timeout: .now() + 2) == .success)
        let response = try #require(capture.get())
        let object = try #require(JSONSerialization.jsonObject(with: response) as? [String: Any])
        #expect(object["request_id"] as? String == "swift-boundary")
        #expect(object["status"] as? String == "completed")
        #expect(comuse_spike_request_drain(handle) == 0)
    }

    @Test
    func drainWaitsForCallbackReturnAndCancelDoesNotDuplicateCompletion() {
        capture = Capture(blocksCallback: true)
        let request = Data(#"{"schema_version":1,"request_id":"swift-cancel","op":"hello"}"#.utf8)
        var handle: UInt64 = 0
        let status = request.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        #expect(status == 0)
        #expect(capture.done.wait(timeout: .now() + 2) == .success)
        #expect(comuse_spike_request_drain(handle) == 6)
        #expect(comuse_spike_request_cancel(handle) == 0)
        capture.releaseCallback.signal()
        let deadline = Date().addingTimeInterval(2)
        var drainStatus = comuse_spike_request_drain(handle)
        while drainStatus == 6 && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.001)
            drainStatus = comuse_spike_request_drain(handle)
        }
        #expect(drainStatus == 0)
        #expect(capture.count() == 1)
    }
}
