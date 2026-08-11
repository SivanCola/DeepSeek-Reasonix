# 将 Reasonix 嵌入 DeepSeek Harness Web GUI

English | [中文](HARNESS_EMBED.zh-CN.md)

Reasonix 的 `serve` 模式通过 HTTP/SSE 暴露一个浏览器 Web UI。与其用 Electron
封装，不如通过 harness 的**客户端插件**（`dshClient`，platform `web`）把该 UI
直接嵌入 DeepSeek Harness Web GUI：插件在页内 iframe 面板中渲染 serve UI。

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│  DeepSeek Harness Web GUI (127.0.0.1:3080)                  │
│    会话头部 [✦]  ← dsh-client-ui-reasonix 插件              │
│    ┌───────────────────────────────────────────────────┐    │
│    │  面板（portal 浮层）                              │    │
│    │  <iframe src="http://127.0.0.1:8787"> ──────┐     │    │
│    └──────────────────────────────────────────────┼─────┘    │
└───────────────────────────────────────────────────┼─────────┘
                                                     │ 回环
┌───────────────────────────────────────────────────▼─────────┐
│  reasonix serve  (127.0.0.1:8787，独立 Go 进程)             │
│  / (Web UI) · /events (SSE) · /submit /cancel /approve …    │
└─────────────────────────────────────────────────────────────┘
```

- 插件只是另一个本地进程的纯视口：harness 内核完全不接触 Reasonix 流量，
  Reasonix 会话由 serve 进程驱动，与浏览器标签页行为一致。
- 两端都是回环地址（`127.0.0.1`），不涉及 CORS、TLS 或跨源策略。

## 组成部分

| 组件 | 位置 | 作用 |
| --- | --- | --- |
| `dsh-client-ui-reasonix` | DSH 仓库 `packages/client/ui-reasonix/` | harness 客户端插件：会话头部操作 + iframe 面板 |
| `reasonix serve` | Reasonix 仓库 `cmd/reasonix` | HTTP/SSE 服务 + Web UI（默认 `127.0.0.1:8787`） |

插件已注册进 harness Web 组合（`packages/bundle/web-app/cordis.patch.yml`、
`tsconfig.client.json`、`tsconfig.base.json` 路径映射、
`packages/bundle/web-app/package.json` 依赖）。

## 运行方式

1. 启动 Reasonix 服务（默认 `127.0.0.1:8787`，无鉴权）：

   ```sh
   go build -o /tmp/reasonix ./cmd/reasonix
   /tmp/reasonix serve
   ```

   如需 token 保护：`/tmp/reasonix serve --auth token`，先打开一次打印的
   URL 确认 token。

2. 以客户端插件 HMR 模式启动 harness GUI，并在同一 checkout 运行插件
   监听构建：

   ```sh
   dsh web --dev          # GUI，启用客户端插件 HMR
   pnpm run dev:web       # 插件源码变更时自动重建 bundle（同一 checkout）
   ```

   不加 `--dev` 时插件在重启 + 刷新页面后同样会加载；HMR 只是省去改插件后
   的刷新。

3. 在 GUI 中打开一个会话，点击会话头部的 ✦ 按钮，Reasonix 面板即滑出。

## 自定义端点

面板默认指向 `http://127.0.0.1:8787`。如需指向其它地址（例如另一端口上带
token 的实例），可在浏览器中设置：

```js
localStorage.setItem('reasonix.embed.url', 'http://127.0.0.1:8788')
```

面板底部会探测 serve 的 logo 资源并显示绿/红连接点；离线时提示先运行
`reasonix serve`。

## 为什么不用 Electron？

对于"在 DeepSeek Harness 中使用 Reasonix 的 Web UI"这个目标，嵌入严格更
简单：无需第二个应用的打包、签名与分发；UI 跑在同一个浏览器会话里；
`reasonix serve` 本身已提供传输与鉴权。只有当需要一个脱离浏览器、带原生
壳能力（Dock、托盘、文件关联）的独立桌面应用时，Electron 才有意义——而那
正是本仓库 Wails 桌面壳的职责。

## 安全说明

- 内嵌 iframe 有意不加 `sandbox`：来源为回环地址且是用户可信内容。不要将
  插件指向远程或不可信来源。
- `reasonix serve` 默认绑定回环且默认无鉴权；机器共享时请使用
  `--auth token`。
