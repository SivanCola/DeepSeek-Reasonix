package broker

import (
	"testing"
	"time"

	"reasonix/internal/provider"
)

func TestHostOutputBackpressureDoesNotBlockDetach(t *testing.T) {
	h := NewHost()
	h.generation = 1
	stream := &hostStream{
		generation:   1,
		out:          make(chan provider.Chunk, 1),
		done:         make(chan struct{}),
		deliveryWake: make(chan struct{}, 1),
		nextSeq:      1,
		pending: map[int64]provider.Chunk{
			1: {Type: provider.ChunkText, Text: "one"},
			2: {Type: provider.ChunkText, Text: "two"},
		},
	}
	h.streams["backpressure"] = stream
	go h.deliverStream(stream)

	h.mu.Lock()
	h.flushLocked("backpressure", stream)
	h.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for len(stream.out) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(stream.out) != 1 {
		t.Fatal("stream never filled its output buffer")
	}

	detached := make(chan struct{})
	go func() {
		h.Detach(1)
		close(detached)
	}()
	select {
	case <-detached:
	case <-time.After(time.Second):
		t.Fatal("Detach blocked behind a slow stream consumer")
	}

	var chunks []provider.Chunk
	for chunk := range stream.out {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 3 || chunks[0].Text != "one" || chunks[1].Text != "two" || chunks[2].Err == nil {
		t.Fatalf("delivered chunks = %#v, want ordered text followed by disconnect error", chunks)
	}
}

func TestHostDeliveryQueueOverflowTerminatesOnlyThatStream(t *testing.T) {
	h := NewHost()
	h.generation = 1
	stream := &hostStream{
		generation:   1,
		out:          make(chan provider.Chunk, 1),
		done:         make(chan struct{}),
		deliveryWake: make(chan struct{}, 1),
		nextSeq:      1,
		pending:      make(map[int64]provider.Chunk),
		delivery:     make([]provider.Chunk, hostDeliveryQueueLimit-1),
	}
	h.streams["overflow"] = stream

	h.mu.Lock()
	stream.pending[1] = provider.Chunk{Type: provider.ChunkText, Text: "overflow"}
	h.flushLocked("overflow", stream)
	_, stillRegistered := h.streams["overflow"]
	final := stream.deliveryFinal
	queued := append([]provider.Chunk(nil), stream.delivery...)
	h.mu.Unlock()

	if stillRegistered || !final {
		t.Fatal("overflowing stream was not terminated")
	}
	if len(queued) != hostDeliveryQueueLimit || queued[len(queued)-1].Err == nil {
		t.Fatalf("overflow queue length/error = %d/%v", len(queued), queued[len(queued)-1].Err)
	}
}
