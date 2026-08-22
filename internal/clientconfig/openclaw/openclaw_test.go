// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-003-adapter-openclaw — contrato del adapter del
// fragmento config del provider `mofgw` para OpenClaw.
//
// El renderer (`openclaw.Renderer`) serializa un `clientconfig.ConfigIR` en el
// fragmento JSON (JSON5-compatible) listo para insertar/mergear bajo la rama
// `models` de `~/.openclaw/openclaw.json` (P1-P11). P12 fija la fallback-rule
// (presente-pero-≤0) heredada de la review de 016-002.
//
// Contrato que estas pruebas exigen (RED, will-not-compile):
//   - tipo `openclaw.Renderer` (struct) con método
//     `Render(ir clientconfig.ConfigIR) ([]byte, error)`
//   - emite JSON válido con top-level clave `"models"`:
//     { models: { mode, providers: { mofgw: { baseUrl, apiKey, api, models: [] } } } }
//   - `providers.mofgw.models` es un ARRAY de objetos {id, name?, contextWindow?,
//     maxTokens?} (NO objeto keyed, NO mapa por id).
//   - `providers.mofgw.apiKey` como template STRING "${<KeyEnvRef>}" (nunca
//     objeto SecretRef, nunca literal sk-); models exclusivamente desde IR.Models.

package openclaw

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
// otro sin ella) para recorrer el JSON de salida.
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

// walkModels deserializa el body y devuelve el objeto top-level `models` y el
// objeto `models.providers.mofgw` (con BaseURL/apiKey/api/models) si el JSON es
// válido y la forma esperada está. Un modelos[] vacío se devuelve como slice
// vacío (no nil), para diferenciar "array vacío" (P7) de "ausente".
func walkModels(t *testing.T, body []byte) (modelsTop map[string]any, mofgw map[string]any) {
	t.Helper()

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("Render no emitió JSON válido: %v (body=%s)", err, body)
	}

	modelsAny, ok := top["models"]
	if !ok {
		t.Fatalf("top-level sin clave \"models\": %s", body)
	}
	modelsTop, ok = modelsAny.(map[string]any)
	if !ok {
		t.Fatalf("\"models\" debe ser un objeto, got %T", modelsAny)
	}

	providersAny, ok := modelsTop["providers"]
	if !ok {
		t.Fatalf("models sin clave \"providers\": %s", body)
	}
	providers, ok := providersAny.(map[string]any)
	if !ok {
		t.Fatalf("models.providers debe ser un objeto, got %T", providersAny)
	}

	mofgwAny, ok := providers["mofgw"]
	if !ok {
		t.Fatalf("models.providers sin clave \"mofgw\": %s", body)
	}
	mofgw, ok = mofgwAny.(map[string]any)
	if !ok {
		t.Fatalf("models.providers.mofgw debe ser un objeto, got %T", mofgwAny)
	}

	return modelsTop, mofgw
}

// walkModelsArray extrae y devuelve `models.providers.mofgw.models` como
// []map[string]any (cada entrada es un objeto). Falla si no es un array.
func walkModelsArray(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	_, mofgw := walkModels(t, body)

	arr, ok := mofgw["models"].([]any)
	if !ok {
		t.Fatalf("models.providers.mofgw.models debe ser un array, got %T (%v)", mofgw["models"], mofgw["models"])
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

// numFromMeta extrae un int64 desde un valor Meta ya deserializado
// (float64 por venir de json).
func numFromMeta(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("Meta carece de clave %q", key)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("Meta[%q] = %T, want float64", key, v)
	}
	return int64(f)
}

// resetOpenClawRegistry deja el registry de clientconfig libre de "openclaw"
// (P10 opera sobre el mapa exportado global). Está acotado a borrar solo el
// adaptador "openclaw" para no destruir registros de otros adaptadores
// (002/004) que corran en el mismo binario de tests.
func resetOpenClawRegistry(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		delete(clientconfig.Renderers, "openclaw")
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
// P1 — JSON válido con top-level "models"
// ---------------------------------------------------------------------------

func TestRED_RenderJsonValidoTopLevelModels_P1(t *testing.T) {
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
	if _, ok := top["models"]; !ok {
		t.Fatalf("top-level sin clave \"models\": %s", body)
	}
}

// ---------------------------------------------------------------------------
// P2 — models.providers.mofgw.baseUrl fiel al IR
// ---------------------------------------------------------------------------

func TestRED_RenderBaseURLFielAlIR_P2(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, mofgw := walkModels(t, body)
	if got, _ := mofgw["baseUrl"].(string); got != "https://gw/v1" {
		t.Fatalf("models.providers.mofgw.baseUrl = %q, want \"https://gw/v1\"", mofgw["baseUrl"])
	}
}

// ---------------------------------------------------------------------------
// P3 — providers.mofgw.apiKey es template STRING "${<KeyEnvRef>}"
// ---------------------------------------------------------------------------

func TestRED_RenderApiKeyEnvRefString_P3(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, mofgw := walkModels(t, body)

	apiKey, ok := mofgw["apiKey"].(string)
	if !ok {
		t.Fatalf("models.providers.mofgw.apiKey debe ser un string, got %T (%v)", mofgw["apiKey"], mofgw["apiKey"])
	}

	want := "${MOFGW_KEY}"
	if apiKey != want {
		t.Fatalf("models.providers.mofgw.apiKey = %q, want %q (template env-ref STRING)", apiKey, want)
	}

	// Nunca debe contener un literal de secret tipo sk-.
	if strings.Contains(string(body), "sk-") {
		t.Fatalf("output no debe contener literal de secret tipo sk-: %s", body)
	}
}

// ---------------------------------------------------------------------------
// P4 — api == "openai-completions" y key del provider "mofgw"
// ---------------------------------------------------------------------------

func TestRED_RenderApiYProviderMofgw_P4(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	modelsTop, _ := top["models"].(map[string]any)
	providers, _ := modelsTop["providers"].(map[string]any)
	mofgw, ok := providers["mofgw"].(map[string]any)
	if !ok {
		t.Fatalf("models.providers.mofgw debe ser un objeto, got %T", providers["mofgw"])
	}

	if got, _ := mofgw["api"].(string); got != "openai-completions" {
		t.Fatalf("models.providers.mofgw.api = %q, want \"openai-completions\"", mofgw["api"])
	}
	if _, ok := providers["mofgw"]; !ok {
		t.Fatalf("models.providers no contiene la key de provider \"mofgw\": %v", providers)
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
		t.Fatalf("models.providers.mofgw.models[] no contiene \"model-a\": %v", byID)
	}
	if got := numFromMeta(t, ea, "contextWindow"); got != 32000 {
		t.Fatalf("model-a contextWindow = %d, want 32000", got)
	}
	if got := numFromMeta(t, ea, "maxTokens"); got != 4096 {
		t.Fatalf("model-a maxTokens = %d, want 4096", got)
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
// P7 — IR.Models vacío -> models.providers.mofgw.models == [] (array vacío,
//       JSON válido, sin error)
// ---------------------------------------------------------------------------

func TestRED_RenderModelosVacioArrayVacio_P7(t *testing.T) {
	var r Renderer
	ir := clientconfig.ConfigIR{
		ClientID:  "test-client",
		BaseURL:   "https://gw/v1",
		KeyEnvRef: "MOFGW_KEY",
		Models:    nil,
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
		ClientID:  "test-client",
		BaseURL:   "https://gw/v1",
		KeyEnvRef: "MOFGW_KEY",
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
// P9 — BaseURL o KeyEnvRef vacíos -> Render error no-nil
// ---------------------------------------------------------------------------

func TestRED_RenderBaseURVVacioError_P9(t *testing.T) {
	var r Renderer
	ir := newIR()
	ir.BaseURL = ""

	if body, err := r.Render(ir); err == nil {
		t.Fatalf("Render con BaseURL vacío devolvió sin error (body=%s)", body)
	}
}

func TestRED_RenderKeyEnvRefVacioError_P9(t *testing.T) {
	var r Renderer
	ir := newIR()
	ir.KeyEnvRef = ""

	if body, err := r.Render(ir); err == nil {
		t.Fatalf("Render con KeyEnvRef vacío devolvió sin error (body=%s)", body)
	}
}

// ---------------------------------------------------------------------------
// P10 — integración con el registry público de 016-001
// ---------------------------------------------------------------------------

func TestRED_RegisterLookupSupportedList_P10(t *testing.T) {
	resetOpenClawRegistry(t)

	if before := clientconfig.Lookup("openclaw"); before != nil {
		t.Fatalf("Lookup(\"openclaw\") recién reseteado no debe ser nil-registrado: %v", before)
	}

	r := Renderer{}
	clientconfig.Register("openclaw", r)

	if clientconfig.Lookup("openclaw") == nil {
		t.Fatal("Lookup(\"openclaw\") = nil, want el renderer registrado")
	}

	found := false
	for _, id := range clientconfig.SupportedList() {
		if id == "openclaw" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SupportedList() no contiene \"openclaw\": %v", clientconfig.SupportedList())
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
// P12 — fallback-rule (lección de la review de 016-002):
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