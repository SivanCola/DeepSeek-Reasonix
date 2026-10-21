package sessionstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/state/sessionv4/v4fixture"
)

// A 1.x conversation whose own listing cache is absent is replayed once, not
// on every listing: a folder of thousands re-read them all each time it opened.
func TestV4ConversationsWithoutAListingCacheAreReplayedOnce(t *testing.T) {
	s := v4fixture.New(t)
	sessions := filepath.Join(filepath.Dir(s.Root), "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", 4000)
	for i := range 60 {
		dir := s.Session(fmt.Sprintf("desktop-%024x", i+1), 3)
		var evs []v4fixture.Event
		for j := range 100 {
			evs = append(evs, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg(fmt.Sprintf("u%d", j), "user", body)})
			evs = append(evs, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg(fmt.Sprintf("a%d", j), "assistant", body)})
		}
		s.Batch(dir, v4fixture.Ended, evs...)
	}
	took := func() (time.Duration, int) {
		start := time.Now()
		out, _ := ListSessions(sessions)
		return time.Since(start), len(out)
	}
	first, rows := took()
	second, again := took()
	if rows != 60 || again != 60 {
		t.Fatalf("rows = %d then %d, want 60", rows, again)
	}
	if entries := len(loadV4Listings(sessions).Entries); entries != 60 {
		t.Fatalf("persisted entries = %d", entries)
	}
	t.Logf("cold=%v warm=%v", first, second)
}
