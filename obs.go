// Package comuse provides bounded semantic sessions over a trusted native host.
package comuse

import "github.com/sirerun/comuse/internal/backend"

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
