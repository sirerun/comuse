//go:build !darwin || !cgo

package darwin

import (
	"context"

	"github.com/sirerun/comuse/internal/backend"
)

var ErrUnsupported = &backend.Error{Code: "unsupported", Message: "The macOS native backend requires Darwin with cgo."}

func Run(context.Context, backend.Config, func(backend.Backend) error) error { return ErrUnsupported }

// ResolvedScope is available only during a successfully opened native Run callback.
func ResolvedScope(backend.Backend) (backend.Scope, error) { return backend.Scope{}, ErrUnsupported }
