---
owner: @esengine
backup: @SivanCola
author: @Oliver-Rui
status: active
reviewed: 2026-10-24
---

# First run without a picked workspace

Status: proposed. Nothing in this document is shipped unless §8 says so. It lives under `docs/design/` so the product-doc retrieval corpus (`docs/*.md`) cannot present it to a model as a feature.

Reference: how MiMo Desktop (observed 2026-10-24 on a machine running it) handles the same three first-run decisions — connect a model, pick a workspace, start a session.

Its behaviour below is read off installed artifacts (config schema, directory layout, state store), not source; the mapping onto this repository in §6 is grounded in source.

## 1. The problem

Studio's first launch asks three things at once, and only one of them is honestly decoupled:

| Decision | Today | Decoupled? |
| --- | --- | --- |
| Connect a model | onboarding screen, provider-only, skippable with "稍后再说", asks again next launch, workbench opens with a "去连接" way back | yes |
| Start a session | the composer is reachable as soon as the workbench opens | yes |
| Pick a workspace | `pickLaunchWorkspace(cwd, appExe, remembered, home)` — with nothing remembered, the first pane's root is the **user's home directory** | no |

The fallback chain in `cmd/reasonix-studio-host/launchroot.go` is careful about *which* cwd it trusts — a Start-menu launch inherits the install directory, and taking that as a project "puts the app's own folder in the sidebar again after every removal" — but its last resort is the home directory.

That folder was never chosen as a project. It is broad, it is sensitive, and it is not represented in the sidebar as anything the user picked, so a session running there reads as a temporary session: not in the user's project, scoped to a folder they did not choose, and removing it does not stick.

Everything else is already in place for a decoupled first run:

- Session transcripts persist in the global store (`SessionDir()` = `<user state>/sessions`), so a session without a chosen project is not lost.
- Each logical session already owns a private scratch directory (`SessionTemp`, rotated per session, deleted when it ends, surfaced to the model as `$TMPDIR`).
- Provider setup already refuses to block. The workspace is the last coupled decision.

## 2. What MiMo does instead

Session-first. The session is the primary unit; the workspace is a property of it, provisioned automatically:

| Decision | MiMo's answer |
| --- | --- |
| Connect a model | one global config file (`~/.config/mimocode/mimocode.jsonc`), a top-level `provider` block, no workspace field anywhere near it |
| Workspace | every session gets its own working directory, created automatically (`<container>/.mimo-sessions/<yyyy>/<mm>/<dd>/<slug>`). No picker, no prerequisite, no empty state |
| State | one store (`mimocode.db`) plus that sessions tree; nothing about a session depends on a folder the user picked |
| Projects | optional groupings, with a `global` scope that always exists — no session is ever homeless |

The observable rule: **a first run never presents a folder picker as a gate**. The app is usable in seconds with zero choices beyond the model key, and the workspace appears as a consequence of starting a session, not as a condition for it.

## 3. Principles

| ID | Principle |
| --- | --- |
| P1 | First run opens a usable session with no folder picked. The workspace the first pane gets is one the kernel owns, never the home directory. |
| P2 | An auto workspace is provisioned, not inherited: created on demand under the user state root, small, kernel-owned, named as auto. |
| P3 | Picking a real folder stays a separate, later act. Moving a session's work into a chosen project uses the workspace switch that already exists. |
| P4 | "Temporary" must mean "not yet in a project", never "will be deleted". Sessions in the auto workspace persist in the global store, which already holds them. |
| P5 | The home-directory fallback leaves the chain. cwd counts only when chosen; remembered workspaces next; the auto workspace last. |
| P6 | Nothing about setup waits on the workspace. Provider onboarding stays skippable and stays its own screen. |

## 4. The auto workspace

When `pickLaunchWorkspace` would fall through to `home`, provision instead:

```
<user state>/workspaces/auto-<instance>/
```

- Created on demand by the kernel at launch, like the session scratch directory already is.
- Used as the first pane's `WorkspaceRoot`; tools stay scoped to a small kernel-owned folder instead of the home directory.
- Shown in the sidebar as an auto workspace — labelled as such, removable, and replaceable by a real project. It is never added to the remembered-workspaces list, so it cannot become the sticky answer to every future launch.
- Per install, not per session, for v1: the session-scoped scratch directory already covers per-session temporary needs, and one row keeps the sidebar honest. (MiMo provisions per session; §7 asks whether that is worth copying.)

## 5. What this does not change

- Provider onboarding, its skip, and its "ask again next launch" behaviour.
- The global session store and project-scoped store; both keep working exactly as they do.
- `resolveWorkspaceRoot` and the workspace switch/prepare flows.
- The rule that a Start-menu launch must not adopt the install directory as a project.

## 6. Mapping onto Studio

| Concern | Where it lives | Change |
| --- | --- | --- |
| Launch workspace choice | `cmd/reasonix-studio-host/launchroot.go` | drop the `home` fallback; call the auto-workspace provisioner last |
| Auto workspace provisioning | new, beside the existing session-temp manager (`internal/session/control`) | create `<state>/workspaces/auto-<instance>` on demand; expose its root and its "auto" nature |
| Sidebar representation | `desktop/frontend-next`, the project list | an auto row that says what it is, removable, with "move to a project" using the existing workspace switch |
| Persistence | `SessionDirFor` | unchanged — sessions in the auto workspace already land in the global store; a test pins that |
| Remembrance | `serve.LaunchWorkspaces()` | the auto workspace is never written here |

## 7. Open questions

1. Per install or per session? Per install keeps one row; per session matches MiMo exactly but fills the sidebar with auto rows. Recommend per install for v1.
2. Should the auto workspace be excluded from backup/export and storage-move surfaces, the way scratch is?
3. When the user picks a real project while an auto-workspace session has history, does the session move (workspace switch) or stay and a new session open? The switch flow supports moving; the product answer is a review call.

## 8. Stages

| Stage | Content | Acceptance |
| --- | --- | --- |
| M1 | auto workspace provisioning; `home` leaves the fallback chain | a first launch with no remembered workspace opens on the auto root, not home; a test fails without the change |
| M2 | sidebar representation and removal | the auto row reads as auto, removes cleanly, and does not return on next launch |
| M3 | move-to-project | an auto-workspace session with history can be moved onto a chosen folder through the existing switch, transcript intact |

Nothing in this document is a commitment; it is the shape a review can argue with.
