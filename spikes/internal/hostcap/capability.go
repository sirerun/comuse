// Package hostcap defines the import-restricted capability required for the
// trusted host input transport. Go package boundaries are not a sandbox; only
// trusted in-process composition under spikes may mint this capability.
package hostcap

type seal struct{}

var trustedSeal = &seal{}

// Capability is opaque outside this package and cannot be forged with its
// zero value. The package is under Go's internal import restriction.
type Capability struct{ seal *seal }

// New is available only to code beneath the spikes module directory. Keep its
// use in the private input composition; CLI and MCP adapters remain readonly.
func New() Capability { return Capability{seal: trustedSeal} }

// Valid reports whether capability was minted by this package.
func Valid(capability Capability) bool { return capability.seal == trustedSeal }
