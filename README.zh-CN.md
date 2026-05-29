# 我对 DeepSeek-Reasonix 的贡献记录

[English](README.md)

这个 fork 用来展示我在官方上游仓库
[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)
中的贡献记录；安装、发布版本和权威文档请以官方仓库为准。

## 数据源

贡献列表来自官方上游仓库的 closed pull requests：

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed

作为个人贡献展示，这里进一步筛选 `SivanCola` 创建的 PR：

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed+author%3ASivanCola

最后刷新时间：2026-05-29。

## 已合入贡献

| PR | 状态 | 日期 | 范围 | PR 元数据 |
| --- | --- | --- | --- | --- |
| [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192) | 已合入 | 2026-05-29 | SSH remote workspace RFC，为 `ssh://` 目标提供 dry-run bootstrap，也是 [#2140](https://github.com/esengine/DeepSeek-Reasonix/issues/2140) 的设计脚手架。 | 3 个文件，+333/-0 |
| [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191) | 已合入 | 2026-05-29 | `delete_symbol`：基于 tree-sitter 的 AST-aware 符号删除工具。 | 10 个文件，+444/-11 |
| [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190) | 已合入 | 2026-05-29 | `delete_range`：基于 anchor range matching 的可靠大块文本删除工具。 | 11 个文件，+314/-15 |
| [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189) | 已合入 | 2026-05-29 | MCP cache-stable canonicalization：稳定工具顺序和 schema key 排序。 | 2 个文件，+138/-11 |
| [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188) | 已合入 | 2026-05-29 | Cache diagnostics v1：`/cache-miss-report`、`doctor --cache` 和 prefix hash evidence。 | 20 个文件，+795/-18 |
| [#2144](https://github.com/esengine/DeepSeek-Reasonix/pull/2144) | 已合入 | 2026-05-29 | 桌面端 close-to-tray 支持，用户启用后关闭窗口可以让会话继续保活。 | 13 个文件，+249/-21 |
| [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) | 已合入 | 2026-05-29 | MCP 功能增强：cc-switch 导入、编辑/状态 UI、协议更新、reload 流程和 i18n。 | 28 个文件，+2710/-121 |
| [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) | 已合入 | 2026-05-29 | 扩展 Reasonix theme palettes，覆盖 CLI、desktop 和 dashboard，并补充 i18n labels。 | 31 个文件，+1042/-74 |

以上 upstream PR 元数据合计：8 个已合入 PR，118 个 changed-file entries，
+6025/-271 行。

## 已关闭但未合入

这些 PR 也出现在同一个 upstream closed-PR 数据源中，因此保留记录，但和已落地贡献分开展示。

| PR | 状态 | 日期 | 结果 |
| --- | --- | --- | --- |
| [#2187](https://github.com/esengine/DeepSeek-Reasonix/pull/2187) | 已关闭 | 2026-05-28 | 改为拆成 5 个独立 Cache-First Roadmap PR：[#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188)、[#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189)、[#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190)、[#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191)、[#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192)。 |
| [#2128](https://github.com/esengine/DeepSeek-Reasonix/pull/2128) | 已关闭 | 2026-05-28 | 被中英双语主题配色 PR [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) 取代。 |
| [#2125](https://github.com/esengine/DeepSeek-Reasonix/pull/2125) | 已关闭 | 2026-05-28 | 撤回，改由中英双语 MCP 功能 PR [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) 承接。 |

## 贡献主题

- Cache-first engineering：缓存诊断、稳定 MCP fingerprint，以及减少 token-heavy edit operations 的工具。
- Desktop lifecycle：close-to-tray、托盘控制、Dock reopen、设置和配置支持。
- MCP UX：导入、编辑、状态、重试、reload、工具展示和多语言 UI 打磨。
- Product polish：补充 Reasonix 在终端和桌面端的主题配色。

## 分支用途

这个分支适合作为我的 fork 的默认落地页。常规开发分支可以继续贴近 upstream，而这个分支让访问者第一眼看到我已经合入官方仓库的具体贡献。

## 链接

- 官方上游仓库：https://github.com/esengine/DeepSeek-Reasonix
- 我的 fork：https://github.com/SivanCola/DeepSeek-Reasonix
- 数据源：https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed
