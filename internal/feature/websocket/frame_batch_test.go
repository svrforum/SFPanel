package websocket

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"
)

// A burst of lines must arrive as a few frames, not one per line, with the
// bytes and their order untouched.
func TestBatchFramesCoalescesABurst(t *testing.T) {
	lines := make(chan []byte, 256)
	var frames [][]byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		batchFrames(context.Background(), lines, func(f []byte) bool {
			frames = append(frames, append([]byte(nil), f...))
			return true
		})
	}()
	var want bytes.Buffer
	for i := 0; i < 5000; i++ {
		l := []byte(fmt.Sprintf("2026-09-03 line %04d some log text here\n", i))
		want.Write(l)
		lines <- l
	}
	close(lines)
	<-done

	var got bytes.Buffer
	for _, f := range frames {
		got.Write(f)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("bytes differ: got %d, want %d", got.Len(), want.Len())
	}
	// 5000 × ~40 B ≈ 200 KB → about 13 frames of 16 KB. Anything near 5000
	// means the coalescing is gone.
	if len(frames) > 100 {
		t.Errorf("5000 lines became %d frames, want a few dozen", len(frames))
	}
	for _, f := range frames {
		if f[len(f)-1] != '\n' {
			t.Errorf("frame does not end on a line boundary: %q", f[len(f)-20:])
		}
	}
}

// A lone line on a quiet stream must not wait for a frame to fill.
func TestBatchFramesFlushesASingleLineQuickly(t *testing.T) {
	lines := make(chan []byte, 1)
	got := make(chan []byte, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go batchFrames(ctx, lines, func(f []byte) bool {
		got <- append([]byte(nil), f...)
		return true
	})
	start := time.Now()
	lines <- []byte("one line\n")
	select {
	case f := <-got:
		if string(f) != "one line\n" {
			t.Errorf("frame = %q", f)
		}
		if d := time.Since(start); d > 10*frameBatchDelay {
			t.Errorf("single line took %v to flush, want about %v", d, frameBatchDelay)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the line never arrived")
	}
}

// Closing the source flushes what is pending; a refused write stops the loop.
func TestBatchFramesFlushesOnCloseAndStopsOnWriteFailure(t *testing.T) {
	lines := make(chan []byte, 4)
	var frames int
	done := make(chan struct{})
	go func() {
		defer close(done)
		batchFrames(context.Background(), lines, func([]byte) bool { frames++; return false })
	}()
	lines <- []byte("a\n")
	close(lines)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("batcher did not stop after the write was refused")
	}
	if frames != 1 {
		t.Errorf("frames = %d, want exactly 1 (the pending flush on close)", frames)
	}
}
