# 模型连接缺陷修复验证

所有复现与验证使用临时 Reasonix home 和本地测试，不接触生产凭据、不请求真实模型。

| 问题 | 修复 | 回归证据 |
| --- | --- | --- |
| F1：粘贴 Key 进入聊天 | 凭据编辑器接管终端和原生剪贴板事件 | `TestAuditSetupPasteDoesNotEnterChat` |
| F2：修复覆盖并发凭据 | 验证只打开文件，不创建、截断或重写内容 | `TestAuditRepairVerificationPreservesConcurrentSave`、`TestRepairPreservesCredentialContentsAndFileIdentity` |
| F3：shell 只换 Key 不保存 | 凭据编辑纳入操作和并发前置条件，触发提交 | `TestAuditShellSetupKeyOnlyEditPersists`、`TestShellCredentialOnlyEditRejectsConcurrentRotation` |
| F4：独立编辑串 Key | 凭据草稿按连接身份保存 | `TestAuditShellSetupDistinctConnectionKeysStaySeparate` |
| F5：修复越过目录链接 | 检查 home、目标路径和对象身份，通过打开的文件句柄修改和回滚 | `TestAuditRepairRefusesLinkedHome` |
| F6：TUI 编辑被遮蔽的项目连接 | 使用配置合并器实际选中的来源 | `TestAuditSetupEditsEffectiveProviderSource`、`TestProviderEditPathRespectsProjectOverrideOfBuiltins` |
| F7：恢复错误确认提交 | 发布前记录准确候选 revision，恢复时同时匹配版本和引用 | `TestAuditRecoverDoesNotInventReceiptAfterExternalEdit` 及持久化边界测试 |
| F8：重启后回执冲突 | 持久化私有 HMAC 密钥，锁内再次查重 | `TestAuditDurableReceiptAcrossProcessRestart`，实际启动子进程 |
| F9：偏好编辑无法恢复回执 | 空槽位集合合法；回执查询恢复有证据的发布 | `TestAuditRecoverCommittedPreferenceReceipt`、`TestReceiptQueryRecoversPublishedPreferenceBeforeMark` |
| F10：清理失败丢证据 | 删除失败或配置变化时保留事务记录 | `TestAuditCleanupRetainsEvidenceOnFailedRemoval` |
| F11：辅助请求反复认证失败 | 标题等辅助入口记录认证拒绝；403 按模型、401 按连接隔离 | `TestAuditTitleAuthenticationRejectionStopsFurtherRequests`、`TestAuxiliaryAuthenticationFailureIsScopedToConnectionAndModel` |
| F12：前端重试绕过就绪检查 | 移除本地发送授权，串行重试后刷新后端权威 tab 状态 | `composer-authentication-recovery.test.tsx`、`TestRetryAuthenticationPublishesAuthoritativeTabRefresh` |

浏览器夹具：`desktop/frontend/bench/authentication-recovery.html`。使用真实 Composer 和
临时宿主，已验证迟到重试、切 tab、controller 未就绪、替换连接、草稿保留及后端 Ready
恢复发送。整个验证过程模型提交数为零。

兼容性：TOML、`.env` 和 RPC 字段名不变。事务仍使用 schema 1，利用现有 revision 字段
增加 `config_prepared` 阶段。旧记录没有发布 revision 且存在引用时保持结果未知，不伪造
成功。旧 Desktop 摘要无法验证重放内容时返回 `unknown_result`。旧应用仍可读取保存后的
连接配置和凭据。

Windows 文件句柄修复已进行本地交叉编译；原生 ACL、共享占用和 reparse point 行为仍需
Windows CI 验证，交叉编译不能替代这些证据。

本地验证（2026-09-17）：根目录和 Desktop 独立模块 `go test ./...`、共享核心与 Desktop 针对性 race
测试、共享包 `go vet`、前端生产构建与测试 typecheck、Composer 和 app-lifecycle
测试及上述浏览器场景均通过。真实 Electron smoke 通过服务握手、renderer RPC、
原生窗口查询、网页视图、服务子进程归属和干净退出检查。smoke 使用标准二进制名称，
因为进程断言按可执行文件名中的 `reasonix-desktop` 匹配。
