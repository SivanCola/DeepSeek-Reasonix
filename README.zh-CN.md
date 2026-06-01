# 我对 DeepSeek-Reasonix 的贡献记录

[English](README.md) | 中文

这个 fork 用来展示我在官方上游仓库
[esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix)
中的贡献记录；安装、发布版本和权威文档请以官方仓库为准。

## 已合入贡献

| PR | 状态 | 日期 | 贡献内容 | PR 元数据 |
| --- | --- | --- | --- | --- |
| [#2607](https://github.com/esengine/DeepSeek-Reasonix/pull/2607) | 已合入 | 2026-06-01 | 强化 typed-nil 边界处理，让接口边缘场景安全失败而不是 panic。 | 18 个文件，+247/-16 |
| [#2621](https://github.com/esengine/DeepSeek-Reasonix/pull/2621) | 已合入 | 2026-06-01 | 梳理 MCP 和 Skills 抽屉层级，让导航更清晰、管理更紧凑。 | 5 个文件，+3977/-18 |
| [#2624](https://github.com/esengine/DeepSeek-Reasonix/pull/2624) | 已合入 | 2026-06-01 | 移除桌面工作区菜单中容易造成困惑的 No Project 选项。 | 1 个文件，+1/-5 |
| [#2601](https://github.com/esengine/DeepSeek-Reasonix/pull/2601) | 已合入 | 2026-06-01 | 让 CLI 审批提示优先使用数字选项，降低输入成本。 | 5 个文件，+11/-11 |
| [#2616](https://github.com/esengine/DeepSeek-Reasonix/pull/2616) | 已合入 | 2026-06-01 | 避免计划审批控件遮挡用户需要阅读的 proposal。 | 5 个文件，+278/-21 |
| [#2561](https://github.com/esengine/DeepSeek-Reasonix/pull/2561) | 已合入 | 2026-06-01 | 交付 v2 桌面工作区体验，包含布局、预览和能力面板。 | 35 个文件，+4466/-582 |
| [#2457](https://github.com/esengine/DeepSeek-Reasonix/pull/2457) | 已合入 | 2026-06-01 | 增加复杂任务自动计划门，让执行前的意图更清楚。 | 14 个文件，+693/-17 |
| [#2412](https://github.com/esengine/DeepSeek-Reasonix/pull/2412) | 已合入 | 2026-06-01 | 增加可复用 mock provider，让 agent 测试更容易维护。 | 2 个文件，+245/-0 |
| [#2517](https://github.com/esengine/DeepSeek-Reasonix/pull/2517) | 已合入 | 2026-06-01 | 折叠 composer 中的长粘贴文本，让大输入保持可读。 | 11 个文件，+433/-30 |
| [#2503](https://github.com/esengine/DeepSeek-Reasonix/pull/2503) | 已合入 | 2026-06-01 | 支持对已配置 MCP server 手动连接。 | 15 个文件，+187/-38 |
| [#2502](https://github.com/esengine/DeepSeek-Reasonix/pull/2502) | 已合入 | 2026-06-01 | 提升 MCP 启动韧性，单个 server 失败时不拖垮整体启动。 | 9 个文件，+342/-47 |
| [#2504](https://github.com/esengine/DeepSeek-Reasonix/pull/2504) | 已合入 | 2026-06-01 | 加固桌面图片附件的本地文件处理安全性。 | 5 个文件，+158/-7 |
| [#2508](https://github.com/esengine/DeepSeek-Reasonix/pull/2508) | 已合入 | 2026-06-01 | 为 CLI 增加粘贴图片附件支持。 | 12 个文件，+992/-26 |
| [#2408](https://github.com/esengine/DeepSeek-Reasonix/pull/2408) | 已合入 | 2026-06-01 | 为 v2 会话增加 DeepSeek thinking effort 控制。 | 3 个文件，+52/-16 |
| [#2410](https://github.com/esengine/DeepSeek-Reasonix/pull/2410) | 已合入 | 2026-06-01 | 为 v2 编辑流程增加 range 和 Go symbol 删除工具。 | 6 个文件，+746/-0 |
| [#2411](https://github.com/esengine/DeepSeek-Reasonix/pull/2411) | 已合入 | 2026-06-01 | 对已知只读 bash 命令跳过审批，减少不必要提示。 | 3 个文件，+316/-0 |
| [#2487](https://github.com/esengine/DeepSeek-Reasonix/pull/2487) | 已合入 | 2026-06-01 | 修复桌面端 transcript 流式输出时的自动滚动问题。 | 1 个文件，+20/-2 |
| [#2460](https://github.com/esengine/DeepSeek-Reasonix/pull/2460) | 已合入 | 2026-06-01 | 增加对话分支树，方便管理和浏览 chat session。 | 16 个文件，+806/-11 |
| [#2470](https://github.com/esengine/DeepSeek-Reasonix/pull/2470) | 已合入 | 2026-06-01 | 增加发布前缓存命中守卫，保护 cache-first 行为。 | 4 个文件，+223/-0 |
| [#2484](https://github.com/esengine/DeepSeek-Reasonix/pull/2484) | 已合入 | 2026-06-01 | 优化 CLI tool approval 提示，让动作和选项更容易理解。 | 7 个文件，+80/-20 |
| [#2403](https://github.com/esengine/DeepSeek-Reasonix/pull/2403) | 已合入 | 2026-05-31 | 对 MCP JSON schema 做 canonicalize，帮助稳定 prompt cache prefix。 | 7 个文件，+299/-20 |
| [#2402](https://github.com/esengine/DeepSeek-Reasonix/pull/2402) | 已合入 | 2026-05-31 | 稳定 tool schema 排序，提升 prompt cache 复用。 | 2 个文件，+44/-3 |
| [#2314](https://github.com/esengine/DeepSeek-Reasonix/pull/2314) | 已合入 | 2026-05-30 | 增加缓存效率守卫和诊断，让 cache-first 发布更稳。 | 25 个文件，+653/-79 |
| [#2188](https://github.com/esengine/DeepSeek-Reasonix/pull/2188) | 已合入 | 2026-05-29 | 增加缓存诊断，让用户更容易理解缓存变化。 | 20 个文件，+795/-18 |
| [#2191](https://github.com/esengine/DeepSeek-Reasonix/pull/2191) | 已合入 | 2026-05-29 | 让 agent 能更准确地删除完整代码单元，减少手动改错。 | 10 个文件，+444/-11 |
| [#2134](https://github.com/esengine/DeepSeek-Reasonix/pull/2134) | 已合入 | 2026-05-29 | 增加更多主题与文案，让 CLI 和桌面端有更好的视觉选择。 | 31 个文件，+1042/-74 |
| [#2192](https://github.com/esengine/DeepSeek-Reasonix/pull/2192) | 已合入 | 2026-05-29 | 探索远程工作区支持，为后续 SSH 远程开发提供安全预览路径。 | 3 个文件，+333/-0 |
| [#2189](https://github.com/esengine/DeepSeek-Reasonix/pull/2189) | 已合入 | 2026-05-29 | 让 MCP 工具加载更稳定，帮助保持缓存表现一致。 | 2 个文件，+138/-11 |
| [#2190](https://github.com/esengine/DeepSeek-Reasonix/pull/2190) | 已合入 | 2026-05-29 | 提供更可靠的大段内容删除能力，降低长文本改动出错率。 | 11 个文件，+314/-15 |
| [#2135](https://github.com/esengine/DeepSeek-Reasonix/pull/2135) | 已合入 | 2026-05-29 | 完善 MCP 管理体验，支持导入、编辑、状态查看、重试和清晰反馈。 | 28 个文件，+2710/-121 |
| [#2144](https://github.com/esengine/DeepSeek-Reasonix/pull/2144) | 已合入 | 2026-05-29 | 让桌面端关闭窗口后仍可保留运行中的会话。 | 13 个文件，+249/-21 |

以上 upstream PR 元数据合计：31 个已合入 PR，329 个 changed-file entries，
+21294/-1240 行。

## 贡献主题

- Cache-first engineering：缓存诊断、稳定 MCP fingerprint，以及减少 token-heavy edit operations 的工具。
- Desktop experience：v2 工作区布局、图片附件、transcript 滚动、composer 可读性和 close-to-tray。
- MCP UX：启动韧性、手动连接、导入、编辑、状态、重试、reload、工具展示和多语言 UI 打磨。
- Agent ergonomics：自动计划、更清晰的审批、更安全的编辑工具、mock provider 和 typed-nil 加固。
- Product polish：补充 Reasonix 主题配色，并清理终端和桌面端的工作区选择体验。

## 分支用途

这个分支适合作为我的 fork 的默认落地页。常规开发分支可以继续贴近 upstream，而这个分支让访问者第一眼看到我已经合入官方仓库的具体贡献。

## 数据源

官方上游仓库中由 `SivanCola` 创建的 closed PR：

https://github.com/esengine/DeepSeek-Reasonix/pulls?q=is%3Apr+is%3Aclosed+author%3ASivanCola

最后刷新时间：2026-06-02。
