#!/usr/bin/env bash
# Probe Parallels Windows 11 VM readiness for remote-desktop E2E.
set -euo pipefail

VM_NAME="${1:-Windows 11}"

if ! command -v prlctl >/dev/null; then
  echo "prlctl not found — is Parallels Desktop installed?"
  exit 1
fi

echo "==> VM list"
prlctl list -a

echo
echo "==> VM status / IP"
prlctl list -i "$VM_NAME" 2>/dev/null | grep -E 'Name:|OS:|GuestTools:|IP Addresses:|STATUS' || true

IP="$(prlctl list -i "$VM_NAME" 2>/dev/null | awk -F': ' '/IP Addresses:/{print $2}' | cut -d, -f1 | tr -d ' ')"
echo "    primary IPv4 guess: ${IP:-unknown}"

echo
echo "==> Host bridge (Parallels Shared)"
ifconfig bridge100 2>/dev/null | grep 'inet ' || echo "    bridge100 not up"

echo
echo "==> Guest reachability from host (optional; Windows firewall may block)"
if [[ -n "${IP:-}" ]]; then
  if ping -c 1 -W 1000 "$IP" >/dev/null 2>&1; then
    echo "    ping $IP: OK"
  else
    echo "    ping $IP: FAIL (common if guest locked/firewall — unlock Windows UI)"
  fi
  for port in 22 3389 445 5985; do
    if nc -z -G 1 "$IP" "$port" 2>/dev/null; then
      echo "    port $port: OPEN"
    else
      echo "    port $port: closed"
    fi
  done
fi

echo
echo "==> prlctl exec (needs Parallels Pro/Business)"
if prlctl exec "$VM_NAME" cmd /c echo ok 2>/dev/null | grep -q ok; then
  echo "    exec: available"
else
  echo "    exec: NOT available on this edition — drive UI steps inside the VM window"
fi

echo
echo "For Windows → Mac E2E, guest must reach Mac at 10.211.55.2:22"
echo "Run: scripts/remote-desktop-e2e/prepare-host.sh"
