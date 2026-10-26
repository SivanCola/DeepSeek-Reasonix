---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-25
---

# Extension UI: wider slots and sandboxed frames

Status: proposal. Nothing below is implemented. Sections marked **Current** describe code on `studio` today; every other section is the proposed change.

## Purpose

1. Give extensions more places to stand in Studio, without changing how they draw.
2. Give extensions one escape hatch for UI the host catalogue cannot express: a document of their own, drawn in an isolated frame.
3. Keep three properties that the structured-only surface holds today, and that this change MUST NOT weaken:
   - an extension cannot redraw an approval, permission or error state;
   - extension code never runs with the page's authority (preload bridge, kernel credential);
   - every frontend can show something for every surface, even when it is only text.

## Non-goals

| Not proposed | Why |
| --- | --- |
| Hiding or replacing UI Studio ships | Presentation answers to canonical state; a takeover restates it. Approvals are the sharpest case. |
| Global CSS, fonts or layout changes from an extension | Appearance stays with theme packs (`docs/THEME_AUTHOR_GUIDE.md`) and their tokens. |
| Extension code in the Studio page's own JavaScript realm | It would hold every authority the page holds. See Security boundary. |
| Frames in the transcript, in tool cards or near approvals | Keeps foreign documents out of the decision region and out of transcript virtualisation. |
| Network access from frames | The sidecar already has the network; a frame needs none of its own. |
| Model-authored frames | `render_surface` stays catalogue-only (`docs/design/INTELLIGENT_UI.md`). |
| TUI or ACP rendering of frames | They get the text fallback. |

## Current behaviour

**Current.** These facts bound the design.

| Fact | Where |
| --- | --- |
| Studio offers two slots, `sidebar` (default) and `composer-trailing`; `above_prompt` is an alias of the second. An unknown slot falls back to the default. | `desktop/frontend-next/src/ui/slots.ts` |
| The slot list is owned by the frontend, so adding a slot is not a protocol change. | same file, header comment |
| A user can move any view to a slot or hand placement back to the extension. The choice is durable intent in `desktop.surface_slots`: at most 64 entries, keys ≤ 128 bytes, slots ≤ 64 bytes. | `ui/SlottedView.tsx`, `internal/frontend/serve/surfaces.go` |
| A `view` names a slot (≤ 64 bytes) or anchors to `tool:<callId>`, never both. Nothing else is addressable. | `internal/ext/extension/protocol/dto_view.go` |
| View bodies render through a closed A2UI catalogue (`reasonix:studio:surface:v1`). | `ui/surface/a2ui/catalog.tsx` |
| Surface kinds: `status`, `card`, `form`, `notification`, `panel`, `view`. Protocol DTOs state that HTML, CSS, JavaScript and URLs are never accepted. | `protocol/dto_ui.go`, `protocol/enums.go` |
| Path of a publication: protocol DTO → `event.ExtensionSurfacePayload` → `eventwire.ExtensionSurface` → `port/wire.ts`, held together by `wire-parity`. | `internal/contract/event/extension.go`, `tools/repolint/wireparity.go` |
| The hub keeps at most 64 current publications; `GET /extensions/surfaces` restores them after a reload. A retired generation returns `surface.expired`. | `internal/ext/extension/uihub/replay.go`, `docs/design/INTELLIGENT_UI.md` |
| ACP clients without `reasonix.extensionSurface` receive a flattened text fallback. | `internal/frontend/acp/dispatch.go` |
| The kernel credential is an HttpOnly, SameSite=Strict, host-only cookie; writes need the exact `Origin`. The page lives under `/_studio/`. | `docs/STUDIO_SHELL_BOUNDARIES.md` §2–3 |
| Untrusted HTML is already served by the kernel with `Content-Security-Policy: default-src 'none'; sandbox`. | `internal/frontend/serve/workspace_preview.go` |
| The main window runs `sandbox`, `contextIsolation`, no `nodeIntegration`. `will-navigate` guards the top frame only; IPC handlers compare `event.sender`, not `event.senderFrame`. The main session sets no permission handler. | `desktop/electron/src/main.js` |

## Part 1: wider slots

### Slot vocabulary

1. The protocol documents a vocabulary of well-known slot names. It MUST stay advisory: no enum, no schema change, unknown names keep falling back.
2. Studio implements every name below. Other frontends MAY map a name to a place of their own or fall back.
3. Existing names keep working: `sidebar` is an alias of `rail`, and `composer-trailing` and `above_prompt` are aliases of `composer.above`.

| Slot | Place in Studio | Accepts | Height | Capacity |
| --- | --- | --- | --- | --- |
| `rail` | Side rail panel list (today's `sidebar`) | view, panel, frame | host decides; frame 120–640 px | unbounded, scrolls |
| `composer.above` | Band above the composer (today's `composer-trailing`) | view, frame | view one row; frame 32–160 px | 2 |
| `session.header` | Trailing edge of the conversation header | view limited to `text`, `pip`, `meter`, `button` | one line | 3 |
| `pane.tab` | A tab in the document/preview pane, opened from the rail or the header | view, frame | fills the pane | 4 per session |
| `settings.section` | The extension's own section on the Extensions settings page | view, form, frame | 120–960 px | 1 per plugin |
| `home.card` | The new-session empty state | view | host decides | 3 |

### Rules

| ID | Rule |
| --- | --- |
| S1 | A slot only ever **adds** content in host-drawn chrome. No slot overlays, replaces or hides UI Studio draws. |
| S2 | Every slotted surface is drawn inside a host frame that names its plugin and carries the existing move menu. The extension cannot hide either. |
| S3 | Placement is resolved in this order: the user's assignment, the slot the extension asked for, then `rail`. A slot over capacity places the extra surfaces in `rail`, never nowhere. |
| S4 | Order inside a slot is the user's assignment order, then `pluginId`, then `surfaceId`. Publish time MUST NOT order anything, so a republish never moves a surface. |
| S5 | A surface keeps its React key (`pluginId:surfaceId`) across moves and republishes. Mounting, replay and moving owe no entrance motion. |
| S6 | A slot that restricts node kinds (`session.header`) renders a disallowed node as its text value. It does not drop the surface or reject it. |
| S7 | `pane.tab` and `settings.section` are opened by the user. Publishing into them MUST NOT focus, open or switch a tab. |
| S8 | `settings.section` holds the extension's own settings. It MUST NOT present Reasonix settings, keys or permissions, and actions there reach only the extension. |
| S9 | The move menu lists every slot the surface's kind fits. `desktop.surface_slots` keeps its existing limits. |

### Changes

| Layer | Change |
| --- | --- |
| Protocol docs | Add the vocabulary table to `docs/EXTENSION_PROTOCOL.md` under Structured UI. |
| Kernel | None. `MaxViewSlotID` already bounds names, and `surfaces.go` already stores any slot name. |
| Studio | Widen `SLOTS`, add the aliases, generalise `useSurfaceSlots` from a two-way split into a per-slot map, and mount a slot host in each place listed above. |
| TUI, ACP | None. Unknown names already fall back. |

## Part 2: sandboxed frames

### Model

1. A frame is a static HTML document that ships **inside the installed plugin package**. The protocol never carries markup.
2. The sidecar publishes a `frame` surface that names a declared frame and passes JSON props. The host serves the document and hands it the props.
3. The document runs in a sandboxed iframe with an opaque origin and no network. It talks to the host over one `MessageChannel`, and that channel can only do what the sidecar is already allowed to do.

### Manifest

```json
{
  "apiVersion": "reasonix.io/plugin/v2",
  "contributes": {
    "frames": [
      { "id": "board", "entry": "frames/board.html", "title": "Task board" }
    ]
  },
  "runtime": { "command": "./bin/board", "capabilities": ["ui"] }
}
```

| ID | Rule |
| --- | --- |
| M1 | `id` matches `^[a-z][a-z0-9-]{0,31}$` and is unique within the package. At most 8 frames per package. |
| M2 | `entry` is a relative path inside the package root ending in `.html`. Traversal, absolute paths and symlinks that leave the root are refused at parse time and again when the file is served. |
| M3 | A package with `frames` MUST declare a runtime with the `ui` capability. The existing check in `sidecar/client.go` is extended to frames. |
| M4 | The install preview (`installsource`, market) lists each frame's id, title, size and SHA-256. A market digest covers frame files like any other package file. |
| M5 | `ManifestExpectation` gains an optional `uiFrames` list. Publishing a frame outside it fails with `capability_not_declared`. |

### Protocol additions

All of these are new enum values, new optional fields or new methods, which major version 2 permits (`docs/EXTENSION_PROTOCOL.md`, Stability contract).

| Item | Shape |
| --- | --- |
| Surface kind | `frame` added to `UISurfaceKind`. |
| Payload | `UIFramePayload { frame, slot?, title, fallback, props? }`. `fallback` is required plain text (≤ 600 bytes) for frontends that cannot draw frames. `props` is a JSON object, ≤ 64 KiB encoded. |
| Host capability | `HostCapabilities.uiFrames bool`. Desktop and web serve set it; TUI, ACP and headless leave it false. A sidecar SHOULD publish a `view` instead when it is false. |
| Expectation | `ManifestExpectation.uiFrames []string`, from the manifest. |
| Submit target | `extension/ui/submit` accepts a frame's `surfaceId`; `values` ≤ 64 KiB. |
| Event and wire | `ExtensionFrameView` on `event.ExtensionSurfacePayload`, `eventwire`, and `port/wire.ts`; new `wire-parity` row. |
| Docs | Amend the `dto_ui.go` package comment: protocol messages carry no markup, and frame markup comes only from installed package files. |

| ID | Rule |
| --- | --- |
| P1 | `frame` payloads reject `anchor`. A `slot` outside `rail`, `composer.above`, `pane.tab` and `settings.section` falls back to `rail`. |
| P2 | Every string leaf in `props`, plus `title` and `fallback`, passes the host credential-redaction pass before it is stored or sent. |
| P3 | A session holds at most 6 live frame surfaces. A seventh is refused with `ui.frame_limit` and the earlier ones stay. |
| P4 | Republishing the same `surfaceId` with the same `frame` updates props in place. A different `frame` id is a new document. |
| P5 | Replay restores identity, frame id, title, fallback and props from the hub snapshot. The wire log keeps recording identity only. A retired generation still returns `surface.expired`. |
| P6 | ACP and TUI render `title` and `fallback` through their existing text paths. |

### Serving the document

`GET /extensions/frames/{pluginId}/{frameId}?generation={n}` in a new file, `internal/frontend/serve/extension_frames.go`.

| ID | Rule |
| --- | --- |
| F1 | The route sits behind the loopback gate like every other kernel route. The iframe navigation is same-site, so it carries the cookie. Nothing inside the frame can reuse it (see B3). |
| F2 | The file is read with `Lstat` and a rooted open. It MUST be a regular file inside the package root, ≤ 2 MiB, valid UTF-8. Otherwise the route refuses with the typed codes below. |
| F3 | A `generation` that is not the current one is refused with `ui.frame_stale`. A frame id not in the manifest is refused with `ui.frame_not_declared`. |
| F4 | Response headers are fixed by the kernel and identical for every frame (next table). |
| F5 | The document MUST be self-contained: inline scripts and styles, images and fonts as `data:` URIs. External subresources are blocked by the CSP and fail closed. |

| Header | Value |
| --- | --- |
| `Content-Security-Policy` | `sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; worker-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'` |
| `Content-Type` | `text/html; charset=utf-8` |
| `X-Content-Type-Options` | `nosniff` |
| `Cache-Control` | `no-store` |
| `Cross-Origin-Resource-Policy` | `same-origin` |
| `ETag` | SHA-256 of the file |

`'unsafe-inline'` is acceptable only because B1–B4 leave the document with nothing to reach: no origin, no credential, no network, no host realm.

Typed refusals, through `refuse` (the `refusal-path` rule applies): `ui.frame_not_declared`, `ui.frame_missing`, `ui.frame_too_large`, `ui.frame_not_utf8`, `ui.frame_outside_root`, `ui.frame_stale`, `ui.frame_limit`. Each is added to the frontend's known codes so the parity test holds.

### Studio frame host

The iframe Studio draws:

```html
<iframe
  src="/extensions/frames/{pluginId}/{frameId}?generation={n}"
  sandbox="allow-scripts"
  allow=""
  referrerpolicy="no-referrer"
  loading="lazy"
  title="{title} — {pluginId}">
</iframe>
```

| ID | Rule |
| --- | --- |
| H1 | `sandbox` is exactly `allow-scripts`. Adding `allow-same-origin`, `allow-top-navigation*`, `allow-popups*`, `allow-forms`, `allow-modals` or `allow-downloads` is a security-review change, never a fix. |
| H2 | `allow=""` grants no permissions-policy feature: no camera, microphone, geolocation, clipboard, fullscreen or payment. |
| H3 | The frame sits inside host chrome that shows the plugin name and an "extension content" mark. Its rectangle never overlaps host UI and is never above the composer's submit row. |
| H4 | Height follows the frame's `size` message, clamped to the slot range. Width is the slot's. An extension cannot request a position. |
| H5 | Theme changes send new tokens over the channel and do not reload the document. Studio passes the semantic tokens from `src/styles/TOKENS.md` as CSS custom properties, never raw palette values. |
| H6 | A props republish sends `props` over the channel and does not reload the document (P4). |
| H7 | Moving a frame between slots reloads the document, because re-parenting an iframe reloads it. The host keeps the frame's last `state` (B6) in memory and passes it in `init`, with `reason: "remount"`, so the frame can restore state without replaying motion (S5). |
| H8 | Load failure, a crashed frame process or a `ui.frame_*` refusal replaces the frame with its `fallback` text and a retry control. It never takes down the pane. |

### Bridge protocol (`reasonix.frame.v1`)

After `load`, Studio creates a `MessageChannel`, keeps `port1`, and posts `port2` to `iframe.contentWindow` with target origin `*`. An opaque origin cannot be named, so the channel is the identity. All later traffic uses the port.

| Direction | Message | Limits |
| --- | --- | --- |
| host → frame | `init { protocol, props, theme, locale, colorScheme, readOnly, state?, reason }` | once per document |
| host → frame | `props { props }`, `theme { theme, colorScheme }`, `readOnly { readOnly }` | – |
| frame → host | `ready {}` | first message; anything earlier is dropped |
| frame → host | `size { height }` | ≤ 10/s, clamped (H4) |
| frame → host | `action { actionId, args? }` | declared `uiActions` only; `args` ≤ 16 string pairs, each ≤ 1 KiB; ≤ 5/s |
| frame → host | `submit { values }` | ≤ 64 KiB; ≤ 1/s |
| frame → host | `state { value }` | ≤ 16 KiB JSON; renderer memory only; cleared on a new generation |

| ID | Rule |
| --- | --- |
| B1 | The host accepts messages only from its own `port1`. `window` `message` events after the handshake are ignored. |
| B2 | Each message is strict-decoded against its schema. An unknown type, an extra field or an over-limit message is dropped and counted; three drops in ten seconds close the port and show the fallback. |
| B3 | `action` and `submit` go to the existing `POST /extensions/action` and `POST /extensions/submit`, issued by the Studio page, which holds the credential. The frame never sees the credential or a URL to the kernel. |
| B4 | The bridge has no message that sends a prompt, answers an ask, settles an approval, changes a setting, reads the session, opens a URL or reads the clipboard. Adding one needs its own design and security review. |
| B5 | While the surface is `readOnly` (expired generation, stale replay), `action` and `submit` are refused locally and the frame is told. |
| B6 | `state` is an opaque value the host stores and returns on remount. It is never persisted, never sent to the kernel and never shown to the model. |

### Electron hardening

These close gaps in `desktop/electron/src/main.js` that frames would otherwise inherit.

| ID | Change |
| --- | --- |
| E1 | Add a `will-frame-navigate` guard. A frame under `/extensions/frames/` may only load that URL; every other subframe navigation is refused. |
| E2 | Set a permission request and check handler on the main window's session that denies every request whose requesting frame is not the main frame. |
| E3 | IPC handlers in `fromWindow` also require `event.senderFrame === win.webContents.mainFrame`. |
| E4 | Keep `nodeIntegrationInSubFrames` off, so the preload and `window.reasonixHost` exist only in the main frame. A smoke check asserts it. |
| E5 | Frames MUST run in a renderer process separate from the Studio page. The smoke test compares `osProcessId` of the frame's `WebFrameMain` with the main frame's. If Electron 44 does not separate them by default, enable Chromium's `IsolateSandboxedIframes` feature. |

## Security boundary

### What a frame can and cannot reach

| Asset | Reachable from a frame? | What holds it |
| --- | --- | --- |
| Studio page DOM and JS realm | No | Opaque origin (`sandbox` without `allow-same-origin`); separate process (E5) |
| Preload bridge, `window.reasonixHost` | No | Preload runs in the main frame only (E4); IPC checks the sender frame (E3) |
| Kernel credential | No | HttpOnly cookie; SameSite=Strict is not sent from an opaque initiator; `connect-src 'none'` |
| Kernel HTTP routes | No | No credential; writes need the exact `Origin` and an opaque origin sends `null` |
| Approvals, permission prompts, error states | No | Not addressable by slot or anchor (S1, P1); no bridge message (B4) |
| Other plugins' frames or surfaces | No | One channel per frame; the host routes by surface identity |
| Network | No subresource, no fetch | CSP (F4) and `will-frame-navigate` (E1). The browser frontend lacks E1; see Residual risks. |
| OS permissions (camera, location, clipboard…) | No | `allow=""` (H2), permission handler (E2) |
| Its own sidecar | Yes, through declared actions and submit | The protocol's existing subset rule |

### Trust classification

1. Frames do not raise extension trust. A runtime extension is already full trust (`docs/EXTENSIONS.md`): its sidecar can read files and reach the network. A frame can only send declared actions and submit values to that same sidecar.
2. What frames add is **presentation risk**: they can draw anything inside their rectangle, including something that looks like Studio. Mitigations: the chrome mark (H3), no overlay slots (S1), no frames in the transcript or approval region (Non-goals), and no permissions or credentials to collect (H2, B4).
3. `internal/frontend/serve/extension_frames.go` and the Electron guards in E1–E3 decide what a frame can reach. Both SHOULD be added to the `sensitive:` list in `REASONIX.md`, so every change needs `review` plus `security_review` and the coverage gate holds them.

### Residual risks

| Risk | Status |
| --- | --- |
| A frame imitates Studio UI and asks the user for a secret. | Mitigated by H3; not eliminated. Anything the user types goes only to the plugin's own sidecar, which is already full trust. |
| In the browser frontend (`reasonix serve`), a frame can navigate itself to an external URL and leak its props. | Open. Props come from the sidecar, which already has the network, so no new data leaves. Browsers have no `will-frame-navigate` equivalent. |
| Speculative side channels between the frame and the page. | Mitigated by process isolation (E5). Browser frontends depend on the browser's own isolation. |
| A frame burns CPU or memory. | Bounded by P3 and lazy loading. A per-frame process can be killed by the user's OS like any tab; Studio shows the fallback (H8). |

## Delivery phases

| Phase | Scope | Touches sensitive paths |
| --- | --- | --- |
| 1 | Slot vocabulary, Studio slot hosts, aliases, move menu | No |
| 2 | Manifest `frames`, protocol additions, schema regeneration, redaction, hub and replay | No |
| 3 | Frame route, Electron hardening E1–E5, `sensitive:` declarations | Yes |
| 4 | Studio frame host and bridge | No (presentation), reviewed with phase 3 |
| 5 | Install preview, market listing, Go SDK helper (`sdk/go`), starter frame example, `docs/EXTENSIONS.md` | No |

Phase 1 is useful without the others and SHOULD land first. Phases 3 and 4 MUST ship together, so no build ever renders frames without its guards.

## Tests

Tests are written first and fail without the change (`REASONIX.md`, Change discipline).

### Kernel (Go)

| Area | Test |
| --- | --- |
| Protocol | `DecodeUIPublishPayload("frame", …)`: unknown fields, missing `fallback`, oversize `props`, an `anchor`, an undeclared frame id are each refused with their identity. |
| Schema | `schema.generated.json` and `EXTENSION_PROTOCOL.generated.md` regenerated; `TestGeneratedArtifactsAreDeterministicAndCommitted` passes. |
| Handshake | A sidecar publishing a frame outside `uiFrames` gets `capability_not_declared`. A package with frames and no `ui` runtime fails to parse. |
| Manifest | `contributes.frames`: id pattern, count limit, traversal, absolute path, symlink out of root, non-`.html` entry. |
| Redaction | A credential-shaped string at any depth of `props`, in `title` or in `fallback` is redacted before reaching the event sink. |
| Hub | Frame limit (P3); same-frame republish updates props (P4); replay restores props; a retired generation answers `surface.expired`. |
| Route | Exact header set (F4); each refusal code (F2–F3); no response without the loopback credential; `HEAD` and `POST` refused; a file swapped for a symlink between parse and serve is refused. |
| Effect | Through `boot.Build`: a fixture sidecar publishes a frame, and the frontend sink receives identity, title, fallback and redacted props. A second build with no frame-capable extension leaves the provider request byte-identical. |
| ACP | A client without `reasonix.extensionSurface` receives `title` and `fallback` as text; one with it receives the structured DTO. |
| Parity | `wire-parity` row for `ExtensionFrameView`; the refusal-code parity test knows every `ui.frame_*` code. |

### Studio (vitest interaction tests)

| Area | Test |
| --- | --- |
| Slots | Every vocabulary name and alias resolves; an unknown name falls to `rail`; overflow goes to `rail` (S3); order ignores publish time (S4); `session.header` renders disallowed nodes as text (S6). |
| Placement | The move menu lists the slots that fit the kind; a move persists through `POST /surfaces`; "hand back" clears it; a reload restores it. |
| Motion | Replay, remount and a move cause no entrance animation (S5). |
| Bridge | Messages from `window` and foreign ports are ignored; a malformed or over-limit message is dropped; three drops close the port; an undeclared action is refused; rate limits hold; `readOnly` blocks action and submit. |
| Lifecycle | A props republish and a theme switch do not reload the iframe; a move reloads and sends `reason: "remount"` with the saved state; a load failure shows the fallback and retry. |
| Attributes | The rendered iframe has exactly `sandbox="allow-scripts"`, `allow=""` and `referrerpolicy="no-referrer"`. |
| Neighbourhood | Existing panel, card, view, form and notification rendering; `surface-replay`, `ExtensionCard` and `extension-*` suites; composer submit, queue, withdraw, steer and restore with a frame in `composer.above`. |

### Electron

| Area | Test |
| --- | --- |
| Smoke (`desktop/electron/test/smoke.js`) | Frame process differs from the main frame (E5); `window.reasonixHost` is undefined inside the frame (E4); an IPC call from a subframe is refused (E3). |
| Navigation | A frame that sets `location` to an external URL or a kernel route stays put (E1); `window.open` is denied. |
| Permissions | A frame's `getUserMedia`, geolocation and clipboard requests are denied (E2, H2). |
| Credential | A frame's `fetch` to any kernel route fails; a frame-driven navigation to a write route is refused by the loopback gate. |
| Live | Against the real kernel, a fixture plugin's frame loads through the gate, round-trips one action to its sidecar, and survives a theme switch without reloading. |

### Verification commands

```bash
gofmt -w .
```

```bash
go vet ./...
```

```bash
make lint
```

```bash
go test ./internal/ext/... ./internal/frontend/serve/ ./internal/frontend/acp/ ./internal/assembly/boot/
```

```bash
make check
```

```bash
make coverage-gate
```

## Open questions

| Question | Default if unanswered |
| --- | --- |
| Does Electron 44 isolate sandboxed iframes into their own process by default? | Enable `IsolateSandboxedIframes` (E5) and keep the smoke check. |
| Should a frame be able to open an external link through a host confirmation? | No; links inside frames do nothing in v1. |
| Should a frame get a declared network allow-list (`connect-src` to manifest hosts)? | No; route data through the sidecar. |
| Should frames be allowed in tool-card anchors once transcript virtualisation can keep them alive? | No until that remount cost is measured. |
| Should the TUI get a richer fallback than `title` and `fallback`? | No; TUI surface work stays deferred. |
