package comuse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/comuse"
	"github.com/sirerun/comuse/internal/backend"
	"github.com/sirerun/comuse/internal/cli"
	comusemcp "github.com/sirerun/comuse/mcp"
)

const ledgerURI = "comuse://session/ledger"

type ledgerMCPBackend struct {
	parityMCPBackend
	denied bool
}

func (b *ledgerMCPBackend) Doctor(context.Context) (backend.Doctor, error) {
	permission := "granted"
	accessibility := true
	if b.denied {
		permission = "denied"
		accessibility = false
	}
	return backend.Doctor{Capabilities: backend.Capabilities{Accessibility: accessibility}, Permissions: map[string]string{"accessibility": permission}}, nil
}

func newLedgerParitySession(t *testing.T, b comuse.Backend) *comuse.Session {
	t.Helper()
	session, err := comuse.NewSession(comuse.Config{
		Backend: b,
		Scope:   comuse.Scope{Processes: []comuse.ProcessIdentity{{PID: 88, BundleID: "com.example.fixture", LaunchID: "fixture-88"}}, ExpiresAt: time.Now().Add(time.Hour)},
		Budget:  comuse.Budget{MaxDepth: 8, MaxNodes: 64, MaxBytes: 16384, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(context.Background()); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return session
}

func TestMCPResourceListsReadsAndChargesOnlyCanonicalLedger(t *testing.T) {
	session := newLedgerParitySession(t, &ledgerMCPBackend{})
	client := parityMCPClient(t, comusemcp.NewServer(session))
	var resources []*sdk.Resource
	for resource, err := range client.Resources(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, resource)
	}
	if len(resources) != 1 || resources[0].URI != ledgerURI || resources[0].Name != "comuse_ledger" || resources[0].MIMEType != "application/json" {
		t.Fatalf("resources=%+v", resources)
	}
	read, err := client.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: ledgerURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || read.Contents[0].URI != ledgerURI || read.Contents[0].MIMEType != "application/json" {
		t.Fatalf("resource contents=%+v", read.Contents)
	}
	var resourceLedger comuse.LedgerSnapshot
	if err := json.Unmarshal([]byte(read.Contents[0].Text), &resourceLedger); err != nil {
		t.Fatal(err)
	}
	if resourceLedger.SerializedTextBytes != 0 {
		t.Fatalf("resource snapshot includes its own charge: %+v", resourceLedger)
	}
	canonical, err := json.Marshal(resourceLedger)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != read.Contents[0].Text {
		t.Fatalf("resource text is not canonical ledger JSON:\n%s\n%s", read.Contents[0].Text, canonical)
	}
	next, err := session.Ledger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next.SerializedTextBytes != uint64(len(read.Contents[0].Text)) {
		t.Fatalf("resource charged %d bytes, want only canonical content length %d", next.SerializedTextBytes, len(read.Contents[0].Text))
	}
	if resourceLedger.Session != next.Session || resourceLedger.Actions != next.Actions || resourceLedger.Observations != next.Observations || resourceLedger.SemanticResults != next.SemanticResults {
		t.Fatalf("resource and library snapshots differ: resource=%+v next=%+v", resourceLedger, next)
	}
}

func TestMCPResourceRejectsSelectorsAndReturnsSafeLifecycleRefusals(t *testing.T) {
	backend := &ledgerMCPBackend{}
	session := newLedgerParitySession(t, backend)
	readWithFreshClient := func(uri string) error {
		client := parityMCPClient(t, comusemcp.NewServer(session))
		_, err := client.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
		return err
	}
	for _, uri := range []string{ledgerURI + "?session=other", ledgerURI + "/other", "comuse://session/other"} {
		if err := readWithFreshClient(uri); err == nil {
			t.Errorf("foreign/query resource URI %q was accepted", uri)
		}
	}
	backend.denied = true
	if err := readWithFreshClient(ledgerURI); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("permission refusal = %v", err)
	}
	backend.denied = false
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := readWithFreshClient(ledgerURI); err == nil || !strings.Contains(err.Error(), "session_closed") {
		t.Fatalf("closed refusal = %v", err)
	}
}

type failRead struct{}

func (failRead) Read([]byte) (int, error) { return 0, errors.New("stdin must not be read") }

func TestCLIStateLedgerFlagUsesSharedDispatcherAndIgnoresStdin(t *testing.T) {
	session := newLedgerParitySession(t, &ledgerMCPBackend{})
	var output bytes.Buffer
	code := cli.Run(context.Background(), session, []string{"state", "--ledger"}, failRead{}, &output, io.Discard)
	if code != 0 {
		t.Fatalf("CLI exit=%d body=%s", code, output.String())
	}
	var envelope struct {
		Action string                `json:"action"`
		OK     bool                  `json:"ok"`
		Result comuse.LedgerSnapshot `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Action != "ledger" || envelope.Result.Session == "" {
		t.Fatalf("CLI did not return shared ledger envelope: %s", output.String())
	}
}
