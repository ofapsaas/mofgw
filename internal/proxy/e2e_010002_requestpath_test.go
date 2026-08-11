// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 010-002-request-path-fiel: el request path
// respeta el catálogo — clamp por modelo (max_output), window-check fiel
// (008-001 con max_tokens EFECTIVO post-clamp) e inyección per-attempt
// del thinking_default prescriptivo (provider-aware). Verifican los
// criterios C1-C12 del spec draft 010-002 (artifact printed-lavender-bee).
//
// Contrato observable: un upstream fake captura el body que recibe
// (upstream.gotBody) — cada criterio se determina por el body del fake,
// el status HTTP o el conteo de intentos. Cero red externa.
//
// NOTA de contrato (path declarativo): el path del provider (zen/bailian)
// es un knob DECLARATIVO por provider (prod: providers[].thinking_path,
// valores "" | "zen" | "bailian", default "" = sin inyección — ADOPTADO).
// En este harness el router se construye directo de ProviderSpec y el ID
// del provider (provider.NewClient) lleva el label del path: los tests
// FIJAN el path por construcción. Si se adopta un campo
// ProviderSpec.ThinkingPath, el cambio es UN punto: el helper buildRP.
//
// RED por aserción: la implementación actual no clampea por modelo ni
// inyecta thinking → los tests que exigen el comportamiento nuevo fallan
// por campos ausentes/incorrectos en gotBody (AssertionError), no por
// compilación. Los criterios de no-cambio (C4/C5/C6/C8b/C8c/C11/C12) son
// guards de regresión y pasan desde el día 1.

package proxy_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- harness local: providers configurables (id/path, modelo, MaxTokens) ----

// rpProvider describe un provider fake del harness con path declarativo.
type rpProvider struct {
	id        string // ID del provider = label de path ("" | "zen" | "bailian")
	model     string // modelo que sirve
	maxTokens int64  // MaxTokens del provider (001-004; 0 = sin límite)
	ups       *upstream
}

// buildRP construye el harness con N providers configurables. Orden de la
// cadena de fallback = orden de provs. Setea h.proxySrv para
// SetModelMetadata/SetContextMargin.
func buildRP(t *testing.T, clientKey string, provs ...rpProvider) *harness {
	t.Helper()
	ups := make([]*upstream, len(provs))
	specs := make([]router.ProviderSpec, len(provs))
	providers := make([]provider.Provider, len(provs))
	for i, p := range provs {
		ups[i] = p.ups
		srv := httptest.NewServer(p.ups.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient(p.id, srv.URL, "sk-upstream", []string{p.model}, p.maxTokens, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, nil, 10<<20, nil, nil, 0)
	h := &harness{ups: ups, key: clientKey, proxySrv: s}
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// ---- helpers de aserción sobre el body capturado por el fake ----

// bodyField parsea el body que recibió un upstream fake.
func bodyField(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("body upstream no es JSON: %v: %q", err, raw)
	}
	return m
}

func wantBodyMaxTokens(t *testing.T, raw string, want int64) {
	t.Helper()
	got, ok := bodyField(t, raw)["max_tokens"]
	if !ok || got != float64(want) {
		t.Fatalf("max_tokens upstream = %v, want %d (body: %s)", got, want, raw)
	}
}

func wantBodyString(t *testing.T, raw, field, want string) {
	t.Helper()
	got, ok := bodyField(t, raw)[field]
	if !ok || got != want {
		t.Fatalf("%s upstream = %v, want %q (body: %s)", field, got, want, raw)
	}
}

func wantBodyBool(t *testing.T, raw, field string, want bool) {
	t.Helper()
	got, ok := bodyField(t, raw)[field]
	if !ok || got != want {
		t.Fatalf("%s upstream = %v, want %v (body: %s)", field, got, want, raw)
	}
}

func wantBodyAbsent(t *testing.T, raw, field string) {
	t.Helper()
	if _, ok := bodyField(t, raw)[field]; ok {
		t.Fatalf("%s presente en body upstream, want ausente: %s", field, raw)
	}
}

// ---- fixtures de metadata (007-001 / 010-001) ----

// metaKimi: kimi-k2.7-code. context_window amplio para AISLAR el clamp
// por modelo del window-check (el window-check fiel se testea en C8); el
// contrato de C1 es el valor max_output 32768.
func metaKimi() config.ModelMetadata {
	return config.ModelMetadata{
		ContextWindow:   1000000,
		MaxOutput:       32768,
		Thinking:        []string{"always"},
		ThinkingDefault: "always",
	}
}

func metaFlash() config.ModelMetadata {
	return config.ModelMetadata{
		ContextWindow:   1000000,
		MaxOutput:       384000,
		Thinking:        []string{"low", "high", "max"},
		ThinkingDefault: "low",
	}
}

func metaQwen() config.ModelMetadata {
	return config.ModelMetadata{
		ContextWindow:   1000000,
		MaxOutput:       131072,
		Thinking:        []string{"high"},
		ThinkingDefault: "high",
	}
}

func metaMinimax() config.ModelMetadata {
	return config.ModelMetadata{
		ContextWindow:   1000000,
		MaxOutput:       128000,
		Thinking:        []string{"adaptive"},
		ThinkingDefault: "adaptive",
	}
}

func metaWindow() config.ModelMetadata {
	return config.ModelMetadata{ContextWindow: 1000, MaxOutput: 100}
}

// ---- C1: clamp por modelo (P1) ----

// C1: kimi con max_tokens 384000 → 200 y upstream recibe EXACTAMENTE
// max_tokens == 32768 (sin 400, sin 502). RED: hoy el upstream recibe
// 65536 (clamp por provider 001-004; no hay clamp por modelo).
func TestE2E010002_C1_KimiClampPorModelo(t *testing.T) {
	u := upstreamOK("m", "x")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 65536, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaKimi()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":      "m",
		"max_tokens": 384000,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (sin 400/sin 502)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if u.gotBody == "" {
		t.Fatal("el request no llegó al upstream")
	}
	wantBodyMaxTokens(t, u.gotBody, 32768)
}

// ---- C2/C3: inyección per-attempt provider-aware (P4) ----

// C2: flash vía zen sin effort → upstream recibe reasoning_effort == "low"
// y el resto del body (model, top_p, tools, messages) intacto; zen NO
// recibe enable_thinking. RED: falta reasoning_effort.
func TestE2E010002_C2_FlashZenInyectaLow(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 384000, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model": "m", "top_p": 0.5,
		"tools":    []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "parameters": map[string]any{}}}},
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	wantBodyString(t, u.gotBody, "reasoning_effort", "low")
	got := bodyField(t, u.gotBody)
	if got["model"] != "m" {
		t.Fatalf("model upstream = %v, want m (resto del body intacto)", got["model"])
	}
	if got["top_p"] != 0.5 {
		t.Fatalf("top_p upstream = %v, want 0.5 (resto del body intacto)", got["top_p"])
	}
	if _, ok := got["tools"]; !ok {
		t.Fatal("tools perdidos en el round-trip")
	}
	wantBodyAbsent(t, u.gotBody, "enable_thinking")
}

// C3: flash vía bailian sin effort → reasoning_effort == "low" Y
// enable_thinking == true. RED: faltan ambos.
func TestE2E010002_C3_FlashBailianInyectaLowYEnable(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "bailian", model: "m", maxTokens: 384000, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	wantBodyString(t, u.gotBody, "reasoning_effort", "low")
	wantBodyBool(t, u.gotBody, "enable_thinking", true)
}

// ---- C4: respeto al effort explícito (P5) ----

// C4: flash con reasoning_effort "high" explícito vía zen → passthrough
// (high intacto), CERO parámetros de thinking agregados. Guard: verde hoy
// (no hay inyección); protege P5 cuando P4 aterrice.
func TestE2E010002_C4_EffortExplicitoSinOverride(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 384000, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model": "m", "reasoning_effort": "high",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	got := bodyField(t, u.gotBody)
	if got["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort upstream = %v, want high (passthrough, cero override)", got["reasoning_effort"])
	}
	wantBodyAbsent(t, u.gotBody, "enable_thinking")
	wantBodyAbsent(t, u.gotBody, "thinking")
}

// ---- C5/C6: kimi y minimax nunca se inyectan (P6) ----

// C5: kimi sin effort → NINGÚN parámetro de thinking agregado (siempre-on;
// disabled → error upstream). Guard: verde hoy.
func TestE2E010002_C5_KimiSinInyeccion(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 65536, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaKimi()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	for _, f := range []string{"reasoning_effort", "enable_thinking", "thinking"} {
		wantBodyAbsent(t, u.gotBody, f)
	}
}

// C6: minimax-m3 sin effort → sin parámetros agregados (nativo adaptive ==
// prescriptivo adaptive; par sin parámetro verificado → fallback rule).
// Guard: verde hoy.
func TestE2E010002_C6_MinimaxSinInyeccion(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 128000, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaMinimax()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	for _, f := range []string{"reasoning_effort", "enable_thinking", "thinking"} {
		wantBodyAbsent(t, u.gotBody, f)
	}
}

// ---- C7: qwen vía bailian (P4c) ----

// C7: qwen3.7-plus vía bailian sin effort → enable_thinking == true y NO
// reasoning_effort (la tabla §5.4 solo activa enable_thinking). RED:
// falta enable_thinking.
func TestE2E010002_C7_QwenBailianEnableThinking(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := buildRP(t, "sk-test-1", rpProvider{id: "bailian", model: "m", maxTokens: 131072, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaQwen()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	wantBodyBool(t, u.gotBody, "enable_thinking", true)
	wantBodyAbsent(t, u.gotBody, "reasoning_effort")
}

// ---- C8: window-check fiel (P3) ----

// C8: modelo context_window 1000, max_output 100, margin 0.
// (a) prompt ~750 + max_tokens 400 → 200 y upstream con max_tokens == 100
//
//	(750 + min(400,100) = 850 ≤ 1000). RED hoy: 400 (raw 750+400 > 1000).
//
// (b) prompt ~1250 + max_tokens 400 → 400 invalid_request_error, CERO
//
//	intentos upstream. Guard (rechaza con la regla vieja y la nueva).
//
// (c) sin metadata, mismo body (b) → 200, max_tokens crudo 400. Guard.
func TestE2E010002_C8_WindowFiel(t *testing.T) {
	t.Run("a_prompt750_max400_cabe_y_clampea", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		h := buildMultiClient(t, []*upstream{u}, "sk-test-1")
		h.proxySrv.SetContextMargin(0)
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaWindow()})

		body := bigBody(3000) // ~750 tokens estimados
		body["max_tokens"] = 400
		resp := h.chatAs(t, "sk-test-1", body)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200 (750+min(400,100)=850 ≤ 1000)", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		if u.gotBody == "" {
			t.Fatal("request que cabe no llegó al upstream")
		}
		wantBodyMaxTokens(t, u.gotBody, 100)
	})

	t.Run("b_prompt1250_max400_rechaza", func(t *testing.T) {
		u := upstreamOK("m", "x")
		h := buildMultiClient(t, []*upstream{u}, "sk-test-1")
		h.proxySrv.SetContextMargin(0)
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaWindow()})

		body := bigBody(5000) // ~1250 tokens estimados
		body["max_tokens"] = 400
		resp := h.chatAs(t, "sk-test-1", body)
		if resp.StatusCode != 400 {
			t.Fatalf("status = %d, want 400 (1250+100 > 1000)", resp.StatusCode)
		}
		raw, _ := io.ReadAll(resp.Body)
		var errBody map[string]any
		_ = json.Unmarshal(raw, &errBody)
		errObj, _ := errBody["error"].(map[string]any)
		if errObj["type"] != "invalid_request_error" {
			t.Fatalf("error.type = %v, want invalid_request_error: %s", errObj["type"], raw)
		}
		if u.gotBody != "" {
			t.Fatalf("hubo intento upstream tras rechazo por ventana: %s", u.gotBody)
		}
	})

	t.Run("c_sin_metadata_200_raw", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		h := buildMultiClient(t, []*upstream{u}, "sk-test-1")
		// sin SetModelMetadata

		body := bigBody(5000)
		body["max_tokens"] = 400
		resp := h.chatAs(t, "sk-test-1", body)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200 (sin metadata, backward compatible)", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		if u.gotBody == "" {
			t.Fatal("sin metadata el request debe llegar al upstream")
		}
		// max_tokens crudo: sin clamp por modelo (nil-safe P8) ni por provider (4096)
		wantBodyMaxTokens(t, u.gotBody, 400)
	})
}

// ---- C9: stream y no-stream cubiertos (P7) ----

// C9a: stream flash vía zen sin effort → upstream recibe reasoning_effort
// "low" Y stream_options.include_usage true (003-001 intacto); el SSE
// llega completo al cliente. RED: falta reasoning_effort.
func TestE2E010002_C9a_StreamFlashZenInyectaYUsage(t *testing.T) {
	u := upstreamStreamOK("m")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 384000, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model": "m", "stream": true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "data: [DONE]") {
		t.Fatalf("SSE incompleto: %q", raw)
	}

	wantBodyString(t, u.gotBody, "reasoning_effort", "low")
	so, ok := bodyField(t, u.gotBody)["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options.include_usage no inyectado (003-001): %s", u.gotBody)
	}
}

// C9b: stream kimi con max_tokens 384000 → upstream recibe max_tokens ==
// 32768 y el SSE llega completo. RED: hoy recibe 65536 (clamp por
// provider; no hay clamp por modelo).
func TestE2E010002_C9b_StreamKimiClampea(t *testing.T) {
	u := upstreamStreamOK("m")
	h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 65536, ups: u})
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaKimi()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model": "m", "stream": true, "max_tokens": 384000,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "data: [DONE]") {
		t.Fatalf("SSE incompleto: %q", raw)
	}

	wantBodyMaxTokens(t, u.gotBody, 32768)
}

// ---- C10: cadena [zen, bailian] con 429 (P9 + P4) ----

// C10: flash servida por cadena [zen, bailian]; zen responde 429 → el
// intento 1 (zen) recibió reasoning_effort "low" SOLO; el intento 2
// (bailian) recibió reasoning_effort "low" + enable_thinking true; el
// cliente recibe 200 del segundo (fallback intacto). RED: faltan los
// campos de inyección en ambos intentos.
func TestE2E010002_C10_CadenaZenBailian(t *testing.T) {
	zen := upstreamFail(429)
	bailian := upstreamOK("m", "rescate")
	h := buildRP(t, "sk-test-1",
		rpProvider{id: "zen", model: "m", maxTokens: 384000, ups: zen},
		rpProvider{id: "bailian", model: "m", maxTokens: 384000, ups: bailian},
	)
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (fallback intacto)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// intento 1 (zen): reasoning_effort low, SIN enable_thinking
	if zen.gotBody == "" {
		t.Fatal("zen (primer intento) no recibió request")
	}
	wantBodyString(t, zen.gotBody, "reasoning_effort", "low")
	wantBodyAbsent(t, zen.gotBody, "enable_thinking")

	// intento 2 (bailian): reasoning_effort low + enable_thinking true
	if bailian.gotBody == "" {
		t.Fatal("bailian (segundo intento) no recibió request")
	}
	wantBodyString(t, bailian.gotBody, "reasoning_effort", "low")
	wantBodyBool(t, bailian.gotBody, "enable_thinking", true)
}

// ---- C11: 4xx no-reintentable (P9) ----

// C11: cadena de dos providers, el primero responde 400 upstream → el
// cliente recibe 400 y el segundo provider NO recibe ningún request.
// Guard: verde hoy.
func TestE2E010002_C11_400NoReintenta(t *testing.T) {
	p1 := upstreamFail(400)
	p2 := upstreamOK("m", "nunca")
	h := buildRP(t, "sk-test-1",
		rpProvider{id: "zen", model: "m", maxTokens: 4096, ups: p1},
		rpProvider{id: "bailian", model: "m", maxTokens: 4096, ups: p2},
	)

	resp := h.chatAs(t, "sk-test-1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (4xx no-reintentable)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if p2.gotBody != "" {
		t.Fatalf("el segundo provider recibió un request tras 400: %s", p2.gotBody)
	}
}

// ---- C12: sin metadata / efectivo = min (P2 + P8) ----

// C12: (a) sin metadata + provider 4096 → 4096 (clamp por provider
// actual); (b) sin metadata + provider sin límite → 384000 crudo;
// (c) P2: metadata max_output 384000 + provider 4096 → 4096 (min);
// (d) P2: metadata 384000 + provider sin límite → 384000 (sin límite NO
// relaja el clamp por modelo). Guards: verdes hoy.
func TestE2E010002_C12_SinMetadataYMinEfectivo(t *testing.T) {
	t.Run("a_provider_4096_sin_metadata", func(t *testing.T) {
		u := upstreamOK("m", "x")
		h := build(t, []*upstream{u}, "sk-test-1") // provider MaxTokens 4096
		resp := h.chat(t, map[string]any{
			"model": "m", "max_tokens": 384000,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		wantBodyMaxTokens(t, u.gotBody, 4096)
	})

	t.Run("b_provider_sin_limite_sin_metadata", func(t *testing.T) {
		u := upstreamOK("m", "x")
		h := buildRP(t, "sk-test-1", rpProvider{id: "", model: "m", maxTokens: 0, ups: u})
		resp := h.chatAs(t, "sk-test-1", map[string]any{
			"model": "m", "max_tokens": 384000,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		wantBodyMaxTokens(t, u.gotBody, 384000)
	})

	t.Run("c_metadata_y_provider_limita", func(t *testing.T) {
		u := upstreamOK("m", "x")
		h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 4096, ups: u})
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})
		resp := h.chatAs(t, "sk-test-1", map[string]any{
			"model": "m", "max_tokens": 384000,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		wantBodyMaxTokens(t, u.gotBody, 4096) // min(max_output 384000, provider 4096)
	})

	t.Run("d_metadata_sin_limite_provider", func(t *testing.T) {
		u := upstreamOK("m", "x")
		h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 0, ups: u})
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})
		resp := h.chatAs(t, "sk-test-1", map[string]any{
			"model": "m", "max_tokens": 384000,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		wantBodyMaxTokens(t, u.gotBody, 384000) // sin límite no relaja el clamp por modelo
	})
}
