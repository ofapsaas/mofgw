// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 007-003-usage-para-clientes: exponer consumo a
// los clientes (headers X-Usage-* por request + endpoint /v1/usage por
// cliente). Verifican postcondiciones P1-P5 del spec
// docs/specs/007-003-usage-para-clientes/spec.md.
//
// Contrato observable: headers en respuestas de /v1/chat/completions y
// JSON de GET /v1/usage con aislamiento por cliente. Cero red externa.

package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/proxy"
)

// usageHeaders verifica los headers X-Usage-* de una respuesta (C1).
func assertUsageHeaders(t *testing.T, resp *http.Response, prompt, completion, total, cacheHit int, cost string) {
	t.Helper()
	check := func(name, want string) {
		if got := resp.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	check("X-Usage-Prompt-Tokens", itoa(prompt))
	check("X-Usage-Completion-Tokens", itoa(completion))
	check("X-Usage-Total-Tokens", itoa(total))
	check("X-Usage-Cache-Hit-Tokens", itoa(cacheHit))
	if cost != "" {
		check("X-Usage-Cost-USD", cost)
	}
}

func itoa(v int) string {
	return strings.TrimSpace(strings.ReplaceAll(jsonNumber(v), `"`, ""))
}

func jsonNumber(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestRED_HeadersUsageNoStream: request no-stream con usage → headers
// X-Usage-* con los valores exactos (C1).
func TestRED_HeadersUsageNoStream(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, total 1280, cached 1000
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0}})

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	// miss 200/1M + completion 80/1M + cached 1000/1M = 0.0002+0.00008+0.001 = 0.00128
	assertUsageHeaders(t, resp, 1200, 80, 1280, 1000, "0.00128")
}

// TestRED_HeadersUsageSinUsage: request sin usage → headers 0 (C2).
func TestRED_HeadersUsageSinUsage(t *testing.T) {
	ups := upstreamNoUsage("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	assertUsageHeaders(t, resp, 0, 0, 0, 0, "0")
}

// TestRED_HeadersUsageStream: request stream → headers presentes antes
// del primer byte (C5). El usage del chunk final va en el body SSE.
func TestRED_HeadersUsageStream(t *testing.T) {
	ups := upstreamStreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// headers presentes (el request en curso aún no tiene usage → 0 o el
	// acumulado previo; el contrato exige que existan)
	for _, hname := range []string{"X-Usage-Prompt-Tokens", "X-Usage-Completion-Tokens", "X-Usage-Total-Tokens", "X-Usage-Cache-Hit-Tokens", "X-Usage-Cost-USD"} {
		if resp.Header.Get(hname) == "" {
			t.Fatalf("stream: header %s ausente", hname)
		}
	}
	// el body SSE sigue trayendo el usage en el chunk final (003-001 intacto)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"cached_tokens":400`) {
		t.Fatalf("stream: chunk final sin usage (003-001 roto): %s", body)
	}
}

// TestRED_UsageEndpointAislamiento: /v1/usage con auth muestra los
// agregados del cliente; dos clientes ven lo suyo (C3).
func TestRED_UsageEndpointAislamiento(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, total 1280, cached 1000
	h := buildMultiClient(t, []*upstream{ups}, "k1", "k2")

	// k1: 2 requests; k2: 1 request
	for i := 0; i < 2; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("k1: status = %d", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	resp := h.chatAs(t, "k2", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("k2: status = %d", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// k1: prompt 2400, completion 160, total 2560
	k1 := h.usageBody(t, "k1")
	if k1["client"] != "client-k1" {
		t.Fatalf("client = %v, want client-k1", k1["client"])
	}
	tot, ok := k1["totals"].(map[string]any)
	if !ok {
		t.Fatalf("sin totals: %v", k1)
	}
	if tot["prompt_tokens"] != float64(2400) {
		t.Fatalf("k1 prompt_tokens = %v, want 2400", tot["prompt_tokens"])
	}
	if tot["completion_tokens"] != float64(160) {
		t.Fatalf("k1 completion_tokens = %v, want 160", tot["completion_tokens"])
	}
	// k2: prompt 1200
	k2 := h.usageBody(t, "k2")
	tot2, _ := k2["totals"].(map[string]any)
	if tot2["prompt_tokens"] != float64(1200) {
		t.Fatalf("k2 prompt_tokens = %v, want 1200", tot2["prompt_tokens"])
	}
}

// TestRED_UsageEndpoint401: /v1/usage sin auth → 401 (C4).
func TestRED_UsageEndpoint401(t *testing.T) {
	ups := upstreamOK("m", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/usage", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("usage sin auth = %d, want 401", resp.StatusCode)
	}
}

// usageBody hace GET /v1/usage con auth y parsea el JSON.
func (h *harness) usageBody(t *testing.T, key string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("usage status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("usage body no es JSON: %v", err)
	}
	return out
}
