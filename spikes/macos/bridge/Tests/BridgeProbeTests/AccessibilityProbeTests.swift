import Foundation
import Testing
@testable import BridgeProbe

@Suite(.serialized)
@MainActor
struct AccessibilityProbeTests {
    @Test
    func doctorReportsIndependentNonPromptingPermissions() throws {
        let response = try decode(#"{"schema_version":1,"request_id":"doctor-1","op":"doctor"}"#)
        #expect(response["schema_version"] as? Int == 1)
        #expect(response["request_id"] as? String == "doctor-1")
        #expect(response["status"] as? String == "completed")
        #expect(response["error"] is NSNull)
        let result = try #require(response["result"] as? [String: Any])
        let accessibility = try #require(result["accessibility"] as? [String: Any])
        let eventPosting = try #require(result["event_posting"] as? [String: Any])
        #expect(accessibility["available"] is Bool)
        #expect(eventPosting["available"] is Bool)
        #expect(accessibility["prompted"] as? Bool == false)
        #expect(eventPosting["prompted"] as? Bool == false)
    }

    @Test
    func fixtureOperationsRequireScopeAndRejectOtherBundleBeforeAX() throws {
        let missingScope = try decode(#"{"schema_version":1,"request_id":"windows-1","op":"windows"}"#)
        #expect(missingScope["status"] as? String == "error")
        #expect(missingScope["error"] as? String == "scope_required")
        #expect(missingScope["result"] is NSNull)

        let foreignScope = #"{"schema_version":1,"request_id":"windows-2","op":"windows","scope":{"pid":1,"bundle_id":"com.apple.finder","fixture_nonce":"n"}}"#
        let denied = try decode(foreignScope)
        #expect(denied["status"] as? String == "error")
        #expect(denied["error"] as? String == "scope_or_permission_denied")
        #expect(denied["result"] is NSNull)
    }

    @Test
    func accessibilityObservationRequiresWindowReference() throws {
        let request = #"{"schema_version":1,"request_id":"a11y-1","op":"a11y","scope":{"pid":123,"bundle_id":"com.sirerun.comuse.fixture","fixture_nonce":"nonce-1"}}"#
        let response = try decode(request)
        #expect(response["status"] as? String == "error")
        #expect(response["error"] as? String == "invalid_request")
        #expect(response["result"] is NSNull)
    }

    @Test
    func malformedAndOversizedPayloadsAreBounded() throws {
        let malformed = try decode("{}")
        #expect(malformed["status"] as? String == "error")
        #expect(malformed["error"] as? String == "invalid_request")
        #expect(malformed["result"] is NSNull)

        let oversized = handleAccessibilityProbe(Data(repeating: 0x20, count: 32 * 1024 + 1))
        let object = try #require(JSONSerialization.jsonObject(with: oversized) as? [String: Any])
        #expect(object["status"] as? String == "error")
        #expect(object["error"] as? String == "request_limit_exceeded")
        #expect(object["result"] is NSNull)
    }

    private func decode(_ request: String) throws -> [String: Any] {
        let data = handleAccessibilityProbe(Data(request.utf8))
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }
}
