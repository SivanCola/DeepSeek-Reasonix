package gemini

import (
	"encoding/json"

	"reasonix/internal/base/flexstr"
)

// The request and the steps it carries, as the Interactions API reference
// (https://ai.google.dev/api/interactions-api, read 2026-10-17) defines them.

type request struct {
	Model             string     `json:"model"`
	Input             []any      `json:"input"`
	SystemInstruction string     `json:"system_instruction,omitempty"`
	Tools             []tool     `json:"tools,omitempty"`
	Stream            bool       `json:"stream"`
	Store             bool       `json:"store"`
	GenerationConfig  *genConfig `json:"generation_config,omitempty"`
}

type genConfig struct {
	MaxOutputTokens   int    `json:"max_output_tokens,omitempty"`
	ThinkingLevel     string `json:"thinking_level,omitempty"`
	ThinkingSummaries string `json:"thinking_summaries,omitempty"`
}

type tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// content is one block of a user_input or model_output step.
type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type contentStep struct {
	Type    string    `json:"type"` // user_input | model_output
	Content []content `json:"content"`
}

type functionCallStep struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type functionResultStep struct {
	Type    string    `json:"type"`
	CallID  string    `json:"call_id"`
	Name    string    `json:"name,omitempty"`
	IsError bool      `json:"is_error,omitempty"`
	Result  []content `json:"result"`
}

type thoughtStep struct {
	Type      string        `json:"type"`
	Summary   []textContent `json:"summary,omitempty"`
	Signature string        `json:"signature,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// The SSE payloads. Every data line repeats its event name in event_type.

type streamEvent struct {
	EventType string `json:"event_type"`
	Index     int    `json:"index"`
	Step      *struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"step"`
	Delta *struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Signature string          `json:"signature"`
		Arguments json.RawMessage `json:"arguments"`
		Content   *textContent    `json:"content"`
	} `json:"delta"`
	Interaction *struct {
		Status string     `json:"status"`
		Usage  *wireUsage `json:"usage"`
		Errors []wireErr  `json:"errors"`
	} `json:"interaction"`
	Error *wireErr `json:"error"`
}

type wireErr struct {
	Code    flexstr.String `json:"code"`
	Message string         `json:"message"`
}

type wireUsage struct {
	TotalInputTokens   int `json:"total_input_tokens"`
	TotalOutputTokens  int `json:"total_output_tokens"`
	TotalThoughtTokens int `json:"total_thought_tokens"`
	TotalCachedTokens  int `json:"total_cached_tokens"`
	TotalTokens        int `json:"total_tokens"`
}
