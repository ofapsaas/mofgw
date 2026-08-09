// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 006-001-usage-accounting: contabilizar tokens
// (prompt/completion/total) por request, agregados por cliente |
// provider | modelo. Verifican postcondiciones P1-P6 del spec
// docs/specs/006-001-usage-accounting/spec.md.
//
// Contrato observable: contadores acumulados en /metrics (totales sin
// labels + desagregado con labels de cliente/provider/modelo), campos de
// request_end en logs. Cero red externa: providers fake en httptest.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- harness multi-cliente ----

// buildMultiClient igual que build() pero con N clientes autenticados.
// Devuelve el harness con la lista de claves válidas.
func buildMultiClient(t *testing.T, ups []*upstream, clientKeys ...string) *harness {
	t.Helper()
	h := &harness{ups: ups, key: clientKeys[0]}
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, nil)
	clients := make([]auth.Client, 0, len(clientKeys))
	for _, k := range clientKeys {
		clients = append(clients, auth.Client{ID: "client-" + k, KeySHA256: auth.HashKey(k)})
	}
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, nil, 10<<20, nil, nil, 0)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// chatAs envía un chat autenticado como la key dada (no-stream por
// defecto). Devuelve el response para inspección.
func (h *harness) chatAs(t *testing.T, key string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) metricsBody(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// ---- P1: prompt/completion/total acumulados por request (no-stream) ----

// TestRED_TotalesAcumuladosPorCliente: 2 requests del MISMO cliente al
// mismo provider+modelo con usage prompt=100/completion=50 → contadores
// acumulan prompt=200, completion=100, total=300 (P1).
func TestRED_TotalesAcumuladosPorCliente(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, total 1280
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	for i := 0; i < 2; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	s := h.metricsBody(t)
	// prompt 1200 × 2 = 2400; completion 80 × 2 = 160; total 1280 × 2 = 2560
	if !strings.Contains(s, "mofgw_prompt_tokens_total 2400") {
		t.Fatalf("prompt_tokens_total mal:\n%s", s)
	}
	if !strings.Contains(s, "mofgw_completion_tokens_total 160") {
		t.Fatalf("completion_tokens_total mal:\n%s", s)
	}
	if !strings.Contains(s, "mofgw_total_tokens_total 2560") {
		t.Fatalf("total_tokens_total mal:\n%s", s)
	}
}

// ---- P2: dimensión de cliente ----

// TestRED_ClientesSeparados: 2 clientes distintos al mismo modelo →
// agregados separados por client|provider|model (P2).
func TestRED_ClientesSeparados(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80
	h := buildMultiClient(t, []*upstream{ups}, "k1", "k2")

	// cliente k1: 1 request
	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("k1: status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	// cliente k2: 2 requests
	for i := 0; i < 2; i++ {
		resp := h.chatAs(t, "k2", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("k2 request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	s := h.metricsBody(t)
	// k1 = 1200 prompt; k2 = 2400 prompt; total = 3600
	if !strings.Contains(s, "mofgw_prompt_tokens_total 3600") {
		t.Fatalf("prompt total mal:\n%s", s)
	}
	// desagregado por cliente: client-k1 vs client-k2
	if !strings.Contains(s, `client="client-k1"`) || !strings.Contains(s, `client="client-k2"`) {
		t.Fatalf("sin labels de cliente en desagregado:\n%s", s)
	}
}

// ---- P3: exposición en /metrics + no rompe 003-001 ----

// TestRED_NoRompeCache003001: tras el cambio, los contadores de cache de
// 003-001 siguen intactos (P5).
func TestRED_NoRompeCache003001(t *testing.T) {
	ups := upstreamUsageOK("m") // cached 1000, reasoning 30
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_cache_hit_tokens_total 1000") {
		t.Fatalf("cache_hit_tokens_total roto (003-001):\n%s", s)
	}
	if !strings.Contains(s, "mofgw_reasoning_tokens_total 30") {
		t.Fatalf("reasoning_tokens_total roto (003-001):\n%s", s)
	}
}

// ---- P4: request_end con prompt/completion/total ----

// TestRED_RequestEndTotales: el evento request_end loguea prompt_tokens,
// completion_tokens, total_tokens (P4).
func TestRED_RequestEndTotales(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, total 1280
	h := buildWithLogger(t, []*upstream{ups}, "sk-test-1", logger)

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	end := waitForMsg(t, &buf, "request_end")
	if end == nil {
		t.Fatalf("sin request_end en logs:\n%s", buf.String())
	}
	if end["prompt_tokens"] != float64(1200) {
		t.Fatalf("prompt_tokens = %v, want 1200", end["prompt_tokens"])
	}
	if end["completion_tokens"] != float64(80) {
		t.Fatalf("completion_tokens = %v, want 80", end["completion_tokens"])
	}
	if end["total_tokens"] != float64(1280) {
		t.Fatalf("total_tokens = %v, want 1280", end["total_tokens"])
	}
}

// ---- P1 stream: totales acumulados en streaming ----

// TestRED_TotalesStream: stream con chunk final de usage acumula
// prompt/completion/total (P1 stream).
func TestRED_TotalesStream(t *testing.T) {
	ups := upstreamStreamUsageOK("m") // prompt 500, completion 20, total 520
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_prompt_tokens_total 500") {
		t.Fatalf("prompt_tokens_total (stream) mal:\n%s", s)
	}
	if !strings.Contains(s, "mofgw_completion_tokens_total 20") {
		t.Fatalf("completion_tokens_total (stream) mal:\n%s", s)
	}
}

var _ = metrics.New
