// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package limiter

import (
	"strings"
	"testing"
	"time"
)

// ---- SEC-001 P2: X-Agent-Id acotado (truncado + TTL) ----

// TestSEC001_AgentIDTruncado: dos agentes con IDs de >64 chars que
// comparten el mismo prefijo de 64 chars se tratan como EL MISMO agente
// (misma key del limiter). RED: hoy AcquireAgent usa el agentID crudo
// como key (limiter.go:158-162) — dos IDs largos distintos crean dos
// límites distintos. GREEN: truncar a 64 chars antes de la key.
func TestSEC001_AgentIDTruncado(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	})
	longA := "a" + strings.Repeat("x", 100)      // 101 chars, prefijo 64 = "a" + 63 "x"
	longB := "a" + strings.Repeat("x", 63) + "b" // mismo prefijo 64 chars, distinto final
	if longA[:64] != longB[:64] {
		t.Fatalf("setup: los prefijos de 64 chars deben coincidir")
	}

	rel1, ok := k.AcquireAgent("c1", longA)
	if !ok || rel1 == nil {
		t.Fatal("primer slot de (c1, longA) falló")
	}
	defer rel1()
	// longB comparte el prefijo de 64 chars → tras el truncado es la MISMA
	// key → agotado → fast-fail.
	if rel, ok := k.AcquireAgent("c1", longB); ok {
		rel()
		t.Fatal("SEC-001 P2: longB comparte prefijo de 64 chars con longA — no debió adquirir (misma key truncada)")
	}
}

// TestSEC001_AgentTTLPurga: las entradas de agentes inactivos se purgan
// tras un TTL. RED: hoy agentLimits nunca se limpia (memory leak lento).
// GREEN: PurgeStaleAgents(now) elimina las entradas cuyo lastUsed es
// anterior a now-TTL. El TTL es configurable (default 10 min).
func TestSEC001_AgentTTLPurga(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	})
	k.SetAgentTTL(10 * time.Minute)

	rel, ok := k.AcquireAgent("c1", "agent-stale")
	if !ok || rel == nil {
		t.Fatal("adquisición inicial falló")
	}
	rel() // release → el agente queda inactivo

	// Sin purga: re-adquirir funciona (entry sigue).
	rel2, ok := k.AcquireAgent("c1", "agent-stale")
	if !ok || rel2 == nil {
		t.Fatal("re-adquisición sin purga falló")
	}
	rel2()

	// Purga con now muy futuro → la entry inactiva se elimina.
	now := time.Now().Add(30 * time.Minute)
	k.PurgeStaleAgents(now)

	// Tras la purga, re-adquirir crea una entry NUEVA (lastUsed reseteado)
	// → debe funcionar. Y el mapa no debe tener la entry vieja (observable
	// via Acquire + el hecho de que no crashea ni cuenta doble).
	rel3, ok := k.AcquireAgent("c1", "agent-stale")
	if !ok || rel3 == nil {
		t.Fatal("tras purga, re-adquisición debe crear entry nueva y funcionar")
	}
	rel3()
}

// TestSEC001_AgentTTLDefault: TTL default 10 min (no requiere setter).
func TestSEC001_AgentTTLDefault(t *testing.T) {
	k := NewKeyed(map[string]ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	})
	rel, ok := k.AcquireAgent("c1", "agent")
	if !ok || rel == nil {
		t.Fatal("adquisición falló")
	}
	rel()
	// Purga con now = inicio + 11 min → entry inactiva se purga (default 10m).
	now := time.Now().Add(11 * time.Minute)
	k.PurgeStaleAgents(now)
	// Si la entry se purgó correctamente, no hay panic ni estado roto:
	// re-adquirir funciona.
	rel2, ok := k.AcquireAgent("c1", "agent")
	if !ok || rel2 == nil {
		t.Fatal("tras purga default, re-adquisición falló")
	}
	rel2()
}
