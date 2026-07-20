# 远程桌面（SSH 原生内核）

Reasonix 远程工作区会打开**完整原生桌面窗口**。聊天、命令、工具、Skills、MCP、
文件与 Git 在远端执行；模型配置与 API Key 留在本机，经 Provider Broker 反向隧道调用。

本文对应 [Issue #6714](https://github.com/esengine/DeepSeek-Reasonix/issues/6714)
的用户向说明。

## 变化对照

| 之前 | 之后 |
| --- | --- |
| 桌面打开远端 `reasonix serve` 网页 | 打开完整 Reasonix UI，绑定 `remote-runtime` |
| 远端需要类本机的 API Key | 远端不读 Key；Broker 仅在本机 |
| 会话权威与本机状态混杂 | 远端 Controller 唯一写入；本机镜像只读 |

独立使用的 `reasonix serve` 仍保留。它**不再**作为桌面 SSH UI 路径，也没有
“打开旧 Serve 网页”的回退。

## 架构（简）

1. 本机完成 SSH 认证（配置与凭据见 Remote SSH 模块）。
2. 校验 Host Key 指纹与 Provider 信任记录。
3. 安装/校验匹配版本的远端 Reasonix。
4. 远端 loopback 启动 `reasonix remote-runtime`（`/remote/v1`）。
   状态目录为 `~/.reasonix/remote-runtime/`（与 serve 分离）。
5. 本机 Provider Broker 签发 capability token（绑定主机+工作区+已授权 Provider）；
   SSH 反向转发（`-R`）仅监听远端 `127.0.0.1`。新增 Provider 首次使用会弹出授权确认。
6. 本机 Remote Gateway 提供子窗口 loopback RPC；单次 mode-0600 ticket 传递令牌
   （不出现在 argv/URL/DOM）。
7. 子窗口加载完整桌面前端；Remote AppBridge 将 submit/cancel/approve/answer/
   compact/rewind/model、`ListTabs` 以及远端文件/Git 经 Gateway 转发。
   远程窗口隐藏 Bot、Heartbeat 与更新入口。
8. Ready 后消息与工具在远端 Controller 执行；状态栏显示 `SSH:主机/工作区`，
   避免误判执行位置。

### 自动化验收（CI / 本地）

| 检查 | 命令 |
| --- | --- |
| Gateway + runtime + FS/Git 代理 E2E | `go test ./internal/remote/gateway -run E2E` |
| SSH `-L` / `-R` 转发 | `go test ./internal/remote -run TestSSHLocalAndReverseForwardsE2E` |
| Broker 请求缓存等价 | `go test ./internal/remote/broker -run TestBrokerPreservesProviderRequestBytes` |
| 前端 AppBridge 分发 | `pnpm --dir desktop/frontend test:remote` |

Windows→Linux 真机验收仍是发版清单项（远端无 Key、多轮工具、断线重连、Host Key 变更）。

缓存稳定性：同一 `provider.Request` 在本机直连与 Broker 模式下必须生成相同的
Provider 请求体（`TestBrokerPreservesProviderRequestBytes`，已纳入
`scripts/cache-guard.sh`）。

## 安全边界

- API Key 仅存在于本机凭据存储与本机 Provider 进程内存。
- 不会向远端同步完整 `config.toml`、`.env`、代理地址、Hooks 或 OS 专属工具路径。
- Host Key 指纹变化会阻止 Broker，直至用户重新授权。
- 新增 Provider 不会自动继承旧授权。
- 连接关闭或本机退出时撤销 token。
- 日志只记录 request id、耗时、Provider ref 与脱敏错误码。

## 会话镜像

本机镜像目录：

```text
<Reasonix home>/remote-mirrors/<host-fingerprint-hash>/<workspace-hash>/
```

仅用于离线查看与灾难恢复。revision/digest 检查点防止静默分叉；恢复始终创建
**新的**远端 session id。

## CLI

```bash
reasonix remote-runtime \
  --workspace /path/on/remote \
  --addr 127.0.0.1:0 \
  --token-file /path/to/token \
  --port-file /path/to/port \
  --broker-url http://127.0.0.1:PORT \
  --broker-token-file /path/to/broker-token
```

`reasonix serve` 的独立浏览器用法不变。

## 升级说明

- 桌面 SSH「打开工作区」不再打开 Serve 网页。
- 远端需包含 `remote-runtime` 与协议主版本 1；不兼容时升级远端二进制并重试，
  不会退回网页模式。
- 旧标签页与桌面状态 JSON 仍可读；缺少 `executionTarget` 视为本机。

## 相关文档

- English: [REMOTE_DESKTOP.md](./REMOTE_DESKTOP.md)
- 配置路径: [CONFIG_PATHS.zh-CN.md](./CONFIG_PATHS.zh-CN.md)
- CLI: [CLI.zh-CN.md](./CLI.zh-CN.md)
