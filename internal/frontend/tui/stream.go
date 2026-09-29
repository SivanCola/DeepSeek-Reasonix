package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/contract/eventwire"
)

// Update is what the stream hands the view: frames in order, or word that the
// frames between two points are gone and the transcript has to be read back
// from /history.
type Update struct {
	Event eventwire.Event
	Gap   bool
}

const reconnectDelay = 500 * time.Millisecond

// Subscribe follows /events until ctx ends. Numbered frames arrive exactly
// once and in order: a jump is filled from /events/replay before the frame
// that revealed it, and what the replay no longer holds is reported as a Gap.
// Unnumbered frames (streaming deltas) are delivered as they come.
func (c *Client) Subscribe(ctx context.Context) <-chan Update {
	out := make(chan Update, 256)
	start := &streamStart{ctx: ctx, ready: make(chan struct{})}
	c.streamStart.Store(start)
	s := &subscription{c: c, out: out, start: start}
	go func() {
		defer close(out)
		for ctx.Err() == nil {
			s.follow(ctx)
			select {
			case <-ctx.Done():
			case <-time.After(reconnectDelay):
			}
		}
	}()
	return out
}

// Mutations can emit transient notices immediately. Wait for the server to
// register the first subscription, not merely for its goroutine to start.
type streamStart struct {
	ctx   context.Context
	ready chan struct{}
	once  sync.Once
}

func (c *Client) awaitStream(ctx context.Context) error {
	start := c.streamStart.Load()
	if start == nil {
		return nil
	}
	select {
	case <-start.ready:
		return nil
	case <-start.ctx.Done():
		return start.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type subscription struct {
	c     *Client
	out   chan<- Update
	start *streamStart
	seen  int64
	// known is false until the stream states a position: 0 cannot tell a
	// subscriber that just attached from one that has seen the stream start.
	known bool
}

func (s *subscription) follow(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.c.Base+"/events", nil)
	if err != nil {
		return
	}
	if s.known && s.seen > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(s.seen, 10))
	}
	resp, err := s.c.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	if s.start != nil {
		s.start.once.Do(func() { close(s.start.ready) })
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev eventwire.Event
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		s.accept(ctx, ev)
	}
	// A read error ends this connection like EOF does; Subscribe reconnects
	// from the last frame it delivered either way.
	_ = sc.Err()
}

func (s *subscription) accept(ctx context.Context, ev eventwire.Event) {
	switch ev.Kind {
	case "stream_watermark":
		// The first one states where this subscriber attached; nothing before
		// it was sent to this subscriber, so nothing before it was lost.
		if !s.known {
			s.seen, s.known = ev.Seq, true
			return
		}
		if ev.Seq > s.seen {
			s.recover(ctx, s.seen)
		}
		return
	case "stream_gap":
		s.seen, s.known = max(s.seen, ev.Seq), true
		s.emit(ctx, Update{Gap: true})
		return
	}
	switch {
	case ev.Seq > 0 && s.known && ev.Seq < s.seen:
		// Numbering that goes backwards is a restarted stream counting from one,
		// never a replay: a resumed client is not sent a lower number than it holds.
		s.seen = ev.Seq
		s.emit(ctx, Update{Gap: true})
		s.emit(ctx, Update{Event: ev})
		return
	case ev.Seq > 0 && s.known && ev.Seq > s.seen+1:
		s.recover(ctx, s.seen)
	}
	s.deliver(ctx, ev)
}

func (s *subscription) deliver(ctx context.Context, ev eventwire.Event) {
	if ev.Seq > 0 {
		if s.known && ev.Seq <= s.seen {
			return
		}
		s.seen, s.known = ev.Seq, true
	}
	s.emit(ctx, Update{Event: ev})
}

// recover fetches what the stream shed after `after`. Frames the replay log no
// longer holds are gone for good, and only the transcript can answer for them.
func (s *subscription) recover(ctx context.Context, after int64) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.c.Base+"/events/replay?lastEventId="+strconv.FormatInt(after, 10), nil)
	if err != nil {
		return
	}
	resp, err := s.c.HTTP.Do(req)
	if err != nil {
		s.emit(ctx, Update{Gap: true})
		return
	}
	defer resp.Body.Close()
	var body struct {
		Frames   []json.RawMessage `json:"frames"`
		Complete bool              `json:"complete"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		s.emit(ctx, Update{Gap: true})
		return
	}
	for _, raw := range body.Frames {
		var ev eventwire.Event
		if json.Unmarshal(raw, &ev) == nil {
			s.deliver(ctx, ev)
		}
	}
	if !body.Complete {
		s.emit(ctx, Update{Gap: true})
	}
}

func (s *subscription) emit(ctx context.Context, u Update) {
	select {
	case s.out <- u:
	case <-ctx.Done():
	}
}
