// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package respcache

import (
	"bytes"
	"testing"
	"time"
)

func TestSetGetHit(t *testing.T) {
	c := New(4, time.Minute)
	e := &Entry{StatusCode: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`), CreatedAt: time.Now()}
	c.Set("k1", e)
	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected hit after Set")
	}
	if got.StatusCode != 200 || !bytes.Equal(got.Body, []byte(`{"ok":true}`)) {
		t.Fatalf("entry mismatch: %+v", got)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestGetMiss(t *testing.T) {
	c := New(4, time.Minute)
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss for unknown key")
	}
}

func TestTTLExpiryLazy(t *testing.T) {
	c := New(4, 50*time.Millisecond)
	c.Set("k1", &Entry{StatusCode: 200, CreatedAt: time.Now()})
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("k1"); ok {
		t.Fatal("expected miss after TTL expiry")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d after expiry, want 0 (lazy eviction)", c.Len())
	}
}

func TestTTLNotExpired(t *testing.T) {
	c := New(4, time.Minute)
	c.Set("k1", &Entry{StatusCode: 200, CreatedAt: time.Now()})
	if _, ok := c.Get("k1"); !ok {
		t.Fatal("expected hit before TTL")
	}
}

func TestLRUEviction(t *testing.T) {
	c := New(2, time.Minute)
	c.Set("a", &Entry{StatusCode: 200, CreatedAt: time.Now()})
	c.Set("b", &Entry{StatusCode: 200, CreatedAt: time.Now()})
	// tocar "a" → "b" queda LRU
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should be present")
	}
	c.Set("c", &Entry{StatusCode: 200, CreatedAt: time.Now()})
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted (LRU)")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should survive (MRU)")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

func TestSetReplaceSameKey(t *testing.T) {
	c := New(4, time.Minute)
	c.Set("k1", &Entry{StatusCode: 200, Body: []byte("v1"), CreatedAt: time.Now()})
	c.Set("k1", &Entry{StatusCode: 200, Body: []byte("v2"), CreatedAt: time.Now()})
	got, ok := c.Get("k1")
	if !ok || !bytes.Equal(got.Body, []byte("v2")) {
		t.Fatalf("expected replaced value, got %+v ok=%v", got, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after replace", c.Len())
	}
}

func TestDefaults(t *testing.T) {
	c := New(0, 0)
	if c.maxEntries != 512 {
		t.Fatalf("default maxEntries = %d, want 512", c.maxEntries)
	}
	if c.ttl != 5*time.Minute {
		t.Fatalf("default ttl = %v, want 5m", c.ttl)
	}
}

func TestZeroLen(t *testing.T) {
	c := New(0, time.Minute)
	if c.Len() != 0 {
		t.Fatal("fresh cache should be empty")
	}
}
