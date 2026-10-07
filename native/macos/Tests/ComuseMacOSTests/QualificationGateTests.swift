#if COMUSE_QUALIFICATION_FIXTURES
import XCTest
@testable import ComuseMacOS

final class QualificationGateTests: XCTestCase {
    func testQualificationFixtureBuildCannotEnableProductionDispatch() async throws {
        try await MainActor.run {
            let runtime = NativeRuntime(id: 900,
                config: NativeConfig(schemaVersion: 1, scope: NativeScope(processes: [], expiresAtUnixMilli: 1), allowValues: false), processes: [])
            let decodedRequest = try decodeNativeRequest(Data(#"{"schema_version":1,"request_id":"fixture","operation":"execute","action":{"id":"a1","window_ref":"w1","element_ref":"e1","state_id":"s1","kind":"press"}}"#.utf8))
            let action = try XCTUnwrap(decodedRequest.action)
            let request = NativeRequest(schemaVersion: 1, requestID: "fixture", operation: "execute", windowRef: nil, elementRef: nil, stateID: nil, budget: nil, action: action)
            XCTAssertThrowsError(try runtime.executeScopedAction(request, requestID: 0)) { error in
                XCTAssertEqual((error as? ProbeFailure)?.code, "unsupported")
            }
        }
    }
}
#endif
