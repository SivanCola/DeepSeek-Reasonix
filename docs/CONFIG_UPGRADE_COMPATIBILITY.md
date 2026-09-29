# Configuration upgrade compatibility / 配置升级兼容

## 升级说明

升级时无需修改 TOML、清空历史规则、运行修复命令或逐个项目确认。

- 旧项目声明已兼容展示，未获授权的操作将在需要时按当前审批模式处理。
- 全局配置及已有项目授权继续按原来的作用范围生效。等价路径和已被授权覆盖的重复声明不再产生配置告警。
- 未获授权的旧命令规则、额外写入目录保留在原文件中，汇总显示在现有“权限”或“沙箱”设置页的“当前项目配置兼容情况”。它们不会自动授予权限。
- 实际操作继续使用“仅本次”或“本会话”审批；本次更新不恢复永久授权按钮。已有持久授权仍可读取，删除后不会被旧项目声明重新创建。
- 配置损坏、授权文件读取失败及功能等待批准仍明确展示。打开配置会定位到问题的实际来源。重新加载只刷新检查结果，不新增权限。
- 诊断随当前主机和项目切换。旧远程服务未提供诊断能力时会明确显示不可用，不使用本机配置替代。

兼容处理发生在加载阶段，不改写用户配置、未知字段、注释、模型角色或供应商设置。不新增迁移记录，也不改变 `project-grants.json` 的格式和工作目录键。

如需向维护者提供诊断，可使用桌面的诊断导出，或：

```sh
reasonix doctor --root /path/to/project --json
```

新增 `configDiagnostics` 包含问题范围、来源、原因及数量，默认不包含完整历史命令或凭据。界面展开规则详情时才按需读取脱敏内容。旧规则存在不代表配置损坏；`doctor repair` 判定配置有效与“仍有按需授权声明”可以同时成立。

旧版再次保存项目配置后，新版会重新评估声明。旧版保存时额外写入的供应商、程序等功能声明仍遵循各自已有的批准流程。

## Upgrade notes

No TOML editing, rule cleanup, repair command, or per-project confirmation is required during upgrade.

- Legacy project declarations now have a compatible presentation. Operations without existing authorization follow the current approval policy when needed.
- Global settings and existing project grants retain their original scope. Equivalent paths and declarations already covered by those grants no longer produce configuration warnings.
- Unapproved legacy command rules and external write directories remain in the original file. The existing Permissions and Sandbox settings pages summarize them under **Current project configuration**. These declarations do not grant access.
- Operations continue to use once or session approval. This update does not restore permanent approval. Existing persistent grants remain readable; removing a grant does not let the old declaration recreate it.
- Broken configuration, unreadable grant storage, and features awaiting approval remain visible. **Open config** locates the actual source. **Reload** refreshes diagnostics without granting permissions.
- Diagnostics follow the selected host and project. An older remote service without diagnostic support is shown as unsupported; local configuration never substitutes for remote evidence.

Compatibility is applied while loading. It does not rewrite configuration, comments, unknown fields, model roles, or provider settings. It introduces no migration ledger and does not change the `project-grants.json` format or workspace key algorithm.

Desktop diagnostic exports and `reasonix doctor --root /path/to/project --json` include `configDiagnostics`: scope, source, reason, and counts. Full historical commands and credentials are omitted by default. Expanding details requests redacted values explicitly. Valid configuration may still contain declarations requiring approval, even when `doctor repair` reports no damage.

The new reader re-evaluates declarations after an older version saves the file. Provider or program declarations added by an old writer retain their existing feature approval requirements.

See [implementation and verification](CONFIG_DIAGNOSTICS_VALIDATION.md).
