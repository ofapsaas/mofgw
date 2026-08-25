// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 010-001-catalogo-fiel: catálogo completo, veraz
// y prescriptivo en /v1/models — supported_parameters, modality y
// architecture derivada, top_provider/max_output_tokens, thinking_default
// prescriptivo. Verifican postcondiciones P1-P8 del spec
// docs/specs/010-001-catalogo-fiel/spec.md.
//
// Contrato observable: GET /v1/models con auth → objeto JSON por modelo
// con los campos nuevos EMITIDOS solo cuando la metadata los declara
// (fallback rule: ausente ≠ 0/[]/{}). Cero red externa: upstreams fake
// (httptest).
//
// Estrategia anti-compile-error: la metadata se carga desde un config YAML
// (observable path: config.LoadFile → SetModelMetadata) y NUNCA se
// referencian los campos tipados SupportedParameters/Modality (no existen
// aún). El parser actual ignora las claves desconocidas → el config carga,
// la metadata queda sin los valores nuevos y el JSON de /v1/models no
// emite los campos → las aserciones positivas (P3/P4/P6/P5) fallan por
// campo ausente. Cuando el implementer agregue los campos + parsing +
// emisión, los mismos tests pasan sin modificación.

package proxy_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
)

// ---- helpers: config YAML observable ----

// catalogYAML arma un config YAML válido con el bloque model_metadata dado
// (bloque con indentación de 2 espacios respecto de model_metadata:).
func catalogYAML(metadataBlock string) string {
	return `server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_010001_TEST_KEY
    models: ["m"]
model_metadata:
` + metadataBlock
}

// writeConfigYAML escribe el YAML y lo carga con el loader real (P1:
// observable path del config). Falla el test si el YAML no carga.
func writeConfigYAML(t *testing.T, yaml string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOFGW_010001_TEST_KEY", "k1")
	cfg, err := config.LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return cfg
}

// buildWithCatalogYAML construye el harness con los modelos dados en el
// catálogo (buildWithModels) y la metadata cargada desde el config YAML
// (vía SetModelMetadata — mismo patrón que e2e_007002_test.go).
func buildWithCatalogYAML(t *testing.T, models []string, yaml string, clientKey string) *harness {
	t.Helper()
	cfg := writeConfigYAML(t, yaml)
	ups := make([]*upstream, len(models))
	for i, m := range models {
		ups[i] = upstreamOK(m, "x")
	}
	h := buildWithModels(t, ups, models, clientKey)
	h.proxySrv.SetModelMetadata(cfg.ModelMetadata)
	return h
}

// wantStringSlice verifica que el valor deserializado de JSON sea
// exactamente el slice de strings esperado, en ese orden.
func wantStringSlice(t *testing.T, got any, want []string) {
	t.Helper()
	gs, ok := got.([]any)
	if !ok {
		t.Fatalf("valor = %#v (%T), want %v", got, got, want)
	}
	if len(gs) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(gs), len(want), gs)
	}
	for i, w := range want {
		s, ok := gs[i].(string)
		if !ok || s != w {
			t.Fatalf("[%d] = %#v, want %q", i, gs[i], w)
		}
	}
}

// wantAbsent falla si la clave está presente (fallback rule: ausente ≠
// 0/[]/{}; nunca se emiten zero-values).
func wantAbsent(t *testing.T, item map[string]any, key string) {
	t.Helper()
	if v, ok := item[key]; ok {
		t.Fatalf("%q presente con valor %#v — debe estar AUSENTE (fallback rule)", key, v)
	}
}

// ---- datos: metadata C1 del spec (Datos esperados) ----

const metaC1 = `  m:
    context_window: 1000000
    max_output: 384000
    thinking: ["low", "high", "max"]
    thinking_default: "low"
    supported_parameters: ["tools", "reasoning"]
    modality: "text+image+video->text"
`

// ---- P3: supported_parameters fiel (valores y orden exactos, sin
// ordenar/deduplicar/agregar) ----

func TestE2E010001_P3_SupportedParameters(t *testing.T) {
	h := buildWithCatalogYAML(t, []string{"m"}, catalogYAML(metaC1), "k1")
	item := findModel(t, modelsBody(t, h), "m")
	wantStringSlice(t, item["supported_parameters"], []string{"tools", "reasoning"})
}

// ---- P4: modality y architecture derivada (split mecánico -> y +) ----

func TestE2E010001_P4_ModalityYArchitecture(t *testing.T) {
	meta := `  full:
    context_window: 1000000
    max_output: 384000
    supported_parameters: ["tools", "reasoning"]
    modality: "text+image+video->text"
  textonly:
    modality: "text->text"
`
	h := buildWithCatalogYAML(t, []string{"full", "textonly"}, catalogYAML(meta), "k1")
	out := modelsBody(t, h)

	full := findModel(t, out, "full")
	if full["modality"] != "text+image+video->text" {
		t.Fatalf("modality = %v, want text+image+video->text", full["modality"])
	}
	arch, ok := full["architecture"].(map[string]any)
	if !ok {
		t.Fatalf("architecture ausente en full: %#v", full["architecture"])
	}
	if arch["modality"] != "text+image+video->text" {
		t.Fatalf("architecture.modality = %v, want text+image+video->text", arch["modality"])
	}
	wantStringSlice(t, arch["input_modalities"], []string{"text", "image", "video"})
	wantStringSlice(t, arch["output_modalities"], []string{"text"})

	tt := findModel(t, out, "textonly")
	if tt["modality"] != "text->text" {
		t.Fatalf("textonly modality = %v, want text->text", tt["modality"])
	}
	arch2, ok := tt["architecture"].(map[string]any)
	if !ok {
		t.Fatalf("architecture ausente en textonly: %#v", tt["architecture"])
	}
	if arch2["modality"] != "text->text" {
		t.Fatalf("textonly architecture.modality = %v, want text->text", arch2["modality"])
	}
	wantStringSlice(t, arch2["input_modalities"], []string{"text"})
	wantStringSlice(t, arch2["output_modalities"], []string{"text"})
}

// ---- P5: modality no parseable → passthrough + architecture AUSENTE ----

func TestE2E010001_P5_ModalityNoParseable(t *testing.T) {
	meta := `  m:
    context_window: 1000000
    max_output: 384000
    modality: "text"
  emptyside:
    modality: "text->"
`
	h := buildWithCatalogYAML(t, []string{"m", "emptyside"}, catalogYAML(meta), "k1")
	out := modelsBody(t, h)

	item := findModel(t, out, "m")
	if item["modality"] != "text" {
		t.Fatalf("modality = %v, want text (passthrough de dato declarado)", item["modality"])
	}
	wantAbsent(t, item, "architecture")

	es := findModel(t, out, "emptyside")
	if es["modality"] != "text->" {
		t.Fatalf("emptyside modality = %v, want text-> (passthrough)", es["modality"])
	}
	wantAbsent(t, es, "architecture")
}

// ---- P6: top_provider y max_output_tokens ----

func TestE2E010001_P6_TopProviderYMaxOutput(t *testing.T) {
	meta := `  full:
    context_window: 1000000
    max_output: 384000
    supported_parameters: ["tools", "reasoning"]
    modality: "text+image+video->text"
  ctxonly:
    context_window: 1000000
`
	h := buildWithCatalogYAML(t, []string{"full", "ctxonly"}, catalogYAML(meta), "k1")
	out := modelsBody(t, h)

	full := findModel(t, out, "full")
	tp, ok := full["top_provider"].(map[string]any)
	if !ok {
		t.Fatalf("top_provider ausente en full: %#v", full["top_provider"])
	}
	if tp["context_length"] != float64(1000000) {
		t.Fatalf("top_provider.context_length = %v, want 1000000", tp["context_length"])
	}
	if tp["max_completion_tokens"] != float64(384000) {
		t.Fatalf("top_provider.max_completion_tokens = %v, want 384000", tp["max_completion_tokens"])
	}
	if full["max_output_tokens"] != float64(384000) {
		t.Fatalf("max_output_tokens = %v, want 384000", full["max_output_tokens"])
	}

	// context_window > 0 pero max_output == 0 → solo context_length
	// (P6: se omiten max_completion_tokens y max_output_tokens).
	ctx := findModel(t, out, "ctxonly")
	tp2, ok := ctx["top_provider"].(map[string]any)
	if !ok {
		t.Fatalf("top_provider ausente en ctxonly: %#v", ctx["top_provider"])
	}
	if tp2["context_length"] != float64(1000000) {
		t.Fatalf("ctxonly top_provider.context_length = %v, want 1000000", tp2["context_length"])
	}
	wantAbsent(t, tp2, "max_completion_tokens")
	wantAbsent(t, ctx, "max_output_tokens")
}

// ---- P7: thinking_default prescriptivo (emite el configurado, no el
// nativo). El passthrough ya está verde por 007-002 (TestRED_ModelsEnriquecido
// verifica thinking.default == valor configurado); este test fija el caso
// "low" del spec C5 como guard. ----

func TestE2E010001_P7_ThinkingDefaultPrescriptivo(t *testing.T) {
	h := buildWithCatalogYAML(t, []string{"m"}, catalogYAML(metaC1), "k1")
	item := findModel(t, modelsBody(t, h), "m")
	think, ok := item["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("sin thinking object: %#v", item["thinking"])
	}
	if think["default"] != "low" {
		t.Fatalf("thinking.default = %v, want low (prescriptivo, no nativo)", think["default"])
	}
}

// ---- P2/C3: metadata SIN las claves nuevas → campos AUSENTES (nunca
// []/{} /0/false) ----

func TestE2E010001_P2_MetadataSinClavesNuevas(t *testing.T) {
	meta := `  m:
    context_window: 1000000
    max_output: 384000
    thinking: ["low", "high", "max"]
    thinking_default: "low"
`
	// C3 enmendado 11 Ago: top_provider/max_output_tokens son alias de
	// context_window/max_output — P6 — y se emiten cuando esos valores son
	// > 0, por eso NO se listan acá.
	h := buildWithCatalogYAML(t, []string{"m"}, catalogYAML(meta), "k1")
	item := findModel(t, modelsBody(t, h), "m")
	for _, k := range []string{"supported_parameters", "modality", "architecture"} {
		wantAbsent(t, item, k)
	}
	// y los campos 007-002 siguen presentes (la metadata existe).
	if item["context_length"] != float64(1000000) {
		t.Fatalf("context_length = %v, want 1000000 (metadata 007-002 intacta)", item["context_length"])
	}
}

// ---- C2: modelo SIN metadata → entrada mínima, sin campos nuevos ----

func TestE2E010001_C2_SinMetadataMinimo(t *testing.T) {
	const yaml = `server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_010001_TEST_KEY
    models: ["plain"]
`
	h := buildWithCatalogYAML(t, []string{"plain"}, yaml, "k1")
	item := findModel(t, modelsBody(t, h), "plain")
	if item["object"] != "model" {
		t.Fatalf("object = %v, want model", item["object"])
	}
	for _, k := range []string{"id", "object", "created", "owned_by"} {
		if _, ok := item[k]; !ok {
			t.Fatalf("campo mínimo %q ausente: %#v", k, item)
		}
	}
	if len(item) != 4 {
		t.Fatalf("entrada mínima tiene %d campos, want 4: %#v", len(item), item)
	}
}

// ---- C6/P8: envelope intacto + 401 sin Bearer ----

func TestE2E010001_C6_EnvelopeYAuth(t *testing.T) {
	h := buildWithCatalogYAML(t, []string{"m"}, catalogYAML(metaC1), "k1")
	out := modelsBody(t, h)
	if out["object"] != "list" {
		t.Fatalf("envelope object = %v, want list", out["object"])
	}
	if _, ok := out["data"].([]any); !ok {
		t.Fatalf("envelope data no es array: %#v", out["data"])
	}

	// 401 sin Bearer (P8/C6) — el gap que TestE2EAuthRequired no cubre
	// (solo prueba chat completions).
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("models sin auth: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("models sin Bearer: status = %d, want 401", resp.StatusCode)
	}
}
