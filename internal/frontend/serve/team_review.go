package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/base/fileutil"
	"reasonix/internal/contract/provider"
	"reasonix/internal/session/team"
	"reasonix/internal/state/memory"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/teamroster"
)

// A review waits this long after a seat's turn ends, so a turn that starts
// again at once is not reviewed half-done.
const (
	teamReviewDelay   = 2 * time.Second
	teamReviewTimeout = 2 * time.Minute
	reviewCursorFile  = "reviewed.json"
)

// teamReviews folds finished turns into memory, one review at a time: an
// agent's own conversation into its own memory, a group's log into the group's.
// Each line is read once; where reading stopped is kept beside the memories.
type teamReviews struct {
	t      *hubTeam
	delay  time.Duration
	ask    team.Reviewer
	mu     sync.Mutex
	timers map[team.SeatRef]*time.Timer
	work   chan team.SeatRef
}

func newTeamReviews(t *hubTeam) *teamReviews {
	r := &teamReviews{t: t, delay: teamReviewDelay, timers: map[team.SeatRef]*time.Timer{}, work: make(chan team.SeatRef, 64)}
	go r.loop()
	return r
}

func (r *teamReviews) loop() {
	for {
		select {
		case <-r.t.stop:
			return
		case seat := <-r.work:
			r.review(seat)
		}
	}
}

// schedule reviews seat once it has been still for the delay.
func (r *teamReviews) schedule(seat team.SeatRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.timers[seat]; old != nil {
		old.Stop()
	}
	r.timers[seat] = time.AfterFunc(r.delay, func() {
		r.mu.Lock()
		delete(r.timers, seat)
		r.mu.Unlock()
		select {
		case r.work <- seat:
		default:
		}
	})
}

// cancel drops a waiting review for a seat whose next turn has begun.
func (r *teamReviews) cancel(seat team.SeatRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.timers[seat]; old != nil {
		old.Stop()
		delete(r.timers, seat)
	}
}

// reviewCursor is how far each conversation and group log has been reviewed.
type reviewCursor struct {
	Sessions map[string]int   `json:"sessions"`
	Groups   map[string]int64 `json:"groups"`
}

func (r *teamReviews) cursorPath() string {
	return filepath.Join(r.t.opts.Dir, "memory", reviewCursorFile)
}

func (r *teamReviews) loadCursor() reviewCursor {
	c := reviewCursor{Sessions: map[string]int{}, Groups: map[string]int64{}}
	data, err := os.ReadFile(r.cursorPath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("serve: read the memory review cursor", "err", err)
		}
		return c
	}
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Warn("serve: read the memory review cursor", "err", err)
	}
	if c.Sessions == nil {
		c.Sessions = map[string]int{}
	}
	if c.Groups == nil {
		c.Groups = map[string]int64{}
	}
	return c
}

func (r *teamReviews) saveCursor(c reviewCursor) {
	data, _ := json.Marshal(c)
	if err := os.MkdirAll(filepath.Dir(r.cursorPath()), 0o700); err == nil {
		err = fileutil.AtomicWriteFile(r.cursorPath(), data, 0o600)
		if err == nil {
			return
		}
	}
	slog.Warn("serve: save the memory review cursor")
}

func (r *teamReviews) review(seat team.SeatRef) {
	if r.t.service() == nil {
		return
	}
	roster, err := r.t.store.Load()
	if err != nil || roster.Prefs.MemoryReviewOff {
		return
	}
	ask := r.ask
	if ask == nil {
		if ask = memoryReviewer(seat.Agent); ask == nil {
			return
		}
	}
	cursor := r.loadCursor()
	lines, store, advance, ok := r.pending(roster, seat, cursor)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), teamReviewTimeout)
	defer cancel()
	report, err := team.ReviewMemory(ctx, store, lines, ask)
	if err != nil {
		slog.Warn("serve: review an agent's memory", "agent", seat.Agent, "group", seat.Group, "err", err)
		return
	}
	advance()
	r.saveCursor(cursor)
	if report != (team.ReviewReport{}) {
		r.t.changed()
	}
}

// pending is what seat has said since it was last reviewed, the memory it
// folds into, and how to mark it read once the review lands.
func (r *teamReviews) pending(roster teamroster.Roster, seat team.SeatRef, cursor reviewCursor) ([]team.ReviewLine, memory.Store, func(), bool) {
	if seat.Group != "" {
		from := cursor.Groups[seat.Group]
		log, err := r.t.store.Lines(seat.Group, from, 0)
		if err != nil || len(log) == 0 {
			return nil, memory.Store{}, nil, false
		}
		var lines []team.ReviewLine
		for _, l := range log {
			if l.Kind == teamroster.LinePost {
				lines = append(lines, team.ReviewLine{From: speakerName(roster, l.From), Text: l.Text, User: l.From == teamroster.ByUser})
			}
		}
		last := log[len(log)-1].Seq
		return lines, team.SharedStore(r.t.opts.Dir, team.TierGroup, seat.Group), func() { cursor.Groups[seat.Group] = last }, true
	}
	rt, ok := r.t.liveRuntime(seat)
	if !ok {
		return nil, memory.Store{}, nil, false
	}
	ctrl := rt.Server.Controller()
	history, path := ctrl.History(), ctrl.SessionPath()
	from := cursor.Sessions[path]
	if from > len(history) {
		from = 0
	}
	name := speakerName(roster, seat.Agent)
	var lines []team.ReviewLine
	for _, m := range history[from:] {
		switch {
		case m.Role == provider.RoleUser && m.HostAuthored:
			lines = append(lines, team.ReviewLine{From: "team", Text: sessionstore.UserMessageText(m)})
		case m.Role == provider.RoleUser:
			lines = append(lines, team.ReviewLine{From: "user", Text: sessionstore.UserMessageText(m), User: true})
		case m.Role == provider.RoleAssistant && m.Content != "":
			lines = append(lines, team.ReviewLine{From: name, Text: m.Content})
		}
	}
	end := len(history)
	return lines, memory.Store{Dir: team.AgentMemoryDir(r.t.opts.Dir, seat.Agent)}, func() { cursor.Sessions[path] = end }, true
}

func speakerName(r teamroster.Roster, id string) string {
	if id == teamroster.ByUser {
		return "user"
	}
	if a, ok := r.Agent(id); ok {
		return a.Name
	}
	return "agent"
}
