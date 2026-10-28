后台任务的用量保留启动时的 Goal 归属。

## 修复

- 旧聊天或旧 Goal 的后台子任务，不再把延迟到达的用量计入新 Goal；嵌套委派继承原归属。关联 #14025。
- 同一 Goal 生命周期内的轮间用量仍累计，保留会话统计与标题排除规则，不改变任务预算或权限。关联 #14025。

---

<!-- lang:en -->

## Fixed

- Background children from an older chat or Goal no longer charge late usage to a new Goal. Nested delegation retains the original accounting owner. Refs #14025.
- Between-turn usage still accumulates within the same Goal lifecycle, preserving session accounting and title exclusion without changing task budgets or permissions. Refs #14025.
