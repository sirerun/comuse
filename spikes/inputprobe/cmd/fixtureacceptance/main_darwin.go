//go:build darwin && cgo

package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sirerun/comuse/spikes/inputprobe"
	"github.com/sirerun/comuse/spikes/internal/hostcap"
)

const (
	maxConfigBytes = 16 << 10
	maxReportBytes = 4 << 10
	keyBytes       = 32
	callTimeout    = 2 * time.Minute
)

func init() {
	runtime.LockOSThread()
}

type trustedConfig struct {
	SchemaVersion      int                        `json:"schema_version"`
	Scenario           inputprobe.FixtureScenario `json:"scenario"`
	LibraryPath        string                     `json:"library_path"`
	FixturePID         int64                      `json:"fixture_pid"`
	FixtureNonce       string                     `json:"fixture_nonce"`
	PrivateJournalRoot string                     `json:"private_journal_root"`
	JournalKeyFile     string                     `json:"journal_key_file"`
	CommitmentKeyFile  string                     `json:"action_commitment_key_file"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fixtureacceptance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to a private, trusted JSON configuration")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *configPath == "" {
		writeStderr(stderr, "usage: fixtureacceptance --config <private-json>")
		return 64
	}

	diskConfig, err := readTrustedConfig(*configPath)
	if err != nil {
		writeStderr(stderr, "fixture acceptance configuration refused")
		return 64
	}
	journalKey, err := readPrivateKey(diskConfig.JournalKeyFile)
	if err != nil {
		writeStderr(stderr, "fixture acceptance key files refused")
		return 64
	}
	defer clearBytes(journalKey)
	commitmentKey, err := readPrivateKey(diskConfig.CommitmentKeyFile)
	if err != nil {
		writeStderr(stderr, "fixture acceptance key files refused")
		return 64
	}
	defer clearBytes(commitmentKey)
	if err := validateDistinctKeys(diskConfig.JournalKeyFile, diskConfig.CommitmentKeyFile, journalKey, commitmentKey); err != nil {
		writeStderr(stderr, "fixture acceptance key files refused")
		return 64
	}
	if err := validatePrivateDirectory(diskConfig.PrivateJournalRoot); err != nil {
		writeStderr(stderr, "fixture acceptance journal directory refused")
		return 64
	}
	config := inputprobe.FixtureAcceptanceConfig{
		Scenario:            diskConfig.Scenario,
		LibraryPath:         diskConfig.LibraryPath,
		PID:                 int32(diskConfig.FixturePID),
		FixtureNonce:        diskConfig.FixtureNonce,
		PrivateJournalRoot:  diskConfig.PrivateJournalRoot,
		JournalKey:          journalKey,
		ActionCommitmentKey: commitmentKey,
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	report, callErr := inputprobe.RunFixtureAcceptance(ctx, hostcap.New(), config)
	if err := writeBoundedReport(stdout, report); err != nil {
		writeStderr(stderr, "fixture acceptance report refused")
		return 70
	}
	if callErr != nil {
		writeStderr(stderr, "fixture acceptance held")
		return 75
	}
	switch report.Status {
	case "completed":
		return 0
	case "partial":
		return 2
	default:
		return 75
	}
}

func writeStderr(writer io.Writer, message string) {
	_, _ = fmt.Fprintln(writer, message)
}

func readTrustedConfig(path string) (trustedConfig, error) {
	var config trustedConfig
	if !filepath.IsAbs(path) || validatePrivateDirectory(filepath.Dir(path)) != nil {
		return config, errors.New("private config path required")
	}
	data, err := readPrivateRegularFile(path, maxConfigBytes)
	if err != nil {
		return config, err
	}
	if err := rejectDuplicateConfigKeys(data); err != nil {
		return config, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return trustedConfig{}, errors.New("invalid trusted config")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return trustedConfig{}, errors.New("trailing trusted config data")
	}
	if err := validateTrustedConfig(config, path); err != nil {
		return trustedConfig{}, err
	}
	return config, nil
}

func validateTrustedConfig(config trustedConfig, configPath string) error {
	if config.SchemaVersion != 1 {
		return errors.New("unsupported trusted config version")
	}
	switch config.Scenario {
	case inputprobe.FixtureReadNormalValue, inputprobe.FixtureReplaceNormalText, inputprobe.FixturePressCounter:
	default:
		return errors.New("unsupported fixture scenario")
	}
	if config.FixturePID <= 0 || config.FixturePID > int64(^uint32(0)>>1) || !validNonce(config.FixtureNonce) ||
		!filepath.IsAbs(config.LibraryPath) || !filepath.IsAbs(config.PrivateJournalRoot) {
		return errors.New("invalid fixture target config")
	}
	for _, filePath := range []string{config.JournalKeyFile, config.CommitmentKeyFile} {
		if !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath || filepath.Clean(filePath) == filepath.Clean(configPath) {
			return errors.New("invalid private key path")
		}
	}
	return nil
}

func validateDistinctKeys(journalPath, commitmentPath string, journalKey, commitmentKey []byte) error {
	if filepath.Clean(journalPath) == filepath.Clean(commitmentPath) ||
		len(journalKey) != keyBytes || len(commitmentKey) != keyBytes || subtle.ConstantTimeCompare(journalKey, commitmentKey) == 1 {
		return errors.New("journal and commitment keys must be distinct")
	}
	return nil
}

func rejectDuplicateConfigKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("trusted config must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("invalid trusted config object")
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("invalid trusted config key")
		}
		if _, exists := seen[key]; exists {
			return errors.New("duplicate trusted config key")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("invalid trusted config value")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("invalid trusted config object ending")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing trusted config data")
	}
	return nil
}

func readPrivateKey(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || validatePrivateDirectory(filepath.Dir(path)) != nil {
		return nil, errors.New("private key path required")
	}
	data, err := readPrivateRegularFile(path, keyBytes)
	if err != nil || len(data) != keyBytes {
		clearBytes(data)
		return nil, errors.New("exact 32-byte private key required")
	}
	return data, nil
}

func readPrivateRegularFile(path string, maxBytes int64) (data []byte, resultErr error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("private file open failed")
	}
	file := os.NewFile(uintptr(fd), "private-input")
	defer func() {
		if err := file.Close(); err != nil && resultErr == nil {
			clearBytes(data)
			data = nil
			resultErr = errors.New("private file close failed")
		}
	}()
	info, err := file.Stat()
	if err != nil || validatePrivateFileInfo(info, maxBytes, uint32(syscall.Getuid())) != nil {
		return nil, errors.New("private regular file with bounded size required")
	}
	data, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		clearBytes(data)
		return nil, errors.New("private file read failed")
	}
	return data, nil
}

func validatePrivateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("absolute clean directory required")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || validatePrivateDirectoryInfo(info, uint32(syscall.Getuid())) != nil {
		return errors.New("private directory mode required")
	}
	return nil
}

func validatePrivateFileInfo(info os.FileInfo, maxBytes int64, owner uint32) error {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() > maxBytes {
		return errors.New("private regular file with bounded size required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return errors.New("private file owner mismatch")
	}
	return nil
}

func validatePrivateDirectoryInfo(info os.FileInfo, owner uint32) error {
	if info == nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("private directory mode required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return errors.New("private directory owner mismatch")
	}
	return nil
}

func validNonce(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._-", char) {
			continue
		}
		return false
	}
	return true
}

func writeBoundedReport(writer io.Writer, report inputprobe.FixtureAcceptanceReport) error {
	if report.SchemaVersion != 1 || !validReportStatus(report.Status) || !validScenario(report.Scenario) ||
		!validReportValue(report.Execution) || !validReportValue(report.Verification) ||
		!validReportValue(report.StateStatus) || !validReportValue(report.Cleanup) ||
		(report.ErrorCode != "" && !safeCode(report.ErrorCode)) {
		return errors.New("unsafe acceptance report")
	}
	data, err := json.Marshal(report)
	if err != nil || len(data)+1 > maxReportBytes {
		return errors.New("acceptance report exceeds output bound")
	}
	data = append(data, '\n')
	return writeBoundedBytes(writer, data)
}

func writeBoundedBytes(writer io.Writer, data []byte) error {
	if len(data) > maxReportBytes {
		return errors.New("acceptance report exceeds output bound")
	}
	_, err := io.Copy(writer, bytes.NewReader(data))
	return err
}

func validReportStatus(value string) bool {
	return value == "completed" || value == "partial" || value == "held"
}

func validScenario(value inputprobe.FixtureScenario) bool {
	return value == inputprobe.FixtureReadNormalValue || value == inputprobe.FixtureReplaceNormalText || value == inputprobe.FixturePressCounter
}

func validReportValue(value string) bool {
	switch value {
	case "not_applied", "applied", "partial", "unknown", "pending", "verified", "available", "unavailable", "not_required", "released", "failed", "error":
		return true
	default:
		return false
	}
}

func safeCode(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
