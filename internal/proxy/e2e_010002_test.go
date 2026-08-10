// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests e2e de la feature 010-002-response-cache (P1): cache exact-match
// de respuestas. Verifican el wiring del proxy con SetResponseCache(true):
// (1) MISS → flujo upstream + header X-Mofgw-Cache: MISS; (2) request
// idéntico → HIT desde cache + X-Mofgw-Cache: HIT + X-Mofgw-Cache-Age,
// SIN llamada upstream; (3) elegibilidad (no-stream + temperature==0 o
// seed); (4) partición por cliente (cero fuga); (5) canonicidad
// (orden de claves JSON irrelevante); (6) contadores en /metrics.
// Upstreams fake (httptest) con contador de requests; cero red externa.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// countingUpstream envuelve un upstream y cuenta requests (para verificar
// "el HIT no tocó upstream").
type countingUpstream struct {
	ups   *upstream
	mu    sync.Mutex
	count int
}

func (c *countingUpstream) handler() http.HandlerFunc {
	inner := c.ups.handler()
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.count++
		c.mu.Unlock()
		inner(w, r)
	}
}

func (c *countingUpstream) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// buildCache construye el harness con el response cache ON
// (SetResponseCache(true, maxEntries, ttl)) y un upstream contador
// no-streaming (upstreamOK). clients: pares (id, key) para auth;
// nil → [("test", clientKey)].
func buildCache(t *testing.T, clientKey string, maxEntries int, ttl time.Duration, clients []auth.Client) (*harness, *countingUpstream) {
	t.Helper()
	return buildCacheWithUpstream(t, upstreamOK("m", "desde-upstream"), clientKey, maxEntries, ttl, clients)
}

// buildCacheWithUpstream igual que buildCache pero con el upstream
// controlado por el test (necesario para streaming: upstreamStreamOK).
func buildCacheWithUpstream(t *testing.T, ups *upstream, clientKey string, maxEntries int, ttl time.Duration, clients []auth.Client) (*harness, *countingUpstream) {
	t.Helper()
	cu := &countingUpstream{ups: ups}
	srv := httptest.NewServer(cu.handler())
	t.Cleanup(srv.Close)
	cl := provider.NewClient("up1", srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
	r := router.New([]router.ProviderSpec{{Provider: cl}}, 2, 0, 0, 30*1000*1000*1000, nil)
	if clients == nil {
		clients = []auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}}
	}
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, []provider.Provider{cl}, authz, m, nil, nil, 10<<20, nil, nil, 0)
	s.SetResponseCache(true, maxEntries, ttl)
	h := &harness{ups: []*upstream{cu.ups}, key: clientKey, proxySrv: s}
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, cu
}

// chatRaw envía un chat con body crudo (para probar orden de claves) y
// devuelve el response (headers + body leído).
func chatRaw(t *testing.T, h *harness, key, rawBody string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(rawBody))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func deterministicBody() map[string]any {
	return map[string]any{
		"model":       "m",
		"temperature": 0,
		"messages":    []map[string]any{{"role": "user", "content": "hola"}},
	}
}

// ---- 1. MISS → upstream → HIT sin upstream ----

func TestE2E010002_MissThenHit(t *testing.T) {
	h, cu := buildCache(t, "sk-test-1", 16, 30*time.Second, nil)

	// 1er request: MISS (header) + upstream contado.
	resp := h.chat(t, deterministicBody())
	if resp.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("1er request: X-Mofgw-Cache = %q, want MISS", resp.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", cu.calls())
	}

	// 2do request idéntico: HIT + Age + SIN upstream.
	resp2 := h.chat(t, deterministicBody())
	if resp2.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("2do request: X-Mofgw-Cache = %q, want HIT", resp2.Header.Get("X-Mofgw-Cache"))
	}
	if resp2.Header.Get("X-Mofgw-Cache-Age") == "" {
		t.Fatal("X-Mofgw-Cache-Age ausente en HIT")
	}
	if cu.calls() != 1 {
		t.Fatalf("upstream calls tras HIT = %d, want 1 (el HIT no toca upstream)", cu.calls())
	}
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "desde-upstream") {
		t.Fatalf("HIT body no contiene la respuesta cacheada: %s", body2)
	}
}

// ---- 2. Off por default: sin header X-Mofgw-Cache ----

func TestE2E010002_HeaderAbsentWhenDisabled(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")
	resp := h.chat(t, deterministicBody())
	if resp.Header.Get("X-Mofgw-Cache") != "" {
		t.Fatalf("X-Mofgw-Cache = %q, want ausente (feature off por default)", resp.Header.Get("X-Mofgw-Cache"))
	}
}

// ---- 3. Elegibilidad: streaming y temperature != 0 no se cachean ----

func TestE2E010002_StreamingNotCached(t *testing.T) {
	h, cu := buildCacheWithUpstream(t, upstreamStreamOK("m"), "sk-test-1", 16, 30*time.Second, nil)
	body := map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	}
	resp := h.chat(t, body)
	if resp.Header.Get("X-Mofgw-Cache") != "" {
		t.Fatalf("streaming: X-Mofgw-Cache = %q, want ausente", resp.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", cu.calls())
	}
}

func TestE2E010002_NonDeterministicNotCached(t *testing.T) {
	h, cu := buildCache(t, "sk-test-1", 16, 30*time.Second, nil)
	body := map[string]any{
		"model":       "m",
		"temperature": 0.7,
		"messages":    []map[string]any{{"role": "user", "content": "hola"}},
	}
	resp := h.chat(t, body)
	if resp.Header.Get("X-Mofgw-Cache") != "" {
		t.Fatalf("temperature=0.7: X-Mofgw-Cache = %q, want ausente", resp.Header.Get("X-Mofgw-Cache"))
	}
	// Repetido: sigue sin cachear (cada request toca upstream).
	resp2 := h.chat(t, body)
	if resp2.Header.Get("X-Mofgw-Cache") != "" {
		t.Fatalf("repeat temperature=0.7: X-Mofgw-Cache = %q, want ausente", resp2.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 2 {
		t.Fatalf("upstream calls = %d, want 2", cu.calls())
	}
}

// Seed presente también habilita el cache (determinístico).
func TestE2E010002_SeedEnablesCache(t *testing.T) {
	h, cu := buildCache(t, "sk-test-1", 16, 30*time.Second, nil)
	body := map[string]any{
		"model":    "m",
		"seed":     42,
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	}
	resp := h.chat(t, body)
	if resp.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("seed: X-Mofgw-Cache = %q, want MISS", resp.Header.Get("X-Mofgw-Cache"))
	}
	resp2 := h.chat(t, body)
	if resp2.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("seed repeat: X-Mofgw-Cache = %q, want HIT", resp2.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", cu.calls())
	}
}

// ---- 4. Canonicidad: orden de claves JSON irrelevante ----

func TestE2E010002_KeyOrderInvariant(t *testing.T) {
	h, cu := buildCache(t, "sk-test-1", 16, 30*time.Second, nil)

	// 1er request con claves en orden A.
	_, _ = chatRaw(t, h, "sk-test-1", `{"model":"m","temperature":0,"messages":[{"role":"user","content":"hola"}]}`)
	if cu.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", cu.calls())
	}

	// Mismo request con claves en orden B → HIT (canonicalización).
	resp, _ := chatRaw(t, h, "sk-test-1", `{"messages":[{"role":"user","content":"hola"}],"temperature":0,"model":"m"}`)
	if resp.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("key-order variant: X-Mofgw-Cache = %q, want HIT", resp.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", cu.calls())
	}

	// Body distinto (contenido cambia) → MISS.
	resp3, _ := chatRaw(t, h, "sk-test-1", `{"model":"m","temperature":0,"messages":[{"role":"user","content":"otro"}]}`)
	if resp3.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("distinto content: X-Mofgw-Cache = %q, want MISS", resp3.Header.Get("X-Mofgw-Cache"))
	}
}

// ---- 5. Partición por cliente: prompts idénticos no comparten cache ----

func TestE2E010002_ClientIsolation(t *testing.T) {
	clients := []auth.Client{
		{ID: "alice", KeySHA256: auth.HashKey("sk-alice")},
		{ID: "bob", KeySHA256: auth.HashKey("sk-bob")},
	}
	h, cu := buildCache(t, "sk-alice", 16, 30*time.Second, clients)

	respA := h.chat(t, deterministicBody()) // alice → MISS + store
	if respA.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("alice: X-Mofgw-Cache = %q, want MISS", respA.Header.Get("X-Mofgw-Cache"))
	}
	// bob, MISMO prompt → MISS (partición por client_id, cero fuga).
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(mustJSON(t, deterministicBody())))
	req.Header.Set("Authorization", "Bearer sk-bob")
	respB, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bob request: %v", err)
	}
	t.Cleanup(func() { respB.Body.Close() })
	if respB.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("bob mismo prompt: X-Mofgw-Cache = %q, want MISS (partición por cliente)", respB.Header.Get("X-Mofgw-Cache"))
	}
	// alice repite → HIT (su propia partición).
	respA2 := h.chat(t, deterministicBody())
	if respA2.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("alice repeat: X-Mofgw-Cache = %q, want HIT", respA2.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (alice miss + bob miss)", cu.calls())
	}
}

// ---- 6. TTL: entrada expirada → MISS de nuevo ----

func TestE2E010002_TTLExpiry(t *testing.T) {
	h, cu := buildCache(t, "sk-test-1", 16, 50*time.Millisecond, nil)
	resp := h.chat(t, deterministicBody())
	if resp.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("1er: X-Mofgw-Cache = %q, want MISS", resp.Header.Get("X-Mofgw-Cache"))
	}
	resp2 := h.chat(t, deterministicBody())
	if resp2.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("2do: X-Mofgw-Cache = %q, want HIT", resp2.Header.Get("X-Mofgw-Cache"))
	}
	time.Sleep(80 * time.Millisecond)
	resp3 := h.chat(t, deterministicBody())
	if resp3.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("post-TTL: X-Mofgw-Cache = %q, want MISS", resp3.Header.Get("X-Mofgw-Cache"))
	}
	if cu.calls() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (miss, hit, miss-post-ttl)", cu.calls())
	}
}

// ---- 7. Contadores en /metrics ----

func TestE2E010002_Metrics(t *testing.T) {
	h, _ := buildCache(t, "sk-test-1", 16, 30*time.Second, nil)
	h.chat(t, deterministicBody()) // miss
	h.chat(t, deterministicBody()) // hit
	resp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{
		`mofgw_response_cache_misses_total{client="test"} 1`,
		`mofgw_response_cache_hits_total{client="test"} 1`,
		`mofgw_response_cache_stored_total{client="test"} 1`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("metrics no contiene %q\n%s", want, s)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
