# Settings UI Guidelines / 设置页 UI 规范

This document defines the desktop settings UI rules for `desktop/src/ui/settings.tsx` and the scoped styles in `desktop/src/styles.css`.

本文档约定桌面端设置页的 UI 规则，适用于 `desktop/src/ui/settings.tsx` 以及 `desktop/src/styles.css` 中的 settings scoped 样式。

## Scope / 范围

- Keep the existing modal size, left navigation, and single-column scroll flow.
- Do not change backend settings schema, storage semantics, or API contracts for visual cleanup.
- Prefer settings-specific primitives before adding one-off flex wrappers or inline styles.

- 保留现有弹窗尺寸、左侧导航和单栏滚动结构。
- 视觉整理不应修改后端配置 schema、存储语义或 API 契约。
- 新增设置项时优先使用 settings 专用 primitives，避免一次性 inline flex/style。

## Row Anatomy / 行结构

Each normal settings row has two sides:

- Left: label and optional help text.
- Right: a control group containing inputs, segmented controls, buttons, and badges.

每个常规设置行分为两侧：

- 左侧：设置名称和可选帮助说明。
- 右侧：控件组，包含 input、select、segmented control、button、badge 等。

Implementation rules:

- Use `SettingsSection` for section ownership.
- Use `SettingsRow` for label/help/control alignment.
- Use `SettingsControlGroup` only when a control needs an inner field layout, such as numeric input plus unit suffix.
- Desktop controls should align right; mobile/narrow widths stack with controls aligned left.
- Settings rows must never create horizontal scrolling. Right-side controls use `width: 100%` plus `max-width`, not viewport-relative widths.
- Avoid inline `display: flex` in settings rows unless a component is not settings-owned.

实现规则：

- 用 `SettingsSection` 表达区块归属。
- 用 `SettingsRow` 统一 label/help/control 对齐。
- 仅在控件内部需要组合布局时使用 `SettingsControlGroup`，例如数值输入 + 单位后缀。
- 桌面端控件靠右对齐；窄屏时上下堆叠并靠左。
- 设置行不得制造横向滚动。右侧控件使用 `width: 100%` 和 `max-width`，不要使用 viewport-relative 宽度。
- settings 行内不要新增 inline `display: flex`，除非该组件不归 settings 所有。

## Button Taxonomy / 按钮分类

Buttons are actions, not state labels.

- `primary`: commit or save the current draft.
- `secondary` / default `.btn`: neutral actions such as configure, test, change, import, back.
- `ghost`: low-emphasis navigation or formatting utilities.
- `danger`: destructive or disconnect actions.
- Disabled: unavailable actions; do not use disabled buttons as the only status indicator.

按钮表示操作，不表示状态。

- `primary`：提交或保存当前草稿。
- `secondary` / 默认 `.btn`：中性操作，例如配置、测试、更换、导入、返回。
- `ghost`：低强调的导航或格式化操作。
- `danger`：破坏性操作或断开连接。
- disabled：不可用操作；不要把 disabled button 当作唯一状态展示。

## Status Badges / 状态徽标

Connection or installation state must use `SettingsStatusBadge`, not a button.

- `neutral`: not tested, not configured, pending.
- `success`: connected, installed, ready.
- `info`: connecting, testing.
- `warning`: needs update or attention.
- `danger`: failed, unsupported, conflicting ownership.

连接或安装状态必须使用 `SettingsStatusBadge`，不要伪装成按钮。

- `neutral`：未测试、未配置、等待中。
- `success`：已连接、已安装、可用。
- `info`：连接中、测试中。
- `warning`：需要更新或关注。
- `danger`：失败、不支持、存在冲突。

## Controls / 控件

- Inputs and selects use `.field`; height, radius, border, and focus state are owned by `.settings .field`.
- Segmented controls use `.seg-ctrl`; selected state is `data-on="true"`.
- Numeric special values should be explicit. Use empty input + placeholder + unit suffix for `budgetUsd` and `contextTokens`; do not make `auto` or `unlimited` look like real numeric values.
- Theme mode and theme style are one setting. Show the light/dark segmented control first, then only the 4 `.style-card` options that belong to the selected mode. Preserve all 8 styles across both modes.
- Credential/API rows use `.credential-row-control`: input and commit actions on the first line, connection status/test/help on the second line. Status stays a badge.
- Empty MCP, Skills, Memory, and Rules states use `SettingsEmptyState`.

- 输入框和选择器使用 `.field`；高度、圆角、边框、focus 由 `.settings .field` 管理。
- 分段控件使用 `.seg-ctrl`；选中态统一为 `data-on="true"`。
- 特殊数值必须清晰表达。`budgetUsd` 和 `contextTokens` 使用空输入 + placeholder + 单位后缀，不要让 `auto` 或 `不限` 看起来像普通数字。
- 主题模式和主题风格归为一个设置项。先展示 light/dark segmented control，再只展示当前模式下的 4 个 `.style-card`；两个模式合计仍保留全部 8 个主题。
- 凭据/API 行使用 `.credential-row-control`：第一行放输入框和保存/清除操作，第二行放连接状态、测试按钮和说明；状态始终使用 badge。
- MCP、Skills、Memory、Rules 的空状态使用 `SettingsEmptyState`。

## Page Ownership / 页面归属

- General owns appearance, workspace, behavior, web search engine, and desktop-level integrations.
- Models owns model id, model-specific context-window override, and reasoning effort.
- Rules owns approval and application mode rules.
- Connection status belongs to the integration/API section that owns the connection.

- General 负责外观、工作区、行为、搜索引擎和桌面级集成。
- Models 负责模型 ID、模型专属上下文窗口覆盖和 reasoning effort。
- Rules 负责审批和应用模式规则。
- 连接状态归属于对应 integration/API 区块。

## Checklist / 修改前检查清单

Before adding or changing a settings item:

- Is the item in the right page and section?
- Is state shown as a badge rather than a button?
- Are primary, default, ghost, danger, and disabled states semantically correct?
- Does the row use `SettingsRow` and a scoped class instead of inline layout?
- Does long Chinese, English, German, Japanese, or Russian copy fit without overlapping?
- Does the row work at desktop width and narrow width?
- Does the setting cover light theme, dark theme, and all theme styles?
- Are component tests or CSS invariant tests updated when behavior or layout rules change?

新增或修改设置项前请确认：

- 设置项是否位于正确页面和区块？
- 状态是否用 badge 展示，而不是按钮？
- primary、默认、ghost、danger、disabled 的语义是否正确？
- 行布局是否使用 `SettingsRow` 和 scoped class，而不是 inline layout？
- 中英德日俄等较长文案是否不会挤压或重叠？
- 桌面宽度和窄屏宽度是否都可用？
- light theme、dark theme 和所有 theme styles 是否覆盖？
- 行为或布局规则变化时，是否同步更新组件测试或 CSS invariant 测试？
