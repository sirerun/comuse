//go:build !darwin || !cgo

package bridgeclient

import (
	"context"
	"errors"
	"time"
)

var ErrUnsupported = errors.New("native Swift bridge requires macOS with cgo enabled")

type Client struct{}

func Open(string) (*Client, error)                           { return nil, ErrUnsupported }
func (*Client) Call(context.Context, []byte) ([]byte, error) { return nil, ErrUnsupported }
func (*Client) Pump(time.Duration) error                     { return ErrUnsupported }
func (*Client) Close(context.Context) error                  { return ErrUnsupported }
