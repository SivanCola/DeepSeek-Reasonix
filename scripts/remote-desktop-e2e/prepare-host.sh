#!/usr/bin/env bash
# Prepare the Mac host as the SSH remote for Windows Reasonix E2E.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WS="${REASONIX_E2E_WORKSPACE:-/tmp/reasonix-remote-e2e-workspace}"
BIN_DIR="${HOME}/bin"
BIN="${BIN_DIR}/reasonix"

echo "==> Build reasonix with remote-runtime"
mkdir -p "$BIN_DIR"
(cd "$ROOT" && go build -o "$BIN" ./cmd/reasonix)
"$BIN" remote-runtime --help >/dev/null
echo "    binary: $BIN"

echo "==> Seed workspace $WS"
rm -rf "$WS"
mkdir -p "$WS"
cat >"$WS/README.md" <<'EOF'
# Reasonix Remote Desktop E2E Workspace

Edit this file from the Windows remote window. Run `!hostname` in chat —
it must print the Mac host name, not the Windows name.
EOF
if command -v git >/dev/null; then
  git -C "$WS" init -q
  git -C "$WS" config user.email e2e@reasonix.local
  git -C "$WS" config user.name e2e
  git -C "$WS" add README.md
  git -C "$WS" commit -q -m "e2e seed" || true
fi

echo "==> Network hints (Parallels Shared)"
echo "    Mac (SSH target):  10.211.55.2"
echo "    Windows guest:     10.211.55.6 (if shared net)"
echo "    User:              $(id -un)"
echo "    Workspace:         $WS"
echo
echo "==> Checks you may need to flip manually"
if nc -z -G 1 127.0.0.1 22 2>/dev/null; then
  echo "    SSH port 22 on localhost: OPEN"
else
  echo "    SSH port 22 on localhost: CLOSED — enable System Settings → Sharing → Remote Login"
fi
echo
echo "Done. On Windows, add SSH host 10.211.55.2 user=$(id -un) workspace=$WS"
echo "See scripts/remote-desktop-e2e/WINDOWS_CLIENT.md"
