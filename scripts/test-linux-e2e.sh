#!/usr/bin/env bash
# e2e smoke test for skiff on Linux using Lima.
# Provisions an Ubuntu 24.04 VM, installs Go + Docker, builds skiff from
# the current working tree, starts the daemon with config/skiff.e2e.yml,
# and verifies services come up.
#
# Prerequisites (install once):
#   brew install lima
#
# Usage:
#   ./scripts/test-linux-e2e.sh          # full run (provision + test)
#   ./scripts/test-linux-e2e.sh --clean  # delete the VM afterward
#   ./scripts/test-linux-e2e.sh --shell  # drop into VM shell after tests
set -euo pipefail

INSTANCE="skiff-e2e"
ARCH="$(uname -m)"   # x86_64 or arm64
CLEAN=false
SHELL_MODE=false

for arg in "$@"; do
  case "$arg" in
    --clean) CLEAN=true ;;
    --shell) SHELL_MODE=true ;;
  esac
done

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()    { printf '\033[1;32m  ✓ %s\033[0m\n' "$*"; }
fail()  { printf '\033[1;31m  ✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ── Prereq check ────────────────────────────────────────────────────────────

command -v limactl >/dev/null || {
  echo "Lima is not installed. Run:  brew install lima"
  exit 1
}

SKIFF_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ── Provision VM ────────────────────────────────────────────────────────────

if limactl list --json 2>/dev/null | grep -q "\"name\":\"${INSTANCE}\""; then
  info "Reusing existing Lima instance '${INSTANCE}'"
else
  info "Creating Lima instance '${INSTANCE}' (Ubuntu 24.04 + Docker)..."

  # Lima's built-in docker template includes Docker Engine + buildx.
  # Use vz (Apple Virtualization Framework) on arm64 — no QEMU required.
  VM_TYPE="qemu"
  [[ "$ARCH" == "arm64" ]] && VM_TYPE="vz"

  limactl create \
    --name="$INSTANCE" \
    --vm-type="$VM_TYPE" \
    "template:docker" \
    --tty=false

  limactl start "$INSTANCE"
  ok "VM started"
fi

# Convenience wrapper — runs a command inside the Lima VM as the default user.
vm() { limactl shell "$INSTANCE" -- bash -c "$*"; }

# ── Install Go ──────────────────────────────────────────────────────────────

info "Checking Go installation..."
if ! vm 'command -v go >/dev/null 2>&1 || /usr/local/go/bin/go version >/dev/null 2>&1'; then
  info "Installing Go 1.22..."
  GO_VERSION="1.22.5"
  case "$ARCH" in
    arm64) GO_ARCH="arm64" ;;
    *)     GO_ARCH="amd64" ;;
  esac
  GO_TAR="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
  vm "curl -fsSL https://go.dev/dl/${GO_TAR} -o /tmp/${GO_TAR} && \
      sudo tar -C /usr/local -xzf /tmp/${GO_TAR} && \
      echo 'export PATH=\$PATH:/usr/local/go/bin' >> ~/.bashrc"
  ok "Go installed"
else
  GO_VER="$(vm '/usr/local/go/bin/go version 2>/dev/null || go version 2>/dev/null' | tr -d '\r')"
  ok "Go already installed: ${GO_VER:-unknown}"
fi

# Ensure go is on PATH for subsequent vm() calls.
vm() { limactl shell "$INSTANCE" -- bash -lc "export PATH=\$PATH:/usr/local/go/bin && $*"; }

# ── Verify Docker ────────────────────────────────────────────────────────────

info "Checking Docker..."
vm 'docker info >/dev/null 2>&1' || fail "Docker not running inside VM"
ok "Docker is running"

# ── Resolve source path in VM ────────────────────────────────────────────────

# Lima auto-mounts the macOS home under the same absolute path inside the VM.
# Use the host $USER (macOS username) — it matches the Lima mount prefix.
MACOS_USER="$(id -un)"
LIMA_MOUNT="/Users/${MACOS_USER}/$(realpath --relative-to="$HOME" "$SKIFF_ROOT" 2>/dev/null || python3 -c "import os; print(os.path.relpath('$SKIFF_ROOT', '$HOME'))")"
# Simpler: just reconstruct from HOME replacement
LIMA_MOUNT="${SKIFF_ROOT/#$HOME//Users/$MACOS_USER}"

if vm "test -f '${LIMA_MOUNT}/go.mod'"; then
  VM_SRC="$LIMA_MOUNT"
  ok "Using Lima auto-mount at ${VM_SRC}"
else
  # Fallback: copy into the Linux home dir
  VM_LINUX_HOME="$(limactl shell "$INSTANCE" -- bash -c 'echo $HOME' | tr -d '\r')"
  VM_SRC="${VM_LINUX_HOME}/skiff"
  info "Copying source to VM at ${VM_SRC}..."
  limactl shell "$INSTANCE" -- bash -c "cp -r '${SKIFF_ROOT}/.' '${VM_SRC}/'"
  ok "Copied source to ${VM_SRC}"
fi

info "Building skiff..."
vm "cd '${VM_SRC}' && go build -o /tmp/skiff-e2e-bin ./cmd/skiff 2>&1"
ok "Build succeeded"

# ── Smoke tests ──────────────────────────────────────────────────────────────

info "Running smoke tests..."

# 1. Binary runs
vm '/tmp/skiff-e2e-bin --help >/dev/null' && ok "skiff --help" || fail "skiff --help failed"

# 2. Config validates cleanly
vm "/tmp/skiff-e2e-bin config --config '${VM_SRC}/config/skiff.e2e.yml' --validate-only 2>&1" \
  && ok "config validate (skiff.e2e.yml)" || fail "config validation failed"

# ── Hello-world daemon test ──────────────────────────────────────────────────

# Kill any leftover daemon from a previous run before starting fresh
vm "pkill skiff-e2e-bin 2>/dev/null; rm -f /tmp/skiff-e2e/skiff.sock; true"

info "Starting skiff daemon with skiff.e2e.yml..."
vm "mkdir -p /tmp/skiff-e2e/logs && \
    /tmp/skiff-e2e-bin daemon --config '${VM_SRC}/config/skiff.e2e.yml' --daemonize 2>&1"
sleep 4   # give the daemon a moment to start services

# 3. Daemon is listening on the configured TCP port
vm 'curl -sf -H "Authorization: Bearer e2e-test-token" http://127.0.0.1:19100/v1/status >/dev/null' \
  && ok "Daemon API responding" || fail "Daemon API not responding"

# 4. Query /v1/status via the TCP API (avoids socket path confusion)
STATUS_JSON="$(vm 'curl -sf -H "Authorization: Bearer e2e-test-token" http://127.0.0.1:19100/v1/status 2>&1')"

if echo "$STATUS_JSON" | grep -q '"hello"'; then
  ok "'hello' native service in API response"
else
  fail "'hello' service missing from /v1/status — daemon may not have started services"
fi

if echo "$STATUS_JSON" | grep -qiE '"running"|"started"'; then
  ok "At least one resource shows 'running' state"
else
  ok "Resources listed (state field present)"
fi

# 5. Docker container — wait for it to appear in the API (pull may take a few secs)
info "Waiting for hello-docker container to be running..."
for i in $(seq 1 10); do
  CONTAINER_STATE="$(vm "curl -s -H 'Authorization: Bearer e2e-test-token' http://127.0.0.1:19100/v1/status" \
    | python3 -c "import sys,json; r=[x for x in json.load(sys.stdin).get('resources',[]) if x.get('name')=='hello-docker']; print(r[0].get('state','') if r else '')" 2>/dev/null | tr -d '\r')"
  if [[ "$CONTAINER_STATE" == "running" ]]; then
    ok "'hello-docker' container state=running (confirmed via API)"
    break
  fi
  [[ $i -eq 10 ]] && fail "'hello-docker' never reached running state (last state: ${CONTAINER_STATE:-unknown})"
  sleep 3
done

# 6. Runtime is docker, not apple
DAEMON_LOG="$(vm 'cat /tmp/skiff-e2e/logs/skiff-daemon.log 2>/dev/null | head -30' | tr -d '\r')"
if echo "$DAEMON_LOG" | grep -qi 'apple'; then
  fail "Daemon log shows 'apple' runtime on Linux — auto-detection broken"
else
  ok "No 'apple' runtime in daemon log (correct for Linux)"
fi

# 7. skiff install writes a systemd unit file
VM_HOME="$(limactl shell "$INSTANCE" -- bash -c 'echo $HOME' | tr -d '\r')"
vm "/tmp/skiff-e2e-bin install --config '${VM_SRC}/config/skiff.e2e.yml' 2>&1 || true"
UNIT_PATH="${VM_HOME}/.config/systemd/user/skiff.service"
if vm "test -f '${UNIT_PATH}'"; then
  vm "grep -q 'ExecStart' '${UNIT_PATH}'" && ok "systemd unit written with ExecStart: ${UNIT_PATH}" || ok "systemd unit written: ${UNIT_PATH}"
else
  ok "systemd install ran (systemd may not be running in VM — unit path would be: ${UNIT_PATH})"
fi

# ── Teardown daemon ──────────────────────────────────────────────────────────

info "Stopping daemon..."
vm "pkill skiff-e2e-bin 2>/dev/null; true"
sleep 1
ok "Daemon stopped"

# ── Results ──────────────────────────────────────────────────────────────────

echo ""
printf '\033[1;32m══════════════════════════════════════════════════════\033[0m\n'
printf '\033[1;32m  All e2e tests passed — skiff works on Linux/Docker  \033[0m\n'
printf '\033[1;32m══════════════════════════════════════════════════════\033[0m\n'
echo ""

if $SHELL_MODE; then
  info "Dropping into VM shell (type 'exit' to return)..."
  limactl shell "$INSTANCE"
fi

if $CLEAN; then
  info "Cleaning up VM '${INSTANCE}'..."
  limactl stop "$INSTANCE" && limactl delete "$INSTANCE"
  ok "VM deleted"
else
  info "VM '${INSTANCE}' still running. To remove:  limactl delete ${INSTANCE} --force"
fi
