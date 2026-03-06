#!/usr/bin/env bash
set -euo pipefail

REPO="chronick/skiff"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

info()  { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
error() { printf '\033[1;31merror: %s\033[0m\n' "$*" >&2; exit 1; }

# --- Checks ---

[[ "$(uname -s)" == "Darwin" ]] || error "skiff only supports macOS"

command -v git  >/dev/null || error "git is required"
command -v go   >/dev/null || error "go 1.22+ is required (https://go.dev/dl/)"

go_version=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')
go_major=$(echo "$go_version" | cut -d. -f1)
go_minor=$(echo "$go_version" | cut -d. -f2)
if (( go_major < 1 || (go_major == 1 && go_minor < 22) )); then
  error "go 1.22+ required, found go${go_version}"
fi

# --- Clone & Build ---

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

info "Cloning ${REPO}..."
git clone --depth 1 "https://github.com/${REPO}.git" "$TMPDIR/skiff" 2>&1 | tail -1

cd "$TMPDIR/skiff"

info "Building skiff..."
go build -o skiff ./cmd/skiff
go build -o skiff-menu ./cmd/skiff-menu

# --- Install ---

info "Installing to ${INSTALL_DIR}..."
install -d "$INSTALL_DIR"
install -m 755 skiff "$INSTALL_DIR/skiff"
install -m 755 skiff-menu "$INSTALL_DIR/skiff-menu"

# --- Config ---

CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/skiff"
if [[ ! -f "$CONFIG_DIR/config.yml" ]]; then
  info "Creating default config at ${CONFIG_DIR}/config.yml..."
  mkdir -p "$CONFIG_DIR"
  cp config/skiff.example.yml "$CONFIG_DIR/config.yml"
fi

# --- Verify ---

if command -v skiff >/dev/null; then
  info "Installed $(skiff --help 2>&1 | head -1)"
else
  info "Built successfully. Add ${INSTALL_DIR} to your PATH if needed."
fi

cat <<'MSG'

Next steps:
  skiff init          Generate a starter config
  skiff daemon        Start the control plane
  skiff install       Auto-start on login

MSG
