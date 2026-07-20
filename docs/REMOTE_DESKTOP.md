# Remote Desktop (SSH Native Kernel)

Reasonix remote workspaces open a **full native desktop window** over SSH. Chat,
commands, tools, Skills, MCP, files, and Git run on the remote host. Model
configuration and API keys stay on the local machine and are reached through a
Provider Broker reverse tunnel.

This document is the user-facing overview of the architecture shipped for
[Issue #6714](https://github.com/esengine/DeepSeek-Reasonix/issues/6714).

## What changed

| Before | After |
| --- | --- |
| Desktop opened a remote `reasonix serve` webpage in a shell window | Desktop opens a full Reasonix UI window bound to `remote-runtime` |
| Remote process needed local-style API keys | Remote never reads API keys; Provider Broker stays on localhost |
| Session authority mixed with local state | Remote Controller is the sole writer; local mirror is read-only |

Standalone `reasonix serve` remains available for browser/CLI use. It is **not**
used as the desktop SSH UI path, and there is no “open old Serve webpage”
fallback.

## Architecture (short)

1. Local desktop authenticates SSH (config + credentials; see Remote SSH module).
2. Host key fingerprint is checked against Provider trust records.
3. Matching Reasonix binary is installed/verified on the remote host.
4. `reasonix remote-runtime` starts on remote loopback (`/remote/v1` protocol).
5. Local Provider Broker issues a capability token scoped to host + workspace +
   authorized provider refs; SSH reverse-forwards it to remote `127.0.0.1` only.
6. Local Remote Gateway binds loopback RPC for the child window; a one-shot
   mode-0600 ticket carries the gateway token (never argv/URL/DOM).
7. Child window loads the full desktop frontend and talks to the gateway.
8. After Ready, messages and tools execute on the remote Controller.

## Security boundaries

- API keys exist only in the local credential store and local Provider process
  memory.
- Full `config.toml`, `.env`, proxy URLs, hooks, and OS-specific tool paths are
  **not** synced to the remote host.
- Host key fingerprint changes block Broker access until the user re-authorizes.
- New providers are not inherited by old trust records; first use confirms.
- Capability tokens are revoked when the connection closes or the local app exits.
- Logs record request id, duration, provider ref, and redacted error codes only.

## Session mirrors

Local mirrors live under:

```text
<Reasonix home>/remote-mirrors/<host-fingerprint-hash>/<workspace-hash>/
```

They are for offline viewing and disaster recovery only. Revision/digest
checkpoints prevent silent forks; restore always creates a **new** remote
session id.

## CLI

```bash
# Headless multi-session runtime (normally launched by desktop bootstrap)
reasonix remote-runtime \
  --workspace /path/on/remote \
  --addr 127.0.0.1:0 \
  --token-file /path/to/token \
  --port-file /path/to/port \
  --broker-url http://127.0.0.1:PORT \
  --broker-token-file /path/to/broker-token
```

`reasonix serve` is unchanged for independent browser use.

## Upgrade notes

- Desktop SSH “Open workspace” no longer opens Serve HTML.
- Remote hosts need a Reasonix build that includes `remote-runtime` and
  protocol major version 1. On mismatch the desktop upgrades the remote binary
  and retries; it does not fall back to the webpage.
- Existing local tabs and desktop state JSON remain valid; missing
  `executionTarget` means local.

## Related

- Chinese overview: [REMOTE_DESKTOP.zh-CN.md](./REMOTE_DESKTOP.zh-CN.md)
- Provider config: [CONFIG_PATHS.md](./CONFIG_PATHS.md)
- CLI reference: [CLI.md](./CLI.md)
