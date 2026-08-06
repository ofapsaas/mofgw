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
	if len(names) != 5 { // 5 contadores (last_provider solo si se seteó)
		t.Fatalf("names = %v (len %d), want 5", names, len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("names no ordenados: %v", names)
		}
	}
}
