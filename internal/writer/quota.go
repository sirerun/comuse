package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirerun/comuse/internal/jsonwire"
)

const (
	MaxActionsPerMinute = 60
	quotaWindow         = time.Minute
	quotaRetention      = 61 * time.Second
	maxQuotaBytes       = 16 * 1024
	// A full burst can expire at60s while still retained for the61s grace.
	maxQuotaRecords = 2 * MaxActionsPerMinute
)

var ErrQuotaExhausted = errors.New("desktop action quota exhausted")

type quotaReservation struct {
	Commitment string `json:"commitment"`
	AtMillis   int64  `json:"at_ms"`
}

type quotaDiskState struct {
	Version         int                `json:"version"`
	WatermarkMillis int64              `json:"watermark_ms"`
	Reservations    []quotaReservation `json:"reservations"`
}

// QuotaStore holds durable UID-scoped admission metadata. It stores only a
// cryptographic action commitment and an admission timestamp for each slot.
type QuotaStore struct {
	root string
	uid  uint32
}

func OpenQuotaStore() (*QuotaStore, error) {
	identity, err := resolveDesktopIdentity()
	if err != nil {
		return nil, ErrDesktopIdentityUnavailable
	}
	root, err := desktopRoot(identity)
	if err != nil {
		return nil, ErrDesktopIdentityUnavailable
	}
	if err := secureDesktopRoot(root, identity.uid); err != nil {
		return nil, fmt.Errorf("protect canonical desktop quota root: %w", err)
	}
	return &QuotaStore{root: root, uid: identity.uid}, nil
}

func newQuotaStoreAt(root string, uid uint32) *QuotaStore {
	return &QuotaStore{root: root, uid: uid}
}

// Reserve admits one policy-approved unique attempt before host approval.
// The 32-byte commitment must be derived by trusted core code from the
// action identity and binding; action text and secret values are never stored.
func (q *QuotaStore) Reserve(ctx context.Context, commitment [32]byte) error {
	return q.reserveAt(ctx, commitment, time.Now())
}

func (q *QuotaStore) reserveAt(ctx context.Context, commitment [32]byte, now time.Time) (returnedErr error) {
	if ctx == nil || q == nil || q.uid == 0 || !filepath.IsAbs(q.root) {
		return errors.New("quota store is not safely initialized")
	}
	if err := verifyPrivateStateRoot(q.root, q.uid); err != nil {
		return err
	}
	lockPath := filepath.Join(q.root, fmt.Sprintf("quota-%d.lock", q.uid))
	statePath := filepath.Join(q.root, fmt.Sprintf("quota-%d.json", q.uid))
	if err := verifyExistingRegular(lockPath, q.uid); err != nil {
		return err
	}
	if err := verifyExistingRegular(statePath, q.uid); err != nil {
		return err
	}
	lockFile, err := openLockFile(lockPath)
	if err != nil {
		return fmt.Errorf("open UID quota lock: %w", err)
	}
	defer func() { returnedErr = errors.Join(returnedErr, lockFile.Close()) }()
	info, err := lockFile.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("UID quota lock is not a regular file")
	}
	if err := verifyUIDOwner(info, q.uid); err != nil {
		return err
	}
	if err := lockFile.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict UID quota lock: %w", err)
	}
	if err := acquireFileLock(ctx, lockFile); err != nil {
		return err
	}
	defer func() { returnedErr = errors.Join(returnedErr, releaseFileLock(lockFile)) }()

	state, err := loadQuotaState(statePath, q.uid)
	if err != nil {
		return err
	}
	if now.IsZero() {
		return errors.New("quota clock is unavailable")
	}
	nowMillis := now.UTC().UnixMilli()
	if nowMillis < 0 {
		return errors.New("quota clock is invalid")
	}
	if nowMillis < state.WatermarkMillis {
		nowMillis = state.WatermarkMillis
	}
	state.WatermarkMillis = nowMillis
	cutoff := nowMillis - quotaRetention.Milliseconds()
	kept := state.Reservations[:0]
	for _, reservation := range state.Reservations {
		if reservation.AtMillis > cutoff {
			kept = append(kept, reservation)
		}
	}
	state.Reservations = kept
	commitmentText := hex.EncodeToString(commitment[:])
	for _, reservation := range state.Reservations {
		if reservation.Commitment == commitmentText {
			return saveQuotaState(statePath, q.uid, state)
		}
	}
	activeCutoff := nowMillis - quotaWindow.Milliseconds()
	active := 0
	for _, reservation := range state.Reservations {
		if reservation.AtMillis > activeCutoff {
			active++
		}
	}
	if active >= MaxActionsPerMinute {
		if err := saveQuotaState(statePath, q.uid, state); err != nil {
			return err
		}
		return ErrQuotaExhausted
	}
	state.Reservations = append(state.Reservations, quotaReservation{Commitment: commitmentText, AtMillis: nowMillis})
	if err := saveQuotaState(statePath, q.uid, state); err != nil {
		return err
	}
	return nil
}

func loadQuotaState(path string, uid uint32) (result quotaDiskState, returnedErr error) {
	state := quotaDiskState{Version: 1, Reservations: []quotaReservation{}}
	file, err := openLedgerFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return quotaDiskState{}, fmt.Errorf("open durable UID quota: %w", err)
	}
	defer func() { returnedErr = errors.Join(returnedErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxQuotaBytes {
		return quotaDiskState{}, errors.New("durable UID quota has invalid type or size")
	}
	if err := verifyUIDOwner(info, uid); err != nil {
		return quotaDiskState{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return quotaDiskState{}, errors.New("durable UID quota permissions are not private")
	}
	var wire struct {
		Version         *int   `json:"version"`
		WatermarkMillis *int64 `json:"watermark_ms"`
		Reservations    *[]struct {
			Commitment *string `json:"commitment"`
			AtMillis   *int64  `json:"at_ms"`
		} `json:"reservations"`
	}
	if err := jsonwire.Decode(file, maxQuotaBytes, &wire); err != nil || wire.Version == nil || wire.WatermarkMillis == nil || wire.Reservations == nil {
		return quotaDiskState{}, errors.New("durable UID quota is unreadable or corrupt")
	}
	state = quotaDiskState{Version: *wire.Version, WatermarkMillis: *wire.WatermarkMillis, Reservations: make([]quotaReservation, 0, len(*wire.Reservations))}
	for _, record := range *wire.Reservations {
		if record.Commitment == nil || record.AtMillis == nil {
			return quotaDiskState{}, errors.New("durable UID quota record is incomplete")
		}
		state.Reservations = append(state.Reservations, quotaReservation{Commitment: *record.Commitment, AtMillis: *record.AtMillis})
	}
	if state.Version != 1 || state.WatermarkMillis < 0 || len(state.Reservations) > maxQuotaRecords || state.Reservations == nil {
		return quotaDiskState{}, errors.New("durable UID quota is outside supported bounds")
	}
	seen := make(map[string]struct{}, len(state.Reservations))
	for _, reservation := range state.Reservations {
		if len(reservation.Commitment) != sha256.Size*2 || strings.ToLower(reservation.Commitment) != reservation.Commitment || reservation.AtMillis < 0 || reservation.AtMillis > state.WatermarkMillis {
			return quotaDiskState{}, errors.New("durable UID quota record is invalid")
		}
		if _, err := hex.DecodeString(reservation.Commitment); err != nil {
			return quotaDiskState{}, errors.New("durable UID quota commitment is invalid")
		}
		if _, exists := seen[reservation.Commitment]; exists {
			return quotaDiskState{}, errors.New("durable UID quota has duplicate commitments")
		}
		seen[reservation.Commitment] = struct{}{}
	}
	return state, nil
}

func saveQuotaState(path string, uid uint32, state quotaDiskState) error {
	if len(state.Reservations) > maxQuotaRecords {
		return errors.New("durable UID quota exceeds record bound")
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxQuotaBytes {
		return errors.New("durable UID quota exceeds size bound")
	}
	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("persist durable UID quota: %w", err)
	}
	return verifyExistingRegular(path, uid)
}
