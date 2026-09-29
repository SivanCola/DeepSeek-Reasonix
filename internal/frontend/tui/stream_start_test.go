package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutationsWaitForInitialEventSubscription(t *testing.T) {
	for _, shell := range []bool{false, true} {
		t.Run(fmt.Sprint(shell), func(t *testing.T) {
			attached := make(chan struct{})
			release := make(chan struct{})
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/events" {
					close(attached)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, ": connected\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				posts.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := &Client{HTTP: srv.Client(), Base: srv.URL}
			c.Subscribe(ctx)
			<-attached
			submit := c.Submit
			if shell {
				submit = c.RunShell
			}
			blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			defer stop()
			if err := submit(blocked, "fixture"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("write before subscription = %v, want deadline", err)
			}
			if posts.Load() != 0 {
				t.Fatal("mutation overtook subscription")
			}
			close(release)
			if err := submit(ctx, "fixture"); err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 1 {
				t.Fatalf("posts = %d", posts.Load())
			}
		})
	}
}

func TestCancelledSubscriptionReleasesWaitingMutations(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := &Client{HTTP: http.DefaultClient, Base: "http://fixture.invalid"}
	c.Subscribe(ctx)
	if err := c.Submit(t.Context(), "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled subscription = %v", err)
	}
}
