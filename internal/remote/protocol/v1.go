// Package protocol defines the versioned /remote/v1 wire types used between the
// local Remote Gateway and the remote-runtime process. It is intentionally free
// of control.Controller so both ends can share types without pulling the agent
// stack into lightweight clients.
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ProtocolMajor is the major protocol version. Mismatches block opening a
// remote window; the desktop upgrades the remote binary and retries. There is
// no fallback to the legacy Serve HTML remote UI.
const ProtocolMajor = 1

// ProtocolMinor is the minor protocol version. Older minors may be accepted when
// the major matches, provided required capabilities are present.
const ProtocolMinor = 0

// ProtocolVersion is the dotted version string returned by hello.
const ProtocolVersion = "1.0"

// APIPrefix is the HTTP path prefix for remote-runtime handlers.
const APIPrefix = "/remote/v1"

// Capability bits returned by hello / capabilities. Values are stable once
// published; new bits must be appended.
const (
	CapSessions    uint64 = 1 << iota // multi-session registry
	CapEvents                         // unified SSE event stream
	CapCheckpoint                     // session checkpoint download
	CapRestore                        // restore-from-mirror as new session
	CapBroker                         // provider broker reverse tunnel
	CapTools                          // tool/skill/MCP surface reporting
	CapAttachments                    // attachment upload path
	CapShell                          // ! shell / PTY support announced
)

// RequiredCapabilities is the minimum bitset a desktop of this version needs.
const RequiredCapabilities = CapSessions | CapEvents | CapCheckpoint | CapBroker

// HelloResponse is returned by GET /remote/v1/hello.
type HelloResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	ProtocolMajor   int    `json:"protocolMajor"`
	ProtocolMinor   int    `json:"protocolMinor"`
	ReasonixVersion string `json:"reasonixVersion"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	Workspace       string `json:"workspace"`
	Capabilities    uint64 `json:"capabilities"`
	PID             int    `json:"pid,omitempty"`
	StartedAt       string `json:"startedAt,omitempty"` // RFC3339
}

// Compatible reports whether a remote hello is acceptable for this client.
func (h HelloResponse) Compatible() error {
	if h.ProtocolMajor != ProtocolMajor {
		return fmt.Errorf("%w: remote major %d, local major %d", ErrProtocolMismatch, h.ProtocolMajor, ProtocolMajor)
	}
	if h.Capabilities&RequiredCapabilities != RequiredCapabilities {
		return fmt.Errorf("%w: remote capabilities %#x missing required %#x", ErrCapabilityMismatch, h.Capabilities, RequiredCapabilities)
	}
	if strings.TrimSpace(h.Workspace) == "" {
		return fmt.Errorf("%w: empty workspace", ErrInvalidResponse)
	}
	return nil
}

// SessionSummary is a lightweight listing entry for remote sessions.
type SessionSummary struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	ModelRef  string `json:"modelRef,omitempty"`
	Path      string `json:"path,omitempty"`
	Revision  int64  `json:"revision"`
	Digest    string `json:"digest,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"` // RFC3339
	Running   bool   `json:"running,omitempty"`
}

// CreateSessionRequest is the body for POST /remote/v1/sessions.
type CreateSessionRequest struct {
	Model   string `json:"model,omitempty"`
	Resume  string `json:"resume,omitempty"` // existing remote session path or id
	Label   string `json:"label,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// CreateSessionResponse is returned after a session is created or resumed.
type CreateSessionResponse struct {
	Session SessionSummary `json:"session"`
}

// RestoreSessionRequest creates a NEW remote session from a local mirror
// checkpoint. Existing remote session IDs must never be overwritten.
type RestoreSessionRequest struct {
	// Checkpoint is the opaque checkpoint payload previously downloaded from
	// GET .../checkpoint. The server allocates a new session ID always.
	Checkpoint json.RawMessage `json:"checkpoint"`
	Label      string          `json:"label,omitempty"`
	SourceHint string          `json:"sourceHint,omitempty"` // UI-only origin label
}

// SubmitRequest is the body for POST .../sessions/{id}/submit.
type SubmitRequest struct {
	Input   string `json:"input"`
	Display string `json:"display,omitempty"`
}

// ApproveRequest is the body for POST .../sessions/{id}/approve.
type ApproveRequest struct {
	ID      string `json:"id"`
	Allow   bool   `json:"allow"`
	Session bool   `json:"session,omitempty"`
	Persist bool   `json:"persist,omitempty"`
}

// AnswerRequest is the body for POST .../sessions/{id}/answer.
type AnswerRequest struct {
	ID      string          `json:"id"`
	Answers json.RawMessage `json:"answers"`
}

// CompactRequest is the body for POST .../sessions/{id}/compact.
type CompactRequest struct {
	// Empty for now; reserved for future options.
}

// RewindRequest is the body for POST .../sessions/{id}/rewind.
type RewindRequest struct {
	Turn  int    `json:"turn"`
	Scope string `json:"scope,omitempty"`
}

// ForkRequest is the body for POST .../sessions/{id}/fork.
type ForkRequest struct {
	Turn int    `json:"turn"`
	Name string `json:"name,omitempty"`
}

// SetModelRequest is the body for POST .../sessions/{id}/model.
type SetModelRequest struct {
	Model  string  `json:"model"`
	Effort *string `json:"effort,omitempty"`
}

// EventEnvelope wraps every SSE frame from GET /remote/v1/events.
type EventEnvelope struct {
	SessionID string          `json:"sessionId"`
	Seq       int64           `json:"seq"`
	Revision  int64           `json:"revision,omitempty"`
	Kind      string          `json:"kind"` // "session" | "checkpoint" | "notice" | wire event kind
	Payload   json.RawMessage `json:"payload,omitempty"`
	At        time.Time       `json:"at"`
}

// CheckpointEvent is emitted after the remote controller persists a revision.
type CheckpointEvent struct {
	SessionID string `json:"sessionId"`
	Revision  int64  `json:"revision"`
	Digest    string `json:"digest"`
}

// CheckpointManifest describes a downloaded session checkpoint.
type CheckpointManifest struct {
	SessionID   string            `json:"sessionId"`
	Revision    int64             `json:"revision"`
	Digest      string            `json:"digest"`
	Workspace   string            `json:"workspace,omitempty"`
	ModelRef    string            `json:"modelRef,omitempty"`
	Label       string            `json:"label,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	ArtifactSHA map[string]string `json:"artifactSha,omitempty"` // relative path → hex sha256
}

// CapabilitiesResponse is returned by GET /remote/v1/capabilities.
type CapabilitiesResponse struct {
	Skills   []CapabilityItem `json:"skills,omitempty"`
	MCP      []CapabilityItem `json:"mcp,omitempty"`
	Tools    []CapabilityItem `json:"tools,omitempty"`
	Sandbox  string           `json:"sandbox,omitempty"`
	Terminal bool             `json:"terminal,omitempty"`
	Bits     uint64           `json:"bits"`
}

// CapabilityItem is a non-sensitive capability advertisement.
type CapabilityItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

// ErrorBody is the stable JSON error shape for all /remote/v1 endpoints.
// Messages must be redacted: no env vars, headers, or raw provider bodies.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e ErrorBody) Error() string {
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// NewError builds a redacted ErrorBody.
func NewError(code, message string) ErrorBody {
	return ErrorBody{Code: code, Message: strings.TrimSpace(message)}
}
