package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TurnRequest is the host-independent unit accepted by SessionBroker. A lane
// is keyed by the stable remote conversation key; Run is never concurrent
// with another turn on that lane.
type TurnRequest struct {
	ID         string
	SessionKey string
	Run        func(context.Context) error
	// OnAccepted runs after this request owns its lane and before Run.
	OnAccepted func()
}

type TurnReceipt struct {
	ID         string
	SessionKey string
	AcceptedAt time.Time
	Accepted   <-chan struct{}
	Done       <-chan error
}

type SessionBroker struct {
	mu    sync.Mutex
	lanes map[string]*sessionLane
	turns map[string]*brokerTurn
}

type sessionLane struct {
	token chan struct{}
	refs  int
}

type brokerTurn struct{ done chan error }

func NewSessionBroker() *SessionBroker {
	return &SessionBroker{lanes: make(map[string]*sessionLane), turns: make(map[string]*brokerTurn)}
}

// SubmitTurn is the compatibility blocking API used by existing adapters.
func (b *SessionBroker) SubmitTurn(ctx context.Context, req TurnRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	receipt, err := b.SubmitTurnAsync(ctx, req)
	if err != nil {
		return err
	}
	// The callback owns the controller's cleanup boundary. Waiting for Done
	// even after ctx cancellation prevents SubmitTurn from returning while the
	// broker goroutine is still executing the Controller turn.
	return <-receipt.Done
}

// SubmitTurnAsync admits a turn and starts it on its conversation lane.
// Duplicate IDs share the original completion channel, so a replayed provider
// event cannot execute a second user turn.
func (b *SessionBroker) SubmitTurnAsync(ctx context.Context, req TurnRequest) (TurnReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Run == nil {
		return TurnReceipt{}, fmt.Errorf("session broker turn callback is nil")
	}
	if b == nil {
		accepted := make(chan struct{})
		close(accepted)
		done := make(chan error, 1)
		go func() { done <- req.Run(ctx); close(done) }()
		return TurnReceipt{ID: req.ID, SessionKey: req.SessionKey, AcceptedAt: time.Now(), Accepted: accepted, Done: done}, nil
	}
	key := strings.TrimSpace(req.SessionKey)
	if key == "" {
		return TurnReceipt{}, fmt.Errorf("session broker session key is empty")
	}
	id := strings.TrimSpace(req.ID)
	b.mu.Lock()
	if id != "" {
		if existing := b.turns[id]; existing != nil {
			b.mu.Unlock()
			accepted := make(chan struct{})
			close(accepted)
			return TurnReceipt{ID: id, SessionKey: key, AcceptedAt: time.Now(), Accepted: accepted, Done: existing.done}, nil
		}
	}
	lane := b.lanes[key]
	if lane == nil {
		lane = &sessionLane{token: make(chan struct{}, 1)}
		b.lanes[key] = lane
	}
	lane.refs++
	turn := &brokerTurn{done: make(chan error, 1)}
	if id != "" {
		b.turns[id] = turn
	}
	b.mu.Unlock()

	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		// If the lane is already free, always admit it. This preserves the
		// shutdown invariant that a turn which raced controller teardown still
		// enters Run and observes cancellation itself.
		select {
		case lane.token <- struct{}{}:
		default:
			select {
			case lane.token <- struct{}{}:
			case <-ctx.Done():
				b.release(key, lane, id)
				turn.done <- ctx.Err()
				close(turn.done)
				return
			}
		}
		if req.OnAccepted != nil {
			req.OnAccepted()
		}
		err := req.Run(ctx)
		<-lane.token
		b.release(key, lane, id)
		turn.done <- err
		close(turn.done)
	}()
	return TurnReceipt{ID: id, SessionKey: key, AcceptedAt: time.Now(), Accepted: accepted, Done: turn.done}, nil
}

func (b *SessionBroker) release(key string, lane *sessionLane, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id != "" && b.turns[id] != nil {
		delete(b.turns, id)
	}
	lane.refs--
	if lane.refs <= 0 && b.lanes[key] == lane {
		delete(b.lanes, key)
	}
}
