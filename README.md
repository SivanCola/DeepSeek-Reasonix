# My Contributions to DeepSeek-Reasonix

[中文](README.zh-CN.md)

This fork showcases my contributions to the upstream
[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)
project; installation, releases, and canonical documentation belong there.

## Data Source

Closed upstream PRs authored by `SivanCola`:

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed+author%3ASivanCola

Last refreshed: 2026-05-29.

## Landed Contributions

| PR | Status | Date | Contribution | PR metadata |
| --- | --- | --- | --- | --- |
| [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192) | Merged | 2026-05-29 | Explored remote workspace support and added a safe preview path for future SSH workflows. | 3 files, +333/-0 |
| [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191) | Merged | 2026-05-29 | Made it easier for the agent to remove complete code units accurately. | 10 files, +444/-11 |
| [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190) | Merged | 2026-05-29 | Added a safer way to delete large content blocks with less manual copying. | 11 files, +314/-15 |
| [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189) | Merged | 2026-05-29 | Made MCP tool loading more stable, helping keep cache behavior consistent. | 2 files, +138/-11 |
| [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188) | Merged | 2026-05-29 | Added cache diagnostics so users can understand cache changes more clearly. | 20 files, +795/-18 |
| [#2144](https://github.com/esengine/DeepSeek-Reasonix/pull/2144) | Merged | 2026-05-29 | Let desktop users close the window while keeping active sessions running. | 13 files, +249/-21 |
| [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) | Merged | 2026-05-29 | Improved MCP management with import, edit, status, retry, and clearer feedback. | 28 files, +2710/-121 |
| [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) | Merged | 2026-05-29 | Added more themes and labels, improving visual choice across CLI and desktop. | 31 files, +1042/-74 |

Merged total from the upstream PR metadata above: 8 PRs, 118 changed-file entries,
+6025/-271 lines.

## Contribution Themes

- Cache-first engineering: diagnostics, stable MCP fingerprints, and tools that reduce token-heavy edit operations.
- Desktop lifecycle: close-to-tray behavior, tray controls, Dock reopen handling, settings, and configuration support.
- MCP UX: import, edit, status, retry, reload, tool display, and cross-language UI polish.
- Product polish: additional Reasonix theme palettes across terminal and desktop surfaces.

## Branch Purpose

This branch is designed as the landing page for my fork. The regular development
branch can stay close to upstream, while this branch gives visitors a quick, readable
view of the concrete contributions that landed in the official repository.
