# Release candidates

The protected `main-v2` control workflow prepares an immutable candidate after
reviewed Stable Notes are embedded. `Prepare release candidate` with `version`
builds the shared CLI/npm files, signs Desktop files, runs final-package native
acceptance, and seals a record. `Publish release candidate` accepts only that
record, checks its provenance and bytes, and requires one `release` approval
before creating the three tags and publishing. `recover` reuses the same sealed
files; it must not rebuild or sign them.

For qualification without publication, dispatch `Prepare release candidate` on
protected `main-v2` with `version` and `rehearsal=true`. An already reviewed
version may be used for this isolated run. It uses separate
`release-candidate-rehearsal-*` artifacts, records `purpose=rehearsal`, and
cannot pass the normal publish resolver or payload verifier. The Desktop child
accepts the existing version tag only in this non-publishing mode. The run must
still complete source CI, signing, and native acceptance. It creates no tags,
GitHub Releases, npm packages, Homebrew updates, R2 pointers, or site changes.

After sealing, run `Verify release candidate rehearsal` on `main-v2` with its
candidate ID. This independent workflow downloads the exact record and payload
artifact IDs. It checks the GitHub archive digest, protected producer run,
OIDC file attestations, sealed file digests, and native acceptance receipts.
Its 90-day report binds the producer and verifier runs without compiler or
signing credentials. The verifier proves reuse of the same signed bytes; it is
not a publication or a substitute for a later formal release's public checks.

The candidate payload lasts 30 days and its record/evidence 90 days. If the
payload expires before publication, prepare a new candidate. The release
skill's public postflight remains the authority for tags, npm, Desktop
updates, Homebrew, and the hydrated website after an authorized publication.

## Tag publisher identity / 标签发布身份

Publication requires the repository secret `RELEASE_TAG_TOKEN` and variable
`RELEASE_TAG_ACTOR` (the token owner's login). Use a maintainer already allowed
by the release-tag rulesets, with repository Contents write access. Do not copy
a workstation login token or repurpose another integration's secret. Configure
this dedicated credential before enabling publication; there is no default
`GITHUB_TOKEN` fallback. The token is available only to protected `main-v2`
identity-check and activation steps, not candidate builds.

发布前需配置仓库 secret `RELEASE_TAG_TOKEN` 和变量 `RELEASE_TAG_ACTOR`（凭据所有者登录名）。
使用标签保护规则已允许的维护者身份，并授予目标仓库 Contents 写权限。不要复制工作站登录凭据，
也不要挪用其他集成的 secret。未配置时发布预检直接失败，不回退到默认 `GITHUB_TOKEN`。
该凭据只在受保护 `main-v2` 的身份检查及标签激活步骤使用，不传给候选构建。

Before approval, the workflow reads the authenticated actor, repository push
access, and inherited rulesets. Activation repeats those checks, binds the actor
ID to preflight, and uses the same credential for one atomic three-tag push.
Unknown matching rules fail closed. This is an early policy check, not proof of
every token scope or a reservation: GitHub remains authoritative at push time.
No rule is disabled, no tag is moved, and an existing complete recovery identity
is verified without pushing. Rotate the dedicated token when required.

审批前检查真实凭据身份、仓库推送权限及继承的规则集；激活前再次检查，并与预检的用户 ID 绑定。
三个标签仍以同一身份原子推送。无法判断的匹配规则按失败处理。预检不能证明全部 token scope，
也不锁定远端状态，推送时仍以 GitHub 的实时判断为准。已有完整标签的恢复只校验，不重新推送。
保护规则不变，已发布标签不移动；按需轮换专用 token。
