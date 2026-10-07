package comuse

import _ "embed"

//go:embed docs/contracts/phase1-parity-v1.schema.json
var responseSchema []byte

// ResponseSchema returns a detached copy of the normative domain schema.
func ResponseSchema() []byte { return append([]byte(nil), responseSchema...) }

// RejectionEnvelope encodes a transport rejection with no valid action
// identity. Decode failures do not advance toolkit counters.
func RejectionEnvelope(code string) (ResultEnvelope, error) {
	fault, err := NewSafeError(code)
	if err != nil {
		return ResultEnvelope{}, err
	}
	state, _ := NewStatePayload(nil)
	observation, _ := NewObservationPayload(nil)
	result, _ := NewResultPayload(nil)
	return NewResultEnvelope(EnvelopeMetadata{Status: "error", OK: false, Action: "doctor", Execution: "not_applied", StateStatus: "unavailable", Verification: MetadataVerification{Status: "unavailable"}, Cleanup: "complete", Error: fault, CompletedSteps: []string{}}, state, observation, result)
}
