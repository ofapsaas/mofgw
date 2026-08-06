// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package metrics

import (
	"strings"
	"testing"
)

func TestCountersAndRender(t *testing.T) {
	m := New()
	m.IncRequests()
	m.IncRequests()
	m.IncStreams()
	m.IncErrors()
	m.IncFailovers()
	m.IncCooldownHit()
	m.SetLastProvider("provider-a")

	render := m.Render()
	if !strings.Contains(render, "mofgw_requests_total 2") {
		t.Fatalf("requests_total mal: %q", render)
	}
	if !strings.Contains(render, "mofgw_streams_total 1") {
		t.Fatalf("streams_total mal: %q", render)
	}
	if !strings.Contains(render, "mofgw_last_provider provider-a") {
		t.Fatalf("last_provider mal: %q", render)
	}
}

func TestNamesSorted(t *testing.T) {
	m := New()
	m.IncErrors()
	names := m.Names()
	// 11 contadores base: requests, streams, errors, failovers, cooldown,
	// cache_hit, cache_miss, reasoning, prompt, completion, total
	// (003-001 P3 + 006-001 P3: los totales se emiten siempre, en 0 si no
	// hubo datos; last_provider solo si se seteó).
	if len(names) != 11 {
		t.Fatalf("names = %v (len %d), want 11", names, len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("names no ordenados: %v", names)
		}
	}
}
