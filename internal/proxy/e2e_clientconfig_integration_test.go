// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Integración E2E del epic mofgw-client-config (016): verifica que el endpoint
// `GET /v1/client-config?client=<id>` devuelve fragmentos REALES, fieles al
// catálogo (`/v1/models`), a través de los adapters concretos registrados
// (opencode 016-002, openclaw 016-003, zot 016-004).
//
// Diferencia clave vs. los tests RED de 016-001 (e2e_clientconfig_test.go):
// aquí NO usamos un renderer de captura (`configCaptureRenderer`), sino los
// tres adapters DE PRODUCCIÓN (opencode/openclaw/zot). En el harness de e2e
// los adapters NO están auto-registrados (el registro vive en el wiring de
// cmd/mofgw/main.go, no en el paquete proxy), así que este test los registra
// explícitamente vía `clientconfig.Register` y los limpia de forma SCOPED
// (solo esos ids) en t.Cleanup.
//
// Contrato observable verificado por cliente:
//   - opencode: 200, top-level "mofgw", `mofgw.options.baseURL == base_url`,
//     `mofgw.options.apiKey == "{env:MOFGW_KEY}"`, `mofgw.npm ==
//     "@ai-sdk/openai-compatible"`, `mofgw.models.<id>.limit.context ==
//     ContextWindow` seteado.
//   - openclaw: 200, top-level "models", `models.providers.mofgw.baseUrl ==
//     base_url`, `apiKey == "${MOFGW_KEY}"`, `api == "openai-completions"`,
//     `models[]` contiene el id del modelo.
//   - zot: 200, top-level "providers", `providers.mofgw.baseUrl == base_url`,
//     SIN campo `apiKey` en ninguna parte, `api == "openai"`, `models[]`
//     contiene el id del modelo.
//   - /v1/models sigue devolviendo el modelo (cero regresión).
//   - negativo: client no registrado → 404 con la lista de los 3 soportados.

package proxy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/clientconfig"
	"github.com/ofapsaas/mofgw/internal/clientconfig/openclaw"
	"github.com/ofapsaas/mofgw/internal/clientconfig/opencode"
	"github.com/ofapsaas/mofgw/internal/clientconfig/zot"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/proxy"
)

func TestE2E_ClientConfigIntegration(t *testing.T) {
	// Registro explícito de los adapters de producción (en PROD lo hace el
	// wiring de cmd/mofgw/main.go; en el harness e2e lo hace el test).
	registered := map[string]clientconfig.Renderer{
		"opencode": opencode.Renderer{},
		"openclaw": openclaw.Renderer{},
		"zot":      zot.Renderer{},
	}
	for id, r := range registered {
		clientconfig.Register(id, r)
		rr := clientconfig.Lookup(id)
		if rr == nil {
			t.Fatalf("Register(%q): Lookup no devuelve el renderer", id)
		}
	}
	// Cleanup SCOPED: elimina SOLO los ids que registramos en este test.
	t.Cleanup(func() {
		for id := range registered {
			delete(clientconfig.Renderers, id)
		}
	})

	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000000, MaxOutput: 384000},
	})
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 0.14, OutputUSDPerM: 0.28, CacheHitUSDPerM: 0.0028},
	})
	h.proxySrv.SetClientConfig("https://mofgw.example/v1", "MOFGW_KEY")

	const baseURL = "https://mofgw.example/v1"
	const modelID = "m"
	const ctxWindow = float64(1000000)

	// ---- opencode (016-002): top-level "mofgw" ----
	t.Run("opencode", func(t *testing.T) {
		resp := getClientConfig(t, h, "opencode", true)
		if resp.status != 200 {
			t.Fatalf("status = %d, want 200", resp.status)
		}
		var doc map[string]any
		if err := json.Unmarshal(resp.body, &doc); err != nil {
			t.Fatalf("body no es JSON: %v: %s", err, resp.body)
		}
		mofgw, ok := doc["mofgw"].(map[string]any)
		if !ok {
			t.Fatalf("falta top-level \"mofgw\": %s", resp.body)
		}
		if mofgw["npm"] != "@ai-sdk/openai-compatible" {
			t.Fatalf("mofgw.npm = %v, want @ai-sdk/openai-compatible", mofgw["npm"])
		}
		if mofgw["name"] != "mofgw" {
			t.Fatalf("mofgw.name = %v, want mofgw", mofgw["name"])
		}
		opts, ok := mofgw["options"].(map[string]any)
		if !ok {
			t.Fatalf("mofgw.options no es objeto: %s", resp.body)
		}
		if opts["baseURL"] != baseURL {
			t.Fatalf("mofgw.options.baseURL = %v, want %s", opts["baseURL"], baseURL)
		}
		if opts["apiKey"] != "{env:MOFGW_KEY}" {
			t.Fatalf("mofgw.options.apiKey = %v, want {env:MOFGW_KEY}", opts["apiKey"])
		}
		models, ok := mofgw["models"].(map[string]any)
		if !ok {
			t.Fatalf("mofgw.models no es objeto: %s", resp.body)
		}
		mObj, ok := models[modelID].(map[string]any)
		if !ok {
			t.Fatalf("mofgw.models no contiene el modelo %q: %s", modelID, resp.body)
		}
		limit, ok := mObj["limit"].(map[string]any)
		if !ok {
			t.Fatalf("mofgw.models.%s.limit ausente: %v", modelID, mObj)
		}
		if limit["context"] != ctxWindow {
			t.Fatalf("limit.context = %v, want %v (= ContextWindow seteado)", limit["context"], ctxWindow)
		}
	})

	// ---- openclaw (016-003): top-level "models" ----
	t.Run("openclaw", func(t *testing.T) {
		resp := getClientConfig(t, h, "openclaw", true)
		if resp.status != 200 {
			t.Fatalf("status = %d, want 200", resp.status)
		}
		var doc map[string]any
		if err := json.Unmarshal(resp.body, &doc); err != nil {
			t.Fatalf("body no es JSON: %v: %s", err, resp.body)
		}
		modelsTop, ok := doc["models"].(map[string]any)
		if !ok {
			t.Fatalf("falta top-level \"models\": %s", resp.body)
		}
		if modelsTop["mode"] != "merge" {
			t.Fatalf("models.mode = %v, want merge", modelsTop["mode"])
		}
		providers, ok := modelsTop["providers"].(map[string]any)
		if !ok {
			t.Fatalf("models.providers no es objeto: %s", resp.body)
		}
		mofgw, ok := providers["mofgw"].(map[string]any)
		if !ok {
			t.Fatalf("models.providers.mofgw ausente: %s", resp.body)
		}
		if mofgw["baseUrl"] != baseURL {
			t.Fatalf("providers.mofgw.baseUrl = %v, want %s", mofgw["baseUrl"], baseURL)
		}
		if mofgw["apiKey"] != "${MOFGW_KEY}" {
			t.Fatalf("providers.mofgw.apiKey = %v, want ${MOFGW_KEY}", mofgw["apiKey"])
		}
		if mofgw["api"] != "openai-completions" {
			t.Fatalf("providers.mofgw.api = %v, want openai-completions", mofgw["api"])
		}
		models, ok := mofgw["models"].([]any)
		if !ok {
			t.Fatalf("providers.mofgw.models no es array: %s", resp.body)
		}
		found := false
		for _, raw := range models {
			if e, ok := raw.(map[string]any); ok && e["id"] == modelID {
				found = true
				if e["contextWindow"] != ctxWindow {
					t.Fatalf("contextWindow = %v, want %v", e["contextWindow"], ctxWindow)
				}
			}
		}
		if !found {
			t.Fatalf("providers.mofgw.models[] no contiene el modelo %q: %s", modelID, resp.body)
		}
	})

	// ---- zot (016-004): top-level "providers", SIN apiKey ----
	t.Run("zot", func(t *testing.T) {
		resp := getClientConfig(t, h, "zot", true)
		if resp.status != 200 {
			t.Fatalf("status = %d, want 200", resp.status)
		}
		// I1: el formato zot NO lleva campo de key en ninguna parte.
		if strings.Contains(string(resp.body), "apiKey") ||
			strings.Contains(string(resp.body), "${") ||
			strings.Contains(string(resp.body), "{env:") {
			t.Fatalf("el fragmento zot NO debe emitir campo de key ni templates de env: %s", resp.body)
		}
		var doc map[string]any
		if err := json.Unmarshal(resp.body, &doc); err != nil {
			t.Fatalf("body no es JSON: %v: %s", err, resp.body)
		}
		providers, ok := doc["providers"].(map[string]any)
		if !ok {
			t.Fatalf("falta top-level \"providers\": %s", resp.body)
		}
		mofgw, ok := providers["mofgw"].(map[string]any)
		if !ok {
			t.Fatalf("providers.mofgw ausente: %s", resp.body)
		}
		if mofgw["baseUrl"] != baseURL {
			t.Fatalf("providers.mofgw.baseUrl = %v, want %s", mofgw["baseUrl"], baseURL)
		}
		if mofgw["api"] != "openai" {
			t.Fatalf("providers.mofgw.api = %v, want openai", mofgw["api"])
		}
		models, ok := mofgw["models"].([]any)
		if !ok {
			t.Fatalf("providers.mofgw.models no es array: %s", resp.body)
		}
		found := false
		for _, raw := range models {
			if e, ok := raw.(map[string]any); ok && e["id"] == modelID {
				found = true
				if e["contextWindow"] != ctxWindow {
					t.Fatalf("contextWindow = %v, want %v", e["contextWindow"], ctxWindow)
				}
			}
		}
		if !found {
			t.Fatalf("providers.mofgw.models[] no contiene el modelo %q: %s", modelID, resp.body)
		}
	})

	// ---- /v1/models sin regresión ----
	t.Run("no_regression_models", func(t *testing.T) {
		models := modelsBody(t, h)
		if models["object"] != "list" {
			t.Fatalf("/v1/models object = %v, want list", models["object"])
		}
		data, _ := models["data"].([]any)
		found := false
		for _, raw := range data {
			if m, ok := raw.(map[string]any); ok && m["id"] == modelID {
				found = true
			}
		}
		if !found {
			t.Fatalf("/v1/models no lista el modelo %q", modelID)
		}
	})

	// ---- negativo: client no registrado → 404 con los 3 soportados ----
	t.Run("unknown_client_404", func(t *testing.T) {
		resp := getClientConfig(t, h, "noexiste", true)
		if resp.status != 404 {
			t.Fatalf("status = %d, want 404 (client no registrado)", resp.status)
		}
		env := decodeOpenAIErr(t, resp.body)
		if env.Error.Type != "not_found_error" {
			t.Fatalf("error.type = %q, want not_found_error", env.Error.Type)
		}
		for _, id := range []string{"opencode", "openclaw", "zot"} {
			if !strings.Contains(env.Error.Message, id) {
				t.Fatalf("error.message = %q debe listar el cliente registrado %q", env.Error.Message, id)
			}
		}
	})
}