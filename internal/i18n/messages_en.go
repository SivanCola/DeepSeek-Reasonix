package i18n

// English is the baseline catalogue. The drift-guard test reflects over its
// fields, so every other catalogue must populate the same set.
var English = Messages{
	Subtitle:        "config + plugin driven coding agent",
	WelcomeTitleFmt: "Welcome to %s",
	NoConfigYet:     "No configuration found yet — let's set it up.",
	StartingChatFmt: "Starting %s…",
	SetKeyHint:      "Set your API key, then run `reasonix chat`.",
	ConfigLabel:     "config",
	ModelsLabel:     "models",
	ConfigNotFound:  "not found — using built-in defaults",
	ConfigErrorFmt:  "%s — error: %v",
	NoKey:           "no key",
	Ready:           "ready",
	GetStarted:      "Get started",
	StepScaffold:    "scaffold reasonix.toml",
	StepSetKey:      "set API key",
	StepSetKeyHint:  "export DEEPSEEK_API_KEY=… or add to .env",
	StepChatDesc:    "interactive session",
	StepRunDesc:     "one-shot task",
	HelpFooter:      "reasonix help · all commands",

	ChatTip:           "Context is kept across turns. Type 'exit' or Ctrl-D to quit.",
	TurnCancelled:     "cancelled — back to prompt",
	NoSessionToResume: "no saved session to resume — start a new one with `reasonix chat`",
	ResumeRequiresTTY: "--resume needs an interactive terminal; pass --continue for the most recent session",
	PickSessionLabel:  "Resume which session?",

	ChatStatusThinkingFmt:  "%s thinking… (%ds · Esc cancels)",
	ChatStatusIdle:         "Tab toggles plan · Enter sends · Esc clears/exits state · PgUp/PgDn scrolls · Ctrl-D quits",
	ChatStatusIdleCompact:  "Tab=plan · Enter=send · Esc=cancel · Ctrl-D quit",
	ChatStatusPlanApproval: "Enter approves · type to revise · Esc dismisses · PgUp/PgDn scrolls",
	PlanApprovalPrompt:     "Plan ready above — Enter to approve, or type to revise",
	ChatStatusToolApproval: "1 approve once · 2 allow this session · 3 deny · Ctrl-C cancels turn",
	ToolApprovalPromptFmt:  "Allow %s%s? — [1] once · [2] this session · [3] no",

	SlashCompactDone:   "session compacted — older middle replaced by a summary, recent turns kept",
	SlashCompactFailed: "compaction failed",
	SlashNewDone:       "fresh session started — previous transcript saved",
	SlashNewFailed:     "could not start a new session",
	SlashUnavailable:   "command unavailable in this build",
	SlashUnknown:       "unknown command",
	SlashHelp:          "commands: /compact · /new · /branch · /tree · /switch · /resume · /mcp · /copy · /goal · /cache · /lang · /help",
	SlashPromptEmpty:   "the MCP prompt returned no content to send",
	SlashMCPNone:       "no MCP servers configured — add a [[plugins]] entry in reasonix.toml",
	SlashMCPImportDone:   "%d MCP servers imported from cc-switch",
	SlashMCPImportSkipped: "%d skipped (already present in reasonix.toml)",
	SlashMCPImportEmpty:   "no MCP servers found in cc-switch",
	CompHintSlash:      "↑/↓ move · Tab/Enter select · Esc close",
	CompHintFile:       "↑/↓ move · Tab/Enter open folder or pick file · Esc close",

	LivePreviewLabel: "Live",

	SelectProvidersLabel:  "Select providers to enable",
	EnterAPIKeysHeader:    "Enter API keys (Enter to skip and set later in .env):",
	MissingKeyIntro:       "reasonix.toml is ready — just an API key away.",
	WroteFileFmt:          "Wrote %s",
	SetupComplete:         "Setup complete.",
	SetupCancelled:        "setup cancelled.",
	TryHintFmt:            "Try: %s",
	NextHint:              "Next: set your API key (export DEEPSEEK_API_KEY=... or add to .env), then run `reasonix run \"your task\"`.",
	ConfirmReconfigureFmt: "%s already exists. Reconfigure and overwrite?",
	KeepingExisting:       "Keeping existing config.",
	NotOverwritingFmt:     "%s already exists; not overwriting",

	UnknownCommandFmt: "unknown command %q",
	UsageRunHint:      "usage: reasonix run [--model NAME] <task>",
	ErrorPrefix:       "error:",
	WriteConfigErr:    "write config:",
	WriteEnvErr:       "write .env:",

	SelectOneHint:  "(↑/↓ · Enter · q to cancel)",
	SelectManyHint: "(↑/↓ · Space · Enter · q)",

	SessionCostFmt: "Turn %d · %s this turn · session: %s total",

	CacheReportTitle:   "Cache Diagnostics Report",
	CacheReportTurnFmt: "Turn %d: %.0f%% hit (hit=%d miss=%d)",
	CacheReportChurn:   "CHURN: %v",
	CacheReportStable:  "  + %d turns with stable prefix",
	CacheReportSummary: "  · %.0f%% overall hit rate · ~%s saved vs cold start",
	CacheReportNoData:  "no cache diagnostics recorded yet",
	CacheDoctorHeader:  "Cache Health Check",

	CompactProgressTitle: "Compacting conversation…",
	CompactTips: "Tip: The /compact command is also available to free up context proactively\n" +
		"Tip: Name your sessions with /branch to experiment without losing work\n" +
		"Tip: Use /cache-report to see how much cache hits are saving you\n" +
		"Tip: Context compaction keeps your session alive when the window fills up\n" +
		"Tip: Tab toggles plan mode — safe exploration that never breaks the cache\n" +
		"Tip: Long sessions benefit from occasional /compact to keep responses fast",

	ResumeStatusFmt:   "Choose a session [%d-%d] · Esc to cancel",
	ResumeListTitle:   "Saved sessions (most recent first):",
	ResumePickerHint:  "↑↓ navigate · Enter select · Esc cancel",
	ResumeNoDir:       "session persistence is not configured — set session_dir in reasonix.toml",
	ResumeFailed:      "resume failed",
	ResumeEmpty:       "no saved sessions found",
	ResumeOutOfRange:  "pick %d-%d",
	ResumeSwitched:    "switched to session: %s",
	ResumeCancelled:   "resume cancelled",

	SlashBranchDone:   "branched from current session: %s",
	SlashTreeTitle:    "Session Tree",
	SlashTreeDisabled: "session tree is not available in this mode",
	SlashSwitchDone:   "switched to session %s",
	SlashSwitchUsage:  "/switch <id> — use /tree to see branch ids",

	SlashCopyDone:    "copied to clipboard",
	SlashCopyFailed:  "clipboard copy failed: %v",
	SlashCopyNoFile:  "nothing to copy",
	SlashCopyWritten: "clipboard unavailable — wrote to %s",

	GoalStartedFmt:   "Goal set: %s — /goal status to check, /goal cancel to abort",
	GoalStatusFmt:    "Goal: %s\nStatus: %s\nAttempts: %d",
	GoalCancelled:    "Goal cancelled.",
	GoalCompletedFmt: "Goal completed: %s",
	GoalNoGoal:       "No active goal — use /goal <description> to set one",

	SlashClearDone:    "context cleared — session and history reset, config preserved",
	SlashClearRunning: "cannot clear while a turn is running — press Esc to cancel first",

	SlashDoctorHeader:    "Doctor — diagnostics report",
	SlashDoctorKeyOK:     "  ✓ API key %s ready (%s)",
	SlashDoctorKeyMissing: "  ✗ API key %s missing (%s)",
	SlashDoctorNetOK:     "  ✓ network: API reachable at %s",
	SlashDoctorNetFail:   "  ✗ network: cannot reach %s (%v)",
	SlashDoctorConfigOK:  "  ✓ config: %s",
	SlashDoctorConfigMiss: "  ✗ no config file found (using defaults)",
	SlashDoctorSummary:   "  · summary: %d/%d checks passed",

	SlashConfigHeader:   "Current Configuration",
	SlashConfigDefaultModel: "  default model: %s",
	SlashConfigMaxSteps: "  max steps: %d",
	SlashConfigLang:     "  language: %s",
	SlashConfigCompact:  "  compact ratio: %.0f%% (recent keep: %d)",

	SlashInitTitle:   "Init — project analysis",
	SlashInitLangHint: "  detected language: %s",
	SlashInitFrameHint: "  detected framework: %s",
	SlashInitFileHint: "  hint: reasonix reads CLAUDE.md at project root for custom instructions",
	SlashInitDone:    "  analysis complete — use /config to review settings",
	SlashInitNoProject: "  no project files detected in current directory",

	SlashCommandsTitle:   "Custom Commands",
	SlashCommandsCreate:  "/commands create <name> <description> — create a new command from your last turn (non-interactive); pipe the body via stdin",
	SlashCommandsCreated: "created command: %s",
	SlashCommandsDelete:  "/commands delete <name> — remove a command",
	SlashCommandsDeleted: "deleted command: %s",
	SlashCommandsNotFound: "command not found: %s",
	SlashCommandsNoDir:   "no commands directory found — create .reasonix/commands/ to add custom commands",
	SlashCommandsEmpty:   "no custom commands loaded — add .md files in .reasonix/commands/",

	SlashImgNoImage: "no image found on clipboard — copy an image first (Cmd+Ctrl+Shift+4 on macOS)",
	SlashImgSaved:   "image saved: %s — @-reference inserted into input",
	SlashImgMCPHint: "hint: MCP vision tools are connected — send your message to have them analyze the image",

	SlashBtwUsage:     "/btw <message> — ask a one-shot question; the answer is shown but not saved to history",

	SlashEffortSetFmt: "thinking effort: %s",
	SlashEffortUsage:  "/effort auto|high|fast — auto: model decides, high: max depth, fast: no thinking",

	UsageBody: `reasonix — a config- and plugin-driven coding agent (multi-model)

Usage:
  reasonix chat [--model NAME]                          interactive session (multi-turn)
  reasonix run  [--model NAME] [--max-steps N] <task>   run one task and exit
  reasonix serve [--model NAME] [--addr HOST:PORT]      serve the session over HTTP+SSE (browser client at /)
  reasonix init [path]                                  interactive setup; writes reasonix.toml (+ .env)
  reasonix version
  reasonix help

Examples:
  reasonix chat
  reasonix run "implement the TODOs in main.go"
  reasonix run --model mimo-pro "add unit tests for this function"
  echo "explain this code" | reasonix run

Configuration:
  Resolution: flag > ./reasonix.toml > ~/.config/reasonix/config.toml > built-in defaults
  Secrets come from the environment via api_key_env (e.g. DEEPSEEK_API_KEY).
  Run 'reasonix init' to scaffold a config; see docs/SPEC.md.
`,
}
