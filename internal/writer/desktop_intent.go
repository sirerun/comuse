package writer

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const desktopIntentVersion = 1
const maxDesktopIntentBytes = 256

type desktopIntent struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	Binding string `json:"binding"`
}

// Begin durably records that this desktop lease is about to perform a native
// operation. Call it before any native event or mutation.
func (l *DesktopLease) Begin(journalBinding [32]byte) error {
	if l == nil || l.closed || l.lock == nil {
		return ErrClosed
	}
	if l.beginFailed {
		return errors.New("desktop intent persistence previously failed")
	}
	if l.intent != nil {
		return errors.New("desktop intent already begun")
	}
	if journalBinding == ([32]byte{}) {
		return errors.New("desktop journal binding is required")
	}
	dirtyPath := filepath.Join(l.root, fmt.Sprintf("desktop-%d-%d.dirty", l.identity.uid, l.identity.sessionID))
	if err := verifyExistingRegular(dirtyPath, l.identity.uid); err != nil {
		return err
	}
	if _, err := os.Lstat(dirtyPath); err == nil {
		return ErrDirty
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect desktop dirty marker: %w", err)
	}
	path := desktopIntentPath(l.root, l.identity)
	if err := verifyExistingRegular(path, l.identity.uid); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return ErrDirty
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect desktop intent marker: %w", err)
	}
	marker := desktopIntent{Version: desktopIntentVersion, Binding: hex.EncodeToString(journalBinding[:])}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("create desktop intent token: %w", err)
	}
	marker.Token = hex.EncodeToString(token[:])
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if len(data) > maxDesktopIntentBytes {
		return errors.New("desktop intent marker exceeds size limit")
	}
	if err := atomicWrite(path, data); err != nil {
		l.beginFailed = true
		return err
	}
	if err := syncDesktopDirectory(l.root); err != nil {
		l.beginFailed = true
		return err
	}
	l.intent = &marker
	return nil
}

// Complete clears only the exact durable intent created by this lease. The
// trusted caller must first establish a known terminal result and durable
// journal settlement.
func (l *DesktopLease) Complete() error {
	if l == nil || l.closed || l.lock == nil {
		return ErrClosed
	}
	if l.intent == nil || l.beginFailed {
		return errors.New("desktop lease has no completable intent")
	}
	path := desktopIntentPath(l.root, l.identity)
	marker, err := readDesktopIntent(path, l.identity.uid)
	if err != nil {
		return err
	}
	if marker != *l.intent {
		return ErrDirty
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove desktop intent marker: %w", err)
	}
	if err := syncDesktopDirectory(l.root); err != nil {
		// Restore ownership on the best-effort path so a failed durability
		// operation cannot turn into permission to perform another action.
		restoreErr := atomicWrite(path, mustMarshalDesktopIntent(marker))
		if restoreErr == nil {
			restoreErr = syncDesktopDirectory(l.root)
		}
		return errors.Join(err, restoreErr)
	}
	l.intent = nil
	return nil
}

// DesktopJournalBinding binds recovery intent to the exact existing private
// journal root and the identity of its secret key. It does not create or
// modify the journal directory.
func DesktopJournalBinding(root string, key []byte) ([32]byte, error) {
	var zero [32]byte
	if len(key) < 32 {
		return zero, errors.New("desktop journal key must contain at least 32 bytes")
	}
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, 0) {
		return zero, errors.New("desktop journal root must be a canonical absolute path")
	}
	if err := verifyCanonicalJournalRoot(root, uint32(os.Getuid())); err != nil {
		return zero, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("comuse-desktop-journal-binding-v1\x00"))
	_, _ = mac.Write([]byte(root))
	copy(zero[:], mac.Sum(nil))
	if zero == ([32]byte{}) {
		return [32]byte{}, errors.New("desktop journal binding is invalid")
	}
	return zero, nil
}

// ReconcileDesktop is the trusted host recovery entry point. On supported
// hosts it resolves the OS GUI identity and application-owned state root; it
// accepts no caller-supplied identity or path.
func ReconcileDesktop(ctx context.Context, journalBinding [32]byte, verify func(context.Context) error) error {
	identity, err := resolveDesktopIdentity()
	if err != nil {
		return ErrDesktopIdentityUnavailable
	}
	root, err := desktopRoot(identity)
	if err != nil {
		return ErrDesktopIdentityUnavailable
	}
	if err := secureDesktopRoot(root, identity.uid); err != nil {
		return fmt.Errorf("protect canonical desktop state: %w", err)
	}
	return reconcileDesktopAt(ctx, identity, root, journalBinding, verify)
}

// reconcileDesktopAt is private so synthetic identity and root injection stay
// confined to package tests.
func reconcileDesktopAt(ctx context.Context, identity desktopIdentity, root string, journalBinding [32]byte, verify func(context.Context) error) error {
	if journalBinding == ([32]byte{}) {
		return errors.New("desktop journal binding is required")
	}
	if verify == nil {
		return errors.New("desktop verifier is required")
	}
	lease, err := acquireDesktopForRecoveryAt(ctx, identity, root)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Close() }()
	path := desktopIntentPath(root, identity)
	marker, err := readDesktopIntent(path, identity.uid)
	if err != nil {
		return err
	}
	if marker.Binding != hex.EncodeToString(journalBinding[:]) {
		return ErrDirty
	}
	lease.intent = &marker
	if err := verify(ctx); err != nil {
		return err
	}
	if err := lease.Complete(); err != nil {
		return err
	}
	return lease.Close()
}

func desktopIntentPath(root string, identity desktopIdentity) string {
	key := fmt.Sprintf("%d-%d", identity.uid, identity.sessionID)
	return filepath.Join(root, "desktop-"+key+".intent")
}

func syncDesktopDirectory(root string) error {
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("open desktop state directory: %w", err)
	}
	return errors.Join(wrapSyncError(directory.Sync()), wrapCloseError(directory.Close()))
}

func readDesktopIntent(path string, uid uint32) (desktopIntent, error) {
	var marker desktopIntent
	if err := verifyExistingRegular(path, uid); err != nil {
		return marker, err
	}
	file, err := os.Open(path)
	if err != nil {
		return marker, fmt.Errorf("open desktop intent marker: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return marker, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDesktopIntentBytes {
		return marker, errors.New("desktop intent marker has invalid size or type")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDesktopIntentBytes+1))
	if err != nil {
		return marker, fmt.Errorf("read desktop intent marker: %w", err)
	}
	if len(data) > maxDesktopIntentBytes {
		return marker, errors.New("desktop intent marker exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return desktopIntent{}, errors.New("desktop intent marker is corrupt")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return desktopIntent{}, errors.New("desktop intent marker has trailing data")
	}
	canonical, err := json.Marshal(marker)
	if err != nil || string(canonical) != string(data) || marker.Version != desktopIntentVersion || !validIntentHex(marker.Token, 32) || !validIntentHex(marker.Binding, 32) {
		return desktopIntent{}, errors.New("desktop intent marker is not canonical")
	}
	return marker, nil
}

func validIntentHex(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func mustMarshalDesktopIntent(marker desktopIntent) []byte {
	data, _ := json.Marshal(marker)
	return data
}

func verifyCanonicalJournalRoot(root string, uid uint32) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(root, string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errors.New("desktop journal root contains an invalid component")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect desktop journal root: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("desktop journal root must contain real directories")
		}
	}
	if err := verifyPrivateStateRoot(root, uid); err != nil {
		return fmt.Errorf("validate existing writer root: %w", err)
	}
	return nil
}
