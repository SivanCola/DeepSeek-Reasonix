// Package turnevent owns the local append-only lifecycle ledger for a session.
// It stores only runtime/display events and never contributes to model context.
package turnevent

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/store"
)

const schemaVersion = 1

// Envelope is one durable runtime event. Dynamic routing fields stay optional:
// Desktop attaches its runtime epoch and submission id without writing them to
// the provider transcript or system prompt.
type Envelope struct {
	SchemaVersion      int              `json:"schemaVersion"`
	SessionID          string           `json:"sessionId"`
	TurnID             string           `json:"turnId"`
	Sequence           uint64           `json:"seq"`
	ItemID             string           `json:"itemId,omitempty"`
	AttemptID          string           `json:"attemptId,omitempty"`
	RuntimeEpoch       string           `json:"runtimeEpoch,omitempty"`
	SubmissionID       string           `json:"submissionId,omitempty"`
	Kind               string           `json:"kind"`
	Status             event.TurnStatus `json:"status"`
	TranscriptRevision int64            `json:"transcriptRevision,omitempty"`
	TranscriptDigest   string           `json:"transcriptDigest,omitempty"`
	CreatedAt          int64            `json:"createdAt"`
	Event              eventwire.Event  `json:"event"`
}

// Ledger serializes compare-and-append, sequence allocation, and terminal
// uniqueness for one session sidecar.
type Ledger struct {
	mu              sync.Mutex
	path            string
	damaged         string
	sessionID       string
	nextSeq         uint64
	turnStartSeq    uint64
	active          string
	status          event.TurnStatus
	terminal        bool
	routing         routingMetadata
	nextRouting     routingMetadata
	submissionTurns map[string]string
	transcript      transcriptSnapshot
}

type routingMetadata struct {
	runtimeEpoch string
	submissionID string
}

type transcriptSnapshot struct {
	revision int64
	digest   string
}

// Open loads the valid prefix, isolates a torn/corrupt tail, and converts an
// orphaned non-terminal turn from a prior process into interrupted. No tool is
// replayed during recovery.
func Open(sessionPath, sessionID string) (*Ledger, error) {
	l := &Ledger{
		path: store.SessionTurnEventLog(sessionPath), damaged: store.SessionTurnEventLogDamaged(sessionPath),
		sessionID: sessionID, nextSeq: 1, submissionTurns: make(map[string]string),
	}
	if l.path == "" {
		return l, nil
	}
	records, err := l.repairAndReadLocked()
	if err != nil {
		return nil, err
	}
	pendingTools := make(map[string]eventwire.Tool)
	pendingToolOrder := make([]string, 0)
	for _, rec := range records {
		if rec.Sequence >= l.nextSeq {
			l.nextSeq = rec.Sequence + 1
		}
		if rec.TurnID != "" {
			if rec.TurnID != l.active {
				clear(pendingTools)
				pendingToolOrder = pendingToolOrder[:0]
				l.turnStartSeq = rec.Sequence
			}
			l.active = rec.TurnID
			l.status = rec.Status
			l.terminal = rec.Status.Terminal()
			l.routing = routingMetadata{runtimeEpoch: rec.RuntimeEpoch, submissionID: rec.SubmissionID}
			if rec.SubmissionID != "" {
				l.submissionTurns[rec.SubmissionID] = rec.TurnID
			}
			l.transcript = transcriptSnapshot{revision: rec.TranscriptRevision, digest: rec.TranscriptDigest}
		}
		if rec.Event.Tool != nil && rec.Event.Tool.ID != "" {
			switch rec.Kind {
			case "tool_dispatch":
				if _, exists := pendingTools[rec.Event.Tool.ID]; !exists {
					pendingToolOrder = append(pendingToolOrder, rec.Event.Tool.ID)
				}
				pendingTools[rec.Event.Tool.ID] = *rec.Event.Tool
			case "tool_result":
				delete(pendingTools, rec.Event.Tool.ID)
			}
		}
	}
	if len(records) == 0 && legacyTranscriptExists(sessionPath) {
		id, idErr := newTurnID()
		if idErr != nil {
			return nil, idErr
		}
		l.active, l.status, l.terminal = id, event.TurnQueued, false
		bootstrap := event.Event{Kind: event.TurnStatusChanged, TurnID: id, Status: event.TurnCompleted}
		if _, ok, appendErr := l.appendLocked(bootstrap, event.TurnCompleted); appendErr != nil {
			return nil, appendErr
		} else if !ok {
			return nil, fmt.Errorf("bootstrap legacy session %s: terminal compare-and-append rejected", sessionID)
		}
	}
	if l.active != "" && !l.terminal {
		// A crash can leave durable tool_dispatch records without matching
		// tool_result records. Close those display items explicitly before the
		// interrupted terminal event; never replay the tool, especially writers.
		for _, id := range pendingToolOrder {
			tool, ok := pendingTools[id]
			if !ok {
				continue
			}
			result := event.Event{Kind: event.ToolResult, TurnID: l.active, Tool: event.Tool{
				ID:           tool.ID,
				Name:         tool.Name,
				ResolvedName: tool.ResolvedName,
				CapabilityID: tool.CapabilityID,
				ReadOnly:     tool.ReadOnly,
				ParentID:     tool.ParentID,
				Err:          "interrupted: runtime restarted before the tool completed",
			}}
			if _, ok, appendErr := l.appendLocked(result, l.status); appendErr != nil {
				return nil, appendErr
			} else if !ok {
				return nil, fmt.Errorf("recover orphaned tool %s in turn %s: append rejected", id, l.active)
			}
		}
		e := event.Event{Kind: event.TurnDone, TurnID: l.active, Status: event.TurnInterrupted, Err: errors.New("runtime restarted before the turn reached a terminal event")}
		if _, ok, appendErr := l.appendLocked(e, event.TurnInterrupted); appendErr != nil {
			return nil, appendErr
		} else if !ok {
			return nil, fmt.Errorf("recover orphaned turn %s: terminal compare-and-append rejected", l.active)
		}
	}
	return l, nil
}

func legacyTranscriptExists(sessionPath string) bool {
	if sessionPath == "" {
		return false
	}
	info, err := os.Stat(sessionPath)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func (l *Ledger) Begin() (string, error) {
	if l == nil {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != "" && !l.terminal {
		return "", fmt.Errorf("turn %s is still active", l.active)
	}
	id, err := newTurnID()
	if err != nil {
		return "", err
	}
	l.active, l.status, l.terminal = id, event.TurnQueued, false
	l.turnStartSeq = l.nextSeq
	l.routing, l.nextRouting = l.nextRouting, routingMetadata{}
	if l.routing.submissionID != "" {
		l.submissionTurns[l.routing.submissionID] = id
	}
	l.transcript = transcriptSnapshot{}
	return id, nil
}

// SetRoutingMetadata binds desktop-local routing identity to the next turn
// without leaking it into provider messages or the system prompt.
func (l *Ledger) SetRoutingMetadata(runtimeEpoch, submissionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	// Desktop binds metadata before Controller admission calls Begin. Keep it in
	// a one-shot slot so an automatically dispatched follow-up cannot inherit a
	// previous optimistic submission id.
	l.nextRouting = routingMetadata{runtimeEpoch: runtimeEpoch, submissionID: submissionID}
	l.mu.Unlock()
}

// TurnIDForSubmission returns the stable admission identity even when a very
// fast provider has already completed (or a queued follow-up has since begun).
func (l *Ledger) TurnIDForSubmission(submissionID string) string {
	if l == nil || submissionID == "" {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.submissionTurns[submissionID]
}

// SetTranscriptSnapshot records the local transcript identity stamped on the
// terminal envelope. It is diagnostic/projection metadata only.
func (l *Ledger) SetTranscriptSnapshot(revision int64, digest string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.transcript = transcriptSnapshot{revision: revision, digest: digest}
	l.mu.Unlock()
}

func (l *Ledger) ActiveTurnID() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return ""
	}
	return l.active
}

func (l *Ledger) CurrentStatus() event.TurnStatus {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// ProjectionCursor returns the durable high-water mark and the sequence after
// which a reconnecting UI must replay. Idle sessions can trust their provider
// transcript through latest; active sessions replay only the current turn so
// historical provider messages are not projected twice.
func (l *Ledger) ProjectionCursor() (latest, replayAfter uint64) {
	if l == nil {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextSeq > 0 {
		latest = l.nextSeq - 1
	}
	replayAfter = latest
	if l.active != "" && !l.terminal && l.turnStartSeq > 0 {
		replayAfter = l.turnStartSeq - 1
	}
	return latest, replayAfter
}

// Append stamps and persists e before its caller publishes it. ok=false means
// a second terminal event was rejected by compare-and-append.
func (l *Ledger) Append(e event.Event, status event.TurnStatus) (event.Event, bool, error) {
	if l == nil {
		return e, true, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appendLocked(e, status)
}

func (l *Ledger) appendLocked(e event.Event, status event.TurnStatus) (event.Event, bool, error) {
	if l.active == "" {
		return e, true, nil
	}
	if l.terminal {
		return e, false, nil
	}
	if status == "" {
		status = l.status
	}
	var err error
	status, err = nextTurnStatus(l.status, status)
	if err != nil {
		return e, false, err
	}
	if l.path == "" {
		if e.TurnID == "" {
			e.TurnID = l.active
		}
		e.Sequence = l.nextSeq
		e.Status = status
		l.nextSeq++
		l.status = status
		if status.Terminal() {
			l.terminal = true
		}
		return e, true, nil
	}
	e.TurnID = l.active
	e.Sequence = l.nextSeq
	e.Status = status
	w := eventwire.ToWire(e)
	attemptID := ""
	if e.Kind == event.StreamAttempt {
		attemptID = e.StreamAttempt.ID
	} else if e.Tool.AttemptID != "" {
		attemptID = e.Tool.AttemptID
	}
	kind, _ := eventwire.KindName(e.Kind)
	rec := Envelope{
		SchemaVersion: schemaVersion, SessionID: l.sessionID, TurnID: e.TurnID,
		Sequence: e.Sequence, ItemID: e.ItemID, AttemptID: attemptID,
		RuntimeEpoch: l.routing.runtimeEpoch, SubmissionID: l.routing.submissionID,
		Kind: kind, Status: status, TranscriptRevision: l.transcript.revision,
		TranscriptDigest: l.transcript.digest, CreatedAt: time.Now().UnixMilli(), Event: w,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return e, false, err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return e, false, err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return e, false, err
	}
	if _, err = f.Write(append(line, '\n')); err == nil && status.Terminal() {
		// Terminal acknowledgement is the commit barrier: fsync flushes earlier
		// ordered appends before TurnDone is published without syncing each delta.
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return e, false, err
	}
	l.nextSeq++
	l.status = status
	if status.Terminal() {
		l.terminal = true
	}
	return e, true, nil
}

func nextTurnStatus(current, requested event.TurnStatus) (event.TurnStatus, error) {
	if current == "" || current == requested {
		return requested, nil
	}
	if current.Terminal() {
		return requested, fmt.Errorf("turn is already terminal (%s)", current)
	}
	// Cancellation is sticky. A concurrently completing prompt/tool callback
	// may still emit an ordinary in_progress/waiting_user event, but it cannot
	// move the authoritative lifecycle backwards out of cancelling.
	if current == event.TurnCancelling && !requested.Terminal() {
		return event.TurnCancelling, nil
	}
	valid := false
	switch current {
	case event.TurnQueued:
		valid = requested == event.TurnInProgress || requested == event.TurnWaitingUser || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnInProgress:
		valid = requested == event.TurnWaitingUser || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnWaitingUser:
		valid = requested == event.TurnInProgress || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnCancelling:
		valid = requested.Terminal()
	}
	if !valid {
		return requested, fmt.Errorf("invalid turn status transition %s -> %s", current, requested)
	}
	return requested, nil
}

// EventsAfter replays only the valid durable prefix and always returns a
// non-nil slice for Wails' empty-array compatibility.
func (l *Ledger) EventsAfter(after uint64) ([]Envelope, error) {
	if l == nil || l.path == "" {
		return []Envelope{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	records, err := l.repairAndReadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Envelope, 0, len(records))
	for _, rec := range records {
		if rec.Sequence > after {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (l *Ledger) repairAndReadLocked() ([]Envelope, error) {
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Envelope{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Envelope, 0)
	validBytes := 0
	expectedSeq := uint64(1)
	for validBytes < len(data) {
		rest := data[validBytes:]
		newline := bytes.IndexByte(rest, '\n')
		if newline < 0 {
			break
		}
		lineEnd := validBytes + newline
		line := bytes.TrimSpace(data[validBytes:lineEnd])
		if len(line) == 0 {
			validBytes = lineEnd + 1
			continue
		}
		var rec Envelope
		if err := json.Unmarshal(line, &rec); err != nil || rec.SchemaVersion != schemaVersion || rec.Sequence != expectedSeq {
			break
		}
		records = append(records, rec)
		expectedSeq++
		validBytes = lineEnd + 1
	}
	if validBytes < len(data) {
		if err := os.WriteFile(l.damaged, data[validBytes:], 0o600); err != nil {
			return nil, err
		}
		if err := os.Truncate(l.path, int64(validBytes)); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func newTurnID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "turn_" + hex.EncodeToString(raw[:]), nil
}
