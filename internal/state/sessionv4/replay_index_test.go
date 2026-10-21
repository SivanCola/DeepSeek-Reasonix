package sessionv4

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/state/sessionv4/v4fixture"
)

func TestReplayIdentityOrdering(t *testing.T) {
	for _, reset := range []string{"history/replace", "legacy/import"} {
		t.Run(reset, func(t *testing.T) {
			f := v4fixture.New(t)
			dir := f.Session("0123456789abcdef0123456789abcdef", 3)
			msg := func(id, content string) any { return v4fixture.Msg(id, "user", content)["message"] }
			f.Batch(dir, v4fixture.Ended,
				v4fixture.Event{Kind: reset, Payload: map[string]any{"messages": []any{msg("a", "first"), msg("a", "duplicate"), msg("", "empty1"), msg("", "empty2")}}},
				v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "ignored")},
				v4fixture.Event{Kind: "message/upsert", Payload: v4fixture.Msg("a", "user", "updated")},
				v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("b", "user", "second")},
				v4fixture.Event{Kind: "message/retract", Payload: map[string]any{"messageIds": []string{"a", "", "absent"}}},
				v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "readded")},
				v4fixture.Event{Kind: "message/upsert", Payload: v4fixture.Msg("", "user", "empty3")},
			)
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Transcript()
			if err != nil {
				t.Fatal(err)
			}
			var contents []string
			for _, m := range got.Messages {
				contents = append(contents, m.Content)
			}
			if !reflect.DeepEqual(contents, []string{"second", "readded", "empty3"}) {
				t.Fatalf("contents = %v", contents)
			}
		})
	}
}

func TestTranscriptContextCancellation(t *testing.T) {
	f := v4fixture.New(t)
	dir := f.Session("0123456789abcdef0123456789abcdef", 3)
	f.Batch(dir, v4fixture.Ended, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "hello")})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.TranscriptContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestReplayScalingMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("performance fixture")
	}
	for _, n := range []int{4000, 8000, 16000} {
		f := v4fixture.New(t)
		dir := f.Session("0123456789abcdef0123456789abcdef", 3)
		events := make([]v4fixture.Event, n)
		for i := range events {
			events[i] = v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg(fmt.Sprintf("message-%08d", i), "user", "short body")}
		}
		f.Batch(dir, v4fixture.Ended, events...)
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		got, err := s.Transcript()
		if err != nil || len(got.Messages) != n {
			t.Fatalf("messages=%d err=%v", len(got.Messages), err)
		}
		t.Logf("messages=%d replay=%v", n, time.Since(start))
	}
}
