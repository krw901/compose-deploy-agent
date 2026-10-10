#!/usr/bin/env bash
# End-to-end integration test against a real docker daemon. Not run by
# unit test CI (no docker available there) - run manually on a machine
# with docker + docker compose installed, e.g. the target VPS class of
# machine, before promoting a build.
#
# Exercises the scenarios that internal/deployer's fake-docker unit tests
# cannot: real `docker compose pull/up`, real HEALTHCHECK semantics, and
# that the .env file left behind matches the actually running container.
#
# Usage: scripts/integration_test.sh
set -euo pipefail

if ! command -v docker &>/dev/null; then
  echo "docker is required to run this script" >&2
  exit 1
fi

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &>/dev/null && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'docker rm -f it-good it-crash 2>/dev/null || true; rm -rf "$WORK_DIR"' EXIT

echo "== building deploy-agent =="
CGO_ENABLED=0 go build -o "${WORK_DIR}/deploy-agent" "${ROOT}/cmd/deploy-agent"

DATA_DIR="${WORK_DIR}/data"
MANIFEST="${WORK_DIR}/services.yaml"
mkdir -p "$DATA_DIR"

# --- fixture 1: a normal long-running service with a HEALTHCHECK ---
GOOD_DIR="${WORK_DIR}/good"
mkdir -p "$GOOD_DIR"
cat > "${GOOD_DIR}/compose.yml" <<'EOF'
services:
  app:
    image: busybox:${IMAGE_TAG:?IMAGE_TAG is required}
    container_name: it-good
    command: ["sh", "-c", "sleep 3600"]
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      retries: 1
      start_period: 1s
EOF

# --- fixture 2: a service whose container exits immediately (simulates
# a bad deploy that must trigger start_failed) ---
CRASH_DIR="${WORK_DIR}/crash"
mkdir -p "$CRASH_DIR"
cat > "${CRASH_DIR}/compose.yml" <<'EOF'
services:
  app:
    image: busybox:${IMAGE_TAG:?IMAGE_TAG is required}
    container_name: it-crash
    command: ["sh", "-c", "exit 1"]
EOF

cat > "$MANIFEST" <<EOF
version: 1
services:
  good:
    dir: ${GOOD_DIR}
    file: compose.yml
    service: app
    image: busybox
  crash:
    dir: ${CRASH_DIR}
    file: compose.yml
    service: app
    image: busybox
EOF

echo "== ca init =="
"${WORK_DIR}/deploy-agent" ca init --ip 127.0.0.1 --data-dir "$DATA_DIR"
echo "== ca issue =="
BUNDLE="$("${WORK_DIR}/deploy-agent" ca issue --name it-test --data-dir "$DATA_DIR")"

echo "== starting serve =="
"${WORK_DIR}/deploy-agent" serve --bind 127.0.0.1:18866 --healthz-bind 127.0.0.1:18867 \
  --manifest "$MANIFEST" --data-dir "$DATA_DIR" &
SERVE_PID=$!
trap 'kill $SERVE_PID 2>/dev/null || true; docker rm -f it-good it-crash 2>/dev/null || true; rm -rf "$WORK_DIR"' EXIT

for _ in $(seq 1 20); do
  if curl -sf http://127.0.0.1:18867/healthz >/dev/null; then break; fi
  sleep 0.5
done

deploy() {
  local service="$1" tag="$2"
  DEPLOY_AGENT_CREDENTIAL="$BUNDLE" "${WORK_DIR}/deploy-agent" deploy \
    --endpoint https://127.0.0.1:18866 --service "$service" --tag "$tag" --poll-timeout 2m
}

echo "== scenario: first deploy (success, healthcheck) =="
deploy good "3.19"
echo "expect .env IMAGE_TAG=3.19 in ${GOOD_DIR}"
grep -q '^IMAGE_TAG=3.19$' "${GOOD_DIR}/.env"

echo "== scenario: redeploy same tag (noop) =="
deploy good "3.19"

echo "== scenario: first deploy of an always-crashing service (no rollback target) =="
if deploy crash "3.19"; then
  echo "expected the crashing deploy to fail" >&2
  exit 1
fi

echo "== scenario: healthy service, then bad tag (rollback to previous tag) =="
deploy good "3.20"
if deploy good "does-not-exist-tag"; then
  echo "expected the bad-tag deploy to fail" >&2
  exit 1
fi
echo "expect .env to have rolled back to IMAGE_TAG=3.20"
grep -q '^IMAGE_TAG=3.20$' "${GOOD_DIR}/.env"

echo "== scenario: deploy-agent update via local unix socket (no endpoint/mTLS) =="
"${WORK_DIR}/deploy-agent" update -s good -t "3.19" --data-dir "$DATA_DIR" --poll-timeout 2m
echo "expect .env IMAGE_TAG=3.19 in ${GOOD_DIR} after update"
grep -q '^IMAGE_TAG=3.19$' "${GOOD_DIR}/.env"

echo "ALL SCENARIOS PASSED"
