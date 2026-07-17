# Reasonix Theme Pack V1

Native theme packs for the Reasonix desktop app. Packs are controlled skins:
semantic color tokens, density/corner recipes, and an optional local background
image. They **cannot** run CSS, JavaScript, fonts, remote URLs, or SVG scripts.

> Chinese: [THEME_PACK.zh-CN.md](./THEME_PACK.zh-CN.md)

## Goals (first release)

- Built-in styles, user themes, backgrounds, live preview, import/export, local library
- Full background on the home (empty) scene; reduced opacity + directional overlay on task scenes
- Works with Classic / Workbench / Creation and `auto` / `light` / `dark`
- **No** online marketplace, cloud sync, or script plugins

## Package format

Distribute as a `.reasonix-theme` ZIP. The archive root may contain **only**:

| File | Required | Notes |
| --- | --- | --- |
| `theme.json` | yes | Manifest (≤ 1 MiB) |
| `background.png` / `.jpg` / `.jpeg` / `.webp` | no | Single image ≤ 16 MiB, ≤ 8192×8192 |

ZIP limits: package ≤ 20 MiB; no nested directories, no symlinks, no duplicate entries, no path traversal.

### `theme.json` example

```json
{
  "schemaVersion": 1,
  "id": "my-theme",
  "name": "My Theme",
  "author": "",
  "description": "",
  "license": "",
  "baseStyle": "graphite",
  "tokens": {
    "light": {
      "bg": "#f4f3ef",
      "fg": "#111827",
      "accent": "#2f5fa8"
    },
    "dark": {
      "bg": "#0c0d10",
      "fg": "#f1f1ef",
      "accent": "#ff6a3d"
    }
  },
  "recipes": {
    "density": "comfortable",
    "corners": "soft"
  },
  "background": {
    "image": "background.webp",
    "focusX": 0.72,
    "focusY": 0.45,
    "safeArea": "left",
    "homeOpacity": 1,
    "taskOpacity": 0.28,
    "overlayStrength": 0.62
  }
}
```

JSON Schema: [theme-pack.schema.json](./theme-pack.schema.json)

### Fields

| Field | Rules |
| --- | --- |
| `schemaVersion` | Must be `1` |
| `id` | Lowercase `[a-z][a-z0-9-]*`, reserved: `graphite`, `aurora`, `slate`, `carbon`, `nocturne`, `amber` |
| `baseStyle` | One of the six built-in directions; uncovered tokens inherit it |
| `tokens.light` / `tokens.dark` | Optional maps of semantic keys → `#RRGGBB` or `#RRGGBBAA` only |
| `recipes.density` | `compact` \| `comfortable` |
| `recipes.corners` | `square` \| `soft` \| `round` |
| `background.image` | Bare file name only (png/jpeg/webp) |
| `background.focusX/Y` | 0–1 focal point |
| `background.safeArea` | `left` \| `right` \| `center` (task overlay direction) |
| `background.homeOpacity` | 0–1 |
| `background.taskOpacity` | 0–0.45 (hard cap) |
| `background.overlayStrength` | 0–1 |

### Allowed token keys

`bg`, `bgSoft`, `bgElev`, `panel`, `sidebar`, `chat`, `workspace`, `workspaceFiles`,
`border`, `borderSoft`, `fg`, `fgDim`, `fgFaint`, `accent`, `accentFg`, `ok`, `warn`, `err`

Colors must **not** include `url()`, gradients, or arbitrary CSS.

## Engine behavior

1. Apply global `auto` / `light` / `dark` and the base visual style.
2. Apply the pack overlay (CSS custom properties) **after** stylesheets so it wins over trailing `:root` and Creation locals.
3. Root gets `data-theme-pack="<id>"`; the app container gets `data-theme-scene="home|task"`.
4. Scene is derived only from whether the current session has content — it does not change chat lifecycle.
5. Background is a fixed, non-interactive layer. Task scene dims the image and paints a directional wash (**no** `backdrop-filter`).

## Storage

| Path under Reasonix home | Purpose |
| --- | --- |
| `desktop-theme-state.json` | Versioned active theme pointer (not `config.toml`) |
| `themes/<id>/` | User theme library (`theme.json` + optional image) |

Legacy installs without theme state keep the previous appearance. Old app versions ignore the new directory. CLI theme, prompts, provider requests, and cache keys are unchanged.

## Desktop bridge (frontend)

List / activate / reset / save / delete / copy / import / export / pick background.
The UI only receives temporary asset URLs (`/__reasonix_theme_asset/...`) or data URLs — never absolute host paths.

Import: same id is rejected until the user confirms atomic replace. Built-ins cannot be overwritten or deleted. Corrupt / missing packs fall back to the Graphite path. Safe mode does not load external themes. `/theme reset` and the command palette restore entry clear the pack.

## Authoring tips

1. Start from a built-in direction and override only the tokens you need.
2. Prefer WCAG AA contrast (≈ 4.5:1 body text). The editor warns but does not block save.
3. Before sharing a pack with a photo or portrait, confirm redistribution rights.
4. Do not ship third-party or copyrighted reference assets from other products.

## Template

A minimal, royalty-free starter (no portrait photos):

```json
{
  "schemaVersion": 1,
  "id": "paper-dawn",
  "name": "Paper Dawn",
  "author": "Reasonix",
  "description": "Template theme — solid tokens only, no background image.",
  "license": "CC0-1.0",
  "baseStyle": "graphite",
  "tokens": {
    "light": {
      "bg": "#f7f4ef",
      "panel": "#ffffff",
      "sidebar": "#f3efe8",
      "chat": "#fbfaf7",
      "fg": "#1c1917",
      "fgDim": "#57534e",
      "fgFaint": "#a8a29e",
      "border": "#e7e5e4",
      "accent": "#c2410c",
      "accentFg": "#fff7ed",
      "ok": "#15803d",
      "warn": "#b45309",
      "err": "#b91c1c"
    },
    "dark": {
      "bg": "#0c0b0a",
      "panel": "#171412",
      "sidebar": "#141210",
      "chat": "#0c0b0a",
      "fg": "#f5f5f4",
      "fgDim": "#a8a29e",
      "fgFaint": "#78716c",
      "border": "#292524",
      "accent": "#fb923c",
      "accentFg": "#0c0b0a",
      "ok": "#4ade80",
      "warn": "#fbbf24",
      "err": "#f87171"
    }
  },
  "recipes": {
    "density": "comfortable",
    "corners": "soft"
  }
}
```

Zip as `paper-dawn.reasonix-theme` with only `theme.json` at the root.
