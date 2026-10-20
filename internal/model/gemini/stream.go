package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"reasonix/internal/contract/provider"
)

// open is one step the stream has started and not yet stopped.
type open struct {
	kind    string // model_output | thought | function_call | other
	text    strings.Builder
	summary strings.Builder
	sig     string
	id      string
	name    string
	args    strings.Builder
	started string // arguments the function_call step carried when it started
	ticked  int
}

// readStream parses the Interactions SSE stream into chunks. Text and thought
// summaries emit live; a function call starts when its step starts and completes
// when it stops, with its streamed argument fragments joined; every stopped step
// is also handed on as a provider item so the next request can replay it. Usage
// and the finish reason come from interaction.completed.
func (c *client) readStream(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer resp.Body.Close()
	defer close(out)

	idleTimeout := c.idleTimeout
	if idleTimeout <= 0 {
		idleTimeout = provider.StreamIdleTimeout
	}
	done := make(chan struct{})
	defer close(done)
	activity := make(chan struct{}, 1)
	var stalled atomic.Bool
	go func() {
		idle := time.NewTimer(idleTimeout)
		defer idle.Stop()
		for {
			select {
			case <-ctx.Done():
				resp.Body.Close()
				return
			case <-idle.C:
				stalled.Store(true)
				resp.Body.Close()
				return
			case <-activity:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleTimeout)
			case <-done:
				return
			}
		}
	}()

	sink := &chunkSink{ctx: ctx, out: out}
	send := sink.send
	state := &streamState{steps: map[int]*open{}, sink: sink}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case activity <- struct{}{}:
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			send(provider.Chunk{Type: provider.ChunkError, Err: provider.StreamDecodeError(c.name, data, err)})
			return
		}
		if c.handle(ctx, ev, state) {
			return
		}
	}

	if ctx.Err() != nil {
		return
	}
	if stalled.Load() {
		err := fmt.Errorf("%s: stream stalled — no data for %s, connection likely dropped; raise idle_timeout_seconds to allow a longer silence", c.name, idleTimeout)
		send(provider.Chunk{Type: provider.ChunkError, Err: provider.StreamInterrupt(err, provider.StreamInterruptIdleTimeout)})
		return
	}
	if err := scanner.Err(); err != nil {
		send(provider.Chunk{Type: provider.ChunkError, Err: provider.ReadCut(c.name, err)})
		return
	}
	send(provider.Chunk{Type: provider.ChunkError, Err: provider.StreamInterrupt(
		fmt.Errorf("%s: stream ended before interaction.completed: %w", c.name, io.ErrUnexpectedEOF),
		provider.StreamInterruptPrematureEOF,
	)})
}

// streamState is what one stream has seen so far: the steps started and not yet
// stopped, and how many function calls completed.
type streamState struct {
	steps map[int]*open
	calls int
	sink  *chunkSink
}

// handle applies one event and reports whether the stream is over.
func (c *client) handle(ctx context.Context, ev streamEvent, state *streamState) bool {
	send := state.sink.send
	switch ev.EventType {
	case "step.start":
		if ev.Step == nil {
			return false
		}
		st := &open{kind: ev.Step.Type, id: ev.Step.ID, name: ev.Step.Name}
		switch ev.Step.Type {
		case "model_output", "thought":
		case "function_call":
			if a := strings.TrimSpace(string(ev.Step.Arguments)); a != "" && a != "{}" && a != "null" {
				st.started = a
			}
			if !send(provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: st.id, Name: st.name}}) {
				return true
			}
		default:
			st.kind = "other"
		}
		state.steps[ev.Index] = st
	case "step.delta":
		if st := state.steps[ev.Index]; st != nil && ev.Delta != nil && !c.delta(st, ev, send) {
			return true
		}
	case "step.stop":
		st := state.steps[ev.Index]
		if st == nil {
			return false
		}
		delete(state.steps, ev.Index)
		n, ok := c.stop(st, send)
		state.calls += n
		return !ok
	case "error":
		send(provider.Chunk{Type: provider.ChunkError, Err: streamError(c.name, ev.Error, state.sink.emitted)})
		return true
	case "interaction.completed":
		c.finish(ctx, ev, state.calls, send)
		return true
	}
	return false
}

func (c *client) delta(st *open, ev streamEvent, send func(provider.Chunk) bool) bool {
	d := ev.Delta
	switch d.Type {
	case "text":
		if d.Text != "" {
			st.text.WriteString(d.Text)
			return send(provider.Chunk{Type: provider.ChunkText, Text: d.Text})
		}
	case "thought_summary":
		if d.Content != nil && d.Content.Text != "" {
			st.summary.WriteString(d.Content.Text)
			return send(provider.Chunk{Type: provider.ChunkReasoning, Text: d.Content.Text})
		}
	case "thought_signature":
		st.sig = d.Signature
	case "arguments_delta":
		var part string
		if json.Unmarshal(d.Arguments, &part) != nil || part == "" {
			return true
		}
		st.args.WriteString(part)
		if bucket := st.args.Len() / 2048; bucket > st.ticked {
			st.ticked = bucket
			return send(provider.Chunk{Type: provider.ChunkToolCallArgsDelta, ToolCall: &provider.ToolCall{ID: st.id, Name: st.name}, ArgChars: st.args.Len()})
		}
	}
	return true
}

// stop closes a step: it emits what the step finished (a call), and hands the
// whole step on as a provider item. It reports how many calls it completed.
func (c *client) stop(st *open, send func(provider.Chunk) bool) (int, bool) {
	switch st.kind {
	case "model_output":
		if text := st.text.String(); text != "" {
			return 0, c.item(send, contentStep{Type: "model_output", Content: []content{{Type: "text", Text: text}}})
		}
	case "thought":
		step := thoughtStep{Type: "thought", Signature: st.sig}
		if s := st.summary.String(); s != "" {
			step.Summary = []textContent{{Type: "text", Text: s}}
		}
		if st.sig != "" && !send(provider.Chunk{Type: provider.ChunkReasoning, Signature: st.sig}) {
			return 0, false
		}
		return 0, c.item(send, step)
	case "function_call":
		args := strings.TrimSpace(st.args.String())
		if args == "" {
			args = st.started
		}
		obj := argumentsObject(args)
		call := provider.ToolCall{ID: st.id, Name: st.name, Arguments: string(obj)}
		if !send(provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &call}) {
			return 0, false
		}
		return 1, c.item(send, functionCallStep{Type: "function_call", ID: st.id, Name: st.name, Arguments: obj})
	}
	return 0, true
}

func (c *client) item(send func(provider.Chunk) bool, step any) bool {
	raw, err := json.Marshal(step)
	if err != nil {
		return true
	}
	return send(provider.Chunk{Type: provider.ChunkResponsesItem, ResponsesItem: raw})
}

// finish ends a completed interaction: its usage, then done. A failed or
// cancelled interaction ends as an error instead.
func (c *client) finish(ctx context.Context, ev streamEvent, calls int, send func(provider.Chunk) bool) {
	status := ""
	var usage *wireUsage
	if ev.Interaction != nil {
		status, usage = ev.Interaction.Status, ev.Interaction.Usage
	}
	switch status {
	case "failed", "cancelled":
		var wire *wireErr
		if ev.Interaction != nil && len(ev.Interaction.Errors) > 0 {
			wire = &ev.Interaction.Errors[0]
		}
		send(provider.Chunk{Type: provider.ChunkError, Err: streamError(c.name, wire, true)})
		return
	}
	if usage != nil {
		u := &provider.Usage{
			PromptTokens:     usage.TotalInputTokens,
			CompletionTokens: usage.TotalOutputTokens + usage.TotalThoughtTokens,
			ReasoningTokens:  usage.TotalThoughtTokens,
			CacheHitTokens:   usage.TotalCachedTokens,
			CacheMissTokens:  max(usage.TotalInputTokens-usage.TotalCachedTokens, 0),
			TotalTokens:      usage.TotalTokens,
			FinishReason:     finishReason(status, calls),
		}
		if u.TotalTokens == 0 {
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
		}
		provider.ApplyRequestAttemptCount(ctx, u)
		if !send(provider.Chunk{Type: provider.ChunkUsage, Usage: u}) {
			return
		}
	}
	send(provider.Chunk{Type: provider.ChunkDone})
}

// finishReason maps an interaction status to the finish reasons the agent knows.
// A turn that ended in function calls is a tool_calls turn whatever the status
// word says; incomplete is what hitting max_output_tokens looks like.
func finishReason(status string, calls int) string {
	switch {
	case calls > 0 || status == "requires_action":
		return "tool_calls"
	case status == "incomplete":
		return "length"
	}
	return "stop"
}

// streamError types an in-stream error. Before any output the attempt can be
// made again, so it leaves as an upstream interruption; after output a replay
// would show it twice, so it stays a plain payload error.
func streamError(name string, wire *wireErr, emitted bool) error {
	payload := &provider.StreamPayloadError{Provider: name, Message: "stream error"}
	if wire != nil {
		payload.Message, payload.Type = wire.Message, wire.Code.String()
	}
	if !emitted {
		return provider.StreamInterrupt(payload, provider.StreamInterruptUpstreamError)
	}
	return payload
}
