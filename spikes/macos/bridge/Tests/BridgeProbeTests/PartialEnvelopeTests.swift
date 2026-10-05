import Foundation
import Testing
@testable import BridgeProbe

@Suite(.serialized)
struct PartialEnvelopeTests {
    @Test
    func acceptsPartialEnvelopeWithResult() {
        let data = Data(#"{"schema_version":1,"request_id":"r1","status":"partial","error":null,"result":{"coverage":{"status":"partial"}}}"#.utf8)
        #expect(validProbeEnvelope(data, requestID: "r1"))
    }

    @Test
    func rejectsPartialEnvelopeWithoutResult() {
        let data = Data(#"{"schema_version":1,"request_id":"r1","status":"partial","error":null,"result":null}"#.utf8)
        #expect(!validProbeEnvelope(data, requestID: "r1"))
    }
}
