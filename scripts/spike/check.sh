#!/usr/bin/env bash
set -uo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/spike/check.sh --artifact-root ABSOLUTE_EXISTING_DIRECTORY --select go|swift|fixture|native|cli|mcp
Run one reproducible spike check and write logs/evidence under a new child of artifact-root.
EOF
}

artifact_root=""
selector=""
while (($#)); do
  case "$1" in
    --artifact-root)
      (($# >= 2)) || { usage; exit 64; }
      artifact_root="$2"
      shift 2
      ;;
    --select)
      (($# >= 2)) || { usage; exit 64; }
      selector="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) usage; exit 64 ;;
  esac
done

[[ "$artifact_root" == /* && -d "$artifact_root" && ! -L "$artifact_root" ]] || {
  echo "artifact root must be an existing absolute non-symlink directory verified by the caller" >&2
  exit 64
}
artifact_root="$(cd "$artifact_root" && pwd -P)" || exit 73
[[ -w "$artifact_root" ]] || { echo "artifact root is not writable" >&2; exit 73; }
case "$selector" in go|swift|fixture|native|cli|mcp) ;; *) usage; exit 64 ;; esac

repo_root="$(cd "$(dirname "$0")/../.." && pwd -P)" || exit 73
cd "$repo_root" || exit 73
head_sha="$(git rev-parse HEAD)" || exit 73
source_status="$(git status --porcelain --untracked-files=normal)" || exit 73
if [[ -n "$source_status" ]]; then
  echo "exact-head checks require a clean source worktree" >&2
  exit 75
fi

probe="$(mktemp "$artifact_root/.comuse-write-check.XXXXXX")" || {
  echo "artifact root failed exclusive write probe" >&2
  exit 73
}
rm -- "$probe"

run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
evidence_dir="$artifact_root/comuse-spike-$run_id"
mkdir "$evidence_dir" 2>/dev/null || {
  echo "refusing to reuse existing evidence directory: $evidence_dir" >&2
  exit 73
}
mkdir "$evidence_dir/tmp" "$evidence_dir/cache" "$evidence_dir/swift-scratch" "$evidence_dir/go-build" "$evidence_dir/go-mod" || exit 73
export TMPDIR="$evidence_dir/tmp"
export GOCACHE="$evidence_dir/go-build"
export GOMODCACHE="$evidence_dir/go-mod"
export SWIFTPM_MODULECACHE="$evidence_dir/cache/swift-modules"
export CLANG_MODULE_CACHE_PATH="$evidence_dir/cache/clang-modules"
mkdir -p "$SWIFTPM_MODULECACHE"
mkdir -p "$CLANG_MODULE_CACHE_PATH"

cd "$repo_root" || exit 73
status_file="$evidence_dir/status.tsv"
log_file="$evidence_dir/commands.log"
tool_file="$evidence_dir/environment.txt"
printf 'Comuse spike command log\n' > "$log_file" || exit 73
template="$(dirname "$0")/evidence-template.md"
cp "$template" "$evidence_dir/evidence.md" || exit 73
printf 'selector\tstatus\tdetail\n' > "$status_file" || exit 73
{
  printf 'head=%s\n' "$head_sha"
  printf 'selector=%s\n' "$selector"
  printf 'os=%s\n' "$(uname -a)"
  printf 'go=%s\n' "$(go version 2>&1 || true)"
  printf 'swift=%s\n' "$(swift --version 2>&1 | head -1 || true)"
  printf 'ci=%s\n' "${GITHUB_ACTIONS:-false}"
  printf 'runner_os=%s\n' "${RUNNER_OS:-local}"
  printf 'artifact_root=%s\n' "$artifact_root"
  printf 'evidence_dir=%s\n' "$evidence_dir"
  printf 'fixture_launch=not_run\naccessibility_tcc=not_run\ninput_tcc=not_run\n'
} > "$tool_file" || exit 73

record() { printf '%s\t%s\t%s\n' "$selector" "$1" "$2" >> "$status_file" || exit 73; }
append_evidence() {
  {
    printf '\n## Run result\n\n- Selector: `%s`\n- Head: `%s`\n- Evidence directory: `%s`\n- Tool and launch state: see `environment.txt`\n- Command log: `commands.log`\n- Status: see `status.tsv`\n' "$selector" "$head_sha" "$evidence_dir"
  } >> "$evidence_dir/evidence.md" || exit 73
}

run_check() {
  local label="$1"; shift
  local command_text
  printf -v command_text '%q ' "$@"
  local load_value="n/a"
  printf '\n[%s] COMMAND %s\n' "$label" "$command_text" | tee -a "$log_file" || exit 73
  if [[ "${GITHUB_ACTIONS:-false}" != "true" ]]; then
    local uptime_text
    uptime_text="$(LC_ALL=C uptime 2>&1)"
    printf '[%s] uptime: %s\n' "$label" "$uptime_text" >> "$log_file" || exit 73
    load_value="$(printf '%s\n' "$uptime_text" | sed -E 's/.*load averages?:[[:space:]]*([0-9]+([.][0-9]+)?).*/\1/' | tail -1)"
    if ! [[ "$load_value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
      printf '[%s] HELD: could not parse one-minute load from uptime: %s\n' "$label" "$uptime_text" | tee -a "$log_file" || exit 73
      record held "$label: load unavailable"
      return 75
    fi
    if ! awk -v load="$load_value" 'BEGIN { exit !(load <= 10) }'; then
      printf '[%s] HELD: one-minute load %s exceeds 10\n' "$label" "$load_value" | tee -a "$log_file" || exit 73
      record held "$label: one-minute load $load_value exceeds 10"
      return 75
    fi
  fi
  "$@" >> "$log_file" 2>&1
  local result=$?
  if ((result == 0)); then
    printf '[%s] PASS (load=%s)\n' "$label" "$load_value" | tee -a "$log_file" || exit 73
    record pass "$label"
  else
    printf '[%s] FAIL (exit=%s, load=%s)\n' "$label" "$result" "$load_value" | tee -a "$log_file" || exit 73
    record fail "$label: exit $result"
  fi
  return "$result"
}

run_swift_test() {
  local package="$1"; shift
  local scratch="$evidence_dir/swift-scratch/$package"
  run_check "swift-test-$package" swift test --package-path "spikes/macos/$package" --scratch-path "$scratch" --jobs 2 "$@"
}

result=0
case "$selector" in
  go)
    run_check "go-tests-cgo" go test ./... || result=$?
    if ((result == 0)); then
      run_check "go-tests-cgo-disabled" env CGO_ENABLED=0 go test ./... || result=$?
    fi
    ;;
  swift)
    run_swift_test bridge || result=$?
    if ((result == 0)); then
      # macOS 14 is the deployment/compile target. This executes on the runner OS.
      run_check "swift-bridge-macos14-build" swift build --package-path spikes/macos/bridge --scratch-path "$evidence_dir/swift-scratch/bridge-macos14" --triple arm64-apple-macosx14.0 --jobs 2 || result=$?
    fi
    ;;
  fixture)
    run_swift_test fixture || result=$?
    record not_run "fixture app launch and Accessibility/Input TCC checks are deliberately outside compile tests"
    ;;
  native)
    if [[ "$(uname -s)" != Darwin ]]; then
      record not_run "native Swift dylib smoke requires macOS"
    else
      binpath_file="$evidence_dir/bridge-bin-path.txt"
      run_check "swift-bridge-build-binpath" bash -c 'swift build --package-path spikes/macos/bridge --scratch-path "$1" --jobs 2 --show-bin-path > "$2"' _ "$evidence_dir/swift-scratch/native" "$binpath_file" || result=$?
      if ((result == 0)); then
        library_dir="$(cat "$binpath_file")"
        run_check "go-to-swift-legacy-callback-smoke" go run ./spikes/seamprobe -library "$library_dir/libBridgeProbe.dylib" || result=$?
      fi
      if ((result == 0)); then
        run_check "go-to-swift-runtime-smoke" go run ./spikes/bridgeclient/cmd/seamprobe -library "$library_dir/libBridgeProbe.dylib" || result=$?
      fi
    fi
    ;;
  cli)
    run_check "go-cli-adapter-tests" go test ./spikes/adapters/cliprobe ./spikes/bridgeclient/cmd/seamprobe || result=$?
    ;;
  mcp)
    if [[ -d spikes/adapters/mcpprobe ]]; then
      run_check "go-mcp-adapter-tests" go test ./spikes/adapters/mcpprobe || result=$?
    elif [[ -d cmd/comuse-mcp ]]; then
      run_check "go-mcp-server-tests" go test ./cmd/comuse-mcp/... || result=$?
    else
      record not_run "no MCP adapter/server package exists at this head"
    fi
    ;;
esac

final_head="$(git rev-parse HEAD)" || final_head=unknown
final_status="$(git status --porcelain --untracked-files=normal)" || final_status=unknown
if [[ "$final_head" != "$head_sha" || -n "$final_status" ]]; then
  record fail "source changed during checks; exact-head evidence invalid"
  result=1
fi
append_evidence
printf 'EVIDENCE_DIR=%s\n' "$evidence_dir"
cat "$status_file" || exit 73
for required_file in "$status_file" "$tool_file" "$log_file" "$evidence_dir/evidence.md"; do
  [[ -s "$required_file" ]] || exit 73
done
status_flags="$(awk -F '\t' 'NR > 1 { print $2 }' "$status_file")" || exit 73
[[ "$status_flags" == *fail* ]] && exit 1
[[ "$status_flags" == *held* ]] && exit 75
exit "$result"
