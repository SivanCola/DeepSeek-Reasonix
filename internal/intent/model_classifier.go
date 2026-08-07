package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// SystemPrompt is the classifier contract. It lives in production code, not in
// the evaluation harness, so the measured accuracy is the accuracy of what
// actually ships: the live corpus run judges this exact text.
//
// Most of it is product rules the model cannot infer. Live evaluation started at
// 94.4% with only the schema, and reached 99.5% once the four "decisive rules"
// below were stated. Every rule here earned its place by fixing a measured
// disagreement with the golden corpus - do not trim it without re-running
// TestLiveClassifierAccuracy.
const SystemPrompt = `You classify ONE user turn sent to a coding agent. Reply with strict JSON only, no prose, no markdown fence, no explanation.

Schema:
{"kind":"<one of: conversation|advisory|observable_read|mutation|persistent_action>",
 "fault_report":<bool>,"read_only_constraint":<bool>,"diagnostic_intent":<bool>,"durable_scope":<bool>}

kind definitions:
- conversation: greetings, thanks, acknowledgements, and requests whose entire scope is this chat (e.g. "remember X and answer next turn").
- advisory: a question seeking explanation, opinion, or general how-to that does NOT require inspecting state on THIS machine or repository.
- observable_read: requires looking at real state HERE - reading files, running read-only commands, inspecting a named file/repo/command - but changing nothing.
- mutation: the user asked for a change (edit, create, delete, run a state-changing command, commit, push, install, configure).
- persistent_action: requires a durable write that outlives this session (e.g. "remember X permanently across sessions").

field definitions:
- fault_report: the user reported something malfunctioning (crash, bug, error, "not working", 崩溃/报错/卡住/不生效), whether or not they asked for a repair.
- read_only_constraint: the user explicitly FORBADE changes ("review only", "do not fix", "只分析", "不要修改").
- diagnostic_intent: the user scoped the work to understanding rather than repairing, WITHOUT forbidding anything ("diagnose", "identify the root cause", "reproduce", "investigate", "诊断", "定位原因", "排查原因"). This is different from read_only_constraint.
- durable_scope: the request explicitly reaches beyond this session ("permanently", "across sessions", "永久", "跨会话").

Decisive rules:
1. LOCAL vs REMOTE. observable_read requires something inspectable HERE - a file, a path, a command, this repo, this codebase. Merely REPORTING or ASKING ABOUT a malfunction in some OTHER product or a remote website (Outlook, WPS, a github.com URL, "the plugin") has nothing local to inspect, so it is advisory. This applies only to reports and questions: if the user explicitly asks you to fix or change it ("帮我修复wps的崩溃问题", "fix the Outlook sync"), that is still mutation no matter which software it is about.
2. REPORTING A FAULT IS NOT REQUESTING A CHANGE. "the app crashes when opening settings" / "应用打开设置时崩溃" describes a problem; the user did not ask for an edit. That is observable_read with fault_report=true, NOT mutation. Only choose mutation when the user actually asked for the change.
3. DIAGNOSIS IS NOT REPAIR. "reproduce the crash and identify the root cause" / "诊断数据库连接失败原因。" is observable_read with diagnostic_intent=true, NOT mutation.
4. NEGATION SCOPE. "I cannot install and update dependencies" states an inability about BOTH verbs -> conversation. But "I cannot install dependencies but patch the parser" refuses one clause and requests the other -> mutation.
5. ASKING SOMEONE TO LOOK IS A TASK. "帮我看下这个问题" / "这个问题很严重" is the user raising a problem for you to handle -> observable_read, not conversation.
6. Politeness and thanks do not change the request. "thanks for fixing that, now update the tests" -> mutation.
7. An email address, URL, or "later" is not a memory request. "thanks, email me@example.com later" is conversation.`

// maxClassifyTokens is generous because reasoning models spend most of their
// budget in reasoning_content and return empty content if the cap is hit
// mid-thought. Measured at roughly 3% empty replies with a tighter cap.
const maxClassifyTokens = 8000

// ModelClassifier reads a user turn with a provider-backed model. Wrap it in
// Bounded before use: this type does the call and the parsing, while Bounded
// owns the timeout, cache, and degradation policy.
//
// The shape mirrors capability.SemanticRouter so routing and intent report usage
// and cost the same way.
type ModelClassifier struct {
	Provider provider.Provider
	Sink     event.Sink
	Model    string
	// Pricing prices this classifier's own usage events; without it the cost
	// always displays as zero.
	Pricing *provider.Pricing
}

type classifyReply struct {
	Kind               string `json:"kind"`
	FaultReport        bool   `json:"fault_report"`
	ReadOnlyConstraint bool   `json:"read_only_constraint"`
	DiagnosticIntent   bool   `json:"diagnostic_intent"`
	DurableScope       bool   `json:"durable_scope"`
}

// Classify returns KindUnknown with a nil error when the turn cannot be read.
// It returns an error only for transport and protocol failures, which Bounded
// converts into the same unknown result.
//
// It retries once on an empty reply. Reasoning models spend their budget in
// reasoning_content and intermittently return empty content - measured at
// roughly 3% of calls against a reasoning model. Without the retry that is a 3%
// silent degradation rate on a gate, which is too high to absorb quietly.
func (c *ModelClassifier) Classify(ctx context.Context, text string) (TurnIntent, error) {
	got, err := c.classifyOnce(ctx, text)
	if err != nil && strings.Contains(err.Error(), errEmptyReply) {
		return c.classifyOnce(ctx, text)
	}
	return got, err
}

const errEmptyReply = "empty reply"

func (c *ModelClassifier) classifyOnce(ctx context.Context, text string) (TurnIntent, error) {
	if c == nil || c.Provider == nil {
		return TurnIntent{}, nil
	}
	if strings.TrimSpace(text) == "" {
		return TurnIntent{}, nil
	}
	ctx = provider.WithRequestAttemptCounter(ctx)

	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: SystemPrompt},
			{Role: provider.RoleUser, Content: text},
		},
		Temperature: provider.TemperaturePtr(0),
		MaxTokens:   maxClassifyTokens,
	}

	var usage *provider.Usage
	defer func() {
		usage = provider.UsageWithRequestAttemptCount(ctx, usage)
		if usage == nil || c.Sink == nil {
			return
		}
		c.Sink.Emit(event.Event{
			Kind:        event.Usage,
			ModelRef:    strings.TrimSpace(c.Model),
			Usage:       usage,
			Pricing:     c.Pricing,
			UsageSource: event.UsageSourceCapabilityRouter,
		})
	}()

	ch, err := c.Provider.Stream(ctx, req)
	if err != nil {
		return TurnIntent{}, err
	}
	var out strings.Builder
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			out.WriteString(chunk.Text)
		case provider.ChunkUsage:
			if chunk.Usage != nil {
				u := *chunk.Usage
				usage = &u
			}
		case provider.ChunkError:
			if chunk.Err != nil {
				return TurnIntent{}, chunk.Err
			}
		}
	}
	return ParseClassifierReply(out.String())
}

// ParseClassifierReply turns a raw model reply into a TurnIntent. It is exported
// so the evaluation harness parses replies exactly the way production does.
//
// An unrecognized kind yields KindUnknown rather than a default, so a model that
// invents a label degrades instead of silently landing in some other bucket.
func ParseClassifierReply(raw string) (TurnIntent, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return TurnIntent{}, fmt.Errorf("%s", errEmptyReply)
	}
	content = stripCodeFence(content)
	if start := strings.Index(content, "{"); start >= 0 {
		if end := strings.LastIndex(content, "}"); end > start {
			content = content[start : end+1]
		}
	}
	var parsed classifyReply
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return TurnIntent{}, fmt.Errorf("unparseable reply %q: %w", clip(content, 120), err)
	}

	kind := KindUnknown
	switch parsed.Kind {
	case "conversation":
		kind = KindConversation
	case "advisory":
		kind = KindAdvisory
	case "observable_read":
		kind = KindObservableRead
	case "mutation":
		kind = KindMutation
	case "persistent_action":
		kind = KindPersistentAction
	}
	if kind == KindUnknown {
		return TurnIntent{}, nil
	}
	return TurnIntent{
		Kind:               kind,
		FaultReport:        parsed.FaultReport,
		ReadOnlyConstraint: parsed.ReadOnlyConstraint,
		DiagnosticIntent:   parsed.DiagnosticIntent,
		DurableScope:       parsed.DurableScope,
		Source:             SourceClassifier,
	}, nil
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
