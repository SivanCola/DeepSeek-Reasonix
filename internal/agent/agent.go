package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// maxToolOutputBytes caps a single tool result before it goes into the model's
// context. ~32KB is roughly 8K tokens — enough for a full file read or a busy
// grep, while preventing one accidental "read this 5 MB log" from blowing the
// window before the next compaction runs.
const maxToolOutputBytes = 32 * 1024

// Renderer redraws the assistant's final-answer text as styled output. It is
// applied only after a turn's text stream completes, so the user sees raw
// markdown stream live, then a single redraw replaces it with formatted
// output. The renderer is intentionally interface-shaped so the agent stays
// independent of the cli's markdown library choice. Consumed by TextSink.
type Renderer interface {
	Render(text string) string
}

// Gate decides, per tool call, whether it may run. The agent consults it at
// execute time (after the plan-mode gate). It is interface-shaped so the agent
// stays independent of the permission package and of how "ask" is resolved
// (silently in headless runs, interactively in the chat TUI). A nil gate means
// no gating — every call runs, preserving behaviour for callers that don't wire
// one in. reason is fed back to the model when allow is false; a non-nil err
// (e.g. ctx cancelled awaiting approval) is treated as a block for that call.
type Gate interface {
	Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (allow bool, reason string, err error)
}

// Agent drives a single task: a Provider, a tool Registry, and a Session wired
// into the main loop.
type Agent struct {
	prov        provider.Provider
	tools       *tool.Registry
	session     *Session
	maxSteps    int
	temperature float64
	pricing     *provider.Pricing

	// sink receives the turn's typed event stream (reasoning/text deltas, tool
	// dispatch/results, usage, notices). The agent no longer formats output
	// itself — a frontend's Sink decides how to render. Never nil; New defaults
	// it to event.Discard.
	sink event.Sink

	// lastUsage caches the most recent per-turn telemetry the provider
	// reported so the CLI can expose a context gauge without re-scraping the
	// usage line out of the output writer.
	lastUsage *provider.Usage

	// lastShape caches the prefix snapshot from the previous turn so we can
	// detect cache-prefix churn (system/tools/log_rewrite) turn-over-turn.
	lastShape *PrefixShape

	// diagHistory keeps the last ~50 cache diagnostic entries for
	// /cache-report inspection.
	diagHistory []event.CacheDiagnostics

	// planMode, when true, refuses any tool call whose ReadOnly() is false.
	// The system prompt and tool list never change with the toggle so the
	// prompt-cache prefix stays valid; the gating happens at execute time
	// and the model sees a "blocked" result it can adapt to. Toggled from
	// the outside via SetPlanMode.
	planMode bool

	// gate, when non-nil, is the per-call permission gate consulted after the
	// plan-mode check. nil disables gating entirely.
	gate Gate

	// Context management: when a turn's prompt nears contextWindow, the older
	// middle of the session is summarized away, keeping recentKeep messages
	// verbatim and archiving the originals under archiveDir.
	contextWindow int
	compactRatio  float64
	recentKeep    int
	archiveDir    string
}

// SetPlanMode flips the read-only gate. While true, executeOne refuses any
// non-ReadOnly tool the model calls and returns a "blocked" result instead of
// running it. The cache-friendly bits — system prompt, tools schema, message
// history — are left untouched, so the toggle costs nothing in cache hits.
func (a *Agent) SetPlanMode(v bool) { a.planMode = v }

// SetGate installs the per-call permission gate. Used by `reasonix chat` to swap the
// headless gate built in setup for an interactive one that prompts the user;
// nil disables gating. Safe to call before the run loop starts.
func (a *Agent) SetGate(g Gate) { a.gate = g }

// Session returns the agent's current conversation, useful for persistence
// hooks that need to read the message log between turns.
func (a *Agent) Session() *Session { return a.session }

// SetSession replaces the agent's conversation wholesale. Used by
// `reasonix chat --resume` to load a saved JSONL transcript before the first turn,
// so the model picks up exactly where it left off.
func (a *Agent) SetSession(s *Session) { a.session = s }

// LastUsage returns the most recent per-turn token telemetry the provider
// reported (nil if no turn has run yet). The TUI uses it to show a context
// gauge alongside the prompt; the actual cache decisions still live inside
// maybeCompact.
func (a *Agent) LastUsage() *provider.Usage { return a.lastUsage }

// ContextWindow returns the configured context-window size in tokens. 0
// means compaction is disabled for this agent.
func (a *Agent) ContextWindow() int { return a.contextWindow }

// CompactNow runs one compaction pass immediately, regardless of the
// usage-ratio threshold maybeCompact normally honours. Used by the chat
// TUI's `/compact` command so the user can reset the prefix before it
// naturally fills up.
func (a *Agent) CompactNow(ctx context.Context) error { return a.compact(ctx) }

// CacheReport returns recent cache diagnostic entries for /cache-report.
func (a *Agent) CacheReport() []event.CacheDiagnostics { return a.diagHistory }

// Options configures an Agent.
type Options struct {
	MaxSteps    int
	Temperature float64
	Pricing     *provider.Pricing // optional, for per-turn cost display

	// Gate is the per-call permission gate. nil disables gating.
	Gate Gate

	// Context management. ContextWindow <= 0 disables compaction. CompactRatio
	// and RecentKeep fall back to defaults when unset.
	ContextWindow int
	CompactRatio  float64
	RecentKeep    int
	ArchiveDir    string
}

// New constructs an Agent. MaxSteps <= 0 defaults to 25. A nil sink is replaced
// with event.Discard so the agent can always emit unconditionally.
func New(prov provider.Provider, tools *tool.Registry, session *Session, opts Options, sink event.Sink) *Agent {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 25
	}
	if opts.CompactRatio <= 0 {
		opts.CompactRatio = defaultCompactRatio
	}
	if opts.RecentKeep <= 0 {
		opts.RecentKeep = defaultRecentKeep
	}
	if sink == nil {
		sink = event.Discard
	}
	return &Agent{
		prov:          prov,
		tools:         tools,
		session:       session,
		maxSteps:      opts.MaxSteps,
		temperature:   opts.Temperature,
		pricing:       opts.Pricing,
		sink:          sink,
		gate:          opts.Gate,
		contextWindow: opts.ContextWindow,
		compactRatio:  opts.CompactRatio,
		recentKeep:    opts.RecentKeep,
		archiveDir:    opts.ArchiveDir,
	}
}

// Run appends the user input and runs the loop until the model stops requesting
// tools or maxSteps is reached.
func (a *Agent) Run(ctx context.Context, input string) error {
	a.sink.Emit(event.Event{Kind: event.TurnStarted})
	a.session.IncrementTurn()
	a.session.Add(provider.Message{Role: provider.RoleUser, Content: input})

	for step := 0; step < a.maxSteps; step++ {
		text, reasoning, calls, usage, err := a.stream(ctx)
		if err != nil {
			return err
		}
		if usage != nil && usage.TotalTokens > 0 {
			sysPrompt := ""
			if len(a.session.Messages) > 0 && a.session.Messages[0].Role == provider.RoleSystem {
				sysPrompt = a.session.Messages[0].Content
			}
			schemas := a.tools.Schemas()
			cur := CaptureShape(sysPrompt, schemas, a.session.RewriteVersion())
			diag := &CacheDiagnostics{}
			hadPrev := a.lastShape != nil
			if hadPrev {
				d := CompareShape(*a.lastShape, cur, usage)
				diag = &d
			}
			a.lastShape = &cur
			a.session.AddTokens(usage.TotalTokens)
			if hadPrev {
				a.diagHistory = append(a.diagHistory, *diag)
			}
			if len(a.diagHistory) > 50 {
				a.diagHistory = a.diagHistory[len(a.diagHistory)-50:]
			}
			a.sink.Emit(event.Event{
				Kind:             event.Usage,
				Usage:            usage,
				Pricing:          a.pricing,
				CacheDiagnostics: diag,
				CumulativeTokens: a.session.CumulativeTokens(),
				TurnCount:        a.session.TurnCount(),
			})
		}
		if msg, ok := finishReasonMessage(usage); ok {
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg})
		}

		// Round-trip reasoning_content on the assistant turn so multi-turn
		// thinking chains stay coherent (MiMo / DeepSeek-reasoner ask for this).
		a.session.Add(provider.Message{
			Role:             provider.RoleAssistant,
			Content:          text,
			ReasoningContent: reasoning,
			ToolCalls:        calls,
		})

		if len(calls) == 0 {
			return nil // model gave a final answer
		}

		results := a.executeBatch(ctx, calls)
		for i, call := range calls {
			a.session.Add(provider.Message{
				Role:       provider.RoleTool,
				Content:    results[i],
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}

		// The prompt only grows from here; compact before the next turn so it
		// stays within the model's window.
		a.maybeCompact(ctx, usage)
	}
	return fmt.Errorf("reached max steps (%d) without completing", a.maxSteps)
}

// stream runs one completion, emitting reasoning and text deltas as typed
// events and collecting complete tool calls. A Message event closes the text
// stream so a sink can re-render the streamed raw text as styled markdown. The
// accumulated text and reasoning are also returned so the caller can round-trip
// reasoning on the next turn.
func (a *Agent) stream(ctx context.Context) (string, string, []provider.ToolCall, *provider.Usage, error) {
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages:    a.session.Messages,
		Tools:       a.tools.Schemas(),
		Temperature: a.temperature,
	})
	if err != nil {
		return "", "", nil, nil, err
	}

	var text, reasoning strings.Builder
	var calls []provider.ToolCall
	var usage *provider.Usage
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkReasoning:
			reasoning.WriteString(chunk.Text)
			a.sink.Emit(event.Event{Kind: event.Reasoning, Text: chunk.Text})
		case provider.ChunkText:
			text.WriteString(chunk.Text)
			a.sink.Emit(event.Event{Kind: event.Text, Text: chunk.Text})
		case provider.ChunkToolCall:
			calls = append(calls, *chunk.ToolCall)
		case provider.ChunkUsage:
			usage = chunk.Usage
			a.lastUsage = chunk.Usage
		case provider.ChunkError:
			return "", "", nil, nil, chunk.Err
		}
	}
	// Close the text stream: a sink may re-render the streamed raw text as
	// styled markdown now that it is complete. Reasoning rides along so the sink
	// has the full chain if it wants it.
	if text.Len() > 0 || reasoning.Len() > 0 {
		a.sink.Emit(event.Event{Kind: event.Message, Text: text.String(), Reasoning: reasoning.String()})
	}
	return text.String(), reasoning.String(), calls, usage, nil
}

// executeBatch dispatches one model turn's tool calls. A ToolDispatch event is
// emitted for every call up front, in call order, so a frontend can show the
// timeline chronologically. Calls fan out across goroutines only when every
// call's tool is ReadOnly (canParallelise); a single non-ReadOnly call drops
// the whole batch back to sequential to preserve write/read ordering. ToolResult
// events are emitted after the batch in call order, so emission stays serial
// even when execution parallelised.
func (a *Agent) executeBatch(ctx context.Context, calls []provider.ToolCall) []string {
	for _, c := range calls {
		t, ok := a.tools.Get(c.Name)
		a.sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
			ID:       c.ID,
			Name:     c.Name,
			Args:     c.Arguments,
			ReadOnly: ok && t.ReadOnly(),
		}})
	}

	results := make([]string, len(calls))
	outcomes := make([]toolOutcome, len(calls))
	run := func(i int) {
		outcomes[i] = a.executeOne(ctx, calls[i])
		results[i] = outcomes[i].output
	}

	if canParallelise(a.tools, calls) && len(calls) > 1 {
		const maxParallel = 8
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for i := range calls {
			i := i
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				run(i)
			}()
		}
		wg.Wait()
	} else {
		for i := range calls {
			run(i)
		}
	}

	for i, c := range calls {
		o := outcomes[i]
		t, ok := a.tools.Get(c.Name)
		a.sink.Emit(event.Event{Kind: event.ToolResult, Tool: event.Tool{
			ID:        c.ID,
			Name:      c.Name,
			Args:      c.Arguments,
			Output:    o.output,
			Err:       o.blockMsg,
			ReadOnly:  ok && t.ReadOnly(),
			Truncated: o.truncated,
		}})
		if o.truncated && o.truncMsg != "" {
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: o.truncMsg})
		}
	}
	return results
}

// toolOutcome is one tool call's result, split into the model-facing output and
// the display-facing notice bits. blockMsg is set (without the "name " prefix)
// when the call was blocked, so a sink can render "⊘ name <blockMsg>"; truncMsg
// is set (without the "· " prefix) when the output was head+tailed.
type toolOutcome struct {
	output    string
	blocked   bool
	blockMsg  string
	truncated bool
	truncMsg  string
}

// executeOne runs a single tool call. It is pure with respect to the event sink
// — the caller emits ToolDispatch/ToolResult — so it is safe to invoke from
// parallel goroutines.
func (a *Agent) executeOne(ctx context.Context, call provider.ToolCall) toolOutcome {
	t, ok := a.tools.Get(call.Name)
	if !ok {
		return toolOutcome{output: fmt.Sprintf("error: unknown tool %q", call.Name)}
	}
	if a.planMode && !t.ReadOnly() {
		return toolOutcome{output: fmt.Sprintf("blocked: %q is a writer tool and plan mode is read-only — propose the change in your final answer instead. The user will toggle plan mode off (Tab) to execute.", call.Name)}
	}
	if a.gate != nil {
		allow, reason, err := a.gate.Check(ctx, call.Name, json.RawMessage(call.Arguments), t.ReadOnly())
		if err != nil {
			return toolOutcome{
				output:   fmt.Sprintf("blocked: %s (%v)", reason, err),
				blocked:  true,
				blockMsg: fmt.Sprintf("blocked: %v", err),
			}
		}
		if !allow {
			return toolOutcome{
				output:   "blocked: " + reason,
				blocked:  true,
				blockMsg: "blocked by permission policy",
			}
		}
	}
	result, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		body, truncMsg := truncateToolOutput(fmt.Sprintf("error: %v\n%s", err, result))
		return toolOutcome{output: body, truncated: truncMsg != "", truncMsg: truncMsg}
	}
	body, truncMsg := truncateToolOutput(result)
	return toolOutcome{output: body, truncated: truncMsg != "", truncMsg: truncMsg}
}

// canParallelise returns true iff every call targets a known, ReadOnly tool.
// Any unknown tool name (let the sequential path produce a clean error) or any
// non-ReadOnly tool (preserve write ordering) forces serial execution.
func canParallelise(r *tool.Registry, calls []provider.ToolCall) bool {
	for _, c := range calls {
		t, ok := r.Get(c.Name)
		if !ok || !t.ReadOnly() {
			return false
		}
	}
	return true
}

// truncateToolOutput head+tails s when it exceeds maxToolOutputBytes, slicing
// on rune boundaries so we never split a multibyte glyph. Returns the possibly
// trimmed body plus a one-line user-facing notice when truncation happened
// (empty when it didn't, without the "· " display prefix).
func truncateToolOutput(s string) (string, string) {
	if len(s) <= maxToolOutputBytes {
		return s, ""
	}
	keep := maxToolOutputBytes / 2
	head := snapToRuneBoundary(s, 0, keep)
	tail := snapToRuneBoundary(s, len(s)-keep, len(s))
	omitted := len(s) - len(head) - len(tail)
	notice := fmt.Sprintf("tool output truncated: %d of %d bytes elided", omitted, len(s))
	body := head + fmt.Sprintf("\n\n…[truncated %d of %d bytes — rerun with narrower args to see the middle]…\n\n", omitted, len(s)) + tail
	return body, notice
}

// snapToRuneBoundary returns s[lo:hi] with the bounds nudged outward until
// both land on rune-start positions.
func snapToRuneBoundary(s string, lo, hi int) string {
	for lo > 0 && !utf8.RuneStart(s[lo]) {
		lo--
	}
	for hi < len(s) && !utf8.RuneStart(s[hi]) {
		hi++
	}
	return s[lo:hi]
}

// finishReasonMessage maps an abnormal finish_reason to a one-line warning,
// returning ok=false for the normal terminations ("stop", "tool_calls") and a
// nil usage. The sink renders the message; the "! " prefix is presentation.
func finishReasonMessage(u *provider.Usage) (string, bool) {
	if u == nil {
		return "", false
	}
	switch u.FinishReason {
	case "length":
		return "response truncated: hit max output tokens", true
	case "content_filter":
		return "response blocked by content filter", true
	case "repetition_truncation":
		return "response truncated: model repetition detected", true
	default:
		return "", false
	}
}
