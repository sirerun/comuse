//go:build !darwin || !cgo

package bridgeclient

import (
	"context"
	"time"

	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

type Client struct{}

func Open(string) (*Client, error)                           { return nil, ErrUnsupported }
func (*Client) Call(context.Context, []byte) ([]byte, error) { return nil, ErrUnsupported }
func (*Client) InputCall(context.Context, HostInputRequest, hostcap.Capability) ([]byte, error) {
	return nil, ErrUnsupported
}
func (*Client) Pump(time.Duration) error    { return ErrUnsupported }
func (*Client) Close(context.Context) error { return ErrUnsupported }
