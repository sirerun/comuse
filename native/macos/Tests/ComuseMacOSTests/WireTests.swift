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
}
