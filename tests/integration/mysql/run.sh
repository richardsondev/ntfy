#!/usr/bin/env bash
# Orchestration script for the MySQL integration test suite (bash version).
# See run.ps1 for the canonical (PowerShell) implementation. The Windows
# dev environment is the primary target; this script exists for CI and
# Linux/macOS contributors.
set -euo pipefail

NO_TEARDOWN="${NO_TEARDOWN:-0}"
SKIP_BUILD="${SKIP_BUILD:-0}"
REBUILD="${REBUILD:-0}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-teardown) NO_TEARDOWN=1 ;;
        --skip-build)  SKIP_BUILD=1 ;;
        --rebuild)     REBUILD=1 ;;
        -h|--help)
            echo "Usage: $0 [--no-teardown] [--skip-build] [--rebuild]"
            exit 0
            ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
    shift
done

export PYTHONIOENCODING=utf-8
export PYTHONUTF8=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$HERE/podman-compose.yml"
RESULTS_DIR="$HERE/results"
VENV_DIR="$HERE/.venv"
JUNIT_PATH="$RESULTS_DIR/junit.xml"

mkdir -p "$RESULTS_DIR"

if command -v podman >/dev/null 2>&1; then
    COMPOSE=(podman compose)
elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE=(docker-compose)
else
    echo "no podman / docker / docker-compose found on PATH" >&2
    exit 1
fi
echo "[run] using compose driver: ${COMPOSE[*]}"

compose() {
    "${COMPOSE[@]}" -f "$COMPOSE_FILE" "$@"
}

teardown() {
    if [[ "$NO_TEARDOWN" == "1" ]]; then
        echo "[run] --no-teardown: leaving stack up; tear down with: ${COMPOSE[*]} -f $COMPOSE_FILE down -v"
    else
        echo "[run] tearing stack down..."
        compose down -v || true
    fi
}
trap teardown EXIT

wait_healthy() {
    local svc="$1" timeout="${2:-180}"
    local deadline=$(( $(date +%s) + timeout ))
    echo "[run] waiting for '$svc' to become healthy (timeout ${timeout}s)..."
    while [[ $(date +%s) -lt $deadline ]]; do
        local status
        status="$(compose ps --format json 2>/dev/null || true)"
        if echo "$status" | grep -q "\"Service\":\"$svc\"" 2>/dev/null || \
           echo "$status" | grep -q "\"$svc\"" 2>/dev/null; then
            if echo "$status" | grep -q "\"Health\":\"healthy\"" 2>/dev/null; then
                echo "[run] '$svc' is healthy."
                return 0
            fi
        fi
        sleep 2
    done
    echo "service '$svc' never became healthy" >&2
    compose logs "$svc" || true
    return 1
}

if [[ "$REBUILD" == "1" ]]; then
    echo "[run] forcing rebuild..."
    compose down -v || true
    "${COMPOSE[@]}" image rm ntfy-mysql-int:local 2>/dev/null || true
fi

echo "[run] bringing stack up..."
if [[ "$SKIP_BUILD" == "1" ]]; then
    compose up -d
else
    compose up -d --build
fi

wait_healthy mysql 180
wait_healthy ntfy 180

if [[ ! -d "$VENV_DIR" ]]; then
    echo "[run] creating Python venv at $VENV_DIR..."
    python3 -m venv "$VENV_DIR"
fi

if [[ -x "$VENV_DIR/bin/python" ]]; then
    VENV_PY="$VENV_DIR/bin/python"
else
    VENV_PY="$VENV_DIR/Scripts/python.exe"
fi

echo "[run] installing test deps..."
"$VENV_PY" -m pip install --quiet --upgrade pip
"$VENV_PY" -m pip install --quiet -r "$HERE/requirements.txt"

echo "[run] running pytest..."
export NTFY_INT_URL="http://127.0.0.1:8888"
export NTFY_INT_COMPOSE_DIR="$HERE"
cd "$HERE"
set +e
"$VENV_PY" -m pytest --junitxml="$JUNIT_PATH"
exit_code=$?
set -e

echo "[run] pytest exit code: $exit_code"
echo "[run] junit report: $JUNIT_PATH"
exit "$exit_code"
