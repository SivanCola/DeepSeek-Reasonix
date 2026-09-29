# HTTP/2 protocol compatibility / HTTP/2 协议兼容

An affected provider connection can opt into HTTP/1.1 before sending requests.
This is a compatibility workaround, not proof that a server or proxy defect
has been repaired. It does not fix idle-connection EOF failures.

受影响的模型连接可在发送请求前指定 HTTP/1.1。这是兼容性绕过，不代表服务端或
代理的故障已被修复，也不是空闲连接 EOF 问题的修复。

Close Reasonix, back up the user configuration, and add the following line
inside the affected existing `[[providers]]` entry. Do not create a duplicate
entry or change its API address, model, credentials, or proxy for this test.

退出 Reasonix，备份用户配置，在受影响的**已有** `[[providers]]` 条目内添加以下
一行。不要新增重复条目，也不要同时修改 API 地址、模型、密钥或代理。

```toml
http1_only = true
```

The user configuration is `%APPDATA%\reasonix\config.toml` on Windows or
`~/.reasonix/config.toml` on macOS/Linux; `REASONIX_HOME` overrides the directory.
Restart the patched application to load the policy. Missing or `false` keeps
automatic negotiation. The v1.39.5 application ignores this field; adding it
without installing a patched build does not enable compatibility mode.

Windows 用户配置位于 `%APPDATA%\reasonix\config.toml`，macOS/Linux 位于
`~/.reasonix/config.toml`；设置了 `REASONIX_HOME` 时以该目录为准。
重启带此补丁的应用后生效。缺省或 `false` 保持自动协商。1.39.5 会忽略此字段，
仅编辑配置而不安装修复版不会启用兼容模式。

## Verification / 用户环境验证

1. With the field absent or false, send a short, non-sensitive test prompt and
   export diagnostics. Record `httpProtocol` and `transportCode`.
2. Enable the field, restart, and manually send the same prompt on the same
   connection and network. Confirm `HTTP/1.1` and successful streaming.
3. Restore false, restart, and repeat only if an additional request is acceptable.
   Record successes and failures; one success does not establish causality.

1. 缺省或设为 `false`，发送简短无敏感信息的测试消息，导出诊断，记录
   `httpProtocol` 与 `transportCode`。
2. 启用后重启，在相同连接和网络下手动发送同样的测试消息，确认 `HTTP/1.1`
   且流式回复成功。
3. 若接受额外测试请求，可恢复 `false`、重启并复测。记录成功与失败，不能仅凭
   一次成功判定原因。每次测试均可能产生供应商费用。

Written requests are never automatically replayed by this compatibility feature:
the server may already have processed them. TLS verification, configured proxy,
SSE and request bodies remain unchanged. Chat Completions, Responses, Anthropic,
model discovery, desktop probes and credential-proxy upstreams use the policy.
Unrelated desktop settings edits retain it without a new desktop RPC field or UI.

此兼容功能不会自动重发已写出的请求，因为服务端可能已经处理。
证书校验、配置的代理、SSE 与请求正文均保持不变。Chat Completions、Responses、
Anthropic、模型发现、桌面连接测试与凭据代理上游均遵守此策略。
桌面修改其他设置时会保留该配置，不新增桌面 RPC 字段或设置界面。
