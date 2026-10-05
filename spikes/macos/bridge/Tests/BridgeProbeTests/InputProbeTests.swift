import Foundation
import Testing
@testable import BridgeProbe

@Suite(.serialized)
@MainActor
struct InputProbeTests {
    @Test
    func invalidRequestsAreRejectedBeforeAnyNativeAction() throws {
        let response = try decode("{}")
        #expect(response["execution"] as? String == "not_applied")
        #expect(response["error"] as? String == "validation_error")
        #expect(response["result"] is NSNull)

        let oversized = handleInputProbe(Data(repeating: 0x20, count: 32 * 1024 + 1))
        let oversizedResponse = try #require(JSONSerialization.jsonObject(with: oversized) as? [String: Any])
        #expect(oversizedResponse["execution"] as? String == "not_applied")
        #expect(oversizedResponse["error"] as? String == "validation_error")
        #expect(oversizedResponse["result"] is NSNull)
    }

    @Test
    func nativeMutationRoutingIsClosedUntilHostAdmissionIntegration() throws {
        let request = #"{"schema_version":1,"request_id":"input-1","action_id":"action-1","op":"replace","scope":{"pid":123,"bundle_id":"com.sirerun.comuse.fixture","fixture_nonce":"nonce-1","process_start_ref":"process-ref","window_ref":"window-ref","element_ref":"element-ref","expected_state_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},"text":"replacement"}"#
        let response = try decode(request)
        #expect(response["request_id"] as? String == "input-1")
        #expect(response["action_id"] as? String == "action-1")
        #expect(response["action"] as? String == "replace")
        #expect(response["execution"] as? String == "not_applied")
        #expect(response["cleanup"] as? [String: String] == ["status": "not_required"])
        #expect(response["error"] as? String == "unsupported")
        #expect(response["result"] is NSNull)
    }

    private func decode(_ request: String) throws -> [String: Any] {
        let data = handleInputProbe(Data(request.utf8))
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }
}
