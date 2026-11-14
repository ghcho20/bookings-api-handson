#!/usr/bin/env bash
# Notes for step 07 (CI):
# - The compose file is at the repo root, not in src/. This script runs from the root.
# - The gate is this script: `scripts/run-collections.sh [baseUrl] [apiKey]`. It runs
#   `docker compose up --build --wait`, runs the five steps below in order, and tears the stack down
#   (`docker compose down -v`) on exit, also when a step fails. It exits non-zero on the first failure.
# - Working keys: demo-key (full access) and ro_demo-key (read-only). The QA CSV hardcodes demo-key.
# - The gate runs with ENABLE_TEST_ROUTES=true (exported below).
# - Collections run with the `local` environment and --no-report-events.
# - The CI workflow should trigger on changes to src/**, docker-compose.yml, scripts/**, qa/** and
#   postman/**, never on the generated-collection refresh alone.
# - One Contract assertion is exempted from the gate (see KNOWN FAILURE below). Everything else in
#   every collection must pass.
#
# Steps (the state-changing ones are preceded by "Reset Mock"):
#   1. Reset Mock
#   2. [Smoke] Bookings API              -i resources -i bookings -i webhook-subscriptions
#   3. [Contract] Bookings API           same -i flags
#   4. QA Integration Tests, Lifecycle   --bail
#   5. QA Integration Tests, "Create booking (data-driven)"  -d qa/qa-create-booking.csv (six rows)
# The generated Webhooks folder is skipped on purpose: it builds broken URLs (/v1booking.confirmed).
#
# KNOWN FAILURE (exempted in step 3 only): the generated [Contract] collection has two checks that
# contradict each other for the Idempotency-Key header. "Request multipart bodies and Idempotency-Key
# follow their specifications" wants a quoted Structured Field String; "Request parameters use the
# OpenAPI-declared wire serialization" rejects a quoted header value. No plain value passes both, so the
# first one always fails on "bookings / Create a booking". The gate accepts exactly that failure (request,
# assertion name and message are all matched; see scripts/classify-run-failures.awk) and still fails on
# any other Contract failure, on a failure count that does not match, or on a run error. When the
# generated checks are fixed the exemption is no longer needed: the script says so, and the exemption
# (run_contract below and the awk file) can be deleted.
set -euo pipefail

BASE_URL="${1:-http://localhost:8080/v1}"
API_KEY="${2:-demo-key}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

command -v docker >/dev/null || { echo "docker is required" >&2; exit 2; }
command -v postman >/dev/null || { echo "the Postman CLI (postman) is required" >&2; exit 2; }

# Test routes on, and the keys the collections use (plus the caller's key, if different).
export ENABLE_TEST_ROUTES=true
export API_KEYS="demo-key${API_KEY:+,${API_KEY}}"
export API_KEYS_READONLY="${API_KEYS_READONLY:-ro_demo-key}"

REPORT_DIR="$(mktemp -d)"

teardown() {
  status=$?
  rm -rf "$REPORT_DIR"
  echo
  echo "==> docker compose down -v"
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
  exit "$status"
}
trap teardown EXIT

run() { # run <collection> [extra postman flags...]
  local collection="$1"
  shift
  echo
  echo "==> postman collection run \"$collection\" $*"
  postman collection run "postman/collections/$collection" \
    -e postman/environments/local.environment.yaml \
    --env-var "baseUrl=${BASE_URL}" \
    --env-var "apiKey=${API_KEY}" \
    --no-report-events \
    "$@"
}

# Step 3. Runs [Contract] and applies the KNOWN FAILURE exemption described at the top.
run_contract() {
  local code=0 line known=0 other=0 failed=0 errors=0
  rm -f "$REPORT_DIR"/*.yaml
  set +e
  run "[Contract] Bookings API" "${SMOKE_CONTRACT_FOLDERS[@]}" --output "$REPORT_DIR"
  code=$?
  set -e
  if [ "$code" -eq 0 ]; then
    echo "NOTE: [Contract] passed with no failures. The KNOWN FAILURE exemption is no longer needed."
    return 0
  fi
  local summary
  summary="$(ls "$REPORT_DIR"/*.collection-run-summary.yaml 2>/dev/null | head -n 1 || true)"
  if [ -z "$summary" ]; then
    echo "GATE FAILED: [Contract] failed (exit $code) and left no run report to classify." >&2
    return 1
  fi
  while IFS='=' read -r key value; do
    case "$key" in known) known=$value ;; other) other=$value ;; failed) failed=$value ;; errors) errors=$value ;; esac
  done < <(awk -f "$SCRIPT_DIR/classify-run-failures.awk" "$summary")
  if [ "$known" -ge 1 ] && [ "$other" -eq 0 ] && [ "$errors" -eq 0 ] && [ "$failed" -eq "$known" ]; then
    echo
    echo "KNOWN FAILURE EXEMPTED: $known x Idempotency-Key assertion on 'bookings / Create a booking'."
    echo "  (conflicting generated checks; see the KNOWN FAILURE note at the top of this script)"
    return 0
  fi
  echo "GATE FAILED: [Contract] has failures beyond the exempted Idempotency-Key assertion" \
       "(known=$known other=$other failed=$failed errors=$errors)." >&2
  return 1
}

echo "==> docker compose up --build --wait"
docker compose up --build --wait

SMOKE_CONTRACT_FOLDERS=(-i resources -i bookings -i webhook-subscriptions)

run "Reset Mock"
run "[Smoke] Bookings API" "${SMOKE_CONTRACT_FOLDERS[@]}"
run "Reset Mock"
run_contract
run "Reset Mock"
run "QA Integration Tests" -i Lifecycle --bail
run "Reset Mock"
run "QA Integration Tests" -i "Create booking (data-driven)" -d qa/qa-create-booking.csv

echo
echo "==> all collections passed"
