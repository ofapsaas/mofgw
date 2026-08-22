// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-002-adapter-opencode — contrato del primer
// adapter concreto del epic mofgw-client-config.
//
// El renderer (`opencode.Renderer`) serializa un `clientconfig.ConfigIR` en
// el fragmento JSON del provider `mofgw` listo para insertar bajo la clave
// `provider` de `opencode.json` (P1-P9, P11). P10 valida la integración con
// el registry público de 016-001 (clientconfig.Register/Lookup/SupportedList).
//
// Contrato que estas pruebas exigen (RED, will-not-compile):
//   - tipo `opencode.Renderer` (struct) con método
//     `Render(ir clientconfig.ConfigIR) ([]byte, error)`
//   - emite JSON válido con top-level clave `"mofgw"`:
//     { mofgw: { npm, name, options:{baseURL, apiKey}, models:{<id>:{name?, limit?}} } }
//   - `options.apiKey` como template STRING "{env:<KeyEnvRef>}" (nunca objeto,
//     nunca literal sk-); Models exclusivamente desde IR.Models.

package opencode

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

// walkMofgw deserializa el body y devuelve el objeto bajo `mofgw.options`
// y `mofgw.models`, fallando si la forma esperada no está. Falla el test si
// el JSON no es válido o no contiene la clave top-level `mofgw`.
func walkMofgw(t *testing.T, body []byte) (options map[string]any, models map[string]any) {
	t.Helper()

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("Render no emitió JSON válido: %v (body=%s)", err, body)
	}

	mofgwAny, ok := top["mofgw"]
	if !ok {
		t.Fatalf("top-level sin clave \"mofgw\": %s", body)
	}
	mofgw, ok := mofgwAny.(map[string]any)
	if !ok {
		t.Fatalf("mofgw debe ser un objeto, got %T", mofgwAny)
	}

	optsAny, ok := mofgw["options"]
	if !ok {
		t.Fatalf("mofgw sin clave \"options\": %s", body)
	}
	options, ok = optsAny.(map[string]any)
	if !ok {
		t.Fatalf("mofgw.options debe ser un objeto, got %T", optsAny)
	}

	modelsAny, ok := mofgw["models"]
	if !ok {
		t.Fatalf("mofgw sin clave \"models\": %s", body)
	}
	models, ok = modelsAny.(map[string]any)
	if !ok {
		t.Fatalf("mofgw.models debe ser un objeto, got %T (era %T)", modelsAny, models)
	}

	return options, models
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

// resetOpenCodeRegistry deja el registry de clientconfig libre de "opencode"
// (P10 opera sobre el mapa exportado global). Está acotado a borrar solo el
// adaptador "opencode" para no destruir registros de otros adaptadores
// (003/004) que corran en el mismo binario de tests.
func resetOpenCodeRegistry(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		delete(clientconfig.Renderers, "opencode")
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

// renderModelMeta renderiza un IR de un modelo "m1" con la Meta dada y
// devuelve su entrada `models.m1` deserializada + el body crudo.
func renderModelMeta(t *testing.T, meta map[string]any) (map[string]any, string) {
	t.Helper()
	var r Renderer
	body, err := r.Render(irWithMeta(meta))
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}
	_, models := walkMofgw(t, body)
	raw, ok := models["m1"]
	if !ok {
		t.Fatalf("mofgw.models no contiene \"m1\": %v", models)
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("models[\"m1\"] = %T, want objeto", raw)
	}
	return entry, string(body)
}

// ---------------------------------------------------------------------------
// P1 — JSON válido con top-level "mofgw"
// ---------------------------------------------------------------------------

func TestRED_RenderJsonValidoTopLevelMofgw_P1(t *testing.T) {
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
	if _, ok := top["mofgw"]; !ok {
		t.Fatalf("top-level sin clave \"mofgw\": %s", body)
	}
}

// ---------------------------------------------------------------------------
// P2 — options.baseURL fiel al IR
// ---------------------------------------------------------------------------

func TestRED_RenderBaseURLFielAlIR_P2(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	options, _ := walkMofgw(t, body)
	if got, _ := options["baseURL"].(string); got != "https://gw/v1" {
		t.Fatalf("options.baseURL = %q, want \"https://gw/v1\"", options["baseURL"])
	}
}

// ---------------------------------------------------------------------------
// P3 — options.apiKey es template STRING "{env:<KeyEnvRef>}"
// ---------------------------------------------------------------------------

func TestRED_RenderApiKeyEnvRefString_P3(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	options, _ := walkMofgw(t, body)

	apiKey, ok := options["apiKey"].(string)
	if !ok {
		t.Fatalf("options.apiKey debe ser un string, got %T (%v)", options["apiKey"], options["apiKey"])
	}

	want := "{env:MOFGW_KEY}"
	if apiKey != want {
		t.Fatalf("options.apiKey = %q, want %q (template env-ref STRING)", apiKey, want)
	}

	// Nunca debe contener un literal de secret tipo sk-.
	if strings.Contains(string(body), "sk-") {
		t.Fatalf("output no debe contener literal de secret tipo sk-: %s", body)
	}
}

// ---------------------------------------------------------------------------
// P4 — npm y name
// ---------------------------------------------------------------------------

func TestRED_RenderNpmYName_P4(t *testing.T) {
	var r Renderer
	body, err := r.Render(newIR())
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	mofgw, _ := top["mofgw"].(map[string]any)

	if got, _ := mofgw["npm"].(string); got != "@ai-sdk/openai-compatible" {
		t.Fatalf("mofgw.npm = %q, want \"@ai-sdk/openai-compatible\"", mofgw["npm"])
	}
	if got, _ := mofgw["name"].(string); got != "mofgw" {
		t.Fatalf("mofgw.name = %q, want \"mofgw\"", mofgw["name"])
	}
}

// ---------------------------------------------------------------------------
// P5 — cada modelo en IR.Models tiene su entrada en mofgw.models,
//       con limit.context/output de Meta cuando está presente
// ---------------------------------------------------------------------------

func TestRED_RenderModelosYLimitsDPMeta_P5(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, models := walkMofgw(t, body)

	// model-a con metadata de limits.
	entryA, ok := models["model-a"]
	if !ok {
		t.Fatalf("mofgw.models no contiene \"model-a\": %v", models)
	}
	ea, ok := entryA.(map[string]any)
	if !ok {
		t.Fatalf("models[\"model-a\"] = %T, want objeto", entryA)
	}
	limitA, ok := ea["limit"].(map[string]any)
	if !ok {
		t.Fatalf("models[\"model-a\"].limit ausente o no-objeto: %v", ea)
	}
	if got := numFromMeta(t, limitA, "context"); got != 32000 {
		t.Fatalf("model-a limit.context = %d, want 32000", got)
	}
	if got := numFromMeta(t, limitA, "output"); got != 4096 {
		t.Fatalf("model-a limit.output = %d, want 4096", got)
	}

	// model-b existe como entrada (aunque no traiga limit).
	if _, ok := models["model-b"]; !ok {
		t.Fatalf("mofgw.models no contiene \"model-b\": %v", models)
	}
}

// ---------------------------------------------------------------------------
// P6 — mofgw.models no tiene ids fuera de IR.Models
// ---------------------------------------------------------------------------

func TestRED_RenderModelosSinIdsExtraneos_P6(t *testing.T) {
	var r Renderer
	ir := newIR()
	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, models := walkMofgw(t, body)

	known := map[string]bool{}
	for _, m := range ir.Models {
		known[m.ID] = true
	}
	for id := range models {
		if !known[id] {
			t.Fatalf("mofgw.models contiene id fuera de IR.Models: %q", id)
		}
	}
}

// ---------------------------------------------------------------------------
// P7 — IR.Models vacío -> mofgw.models == {} (JSON válido, sin error)
// ---------------------------------------------------------------------------

func TestRED_RenderModelosVacioObjetoVacio_P7(t *testing.T) {
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

	_, models := walkMofgw(t, body)
	if len(models) != 0 {
		t.Fatalf("mofgw.models debe ser objeto vacío, got %v", models)
	}
}

// ---------------------------------------------------------------------------
// P8 — modelo sin metadata de limits -> sin clave "limit" (sin ceros)
// ---------------------------------------------------------------------------

func TestRED_RenderModeloSinMetaSinLimit_P8(t *testing.T) {
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

	_, models := walkMofgw(t, body)
	entry, ok := models["no-meta-model"]
	if !ok {
		t.Fatalf("mofgw.models no contiene \"no-meta-model\": %v", models)
	}
	em, ok := entry.(map[string]any)
	if !ok {
		t.Fatalf("models[\"no-meta-model\"] = %T, want objeto", entry)
	}
	if _, has := em["limit"]; has {
		t.Fatalf("models[\"no-meta-model\"] no debe tener clave \"limit\" (sin metadata): %v", em)
	}
	if strings.Contains(string(body), `"limit"`) {
		t.Fatalf("output no debe emitir clave \"limit\" (modelo sin metadata): %s", body)
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
	resetOpenCodeRegistry(t)

	if before := clientconfig.Lookup("opencode"); before != nil {
		t.Fatalf("Lookup(\"opencode\") recién reseteado no debe ser nil-registrado: %v", before)
	}

	r := Renderer{}
	clientconfig.Register("opencode", r)

	if clientconfig.Lookup("opencode") == nil {
		t.Fatal("Lookup(\"opencode\") = nil, want el renderer registrado")
	}

	found := false
	for _, id := range clientconfig.SupportedList() {
		if id == "opencode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SupportedList() no contiene \"opencode\": %v", clientconfig.SupportedList())
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

	// Como el orden de mapas de Go es no-determinista por diseño en Marshal,
	// un orden estable requiere ordenar claves o equivalente: exigimos que dos
	// salidas coincidan byte a byte.
	if string(first) != string(second) {
		t.Fatalf("Render no es determinista (strings diff)")
	}
}

// ---------------------------------------------------------------------------
// Voluntario — fallbacks de claves alternativas (max_context_length /
// max_completion_tokens) si el renderer decide soportarlos; presente solo para
// fijar el contrato de fallback del spec, sin imponer su implementación.
// ---------------------------------------------------------------------------

func TestRED_RenderModeloLimitIgualEnFallbackKeys_P5(t *testing.T) {
	var r Renderer
	ir := clientconfig.ConfigIR{
		ClientID:  "test-client",
		BaseURL:   "https://gw/v1",
		KeyEnvRef: "MOFGW_KEY",
		Models: []clientconfig.ModelEntry{
			{
				ID: "fb-model",
				Meta: map[string]any{
					"context_length":        float64(16000),
					"max_output_tokens":     float64(1024),
					"max_context_length":    float64(0), // alternativo, no tomado si el principal está presente
					"max_completion_tokens": float64(0), // alternativo, no tomado
				},
			},
		},
	}

	body, err := r.Render(ir)
	if err != nil {
		t.Fatalf("Render error inesperado: %v", err)
	}

	_, models := walkMofgw(t, body)
	entry, _ := models["fb-model"].(map[string]any)
	limit, _ := entry["limit"].(map[string]any)

	if got := numFromMeta(t, limit, "context"); got != 16000 {
		t.Fatalf("fb-model limit.context = %d, want 16000 (context_length primario)", got)
	}
	if got := numFromMeta(t, limit, "output"); got != 1024 {
		t.Fatalf("fb-model limit.output = %d, want 1024 (max_output_tokens primario)", got)
	}
}

// ---------------------------------------------------------------------------
// Review 016-002: finding 1a — context_length presente pero 0 no emite
// limit.context (fallback-rule: ausente ≠ 0).
// ---------------------------------------------------------------------------

func TestRED_Review_ContextLengthCeroNoEmiteLimitContext(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"context_length":    float64(0), // presente pero zero-value
		"max_output_tokens": float64(4096),
	})

	limit, hasLimit := entry["limit"].(map[string]any)
	if !hasLimit {
		t.Fatalf("esperaba limit presente (output=4096 no es 0): %v", entry)
	}
	if _, has := limit["context"]; has {
		t.Fatalf("context_length=0 presente no debe emitir limit.context: %v", limit)
	}
	if got := numFromMeta(t, limit, "output"); got != 4096 {
		t.Fatalf("limit.output = %d, want 4096", got)
	}
}

// ---------------------------------------------------------------------------
// Review 016-002: finding 1b — max_completion_tokens presente pero 0 (sin
// max_output_tokens primario) no emite limit.output.
// ---------------------------------------------------------------------------

func TestRED_Review_OutputCeroNoEmiteLimitOutput(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"context_length":        float64(32000),
		"max_completion_tokens": float64(0), // presente pero zero-value
	})

	limit, hasLimit := entry["limit"].(map[string]any)
	if !hasLimit {
		t.Fatalf("esperaba limit presente (context=32000 no es 0): %v", entry)
	}
	if _, has := limit["output"]; has {
		t.Fatalf("max_completion_tokens=0 presente no debe emitir limit.output: %v", limit)
	}
	if got := numFromMeta(t, limit, "context"); got != 32000 {
		t.Fatalf("limit.context = %d, want 32000", got)
	}
}

// ---------------------------------------------------------------------------
// Review 016-002: finding 2 — el trigger del fallback (primario ausente →
// valor del alternativo) está cubierto.
// ---------------------------------------------------------------------------

func TestRED_Review_FallbackSoloMaxContextLength(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"max_context_length": float64(8000), // sin context_length primario
	})

	limit, ok := entry["limit"].(map[string]any)
	if !ok {
		t.Fatalf("esperaba limit presente vía fallback max_context_length: %v", entry)
	}
	if got := numFromMeta(t, limit, "context"); got != 8000 {
		t.Fatalf("limit.context = %d, want 8000 (fallback max_context_length)", got)
	}
	if _, has := limit["output"]; has {
		t.Fatalf("sin metadata de output no debe emitir limit.output: %v", limit)
	}
}

func TestRED_Review_FallbackSoloMaxCompletionTokens(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"max_completion_tokens": float64(2048), // sin max_output_tokens primario
	})

	limit, ok := entry["limit"].(map[string]any)
	if !ok {
		t.Fatalf("esperaba limit presente vía fallback max_completion_tokens: %v", entry)
	}
	if got := numFromMeta(t, limit, "output"); got != 2048 {
		t.Fatalf("limit.output = %d, want 2048 (fallback max_completion_tokens)", got)
	}
}

// ---------------------------------------------------------------------------
// Review 016-002: finding 3 — el branch "name" lee Meta["display_name"]:
// emitido cuando está presente (no vacío) y omitido cuando no.
// ---------------------------------------------------------------------------

func TestRED_Review_NameDesdeDisplayNameEmite(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"display_name": "My Nice Model",
		"object":       "model",
	})
	if got, _ := entry["name"].(string); got != "My Nice Model" {
		t.Fatalf("models.m1.name = %q, want \"My Nice Model\" (desde display_name)", entry["name"])
	}
}

func TestRED_Review_SinDisplayNameOmiteName(t *testing.T) {
	entry, _ := renderModelMeta(t, map[string]any{
		"object": "model",
	})
	if _, has := entry["name"]; has {
		t.Fatalf("sin display_name no debe emitir name: %v", entry)
	}
}
