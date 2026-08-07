// Package corpus is the single source of truth for the labeled turn corpus that
// pins host-side intent behavior. It was extracted verbatim from the three
// golden tests that previously each carried their own inline table:
//
//	internal/taskintent/classification_test.go  (evidence + mutation labels)
//	internal/taskintent/heuristic_test.go       (task-vs-chat label)
//	internal/taskintent/goal_budget_test.go     (Goal write-budget + mutation)
//
// Those tests now read from here, so the gate tests and the classifier
// evaluation harness judge one corpus. Keeping a single copy matters for the
// same reason internal/intent exists: the defect being removed is five drifting
// keyword tables, and duplicating the corpus that judges them would reproduce
// the defect one level up.
//
// It is a separate package, not a file in internal/intent, so that production
// code depending on the intent contract does not link ~120 test strings.
//
// Labels are pointers so a case can carry only the labels its source test
// asserted. A nil label means "this source made no claim", not "false".
package corpus

// Case is one labeled user turn.
type Case struct {
	Name string
	Text string
	// Origin records which golden test asserted these labels.
	Origin string

	NeedsEvidence    *bool
	NeedsMutation    *bool
	IsTask           *bool
	NeedsWriteBudget *bool
}

func b(v bool) *bool { return &v }

// Delivery carry evidence/mutation labels from the delivery classification
// matrix. They protect the boundary between advisory troubleshooting, observable
// read-only work, and mutation delivery.
var Delivery = []Case{
	{Name: "make sense", Text: "why does this make sense?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "node prose", Text: "why does the node selection matter?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "swift prose", Text: "why does Swift concurrency work this way?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "go prose", Text: "why does this go wrong so often?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "remote url", Text: "why can't I open https://github.com?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "remote analysis", Text: "can you analyze why Outlook won't sync?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "how to install", Text: "how do I install the plugin?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "chinese how to install", Text: "如何安装插件", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "chinese how to modify", Text: "怎么修改 WPS 设置", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "python prose", Text: "why is Python popular?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "docker prose", Text: "why is Docker popular?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "pytest prose", Text: "why is pytest popular?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "backtick python prose", Text: "why is `Python` popular?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "backtick code identifier", Text: "what does `context.Context` mean?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "double dash prose", Text: "why is this -- strangely -- happening?", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "advisory upgrade", Text: "为什么升级插件后失败", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "advisory adjustment", Text: "为什么调整设置后没有效果", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "advisory change application", Text: "为什么申请修改失败", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "negated change", Text: "please don't make the requested changes", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "negated chinese", Text: "请勿修改代码", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "negated inspection", Text: "Explain why it fails. Do not inspect or change files.", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "negated chinese inspection", Text: "解释失败原因，不要检查或修改文件", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "review only", Text: "review only and do not fix anything", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "markdown anchor", Text: "why does README.md render incorrectly?", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "git command", Text: "why does git diff --check fail?", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "custom command", Text: "why does mytool --strict fail?", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "repository anchor", Text: "can you analyze this repository and explain why it fails?", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "observable audit", Text: "audit the current configuration", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "make changes", Text: "make the necessary changes", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "thanks then update", Text: "thanks for fixing that, now update the tests", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "chinese thanks then update", Text: "谢谢你，请继续修改配置", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "thanks then review", Text: "thanks for the help; now review this pull request", NeedsEvidence: b(true), NeedsMutation: b(false)},
	{Name: "modify", Text: "please modify the config", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "push", Text: "push the branch", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "commit", Text: "commit the fix", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "move", Text: "move the file", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "bump", Text: "bump the dependency", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "advice then fix", Text: "why does it fail and fix it", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "polite fix before advice", Text: "could you please fix why it fails", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "chinese polite fix before advice", Text: "请你帮我修复为什么会失败", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "chinese advice then fix", Text: "为什么失败然后修复它", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "negated install then patch", Text: "I cannot install dependencies but patch the parser", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "shared negation", Text: "I cannot install and update dependencies", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "reset negation", Text: "I cannot install dependencies and please update config", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "chinese reset", Text: "无法安装依赖请在现有配置中修改", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "negated request", Text: "不想请团队修改代码", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "negated application", Text: "禁止申请修改配置", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "deferred conversation token", Text: "Remember ORBIT-42 and answer on the next turn.", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "chinese deferred conversation token", Text: "记住 ORBIT-42，下一轮回答", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "deferred token with durable negation", Text: "Remember ORBIT-42 for the next turn. Do not save it permanently.", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "chinese deferred token with durable negation", Text: "记住 ORBIT-42，下一轮回答。不要写入文件或长期记忆。", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "long conversational context", Text: "Please keep this code in mind because I will ask you about it in my next message", NeedsEvidence: b(false), NeedsMutation: b(false)},
	{Name: "durable memory", Text: "Remember ORBIT-42 permanently across sessions", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "chinese durable memory", Text: "请永久记住 ORBIT-42，跨会话也要保留", NeedsEvidence: b(true), NeedsMutation: b(true)},
	{Name: "durable memory advice", Text: "How do I save a preference permanently across sessions?", NeedsEvidence: b(false), NeedsMutation: b(false)},
}

// Task carry the task-vs-chat label: greetings and acknowledgements must
// not arm the delivery gates, while actionable requests and failure reports must.
var Task = []Case{
	{Name: "hello", Text: "hello", IsTask: b(false)},
	{Name: "hi", Text: "hi", IsTask: b(false)},
	{Name: "你好", Text: "你好", IsTask: b(false)},
	{Name: "thanks", Text: "thanks", IsTask: b(false)},
	{Name: "谢谢", Text: "谢谢", IsTask: b(false)},
	{Name: "ok", Text: "ok", IsTask: b(false)},
	{Name: "好的", Text: "好的", IsTask: b(false)},
	{Name: "收到", Text: "收到", IsTask: b(false)},
	{Name: "我知道了", Text: "我知道了", IsTask: b(false)},
	{Name: "先不用", Text: "先不用", IsTask: b(false)},
	{Name: "fix bug", Text: "fix the bug", IsTask: b(true)},
	{Name: "create component", Text: "create a component", IsTask: b(true)},
	{Name: "修复问题", Text: "修复这个问题", IsTask: b(true)},
	{Name: "run tests", Text: "run tests", IsTask: b(true)},
	{Name: "modify config", Text: "modify config", IsTask: b(true)},
	{Name: "make changes", Text: "make the requested changes", IsTask: b(true)},
	{Name: "adjust config", Text: "调整现有配置", IsTask: b(true)},
	{Name: "push branch", Text: "push the branch", IsTask: b(true)},
	{Name: "publish release", Text: "publish release", IsTask: b(true)},
	{Name: "看看", Text: "帮我看看这个错误", IsTask: b(true)},
	{Name: "帮我看下", Text: "帮我看下这个问题", IsTask: b(true)},
	{Name: "处理下", Text: "处理下这个 issue", IsTask: b(true)},
	{Name: "排查", Text: "排查一下启动失败", IsTask: b(true)},
	{Name: "定位", Text: "定位这个异常", IsTask: b(true)},
	{Name: "thanks for fixing", Text: "thanks for fixing that!", IsTask: b(false)},
	{Name: "check later", Text: "I'll check later", IsTask: b(false)},
	{Name: "test later", Text: "I'll test it later", IsTask: b(false)},
	{Name: "test was helpful", Text: "that test was helpful", IsTask: b(false)},
	{Name: "辛苦了", Text: "辛苦了", IsTask: b(false)},
	{Name: "thanks then update", Text: "thanks for fixing that, now update the tests", IsTask: b(true)},
	{Name: "chinese thanks then update", Text: "谢谢你，请继续修改配置", IsTask: b(true)},
	{Name: "task before thanks", Text: "review this PR; thanks for the help", IsTask: b(true)},
	{Name: "auth not working", Text: "the auth isn't working", IsTask: b(true)},
	{Name: "help with login", Text: "can you help with login?", IsTask: b(true)},
	{Name: "问题严重", Text: "这个问题很严重", IsTask: b(true)},
	{Name: "卡住了", Text: "页面卡住了", IsTask: b(true)},
	{Name: "没反应", Text: "按钮点击没反应", IsTask: b(true)},
	{Name: "不生效", Text: "配置不生效", IsTask: b(true)},
	{Name: "异常退出", Text: "程序异常退出", IsTask: b(true)},
	{Name: "file reference", Text: "what about @auth.go", IsTask: b(true)},
	{Name: "localized file reference", Text: "检查（@配置.yaml）", IsTask: b(true)},
	{Name: "python file", Text: "check main.py", IsTask: b(true)},
	{Name: "markdown file", Text: "why does README.md render incorrectly", IsTask: b(true)},
	{Name: "relative script", Text: "why does ./scripts/verify.sh fail", IsTask: b(true)},
	{Name: "email is not file reference", Text: "thanks, email me@example.com later", IsTask: b(false)},
	{Name: "long conversation is not task", Text: "Remember ORBIT-42 and answer on the next turn please", IsTask: b(false)},
	{Name: "durable memory is task", Text: "Remember ORBIT-42 permanently across sessions", IsTask: b(true)},
	{Name: "empty", Text: "", IsTask: b(false)},
	{Name: "spaces", Text: "   ", IsTask: b(false)},
	{Name: "question mark", Text: "?", IsTask: b(false)},
}

// GoalBudget carry the Goal write-budget label. Several also carry a
// mutation label, because the corpus deliberately pins turns where the two
// consumers must disagree: a bare fault report is Goal write work but not an
// ordinary Delivery mutation request.
var GoalBudget = []Case{
	{Name: "chinese historical bug", Text: "数据模型管理器又出现历史 BUG 了……", NeedsWriteBudget: b(true), NeedsMutation: b(false)},
	{Name: "chinese settings crash", Text: "应用打开设置时崩溃", NeedsWriteBudget: b(true), NeedsMutation: b(false)},
	{Name: "auth crashes", Text: "the auth service crashes on login", NeedsWriteBudget: b(true)},
	{Name: "parser exception", Text: "parser throws an exception on empty input", NeedsWriteBudget: b(true)},
	{Name: "fix crash in file", Text: "fix the crash in a.go", NeedsWriteBudget: b(true), NeedsMutation: b(true)},
	{Name: "chinese fix wps crash", Text: "帮我修复wps的崩溃问题", NeedsWriteBudget: b(true)},
	{Name: "why fail and fix", Text: "why does it fail and fix it", NeedsWriteBudget: b(true)},
	{Name: "chinese why fail then fix", Text: "为什么失败然后修复它", NeedsWriteBudget: b(true), NeedsMutation: b(true)},
	{Name: "chinese explain and fix", Text: "解释失败原因并修复", NeedsWriteBudget: b(true)},

	{Name: "chinese why bug question", Text: "为什么会出现这个 BUG？", NeedsWriteBudget: b(false), NeedsMutation: b(false)},
	{Name: "chinese analysis only", Text: "只分析原因，不要修改代码。", NeedsWriteBudget: b(false)},
	{Name: "chinese diagnose db", Text: "诊断数据库连接失败原因。", NeedsWriteBudget: b(false), NeedsMutation: b(false)},
	{Name: "chinese reproduce no fix", Text: "复现并定位问题，但不要修复。", NeedsWriteBudget: b(false)},
	{Name: "why bug question", Text: "why does this bug happen?", NeedsWriteBudget: b(false)},
	{Name: "explain without changing", Text: "explain the crash without changing code", NeedsWriteBudget: b(false)},
	{Name: "reproduce and root cause", Text: "reproduce the crash and identify the root cause", NeedsWriteBudget: b(false), NeedsMutation: b(false)},
	{Name: "review only no fix", Text: "review only and do not fix anything", NeedsWriteBudget: b(false)},
	{Name: "hello", Text: "hello", NeedsWriteBudget: b(false)},
}

// All is every labeled case across the three sources.
func All() []Case {
	out := make([]Case, 0, len(Delivery)+len(Task)+len(GoalBudget))
	for _, c := range Delivery {
		c.Origin = "delivery_classification"
		out = append(out, c)
	}
	for _, c := range Task {
		c.Origin = "task_heuristic"
		out = append(out, c)
	}
	for _, c := range GoalBudget {
		c.Origin = "goal_budget_class"
		out = append(out, c)
	}
	return out
}
