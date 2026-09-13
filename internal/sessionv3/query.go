package sessionv3

import (
	"context"
	"fmt"

	"reasonix/internal/provider"
)

// Query is the read model shared by desktop, CLI, Serve, ACP and bots. It uses
// an attached runtime when one exists and otherwise opens only a read handle;
// querying cold history never constructs an Agent or acquires writer ownership.
type Query struct {
	hostID      string
	persistence SessionPersistence
	service     *Service
}

func (s *Service) Query() *Query {
	if s == nil {
		return nil
	}
	return &Query{hostID: s.hostID, persistence: s.persistence, service: s}
}

func (q *Query) Snapshot(ctx context.Context, ref SessionRef) (Snapshot, error) {
	if q == nil || q.persistence == nil {
		return Snapshot{}, fmt.Errorf("sessionv3: nil session query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return Snapshot{}, err
	}
	if q.service != nil {
		if runtime, ok := q.service.Runtime(ref); ok {
			return runtime.Session().Snapshot(), nil
		}
	}
	handle, err := q.persistence.Open(ref.SessionID, ReadOnly)
	if err != nil {
		return Snapshot{}, err
	}
	defer handle.Close(context.Background())
	commits := []Commit{}
	var cursor uint64
	for {
		page, readErr := handle.Read(ctx, cursor, 1000)
		if readErr != nil {
			return Snapshot{}, readErr
		}
		commits = append(commits, page.Commits...)
		if !page.Truncated {
			break
		}
		if page.Next <= cursor {
			return Snapshot{}, fmt.Errorf("%w: cold history cursor did not advance", ErrDamagedStore)
		}
		cursor = page.Next
	}
	projection, err := Project(commits)
	if err != nil {
		return Snapshot{}, err
	}
	sequence := projection.CommittedSequence
	return Snapshot{EventSequence: sequence, DurableSequence: sequence, PersistenceStatus: PersistenceReady, Projection: projection}, nil
}

func (q *Query) History(ctx context.Context, ref SessionRef) ([]provider.Message, error) {
	snapshot, err := q.Snapshot(ctx, ref)
	if err != nil {
		return nil, err
	}
	return append([]provider.Message(nil), snapshot.Projection.Messages...), nil
}

func (q *Query) List(cursor string, limit int) (SessionPage, error) {
	if q == nil || q.persistence == nil {
		return SessionPage{}, fmt.Errorf("sessionv3: nil session query")
	}
	page, err := q.persistence.List(cursor, limit)
	if err != nil {
		return SessionPage{}, err
	}
	for i := range page.Sessions {
		info := &page.Sessions[i]
		info.Ref = SessionRef{HostID: q.hostID, SessionID: info.SessionID}
		if info.Error != "" {
			continue
		}
		snapshot, snapshotErr := q.Snapshot(context.Background(), info.Ref)
		if snapshotErr != nil {
			info.Error = snapshotErr.Error()
			continue
		}
		info.Title = snapshot.Projection.Title
		info.ModelRef = snapshot.Projection.ModelRef
		info.ModelIdentity = snapshot.Projection.ModelIdentity
		for _, turn := range snapshot.Projection.Turns {
			if turn.EndSequence != 0 {
				info.Turns++
			}
		}
	}
	return page, nil
}
