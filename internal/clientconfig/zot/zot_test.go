// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-004-adapter-zot — contrato del adapter del
// fragmento config del provider `mofgw` para zot (`patriceckhart/zot`).
//
// El renderer (`zot.Renderer`) serializa un `clientconfig.ConfigIR` en el
// fragmento JSON listo para insertar/mergear bajo el `UserModelsFile` de zot
// (P1-P11). P12 fija la fallback-rule (presente-pero-≤0) heredada de la review
// de 016-002/016-003.
//
// DESVIACIÓN I1 (diferencial vs opencode/openclaw): el formato de zot NO elige
// campo `apiKey` en absoluto — `models.json` jamás almacena secrets. zot
// resuelve la key en runtime vía env derivada `UPPER_SNAKE(provider)_API_KEY`
// (= `MOFGW_API_KEY`). Por tanto este adapter NO emite template alguno
// (`${VAR}`/`{env:VAR}`) y `ir.KeyEnvRef` NO se emite ni es obligatorio
// (vacío NO es error). Esto se refleja en P3 y P9.
//
// Contract que estas pruebas exigen (RED, will-not-compile):
//   - tipo `zot.Renderer` (struct) con método
//     `Render(ir clientconfig.ConfigIR) ([]byte, error)`
//   - emite JSON válido con top-level clave `"providers"` (NO "models", NO
//     "mode"):
//     { providers: { mofgw: { baseUrl, api, models: [] } } }
//   - `providers.mofgw.models` es un ARRAY de objetos {id, name?,
//     contextWindow?, maxTokens?} (NO objeto keyed, NO mapa por id).
//   - `providers.mofgw` NO tiene clave `apiKey` ni `key`; `api == "openai"`;
//     models exclusivamente desde IR.Models.

package zot

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/clientconfig"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newIR construye un ConfigIR base con 2 modelos (uno con metadata de limits,
// otro sin ella) para recorrer el JSON de salida. KeyEnvRef se puebla con un
// valor "secret-like" deliberadamente, para que P3 pueda verificar que el
// output NO contiene ni el literal ni un template del mismo.
func newIR() clientconfig.ConfigIR {
	return clientconfig.ConfigIR{
		ClientID:  "test-client",
		BaseURL:   "https://gw/v1",
		KeyEnvRef: "MOFGW_KEY",
		Models: []clientconfig.ModelEntry{
			{
				ID: "model-a",
				Meta: map[string]any{
					"context_length":       float64(32000),
					"max_output_tokens":    float64(4096),
					"display_name":         "Model A",
					"object":               "model",
					"owned_by":             "acme",
					"supported_parameters": []any{"temperature", "top_p"},
				},
			},
			{
				ID: "model-b",
				Meta: map[string]any{
					"object":   "model",
					"owned_by": "acme",
				},
			},
		},
	}
}

// walkTop deserializa el body y devuelve el objeto top-level `providers` y el
// objeto `providers.mofgw` (con baseUrl/api/models) si el JSON es válido y la
// forma esperada está.
func walkTop(t *testing.T, body []byte) (providersTop map[string]any, mofgw map[string]any) {
	t.Helper()

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("Render no emitió JSON válido: %v (body=%s)", err, body)
	}

	providersAny, ok := top["providers"]
	if !ok {
		t.Fatalf("top-level sin clave \"providers\": %s", body)
	}
	providersTop, ok = providersAny.(map[string]any)
	if !ok {
		t.Fatalf("\"providers\" debe ser un objeto, got %T", providersAny)
	}

	mofgwAny, ok := providersTop["mofgw"]
	if !ok {
		t.Fatalf("providers sin clave \"mofgw\": %s", body)
	}
	mofgw, ok = mofgwAny.(map[string]any)
	if !ok {
		t.Fatalf("providers.mofgw debe ser un objeto, got %T", mofgwAny)
	}

	return providersTop, mofgw
}

// walkModelsArray extrae y devuelve `providers.mofgw.models` como
// []map[string]any (cada entrada es un objeto). Falla si no es un array.
func walkModelsArray(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	_, mofgw := walkTop(t, body)

	arr, ok := mofgw["models"].([]any)
	if !ok {
		t.Fatalf("providers.mofgw.models debe ser un array, got %T (%v)", mofgw["models"], mofgw["models"])
	}

	out := make([]map[string]any, 0, len(arr))
	for i, raw := range arr {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("models[%d] debe ser un objeto, got %T", i, raw)
		}
		out = append(out, entry)
	}
	return out
}

// numFromMeta extrae un int64 desde un valor ya deserializado
// (float64 por venir de json).
func numFromMeta(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("objeto carece de clave %q", key)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%q = %T, want float64", key, v)
	}
	return int64(f)
}

// resetZotRegistry deja el registry de clientconfig libre de "zot"
// (P10 opera sobre el mapa exportado global). Está acotado a borrar solo el
// adaptador "zot" para no destruir registros de otros adaptadores
// (002/003) que corran en el mismo binario de tests.
func resetZotRegistry(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		delete(clientconfig.Renderers, "zot")
	})
}

// irWithMeta construye un ConfigIR de un solo modelo "m1" con la Meta dada,
// para aislar comportamientos de metadata en un test sin arrastrar los
// modelos del fixture por defecto.
func irWithMeta(meta map[string]any) clientconfig.ConfigIR {
	return clientconfig.ConfigIR{
		ClientID:  "test-client",
		BaseURL:   "https://gw/v1",
		KeyEnvRef: "MOFGW_KEY",
		Models: []clientconfig.ModelEntry{
			{ID: "m1", Meta: meta},
		},
	}
}

// renderModelEntry renderiza un IR de un solo modelo "m1" con la Meta dada y
// devuelve su entrada `models[0]` deserializada + el body crudo.
func renderModelEntry(t *testing.T, meta map[string]any) (map[string]any, string) {
	t.Helper()
	var r Renderer
	body, err := r.Render(irWithMeta(meta))
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}
	entries := walkModelsArray(t, body)
	if len(entries) != 1 {
		t.Fatalf("models[] = %d entradas, want 1", len(entries))
	}
	if got, _ := entries[0]["id"].(string); got != "m1" {
		t.Fatalf("models[0].id = %q, want \"m1\"", entries[0]["id"])
	}
	return entries[0], string(body)
}

// ---------------------------------------------------------------------------
// P1 — JSON válido con top-level "providers" (no "models", no "mode")
// ---------------------------------------------------------------------------

func TestRED_RenderJsonValidoTopLevelProviders_P1(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	if !json.Valid(body) {
		t.Fatalf("Render no emitió JSON válido: %s", body)
	}

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("json.Unmarshal(body) error: %v", err)
	}
	if _, ok := top["providers"]; !ok {
		t.Fatalf("top-level sin clave \"providers\": %s", body)
	}
	// Diferencial: zot no usa "models" top-level ni "mode".
	if _, ok := top["models"]; ok {
		t.Fatalf("top-level no debe tener \"models\" (shape zot es providers): %s", body)
	}
}

// ---------------------------------------------------------------------------
// P2 — providers.mofgw.baseUrl fiel al IR
// ---------------------------------------------------------------------------

func TestRED_RenderBaseURLFielAlIR_P2(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, mofgw := walkTop(t, body)
	if got, _ := mofgw["baseUrl"].(string); got != "https://gw/v1" {
		t.Fatalf("providers.mofgw.baseUrl = %q, want \"https://gw/v1\"", mofgw["baseUrl"])
	}
}

// ---------------------------------------------------------------------------
// P3 — DESVIACIÓN I1: el output NO tiene clave "apiKey"/"key" ni template
// ---------------------------------------------------------------------------

func TestRED_RenderSinApiKeyNiTemplate_P3(t *testing.T) {
	var r Renderer
	ir := newIR() // KeyEnvRef = "MOFGW_KEY" — secret-looking, deliberado
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, mofgw := walkTop(t, body)

	// zot NO elige campo apiKey en absoluto (JsonShape UserModelsFile).
	if _, has := mofgw["apiKey"]; has {
		t.Fatalf("providers.mofgw NO debe tener clave \"apiKey\" (zot resuelve por env derivada MOFGW_API_KEY): %v", mofgw)
	}
	if _, has := mofgw["key"]; has {
		t.Fatalf("providers.mofgw NO debe tener clave \"key\": %v", mofgw)
	}

	// Ningún template de env por ningún lado (ni `${` ni `{env:`).
	if strings.Contains(string(body), "${") {
		t.Fatalf("output no debe contener template \"${\": %s", body)
	}
	if strings.Contains(string(body), "{env:") {
		t.Fatalf("output no debe contener template \"{env:\": %s", body)
	}

	// Ni siquiera el literal de la env que llevaba el IR debe filtrarse.
	if strings.Contains(string(body), "MOFGW_KEY") {
		t.Fatalf("output no debe contener el literal del KeyEnvRef: %s", body)
	}
}

// ---------------------------------------------------------------------------
// P4 — api == "openai" y provider accesible bajo clave "mofgw"
// ---------------------------------------------------------------------------

func TestRED_RenderApiOpenaiYProviderMofgw_P4(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, mofgw := walkTop(t, body)

	if got, _ := mofgw["api"].(string); got != "openai" {
		t.Fatalf("providers.mofgw.api = %q, want \"openai\"", mofgw["api"])
	}
}

// ---------------------------------------------------------------------------
// P5 — cada modelo en IR.Models tiene su entrada en mofgw.models[],
//       con contextWindow/maxTokens de Meta cuando está presente
// ---------------------------------------------------------------------------

func TestRED_RenderModelosYLimitsDesdeMeta_P5(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	entries := walkModelsArray(t, body)

	byID := map[string]map[string]any{}
	for _, e := range entries {
		id, _ := e["id"].(string)
		byID[id] = e
	}

	// model-a con metadata de limits.
	ea, ok := byID["model-a"]
	if !ok {
		t.Fatalf("providers.mofgw.models[] no contiene \"model-a\": %v", byID)
	}
	if got := numFromMeta(t, ea, "contextWindow"); got != 32000 {
		t.Fatalf("model-a contextWindow = %d, want 32000", got)
	}
	if got := numFromMeta(t, ea, "maxTokens"); got != 4096 {
		t.Fatalf("model-a maxTokens = %d, want 4096", got)
	}
	if got, _ := ea["name"].(string); got != "Model A" {
		t.Fatalf("model-a name = %q, want \"Model A\"", ea["name"])
	}

	// model-b existe como entrada (aunque no traiga limit).
	if _, ok := byID["model-b"]; !ok {
		t.Fatalf("models[] no contiene \"model-b\": %v", byID)
	}
}

// ---------------------------------------------------------------------------
// P6 — mofgw.models[] no tiene ids fuera de IR.Models
// ---------------------------------------------------------------------------

func TestRED_RenderModelosSinIdsExtraneos_P6(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	entries := walkModelsArray(t, body)

	known := map[string]bool{}
	for _, m := range ir.Models {
		known[m.ID] = true
	}
	for _, e := range entries {
		id, _ := e["id"].(string)
		if id == "" {
			t.Fatalf("entrada models[] sin id: %v", e)
		}
		if !known[id] {
			t.Fatalf("models[] contiene id fuera de IR.Models: %q", id)
		}
	}
	if len(entries) != len(ir.Models) {
		t.Fatalf("models[] = %d entradas, want %d (una por modelo, sin extras)", len(entries), len(ir.Models))
	}
}

// ---------------------------------------------------------------------------
// P7 — IR.Models vacío -> providers.mofgw.models == [] (array vacío,
//       JSON válido, sin error)
// ---------------------------------------------------------------------------

func TestRED_RenderModelosVacioArrayVacio_P7(t *testing.T) {
	var r Renderer
	ir := clientconfig.ConfigIR{
		ClientID: "test-client",
		BaseURL:  "https://gw/v1",
		Models:   nil,
	}

	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render con Models vacío devolvió error inesperado: %v", err)
	}

	if !json.Valid(body) {
		t.Fatalf("Render no emitió JSON válido: %s", body)
	}

	entries := walkModelsArray(t, body)
	if len(entries) != 0 {
		t.Fatalf("models[] debe ser array vacío, got %v", entries)
	}
}

// ---------------------------------------------------------------------------
// P8 — modelo sin metadata de limits -> entrada solo con "id"
//       (sin contextWindow/maxTokens/name, sin ceros)
// ---------------------------------------------------------------------------

func TestRED_RenderModeloSinMetaSoloID_P8(t *testing.T) {
	var r Renderer
	ir := clientconfig.ConfigIR{
		ClientID: "test-client",
		BaseURL:  "https://gw/v1",
		Models: []clientconfig.ModelEntry{
			{
				ID:   "no-meta-model",
				Meta: map[string]any{"object": "model", "owned_by": "acme"},
			},
		},
	}

	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	entries := walkModelsArray(t, body)
	if len(entries) != 1 {
		t.Fatalf("models[] = %d entradas, want 1", len(entries))
	}
	em := entries[0]
	if got, _ := em["id"].(string); got != "no-meta-model" {
		t.Fatalf("models[0].id = %q, want \"no-meta-model\"", em["id"])
	}
	if _, has := em["contextWindow"]; has {
		t.Fatalf("modelo sin metadata no debe tener \"contextWindow\": %v", em)
	}
	if _, has := em["maxTokens"]; has {
		t.Fatalf("modelo sin metadata no debe tener \"maxTokens\": %v", em)
	}
	if _, has := em["name"]; has {
		t.Fatalf("modelo sin metadata no debe tener \"name\": %v", em)
	}
}

// ---------------------------------------------------------------------------
// P9 — DESVIACIÓN I1: BaseURL vacío -> error; KeyEnvRef vacío -> NO error
// ---------------------------------------------------------------------------

func TestRED_RenderBaseURLVacioError_P9(t *testing.T) {
	var r Renderer
	ir := newIR()
	ir.BaseURL = ""

	if body, err := r.Render(ir); err == nil {
		t.Fatalf("Render con BaseURL vacío devolvió sin error (body=%s)", body)
	}
}

// Diferencial I1: a diferencia de opencode/openclaw (002/003), en zot el
// KeyEnvRef NO es obligatorio: el adapter NO emite campo de key en absoluto y
// la key se resuelve por env derivada MOFGW_API_KEY. Por tanto KeyEnvRef vacío
// NO es error (y el output sigue siendo un fragmento válido sin la key).
func TestRED_RenderKeyEnvRefVacioNoError_P9(t *testing.T) {
	var r Renderer
	ir := newIR()
	ir.KeyEnvRef = ""

	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render con KeyEnvRef vacío NO debe ser error (desviación I1 zot): %v", err)
	}

	// Además el fragmento resultante sigue siendo válido y sin key.
	if !json.Valid(body) {
		t.Fatalf("Render con KeyEnvRef vacío no emitió JSON válido: %s", body)
	}
	_, mofgw := walkTop(t, body)
	if _, has := mofgw["apiKey"]; has {
		t.Fatalf("KeyEnvRef vacío no debe materializar clave apiKey: %v", mofgw)
	}
}

// ---------------------------------------------------------------------------
// P10 — integración con el registry público de 016-001
// ---------------------------------------------------------------------------

func TestRED_RegisterLookupSupportedList_P10(t *testing.T) {
	resetZotRegistry(t)

	if before := clientconfig.Lookup("zot"); before != nil {
		t.Fatalf("Lookup(\"zot\") recién reseteado no debe ser nil-registrado: %v", before)
	}

	r := Renderer{}
	clientconfig.Register("zot", r)

	if clientconfig.Lookup("zot") == nil {
		t.Fatal("Lookup(\"zot\") = nil, want el renderer registrado")
	}

	found := false
	for _, id := range clientconfig.SupportedList() {
		if id == "zot" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SupportedList() no contiene \"zot\": %v", clientconfig.SupportedList())
	}
}

// ---------------------------------------------------------------------------
// P11 — orden determinista: dos llamadas, mismos bytes
// ---------------------------------------------------------------------------

func TestRED_RenderDeterministaMismosBytes_P11(t *testing.T) {
	var r Renderer
	ir := newIR()

	first, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render #1 error: %v", err)
	}
	second, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render #2 error: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("Render no es determinista:\nfirst = %s\nsecond= %s", first, second)
	}
	if string(first) != string(second) {
		t.Fatalf("Render no es determinista (strings diff)")
	}
}

// ---------------------------------------------------------------------------
// P12 — fallback-rule (lección de la review de 016-002/003):
//   (a) context_length presente pero 0 -> sin contextWindow
//   (b) solo max_context_length (sin context_length) -> contextWindow = fallback
//   (c) max_completion_tokens presente pero 0 (sin max_output_tokens) -> sin maxTokens
//   (d) solo max_completion_tokens (sin max_output_tokens) -> maxTokens = fallback
// ---------------------------------------------------------------------------

func TestRED_Review_ContextLengthCeroNoEmiteContextWindow_P12a(t *testing.T) {
	entry, _ := renderModelEntry(t, map[string]any{
		"context_length":    float64(0), // presente pero zero-value
		"max_output_tokens": float64(4096),
	})
	if _, has := entry["contextWindow"]; has {
		t.Fatalf("context_length=0 presente no debe emitir contextWindow: %v", entry)
	}
	if got := numFromMeta(t, entry, "maxTokens"); got != 4096 {
		t.Fatalf("maxTokens = %d, want 4096", got)
	}
}

func TestRED_Review_OutputCeroNoEmiteMaxTokens_P12c(t *testing.T) {
	entry, _ := renderModelEntry(t, map[string]any{
		"context_length":        float64(32000),
		"max_completion_tokens": float64(0), // presente pero zero-value
	})
	if _, has := entry["maxTokens"]; has {
		t.Fatalf("max_completion_tokens=0 presente no debe emitir maxTokens: %v", entry)
	}
	if got := numFromMeta(t, entry, "contextWindow"); got != 32000 {
		t.Fatalf("contextWindow = %d, want 32000", got)
	}
}

func TestRED_Review_FallbackSoloMaxContextLength_P12b(t *testing.T) {
	entry, _ := renderModelEntry(t, map[string]any{
		"max_context_length": float64(8000), // sin context_length primario
	})
	if got := numFromMeta(t, entry, "contextWindow"); got != 8000 {
		t.Fatalf("contextWindow = %d, want 8000 (fallback max_context_length)", got)
	}
	if _, has := entry["maxTokens"]; has {
		t.Fatalf("sin metadata de output no debe emitir maxTokens: %v", entry)
	}
}

func TestRED_Review_FallbackSoloMaxCompletionTokens_P12d(t *testing.T) {
	entry, _ := renderModelEntry(t, map[string]any{
		"max_completion_tokens": float64(2048), // sin max_output_tokens primario
	})
	if got := numFromMeta(t, entry, "maxTokens"); got != 2048 {
		t.Fatalf("maxTokens = %d, want 2048 (fallback max_completion_tokens)", got)
	}
	if _, has := entry["contextWindow"]; has {
		t.Fatalf("sin metadata de context no debe emitir contextWindow: %v", entry)
	}
}