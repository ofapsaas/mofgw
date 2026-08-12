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

	// deduped: requests coalescidos por single-flight (010-001 P0) —
	// el follower esperó el resultado de un vuelo idéntico en curso.
	// Cardinalidad acotada por clientes configurados.
	dedupedMu sync.Mutex
	deduped   map[string]int64 // "client" -> count

	// respCache: hits/misses/stores del cache exact-match de respuestas
	// (010-002 P1) — un HIT responde sin tocar la cadena de providers.
	// Cardinalidad acotada por clientes configurados.
	respCacheMu    sync.Mutex
	respCacheHit   map[string]int64 // "client" -> hits
	respCacheMiss  map[string]int64 // "client" -> misses (elegibles, no en cache)
	respCacheStore map[string]int64 // "client" -> stores

	// tokens: contadores de tokens con labels (provider, model) —
	// 003-001-provider-cache (P3). Cardinalidad acotada por providers
	// configurados x modelos servidos.
	tokensMu  sync.Mutex
	cacheHit  map[string]int64 // "provider|model" -> cached tokens
	cacheMiss map[string]int64 // "provider|model" -> prompt - cached
	reasoning map[string]int64 // "provider|model" -> reasoning tokens

	// usage: contadores de tokens por (client, provider, model) —
	// 006-001-usage-accounting (P1-P3). Cardinalidad acotada por
	// clientes configurados x providers x modelos.
	usageMu       sync.Mutex
	promptTokens  map[string]int64 // "client|provider|model" -> prompt tokens
	completionTok map[string]int64 // "client|provider|model" -> completion tokens
	totalTokens   map[string]int64 // "client|provider|model" -> total tokens

	// cost: costo estimado USD por (client, provider, model) —
	// 006-002-costos (P3). Misma cardinalidad que usage.
	costMu sync.Mutex
	cost   map[string]float64 // "client|provider|model" -> USD acumulado

	// sessions: registro de consumo por (client, session) — 008-003.
	// Cardinalidad acotada por maxSessionsRetained (FIFO por
	// lastRequestAt).
	sessionsMu          sync.Mutex
	sessions            map[string]*SessionRecord // "client|session" -> record
	maxSessionsRetained int

	// contexts: composición estructural del contexto por (client, session)
	// — 009-001 (P2). Keyed como 008-003: "client|session" cuando hay
	// sesión; "client|" para el record de cliente. Los records por sesión
	// se evictan FIFO por last_request_at con el MISMO maxSessionsRetained;
	// el record de cliente (client|) NUNCA se evicta. History acotada por
	// contextHistoryPerSession (ring buffer, default 50).
	contextsMu               sync.Mutex
	contexts                 map[string]*ContextRecord
	contextHistoryPerSession int
}

// SessionRecord es el acumulado de consumo de una sesión (008-003 P2/P3).
type SessionRecord struct {
	SessionID      string
	Requests       int64
	PromptTokens   int64
	CompletionTok  int64
	TotalTokens    int64
	CostUSD        float64
	FirstRequestAt int64 // unix seconds
	LastRequestAt  int64 // unix seconds
}

// SessionSnapshot es la vista expuesta de una sesión (008-003 P3).
type SessionSnapshot struct {
	SessionID      string
	Requests       int64
	PromptTokens   int64
	CompletionTok  int64
	TotalTokens    int64
	CostUSD        float64
	FirstRequestAt int64
	LastRequestAt  int64
}

// New construye un registro vacío.
func New() *Metrics {
	m := &Metrics{
		rejected:            make(map[string]int64),
		deduped:             make(map[string]int64),
		respCacheHit:        make(map[string]int64),
		respCacheMiss:       make(map[string]int64),
		respCacheStore:      make(map[string]int64),
		cacheHit:            make(map[string]int64),
		cacheMiss:           make(map[string]int64),
		reasoning:           make(map[string]int64),
		promptTokens:        make(map[string]int64),
		completionTok:       make(map[string]int64),
		totalTokens:         make(map[string]int64),
		cost:                make(map[string]float64),
		sessions:            make(map[string]*SessionRecord),
		maxSessionsRetained: 100,

		contexts:                 make(map[string]*ContextRecord),
		contextHistoryPerSession: 50,
	}
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

// IncDeduped incrementa el contador de requests coalescidos por
// single-flight (010-001 P0): un request idéntico en vuelo respondió
// por el líder, sin llamada upstream propia.
func (m *Metrics) IncDeduped(client string) {
	m.dedupedMu.Lock()
	m.deduped[client]++
	m.dedupedMu.Unlock()
}

// IncRespCacheHit incrementa el contador de hits del cache exact-match
// de respuestas (010-002 P1): el request se respondió desde el cache,
// sin llamada upstream.
func (m *Metrics) IncRespCacheHit(client string) {
	m.respCacheMu.Lock()
	m.respCacheHit[client]++
	m.respCacheMu.Unlock()
}

// IncRespCacheMiss incrementa el contador de misses del cache
// exact-match (010-002 P1): el request era elegible pero no estaba en
// cache → fluyó al router/chain.
func (m *Metrics) IncRespCacheMiss(client string) {
	m.respCacheMu.Lock()
	m.respCacheMiss[client]++
	m.respCacheMu.Unlock()
}

// IncRespCacheStore incrementa el contador de respuestas almacenadas en
// el cache exact-match (010-002 P1): un request elegible completó con
// éxito y su respuesta quedó disponible para hits futuros.
func (m *Metrics) IncRespCacheStore(client string) {
	m.respCacheMu.Lock()
	m.respCacheStore[client]++
	m.respCacheMu.Unlock()
}

// IncCacheTokens acumula tokens de cache/reasoning de un request
// (003-001 P3). hit = cached_tokens, miss = prompt - cached,
// reasoning = reasoning_tokens. El llamado con valores 0 es válido y
// asegura que el contador aparezca en /metrics (aunque en 0).
func (m *Metrics) IncCacheTokens(provider, model string, hit, miss, reasoning int64) {
	key := provider + "|" + model
	m.tokensMu.Lock()
	m.cacheHit[key] += hit
	m.cacheMiss[key] += miss
	m.reasoning[key] += reasoning
	m.tokensMu.Unlock()
}

// IncUsage acumula los tokens totales de un request por
// (client, provider, model) — 006-001-usage-accounting (P1-P3).
// prompt/completion/total se acumulan en sus propios contadores; el
// llamado con 0 es válido y asegura la presencia del contador.
// client vacío (request sin auth — no debería pasar) cae en "unknown".
func (m *Metrics) IncUsage(client, provider, model string, prompt, completion, total int64) {
	if client == "" {
		client = "unknown"
	}
	key := client + "|" + provider + "|" + model
	m.usageMu.Lock()
	m.promptTokens[key] += prompt
	m.completionTok[key] += completion
	m.totalTokens[key] += total
	m.usageMu.Unlock()
}

// IncCost acumula el costo estimado en USD de un request por
// (client, provider, model) — 006-002-costos (P3). client vacío cae en
// "unknown" (consistente con IncUsage).
func (m *Metrics) IncCost(client, provider, model string, costUsd float64) {
	if client == "" {
		client = "unknown"
	}
	key := client + "|" + provider + "|" + model
	m.costMu.Lock()
	m.cost[key] += costUsd
	m.costMu.Unlock()
}

// ModelUsageSnapshot es el agregado de consumo de un modelo para un
// cliente (007-003 /v1/usage).
type ModelUsageSnapshot struct {
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CostUSD          float64
}

// UsageSnapshot es el snapshot de consumo de un cliente (007-003 P3).
// client "" o desconocido → todos 0 (nunca nil).
type UsageSnapshot struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CacheHitTokens   int64
	CostUSD          float64
	ByModel          []ModelUsageSnapshot
}

// SnapshotClient devuelve el snapshot de consumo del cliente dado
// (007-003). Solo suma las keys cuyo primer segmento == client.
func (m *Metrics) SnapshotClient(client string) UsageSnapshot {
	var snap UsageSnapshot
	prefix := client + "|"
	m.usageMu.Lock()
	for k, v := range m.promptTokens {
		if strings.HasPrefix(k, prefix) {
			snap.PromptTokens += v
		}
	}
	for k, v := range m.completionTok {
		if strings.HasPrefix(k, prefix) {
			snap.CompletionTokens += v
		}
	}
	for k, v := range m.totalTokens {
		if strings.HasPrefix(k, prefix) {
			snap.TotalTokens += v
		}
	}
	m.usageMu.Unlock()
	m.tokensMu.Lock()
	for k, v := range m.cacheHit {
		if strings.HasPrefix(k, prefix) {
			snap.CacheHitTokens += v
		}
	}
	m.tokensMu.Unlock()
	m.costMu.Lock()
	byModel := map[string]*ModelUsageSnapshot{}
	for k, v := range m.cost {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		snap.CostUSD += v
		parts := strings.SplitN(k, "|", 3)
		model := parts[2]
		ms, ok := byModel[model]
		if !ok {
			ms = &ModelUsageSnapshot{Model: model}
			byModel[model] = ms
		}
		ms.CostUSD += v
	}
	m.costMu.Unlock()
	// completar byModel con tokens (misma key estructura)
	m.usageMu.Lock()
	for k, v := range m.promptTokens {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		parts := strings.SplitN(k, "|", 3)
		model := parts[2]
		ms, ok := byModel[model]
		if !ok {
			ms = &ModelUsageSnapshot{Model: model}
			byModel[model] = ms
		}
		ms.PromptTokens = v
	}
	for k, v := range m.completionTok {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		parts := strings.SplitN(k, "|", 3)
		model := parts[2]
		ms, ok := byModel[model]
		if !ok {
			ms = &ModelUsageSnapshot{Model: model}
			byModel[model] = ms
		}
		ms.CompletionTokens = v
	}
	for k, v := range m.totalTokens {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		parts := strings.SplitN(k, "|", 3)
		model := parts[2]
		ms, ok := byModel[model]
		if !ok {
			ms = &ModelUsageSnapshot{Model: model}
			byModel[model] = ms
		}
		ms.TotalTokens = v
	}
	m.usageMu.Unlock()
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		snap.ByModel = append(snap.ByModel, *byModel[model])
	}
	return snap
}

// RecordSession registra el consumo de un request en su sesión
// (008-003 P2). session "" → se ignora (sesión implícita, sin registro).
// Aplica FIFO de retención (maxSessionsRetained): al superar el tope se
// descarta la sesión con last_request_at más viejo.
func (m *Metrics) RecordSession(client, session string, prompt, completion, total int64, costUSD float64, nowUnix int64) {
	if session == "" {
		return
	}
	key := client + "|" + session
	m.sessionsMu.Lock()
	rec, ok := m.sessions[key]
	if !ok {
		rec = &SessionRecord{SessionID: session, FirstRequestAt: nowUnix}
		m.sessions[key] = rec
		if len(m.sessions) > m.maxSessionsRetained {
			m.evictOldestLocked()
		}
	}
	rec.Requests++
	rec.PromptTokens += prompt
	rec.CompletionTok += completion
	rec.TotalTokens += total
	rec.CostUSD += costUSD
	rec.LastRequestAt = nowUnix
	m.sessionsMu.Unlock()
}

// evictOldestLocked descarta la sesión con last_request_at más viejo
// (FIFO de retención, 008-003 P4). Requiere sessionsMu held.
func (m *Metrics) evictOldestLocked() {
	var oldestKey string
	var oldestAt int64 = 1<<63 - 1
	for k, r := range m.sessions {
		if r.LastRequestAt < oldestAt {
			oldestAt = r.LastRequestAt
			oldestKey = k
		}
	}
	if oldestKey != "" {
		delete(m.sessions, oldestKey)
	}
}

// SessionSnapshot devuelve las stats de una sesión del cliente
// (008-003 P3). ok=false si la sesión no existe para este cliente.
func (m *Metrics) SessionSnapshot(client, session string) (SessionSnapshot, bool) {
	key := client + "|" + session
	m.sessionsMu.Lock()
	rec, ok := m.sessions[key]
	var snap SessionSnapshot
	if ok {
		snap = SessionSnapshot{
			SessionID:      rec.SessionID,
			Requests:       rec.Requests,
			PromptTokens:   rec.PromptTokens,
			CompletionTok:  rec.CompletionTok,
			TotalTokens:    rec.TotalTokens,
			CostUSD:        rec.CostUSD,
			FirstRequestAt: rec.FirstRequestAt,
			LastRequestAt:  rec.LastRequestAt,
		}
	}
	m.sessionsMu.Unlock()
	return snap, ok
}

// SessionsList lista las sesiones del cliente (008-003 P3), ordenadas por
// last_request_at desc (más recientes primero). Vacío si no hay.
func (m *Metrics) SessionsList(client string) []SessionSnapshot {
	prefix := client + "|"
	m.sessionsMu.Lock()
	out := make([]SessionSnapshot, 0, 8)
	for k, r := range m.sessions {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		out = append(out, SessionSnapshot{
			SessionID:      r.SessionID,
			Requests:       r.Requests,
			PromptTokens:   r.PromptTokens,
			CompletionTok:  r.CompletionTok,
			TotalTokens:    r.TotalTokens,
			CostUSD:        r.CostUSD,
			FirstRequestAt: r.FirstRequestAt,
			LastRequestAt:  r.LastRequestAt,
		})
	}
	m.sessionsMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastRequestAt > out[j].LastRequestAt })
	return out
}

// SetMaxSessionsRetained ajusta el tope de retención (008-003 P4).
// Default 100 en New.
func (m *Metrics) SetMaxSessionsRetained(n int) {
	m.sessionsMu.Lock()
	m.maxSessionsRetained = n
	m.sessionsMu.Unlock()
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
	// deduped: una línea por client (010-001 P0).
	m.dedupedMu.Lock()
	dedupedClients := make([]string, 0, len(m.deduped))
	dedupedVals := make(map[string]int64, len(m.deduped))
	for k, v := range m.deduped {
		dedupedClients = append(dedupedClients, k)
		dedupedVals[k] = v
	}
	m.dedupedMu.Unlock()
	sort.Strings(dedupedClients)
	for _, k := range dedupedClients {
		b.WriteString("mofgw_single_flight_deduped_total{client=\"")
		b.WriteString(k)
		b.WriteString("\"} ")
		b.WriteString(fmt.Sprint(dedupedVals[k]))
		b.WriteString("\n")
	}
	// resp cache: una línea por client por métrica (010-002 P1).
	m.respCacheMu.Lock()
	respClients := make([]string, 0, len(m.respCacheHit))
	respHits := make(map[string]int64, len(m.respCacheHit))
	respMisses := make(map[string]int64, len(m.respCacheMiss))
	respStores := make(map[string]int64, len(m.respCacheStore))
	for k, v := range m.respCacheHit {
		respClients = append(respClients, k)
		respHits[k] = v
	}
	for k, v := range m.respCacheMiss {
		respMisses[k] = v
	}
	for k, v := range m.respCacheStore {
		respStores[k] = v
	}
	m.respCacheMu.Unlock()
	sort.Strings(respClients)
	for _, k := range respClients {
		b.WriteString("mofgw_response_cache_hits_total{client=\"")
		b.WriteString(k)
		b.WriteString("\"} ")
		b.WriteString(fmt.Sprint(respHits[k]))
		b.WriteString("\n")
		b.WriteString("mofgw_response_cache_misses_total{client=\"")
		b.WriteString(k)
		b.WriteString("\"} ")
		b.WriteString(fmt.Sprint(respMisses[k]))
		b.WriteString("\n")
		b.WriteString("mofgw_response_cache_stored_total{client=\"")
		b.WriteString(k)
		b.WriteString("\"} ")
		b.WriteString(fmt.Sprint(respStores[k]))
		b.WriteString("\n")
	}
	// tokens: totales sin labels (siempre presentes, 0 si no hubo datos)
	// + una línea por (provider, model) cuando hay datos (003-001 P3).
	m.tokensMu.Lock()
	type tokenRow struct{ hit, miss, reasoning int64 }
	rows := make(map[string]tokenRow, len(m.cacheHit))
	var hitTotal, missTotal, reasoningTotal int64
	for k, v := range m.cacheHit {
		r := rows[k]
		r.hit = v
		rows[k] = r
		hitTotal += v
	}
	for k, v := range m.cacheMiss {
		r := rows[k]
		r.miss = v
		rows[k] = r
		missTotal += v
	}
	for k, v := range m.reasoning {
		r := rows[k]
		r.reasoning = v
		rows[k] = r
		reasoningTotal += v
	}
	m.tokensMu.Unlock()

	write("cache_hit_tokens_total", fmt.Sprint(hitTotal))
	write("cache_miss_tokens_total", fmt.Sprint(missTotal))
	write("reasoning_tokens_total", fmt.Sprint(reasoningTotal))

	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 2)
		label := "mofgw_cache_tokens{provider=\"" + parts[0] + "\",model=\"" + parts[1] + "\"}"
		b.WriteString(label)
		b.WriteString(" hit=")
		b.WriteString(fmt.Sprint(rows[k].hit))
		b.WriteString(" miss=")
		b.WriteString(fmt.Sprint(rows[k].miss))
		b.WriteString(" reasoning=")
		b.WriteString(fmt.Sprint(rows[k].reasoning))
		b.WriteString("\n")
	}
	// usage por (client, provider, model): totales sin labels + línea
	// por clave con labels (006-001 P2/P3).
	m.usageMu.Lock()
	type usageRow struct{ prompt, completion, total int64 }
	usageRows := make(map[string]usageRow, len(m.promptTokens))
	var promptTotal, completionTotal, totalTotal int64
	for k, v := range m.promptTokens {
		r := usageRows[k]
		r.prompt = v
		usageRows[k] = r
		promptTotal += v
	}
	for k, v := range m.completionTok {
		r := usageRows[k]
		r.completion = v
		usageRows[k] = r
		completionTotal += v
	}
	for k, v := range m.totalTokens {
		r := usageRows[k]
		r.total = v
		usageRows[k] = r
		totalTotal += v
	}
	m.usageMu.Unlock()

	write("prompt_tokens_total", fmt.Sprint(promptTotal))
	write("completion_tokens_total", fmt.Sprint(completionTotal))
	write("total_tokens_total", fmt.Sprint(totalTotal))

	uKeys := make([]string, 0, len(usageRows))
	for k := range usageRows {
		uKeys = append(uKeys, k)
	}
	sort.Strings(uKeys)
	for _, k := range uKeys {
		parts := strings.SplitN(k, "|", 3)
		label := "mofgw_usage_tokens{client=\"" + parts[0] + "\",provider=\"" + parts[1] + "\",model=\"" + parts[2] + "\"}"
		b.WriteString(label)
		b.WriteString(" prompt=")
		b.WriteString(fmt.Sprint(usageRows[k].prompt))
		b.WriteString(" completion=")
		b.WriteString(fmt.Sprint(usageRows[k].completion))
		b.WriteString(" total=")
		b.WriteString(fmt.Sprint(usageRows[k].total))
		b.WriteString("\n")
	}
	// costo estimado por (client, provider, model) (006-002 P3).
	m.costMu.Lock()
	costRows := make(map[string]float64, len(m.cost))
	var costTotal float64
	for k, v := range m.cost {
		costRows[k] = v
		costTotal += v
	}
	m.costMu.Unlock()

	write("cost_usd_total", fmt.Sprintf("%.6f", costTotal))

	cKeys := make([]string, 0, len(costRows))
	for k := range costRows {
		cKeys = append(cKeys, k)
	}
	sort.Strings(cKeys)
	for _, k := range cKeys {
		parts := strings.SplitN(k, "|", 3)
		label := "mofgw_cost_usd{client=\"" + parts[0] + "\",provider=\"" + parts[1] + "\",model=\"" + parts[2] + "\"}"
		b.WriteString(label)
		b.WriteString(" ")
		b.WriteString(fmt.Sprintf("%.6f", costRows[k]))
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
