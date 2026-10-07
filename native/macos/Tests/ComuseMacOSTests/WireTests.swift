import Foundation
import XCTest
@testable import ComuseMacOS

final class WireTests: XCTestCase {
    func testDecodeGoObserveRequest() throws {
        let payload = #"{"schema_version":1,"request_id":"r1","operation":"observe","window_ref":"w1","budget":{"max_depth":8,"max_nodes":64,"max_bytes":8192,"timeout":1000000000}}"#.data(using: .utf8)!
        let request = try JSONDecoder().decode(NativeRequest.self, from: payload)
        XCTAssertEqual(request.requestID, "r1")
        XCTAssertEqual(request.windowRef, "w1")
        XCTAssertEqual(request.budget?.timeoutNanoseconds, 1_000_000_000)
        XCTAssertEqual(request.budget?.maxDepth, 8)
    }

    func testDecodeGoPressAndReplaceWithOmittedText() throws {
        let press = #"{"schema_version":1,"request_id":"r2","operation":"execute","action":{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"press"}}"#.data(using: .utf8)!
        let decodedPress = try JSONDecoder().decode(NativeRequest.self, from: press)
        XCTAssertEqual(decodedPress.action?.text, "")

        let replace = #"{"schema_version":1,"request_id":"r3","operation":"execute","action":{"id":"a2","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"replace"}}"#.data(using: .utf8)!
        let decodedReplace = try JSONDecoder().decode(NativeRequest.self, from: replace)
        XCTAssertEqual(decodedReplace.action?.text, "")
    }

    func testErrorEnvelopeUsesCodeStringAndNoResult() throws {
        let data = try JSONEncoder().encode(NativeEnvelope(requestID: "r4", status: "error", result: nil,
                                                          error: "permission_denied"))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["error"] as? String, "permission_denied")
        XCTAssertNil(object["result"])
        XCTAssertEqual(object["status"] as? String, "error")
    }

    func testPermissionLossPurgesReferencesAndSnapshots() async throws {
        try await MainActor.run {
            let process = NativeProcess(pid: 1, bundleID: "example.app", launchID: "1.1")
            let runtime = NativeRuntime(
                id: 77,
                config: NativeConfig(schemaVersion: 1,
                                     scope: NativeScope(processes: [process], expiresAtUnixMilli: 1),
                                     allowValues: false),
                processes: [process]
            )
            let element = AXUIElementCreateSystemWide()
            ReferenceStore.shared.set(runtimeID: 77, value: [
                "ref": NativeReference(element: element, process: process, windowRef: nil,
                                        kind: .window, lastSeen: 0)
            ])
            SnapshotStore.shared.set(runtimeID: 77, value: [
                "state": NativeSnapshot(stateID: "state", windowRef: "window", process: process,
                                         digest: "digest", complete: true, coverageReason: "", refs: [],
                                         createdAt: 0, byteCount: 0)
            ])

            XCTAssertThrowsError(try runtime.requireAccessibilityPermission(false))
            XCTAssertTrue(ReferenceStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertTrue(SnapshotStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertNoThrow(try runtime.requireAccessibilityPermission(true))
            XCTAssertTrue(ReferenceStore.shared.get(runtimeID: 77).isEmpty)
            XCTAssertTrue(SnapshotStore.shared.get(runtimeID: 77).isEmpty)
        }
    }

    func testBoundedTextUsesUTF8BytesAndPreservesScalarBoundaries() {
        let bounded = boundedUTF8Prefix("a🙂b", byteLimit: 4)
        XCTAssertEqual(bounded.text, "a")
        XCTAssertTrue(bounded.truncated)

        let exact = boundedUTF8Prefix("a🙂", byteLimit: 5)
        XCTAssertEqual(exact.text, "a🙂")
        XCTAssertFalse(exact.truncated)
    }

    func testChildFrontierBudgetReportsDroppedNodes() {
        XCTAssertEqual(boundedChildCount(100_000, limit: 12).count, 12)
        XCTAssertTrue(boundedChildCount(100_000, limit: 12).truncated)
        XCTAssertEqual(boundedChildCount(4, limit: 4).count, 4)
        XCTAssertFalse(boundedChildCount(4, limit: 4).truncated)
        XCTAssertEqual(boundedChildCount(4, limit: 0).count, 0)
        XCTAssertTrue(boundedChildCount(4, limit: 0).truncated)
    }

    func testDepthBoundMarksEvenAnOmittedLeafAsOutOfCoverage() {
        let maxDepth = 3
        let leafAtFrontierDepth = maxDepth
        XCTAssertFalse(depthIsIncluded(leafAtFrontierDepth, maximum: maxDepth))
        XCTAssertTrue(depthIsIncluded(maxDepth - 1, maximum: maxDepth))
    }
}
