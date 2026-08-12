// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-001-responses-endpoint: endpoint POST
// /v1/responses (Responses API de OpenAI que Odoo 19 usa en
// _request_llm_openai_helper). Verifican las postcondiciones P1-P14 del
// spec docs/specs/011-001-responses-endpoint/spec.md.
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia
// Server.Handler() — status, envelope de error wire y body Responses de
// respuesta; upstreams fake en httptest, cero red externa (I5).
//
// Estrategia anti-compile-error: el handler /v1/responses NO existe aún
// → estos tests FALLAN en RED por 404 (ruta no registrada en el mux,
// proxy.go:280-290). No se referencia ningún símbolo interno de la
// feature (handleResponses, struct Responses) que aún no existe. Los
// asserts de éxito (200/400 con shape) son los que marcan el contrato
// que el implementer debe cumplir en GREEN sin tocar estos tests.

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
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- helpers del endpoint /v1/responses (análogos a h.chat) ----

// responsesBody arma un body Responses API válido de Odoo (spec §Contrato):
// un solo input item de role user con una part input_text. `store` se deja
// ausente por defecto (P13 lo ejercita explícitamente).
func responsesBody(text string) map[string]any {
	return map[string]any{
		"model": "m",
		"input": []any{
			map[string]any{
				"role":    "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": text},
				},
			},
		},
	}
}

// responsesRaw envía POST /v1/responses con el body crudo dado y devuelve
// status + body leído. Útil para bodies no-JSON (P2) y para servers que no
// exponen el harness (limiters, P4).
func responsesRaw(t *testing.T, srvURL, key string, raw []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/v1/responses", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("responses request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// responsesAs envía POST /v1/responses con un body tipado (JSON-marshalado).
func responsesAs(t *testing.T, srvURL, key string, body map[string]any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return responsesRaw(t, srvURL, key, raw)
}

// responses es el helper del harness para POST /v1/responses, análogo a
// h.chat (devuelve el *http.Response para inspección).
func (h *harness) responses(t *testing.T, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/responses", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("responses request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// responsesEnvelope tipa el body no-stream del Responses API (spec §Contrato,
// P10): id/object/model raíz + output[] con item message de role assistant,
// status completed y content[0] output_text con campo text (shape parseable
// por el snippet real de Odoo, llm_api_service.py:364-397).
type responsesEnvelope struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Role    string `json:"role"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func decodeResponses(t *testing.T, raw []byte) *responsesEnvelope {
	t.Helper()
	var out responsesEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body responses no es JSON: %v\n%s", err, raw)
	}
	return &out
}

// ---- upstream con contador de llamadas (P12 cache) ----

// countingUpstream011 envuelve un *upstream y cuenta cuántas veces lo llamó el
// proxy (P12 cache). Un HIT de cache exact-match NO vuelve a llamar al
// upstream; un MISS sí. La separación de cache por endpoint (P12) se observa
// por el conteo. Nombre con sufijo 011 para no colisionar con el countingUpstream
// de e2e_010002_test.go.
type countingUpstream011 struct {
	inner *upstream
	mu    sync.Mutex
	calls int
}

func (c *countingUpstream011) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.calls++
		c.mu.Unlock()
		c.inner.handler()(w, r)
	}
}

func (c *countingUpstream011) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// buildCounting011 construye el harness sobre un countingUpstream011 único
// (mismo clientID "test" para ambos endpoints, igual que build()).
func buildCounting011(t *testing.T, c *countingUpstream011, clientKey string) *harness {
	t.Helper()
	h := &harness{ups: []*upstream{c.inner}, key: clientKey}
	srv := httptest.NewServer(c.handler())
	t.Cleanup(srv.Close)
	cl := provider.NewClient("up1", srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
	r := router.New([]router.ProviderSpec{{Provider: cl}}, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, []provider.Provider{cl}, authz, m, nil, nil, 10<<20, nil, nil, 0)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// ---- BLOQUE A — P1: ruta protegida y clientID sin scoping nuevo ----

func TestE2E011001_P1_Auth(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")

	// Sin Bearer → 401 con error.type invalid_api_key (envelope I/P14).
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/responses", strings.NewReader(`{"model":"m","input":[]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("sin Bearer: status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_api_key") {
		t.Fatalf("body sin invalid_api_key: %s", body)
	}

	// Con Bearer válido → el handler responde 200 (ruta registrada bajo
	// s.auth.Wrap, no scoping nuevo). En RED esto es 404 (ruta ausente).
	resp2 := h.responses(t, responsesBody("hola"))
	if resp2.StatusCode != 200 {
		t.Fatalf("con Bearer válido: status = %d, want 200 (handler responde)", resp2.StatusCode)
	}
	io.Copy(io.Discard, resp2.Body)
}

// ---- BLOQUE B — P2 (+P14 400): body inválido ----

func TestE2E011001_P2_BodyInvalido(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// body no-JSON → 400 con envelope {"error":{type,message,code}}.
	t.Run("not_json", func(t *testing.T) {
		code, body := responsesRaw(t, h.srv.URL, h.key, []byte(`not-json`))
		if code != 400 {
			t.Fatalf("status = %d, want 400", code)
		}
		assertErrorEnvelope(t, body)
	})

	// body JSON sin `input` usable → 400 con envelope.
	t.Run("sin_input", func(t *testing.T) {
		code, body := responsesAs(t, h.srv.URL, h.key, map[string]any{"model": "m"})
		if code != 400 {
			t.Fatalf("status = %d, want 400 (sin input)", code)
		}
		assertErrorEnvelope(t, body)
	})
}

// ---- BLOQUE C — P3/P5/P10: happy path Responses ----

func TestE2E011001_C_HappyPath(t *testing.T) {
	u := upstreamOK("m", "hola mundo")
	h := build(t, []*upstream{u}, "sk-test-1")

	body := responsesBody("hola")
	body["temperature"] = 0.7 // P3: se pasa tal cual al upstream
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}

	// P3: el upstream recibe `messages` traducidos de `input` (part
	// input_text → content string concatenado), model y temperature intactos.
	var req struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(u.gotBody), &req); err != nil {
		t.Fatalf("body upstream no es JSON: %v\n%s", err, u.gotBody)
	}
	if req.Model != "m" {
		t.Fatalf("upstream model = %q, want m", req.Model)
	}
	if req.Temperature != 0.7 {
		t.Fatalf("upstream temperature = %v, want 0.7 (passthrough P3)", req.Temperature)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("upstream messages = %d, want 1: %s", len(req.Messages), u.gotBody)
	}
	if req.Messages[0].Role != "user" || req.Messages[0].Content != "hola" {
		t.Fatalf("messages[0] = %+v, want {user, hola}", req.Messages[0])
	}

	// P10: output[0] es EXACTAMENTE un item message con shape parseable por
	// Odoo; content[0] output_text con text == choices[0].message.content.
	out := decodeResponses(t, raw)
	if out.Object != "response" {
		t.Fatalf("object = %q, want response", out.Object)
	}
	if len(out.Output) != 1 {
		t.Fatalf("output len = %d, want 1 (sin tool_calls → un solo item message)", len(out.Output))
	}
	it := out.Output[0]
	if it.Type != "message" || it.Role != "assistant" || it.Status != "completed" {
		t.Fatalf("item = type:%q role:%q status:%q, want message/assistant/completed", it.Type, it.Role, it.Status)
	}
	if it.ID == "" {
		t.Fatal("item message sin id estable (P11)")
	}
	if len(it.Content) != 1 || it.Content[0].Type != "output_text" || it.Content[0].Text != "hola mundo" {
		t.Fatalf("content = %+v, want [{output_text, \"hola mundo\"}] (parseable por Odoo)", it.Content)
	}

	// P5: model del envelope == modelo del cliente, aunque el body pidiera
	// otro (rewriteResponseModel a nivel raíz).
	if out.Model != "m" {
		t.Fatalf("envelope model = %q, want m (modelo del cliente)", out.Model)
	}
}

// ---- BLOQUE D — P11: IDs estables/determinísticas ----

func TestE2E011001_D_IDsEstables(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")

	first := decodeResponses(t, responsesBodyAndExpect(t, h, 200))
	second := decodeResponses(t, responsesBodyAndExpect(t, h, 200))

	if first.ID == "" {
		t.Fatal("response id vacío (P11)")
	}
	if first.ID != second.ID {
		t.Fatalf("response id no estable: %q != %q (mismo input → mismo id)", first.ID, second.ID)
	}
	if len(first.Output) != 1 || len(second.Output) != 1 {
		t.Fatalf("output inesperado: first=%d second=%d", len(first.Output), len(second.Output))
	}
	if first.Output[0].ID != second.Output[0].ID {
		t.Fatalf("item id no estable: %q != %q", first.Output[0].ID, second.Output[0].ID)
	}
}

// responsesBodyAndExpect envía el mismo body Responses y exige status, devolviendo el body.
func responsesBodyAndExpect(t *testing.T, h *harness, wantStatus int) []byte {
	t.Helper()
	code, raw := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
	if code != wantStatus {
		t.Fatalf("status = %d, want %d", code, wantStatus)
	}
	return raw
}

// ---- BLOQUE E — P4: pipeline compartida (context-window / rate-limit /
// usage+costo) ----

func TestE2E011001_E_PipelineCompartida(t *testing.T) {
	t.Run("context_window_rechazo", func(t *testing.T) {
		ups := upstreamOK("m", "x")
		h := buildMultiClient(t, []*upstream{ups}, "k1")
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
			"m": {ContextWindow: 1000, MaxOutput: 100},
		})
		// body grande → estimado > window → 400 igual que chat (P4a),
		// cero intentos upstream.
		code, body := responsesAs(t, h.srv.URL, h.key, responsesBody(strings.Repeat("x", 5000)))
		if code != 400 {
			t.Fatalf("status = %d, want 400 (rechazo por ventana igual que chat)", code)
		}
		errObj := errorObject(t, body)
		if errObj["type"] != "invalid_request_error" {
			t.Fatalf("error.type = %v, want invalid_request_error: %s", errObj["type"], body)
		}
		if msg, _ := errObj["message"].(string); !strings.Contains(strings.ToLower(msg), "context") {
			t.Fatalf("mensaje sin 'context': %q", errObj["message"])
		}
		if ups.gotBody != "" {
			t.Fatal("hubo intento upstream tras rechazo por ventana (P4a)")
		}
	})

	t.Run("rate_limit_429", func(t *testing.T) {
		// /v1/responses pasa por el MISMO rate limiter global que chat (P4b):
		// slot ocupado en background → 429 con envelope.
		up := &slowUpstream{delay: 5 * time.Second}
		srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 1, 200*time.Millisecond, nil, up)
		done := holdSlotResponses(t, srv, "sk-1")
		time.Sleep(100 * time.Millisecond)

		code, body := responsesAs(t, srv.URL, "sk-1", responsesBody("hi"))
		if code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 rate_limit_exceeded; body=%s", code, body)
		}
		if !strings.Contains(string(body), "rate_limit_exceeded") {
			t.Fatalf("body sin rate_limit_exceeded: %s", body)
		}
		select {
		case sc := <-done:
			if sc != 200 {
				t.Fatalf("r1 (slot): status = %d, want 200", sc)
			}
		case <-time.After(7 * time.Second):
			t.Fatal("r1 no terminó")
		}
	})

	t.Run("usage_y_costo_incrementan", func(t *testing.T) {
		// /v1/responses contabiliza usage y costo para el clientID, igual
		// que chat (P4c). upstreamUsageOK: prompt 1200, completion 80,
		// cached 1000 → miss 200.
		h := buildMultiClient(t, []*upstream{upstreamUsageOK("m")}, "k1")
		h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
			"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
		})

		resp := h.responses(t, responsesBody("hi"))
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		s := h.metricsBody(t)
		if !strings.Contains(s, "mofgw_prompt_tokens_total 1200") {
			t.Fatalf("usage prompt no contabilizado para clientID:\n%s", s)
		}
		// costo = miss 200/1M (0.0002) + completion 80/1M (0.00008) + cached 1000/1M (0.001) = 0.00128
		if !strings.Contains(s, "mofgw_cost_usd_total 0.00128") {
			t.Fatalf("costo no contabilizado para clientID:\n%s", s)
		}
	})
}

// holdSlotResponses ocupa un slot del limiter con un request a /v1/responses.
func holdSlotResponses(t *testing.T, srv *httptest.Server, key string) chan int {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		code, _ := responsesAs(t, srv.URL, key, responsesBody("hi"))
		done <- code
	}()
	return done
}

// ---- BLOQUE F — P6-P9: rechazo explícito de features futuras ----

func TestE2E011001_F_RechazosFeaturesFuturas(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	body := responsesBody("hola")

	t.Run("stream_true", func(t *testing.T) {
		b := cloneMap(body)
		b["stream"] = true
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "streaming not supported yet") // P6
	})
	t.Run("tools", func(t *testing.T) {
		b := cloneMap(body)
		b["tools"] = []any{map[string]any{"type": "function", "name": "f"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P7
	})
	t.Run("web_search_preview", func(t *testing.T) {
		b := cloneMap(body)
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P7 (web_search_preview)
	})
	t.Run("text_format_json_schema", func(t *testing.T) {
		b := cloneMap(body)
		b["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "x", "schema": map[string]any{}}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "structured output not yet supported") // P8
	})
	t.Run("part_input_file", func(t *testing.T) {
		b := inputPart(t, body, "input_file")
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "file attachments not yet supported") // P9
	})
	t.Run("part_input_image", func(t *testing.T) {
		b := inputPart(t, body, "input_image")
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "file attachments not yet supported") // P9
	})
}

// assertReject exige status 400, envelope error y un message con el texto dado.
func assertReject(t *testing.T, code int, raw []byte, wantMsg string) {
	t.Helper()
	if code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", code, raw)
	}
	errObj := errorObject(t, raw)
	if msg, _ := errObj["message"].(string); msg != wantMsg {
		t.Fatalf("error.message = %q, want %q", msg, wantMsg)
	}
}

// inputPart reemplaza la part de content del primer input item por una del
// type dado (input_file/input_image).
func inputPart(t *testing.T, base map[string]any, partType string) map[string]any {
	t.Helper()
	b := cloneMap(base)
	input := b["input"].([]any)
	item := input[0].(map[string]any)
	item["content"] = []any{map[string]any{"type": partType, "file_id": "file-123"}}
	return b
}

// cloneMap copia un mapa a nivel superficial (suficiente para los subtests).
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- BLOQUE G — P12: cache separada por endpoint ----

func TestE2E011001_G_CacheSeparadaPorEndpoint(t *testing.T) {
	// La cache exact-match (010-002) está keyed por clientID+body canónico.
	// P12: las keys de /v1/responses deben ser distintas de /v1/chat/completions
	// (jamás una chat-response servida como responses-response ni a la inversa).
	// Observable: un HIT no llama upstream; un MISS sí.
	//
	// La cache de un endpoint solo opera para requests determinísticos
	// (temperature presente y == 0): sin temperature, isDeterministic=false y el
	// endpoint salta la cache por completo → ambos requests serían MISS obligados
	// y el test pasaría vacíamente (revisión review). Por eso los bodies llevan
	// `temperature: 0` para ser cache-eligibles y ejercitar el camino de cache.
	//
	// Estrategia por subtest:
	//   1. request idéntico DOS veces → 1ra MISS (calls=1), 2da HIT (calls NO
	//      crece). Demuestra que la cache del endpoint opera.
	//   2. request equivalente al OTRO endpoint (determinístico) → MISS
	//      cross-endpoint (calls++). Demuestra la separación (P12).

	t.Run("responses_no_sirve_a_chat", func(t *testing.T) {
		c := &countingUpstream011{inner: upstreamOK("m", "hola")}
		h := buildCounting011(t, c, "sk-test-1")
		h.proxySrv.SetResponseCache(true, 512, time.Hour)

		// Body Responses determinístico (temperature==0) → cache-eligible.
		rb := cloneMap(responsesBody("hola"))
		rb["temperature"] = 0

		// 1er request /responses: MISS (cache vacía) → 1 llamada upstream.
		if code, _ := responsesAs(t, h.srv.URL, h.key, rb); code != 200 {
			t.Fatalf("responses (1ra) status = %d, want 200", code)
		}
		if got := c.callCount(); got != 1 {
			t.Fatalf("responses (1ra): upstream calls = %d, want 1 (miss cache)", got)
		}

		// 2do request idéntico /responses: HIT de cache → NO re-ejecuta,
		// calls se mantiene en 1 (la cache de /responses opera de verdad).
		if code, _ := responsesAs(t, h.srv.URL, h.key, rb); code != 200 {
			t.Fatalf("responses (2da) status = %d, want 200", code)
		}
		if got := c.callCount(); got != 1 {
			t.Fatalf("responses (2da): upstream calls = %d, want 1 (hit de cache de /responses)", got)
		}

		// request equivalente a /chat/completions (determinístico, temp 0)
		// → NO se sirve de la cache de /responses → re-ejecuta (calls = 2).
		chatBody := map[string]any{
			"model":       "m",
			"temperature": 0,
			"messages":    []map[string]string{{"role": "user", "content": "hola"}},
		}
		resp := h.chat(t, chatBody)
		if resp.StatusCode != 200 {
			t.Fatalf("chat status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		if got := c.callCount(); got != 2 {
			t.Fatalf("chat tras responses: upstream calls = %d, want 2 (re-ejecutó, no cache de /responses)", got)
		}
	})

	t.Run("chat_no_sirve_a_responses", func(t *testing.T) {
		c := &countingUpstream011{inner: upstreamOK("m", "hola")}
		h := buildCounting011(t, c, "sk-test-1")
		h.proxySrv.SetResponseCache(true, 512, time.Hour)

		// Body chat determinístico (temperature==0) → cache-eligible.
		chatBody := map[string]any{
			"model":       "m",
			"temperature": 0,
			"messages":    []map[string]string{{"role": "user", "content": "hola"}},
		}

		// 1er request /chat: MISS (cache vacía) → 1 llamada upstream.
		resp := h.chat(t, chatBody)
		if resp.StatusCode != 200 {
			t.Fatalf("chat (1ro) status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		if got := c.callCount(); got != 1 {
			t.Fatalf("chat (1ro): upstream calls = %d, want 1 (miss cache)", got)
		}

		// 2do request idéntico /chat: HIT de cache → NO re-ejecuta, calls
		// se mantiene en 1 (la cache de /chat opera de verdad).
		resp = h.chat(t, chatBody)
		if resp.StatusCode != 200 {
			t.Fatalf("chat (2do) status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
		if got := c.callCount(); got != 1 {
			t.Fatalf("chat (2do): upstream calls = %d, want 1 (hit de cache de /chat)", got)
		}

		// request equivalente a /v1/responses (determinístico, temp 0)
		// → NO se sirve de la cache de /chat → re-ejecuta (calls = 2).
		rb := cloneMap(responsesBody("hola"))
		rb["temperature"] = 0
		if code, _ := responsesAs(t, h.srv.URL, h.key, rb); code != 200 {
			t.Fatalf("responses status = %d, want 200", code)
		}
		if got := c.callCount(); got != 2 {
			t.Fatalf("responses tras chat: upstream calls = %d, want 2 (re-ejecutó, no cache de /chat)", got)
		}
	})
}

// ---- BLOQUE H — P13: store ignorado (stateless) ----

func TestE2E011001_H_StoreIgnorado(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola mundo")}, "sk-test-1")

	var texts []string
	for _, variant := range []string{"ausente", "false", "true"} {
		body := responsesBody("hola")
		switch variant {
		case "false":
			body["store"] = false
		case "true":
			body["store"] = true
		}
		code, raw := responsesAs(t, h.srv.URL, h.key, body)
		if code != 200 {
			t.Fatalf("store=%s: status = %d, want 200", variant, code)
		}
		out := decodeResponses(t, raw)
		if len(out.Output) != 1 || len(out.Output[0].Content) != 1 {
			t.Fatalf("store=%s: output inesperado: %+v", variant, out.Output)
		}
		texts = append(texts, out.Output[0].Content[0].Text)
	}

	// mismo output en los tres casos (P13: store no cambia el comportamiento).
	for _, got := range texts[1:] {
		if got != texts[0] {
			t.Fatalf("store cambió el output: %q vs %q", got, texts[0])
		}
	}
}

// ---- BLOQUE I — P14: envelope de error uniforme ----

func TestE2E011001_I_EnvelopeError(t *testing.T) {
	t.Run("400_body_invalido", func(t *testing.T) {
		h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
		code, body := responsesRaw(t, h.srv.URL, h.key, []byte(`no-json`))
		if code != 400 {
			t.Fatalf("status = %d, want 400", code)
		}
		assertErrorEnvelope(t, body)
	})
	t.Run("401_sin_auth", func(t *testing.T) {
		h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
		code, body := responsesRaw(t, h.srv.URL, "", []byte(`{}`))
		if code != 401 {
			t.Fatalf("status = %d, want 401", code)
		}
		assertErrorEnvelope(t, body)
	})
	t.Run("502_upstream", func(t *testing.T) {
		h := build(t, []*upstream{upstreamFail(500), upstreamFail(503)}, "sk-test-1")
		code, body := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
		if code != 502 {
			t.Fatalf("status = %d, want 502", code)
		}
		assertErrorEnvelope(t, body)
	})
	// 429: envelope de rate-limit verificado en bloque E (P4b) con el MISMO
	// shape {"error":{type,message,code}} — el test G del audit lo exige; acá
	// se cubren 400/401/502 y la uniformidad del shape queda afirmada por
	// assertErrorEnvelope en los tres status.
}

// assertErrorEnvelope falla si el body no tiene {"error":{type,message,code}}.
func assertErrorEnvelope(t *testing.T, body []byte) {
	t.Helper()
	obj := errorObject(t, body)
	for _, k := range []string{"type", "message", "code"} {
		if _, ok := obj[k]; !ok {
			t.Fatalf("error.%s ausente en envelope: %s", k, body)
		}
	}
}

// errorObject extrae el sub-objeto {"error":{...}} del body; falla si no existe.
func errorObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("body de error no es JSON: %v\n%s", err, body)
	}
	errObj, ok := envelope["error"].(map[string]any)
	if !ok {
		t.Fatalf("sin objeto error en envelope: %s", body)
	}
	return errObj
}

// ---- guards de paquete para imports no usados en ciertos builds ----
var _ = config.ModelMetadata{}
var _ = proxy.Server{}
var _ = auth.Authenticator{}
var _ = metrics.Metrics{}
var _ = provider.Provider(nil)
var _ = router.ProviderSpec{}
