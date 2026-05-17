package docker

import (
	"testing"
	"time"
)

func TestCacheInvalidate(t *testing.T) {
	var c cache[int]
	c.set(42, time.Minute)
	if v, ok := c.get(); !ok || v != 42 {
		t.Fatalf("get after set: got (%d, %v), want (42, true)", v, ok)
	}
	c.invalidate()
	if _, ok := c.get(); ok {
		t.Fatal("get after invalidate should miss")
	}
}

func TestCacheExpiry(t *testing.T) {
	var c cache[string]
	c.set("hello", 10*time.Millisecond)
	if _, ok := c.get(); !ok {
		t.Fatal("get within TTL should hit")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.get(); ok {
		t.Fatal("get after TTL should miss")
	}
}
