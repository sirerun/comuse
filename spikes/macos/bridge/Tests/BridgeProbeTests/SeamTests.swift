import Foundation
import XCTest
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

final class SeamTests: XCTestCase {
    func testVersionedHelloCompletesAndDrains() throws {
        XCTAssertEqual(comuse_spike_abi_version(), 1)
        capture = Capture()
        let request = Data(#"{"schema_version":1,"request_id":"swift-test","op":"hello"}"#.utf8)
        var handle: UInt64 = 0
        let status = request.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        XCTAssertEqual(status, 0)
        XCTAssertGreaterThan(handle, 0)
        XCTAssertEqual(capture.done.wait(timeout: .now() + 2), .success)
        let data = try XCTUnwrap(capture.get())
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["schema_version"] as? Int, 1)
        XCTAssertEqual(object["request_id"] as? String, "swift-test")
        XCTAssertEqual(object["status"] as? String, "completed")
        XCTAssertEqual(object["result"] as? String, "hello")
        XCTAssertEqual(comuse_spike_request_drain(handle), 0)
        XCTAssertEqual(comuse_spike_request_drain(handle), 5)
    }

    func testMalformedRequestFailsBeforeCreatingAHandle() {
        var handle: UInt64 = 0
        let bad = Data("{}".utf8)
        XCTAssertEqual(bad.withUnsafeBytes { comuse_spike_request_start($0.bindMemory(to: UInt8.self).baseAddress, $0.count, 42, seamTestCompletion, &handle) }, 3)
        XCTAssertEqual(comuse_spike_request_start(nil, 32 * 1024 + 1, 42, seamTestCompletion, &handle), 2)
    }

    func testDrainWaitsForCallbackReturnAndCancelDoesNotDuplicateCompletion() {
        capture = Capture(blocksCallback: true)
        let request = Data(#"{"schema_version":1,"request_id":"swift-cancel","op":"hello"}"#.utf8)
        var handle: UInt64 = 0
        let status = request.withUnsafeBytes { raw in
            comuse_spike_request_start(raw.bindMemory(to: UInt8.self).baseAddress, raw.count, 42, seamTestCompletion, &handle)
        }
        XCTAssertEqual(status, 0)
        XCTAssertEqual(capture.done.wait(timeout: .now() + 2), .success)
        XCTAssertEqual(comuse_spike_request_drain(handle), 6)
        XCTAssertEqual(comuse_spike_request_cancel(handle), 0)
        capture.releaseCallback.signal()
        let deadline = Date().addingTimeInterval(2)
        while comuse_spike_request_drain(handle) == 6 && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.001)
        }
        XCTAssertEqual(comuse_spike_request_drain(handle), 0)
        XCTAssertEqual(capture.count(), 1)
    }
}
