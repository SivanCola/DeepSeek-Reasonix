# Reasonix 主题包 V1

Reasonix 桌面端原生主题包。主题是**受控皮肤**：语义颜色令牌、密度/圆角配方，以及可选的本地背景图。主题**不能**执行 CSS、JavaScript、加载字体、远程 URL 或 SVG 脚本。

> English: [THEME_PACK.md](./THEME_PACK.md)

## 首版目标

- 内置风格、自建主题、背景图、实时预览、导入/导出、本地主题库
- 首页完整展示背景；进入任务后自动降低透明度并增加方向性遮罩
- 同时支持 Classic / Workbench / Creation，以及 `auto` / `light` / `dark`
- **不包含**在线主题市场、云同步或脚本插件

## 包格式

以 `.reasonix-theme` ZIP 分发。根目录**只能**包含：

| 文件 | 必需 | 说明 |
| --- | --- | --- |
| `theme.json` | 是 | 清单（≤ 1 MiB） |
| `background.png` / `.jpg` / `.jpeg` / `.webp` | 否 | 单张图片 ≤ 16 MiB，边长 ≤ 8192 |

ZIP 限制：包体 ≤ 20 MiB；禁止子目录、符号链接、重复条目与路径穿越。

### `theme.json` 示例

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

JSON Schema： [theme-pack.schema.json](./theme-pack.schema.json)

### 字段规则

| 字段 | 规则 |
| --- | --- |
| `schemaVersion` | 必须为 `1` |
| `id` | 小写 `[a-z][a-z0-9-]*`；保留：`graphite`/`aurora`/`slate`/`carbon`/`nocturne`/`amber` |
| `baseStyle` | 六套内置方向之一；未覆盖令牌继承该方向 |
| `tokens.light` / `tokens.dark` | 可选；键 → `#RRGGBB` 或 `#RRGGBBAA` |
| `recipes.density` | `compact` \| `comfortable` |
| `recipes.corners` | `square` \| `soft` \| `round` |
| `background.image` | 仅允许裸文件名（png/jpeg/webp） |
| `background.focusX/Y` | 0–1 焦点 |
| `background.safeArea` | `left` \| `right` \| `center`（任务页遮罩方向） |
| `background.homeOpacity` | 0–1 |
| `background.taskOpacity` | 0–0.45（硬上限） |
| `background.overlayStrength` | 0–1 |

### 允许的令牌键

`bg`, `bgSoft`, `bgElev`, `panel`, `sidebar`, `chat`, `workspace`, `workspaceFiles`,
`border`, `borderSoft`, `fg`, `fgDim`, `fgFaint`, `accent`, `accentFg`, `ok`, `warn`, `err`

颜色**不得**包含 `url()`、渐变或任意 CSS。

## 引擎行为

1. 先应用全局 `auto`/`light`/`dark` 与基础视觉风格。
2. 再应用主题包覆盖层（CSS 变量），挂在样式表之后，避免被后置 `:root` 与 Creation 局部变量压掉。
3. 根节点 `data-theme-pack="<id>"`；应用容器 `data-theme-scene="home|task"`。
4. 场景仅由当前会话是否有内容决定，不改变聊天状态或布局生命周期。
5. 背景为独立、不可交互层。任务页限制最高透明度并叠加方向性遮罩（**不**使用 `backdrop-filter`）。

## 存储

| Reasonix 主目录路径 | 用途 |
| --- | --- |
| `desktop-theme-state.json` | 版本化的当前主题指针（**不**改 `config.toml`） |
| `themes/<id>/` | 用户主题库（`theme.json` + 可选图片） |

旧配置缺少主题状态时保持原行为；旧版本忽略新目录。CLI 主题、提示词、Provider 请求与缓存键均不变。

## 桌面桥接

列出 / 启用 / 重置 / 保存 / 删除 / 复制 / 导入 / 导出 / 选择背景。
前端只接收临时资源 URL（`/__reasonix_theme_asset/...`）或 data URL，不暴露本机绝对路径。

同 ID 导入默认拒绝，确认后才允许原子替换。内置主题不可覆盖或删除。损坏/丢失回退 Graphite 路径。安全模式不加载外部主题。`/theme reset` 与命令面板可恢复默认。

## 创作建议

1. 从内置方向起步，只覆盖需要的令牌。
2. 尽量满足 WCAG AA（正文约 4.5:1）；编辑器会警告但允许继续保存。
3. 分享含背景图的主题前，确认照片/肖像/第三方素材的分发权利。
4. 首版不复制参考仓库的人物或第三方图片资产。

## 模板

无版权素材的纯色模板（不含背景图）见英文版 [THEME_PACK.md](./THEME_PACK.md) 中的 `paper-dawn` 示例；将仅含 `theme.json` 的根目录打成 `paper-dawn.reasonix-theme` 即可导入。
