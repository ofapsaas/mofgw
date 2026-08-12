// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-006-embeddings: endpoint POST /v1/embeddings
// (forward a Ollama, modelo de embeddings forzado por cliente). Verifican
// las postcondiciones P1-P10 del spec
// docs/specs/011-006-embeddings/spec.md.
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia
// Server.Handler() — status, envelope de error wire, body OpenAI-compatible
// de respuesta, headers X-Usage-*, y el wire upstream hacia Ollama
// (embeddingsUpstream.gotBody para P3/P5). Upstream fake de Ollama en
// httptest, cero red externa (I5/I2).
//
// Estrategia anti-compile-error (patrón 001, decisión del orquestador en el
// test-audit): el handler /v1/embeddings NO existe aún → estos tests FALLAN
// en RED por 404 (ruta no registrada en el mux). No se referencia ningún
// símbolo interno de la feature (handleEmbeddings, structs) ni los setters
// que el implementer agrega en GREEN (SetEmbeddings/SetClientEmbeddingsModel),
// porque no existen y romperían la compilación. Los asserts de éxito
// (200/400/429/502 con shape) son los que marcan el contrato que el
// implementer debe cumplir en GREEN.
//
// CONTRATO DE SETTERS (el implementer los implementa en GREEN, patrón
// SetWebSearch/SetBudget de 005/008):
//   - func (s *Server) SetEmbeddings(baseURL, apiKey string) — global:
//     apunta el cliente HTTP dedicado de embeddings a baseURL+"/embeddings"
//     (I2: nunca api.openai.com); apiKey opcional (Ollama local sin key).
//   - func (s *Server) SetClientEmbeddingsModel(clientID, model string) —
//     modelo de embeddings forzado por cliente (P3); cliente sin modelo →
//     P4 (400 fail-fast).
//
// El helper buildWithEmbeddings (comentado abajo) es el que en GREEN
// materializaría ese contrato; NO se define como código en este archivo
// porque referenciar los setters inexistentes rompería la compilación en
// RED. En RED los tests usan build/buildMultiClient + el request a
// /v1/embeddings y fallan por 404; el implementer/GREEN realiza el wiring.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
)

// ---- mock del upstream Ollama (POST <baseURL>/embeddings) ----

// embeddingsUpstream es un fake de Ollama para el flujo de embeddings
// (I2/I7: el forward de mofgw hace POST a base_url+"/embeddings", NO a
// /chat/completions). Registra el body recibido (gotBody, P3/P5), el path
// (gotPath, I1/I7) y devuelve un body OpenAI-compatible de embeddings con
// status configurable. Se configura para GREEN; en RED el endpoint mofgw da
// 404 antes de llegar acá, así que el mock no se alcanza.
type embeddingsUpstream struct {
	status     int
	body       string
	vectors    [][]float64 // vectores de data[].embedding (para assert verbatim, P6/I4)
	gotBody    string      // último body recibido
	gotPath    string      // path recibido (assert I1/I7 en C)
	gotHeaders http.Header
	calls      int // cuántas veces lo llamó el proxy
}

func (u *embeddingsUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u.calls++
		raw, _ := io.ReadAll(r.Body)
		u.gotBody = string(raw)
		u.gotPath = r.URL.Path
		u.gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.status)
		io.WriteString(w, u.body)
	}
}

// embeddingsResponseBody arma el body OpenAI-compatible de embeddings
// (P6): object list + data[] con un item embedding por vector (index i) +
// usage.prompt_tokens/total_tokens.
func embeddingsResponseBody(model string, vectors [][]float64, promptTokens int) string {
	data := make([]any, len(vectors))
	for i, v := range vectors {
		data[i] = map[string]any{"object": "embedding", "index": i, "embedding": v}
	}
	b, _ := json.Marshal(map[string]any{
		"object": "list",
		"data":   data,
		"model":  model,
		"usage":  map[string]any{"prompt_tokens": promptTokens, "total_tokens": promptTokens},
	})
	return string(b)
}

// embeddingsOK devuelve un upstream 200 que sirve `vectors` con
// prompt_tokens = promptTokens (P6/P7). `vectors` se guarda para comparar el
// vector devuelto verbatim (P6/I4).
func embeddingsOK(model string, vectors [][]float64, promptTokens int) *embeddingsUpstream {
	return &embeddingsUpstream{
		status:  200,
		vectors: vectors,
		body:    embeddingsResponseBody(model, vectors, promptTokens),
	}
}

// embeddingsFail devuelve un upstream que responde con el status dado y un
// error OpenAI-compatible (P9: 500/429 no-reintentable → mofgw 502).
func embeddingsFail(status int) *embeddingsUpstream {
	return &embeddingsUpstream{
		status: status,
		body:   `{"error":{"message":"ollama boom","type":"server_error"}}`,
	}
}

// CONTRATO buildWithEmbeddings (GREEN, implementer): el wiring del mock de
// Ollama + modelo por cliente. NO es código en este archivo porque los
// setters aún no existen (RED por 404). En GREEN reemplaza buildMultiClient:
//
//	h := buildMultiClient(t, ups, clientKey)               // clientID "client-"+clientKey
//	srv := httptest.NewServer(u.handler())                 // mock de Ollama /embeddings
//	h.proxySrv.SetEmbeddings(srv.URL, "")                  // base_url global (I2)
//	h.proxySrv.SetClientEmbeddingsModel("client-"+clientKey, "m") // modelo forzado (P3)
//
// Los tests que necesitan llegar a Ollama (C/E/F/G/H) asumen ese wiring.

// ---- helpers del endpoint /v1/embeddings (análogos a h.chat/responses) ----

// embeddings envía POST /v1/embeddings autenticado y devuelve el
// *http.Response para inspección (headers X-Usage-*, P7).
func (h *harness) embeddings(t *testing.T, key string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/embeddings", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("embeddings request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// embeddingsRaw envía POST /v1/embeddings con el body crudo dado y devuelve
// status + body leído. Útil para bodies no-JSON (P2) y sin auth (P1).
func embeddingsRaw(t *testing.T, srvURL, key string, raw []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/v1/embeddings", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("embeddings request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// embeddingsAs envía POST /v1/embeddings con un body tipado (JSON-marshalado).
func embeddingsAs(t *testing.T, srvURL, key string, body map[string]any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	return embeddingsRaw(t, srvURL, key, raw)
}

// embeddingsBody arma el body OpenAI-compatible que manda Odoo (spec
// §Contrato): input + model + dimensions + encoding_format. `dimensions` se
// omite si <=0; `encoding_format` se omite si "" (para ejercitar ausencia).
func embeddingsBody(input any, model string, dimensions int, encodingFormat string) map[string]any {
	b := map[string]any{"input": input, "model": model}
	if dimensions > 0 {
		b["dimensions"] = dimensions
	}
	if encodingFormat != "" {
		b["encoding_format"] = encodingFormat
	}
	return b
}

// embeddingsEnvelope tipa el body OpenAI-compatible de embeddings (P6):
// object list + data[] + model + usage.
type embeddingsEnvelope struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func decodeEmbeddings(t *testing.T, raw []byte) *embeddingsEnvelope {
	t.Helper()
	var out embeddingsEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body embeddings no es JSON: %v\n%s", err, raw)
	}
	return &out
}

// ---- BLOQUE A — P1: ruta protegida y clientID sin scoping nuevo ----

func TestE2E011006_A_Auth(t *testing.T) {
	h := buildMultiClient(t, []*upstream{}, "k1")

	// Sin Bearer → 401 con envelope (P1, auth.Wrap protege /v1/*). En RED la
	// ruta no está registrada; auth.Wrap intercepta sin Bearer → 401, y con
	// Bearer válido el mux responde 404. La 401 es correcta ya en RED (I6).
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/embeddings", strings.NewReader(`{"input":"hola","model":"m"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("sin Bearer: status = %d, want 401 (P1)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	assertErrorEnvelope(t, body)

	// Con Bearer válido → el handler responde 200 (ruta registrada bajo
	// s.auth.Wrap, sin scoping nuevo). En RED esto es 404 (ruta ausente) →
	// AssertionError de status.
	resp2 := h.embeddings(t, "k1", embeddingsBody("hola", "m", 0, "float"))
	if resp2.StatusCode != 200 {
		t.Fatalf("con Bearer válido: status = %d, want 200 (P1, handler responde)", resp2.StatusCode)
	}
	io.Copy(io.Discard, resp2.Body)
}

// ---- BLOQUE B — P2 (+P10 400): body inválido ----

func TestE2E011006_B_BodyInvalido(t *testing.T) {
	h := buildMultiClient(t, []*upstream{}, "k1")

	// body no-JSON → 400 con envelope {"error":{type,message,code}}.
	t.Run("not_json", func(t *testing.T) {
		code, raw := embeddingsRaw(t, h.srv.URL, h.key, []byte(`not-json`))
		if code != 400 {
			t.Fatalf("status = %d, want 400 (P2)", code)
		}
		assertErrorEnvelope(t, raw)
	})

	// body JSON sin `input` usable → 400 con envelope (P2).
	t.Run("sin_input", func(t *testing.T) {
		code, raw := embeddingsAs(t, h.srv.URL, h.key, map[string]any{"model": "m"})
		if code != 400 {
			t.Fatalf("status = %d, want 400 (sin input, P2)", code)
		}
		assertErrorEnvelope(t, raw)
	})
}

// ---- BLOQUE C — P3: modelo forzado por cliente (no el de Odoo) ----

func TestE2E011006_C_ModeloForzado(t *testing.T) {
	u := embeddingsOK("m", [][]float64{{0.1, 0.2, 0.3}}, 3)
	h := buildMultiClient(t, []*upstream{}, "k1")

	// Odoo manda model "x" y dimensions 1536; el cliente tiene
	// embeddings.model == "m" → el body upstream hacia Ollama lleva "m",
	// nunca "x" (P3: "el cliente nunca elige").
	body := embeddingsBody("hola", "x", 1536, "float")
	code, raw := embeddingsAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P3); body=%s", code, raw)
	}

	// P3: upstream recibe model "m" (I1/I7: path /embeddings, no /chat/completions).
	if u.gotPath != "/embeddings" {
		t.Fatalf("upstream path = %q, want /embeddings (I1/I7)", u.gotPath)
	}
	got := bodyField(t, u.gotBody)
	if got["model"] != "m" {
		t.Fatalf("upstream model = %v, want m (P3: modelo forzado del cliente)", got["model"])
	}
	// el model "x" de Odoo se IGNORA por completo: no llega al upstream.
	if strings.Contains(u.gotBody, `"model":"x"`) {
		t.Fatalf("model 'x' de Odoo llega al upstream (P3: se ignora): %s", u.gotBody)
	}

	// P3/P6: el model del envelope de respuesta == el modelo forzado.
	out := decodeEmbeddings(t, raw)
	if out.Model != "m" {
		t.Fatalf("envelope model = %q, want m (P3/P6)", out.Model)
	}
}

// ---- BLOQUE D — P4: cliente sin embeddings.model → 400 fail-fast ----

func TestE2E011006_D_ClienteSinModelo(t *testing.T) {
	// cliente sin embeddings.model configurado → 400 con message
	// descriptivo (P4/D2, sin default global silencioso).
	h := buildMultiClient(t, []*upstream{}, "k1")

	code, raw := embeddingsAs(t, h.srv.URL, h.key, embeddingsBody("hola", "x", 0, "float"))
	if code != 400 {
		t.Fatalf("status = %d, want 400 (P4: cliente sin modelo de embeddings)", code)
	}
	errObj := errorObject(t, raw)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "no embeddings model configured for client") {
		t.Fatalf("error.message = %q, want 'no embeddings model configured for client' (P4)", errObj["message"])
	}
}

// ---- BLOQUE E — P5: input byte-idéntico + dimensions/encoding passthrough ----

func TestE2E011006_E_InputDimensionsPassthrough(t *testing.T) {
	u := embeddingsOK("m", [][]float64{{0.1}}, 3)
	h := buildMultiClient(t, []*upstream{}, "k1")

	body := embeddingsBody("hola mundo", "x", 1536, "float")
	code, _ := embeddingsAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P5)", code)
	}

	got := bodyField(t, u.gotBody)
	// P5: input pasado intacto (byte-idéntico) al upstream.
	if got["input"] != "hola mundo" {
		t.Fatalf("upstream input = %v, want 'hola mundo' (P5)", got["input"])
	}
	// P5/D1: dimensions passthrough (mofgw NO lo valida contra la dimensión
	// nativa del modelo, I4).
	if got["dimensions"] != float64(1536) {
		t.Fatalf("upstream dimensions = %v, want 1536 (P5 passthrough, I4)", got["dimensions"])
	}
	// P5: encoding_format passthrough si viene.
	if got["encoding_format"] != "float" {
		t.Fatalf("upstream encoding_format = %v, want float (P5 passthrough)", got["encoding_format"])
	}
}

// ---- BLOQUE F — P6: respuesta OpenAI-compatible + vector nativo verbatim ----

func TestE2E011006_F_RespuestaOpenAICompatible(t *testing.T) {
	// vector de dimensión nativa 384 (all-minilm, D1/I4) devuelto por Ollama.
	vec := make([]float64, 384)
	for i := range vec {
		vec[i] = float64(i) / 100.0
	}
	u := embeddingsOK("m", [][]float64{vec}, 5)
	h := buildMultiClient(t, []*upstream{}, "k1")

	code, raw := embeddingsAs(t, h.srv.URL, h.key, embeddingsBody("hola", "x", 1536, "float"))
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P6)", code)
	}

	out := decodeEmbeddings(t, raw)
	if out.Object != "list" {
		t.Fatalf("object = %q, want list (P6)", out.Object)
	}
	if len(out.Data) != 1 {
		t.Fatalf("data len = %d, want 1 (P6)", len(out.Data))
	}
	it := out.Data[0]
	if it.Object != "embedding" || it.Index != 0 {
		t.Fatalf("data[0] = object:%q index:%d, want embedding/0 (P6)", it.Object, it.Index)
	}
	// P6/I4: el vector real es la dimensión NATIVA del modelo, verbatim
	// (mofgw no trunca, no paddea, no re-valida).
	want := u.vectors[0]
	if len(it.Embedding) != len(want) {
		t.Fatalf("embedding len = %d, want %d (I4, vector nativo)", len(it.Embedding), len(want))
	}
	for i, v := range it.Embedding {
		if v != want[i] {
			t.Fatalf("embedding[%d] = %v, want %v (P6/I4 verbatim)", i, v, want[i])
		}
	}
	// P3/P6: model del envelope forzado por cliente.
	if out.Model != "m" {
		t.Fatalf("envelope model = %q, want m (P3/P6)", out.Model)
	}
	// usage passthrough (P6/P7): prompt_tokens del usage de Ollama.
	if out.Usage.PromptTokens != 5 {
		t.Fatalf("usage.prompt_tokens = %d, want 5 (P6/P7)", out.Usage.PromptTokens)
	}
}

// ---- BLOQUE G — P7: pricing por tokens del input (costo 0 sin pricing) ----

func TestE2E011006_G_PricingUsage(t *testing.T) {
	// prompt_tokens 10 por request; modelo AUSENTE de pricing: → costo 0
	// (P7/D5). Los contadores de usage del clientID se incrementan.
	u := embeddingsOK("m", [][]float64{{0.1}}, 10)
	h := buildMultiClient(t, []*upstream{}, "k1")

	resp := h.embeddings(t, "k1", embeddingsBody("hi", "x", 0, "float"))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (P7)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if u.calls != 1 {
		t.Fatalf("upstream embeddings calls = %d, want 1 (P7)", u.calls)
	}

	// P7: prompt_tokens 10 en headers X-Usage-*; costo 0 sin pricing.
	assertUsageHeaders(t, resp, 10, 0, 10, 0, "0")

	// P7: usage acumulado del clientID.
	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_prompt_tokens_total 10") {
		t.Fatalf("usage prompt no contabilizado para clientID:\n%s", s)
	}
}

// ---- BLOQUE H — P8: budget por cliente aplicado → 429 ----

func TestE2E011006_H_Budget(t *testing.T) {
	// prompt_tokens 10 por request; budget tokens 15 → tras 2 requests el
	// acumulado es 20 ≥ 15 → el 3er request responde 429 (P8), igual que
	// /v1/chat/completions. El upstream mock (embeddingsOK prompt 10) se
	// wirea en GREEN vía buildWithEmbeddings.
	h := buildMultiClient(t, []*upstream{}, "k1")
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"client-k1": {TokensMax: 15},
	})

	for i := 0; i < 2; i++ {
		resp := h.embeddings(t, "k1", embeddingsBody("hi", "x", 0, "float"))
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200 (budget bajo)", i+1, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	resp := h.embeddings(t, "k1", embeddingsBody("hi", "x", 0, "float"))
	if resp.StatusCode != 429 {
		t.Fatalf("request 3: status = %d, want 429 (P8 budget excedido)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	errObj := errorObject(t, body)
	if errObj["type"] != "rate_limit_exceeded" {
		t.Fatalf("error.type = %v, want rate_limit_exceeded (P8/P10)", errObj["type"])
	}
}

// ---- BLOQUE I — P9: error upstream → HTTP 502 ----

func TestE2E011006_I_ErrorUpstream(t *testing.T) {
	t.Run("ollama_500", func(t *testing.T) {
		h := buildMultiClient(t, []*upstream{}, "k1")
		code, raw := embeddingsAs(t, h.srv.URL, h.key, embeddingsBody("hi", "x", 0, "float"))
		if code != 502 {
			t.Fatalf("status = %d, want 502 (P9: upstream 500)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("ollama_429_no_reintentable", func(t *testing.T) {
		// un 429 de Ollama no-reintentable responde como fallo upstream 502
		// (P9: no se reenvía el request).
		h := buildMultiClient(t, []*upstream{}, "k1")
		code, raw := embeddingsAs(t, h.srv.URL, h.key, embeddingsBody("hi", "x", 0, "float"))
		if code != 502 {
			t.Fatalf("status = %d, want 502 (P9: 429 no-reintentable)", code)
		}
		assertErrorEnvelope(t, raw)
	})
}

// ---- BLOQUE J — P10: envelope de error OpenAI-compatible uniforme ----

func TestE2E011006_J_EnvelopeUniforme(t *testing.T) {
	t.Run("400_body_invalido", func(t *testing.T) {
		h := buildMultiClient(t, []*upstream{}, "k1")
		code, raw := embeddingsRaw(t, h.srv.URL, h.key, []byte(`no-json`))
		if code != 400 {
			t.Fatalf("status = %d, want 400 (P2/P10)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("401_sin_auth", func(t *testing.T) {
		h := buildMultiClient(t, []*upstream{}, "k1")
		code, raw := embeddingsRaw(t, h.srv.URL, "", []byte(`{}`))
		if code != 401 {
			t.Fatalf("status = %d, want 401 (P1/P10)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("429_budget", func(t *testing.T) {
		h := buildMultiClient(t, []*upstream{}, "k1")
		h.proxySrv.SetBudget(map[string]config.BudgetConfig{
			"client-k1": {TokensMax: 15},
		})
		// 2 requests acumulan 20 ≥ 15 → 3er → 429 con envelope (P8/P10).
		for i := 0; i < 2; i++ {
			r := h.embeddings(t, "k1", embeddingsBody("hi", "x", 0, "float"))
			if r.StatusCode != 200 {
				t.Fatalf("request %d: status = %d, want 200", i+1, r.StatusCode)
			}
			io.Copy(io.Discard, r.Body)
		}
		r := h.embeddings(t, "k1", embeddingsBody("hi", "x", 0, "float"))
		if r.StatusCode != 429 {
			t.Fatalf("status = %d, want 429 (P8/P10)", r.StatusCode)
		}
		body, _ := io.ReadAll(r.Body)
		assertErrorEnvelope(t, body)
	})
	t.Run("502_upstream", func(t *testing.T) {
		h := buildMultiClient(t, []*upstream{}, "k1")
		code, raw := embeddingsAs(t, h.srv.URL, h.key, embeddingsBody("hi", "x", 0, "float"))
		if code != 502 {
			t.Fatalf("status = %d, want 502 (P9/P10)", code)
		}
		assertErrorEnvelope(t, raw)
	})
}
