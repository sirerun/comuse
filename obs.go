// Package comuse provides bounded semantic sessions over a trusted native host.
package comuse

import (
	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/semantic"
	"time"
)

const SchemaVersion = 1

type Backend = backend.Backend
type Error = backend.Error
type ProcessIdentity = backend.ProcessIdentity
type Scope = backend.Scope
type Budget = backend.Budget
type Capabilities = backend.Capabilities
type DoctorReport = backend.Doctor
type Window = backend.Window
type Element = backend.Element
type Coverage = backend.Coverage
type Observation = backend.Snapshot
type ElementContent = backend.ElementContent

func ErrorCode(err error) string { return backend.ErrorCode(err) }

type Envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Result        any    `json:"result,omitempty"`
	Error         *Error `json:"error,omitempty"`
}

// Canonical semantic records are additive to the legacy Observation DTO.
type SemanticSnapshot = semantic.Snapshot
type SemanticDelta = semantic.Delta
type SemanticUnchanged = semantic.Unchanged
type SemanticMetadata = semantic.Metadata
type SemanticNode = semantic.Node
type SemanticContext = semantic.Context
type SemanticCoverage = semantic.Coverage
type ReadResult struct {
	Kind       string    `json:"kind"`
	WindowRef  string    `json:"window_ref"`
	ElementRef string    `json:"element_ref"`
	StateID    string    `json:"state_id"`
	ObservedAt time.Time `json:"observed_at"`
	Text       string    `json:"text"`
	Truncated  bool      `json:"truncated"`
}
type WaitResult struct {
	Condition    string  `json:"condition"`
	Satisfied    bool    `json:"satisfied"`
	ElapsedMS    uint64  `json:"elapsed_ms"`
	Reason       string  `json:"reason"`
	FinalStateID *string `json:"final_state_id"`
	WindowRef    *string `json:"window_ref"`
}
type FocusedWindow struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}
type CompactState struct {
	FocusedWindow     *FocusedWindow `json:"focused_window"`
	DisplayID         string         `json:"display_id"`
	DisplayGeneration uint64         `json:"display_generation"`
}
