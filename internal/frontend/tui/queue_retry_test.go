package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// heldInbox is an inbox whose one entry the kernel marked uncertain, with the
// code that says whether its turn may have landed.
func heldInbox(t *testing.T, code string) (*model, *inboxKernel) {
	t.Helper()
	m, k := inboxModel(t, "recovered")
	k.mu.Lock()
	k.items[0].State = "uncertain"
	k.items[0].BlockCode = code
	k.items[0].BlockReason = "in-flight owner is no longer active"
	k.mu.Unlock()
	run(m, m.fetchQueue())
	return m, k
}

// A kill between the turn's marker clear and the queue's ack leaves the entry
// uncertain under owner_inactive, and its reply is already in the transcript.
// Retrying it would send the prompt a second time, so the key must refuse and
// say why instead of reaching the kernel.
func TestRetryRefusesAnEntryThatMayAlreadyHaveRun(t *testing.T) {
	m, k := heldInbox(t, "owner_inactive")
	run(m, keyUp(m))
	run(m, letter(m, 'r'))
	if k.did("POST /inbox/items/q-a/retry") {
		t.Fatalf("the retry key re-sent an entry whose turn already landed: %v", k.seen())
	}
	want := "not retrying #q-a: the session that was running it is gone — " +
		"it may already have run; check the transcript, or delete it"
	if got := queueLastNotice(m); got != want {
		t.Fatalf("refusal = %q, want %q", got, want)
	}
}

// The desktop retries an uncertain entry only for steer_unapplied, the one code
// that cannot have reached the model; a queued entry is not held at all and the
// key keeps its old behaviour.
func TestRetryStillRunsAnEntryThatCannotHaveRun(t *testing.T) {
	t.Run("steer_unapplied", func(t *testing.T) {
		m, k := heldInbox(t, "steer_unapplied")
		run(m, keyUp(m))
		run(m, letter(m, 'r'))
		if !k.did("POST /inbox/items/q-a/retry") {
			t.Fatalf("a retry-safe uncertain entry was not retried: %v", k.seen())
		}
		if got := queueLastNotice(m); got != "retry queued #q-a" {
			t.Fatalf("notice = %q", got)
		}
	})
	t.Run("queued", func(t *testing.T) {
		m, k := inboxModel(t, "queued")
		run(m, keyUp(m))
		run(m, letter(m, 'r'))
		if !k.did("POST /inbox/items/q-a/retry") {
			t.Fatalf("the retry key stopped reaching the kernel for a queued entry: %v", k.seen())
		}
	})
}

// The command layer is the other way the surface sends a retry; it reads the
// same entry and refuses with the same words.
func TestQueueRetryCommandRefusesAnEntryThatMayAlreadyHaveRun(t *testing.T) {
	m, k := queueModel(t, map[string]any{
		"GET /inbox": map[string]any{"revision": 1, "items": []map[string]any{
			{"id": "abcdef1234567890", "intent": "followup", "state": "uncertain", "preview": "p",
				"blockCode": "owner_inactive", "blockReason": "in-flight owner is no longer active"},
		}},
	})
	queueSend(m, "/queue retry 1")
	if strings.Contains(k.seen(), "/retry") {
		t.Fatalf("/queue retry re-sent an entry that may already have run:\n%s", k.seen())
	}
	want := "not retrying #abcdef12: the session that was running it is gone — " +
		"it may already have run; check the transcript, or delete it"
	if got := queueLastNotice(m); got != want {
		t.Fatalf("refusal = %q, want %q", got, want)
	}
}

// The rule reads blockCode, so the kernel's fields have to survive the wire
// decode: without them every held entry looks like it has no code.
func TestInboxDecodeCarriesTheBlockCodeAndReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"revision":7,"paused":true,"items":[`+
			`{"id":"rec1","intent":"followup","state":"uncertain","preview":"p",`+
			`"blockCode":"owner_inactive","blockReason":"in-flight owner is no longer active"},`+
			`{"id":"rec2","intent":"followup","state":"queued","preview":"q"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := &Client{HTTP: srv.Client(), Base: srv.URL}
	snap, err := c.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(snap.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(snap.Items))
	}
	if got := snap.Items[0]; got.State != "uncertain" || got.BlockCode != "owner_inactive" ||
		got.BlockReason != "in-flight owner is no longer active" {
		t.Fatalf("held item = %+v, want state/code/reason decoded", got)
	}
	if got := snap.Items[1]; got.BlockCode != "" || got.BlockReason != "" {
		t.Fatalf("queued item = %+v, want no block code or reason", got)
	}
}
