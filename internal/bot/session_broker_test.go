package bot

import (
	"context"
	"sync"
	"testing"
)

func TestSessionBrokerSerializesOneConversationLane(t *testing.T) {
	b := NewSessionBroker()
	var mu sync.Mutex
	active, maxActive, completed := 0, 0, 0
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.SubmitTurn(context.Background(), TurnRequest{SessionKey: "qq/group/1", Run: func(context.Context) error {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				<-start
				mu.Lock()
				active--
				completed++
				mu.Unlock()
				return nil
			}}); err != nil {
				t.Errorf("submit turn: %v", err)
			}
		}()
	}
	// Let the first callback enter, then release it; the second can only enter
	// after the first lane token is returned.
	close(start)
	wg.Wait()
	if maxActive != 1 || completed != 2 {
		t.Fatalf("max active=%d completed=%d, want 1/2", maxActive, completed)
	}
	b.mu.Lock()
	remaining := len(b.lanes)
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("session lanes retained after completion: %d", remaining)
	}
}
