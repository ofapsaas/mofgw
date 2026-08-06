// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package metrics expone counters/histograms mínimos para el MVP.
// EPIC-005 (001-001) definirá el formato Prometheus real; acá solo
// contadores atómicos que el endpoint /metrics serializa en texto plano,
// sin dependencias externas.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Metrics es el registro global de contadores del proxy.
type Metrics struct {
	requests     atomic.Int64
	streams      atomic.Int64
	errors       atomic.Int64
	failovers    atomic.Int64
	cooldownHits atomic.Int64
	lastProvider atomic.Value // string

	// rejected: contador con labels (client, reason) — 004-001/004-002.
	// Cardinalidad acotada por clientes configurados x razones fijas.
	rejectedMu sync.Mutex
	rejected   map[string]int64 // "client|reason" -> count
}

// New construye un registro vacío.
func New() *Metrics {
	m := &Metrics{rejected: make(map[string]int64)}
	m.lastProvider.Store("")
	return m
}

func (m *Metrics) IncRequests()    { m.requests.Add(1) }
func (m *Metrics) IncStreams()     { m.streams.Add(1) }
func (m *Metrics) IncErrors()      { m.errors.Add(1) }
func (m *Metrics) IncFailovers()   { m.failovers.Add(1) }
func (m *Metrics) IncCooldownHit() { m.cooldownHits.Add(1) }

// IncRejected incrementa el contador de requests rechazados por límites
// de concurrencia (004-001/004-002). reason: global_timeout |
// client_limit | agent_limit.
func (m *Metrics) IncRejected(client, reason string) {
	m.rejectedMu.Lock()
	m.rejected[client+"|"+reason]++
	m.rejectedMu.Unlock()
}

// SetLastProvider registra el provider que respondió (observabilidad).
func (m *Metrics) SetLastProvider(id string) { m.lastProvider.Store(id) }

// Render serializa el snapshot en texto plano (sufijo para Prometheus
// text exposition: `mofgw_*`).
func (m *Metrics) Render() string {
	var b strings.Builder
	write := func(name, value string) {
		b.WriteString("mofgw_")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	write("requests_total", fmt.Sprint(m.requests.Load()))
	write("streams_total", fmt.Sprint(m.streams.Load()))
	write("errors_total", fmt.Sprint(m.errors.Load()))
	write("failovers_total", fmt.Sprint(m.failovers.Load()))
	write("cooldown_hits_total", fmt.Sprint(m.cooldownHits.Load()))
	if p := m.lastProvider.Load().(string); p != "" {
		write("last_provider", p)
	}
	// rejected: una línea por (client, reason), labels Prometheus-style.
	m.rejectedMu.Lock()
	clients := make([]string, 0, len(m.rejected))
	vals := make(map[string]int64, len(m.rejected))
	for k, v := range m.rejected {
		clients = append(clients, k)
		vals[k] = v
	}
	m.rejectedMu.Unlock()
	sort.Strings(clients)
	for _, k := range clients {
		parts := strings.SplitN(k, "|", 2)
		b.WriteString("mofgw_rejected_requests_total{client=\"")
		b.WriteString(parts[0])
		b.WriteString("\",reason=\"")
		b.WriteString(parts[1])
		b.WriteString("\"} ")
		b.WriteString(fmt.Sprint(vals[k]))
		b.WriteString("\n")
	}
	return b.String()
}

// Names devuelve los nombres de métricas ordenados (para tests).
func (m *Metrics) Names() []string {
	lines := strings.Split(strings.TrimSpace(m.Render()), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		out = append(out, strings.Fields(l)[0])
	}
	sort.Strings(out)
	return out
}
