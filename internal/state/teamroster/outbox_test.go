package teamroster

import (
	"context"
	"testing"
)

// A message the user sent from a window that does not hold the team waits in
// the outbox until the holder takes it; taking it empties the outbox.
func TestTheOutboxHoldsWhatTheUserSentUntilItIsTaken(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	ctx := context.Background()
	ada := newAgentID()
	if _, err := s.QueueUserMessage(ctx, ada, "write the weekly report"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueUserMessage(ctx, ada, "   "); err == nil {
		t.Fatal("an empty message was queued")
	}
	got, err := s.TakeUserMessages(ctx)
	if err != nil || len(got) != 1 || got[0].To != ada || got[0].Text != "write the weekly report" || got[0].ID == "" {
		t.Fatalf("taken = %+v, %v", got, err)
	}
	if again, _ := s.TakeUserMessages(ctx); len(again) != 0 {
		t.Fatalf("the outbox kept %+v after it was taken", again)
	}
}
