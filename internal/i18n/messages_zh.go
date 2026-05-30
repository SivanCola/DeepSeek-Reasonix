package i18n

// Chinese is the zh-Hans catalogue. Keep the %s placeholders in the same order
// as English unless a phrase genuinely demands re-ordering — call sites pass
// arguments positionally and won't reshuffle.
var Chinese = Messages{
	Subtitle:        "配置与插件驱动的 coding agent",
	WelcomeTitleFmt: "欢迎使用 %s",
	NoConfigYet:     "还没有配置 — 现在来设置一下吧。",
	StartingChatFmt: "正在启动 %s…",
	SetKeyHint:      "设置好 API key 后运行 `reasonix chat`。",
	ConfigLabel:     "配置",
	ModelsLabel:     "模型",
	ConfigNotFound:  "未找到 — 使用内置默认值",
	ConfigErrorFmt:  "%s — 错误：%v",
	NoKey:           "未设置 key",
	Ready:           "已就绪",
	GetStarted:      "开始使用",
	StepScaffold:    "生成 reasonix.toml",
	StepSetKey:      "设置 API key",
	StepSetKeyHint:  "执行 export DEEPSEEK_API_KEY=… 或写入 .env",
	StepChatDesc:    "交互式会话",
	StepRunDesc:     "执行单次任务",
	HelpFooter:      "reasonix help · 查看全部命令",

	ChatTip:           "对话上下文将跨轮保留。输入 'exit' 或按 Ctrl-D 退出。",
	TurnCancelled:     "已取消 — 回到提示符",
	NoSessionToResume: "没有可恢复的会话 — 用 `reasonix chat` 开一个新的",
	ResumeRequiresTTY: "--resume 需要交互式终端；用 --continue 直接恢复最近一次",
	PickSessionLabel:  "恢复哪个会话？",

	ChatStatusThinkingFmt:  "%s 思考中… (%d 秒 · Esc 取消)",
	ChatStatusIdle:         "Tab 切换 plan · Enter 发送 · Esc 退出当前状态 · PgUp/PgDn 滚动 · Ctrl-D 退出",
	ChatStatusIdleCompact:  "Tab=plan · Enter=发送 · Esc=取消 · Ctrl-D 退出",
	ChatStatusPlanApproval: "Enter 同意 · 输入文本反馈以让 AI 修改 · Esc 撤掉横幅 · PgUp/PgDn 滚动",
	PlanApprovalPrompt:     "计划已生成（看上方）— 回车通过，或输入修改建议",
	ChatStatusToolApproval: "1 同意一次 · 2 本会话允许 · 3 拒绝 · Ctrl-C 取消本轮",
	ToolApprovalPromptFmt:  "允许 %s%s？— [1] 本次 · [2] 本会话 · [3] 拒绝",

	SlashCompactDone:   "已压缩 — 旧的中段换成一段摘要，最近几轮保留原样",
	SlashCompactFailed: "压缩失败",
	SlashNewDone:       "已新建会话 — 之前的对话已存档",
	SlashNewFailed:     "新建会话失败",
	SlashUnavailable:   "当前构建不支持该命令",
	SlashUnknown:       "未知命令",
	SlashHelp:          "命令：/compact · /new · /branch · /tree · /switch · /resume · /mcp · /copy · /goal · /cache · /lang · /help",
	SlashPromptEmpty:   "该 MCP prompt 没有返回可发送的内容",
	SlashMCPNone:       "没有配置 MCP 服务器 — 在 reasonix.toml 加一个 [[plugins]] 条目",
	SlashMCPImportDone:   "从 cc-switch 导入了 %d 个 MCP 服务器",
	SlashMCPImportSkipped: "%d 个已跳过（reasonix.toml 中已存在）",
	SlashMCPImportEmpty:   "cc-switch 中未找到 MCP 服务器",
	CompHintSlash:      "↑/↓ 移动 · Tab/Enter 选中 · Esc 关闭",
	CompHintFile:       "↑/↓ 移动 · Tab/Enter 进入文件夹或选中文件 · Esc 关闭",

	LivePreviewLabel: "实时",

	SelectProvidersLabel:  "选择要启用的 provider",
	EnterAPIKeysHeader:    "输入 API key（回车跳过、稍后写入 .env）：",
	MissingKeyIntro:       "reasonix.toml 已配置好 — 只差一个 API key 就可以开始。",
	WroteFileFmt:          "已写入 %s",
	SetupComplete:         "设置完成。",
	SetupCancelled:        "设置已取消。",
	TryHintFmt:            "试试: %s",
	NextHint:              "下一步：设置 API key（export DEEPSEEK_API_KEY=... 或写入 .env），然后运行 `reasonix run \"你的任务\"`。",
	ConfirmReconfigureFmt: "%s 已存在。重新配置并覆盖？",
	KeepingExisting:       "保留原配置不变。",
	NotOverwritingFmt:     "%s 已存在，不覆盖",

	UnknownCommandFmt: "未知命令 %q",
	UsageRunHint:      "用法：reasonix run [--model NAME] <task>",
	ErrorPrefix:       "错误：",
	WriteConfigErr:    "写入配置失败：",
	WriteEnvErr:       "写入 .env 失败：",

	SelectOneHint:  "(↑/↓ · Enter · q 取消)",
	SelectManyHint: "(↑/↓ · Space · Enter · q)",

	SessionCostFmt: "第 %d 轮 · 本轮 %s · 累计: %s",

	CacheReportTitle:   "缓存诊断报告",
	CacheReportTurnFmt: "第 %d 轮: %.0f%% 命中 (命中=%d 未命中=%d)",
	CacheReportChurn:   "变化原因: %v",
	CacheReportStable:  "  + %d 轮缓存前缀稳定",
	CacheReportSummary: "  · %.0f%% 整体命中率 · 相比冷启动约节省 %s",
	CacheReportNoData:  "暂无缓存诊断数据",
	CacheDoctorHeader:  "缓存健康检查",

	CompactProgressTitle: "正在压缩对话…",
	CompactTips: "提示: /compact 命令也可用于主动释放上下文空间\n" +
		"提示: 使用 /branch 为会话命名，便于自由探索而不会丢失进度\n" +
		"提示: 使用 /cache-report 查看缓存命中为你节省了多少成本\n" +
		"提示: 上下文压缩可以在窗口快满时让会话持续运行\n" +
		"提示: 按 Tab 切换计划模式 — 安全探索，不会破坏缓存\n" +
		"提示: 长时间的会话可以从偶尔的 /compact 中受益，保持响应速度",

	ResumeStatusFmt:   "选择会话 [%d-%d] · Esc 取消",
	ResumeListTitle:   "已保存的会话（最近优先）:",
	ResumePickerHint:  "↑↓ 移动 · Enter 选择 · Esc 取消",
	ResumeNoDir:       "会话持久化未配置 — 在 reasonix.toml 中设置 session_dir",
	ResumeFailed:      "恢复失败",
	ResumeEmpty:       "没有找到已保存的会话",
	ResumeOutOfRange:  "请选择 %d-%d",
	ResumeSwitched:    "已切换到会话: %s",
	ResumeCancelled:   "已取消恢复",

	SlashBranchDone:   "已从当前会话创建分支: %s",
	SlashTreeTitle:    "会话分支树",
	SlashTreeDisabled: "当前模式不支持会话树",
	SlashSwitchDone:   "已切换到会话 %s",
	SlashSwitchUsage:  "/switch <id> — 使用 /tree 查看分支编号",

	SlashCopyDone:    "已复制到剪贴板",
	SlashCopyFailed:  "剪贴板复制失败: %v",
	SlashCopyNoFile:  "没有可复制的消息",
	SlashCopyWritten: "剪贴板不可用 — 已写入 %s",

	GoalStartedFmt:   "目标已设定: %s — /goal status 查看进度, /goal cancel 取消",
	GoalStatusFmt:    "目标: %s\n状态: %s\n尝试次数: %d",
	GoalCancelled:    "目标已取消。",
	GoalCompletedFmt: "目标已完成: %s",
	GoalNoGoal:       "没有活跃的目标 — 使用 /goal <描述> 来设定",

	SlashClearDone:    "上下文已清除 — 会话和历史已重置，配置已保留",
	SlashClearRunning: "运行中无法清除 — 请先按 Esc 取消当前轮次",

	SlashDoctorHeader:    "诊断报告",
	SlashDoctorKeyOK:     "  ✓ API key %s 已就绪 (%s)",
	SlashDoctorKeyMissing: "  ✗ API key %s 未设置 (%s)",
	SlashDoctorNetOK:     "  ✓ 网络: API 可达 %s",
	SlashDoctorNetFail:   "  ✗ 网络: 无法连接 %s (%v)",
	SlashDoctorConfigOK:  "  ✓ 配置: %s",
	SlashDoctorConfigMiss: "  ✗ 未找到配置文件（使用默认值）",
	SlashDoctorSummary:   "  · 总计: %d/%d 项检查通过",

	SlashConfigHeader:   "当前配置",
	SlashConfigDefaultModel: "  默认模型: %s",
	SlashConfigMaxSteps: "  最大步骤: %d",
	SlashConfigLang:     "  语言: %s",
	SlashConfigCompact:  "  压缩比例: %.0f%% (保留最近: %d)",

	SlashInitTitle:   "项目分析",
	SlashInitLangHint: "  检测到的语言: %s",
	SlashInitFrameHint: "  检测到的框架: %s",
	SlashInitFileHint: "  提示: reasonix 会读取项目根目录下的 CLAUDE.md 作为自定义指令",
	SlashInitDone:    "  分析完成 — 使用 /config 查看设置",
	SlashInitNoProject: "  当前目录未检测到项目文件",

	SlashCommandsTitle:   "自定义命令",
	SlashCommandsCreate:  "/commands create <名称> <描述> — 从上一轮对话创建新命令；通过 stdin 传入正文",
	SlashCommandsCreated: "已创建命令: %s",
	SlashCommandsDelete:  "/commands delete <名称> — 删除命令",
	SlashCommandsDeleted: "已删除命令: %s",
	SlashCommandsNotFound: "命令未找到: %s",
	SlashCommandsNoDir:   "未找到命令目录 — 创建 .reasonix/commands/ 以添加自定义命令",
	SlashCommandsEmpty:   "暂无自定义命令 — 在 .reasonix/commands/ 中添加 .md 文件",

	SlashImgNoImage: "剪贴板中没有图片 — 请先截图或复制图片 (macOS: Cmd+Ctrl+Shift+4)",
	SlashImgSaved:   "图片已保存: %s — @引用已插入输入框",
	SlashImgMCPHint: "提示: MCP vision 工具已连接 — 发送消息即可分析图片",

	SlashBtwUsage:     "/btw <消息> — 临时提问，回答会显示但不会保存到对话历史",

	SlashEffortSetFmt: "推理深度: %s",
	SlashEffortUsage:  "/effort auto|high|fast — auto: 模型自行决定, high: 最大深度, fast: 跳过推理",

	UsageBody: `reasonix — 由配置和插件驱动的 coding agent（多模型）

用法：
  reasonix chat [--model NAME]                          交互式会话（多轮）
  reasonix run  [--model NAME] [--max-steps N] <task>   执行单次任务后退出
  reasonix serve [--model NAME] [--addr HOST:PORT]      通过 HTTP+SSE 提供会话（浏览器客户端在 /）
  reasonix init [path]                                  交互式设置；生成 reasonix.toml（及 .env）
  reasonix version
  reasonix help

示例：
  reasonix chat
  reasonix run "把 main.go 里的 TODO 实现掉"
  reasonix run --model mimo-pro "给这个函数补单元测试"
  echo "解释这段代码" | reasonix run

配置：
  优先级：flag > ./reasonix.toml > ~/.config/reasonix/config.toml > 内置默认值
  密钥通过 api_key_env 从环境变量注入（如 DEEPSEEK_API_KEY）。
  运行 'reasonix init' 生成配置；详见 docs/SPEC.md。
`,
}
