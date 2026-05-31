#!/usr/bin/env bash
#
# Run every native Go fuzz target that exercises a MySQL-specific code path.
# See scripts/fuzz-mysql.ps1 for the canonical (PowerShell) implementation.
#
# Usage:
#   ./scripts/fuzz-mysql.sh                 # default 10s per target
#   FUZZTIME=60s ./scripts/fuzz-mysql.sh    # longer soak
#   FUZZTIME=5m  ./scripts/fuzz-mysql.sh    # heavy fuzz before release
set -euo pipefail

FUZZTIME="${FUZZTIME:-10s}"
RESULTS_DIR="${RESULTS_DIR:-tests/fuzz/results}"

PACKAGES=(
    "./db/mysql/..."
    "./user/..."
    "./message/..."
    "./webpush/..."
)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

mkdir -p "$RESULTS_DIR"
SUMMARY="$RESULTS_DIR/fuzz-summary.log"
: > "$SUMMARY"

tee_log() {
    echo "$*" | tee -a "$SUMMARY"
}

tee_log "fuzz-mysql.sh start: $(date -Iseconds)"
tee_log "FuzzTime per target: $FUZZTIME"
tee_log ""

all_targets=()
for pkg in "${PACKAGES[@]}"; do
    echo "[discover] $pkg"
    listed="$(go test -list 'Fuzz.*' "$pkg" 2>/dev/null || true)"
    while IFS= read -r line; do
        if [[ "$line" =~ ^Fuzz[A-Za-z0-9_]+$ ]]; then
            all_targets+=("$pkg|$line")
        fi
    done <<< "$listed"
done

if [[ ${#all_targets[@]} -eq 0 ]]; then
    tee_log "[fatal] no FuzzXxx targets discovered"
    exit 2
fi

tee_log "Discovered ${#all_targets[@]} fuzz targets:"
for t in "${all_targets[@]}"; do
    tee_log "  - ${t//|/ :: }"
done
tee_log ""

failed=0
failures=()
for t in "${all_targets[@]}"; do
    pkg="${t%%|*}"
    name="${t##*|}"
    tee_log "=== [$pkg] $name (fuzztime=$FUZZTIME) ==="
    log_file="$RESULTS_DIR/fuzz-${name}.log"
    if ! go test -run '^$' -fuzz "^${name}\$" -fuzztime "$FUZZTIME" "$pkg" 2>&1 | tee "$log_file"; then
        tee_log "[fail] $name (log: $log_file)"
        failed=1
        failures+=("$pkg :: $name")
    else
        tee_log "[pass] $name"
    fi
    tee_log ""
done

tee_log ""
tee_log "=== Summary ==="
tee_log "Total targets: ${#all_targets[@]}"
if [[ $failed -eq 0 ]]; then
    tee_log "Passed:        ${#all_targets[@]}"
    tee_log "Failed:        0"
    tee_log ""
    tee_log "All fuzz targets clean for $FUZZTIME each."
    exit 0
else
    tee_log "Failed:        ${#failures[@]}"
    tee_log ""
    tee_log "Failed targets (check the per-target log under $RESULTS_DIR):"
    for f in "${failures[@]}"; do
        tee_log "  - $f"
    done
    tee_log ""
    tee_log "Inspect crashes under each package's testdata/fuzz/<TargetName>/ directory."
    exit 1
fi
