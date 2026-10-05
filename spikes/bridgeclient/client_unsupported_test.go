//go:build !darwin || !cgo

package bridgeclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

func TestUnsupportedPlatformNeverFabricatesBridgeSuccess(t *testing.T) {
	client, err := Open("unused")
	if !errors.Is(err, ErrUnsupported) || client != nil {
		t.Fatalf("Open client=%v err=%v", client, err)
	}
	client = &Client{}
	if _, err := client.Call(context.Background(), []byte(`{"schema_version":1,"request_id":"r","op":"hello"}`)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Call err=%v", err)
	}
	if _, err := client.InputCall(context.Background(), HostInputRequest{}, hostcap.Capability{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("InputCall err=%v", err)
	}
	if err := client.Pump(time.Millisecond); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Pump err=%v", err)
	}
	if err := client.Close(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Close err=%v", err)
	}
}
