// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-001-config-renderer-core: endpoint
// `GET /v1/client-config?client=<id>` + IR + registry de renderers.
//
// Contrato observable (spec docs/specs/016-001-config-renderer-core/spec.md):
//   - P1: sin Bearer → 401 `invalid_api_key`
//   - P2: client ausente → 400 `invalid_request_error` "missing required query param: client"
//   - P3: client desconocido + base_url seteado → 404 `not_found_error`, mensaje con "supported:"
//   - P4: base_url vacío → 503 `server_error` "client_config.base_url not set" (aun con renderer)
//   - P5: Register("test",r) + base_url → 200, body == bytes de r.Render(ir)
//   - P6: el ConfigIR capturado por el renderer == iteración dedup de /v1/models
//   - P7: ConfigIR.BaseURL / KeyEnvRef (default "MOFGW_KEY") vivos
//   - P8: ningún status filtra el secret; IR lleva KeyEnvRef, no el valor
//   - P9: errores usan envelope openAIError; /v1/models no cambia
//
// Registro del renderer de prueba SIEMPRE vía la API pública
// `clientconfig.Register` — nunca un mock de plumbing (P10).

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ofapsaas/mofgw/internal/clientconfig"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/proxy"
)

// secretNeverLeak es el literal que mofgw NUNCA debe emitir en ningún estado
// (I1/P8): el IR transporta `KeyEnvRef`, jamás el valor del secret.
const secretNeverLeak = "sk-literal-secret-never-leak-016"

// configCaptureRenderer implementa clientconfig.Renderer y captura el
// ConfigIR recibido para aserciones P6/P7. El body devuelto es controlado
// por el test (P5) — ejercita el path 200 del endpoint.
type configCaptureRenderer struct {
	mu   sync.Mutex
	last clientconfig.ConfigIR
	body []byte
}

func (r *configCaptureRenderer) Render(ir clientconfig.ConfigIR) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = ir
	out := make([]byte, len(r.body))
	copy(out, r.body)
	return out, nil
}

func (r *configCaptureRenderer) get() clientconfig.ConfigIR {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// resetClientConfigRegistry deja el registry público en estado limpio para
// cada test (limpia los registros del test anterior vía la API exportada).
func resetClientConfigRegistry(t *testing.T) {
	t.Helper()
	clear := func() {
		for id := range clientconfig.Renderers {
			delete(clientconfig.Renderers, id)
		}
	}
	clear()
	t.Cleanup(clear)
}

// clientConfigResp resume la respuesta del endpoint.
type clientConfigResp struct {
	status int
	body   []byte
}

// getClientConfig hace GET /v1/client-config con el query param client
// opcional. authed=true → envía h.key como Bearer.
func getClientConfig(t *testing.T, h *harness, client string, authed bool) clientConfigResp {
	t.Helper()
	u := h.srv.URL + "/v1/client-config"
	if client != "" {
		u += "?client=" + client
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("client-config request: %v", err)
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+h.key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client-config: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return clientConfigResp{status: resp.StatusCode, body: body}
}

// openAIErrEnvelope es el envelope de error OpenAI-compatible.
type openAIErrEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func decodeOpenAIErr(t *testing.T, body []byte) openAIErrEnvelope {
	t.Helper()
	var e openAIErrEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("body no es envelope openAIError: %v: %s", err, body)
	}
	return e
}

// ---- P1: 401 sin auth ----

func TestRED_ClientConfig401NoAuth_P1(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	resp := getClientConfig(t, h, "anything", false)
	if resp.status != 401 {
		t.Fatalf("status = %d, want 401 (ruta /v1/* autenticada, fuera de publicPrefixes)", resp.status)
	}
	env := decodeOpenAIErr(t, resp.body)
	if env.Error.Type != "invalid_api_key" {
		t.Fatalf("error.type = %q, want invalid_api_key (lo emite auth.Wrap)", env.Error.Type)
	}
}

// ---- P2: 400 sin client param ----

func TestRED_ClientConfig400NoClient_P2(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// client ausente
	resp := getClientConfig(t, h, "", true)
	if resp.status != 400 {
		t.Fatalf("status = %d, want 400 (client ausente)", resp.status)
	}
	env := decodeOpenAIErr(t, resp.body)
	if env.Error.Type != "invalid_request_error" {
		t.Fatalf("error.type = %q, want invalid_request_error", env.Error.Type)
	}
	if env.Error.Message != "missing required query param: client" {
		t.Fatalf("error.message = %q, want exacto \"missing required query param: client\"", env.Error.Message)
	}
}

// ---- P3: 404 cliente desconocido con lista de soportados ----

func TestRED_ClientConfig404UnknownClient_P3(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")

	resp := getClientConfig(t, h, "noexiste", true)
	if resp.status != 404 {
		t.Fatalf("status = %d, want 404 (cliente desconocido)", resp.status)
	}
	env := decodeOpenAIErr(t, resp.body)
	if env.Error.Type != "not_found_error" {
		t.Fatalf("error.type = %q, want not_found_error", env.Error.Type)
	}
	if !strings.Contains(env.Error.Message, "unsupported client \"noexiste\"; supported:") {
		t.Fatalf("error.message = %q, want prefix \"unsupported client \\\"noexiste\\\"; supported:\"", env.Error.Message)
	}
	if !strings.Contains(env.Error.Message, "supported:") {
		t.Fatalf("error.message = %q, debe contener \"supported:\"", env.Error.Message)
	}

	// El registry es la fuente: registrar un renderer cambia la lista
	// observable en el mensaje 404 (P3: cada Register cambia la lista).
	clientconfig.Register("zzz", &configCaptureRenderer{body: []byte(`{}`)})
	resp2 := getClientConfig(t, h, "otro", true)
	if resp2.status != 404 {
		t.Fatalf("status = %d, want 404 tras registrar", resp2.status)
	}
	env2 := decodeOpenAIErr(t, resp2.body)
	if !strings.Contains(env2.Error.Message, "zzz") {
		t.Fatalf("error.message = %q, debe listar el renderer registrado (registry = fuente)", env2.Error.Message)
	}
}

// ---- P4: 503 base_url vacío ----

func TestRED_ClientConfig503NoBaseURL_P4(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// base_url NO seteado (default vacío) → 503, incluso con client válido
	// y un renderer registrado. No hay fallback silencioso a otro base_url (I4).
	h.proxySrv.SetClientConfig("", "MOFGW_KEY")
	clientconfig.Register("test", &configCaptureRenderer{body: []byte(`{"k":"v"}`)})

	resp := getClientConfig(t, h, "test", true)
	if resp.status != 503 {
		t.Fatalf("status = %d, want 503 (client_config.base_url vacío), aun con renderer registrado", resp.status)
	}
	env := decodeOpenAIErr(t, resp.body)
	if env.Error.Type != "server_error" {
		t.Fatalf("error.type = %q, want server_error", env.Error.Type)
	}
	if env.Error.Message != "client_config.base_url not set" {
		t.Fatalf("error.message = %q, want exacto \"client_config.base_url not set\"", env.Error.Message)
	}
}

// ---- P5: 200 con renderer registrado (path core) ----

func TestRED_ClientConfig200RegisteredRenderer_P5(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")

	expected := []byte(`{"fragment":"opencode.json","base":"https://mofgw.example/v1","key_env":"MOFGW_KEY"}`)
	renderer := &configCaptureRenderer{body: expected}
	clientconfig.Register("test", renderer)

	resp := getClientConfig(t, h, "test", true)
	if resp.status != 200 {
		t.Fatalf("status = %d, want 200 (renderer registrado + base_url)", resp.status)
	}
	if !bytes.Equal(resp.body, expected) {
		t.Fatalf("body = %s, want exactamente los bytes que devolvió r.Render(ir): %s", resp.body, expected)
	}
}

// ---- P6 + P7: IR fiel al catálogo + knobs vivos ----

func TestRED_ClientConfigIRMatchesModelsAndKnobs_P6P7(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {
			ContextWindow:       1000000,
			MaxOutput:           384000,
			Thinking:            []string{"low", "high", "max"},
			ThinkingDefault:     "low",
			SupportedParameters: []string{"tools", "reasoning"},
			Modality:            "text->text",
		},
	})
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 0.14, OutputUSDPerM: 0.28, CacheHitUSDPerM: 0.0028},
	})
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")

	renderer := &configCaptureRenderer{body: []byte(`{}`)}
	clientconfig.Register("test", renderer)

	resp := getClientConfig(t, h, "test", true)
	if resp.status != 200 {
		t.Fatalf("status = %d, want 200", resp.status)
	}
	ir := renderer.get()

	// P7 — knobs vivos en el IR.
	if ir.BaseURL != "https://mofgw.example/v1" {
		t.Fatalf("ConfigIR.BaseURL = %q, want https://mofgw.example/v1", ir.BaseURL)
	}
	if ir.KeyEnvRef != "MOFGW_KEY" {
		t.Fatalf("ConfigIR.KeyEnvRef = %q, want default MOFGW_KEY", ir.KeyEnvRef)
	}
	if ir.ClientID != "test" {
		t.Fatalf("ConfigIR.ClientID = %q, want test (id del cliente autenticado)", ir.ClientID)
	}

	// P6 — mismo catálogo dedup que /v1/models (fuente de verdad viva, I2).
	models := modelsBody(t, h)
	mData, ok := models["data"].([]any)
	if !ok {
		t.Fatalf("/v1/models data no es array: %T", models["data"])
	}
	if len(ir.Models) != len(mData) {
		t.Fatalf("IR.Models len = %d, /v1/models len = %d — deben ser la misma lista dedup", len(ir.Models), len(mData))
	}
	for _, raw := range mData {
		entry := raw.(map[string]any)
		id, _ := entry["id"].(string)

		var irEntry *clientconfig.ModelEntry
		for i := range ir.Models {
			if ir.Models[i].ID == id {
				e := ir.Models[i]
				irEntry = &e
				break
			}
		}
		if irEntry == nil {
			t.Fatalf("IR.Models no contiene el modelo %q de /v1/models", id)
		}

		// "created" es timestamp (~Unix), difiere entre las dos llamadas:
		// lo descartamos y comparamos el resto de la metadata enriquecida.
		strip := func(m map[string]any) map[string]any {
			out := make(map[string]any, len(m))
			for k, v := range m {
				if k == "created" {
					continue
				}
				out[k] = v
			}
			return out
		}
		got, _ := json.Marshal(strip(irEntry.Meta))
		want, _ := json.Marshal(strip(entry))
		if !bytes.Equal(got, want) {
			t.Fatalf("IR.Models[%q].Meta ≠ /v1/models: got %s, want %s", id, got, want)
		}
	}
}

// ---- P8: key nunca en la respuesta ----

func TestRED_ClientConfigNeverLeaksSecret_P8(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// base_url seteado y renderer que solo emite la REFERENCIA (no el valor).
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")
	renderer := &configCaptureRenderer{body: []byte(`{"key_env":"MOFGW_KEY"}`)}
	clientconfig.Register("test", renderer)

	// 200 (con renderer registrado): contiene la ref, nunca el valor.
	resp200 := getClientConfig(t, h, "test", true)
	if resp200.status != 200 {
		t.Fatalf("status = %d, want 200", resp200.status)
	}
	if strings.Contains(string(resp200.body), secretNeverLeak) {
		t.Fatalf("200 filtra el secret: %s", resp200.body)
	}
	if !strings.Contains(string(resp200.body), "MOFGW_KEY") {
		t.Fatalf("200 debe contener la referencia MOFGW_KEY: %s", resp200.body)
	}

	// El IR transporta KeyEnvRef, no el valor.
	if ir := renderer.get(); ir.KeyEnvRef == secretNeverLeak {
		t.Fatalf("ConfigIR.KeyEnvRef contiene el valor del secret: %q", ir.KeyEnvRef)
	}

	// estados de error (400/404/503): ningún body contiene el secret.
	_ = getClientConfig(t, h, "", true)                               // 400
	resp404 := getClientConfig(t, h, "ghost", true)                   // 404
	resp503 := getClientConfigUnderNoBaseURL(t, h)                    // 503
	for _, r := range []clientConfigResp{resp404, resp503} {
		if strings.Contains(string(r.body), secretNeverLeak) {
			t.Fatalf("respuesta de error filtra el secret: %s", r.body)
		}
	}
}

// getClientConfigUnderNoBaseURL fuerza un 503 (base_url vacío) sobre un
// harness que YA no lo tiene seteado: se construye un proxy con base_url
// vacío y el mismo patrón de auth, y se golpea el endpoint.
func getClientConfigUnderNoBaseURL(t *testing.T, h *harness) clientConfigResp {
	t.Helper()
	// Reutilizamos el harness pero necesitamos un Server con base_url vacío.
	// Para no acoplar el helper al Server, reconstruimos el request contra el
	// MISMO harness solo si su base_url quedó vacío; en caso del test P8 ese
	// harness tiene base_url set, así que pedimos explícitamente un 503 usando
	// un segundo server dedicado vía build (SetClientConfig vacío).
	h2 := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h2.proxySrv.SetClientConfig("", "MOFGW_KEY")
	clientconfig.Register("test", &configCaptureRenderer{body: []byte(`{}`)})
	return getClientConfig(t, h2, "test", true)
}

// ---- P9: envelope openAIError + /v1/models sin regresión ----

func TestRED_ClientConfigErrorEnvelopeAndNoRegression_P9(t *testing.T) {
	resetClientConfigRegistry(t)
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// Envelope OpenAI-compatible en TODOS los estados de error.
	resp400 := getClientConfig(t, h, "", true)
	env400 := decodeOpenAIErr(t, resp400.body)
	if env400.Error.Type != "invalid_request_error" || env400.Error.Message == "" {
		t.Fatalf("400 env mal: type=%q msg=%q", env400.Error.Type, env400.Error.Message)
	}

	resp503 := getClientConfigUnderNoBaseURL(t, h)
	env503 := decodeOpenAIErr(t, resp503.body)
	if env503.Error.Type != "server_error" || env503.Error.Message != "client_config.base_url not set" {
		t.Fatalf("503 env mal: type=%q msg=%q", env503.Error.Type, env503.Error.Message)
	}

	// 404 necesita base_url seteado y client desconocido.
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")
	resp404 := getClientConfig(t, h, "nope", true)
	env404 := decodeOpenAIErr(t, resp404.body)
	if env404.Error.Type != "not_found_error" || !strings.Contains(env404.Error.Message, "supported:") {
		t.Fatalf("404 env mal: type=%q msg=%q", env404.Error.Type, env404.Error.Message)
	}

	// /v1/models sin regresión de shape ni de cardinalidad tras estas llamadas:
	// sigue 200, object=list y lista el modelo.
	models := modelsBody(t, h)
	if models["object"] != "list" {
		t.Fatalf("/v1/models object = %v, want list", models["object"])
	}
	data, _ := models["data"].([]any)
	found := false
	for _, raw := range data {
		if m, ok := raw.(map[string]any); ok && m["id"] == "m" {
			found = true
		}
	}
	if len(data) != 1 || !found {
		t.Fatalf("/v1/models data len = %d (want 1) y debe listar \"m\" — regresión en el catálogo", len(data))
	}
}