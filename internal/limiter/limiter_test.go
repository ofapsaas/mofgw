// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package limiter

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLimiterUnlimited(t *testing.T) {
	l := New(0)
	for i := 0; i < 100; i++ {
		if !l.Acquire(context.Background()) {
			t.Fatalf("iter %d: Acquire en modo ilimitado devolvió false", i)
		}
		if !l.TryAcquire() {
			t.Fatalf("iter %d: TryAcquire en modo ilimitado devolvió false", i)
		}
	}
	// Release en modo ilimitado no debe panic ni bloquear.
	l.Release()
}

func TestLimiterAcquireRelease(t *testing.T) {
	l := New(1)
	ctx := context.Background()
	if !l.Acquire(ctx) {
		t.Fatal("primer Acquire con 1 slot falló")
	}
	// Segundo acquire debe esperar: con timeout corto, expira.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if l.Acquire(shortCtx) {
		t.Fatal("segundo Acquire con slot ocupado no debió adquirir")
	}
	l.Release()
	// Ahora el slot está libre.
	if !l.Acquire(ctx) {
		t.Fatal("Acquire post-release falló")
	}
	l.Release()
}

func TestLimiterTryAcquire(t *testing.T) {
	l := New(1)
	if !l.TryAcquire() {
		t.Fatal("primer TryAcquire con 1 slot falló")
	}
	if l.TryAcquire() {
		t.Fatal("TryAcquire con slot ocupado no debió adquirir (fast-fail)")
	}
	l.Release()
	if !l.TryAcquire() {
		t.Fatal("TryAcquire post-release falló")
	}
	l.Release()
}

func TestLimiterContextCancel(t *testing.T) {
	l := New(1)
	_ = l.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		done <- l.Acquire(ctx)
	}()
	cancel()
	select {
	case got := <-done:
		if got {
			t.Fatal("Acquire con context cancelado no debió adquirir")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire con context cancelado se colgó")
	}
	l.Release()
}

func TestLimiterConcurrentN(t *testing.T) {
	const n = 5
	const workers = 50
	l := New(n)
	var active, maxActive int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !l.Acquire(context.Background()) {
				t.Error("Acquire con capacidad suficiente falló")
				return
			}
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			l.Release()
		}()
	}
	wg.Wait()
	if maxActive > n {
		t.Fatalf("máximo de concurrentes %d superó el límite %d", maxActive, n)
	}
	if maxActive != n {
		t.Fatalf("esperaba saturación a %d concurrentes, got %d", n, maxActive)
	}
}

func TestKeyedUnlimited(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{})
	if rel, ok := k.AcquireClient("c1"); !ok || rel != nil {
		t.Fatal("cliente sin budget debe ser ilimitado")
	}
	if rel, ok := k.AcquireAgent("c1", "a1"); !ok || rel != nil {
		t.Fatal("agente sin budget debe ser ilimitado")
	}
}

func TestKeyedClientLimit(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{
		"c1": {MaxConcurrent: 1, MaxPerAgent: 0},
		"c2": {MaxConcurrent: 1, MaxPerAgent: 0},
	})
	rel1, ok := k.AcquireClient("c1")
	if !ok || rel1 == nil {
		t.Fatal("primer slot de c1 falló")
	}
	defer rel1()
	// c1 agotado → fast-fail.
	if rel, ok := k.AcquireClient("c1"); ok {
		rel()
		t.Fatal("c1 con budget agotado no debió adquirir")
	}
	// c2 NO se ve afectado por c1 (aislamiento).
	rel2, ok := k.AcquireClient("c2")
	if !ok || rel2 == nil {
		t.Fatal("c2 debió adquirir aunque c1 esté saturado")
	}
	rel2()
}

func TestKeyedAgentLimit(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	})
	rel1, ok := k.AcquireAgent("c1", "agent-a")
	if !ok || rel1 == nil {
		t.Fatal("primer slot de (c1, agent-a) falló")
	}
	defer rel1()
	// Mismo (cliente, agente) agotado → fast-fail.
	if rel, ok := k.AcquireAgent("c1", "agent-a"); ok {
		rel()
		t.Fatal("(c1, agent-a) agotado no debió adquirir")
	}
	// Distinto agente del MISMO cliente → OK (aislamiento fino).
	rel2, ok := k.AcquireAgent("c1", "agent-b")
	if !ok || rel2 == nil {
		t.Fatal("(c1, agent-b) debió adquirir")
	}
	rel2()
	// Sin X-Agent-Id → usa clientID como agente único; si ya está
	// ocupado por agent-a, agent "" es key distinta → OK. Pero si el
	// cliente llama sin header dos veces, la segunda debe fallar.
	rel3, ok := k.AcquireAgent("c1", "")
	if !ok || rel3 == nil {
		t.Fatal("(c1, \"\") debió adquirir (key distinta de agent-a)")
	}
	rel3()
	// Ocupar la key "" dos veces.
	rel4, ok := k.AcquireAgent("c1", "")
	if !ok || rel4 == nil {
		t.Fatal("primer slot de (c1, \"\") falló")
	}
	if rel, ok := k.AcquireAgent("c1", ""); ok {
		rel()
		t.Fatal("(c1, \"\") agotado no debió adquirir")
	}
	rel4()
}

func TestKeyedAgentKeyNoCollision(t *testing.T) {
	// agentKey usa \x00 como separador: "ab" + "c" != "a" + "bc".
	if agentKey("ab", "c") == agentKey("a", "bc") {
		t.Fatal("agentKey colisiona para pares distintos")
	}
}
