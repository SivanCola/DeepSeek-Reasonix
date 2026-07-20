# Remote Desktop E2E — Windows 11 (Parallels client)

Run these steps **inside** the Windows 11 VM (unlock the session first).

## Prerequisites

- Reasonix **desktop** build from branch `feature/remote-desktop-kernel`
  (or a build that includes native remote desktop + `remote-runtime` on the host).
- Local DeepSeek (or other) provider configured **on Windows** with API key.
- Mac host reachable at `10.211.55.2` (Parallels Shared Network).

## PowerShell smoke (guest → host)

```powershell
Test-NetConnection 10.211.55.2 -Port 22
ssh YOUR_MAC_USER@10.211.55.2 hostname
```

If port 22 fails: enable Remote Login on the Mac.

## Reasonix UI path

1. Open Reasonix Desktop on Windows.
2. Open **Remote / SSH hosts**.
3. Add host:
   - Name: `mac-e2e`
   - Host: `10.211.55.2`
   - User: your Mac username
   - Auth: password or key
   - Default workspace: `/tmp/reasonix-remote-e2e-workspace`
4. Connect → accept host key if prompted.
5. **Provider Broker authorization**: confirm the dialog (remote will use local
   Windows model quota; keys stay on Windows).
6. Open workspace → expect a **native** Reasonix window titled like
   `Reasonix [SSH: mac-e2e]`, **not** a Serve HTML page.
7. Status bar should show `SSH:mac-e2e/...`.

## Acceptance checklist (issue #6714 matrix)

| # | Check | Pass? |
| --- | --- | --- |
| 1 | Multi-turn chat + tool call without `DEEPSEEK_API_KEY` on the Mac | |
| 2 | Mac side: no API key in remote process env / logs / session files | |
| 3 | Full desktop UI (chat, sessions, files, git, model picker) | |
| 4 | `!hostname` returns Mac hostname | |
| 5 | Edit a file under `/tmp/reasonix-remote-e2e-workspace`; Git Diff shows remote | |
| 6 | Mid-generation disconnect SSH → turn stops safely; reconnect continues session | |
| 7 | Delete remote session → local mirror still openable read-only | |
| 8 | Host key change → Broker blocked until re-auth | |
| 9 | No Serve webpage / no “open old remote UI” button | |
| 10 | Local workspace + standalone `reasonix serve` still work on Windows | |

## Build desktop for Windows arm64 (on Mac)

Parallels Windows 11 is **arm64**. Cross-build desktop from Mac if needed:

```bash
# Follow desktop README; typical:
cd desktop
# GOOS=windows GOARCH=arm64 wails build ... (project-specific flags)
```

If only amd64 builds exist, install Reasonix on Windows via the project’s
Windows release pipeline for arm64, or run E2E from an amd64 Windows VM.
