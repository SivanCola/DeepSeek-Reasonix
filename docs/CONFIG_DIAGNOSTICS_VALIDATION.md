# Configuration diagnostics verification / 配置诊断验证

Development baseline / 开发基线: `14ed15ba5e`.

## Local results / 本地验证记录

Environment / 环境: macOS arm64, Go 1.26.5; 2026-09-29.

| Gate / 检查 | Result / 结果 |
| --- | --- |
| Root module `go test ./...` / 根模块完整测试 | Passed / 通过；最后的路径及来源归属调整另行通过配置、doctor 完整测试 |
| Desktop complete test inventory, 12 partitions / Desktop 全部测试清单及 12 个分组 | Passed; E–H rerun passed after event registration fix / 通过，修正事件注册后 E–H 完整复验通过 |
| Config, boot, Serve and Desktop focused race checks / 相关竞态检查 | Passed / 通过；含确定性迟到请求、事件及重绑测试 |
| Root and Desktop changed-code lint / 两个模块改动 lint | Passed, pinned golangci-lint 2.12.2 / 通过 |
| Frontend typecheck, related state tests, production build and bundle budgets / 前端检查 | Passed / 通过 |
| Real Chromium interactions in en, zh, zh-TW / 三种语言浏览器交互 | Passed / 通过 |
| Actual 1.38.3 and 1.39.5 config readers and writers / 真实旧版本读写 | Passed / 通过 |
| Windows config test binary cross compilation / Windows 测试交叉编译 | Passed; runtime remains unverified / 通过，仍不代表原生运行验收 |

The first root run encountered an intermittent HTTP/2 provider test failure.
That unchanged test passed ten focused repetitions and the subsequent full root
run. No provider transport fix is claimed here. Desktop's new event registration
was corrected after its inventory test caught the omission, and the host contract
suite and the full E–H partition passed again. All other eleven partitions passed;
the inventory verifier covered 3,167 tests. Retired global-warning source assertions were replaced by
the controlled binding/order tests rather than retaining an unused global store.

首次根模块测试有一次既有 HTTP/2 用例偶发失败；未修改供应商代码，该用例连续复验 10 次及之后完整根模块运行均通过。Desktop 新事件的注册遗漏已由清单测试发现并修正，协议检查及 E–H 完整分组复验通过，其余 11 个分组均通过，清单校验覆盖 3,167 个测试。旧全局告警的源码断言随旧状态删除，改由新的确定性行为测试覆盖。

## Ownership and protocol / 实现边界

- `internal/config/project_scope.go` merges existing workspace grants into held user authority **before** evaluating project declarations. Permission coverage uses the existing rule parser; exact shell commands remain exact. Project deny/ask rules and mode restrictions remain enforced.
- `diagnostic_paths.go` resolves paths relative to the actual project, expands the same process environment variables as the sandbox, resolves links and missing tails, and compares directory identity. No parent project configuration inheritance or grant-key migration is introduced.
- `Diagnostics()` is the typed source; `DiagnosticGroups()` aggregates by source/field/reason. `LoadWarnings()` is its warning/error projection. IDs change with reasons or declarations but remain stable when equivalent declarations are reordered.
- `InspectDiagnostics` is a credential-free, read-only snapshot with host, workspace, process instance, revision, status and an owned item array. Only the explicit detail endpoint includes redacted declaration values.
- Desktop resolves opaque tab IDs on the backend. Serve resolves the existing session identity route for `/config-diagnostics`; caller directory parameters are ignored. Remote support is negotiated as `config-diagnostics-v1`.
- The renderer no longer owns project warnings in application preferences. Binding/request/service-generation checks discard stale results. `config:diagnostics` events can update only an identity already established by a bound request; legacy events only invalidate a bound read. Empty snapshots clear resolved issues, and individual dismissal keys include host and project scope.
- Diagnostic exports pin the source identity before the save dialog. Serve replaces any client-supplied configuration evidence with its own bound snapshot. Old remote exports explicitly carry `unsupported` with no local configuration substitution.

共享加载层先合并可信授权再评估项目声明；结构化诊断是界面、CLI 和导出的共同来源。前端不再把项目告警存进应用偏好，旧事件仅触发按绑定重新读取。自动兼容没有写盘步骤，因此不存在需要恢复的跨文件迁移事务。

## Regression coverage / 回归覆盖

| Mechanism / 场景 | Evidence / 证据 |
| --- | --- |
| 39 legacy rules, no automatic grant or banner / 39 条旧规则不自动放行、不制造横幅 | `TestLegacyProjectDeclarationsAreNotGrantsOrLoadFailures`; real browser fixture |
| Existing grants, workspace isolation, revocation / 已有授权、目录隔离与撤销 | `TestExistingProjectGrantsCoverDeclarationsAndDoNotResurrect` |
| Slash variants and identical roots / 等价路径 | `TestEquivalentProjectRootUsesExistingUserValue` |
| Directory boundary, symlink, missing tail, filesystem case / 目录边界、链接、缺失尾部及大小写 | `TestDiagnosticPathIdentityAndBoundary` |
| Corrupt grants and configuration / 损坏配置与授权存储 | `TestCorruptProjectGrantsAreVisible`; Desktop project separation regression |
| Read preserves TOML bytes and mtime / 读取保留内容及修改时间 | configuration regressions and actual release probes |
| Source-bound CLI diagnostics / CLI 显式项目根目录 | `TestDoctorExplicitRootIsReadOnly` |
| Session-bound HTTP and authoritative export / HTTP 绑定与真实导出 | `TestConfigDiagnosticsUsesBoundWorkspace` |
| Late remote response after rebind / 远程重绑后的迟到响应 | `TestRemoteConfigDiagnosticsRejectsReboundResponse` (channel-controlled ordering) |
| Projects, empty state, service restart, host scope, dismissal / 项目、空状态、重启、主机范围、稍后 | `config-diagnostics.test.tsx` (controlled promises) |
| Old remote export / 旧远程服务导出 | `TestBrowserDiagnosticRemoteMergePreservesOldProtocolAndFixedScope` |
| English, simplified/traditional Chinese, narrow viewport / 三种语言与窄屏 | `bench/config-diagnostics.mjs`, real Chromium at 1100 and 400 px; embedded browser visual inspection |

## Cross-version verification / 跨版本验证

Run / 运行:

```sh
python3 scripts/verify-config-upgrade.py
```

The script extracts actual local release tags into temporary directories and runs their Go config reader/writer. It checks that the test really executed, uses synthetic files only, and removes its temporary directories afterwards.

脚本使用真实发行标签的读取和保存代码，不以新版本模拟旧版本。所有文件均为临时合成数据。每次新版本只读加载均检查用户配置、项目配置及授权文件的内容和修改时间。

| Version / 版本 | Read current untouched files / 读取原文件 | Save then read with new version / 保存后新版读取 | Downgrade read / 降级读取 |
| --- | --- | --- | --- |
| 1.38.3 | Passed on macOS / 通过 | Passed; new command stays ungranted / 通过，新增命令未自动放行 | Passed / 通过 |
| 1.39.5 | Passed on macOS / 通过 | Passed; new command stays ungranted / 通过，新增命令未自动放行 | Passed / 通过 |

The old writer also materializes provider declarations in this fixture. Their feature approval notices intentionally remain; this test rejects **permission-rule** banners, not all feature approval notices. New read-only loading retains unknown fields and comments; this does not promise that an old writer never changes its own output.

旧版保存还会写入供应商声明，它们对应的功能批准提示继续保留。验证区分命令权限兼容与功能批准，不通过隐藏全部告警来通过测试。

## Commands / 验证命令

```sh
go test ./...
go test -race ./internal/config ./internal/serve -run 'Test(Diagnostic|ConfigDiagnostics|LegacyProject|ExistingProjectGrants|CorruptProjectGrants|EquivalentProjectRoot)'
cd desktop
node ../scripts/desktop-windows-go-tests.mjs --verify
node ../scripts/desktop-windows-go-tests.mjs --all
go test -race . -run 'Test(ConfigDiagnostics|RemoteConfigDiagnostics|ColdSessionDiagnostics|BrowserDiagnosticRemoteMerge|BrowserDiagnosticAppend)'
cd frontend
pnpm test:typecheck
pnpm exec tsx src/__tests__/config-diagnostics.test.tsx
pnpm exec tsx src/__tests__/desktop-preferences-lifecycle.test.tsx
pnpm build
node bench/config-diagnostics.mjs
```

Set `CHROME_EXECUTABLE` to an installed Chrome executable if Playwright Chromium is not installed. The browser script discovers its ephemeral dev-server URL.

Desktop's single-process `go test ./...` hit the default 10-minute aggregate alarm while a normal test had run for only 2 seconds. The existing complete test partition is used instead; its inventory verifier proves every discovered test has one owner. This does not remove coverage or increase a failing individual test's timeout.

Desktop 单进程完整测试触及总计 10 分钟限制时，当前用例仅运行 2 秒。后续使用已有分组及完整清单校验，保持所有测试覆盖。

## Windows and Linux evidence / 平台证据边界

**Native Windows and Linux execution: not available in this macOS workspace. This is an outstanding acceptance gate.** Cross compilation is not runtime evidence.

**当前工作区没有 Windows 或 Linux 原生执行环境，原生验收尚未完成。不能用交叉编译或 macOS 字符串测试代替。**

Windows runtime tests are provided in `diagnostic_paths_windows_test.go`. On a Windows test machine:

```powershell
# Use an existing disposable test share; do not create broader access for testing.
$env:REASONIX_TEST_UNC_ROOT = '\\test-server\test-share'
go test ./internal/config -run 'Test(DiagnosticWindowsJunctionAndUNC|EquivalentProjectRootUsesExistingUserValue|DiagnosticPathIdentityAndBoundary)' -count=1 -v
```

Record Windows version, Go version, volume case behavior, junction outcome and UNC subtest outcome. An unset UNC variable skips that subtest and does **not** satisfy UNC acceptance. The junction test fails if it cannot create and verify a junction.

Linux must run `TestDiagnosticPathIdentityAndBoundary` on a native Linux filesystem. macOS has exercised actual symlinks, directory boundaries and the filesystem's case behavior; it is not evidence for Linux or Windows.
