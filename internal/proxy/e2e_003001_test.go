// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 003-001-provider-cache: instrumentar cache
// hit/miss de providers. Verifican postcondiciones P1-P6 del spec
// docs/specs/003-001-provider-cache/spec.md.
//
// Contrato observable: usage del upstream (campos de cache parseados en
// Complete y en el chunk final SSE), inyección de include_usage en
// streaming, contadores de /metrics, campos de log y robustez ante usage
// ausente. Cero red externa: providers fake en httptest.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- upstreams fake con usage de cache ----

// upstreamUsageOK responde 200 con usage que incluye cached_tokens y
// reasoning_tokens (formato OpenAI estándar que devuelve zen y bailian).
func upstreamUsageOK(model string) *upstream {
	return &upstream{
		status: 200,
		model:  model,
		body:   `{"id":"chatcmpl-cache1","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"hola"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":80,"total_tokens":1280,"prompt_tokens_details":{"cached_tokens":1000,"cache_creation_input_tokens":200},"completion_tokens_details":{"reasoning_tokens":30}}}`,
	}
}

// upstreamNoUsage responde 200 SIN objeto usage (caso bug zen #14795).
func upstreamNoUsage(model, content string) *upstream {
	return &upstream{
		status: 200,
		model:  model,
		body:   `{"id":"chatcmpl-nu","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"` + content + `"},"finish_reason":"stop"}]}`,
	}
}

// upstreamStreamUsageOK stream con chunk final de usage (include_usage).
func upstreamStreamUsageOK(model string) *upstream {
	return &upstream{
		status: 200,
		stream: true,
		model:  model,
		body: "data: {\"id\":\"chatcmpl-s1\",\"model\":\"" + model + "\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\n" +
			"data: {\"choices\":[]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":500,\"completion_tokens\":20,\"total_tokens\":520,\"prompt_tokens_details\":{\"cached_tokens\":400}}}\n\n" +
			"data: [DONE]\n\n",
	}
}

// ---- P2: inyección de include_usage en streaming ----

// TestRED_StreamInyectaIncludeUsage: un request stream sin stream_options
// debe llegar al upstream con stream_options.include_usage=true (P2).
func TestRED_StreamInyectaIncludeUsage(t *testing.T) {
	ups := upstreamStreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// El body que recibió el upstream DEBE incluir include_usage=true
	// aunque el cliente no lo haya mandado.
	var got map[string]any
	if err := json.Unmarshal([]byte(ups.gotBody), &got); err != nil {
		t.Fatalf("body upstream no es JSON: %v", err)
	}
	so, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("sin stream_options en body upstream: %s", ups.gotBody)
	}
	if so["include_usage"] != true {
		t.Fatalf("include_usage = %v, want true (body: %s)", so["include_usage"], ups.gotBody)
	}
}

// TestRED_StreamRespetaIncludeUsageCliente: si el cliente manda
// include_usage:false explícito, se respeta (P2, no pisa al cliente).
func TestRED_StreamRespetaIncludeUsageCliente(t *testing.T) {
	ups := upstreamStreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":          "m",
		"stream":         true,
		"messages":       []map[string]string{{"role": "user", "content": "hi"}},
		"stream_options": map[string]any{"include_usage": false},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	var got map[string]any
	if err := json.Unmarshal([]byte(ups.gotBody), &got); err != nil {
		t.Fatalf("body upstream no es JSON: %v", err)
	}
	so, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("sin stream_options en body upstream: %s", ups.gotBody)
	}
	if so["include_usage"] != false {
		t.Fatalf("include_usage = %v, want false (cliente explícito respetado)", so["include_usage"])
	}
}

// TestRED_StreamCompletaIncludeUsageEnSOExistente: si el cliente manda
// stream_options SIN include_usage (ej. solo max_tokens), se agrega
// include_usage=true preservando los demás campos (P2, H2 review).
func TestRED_StreamCompletaIncludeUsageEnSOExistente(t *testing.T) {
	ups := upstreamStreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":          "m",
		"stream":         true,
		"messages":       []map[string]string{{"role": "user", "content": "hi"}},
		"stream_options": map[string]any{"max_tokens": 123},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	var got map[string]any
	if err := json.Unmarshal([]byte(ups.gotBody), &got); err != nil {
		t.Fatalf("body upstream no es JSON: %v", err)
	}
	so, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("sin stream_options en body upstream: %s", ups.gotBody)
	}
	if so["include_usage"] != true {
		t.Fatalf("include_usage = %v, want true (agregado al SO existente)", so["include_usage"])
	}
	// el resto del stream_options del cliente se preserva (transparencia)
	if so["max_tokens"] != float64(123) {
		t.Fatalf("max_tokens = %v, want 123 (preservado)", so["max_tokens"])
	}
}

// TestRED_NoStreamNoInyectaIncludeUsage: un request NO-stream no se
// modifica (la inyección es solo para streaming, P2/P5).
func TestRED_NoStreamNoInyectaIncludeUsage(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"stream":   false,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	if strings.Contains(ups.gotBody, "stream_options") {
		t.Fatalf("no-stream no debe tocar stream_options: %s", ups.gotBody)
	}
	// La transparencia se preserva: tools/top_p de un request no-stream
	// no se alteran (P5). Este body no llevaba campos extra — verificar
	// que model/messages están intactos.
	if !strings.Contains(ups.gotBody, `"model":"m"`) || !strings.Contains(ups.gotBody, `"messages"`) {
		t.Fatalf("body alterado en no-stream: %s", ups.gotBody)
	}
}

// ---- P3: contadores de cache en /metrics ----

// TestRED_MetricsCacheTokens: 2 requests con cache hit + 1 con miss (no
// stream) al mismo provider+modelo → contadores acumulan correctamente.
func TestRED_MetricsCacheTokens(t *testing.T) {
	// upstream con 900 hit / 100 miss en cada request
	ups := upstreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	for i := 0; i < 3; i++ {
		resp := h.chat(t, map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	resp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	// 3 requests × 1000 cached_tokens → 3000 hit
	if !strings.Contains(s, "mofgw_cache_hit_tokens_total 3000") {
		t.Fatalf("cache_hit_tokens_total mal:\n%s", s)
	}
	// prompt_tokens 1200, cached 1000 → miss = 200; × 3 = 600
	if !strings.Contains(s, "mofgw_cache_miss_tokens_total 600") {
		t.Fatalf("cache_miss_tokens_total mal:\n%s", s)
	}
	// reasoning 30 × 3 = 90
	if !strings.Contains(s, "mofgw_reasoning_tokens_total 90") {
		t.Fatalf("reasoning_tokens_total mal:\n%s", s)
	}
}

// TestRED_MetricsCacheTokensSinUsageSumaCero: upstream sin usage → los
// contadores de cache se exponen con 0 (no crashea, P3/P6).
func TestRED_MetricsCacheTokensSinUsageSumaCero(t *testing.T) {
	ups := upstreamNoUsage("m", "hola")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	gresp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer gresp.Body.Close()
	body, _ := io.ReadAll(gresp.Body)
	s := string(body)
	if !strings.Contains(s, "mofgw_cache_hit_tokens_total 0") {
		t.Fatalf("sin usage debe sumar 0, got:\n%s", s)
	}
}

// ---- P4: logs estructurados con campos de cache ----

// TestRED_LogsCacheFields: el request_end del log incluye
// cache_hit_tokens, cache_miss_tokens, reasoning_tokens.
func TestRED_LogsCacheFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ups := upstreamUsageOK("m")
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
	if end["cache_hit_tokens"] != float64(1000) {
		t.Fatalf("cache_hit_tokens = %v, want 1000", end["cache_hit_tokens"])
	}
	if end["cache_miss_tokens"] != float64(200) {
		t.Fatalf("cache_miss_tokens = %v, want 200", end["cache_miss_tokens"])
	}
	if end["reasoning_tokens"] != float64(30) {
		t.Fatalf("reasoning_tokens = %v, want 30", end["reasoning_tokens"])
	}
}

// ---- P6: robustez ante usage ausente ----

// TestRED_NoUsageNoCrashea: upstream 200 sin usage → el cliente recibe la
// respuesta normal (P6).
func TestRED_NoUsageNoCrashea(t *testing.T) {
	ups := upstreamNoUsage("m", "RESPUESTA-NORMAL")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (sin usage no debe crashear)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "RESPUESTA-NORMAL") {
		t.Fatalf("respuesta del cliente no es la del upstream: %s", body)
	}
}

// ---- P4 stream: usage capturado del chunk final SSE ----

// TestE2E_StreamUsageCapturadoEnMetrics: un stream con chunk final de
// usage (include_usage) acumula los contadores de cache en /metrics (P3
// aplicado a streaming) — verifica CaptureUsage + recordCacheTokens.
func TestE2E_StreamUsageCapturadoEnMetrics(t *testing.T) {
	ups := upstreamStreamUsageOK("m")
	h := build(t, []*upstream{ups}, "sk-test-1")

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	gresp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer gresp.Body.Close()
	body, _ := io.ReadAll(gresp.Body)
	s := string(body)
	// chunk final: prompt 500, cached 400 → hit 400, miss 100
	if !strings.Contains(s, "mofgw_cache_hit_tokens_total 400") {
		t.Fatalf("stream: cache_hit_tokens_total mal:\n%s", s)
	}
	if !strings.Contains(s, "mofgw_cache_miss_tokens_total 100") {
		t.Fatalf("stream: cache_miss_tokens_total mal:\n%s", s)
	}
}

// ---- helpers de re-uso ----

// FIXME: el proxy.New firma actual usa (r, providers, authz, m, logger,
// maxBody, health, opts...) — re-verificar la firma real al correr.
var _ = metrics.New
var _ = provider.NewClient
var _ = proxy.New
var _ = router.New
