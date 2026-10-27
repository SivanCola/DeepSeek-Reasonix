---
owner: @SivanCola
backup: @esengine
status: active
reviewed: 2026-10-27
---

# Model connection checks / 模型连接检测

Opening Studio's chat model picker checks each saved connection in the background.
Rows show connection/authentication failures, payment or quota refusals, temporary
rate limits, and insufficient balance reported by a configured wallet endpoint.

Checks keep every model and the current selection; use **Manage models and
connections** to repair a connection or run an explicit model trial.

The current selection stays above the other models. Each row names its account
and protocol and shows one compact status. Hovering the row or reading its
accessible description reveals the full diagnostic. Provider filters include
the current model's account even when it has no other models.

The automatic check reads the model listing and configured wallet only. It sends
no completion requests. A reachable listing does not verify that a particular
model can generate; an unsupported, unreadable, or unconfigured wallet stays
unknown.

Resolver-backed and extension catalogs are not checked against local
credentials. Servers without provider-edit permission do not run these probes.

Checks run up to three connections at a time in the picker and share an in-flight
request within one runtime. Results are cached for one minute; reopening the
picker refreshes expired results. Changing credentials, connection settings, or
proxy configuration invalidates the cache. Results are kept in memory only.

打开 Studio 聊天输入框的模型列表，会在后台检查已保存的连接。模型行会提示
连接或鉴权失败、付费要求、配额耗尽、临时限流，以及余额接口报告的余额不足。
检测保留所有模型与当前选择；可通过「管理模型与连接」修复配置或主动试调用模型。

当前选择独立置顶。每行显示所属服务商、协议和一个简短状态，悬停或读屏说明提供
完整诊断。当前模型的服务商始终保留在筛选项中，即使该服务商没有其他模型。

自动检测只读取模型列表和已配置的余额接口，不发送生成请求。列表连通不等于
每个模型都能调用；不支持、未配置或无法读取的余额保持未知。远程解析器和扩展
模型不会拿本机凭据进行检测；没有服务商编辑权限的服务端也不会执行这些探测。

菜单最多同时检查三个连接，同一运行时复用正在进行的检测。结果缓存一分钟，
过期后重新打开菜单会再次检查；修改密钥、连接或代理配置会使旧缓存失效。
检测结果只保留在内存中，不写入配置，也不自动切换模型。

## Compatibility / 兼容性

| Contract / 合约 | Older client / 旧客户端 | New client / 新客户端 |
| --- | --- | --- |
| `POST /providers/health` | Does not call it / 不调用 | Missing route or rejected request shows unknown / 路由缺失或请求被拒时显示未知 |
| Saved configuration / 持久化配置 | Unchanged / 不变 | No new persisted fields / 无新增持久化字段 |
