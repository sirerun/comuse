# Controlled fixture acceptance driver

This command is a macOS+cgo source harness for the synthetic Comuse fixture.
Its only scenarios are `read_normal_value`, `replace_normal_text`, and
`press_counter`; the native host owns their fixed targets and approval setup.
The command exposes no arbitrary operation, text, approval, or readonly-adapter
input route. A held report is an unsuccessful or unavailable attempt, never a
pass.

The trusted JSON config is a closed schema. Unknown fields are rejected:

```json
{
  "schema_version": 1,
  "scenario": "read_normal_value",
  "library_path": "/absolute/path/to/libBridgeProbe.dylib",
  "fixture_pid": 1234,
  "fixture_nonce": "operator-chosen-fixture-nonce",
  "private_journal_root": "/absolute/path/to/private-journal",
  "journal_key_file": "/absolute/path/to/journal.key",
  "action_commitment_key_file": "/absolute/path/to/action.key"
}
```

Store the config and each key in owner-only mode `0600`; each key file must
contain exactly 32 bytes. The journal root must already exist as an owner-only
`0700` directory. The driver does not create keys, rotate them, reset the
journal, or print key material or filesystem paths. Keep the journal and key
files for the same caller-owned acceptance history.

Run the command with `--config <private-json>`. It pins the initial Go
goroutine to the process main thread before invoking the native host. The host
owns native open, pump, close, and close-before-quarantine-release ordering.
The JSON report is limited to scenario, status, execution, verification,
state, cleanup, and a safe error code; it contains no native values.
