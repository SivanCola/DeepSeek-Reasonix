# 远程工作台 + 本机 Provider Broker

本文描述 Issue #6714 引入的 Remote Workbench 架构。

## 目标

- 通过 SSH 将完整桌面工作台投影到远端工作区。
- **Provider 配置与 API Key 始终留在 Desktop**；Host 不需要 `DEEPSEEK_API_KEY`。
- 主窗口仅一个可见 Target，但允许 **Local + 1 个 Remote** Adapter 同时后台运行。
- SSH stdio 承载双向 JSON-RPC（`rpcwire`）；不使用 HTTP Gateway，也不使用 SSH `-L/-R` Broker 隧道。

## 架构

```
Desktop UI → TargetManager → LocalAdapter | RemoteAdapter
RemoteAdapter → SSH stdio (rpcwire) → attach-workspace 代理 → 每工作区 remote-runtime
remote-runtime --broker RPC→ Desktop Provider Broker → 本机 Provider / API Key
```

## 协议

- 唯一来源：`internal/remote/protocol` 的 Go registry。
- 生成物（需提交）：
  - `internal/remote/protocol/schema.generated.json`
  - `internal/remote/protocol/schema_hash.generated.go`
  - `desktop/frontend/src/generated/remoteProtocol.generated.ts`
- 生成：`go run ./cmd/remote-protocol-gen -root .`
- 校验：`go run ./cmd/remote-protocol-gen -check -root .`
- 握手携带 Schema Hash：不一致则拒绝并可触发一次自动升级；Build/source 不同但 Schema 相同仅作诊断。

## Provider Broker

Host → Desktop：`broker/catalog`、`broker/stream/open`、`broker/stream/cancel`  
Desktop → Host：`broker/stream/chunk`、`broker/stream/end`、`broker/catalog-changed`

目录项为非敏感 Descriptor，并透传 `toolCallReasoning` / `warnOnMissingToolCallReasoning`，保证 DeepSeek 工具循环与本地一致。

## TargetManager

- Local Adapter 永久在线。
- 至多一个 Remote Adapter。
- 单一 `activeTarget`；隐藏 Target 仅 badge/Toast，不弹全局审批 Modal。
- 重启后始终进入 Local，仅提示“重新连接”上次 Remote（不自动 SSH / AskPass / 授权）。

## 明确非目标

- Host 持有 Provider 凭据  
- systemd 常驻 daemon  
- 远端子窗口、HTTP Gateway、SFTP 作为工作台数据面  
- 超出工作台 RuntimeAPI 的完整 AppBindings 对齐  
- 多个 Remote Host 同时常驻  

## SSH 传输

| 平台 | 传输 |
| --- | --- |
| Windows | 系统 `ssh.exe`、AskPass、Job Object 失败封闭进程树 |
| macOS / Linux | Go SSH；远端命令 `reasonix remote attach-workspace --stdio` |

## Attach 与 runtime 生命周期

意外断连：runtime 保留 **5 分钟** 宽限后在空闲时快照退出。显式断开在空闲时立即退出；忙碌时禁止替换 Host。

## Provider Trust

持久授权键：`HostID + fingerprint` → allowed refs。绝不写入 API Key / base URL / Header / env 名 / 密码。

## 真实验收矩阵

合并前需完成 Windows Desktop → Linux Host 矩阵（见英文版 11 条），并附提交 SHA 证据。

## 仓库共同贡献者（来源 PR）

本整合 PR 在提交级使用 `Co-authored-by`，将下列来源作者列为 GitHub 共同贡献者：

| 来源 PR | 作者 | 本仓库采用的贡献 |
| --- | --- | --- |
| #6722 | @SivanCola | 本机 Provider Broker 方向、远端无密钥、Provider 信任意图、checkpoint/mirror 方向；整合提交的 Author |
| #6725 | @taibai233 | `rpcwire`、生成式 RuntimeAPI Schema、Windows AskPass/Job Object、Target fencing 思路；对应移植提交含 `Co-authored-by` |

说明：仅 PR 正文致谢**不能**进入贡献图；有效归属依赖 commit trailer 中的公开 GitHub noreply 邮箱。

## 来源说明

| 来源 | 采用/改造 |
| --- | --- |
| PR #6722 (@SivanCola) | 本机 Broker 方向、信任模型、远端无密钥 runtime、checkpoint/mirror 方向 |
| PR #6725 (@taibai233) | `rpcwire`、生成式 Schema、RuntimeAPI registry、Windows AskPass/Job Object 方向、Target fencing |
| 不采用 | #6725 Host 凭据、systemd、Build ID 强失败、完整 71 方法 Host 实现原样落地 |
| 不保留自 #6722 | 子窗口、HTTP Gateway、SSH 端口转发 Broker、SFTP 工作台 I/O |

## 缓存影响

Broker 的 `stream/open` 携带结构化 `provider.Request` 字节；Desktop 走本机 Provider，工具顺序、reasoning replay、消息序与 compaction 与 Local 一致。Host/Target/Schema 等动态信息不得写入 system prompt 或 tool schema。
