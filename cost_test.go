package comuse

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLedgerCountersSaturateAndRejectInvalidUpdates(t *testing.T) {
	ledger := NewLedger()
	if got, err := ledger.Add(CounterActions, JSONSafeIntegerMax-2); err != nil || got != JSONSafeIntegerMax-2 {
		t.Fatalf("initial action increment = %d, %v", got, err)
	}
	if got, err := ledger.Add(CounterActions, 100); err != nil || got != JSONSafeIntegerMax {
		t.Fatalf("overflow action increment = %d, %v", got, err)
	}
	if _, err := ledger.Add(0, 1); !errors.Is(err, ErrUnknownCounter) {
		t.Fatalf("unknown counter error = %v", err)
	}
	if err := ledger.SetRetainedBytes(maxRetainedBytes + 1); !errors.Is(err, ErrRetainedBytes) {
		t.Fatalf("oversize retained bytes error = %v", err)
	}
	if got := ledger.Snapshot("session").RetainedBytes; got != 0 {
		t.Fatalf("rejected gauge update changed value: %d", got)
	}
}

func TestLedgerConcurrentCountersAreMonotonic(t *testing.T) {
	ledger := NewLedger()
	const workers, increments = 16, 1000
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				if _, err := ledger.Add(CounterObservationA11y, 1); err != nil {
					t.Errorf("Add: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := ledger.Snapshot("s").Observations.A11y, uint64(workers*increments); got != want {
		t.Fatalf("a11y observations = %d, want %d", got, want)
	}
}

func TestLedgerSnapshotIsDetachedAndElapsedNeverMovesBack(t *testing.T) {
	ledger := NewLedger()
	first := ledger.Snapshot("opaque-session")
	ledger.AdvanceElapsed(3 * time.Second)
	ledger.AdvanceElapsed(time.Second)
	if first.ElapsedMS != 0 {
		t.Fatalf("old snapshot changed: %+v", first)
	}
	if got := ledger.Snapshot("opaque-session").ElapsedMS; got < 3000 {
		t.Fatalf("elapsed_ms regressed: %d", got)
	}
}

func TestStandaloneLedgerSnapshotChargeUsesPrechargeValue(t *testing.T) {
	ledger := NewLedger()
	precharge := ledger.Snapshot("session")
	encoded, err := canonicalJSON(precharge)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := ledger.ChargeLedgerSnapshot(precharge)
	if err != nil {
		t.Fatal(err)
	}
	if charged != uint64(len(encoded)) {
		t.Fatalf("ledger charge = %d, want %d", charged, len(encoded))
	}
	if precharge.SerializedTextBytes != 0 {
		t.Fatalf("precharge snapshot mutated: %+v", precharge)
	}
	if got := ledger.Snapshot("session").SerializedTextBytes; got != charged {
		t.Fatalf("cumulative bytes = %d, want %d", got, charged)
	}
}
