//go:build darwin && cgo

package bridgeclient

import (
	"context"
	"testing"

	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

func TestInputCallRejectsUnmintedCapabilityBeforeNativeDispatch(t *testing.T) {
	client := &Client{}
	if _, err := client.InputCall(context.Background(), HostInputRequest{}, hostcap.Capability{}); err != ErrUntrustedInputCaller {
		t.Fatalf("zero capability err=%v", err)
	}
}
