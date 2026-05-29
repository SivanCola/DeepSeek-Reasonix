# 我对 DeepSeek-Reasonix 的贡献记录

[English](README.md)

这个 fork 用来展示我在官方上游仓库
[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)
中的贡献记录；安装、发布版本和权威文档请以官方仓库为准。

## 数据源

官方上游仓库中由 `SivanCola` 创建的 closed PR：

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed+author%3ASivanCola

最后刷新时间：2026-05-29。

## 已合入贡献

| PR | 状态 | 日期 | 贡献内容 | PR 元数据 |
| --- | --- | --- | --- | --- |
| [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192) | 已合入 | 2026-05-29 | 探索远程工作区支持，为后续 SSH 远程开发提供安全预览路径。 | 3 个文件，+333/-0 |
| [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191) | 已合入 | 2026-05-29 | 让 agent 能更准确地删除完整代码单元，减少手动改错。 | 10 个文件，+444/-11 |
| [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190) | 已合入 | 2026-05-29 | 提供更可靠的大段内容删除能力，降低长文本改动出错率。 | 11 个文件，+314/-15 |
| [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189) | 已合入 | 2026-05-29 | 让 MCP 工具加载更稳定，帮助保持缓存表现一致。 | 2 个文件，+138/-11 |
| [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188) | 已合入 | 2026-05-29 | 增加缓存诊断，让用户更容易理解缓存变化。 | 20 个文件，+795/-18 |
| [#2144](https://github.com/esengine/DeepSeek-Reasonix/pull/2144) | 已合入 | 2026-05-29 | 让桌面端关闭窗口后仍可保留运行中的会话。 | 13 个文件，+249/-21 |
| [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) | 已合入 | 2026-05-29 | 完善 MCP 管理体验，支持导入、编辑、状态查看、重试和清晰反馈。 | 28 个文件，+2710/-121 |
| [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) | 已合入 | 2026-05-29 | 增加更多主题与文案，让 CLI 和桌面端有更好的视觉选择。 | 31 个文件，+1042/-74 |

以上 upstream PR 元数据合计：8 个已合入 PR，118 个 changed-file entries，
+6025/-271 行。

## 贡献主题

- Cache-first engineering：缓存诊断、稳定 MCP fingerprint，以及减少 token-heavy edit operations 的工具。
- Desktop lifecycle：close-to-tray、托盘控制、Dock reopen、设置和配置支持。
- MCP UX：导入、编辑、状态、重试、reload、工具展示和多语言 UI 打磨。
- Product polish：补充 Reasonix 在终端和桌面端的主题配色。

## 分支用途

这个分支适合作为我的 fork 的默认落地页。常规开发分支可以继续贴近 upstream，而这个分支让访问者第一眼看到我已经合入官方仓库的具体贡献。
