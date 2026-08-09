# 计费、展示币种与费用报价

<a href="../README.zh-CN.md">README</a>
&nbsp;·&nbsp;
<a href="./BILLING.md">English</a>
&nbsp;·&nbsp;
<a href="./CLI.zh-CN.md">CLI</a>

Reasonix 把三个概念彻底拆开，禁止混加：

1. **供应商原币价表**（`billing_currency` + 价表）— 账单原币事实
2. **发生时估值**（`CostQuote.valuations`）— 调用完成时记录的 CNY/USD 视图
3. **钱包余额** — 接口返回的多币种额度，**只用汇率**换算，不用模型区域价表

## 配置

```toml
[billing]
display_currency = "auto"   # auto | CNY | USD — 仅影响展示

[[providers]]
name = "deepseek-flash"
billing_currency = "USD"    # 冻结的原币价表币种
billing_mode = "payg"       # payg | subscription_equivalent
# price / prices：用户自定义价始终优先，不被官方目录覆盖
```

- 旧 `[desktop].currency` 仍可读，并迁移到 `[billing].display_currency`。
- 切换展示币种**绝不会**改写官方或自定义价表。
- `auto` 在 Go 后端解析：配置语言 → 主机区域 → USD。浏览器 locale 不再单独改变 Serve/Desktop 价表。

## CostQuote 协议字段

Desktop、Serve SSE、CLI JSON、ACP 状态、Remote 等主机面在 usage 事件上携带
`usage.costQuote`：

| 字段 | 含义 |
| --- | --- |
| `original` | 供应商原币金额 |
| `valuations.CNY` / `valuations.USD` | 发生时估值 |
| `valuations.*.basis` | `identity` \| `official_table` \| `fx` |
| `selected` | 当前展示偏好下的取值 |
| `estimated` | 公开价表与 ECB 参考率均为估算 |
| `complete` | 无法形成统一展示总额时为 false |
| `billingMode` | `payg` 或 `subscription_equivalent`（按量等效估算） |

旧别名 `cost` / `costUsd` / `total_cost` / `sessionCostUsd` 仅镜像 `selected`，
除非 `currency`/`currencyCode` 标明，否则不要当成美元。

非原币展示估值优先级：

1. **official_table** — 同模型对端区域公开价表（DeepSeek、LongCat）
2. **fx** — ECB 欧元参考交叉汇率（日缓存；模型请求永不等待网络）
3. incomplete — 保留原币，绝不伪造统一总额

## 诊断

```sh
reasonix doctor billing
reasonix doctor billing --json
```

## 钱包

DeepSeek 类余额接口返回多个 `balance_infos`，但不提供「当前计费币种」。Reasonix
禁止用余额数值大小推断地区。状态栏在可换算时显示 `≈目标币种`，详情列出各原币
钱包、转换值、汇率日期与 stale。

## 相关问题

本模型取代仅覆盖 Desktop 局部症状的 PR #7790，并对应 #4565、#3527、#4546 的
跨币种账本正确性需求。
