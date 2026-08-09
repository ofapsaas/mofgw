// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package singleflight

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoCoalescesConcurrent(t *testing.T) {
	g := New(0)
	var calls atomic.Int64
	start := make(chan struct{})
	results := make(chan *Result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, shared := g.Do("k", func() *Result {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return &Result{StatusCode: 200, Body: []byte("hello")}
			})
			results <- &Result{StatusCode: r.StatusCode, Body: r.Body}
			_ = shared
		}()
	}
	close(start)
	r1, r2 := <-results, <-results
	if calls.Load() != 1 {
		t.Fatalf("fn calls = %d, want 1 (coalesced)", calls.Load())
	}
	if string(r1.Body) != "hello" || string(r2.Body) != "hello" {
		t.Fatalf("bodies = %q, %q; want both hello", r1.Body, r2.Body)
	}
}

func TestDoDifferentKeysRunSeparately(t *testing.T) {
	g := New(0)
	var calls atomic.Int64
	r1c := make(chan struct{})
	go func() {
		<-r1c
		g.Do("a", func() *Result {
			calls.Add(1)
			time.Sleep(30 * time.Millisecond)
			return &Result{StatusCode: 200}
		})
	}()
	g.Do("b", func() *Result {
		calls.Add(1)
		return &Result{StatusCode: 200}
	})
	close(r1c)
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("fn calls = %d, want 2 (distinct keys)", calls.Load())
	}
}

func TestSharedFlag(t *testing.T) {
	g := New(0)
	first := make(chan struct{})
	release := make(chan struct{})
	got := make(chan bool, 2)
	// Líder: se bloquea dentro de fn hasta que el follower se una.
	go func() {
		_, shared := g.Do("k", func() *Result {
			close(first)
			<-release
			return &Result{StatusCode: 200}
		})
		got <- shared
	}()
	<-first
	// Follower: se une al vuelo en curso. El Do del follower va en
	// goroutine para NO bloquear el close(release) — el líder lo
	// necesita para completar (si no, deadlock: líder espera release,
	// main espera f.done).
	followerGot := make(chan bool, 1)
	go func() {
		_, shared := g.Do("k", func() *Result { return &Result{StatusCode: 500} })
		followerGot <- shared
	}()
	// Ventana para que el follower se registre, luego libera al líder.
	time.Sleep(100 * time.Millisecond)
	close(release)
	if s := <-followerGot; !s {
		t.Fatal("follower: shared = false, want true")
	}
	if s := <-got; s {
		t.Fatal("leader: shared = true, want false")
	}
}

func TestFlightRemovedAfterDone(t *testing.T) {
	g := New(0)
	var calls atomic.Int64
	g.Do("k", func() *Result { calls.Add(1); return &Result{StatusCode: 200} })
	// Segundo Do con la misma key DESPUÉS de completar → nuevo vuelo.
	g.Do("k", func() *Result { calls.Add(1); return &Result{StatusCode: 200} })
	if calls.Load() != 2 {
		t.Fatalf("fn calls = %d, want 2 (flight removed post-completion)", calls.Load())
	}
}

func TestLeaderPanicDoesNotHangFollowers(t *testing.T) {
	g := New(0)
	leaderStarted := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // líder del vuelo — paniquea dentro de fn
		defer wg.Done()
		g.Do("k", func() *Result {
			close(leaderStarted)
			<-release
			panic("boom")
		})
	}()
	<-leaderStarted
	followerDone := make(chan struct{})
	go func() { // follower: se une al vuelo en curso
		g.Do("k", func() *Result { return &Result{StatusCode: 200} })
		close(followerDone)
	}()
	// Ventana para que el follower se registre como waiter. El contrato
	// bajo test es NO colgarse (gane o pierda la carrera contra el
	// panic) — la aserción de nil-result se omite porque la carrera la
	// hace flaky (si el follower llega post-panic, es líder legítimo).
	time.Sleep(50 * time.Millisecond)
	close(release) // líder paniquea ahora
	leaderDone := make(chan struct{})
	go func() { wg.Wait(); close(leaderDone) }()
	select {
	case <-followerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("follower hung after leader panic")
	}
	select {
	case <-leaderDone:
	case <-time.After(2 * time.Second):
		t.Fatal("leader hung after panic recovery")
	}
}

func TestMaxFlightsBypass(t *testing.T) {
	g := New(1) // tope 1
	var calls atomic.Int64
	release := make(chan struct{})
	first := make(chan struct{})
	go func() {
		g.Do("k1", func() *Result {
			calls.Add(1)
			close(first)
			<-release
			return &Result{StatusCode: 200}
		})
	}()
	<-first
	// Grupo lleno (k1 en vuelo) → k2 se ejecuta SIN coalescing.
	r, shared := g.Do("k2", func() *Result {
		calls.Add(1)
		return &Result{StatusCode: 200}
	})
	close(release)
	if shared {
		t.Fatal("bypassed call: shared = true, want false")
	}
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("bypassed result = %+v, want 200", r)
	}
	if calls.Load() != 2 {
		t.Fatalf("fn calls = %d, want 2", calls.Load())
	}
}
