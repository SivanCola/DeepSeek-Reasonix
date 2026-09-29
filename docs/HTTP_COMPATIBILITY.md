# Model connection compatibility / 模型连接兼容模式

Model connections negotiate HTTP automatically by default. When a server or
network path fails with an HTTP/2 protocol error, open the connection's
**Compatibility settings → Connection protocol → HTTP/1.1 compatibility mode**.
Save the change, then explicitly send the message again. The local chat error
also offers an **Enable HTTP/1.1 compatibility mode** shortcut for an identified
provider connection. It saves only that connection's transport choice.

模型连接默认自动协商 HTTP 协议。遇到 HTTP/2 协议错误时，可在该连接的
**兼容设置 → 连接协议 → HTTP/1.1 兼容模式**中修改并保存，然后手动重新发送消息。
本地聊天的结构化协议错误也会显示**启用 HTTP/1.1 兼容模式**入口，只修改错误中
明确标识的连接。远程错误的连接身份可能属于远端，因此不直接修改同名本地连接；
请在实际提供凭据的连接设置中选择兼容模式。

The same setting controls Chat Completions, Responses, Anthropic Messages,
connection probes and model discovery. Desktop credential-proxy routes honor
the source connection's choice. Other connections keep their own settings.
SSE streaming, model selection and serialized request bodies are unchanged.
The setting does not disable TLS verification or bypass the configured proxy.
An invalid proxy configuration fails explicitly instead of falling back to a
direct/default client. Credential-proxy upstreams use the same configured
network policy in both automatic and HTTP/1.1 modes.

此设置统一覆盖 Chat Completions、Responses、Anthropic Messages、连接测试与模型列表
获取。Desktop 凭据代理向上游转发时也遵守源连接的设置。其他连接保持原有设置；
SSE 流式输出、模型选择与请求正文不变，不关闭证书校验，也不绕过配置的代理。
代理配置无效时会明确报错，不会退回直连或默认客户端。凭据代理的上游请求在自动
模式和 HTTP/1.1 模式下遵守相同的网络设置。

Saving never resends the failed request: a request written to the network may
already have been processed or billed. Existing model-setting application gates
install the new client before the next turn; busy runtimes can defer application.
Saving is not a claim that the remote fault has been repaired. Restore
**Automatic** to allow HTTP/2 negotiation again.

保存不会重发失败请求，因为已经写出的请求可能已被服务端处理或计费。
现有模型设置生效机制会在下一轮之前安装新客户端，运行中的任务可延后应用。
保存成功不代表远端故障已修复。选择**自动协商**可恢复 HTTP/2 协商。

For CLI or configuration-file use, add this field to the affected provider in
the user configuration; omitted or `false` retains automatic negotiation:

CLI 或配置文件用户可在用户配置中对应的 provider 条目添加以下字段；缺省或
`false` 保持自动协商：

```toml
[[providers]]
name = "my-connection"
kind = "openai"
base_url = "https://provider.example/v1"
model = "my-model"
http1_only = true
```

Diagnostics record the actual negotiated `httpProtocol` and, for ordinary
owned HTTP transports, `httpMode` (`auto` or `http1`). Missing fields in older
records remain unknown. Existing configurations read as automatic. Previous
readers ignore the added field; the previous model-settings delta writer keeps
unknown fields when editing other settings. Older clients cannot enforce the
new option. No session storage migration is required.

诊断记录实际协商的 `httpProtocol`，普通自有 HTTP 客户端还记录 `httpMode`
（`auto` 或 `http1`）。旧记录缺字段仍表示未知。旧配置按自动模式读取；旧版读取器
忽略新增字段，旧版模型设置增量写入器在修改其他设置时保留未知字段，但旧客户端
不能执行此协议选项。不需要会话存储迁移。
