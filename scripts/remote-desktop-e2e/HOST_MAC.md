# Remote Desktop E2E — Mac host (SSH target)

This machine is the **Linux/macOS SSH remote** for the Windows Reasonix client
running in Parallels.

## Detected environment

| Item | Value |
| --- | --- |
| Parallels VM | `Windows 11` (running, Guest Tools 26.4) |
| Windows guest IP (shared net) | `10.211.55.6` |
| Mac host IP on shared net | `10.211.55.2` |
| Architecture | Mac arm64 → Windows 11 arm64 |

## One-time host setup

1. **Enable Remote Login (SSH)**  
   System Settings → General → Sharing → Remote Login → On  
   Allow your user (or All users).

2. **Confirm SSH from the Mac itself**

   ```bash
   ssh -o BatchMode=yes "$(whoami)@127.0.0.1" hostname
   ```

3. **Install a Reasonix build that includes `remote-runtime`** on the PATH used
   by SSH logins (interactive shell PATH may differ from GUI PATH):

   ```bash
   cd /path/to/DeepSeek-Reasonix
   go build -o "$HOME/bin/reasonix" ./cmd/reasonix
   mkdir -p "$HOME/bin"
   # Ensure non-interactive SSH sees it:
   # echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zprofile
   "$HOME/bin/reasonix" remote-runtime --help
   ```

4. **Disposable workspace** (created by the agent when preparing E2E):

   ```text
   /tmp/reasonix-remote-e2e-workspace
   ```

5. **API keys stay on Windows** — do **not** put `DEEPSEEK_API_KEY` in the Mac
   guest environment for this test.

## Network note (Parallels Shared)

Windows guest reaches the Mac as **`10.211.55.2`** (not the Mac’s LAN IP).

If the Mac cannot ping `10.211.55.6`, the guest may be locked, firewall-blocked,
or asleep for networking. Unlock the Windows session in the Parallels window
and allow File and Printer Sharing / OpenSSH inbound if you use host→guest SSH.
For the official matrix (**Windows → Mac/Linux**), only guest→host connectivity
is required.
