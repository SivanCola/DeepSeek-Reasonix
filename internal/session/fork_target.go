package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/event"
)

// ForkAvailability names why one source turn can or cannot start a child
// session. A surface shows the reason instead of collapsing every refusal into
// one "unavailable" message.
type ForkAvailability string

const (
	// ForkAvailable means the turn closed at an atomic commit boundary.
	ForkAvailable ForkAvailability = "available"
	// ForkTurnOpen means the turn has no terminal turn/end record yet.
	ForkTurnOpen ForkAvailability = "turn_open"
	// ForkActiveAuthority means the cut would inherit in-flight execution state.
	ForkActiveAuthority ForkAvailability = "active_authority"
	// ForkHistoryUnverifiable means the source keeps no persisted turn records,
	// so no boundary can be proven. Text matching, elapsed time, or a turn that
	// merely looks finished must never substitute for one.
	ForkHistoryUnverifiable ForkAvailability = "history_unverifiable"
)

// ForkTarget is one source turn a client may fork from. It is derived only from
// committed events, so the same source yields the same targets whether it is
// live in this process, owned by another process, or read cold from disk.
type ForkTarget struct {
	TurnID        string           `json:"turnId"`
	TurnNumber    int              `json:"turnNumber"`
	StartSequence uint64           `json:"startSequence"`
	EndSequence   uint64           `json:"endSequence"`
	Status        event.TurnStatus `json:"status"`
	// MessageID is the stable transcript identity of this turn's final assistant
	// reply, empty when the turn committed none.
	MessageID string           `json:"messageId,omitempty"`
	Available bool             `json:"available"`
	Reason    ForkAvailability `json:"reason,omitempty"`
}

// ForkTargetSet is the fork state of one source session. Targets stay empty and
// Verifiable stays false for legacy history that keeps messages without turn
// records, which is what lets a surface say the boundary is unverifiable
// instead of offering a cut it cannot prove.
type ForkTargetSet struct {
	Targets    []ForkTarget `json:"targets"`
	Verifiable bool         `json:"verifiable"`
}

// ForkTargets lists the source's turns in display order. The open turn is
// included as ForkTurnOpen so a surface can explain why its own turn is not
// forkable yet without disabling the turns that already finished.
func ForkTargets(projection Projection) ForkTargetSet {
	targets := make([]ForkTarget, 0, len(projection.Turns)+1)
	for index, turn := range projection.Turns {
		target := ForkTarget{
			TurnID: turn.TurnID, TurnNumber: index + 1,
			StartSequence: turn.StartSequence, EndSequence: turn.EndSequence,
			Status: turn.Status, MessageID: turn.MessageID,
			Available: turn.BoundarySequence != 0,
		}
		if !target.Available {
			target.Reason = ForkTurnOpen
		}
		targets = append(targets, target)
	}
	if projection.TurnID != "" {
		targets = append(targets, ForkTarget{
			TurnID: projection.TurnID, TurnNumber: len(targets) + 1,
			StartSequence: projection.CurrentTurnStart, Status: projection.TurnStatus,
			MessageID: projection.CurrentTurnMessageID, Reason: ForkTurnOpen,
		})
	}
	return ForkTargetSet{Targets: targets, Verifiable: len(projection.Turns) > 0 || projection.TurnID != ""}
}

// ForkSequence resolves the cut for one turn identity. Only a turn that closed
// at an atomic commit boundary resolves; an open turn and an unknown identity
// are refused rather than silently redirected to the newest turn.
func ForkSequence(projection Projection, turnID string) (uint64, ForkAvailability, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return 0, "", fmt.Errorf("session: fork needs a turn id")
	}
	if projection.TurnID == turnID {
		return 0, ForkTurnOpen, nil
	}
	for _, turn := range projection.Turns {
		if turn.TurnID != turnID {
			continue
		}
		if turn.BoundarySequence == 0 {
			return 0, ForkTurnOpen, nil
		}
		return turn.BoundarySequence, ForkAvailable, nil
	}
	return 0, ForkHistoryUnverifiable, nil
}

// ForkSequenceForNumber resolves the cut for a display turn number. It exists
// only for clients that still address turns by their 1-based position; new
// clients carry the stable turn identity instead.
func ForkSequenceForNumber(projection Projection, turn int) (uint64, ForkAvailability, error) {
	set := ForkTargets(projection)
	if turn < 1 || turn > len(set.Targets) {
		return 0, ForkHistoryUnverifiable, fmt.Errorf("session: turn %d is not a forkable turn", turn)
	}
	return ForkSequence(projection, set.Targets[turn-1].TurnID)
}

// ForkTargetSetFor reads the fork state of one session without requiring a live
// runtime. A session owned by another process is read from its durable commits
// and keeps its lease untouched.
func (s *Service) ForkTargetSetFor(ctx context.Context, ref SessionRef) (ForkTargetSet, error) {
	if s == nil {
		return ForkTargetSet{}, fmt.Errorf("session: nil service")
	}
	if err := ref.validate(s.hostID); err != nil {
		return ForkTargetSet{}, err
	}
	snapshot, err := s.query.Snapshot(ctx, ref)
	if err != nil {
		return ForkTargetSet{}, err
	}
	return ForkTargets(snapshot.Projection), nil
}

// ForkUnavailableError reports a refused cut together with the reason a surface
// shows. Every refusal keeps its own reason so "still running", "read-only" and
// "no boundary" never collapse into one message.
type ForkUnavailableError struct {
	TurnID string
	Reason ForkAvailability
}

func (e *ForkUnavailableError) Error() string {
	return fmt.Sprintf("session: turn %q cannot start a fork (%s)", e.TurnID, e.Reason)
}

// ForkRequest identifies one create-a-child-session request. The cut is always
// resolved by the host from persisted turn records: a client never supplies a
// sequence, an array index, or a checkpoint number.
type ForkRequest struct {
	Source SessionRef
	// TurnID is the stable identity of the completed turn to cut after.
	TurnID string
	// ChildID is optional; the host mints one when empty.
	ChildID string
	// OperationID identifies this creation request. A retried submission with
	// the same operation id addresses the same child instead of minting a
	// second fork of one turn.
	OperationID string
}

// ForkResult reports the created child and the turn it was cut at.
type ForkResult struct {
	Child SessionRef
	Turn  ForkTarget
}

// CreateFork publishes an independent child session from one completed turn of
// the source. It never switches, closes, or writes to the source: a running
// parent keeps running, and a parent owned by another process keeps its lease.
func (s *Service) CreateFork(ctx context.Context, request ForkRequest) (ForkResult, error) {
	if s == nil {
		return ForkResult{}, fmt.Errorf("session: nil service")
	}
	if err := request.Source.validate(s.hostID); err != nil {
		return ForkResult{}, err
	}
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return ForkResult{}, errors.New("session: persistence does not support filesystem fork")
	}
	snapshot, err := s.query.Snapshot(ctx, request.Source)
	if err != nil {
		return ForkResult{}, err
	}
	sequence, availability, err := ForkSequence(snapshot.Projection, request.TurnID)
	if err != nil {
		return ForkResult{}, err
	}
	if availability != ForkAvailable {
		return ForkResult{}, &ForkUnavailableError{TurnID: request.TurnID, Reason: availability}
	}
	target, ok := forkTargetByID(snapshot.Projection, request.TurnID)
	if !ok {
		return ForkResult{}, &ForkUnavailableError{TurnID: request.TurnID, Reason: ForkHistoryUnverifiable}
	}
	childID := strings.TrimSpace(request.ChildID)
	if childID == "" {
		if operation := strings.TrimSpace(request.OperationID); operation != "" {
			childID = deterministicID("fork\x00" + request.Source.SessionID + "\x00" + request.TurnID + "\x00" + operation)
		} else {
			childID = randomID()
		}
	}
	if err := validateSessionID(childID); err != nil {
		return ForkResult{}, err
	}
	parent, closeParent, err := s.forkSource(ctx, request.Source)
	if err != nil {
		return ForkResult{}, err
	}
	defer closeParent()
	childRef := SessionRef{HostID: s.hostID, SessionID: childID}
	childDir := filepath.Join(filesystem.Root, childID)
	parentDir := parent.dir()
	if manifest, readErr := readStoredManifest(filepath.Join(childDir, "manifest.json")); readErr == nil {
		// A retried request reuses the child it already published. A different
		// session that merely holds this identity is a real conflict.
		if manifest.InheritedEvents == sequence && manifest.Source != nil &&
			filepath.Clean(manifest.Source.Path) == filepath.Clean(parentDir) {
			return ForkResult{Child: childRef, Turn: target}, nil
		}
		return ForkResult{}, fmt.Errorf("session: child session %q already exists", childID)
	} else if !os.IsNotExist(readErr) {
		return ForkResult{}, readErr
	}
	if _, err := parent.Fork(ctx, childDir, childID, sequence); err != nil {
		// Both refusals mean the boundary this target advertised cannot carry a
		// safe child. Each keeps its own reason so the surface says which one.
		switch {
		case errors.Is(err, ErrForkActiveAuthority):
			return ForkResult{}, &ForkUnavailableError{TurnID: request.TurnID, Reason: ForkActiveAuthority}
		case errors.Is(err, ErrForkBoundaryNotAtomic):
			return ForkResult{}, &ForkUnavailableError{TurnID: request.TurnID, Reason: ForkHistoryUnverifiable}
		default:
			return ForkResult{}, err
		}
	}
	return ForkResult{Child: childRef, Turn: target}, nil
}

// forkSource returns the session to read the durable prefix from, plus its
// release. A live source is used as-is so its accepted tail is flushed first; a
// source with no runtime in this process is opened read-only, which neither
// restores the parent agent nor disturbs another process's lease.
func (s *Service) forkSource(ctx context.Context, ref SessionRef) (*Session, func(), error) {
	if runtime, ok := s.Runtime(ref); ok {
		return runtime.session, func() {}, nil
	}
	parent, err := s.persistence.Open(ref.SessionID, ReadOnly)
	if err != nil {
		return nil, nil, err
	}
	return parent, func() { _ = parent.Close(context.WithoutCancel(ctx)) }, nil
}

func forkTargetByID(projection Projection, turnID string) (ForkTarget, bool) {
	turnID = strings.TrimSpace(turnID)
	for _, target := range ForkTargets(projection).Targets {
		if target.TurnID == turnID {
			return target, true
		}
	}
	return ForkTarget{}, false
}
