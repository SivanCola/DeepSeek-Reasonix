# My Contributions to DeepSeek-Reasonix

[Chinese version](README.zh-CN.md) | [Upstream project](https://github.com/esengine/DeepSeek-Reasonix)

This fork is a contribution showcase for my work on
[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix).

It is not a replacement for the official project. For installation, usage, releases,
and canonical documentation, please visit the upstream repository:

https://github.com/esengine/DeepSeek-Reasonix

## Data Source

The contribution list is sourced from the upstream repository's closed pull
requests:

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed

For this personal showcase, the list is filtered to PRs authored by
`SivanCola`:

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed+author%3ASivanCola

Last refreshed: 2026-05-29.

## Landed Contributions

| PR | Status | Date | Scope | PR metadata |
| --- | --- | --- | --- | --- |
| [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192) | Merged | 2026-05-29 | SSH remote workspace RFC and dry-run bootstrap for `ssh://` targets; design scaffold for [#2140](https://github.com/esengine/DeepSeek-Reasonix/issues/2140). | 3 files, +333/-0 |
| [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191) | Merged | 2026-05-29 | `delete_symbol`, an AST-aware symbol deletion tool powered by tree-sitter. | 10 files, +444/-11 |
| [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190) | Merged | 2026-05-29 | `delete_range`, a reliable large text deletion tool using anchor-based range matching. | 11 files, +314/-15 |
| [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189) | Merged | 2026-05-29 | MCP cache-stable canonicalization with deterministic tool ordering and schema key sorting. | 2 files, +138/-11 |
| [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188) | Merged | 2026-05-29 | Cache diagnostics v1: `/cache-miss-report`, `doctor --cache`, and prefix hash evidence. | 20 files, +795/-18 |
| [#2144](https://github.com/esengine/DeepSeek-Reasonix/pull/2144) | Merged | 2026-05-29 | Desktop close-to-tray support so users can keep sessions alive when the behavior is enabled. | 13 files, +249/-21 |
| [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) | Merged | 2026-05-29 | MCP feature enrichment: cc-switch import, edit/status UI, protocol updates, reload flow, and i18n. | 28 files, +2710/-121 |
| [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) | Merged | 2026-05-29 | Expanded Reasonix theme palettes across CLI, desktop, and dashboard surfaces with i18n labels. | 31 files, +1042/-74 |

Merged total from the upstream PR metadata above: 8 PRs, 118 changed-file entries,
+6025/-271 lines.

## Closed, Not Merged

These PRs are included because they appear in the same upstream closed-PR data
source. They are kept separate from landed contributions.

| PR | Status | Date | Outcome |
| --- | --- | --- | --- |
| [#2187](https://github.com/esengine/DeepSeek-Reasonix/pull/2187) | Closed | 2026-05-28 | Closed in favor of five separate PRs for the Cache-First Roadmap: [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188), [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189), [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190), [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191), and [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192). |
| [#2128](https://github.com/esengine/DeepSeek-Reasonix/pull/2128) | Closed | 2026-05-28 | Superseded by the bilingual theme palette PR [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134). |
| [#2125](https://github.com/esengine/DeepSeek-Reasonix/pull/2125) | Closed | 2026-05-28 | Withdrawn in favor of the bilingual MCP feature PR [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135). |

## Contribution Themes

- Cache-first engineering: diagnostics, stable MCP fingerprints, and tools that reduce token-heavy edit operations.
- Desktop lifecycle: close-to-tray behavior, tray controls, Dock reopen handling, settings, and configuration support.
- MCP UX: import, edit, status, retry, reload, tool display, and cross-language UI polish.
- Product polish: additional Reasonix theme palettes across terminal and desktop surfaces.

## Branch Purpose

This branch is designed as the landing page for my fork. The regular development
branch can stay close to upstream, while this branch gives visitors a quick, readable
view of the concrete contributions that landed in the official repository.

## Links

- Upstream repository: https://github.com/esengine/DeepSeek-Reasonix
- My fork: https://github.com/SivanCola/DeepSeek-Reasonix
- Source query: https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed
