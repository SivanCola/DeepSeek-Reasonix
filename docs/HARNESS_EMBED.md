# Embedding Reasonix in the DeepSeek Harness Web GUI

English | [中文](HARNESS_EMBED.zh-CN.md)

The Reasonix `serve` mode exposes a browser web UI over HTTP/SSE. Instead of
wrapping it in Electron, you can embed that UI directly inside the DeepSeek
Harness Web GUI through a harness **client plugin** (`dshClient`, platform
`web`) that renders the serve UI in an in-page iframe panel.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  DeepSeek Harness Web GUI (127.0.0.1:3080)                  │
│    session header [✦]  ← dsh-client-ui-reasonix plugin      │
│    ┌───────────────────────────────────────────────────┐    │
│    │  panel (portal overlay)                           │    │
│    │  <iframe src="http://127.0.0.1:8787"> ──────┐     │    │
│    └──────────────────────────────────────────────┼─────┘    │
└───────────────────────────────────────────────────┼─────────┘
                                                     │ loopback
┌───────────────────────────────────────────────────▼─────────┐
│  reasonix serve  (127.0.0.1:8787, own Go process)           │
│  / (web UI) · /events (SSE) · /submit /cancel /approve …    │
└─────────────────────────────────────────────────────────────┘
```

- The plugin is a pure viewport over a separate local process: the harness
  kernel never touches Reasonix traffic, and Reasonix sessions are driven by
  the serve process exactly as from a browser tab.
- Both origins are loopback (`127.0.0.1`), so no CORS, TLS, or origin
  policies are involved.

## Components

| Piece | Where | Role |
| --- | --- | --- |
| `dsh-client-ui-reasonix` | DSH repo `packages/client/ui-reasonix/` | Harness client plugin: header action + iframe panel |
| `reasonix serve` | Reasonix repo `cmd/reasonix` | HTTP/SSE server + web UI (default `127.0.0.1:8787`) |

The plugin was registered in the harness web composition
(`packages/bundle/web-app/cordis.patch.yml`, `tsconfig.client.json`,
`tsconfig.base.json` path map, `packages/bundle/web-app/package.json`
dependency).

## Running it

1. Start the Reasonix server (defaults to `127.0.0.1:8787`, no auth):

   ```sh
   go build -o /tmp/reasonix ./cmd/reasonix
   /tmp/reasonix serve
   ```

   For a token-protected instance: `/tmp/reasonix serve --auth token` and
   open the printed URL once to confirm the token.

2. Start the harness GUI with the client-plugin HMR receiver and rebuild
   plugins from the same checkout:

   ```sh
   dsh web --dev          # GUI, client-plugin HMR active
   pnpm run dev:web       # rebuilds client-plugin bundles on change (same checkout)
   ```

   Without `--dev` the plugin still loads after a restart + page refresh; HMR
   only removes the need to refresh after plugin edits.

3. Open a session in the GUI, click the ✦ action in the session header, and
   the Reasonix panel slides in.

## Customizing the endpoint

The panel defaults to `http://127.0.0.1:8787`. To point it elsewhere (for
example a token-protected instance on another port), set per browser:

```js
localStorage.setItem('reasonix.embed.url', 'http://127.0.0.1:8788')
```

The panel footer probes the serve logo asset and shows a green/red
connection dot, with a hint to start `reasonix serve` when offline.

## Why not Electron?

For "use Reasonix's web UI from the DeepSeek Harness", embedding is strictly
simpler: no second app to package, sign, or ship; the UI runs in the same
browser session; and `reasonix serve` already provides the transport and
auth. Electron would only make sense for a standalone desktop app that must
run without a browser or with native shell integration (dock, tray, file
associations) — that remains the Wails desktop shell's job in this repo.

## Security notes

- The embedded iframe is intentionally unsandboxed: the origin is loopback
  and user-trusted. Do not point the plugin at a remote or untrusted origin.
- `reasonix serve` binds loopback by default and defaults to no auth; use
  `--auth token` when the machine is shared.
