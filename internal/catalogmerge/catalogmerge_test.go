// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-003-merge-provider-catalog — bloques B2..B13
// del test-audit.md (B1 vive en internal/config/sync_source_test.go).
//
// RED HÍBRIDO esperado (validación definitiva en GREEN; precedente aceptado
// del proyecto: 018-001 opencode_session_red_test.go, 019-001/002):
//
//  1. COMPILACIÓN: el paquete internal/catalogmerge no existe → los
//     símbolos Plan/ProviderPlan/Merge de este archivo no resuelven.
//  2. COMPILACIÓN: config.ProviderConfig.SyncSource/SyncMirror aún no
//     existen (P14/D2) → las referencias en TestSourceAssociation
//     (caso knob-gana) y TestMirror_KnobWins caen en ESTE paquete hasta
//     que GREEN agregue los 2 knobs.
//  3. COMPILACIÓN: upstream.OpenRouterModel.AliasTargetSlug aún no existe
//     (D3) → la referencia en TestMerge_OpenRouterAliasResolution cae en
//     ESTE paquete de tests; la suite 002 permanece intacta y verde
//     (ningún test de 002 referencia el campo, grep verificado en audit).
//
// Contrato congelado (spec D1/D8 — los tests NO lo redefinen):
//
//	package catalogmerge
//	type Plan struct {
//	    Providers   []ProviderPlan  // ordenado por ProviderID (D7)
//	    SourcesUsed map[string]bool // "modelsdev"|"zen"|"go"|"openrouter"
//	    Warnings    []string        // ordenado, dedup (D7)
//	}
//	type ProviderPlan struct {
//	    ProviderID string
//	    Source     string // zen|go|openrouter|modelsdev
//	    Models     []string
//	    Pricing    map[string]config.PricingConfig
//	    Metadata   map[string]config.ModelMetadata
//	    Warnings   []string
//	}
//	func Merge(catalog *modelsdev.Catalog, zen, goList *upstream.ModelList,
//	    or *upstream.OpenRouterCatalog, providers []config.ProviderConfig) (Plan, error)
//
// Firma a congelar: 5 inputs, nil = fuente ausente; error SOLO si TODAS las
// fuentes son nil (D8). Pureza (D9/P13): fixtures inline, sin red/disco.
//
// Decisiones de freezing documentadas (desviaciones conscientes del audit):
//
//   - B6 (P5 vs D3(d)): las postcondiciones lockeadas P2/I2 mandan — el ID
//     ausente en models.dev PERMANECE en Models (la lista de acceso define
//     Models, I2) y NO entra a Pricing/Metadata + warning (P5). Este test
//     NO asume expulsión del ID de Models; D3(d) "omitir del plan" se
//     interpreta como omisión de pricing/metadata. Punto a reconciliar en
//     review si GREEN implementa expulsión.
//
//   - B10 (P9 vs I7): P9 exige derivar Thinking de reasoning_options del
//     espejo models.dev, pero modelsdev.Model no expone reasoning_options
//     y ParseCatalog la descarta hoy (schema variable, P14 de 001). El
//     fixture usa el shape real capturado 2026-09-11 ({"type":"effort",
//     "values":[...]}) vía ParseCatalog y congela el comportamiento P9:
//     GREEN requerirá la misma clase de extensión aditiva zero-value que
//     D3 hizo en upstream (suite 001 permanece verde) o enmienda de I7 vía
//     HITL. Es el ÚNICO test que fuerza extensión en modelsdev: B8 detecta
//     el descarte vía cache_write (expuesto en modelsdev.Cost) y B11
//     deriva solo de tool_call/reasoning (expuestos en modelsdev.Model).
//
//   - B5, forma elegida: fixture OR crudo + ParseOpenRouterCatalog +
//     asserts sobre Models[i].AliasTargetSlug (hidratación D3) y luego
//     merge-level. Produce el RED más claro: el compile-error del campo
//     cae en este paquete de tests sin tocar la suite 002.
package catalogmerge

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/modelsdev"
	"github.com/ofapsaas/mofgw/internal/upstream"
)

// ---------------------------------------------------------------------------
// Fixtures inline self-contained (shapes reales capturados 2026-09-11).
// ---------------------------------------------------------------------------

// mdShared es el catálogo models.dev recortado: "opencode" (espejo default
// de zen) con cost/limit/modalities/tool_call/reasoning reales, y
// "opencode-go" (espejo default de go) con deepseek-flash.
const mdShared = `{
  "opencode": {
    "name": "OpenCode",
    "api": "https://opencode.ai/zen/v1",
    "models": {
      "glm-5.2": {
        "name": "GLM 5.2",
        "attachment": true,
        "reasoning": true,
        "tool_call": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 200000, "output": 128000, "input": 180000},
        "cost": {"input": 1.4, "output": 4.4, "cache_read": 0.26}
      },
      "minimax-m3": {
        "name": "MiniMax M3",
        "reasoning": true,
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 1000000, "output": 16000},
        "cost": {"input": 0.3, "output": 1.2, "cache_read": 0.03}
      },
      "glm-5.3-flash": {
        "name": "GLM 5.3 Flash",
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 204800, "output": 131072},
        "cost": {"input": 0.2, "output": 1.1, "cache_read": 0.02}
      },
      "deepseek-flash": {
        "name": "DeepSeek Flash",
        "limit": {"context": 128000, "output": 8192},
        "cost": {"input": 0.1, "output": 0.3, "cache_read": 0.01}
      }
    }
  },
  "opencode-go": {
    "name": "OpenCode Go",
    "models": {
      "deepseek-flash": {
        "name": "DeepSeek Flash",
        "limit": {"context": 128000, "output": 8192},
        "cost": {"input": 0.11, "output": 0.44, "cache_read": 0.014}
      }
    }
  }
}`

// zenListShared / goListShared: listas de acceso OpenAI-shape recortadas
// (orden NO alfabético y duplicado para ejercitar orden+dedup de P11).
const zenListShared = `{
  "object": "list",
  "data": [
    {"id": "minimax-m3", "object": "model", "owned_by": "opencode", "created": 1754000000},
    {"id": "glm-5.2", "object": "model", "owned_by": "opencode", "created": 1754000000}
  ]
}`

const goListShared = `{
  "object": "list",
  "data": [
    {"id": "deepseek-flash", "object": "model", "owned_by": "opencode", "created": 1754000000}
  ]
}`

// orShared: catálogo OpenRouter recortado (shape real: pricing strings
// omitidos — NUNCA se usa para pricing según spec; architecture anidada;
// top_provider; supported_parameters ya alfabético tal cual upstream).
const orShared = `{
  "data": [
    {
      "id": "z-ai/glm-5.3-flash",
      "canonical_slug": "z-ai/glm-5.3-flash",
      "name": "GLM 5.3 Flash",
      "context_length": 204800,
      "supported_parameters": ["seed", "stop", "temperature", "tools"],
      "architecture": {"modality": "text->text", "input_modalities": ["text"], "output_modalities": ["text"]},
      "top_provider": {"context_length": 204800, "max_completion_tokens": 131072}
    }
  ]
}`

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func mustParseCatalog(t *testing.T, raw string) *modelsdev.Catalog {
	t.Helper()
	cat, err := modelsdev.ParseCatalog([]byte(raw))
	if err != nil {
		t.Fatalf("modelsdev.ParseCatalog: %v", err)
	}
	return cat
}

func mustParseList(t *testing.T, raw string) *upstream.ModelList {
	t.Helper()
	list, err := upstream.ParseModelList([]byte(raw))
	if err != nil {
		t.Fatalf("upstream.ParseModelList: %v", err)
	}
	return &list
}

func mustParseOR(t *testing.T, raw string) *upstream.OpenRouterCatalog {
	t.Helper()
	cat, err := upstream.ParseOpenRouterCatalog([]byte(raw))
	if err != nil {
		t.Fatalf("upstream.ParseOpenRouterCatalog: %v", err)
	}
	return &cat
}

// planFor devuelve el ProviderPlan del provider pedido (los planes van
// ordenados por ProviderID, D7 — la búsqueda lineal no asume orden).
func planFor(t *testing.T, plan Plan, providerID string) ProviderPlan {
	t.Helper()
	for _, p := range plan.Providers {
		if p.ProviderID == providerID {
			return p
		}
	}
	t.Fatalf("plan no contiene provider %q (tiene %d providers)", providerID, len(plan.Providers))
	return ProviderPlan{}
}

// assertSourceUsed verifica SourcesUsed para una fuente (P12: refleja solo
// fuentes no-nil; fuente ausente del mapa = false).
func assertSourceUsed(t *testing.T, used map[string]bool, source string, want bool) {
	t.Helper()
	if got := used[source]; got != want {
		t.Errorf("SourcesUsed[%q] = %v, want %v", source, got, want)
	}
}

// assertWarningsClean verifica que Warnings está ordenado y sin duplicados
// (P11/D7). Tolerante a nil.
func assertWarningsClean(t *testing.T, warnings []string, ctx string) {
	t.Helper()
	if !sort.StringsAreSorted(warnings) {
		t.Errorf("%s: Warnings sin ordenar: %q", ctx, warnings)
	}
	for i := 1; i < len(warnings); i++ {
		if warnings[i] == warnings[i-1] {
			t.Errorf("%s: Warnings duplicados: %q", ctx, warnings)
			return
		}
	}
}

// assertHasWarning exige al menos un warning (fail-soft con evidencia).
func assertHasWarning(t *testing.T, warnings []string, ctx string) {
	t.Helper()
	if len(warnings) == 0 {
		t.Errorf("%s: se esperaba al menos un warning, hay 0", ctx)
	}
}

// ---------------------------------------------------------------------------
// B2 — P1: asociación provider→fuente (knob gana + auto determinística).
// ---------------------------------------------------------------------------

// TestSourceAssociation: tabla de los 7 casos de P1/C1 usando las CONSTS de
// upstream (B2 del audit: nunca URLs hardcodeadas; el test construye el
// base_url PEGANDO la const sin sufijo /models). La fuente se observa vía
// ProviderPlan.Source del merge (única salida observable del IR).
//
// RED: compila-falla por config.ProviderConfig.SyncSource (caso knob-gana)
// y por los símbolos de catalogmerge.
func TestSourceAssociation(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)
	goList := mustParseList(t, goListShared)
	or := mustParseOR(t, orShared)

	providers := []config.ProviderConfig{
		// P1(a): el knob gana SIN importar base_url (que apunta a la const go).
		{ID: "knob-zen-gana", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}, SyncSource: "zen"},
		{ID: "auto-zen", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
		{ID: "auto-go", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
		{ID: "auto-openrouter", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"z-ai/glm-5.3-flash"}},
		// Trim del "/" final: match exacto contra la const sin sufijo.
		{ID: "auto-trailing-slash", BaseURL: upstream.ZenURL + "/", APIKeyEnv: "K", Models: []string{"glm-5.2"}},
		// Sin match (qwen aliyuncs del config vivo) → modelsdev.
		{ID: "auto-sin-match", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKeyEnv: "K", Models: []string{"glm-5.2"}},
		// subprocess → modelsdev (sin base_url, D2).
		{ID: "subprocess-acc", Type: "subprocess", Backend: "claude", Models: []string{"glm-5.2"}},
	}

	plan, err := Merge(md, zenList, goList, or, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	want := map[string]string{
		"knob-zen-gana":       "zen", // P1(a): knob declara, nunca se infiere (I3)
		"auto-zen":            "zen", // P1(b): match exacto const ZenURL
		"auto-go":             "go",
		"auto-openrouter":     "openrouter",
		"auto-trailing-slash": "zen", // trim "/" final
		"auto-sin-match":      "modelsdev",
		"subprocess-acc":      "modelsdev", // type: subprocess → modelsdev
	}
	for id, wantSource := range want {
		p := planFor(t, plan, id)
		if p.Source != wantSource {
			t.Errorf("provider %q: Source = %q, want %q", id, p.Source, wantSource)
		}
	}
}

// ---------------------------------------------------------------------------
// B3 — P2: matching corto directo (upstream manda).
// ---------------------------------------------------------------------------

// TestMerge_ShortIDDirectMatch: listas zen/go vs keys de models.dev.
// Models = la lista de acceso COMPLETA (I2), macheada contra las keys del
// catálogo; los ausentes solo afectan pricing/metadata (P5). Source de zen
// default espejo "opencode" (D4) y go "opencode-go".
func TestMerge_ShortIDDirectMatch(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)
	goList := mustParseList(t, goListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2", "minimax-m3"}},
		{ID: "go-acc", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
	}

	plan, err := Merge(md, zenList, goList, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	zp := planFor(t, plan, "zen-acc")
	// P2/I2: la lista de acceso completa entra, ORDENADA y dedup (D7).
	wantZen := []string{"glm-5.2", "minimax-m3"}
	if !reflect.DeepEqual(zp.Models, wantZen) {
		t.Errorf("zen Models = %v, want %v", zp.Models, wantZen)
	}
	if zp.Source != "zen" {
		t.Errorf("zen Source = %q, want zen", zp.Source)
	}
	// Pricing del espejo default opencode (D4): passthrough fiel.
	pr := zp.Pricing["glm-5.2"]
	if pr.InputUSDPerM != 1.4 || pr.OutputUSDPerM != 4.4 || pr.CacheHitUSDPerM != 0.26 {
		t.Errorf("glm-5.2 Pricing = %+v, want {1.4 4.4 0.26}", pr)
	}

	gp := planFor(t, plan, "go-acc")
	if !reflect.DeepEqual(gp.Models, []string{"deepseek-flash"}) {
		t.Errorf("go Models = %v, want [deepseek-flash]", gp.Models)
	}
	if gp.Source != "go" {
		t.Errorf("go Source = %q, want go", gp.Source)
	}
	// Espejo default go→opencode-go (D4).
	prg := gp.Pricing["deepseek-flash"]
	if prg.InputUSDPerM != 0.11 || prg.OutputUSDPerM != 0.44 || prg.CacheHitUSDPerM != 0.014 {
		t.Errorf("go deepseek-flash Pricing = %+v, want espejo opencode-go {0.11 0.44 0.014}", prg)
	}
}

// ---------------------------------------------------------------------------
// B4 — P3: strip de vendor OpenRouter (el ID de acceso manda).
// ---------------------------------------------------------------------------

// TestMerge_OpenRouterVendorStrip: OR "z-ai/glm-5.3-flash" + models.dev key
// "glm-5.3-flash" → plan Models=["z-ai/glm-5.3-flash"] con Pricing/Metadata
// keyed por el ID de ACCESO (el strip es SOLO para buscar la metadata, I2).
func TestMerge_OpenRouterVendorStrip(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	or := mustParseOR(t, orShared)

	providers := []config.ProviderConfig{
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"z-ai/glm-5.3-flash"}},
	}

	plan, err := Merge(md, nil, nil, or, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "or-acc")
	if p.Source != "openrouter" {
		t.Errorf("Source = %q, want openrouter", p.Source)
	}
	if !reflect.DeepEqual(p.Models, []string{"z-ai/glm-5.3-flash"}) {
		t.Fatalf("Models = %v, want [z-ai/glm-5.3-flash] (I2: el ID de acceso manda)", p.Models)
	}
	pr, ok := p.Pricing["z-ai/glm-5.3-flash"]
	if !ok {
		t.Fatalf("Pricing no keyed por ID de acceso %q (keys: %v)", "z-ai/glm-5.3-flash", keysOf(p.Pricing))
	}
	if pr.InputUSDPerM != 0.2 || pr.OutputUSDPerM != 1.1 || pr.CacheHitUSDPerM != 0.02 {
		t.Errorf("Pricing = %+v, want espejo opencode {0.2 1.1 0.02}", pr)
	}
	mdMeta, ok := p.Metadata["z-ai/glm-5.3-flash"]
	if !ok {
		t.Fatalf("Metadata no keyed por ID de acceso %q", "z-ai/glm-5.3-flash")
	}
	if mdMeta.ContextWindow != 204800 || mdMeta.MaxOutput != 131072 {
		t.Errorf("Metadata = %+v, want ContextWindow 204800 MaxOutput 131072", mdMeta)
	}
}

// keysOf devuelve las keys de un mapa de pricing (helper de diagnóstico).
func keysOf(m map[string]config.PricingConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// B5 — P4: alias de 2 saltos + alias sin target.
// ---------------------------------------------------------------------------

// orAliasFixture: shape REAL capturado del catálogo vivo 2026-09-11 —
// "~deepseek/deepseek-v4-flash-latest" con alias_target.slug
// "deepseek/deepseek-v4-flash-0731" (caso vivo del spec D3), y un alias
// sin alias_target (schema variable tolerado).
const orAliasFixture = `{
  "data": [
    {
      "id": "~deepseek/deepseek-v4-flash-latest",
      "canonical_slug": "~deepseek/deepseek-v4-flash-latest",
      "name": "DeepSeek: DeepSeek V4 Flash (latest)",
      "context_length": 1048576,
      "supported_parameters": ["seed", "temperature", "tools"],
      "architecture": {"modality": "text->text", "input_modalities": ["text"], "output_modalities": ["text"]},
      "top_provider": {"context_length": 1048576, "max_completion_tokens": 393216},
      "alias_target": {"name": "DeepSeek: DeepSeek V4 Flash 0731", "slug": "deepseek/deepseek-v4-flash-0731"}
    },
    {
      "id": "~ghost/model-sin-target",
      "canonical_slug": "~ghost/model-sin-target",
      "name": "Ghost: sin alias_target"
    }
  ]
}`

// TestMerge_OpenRouterAliasResolution: P4 — "~deepseek/deepseek-v4-flash-latest"
// → alias_target.slug "deepseek/deepseek-v4-flash-0731" → strip vendor →
// key corta en models.dev; el ID en el plan sigue siendo el alias "~" (I2).
//
// RED híbrido B5 (forma elegida, documentada en el header): fixture OR crudo
// + ParseOpenRouterCatalog + asserts sobre Models[i].AliasTargetSlug →
// compile-error en ESTE paquete hasta que GREEN hidrate el campo (D3);
// la suite 002 no referencia el campo y queda intacta.
func TestMerge_OpenRouterAliasResolution(t *testing.T) {
	// Nivel 1 (parse, D3): la extensión aditiva hidrata alias_target.slug.
	orCat := mustParseOR(t, orAliasFixture)
	var al *upstream.OpenRouterModel
	for i := range orCat.Models {
		if orCat.Models[i].ID == "~deepseek/deepseek-v4-flash-latest" {
			al = &orCat.Models[i]
			break
		}
	}
	if al == nil {
		t.Fatal("alias ~deepseek/deepseek-v4-flash-latest ausente del catálogo parseado")
	}
	if al.AliasTargetSlug != "deepseek/deepseek-v4-flash-0731" {
		t.Fatalf("AliasTargetSlug = %q, want %q (D3: campo no hidratado — RED híbrido esperado)", al.AliasTargetSlug, "deepseek/deepseek-v4-flash-0731")
	}

	// Nivel 2 (merge): catálogo models.dev SIN espejo natural para
	// deepseek-v4-flash-0731 (14 providers con cost en el catálogo real,
	// ninguno "opencode") → fallback alfabético D4.
	mdRaw := `{
  "aihubmix": {"name": "AiHubMix", "models": {"deepseek-v4-flash-0731": {"limit": {"context": 131072, "output": 8192}}}},
  "bothub": {"name": "Bothub", "models": {"deepseek-v4-flash-0731": {"cost": {"input": 0.27, "output": 1.1, "cache_read": 0.027}, "limit": {"context": 131072, "output": 8192}}}}
}`
	md := mustParseCatalog(t, mdRaw)

	providers := []config.ProviderConfig{
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"~deepseek/deepseek-v4-flash-latest"}},
	}

	plan, err := Merge(md, nil, nil, orCat, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "or-acc")
	if !reflect.DeepEqual(p.Models, []string{"~deepseek/deepseek-v4-flash-latest"}) {
		t.Fatalf("Models = %v, want el alias TAL CUAL (I2/P4)", p.Models)
	}
	pr, ok := p.Pricing["~deepseek/deepseek-v4-flash-latest"]
	if !ok {
		t.Fatalf("Pricing keyed por el ID alias ausente (keys: %v)", keysOf(p.Pricing))
	}
	// Fallback alfabético: "aihubmix" no tiene cost; "bothub" (2º) sí →
	// Pricing de bothub + warning (B7 risk (c): discrimina el fallback).
	if pr.InputUSDPerM != 0.27 || pr.OutputUSDPerM != 1.1 || pr.CacheHitUSDPerM != 0.027 {
		t.Errorf("Pricing = %+v, want fallback bothub {0.27 1.1 0.027}", pr)
	}
	assertHasWarning(t, p.Warnings, "alias fallback alfabético")
	assertWarningsClean(t, p.Warnings, "alias")
}

// TestMerge_OpenRouterAliasWithoutTarget: alias sin alias_target → omitido
// del plan + warning (P4/D3(c)); sin error (I4 fail-soft de datos).
func TestMerge_OpenRouterAliasWithoutTarget(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	orCat := mustParseOR(t, orAliasFixture)

	providers := []config.ProviderConfig{
		// El subset declarado pide el alias roto y un modelo sano.
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"~ghost/model-sin-target", "z-ai/glm-5.3-flash"}},
	}

	plan, err := Merge(md, nil, nil, orCat, providers)
	if err != nil {
		t.Fatalf("Merge (P4: fail-soft, nunca aborta): %v", err)
	}

	p := planFor(t, plan, "or-acc")
	for _, m := range p.Models {
		if m == "~ghost/model-sin-target" {
			t.Errorf("alias sin alias_target entró al plan: %v (P4: omitir)", p.Models)
		}
	}
	if !reflect.DeepEqual(p.Models, []string{"z-ai/glm-5.3-flash"}) {
		t.Errorf("Models = %v, want solo el modelo sano", p.Models)
	}
	assertHasWarning(t, p.Warnings, "alias sin target")
	assertWarningsClean(t, p.Warnings, "alias sin target")
}

// ---------------------------------------------------------------------------
// B6 — P5: ID ausente en models.dev → sin pricing/metadata + warning.
// ---------------------------------------------------------------------------

// TestMerge_MissingIDSoftSkip: "id-fantasma" no existe en ninguna key de
// models.dev → NO entra a Pricing/Metadata + warning; el merge NO falla
// (I4) y el ID permanece en Models (postcondición P2/I2 — ver decisión B6
// en el header; D3(d) "omitir" se interpreta como omisión de datos).
func TestMerge_MissingIDSoftSkip(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		// Lista de acceso con un ID que models.dev no conoce.
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K",
			Models: []string{"glm-5.2", "minimax-m3", "id-fantasma"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge (P5: fail-soft, nunca aborta): %v", err)
	}

	p := planFor(t, plan, "zen-acc")
	if _, ok := p.Pricing["id-fantasma"]; ok {
		t.Error("Pricing[id-fantasma] existe (P5: NO debe entrar)")
	}
	if _, ok := p.Metadata["id-fantasma"]; ok {
		t.Error("Metadata[id-fantasma] existe (P5: NO debe entrar)")
	}
	// P2/I2: Models es la lista de acceso completa (el ID NO se expulsa).
	found := false
	for _, m := range p.Models {
		if m == "id-fantasma" {
			found = true
		}
	}
	if !found {
		t.Errorf("id-fantasma expulsado de Models %v (P2/I2: la lista de acceso manda)", p.Models)
	}
	assertHasWarning(t, p.Warnings, "id ausente en models.dev")
	assertWarningsClean(t, p.Warnings, "id ausente")
}

// ---------------------------------------------------------------------------
// B7 — P6: espejo (knob gana; defaults; fallback alfabético; sin cost).
// ---------------------------------------------------------------------------

// mdMirrorFallback: 3 providers con "deepseek-flash"; cost SOLO en el 2º
// alfabético ("beta-corp") → discrimina que el fallback NO tomó ni el
// primero ("alpha-corp", sin cost) ni otro (risk (c) del audit).
const mdMirrorFallback = `{
  "alpha-corp": {"name": "Alpha", "models": {"deepseek-flash": {"limit": {"context": 128000, "output": 8192}}}},
  "beta-corp": {"name": "Beta", "models": {"deepseek-flash": {"cost": {"input": 0.5, "output": 2, "cache_read": 0.05}, "limit": {"context": 128000, "output": 8192}}}},
  "gamma-corp": {"name": "Gamma", "models": {"deepseek-flash": {"cost": {"input": 9, "output": 9, "cache_read": 0.09}, "limit": {"context": 128000, "output": 8192}}}}
}`

// TestMirror_KnobWins: sync_mirror seteado y existente gana sobre el
// default por fuente (zen default opencode; aquí espejo opencode-go).
// RED: compila-falla por config.ProviderConfig.SyncMirror.
func TestMirror_KnobWins(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		// BaseURL zen → default espejo "opencode"; el knob manda a "opencode-go".
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}, SyncMirror: "opencode-go"},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "zen-acc")
	pr := p.Pricing["deepseek-flash"]
	// opencode-go cobra 0.11/0.44/0.014; opencode 0.1/0.3/0.01 → el knob gana.
	if pr.InputUSDPerM != 0.11 || pr.OutputUSDPerM != 0.44 || pr.CacheHitUSDPerM != 0.014 {
		t.Errorf("Pricing = %+v, want espejo opencode-go {0.11 0.44 0.014} (sync_mirror debe ganar)", pr)
	}
}

// TestMirror_DefaultsBySource: sin sync_mirror → zen→opencode, go→
// opencode-go (D4; tabla, misma entrada por fuente).
func TestMirror_DefaultsBySource(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)
	goList := mustParseList(t, goListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
		{ID: "go-acc", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
	}

	plan, err := Merge(md, zenList, goList, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	cases := []struct {
		id          string
		in, out, cr float64
	}{
		{"zen-acc", 0.1, 0.3, 0.01},   // espejo default opencode
		{"go-acc", 0.11, 0.44, 0.014}, // espejo default opencode-go
	}
	for _, c := range cases {
		p := planFor(t, plan, c.id)
		pr := p.Pricing["deepseek-flash"]
		if pr.InputUSDPerM != c.in || pr.OutputUSDPerM != c.out || pr.CacheHitUSDPerM != c.cr {
			t.Errorf("%s: Pricing = %+v, want espejo default {%v %v %v}", c.id, pr, c.in, c.out, c.cr)
		}
	}
}

// TestMirror_AlphaFallback: espejo ausente (openrouter) + ID en 3 providers
// con cost solo en el 2º alfabético → primer provider alfabético CON cost
// ("beta-corp") + warning (P6/D4; NO "alpha-corp", NO "gamma-corp").
func TestMirror_AlphaFallback(t *testing.T) {
	md := mustParseCatalog(t, mdMirrorFallback)
	orCat := mustParseOR(t, orShared)
	// orShared trae "z-ai/glm-5.3-flash"; renombramos el ID de acceso para
	// apuntar a deepseek-flash vía strip de vendor.
	orDS := mustParseOR(t, `{
  "data": [
    {"id": "deepseek/deepseek-flash", "canonical_slug": "deepseek/deepseek-flash", "name": "DeepSeek Flash",
     "supported_parameters": ["temperature"],
     "architecture": {"modality": "text->text", "input_modalities": ["text"], "output_modalities": ["text"]}}
  ]
}`)

	providers := []config.ProviderConfig{
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"deepseek/deepseek-flash"}},
	}

	plan, err := Merge(md, nil, nil, orDS, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "or-acc")
	pr, ok := p.Pricing["deepseek/deepseek-flash"]
	if !ok {
		t.Fatalf("Pricing ausente (fallback alfabético debe dar beta-corp)")
	}
	if pr.InputUSDPerM != 0.5 || pr.OutputUSDPerM != 2 || pr.CacheHitUSDPerM != 0.05 {
		t.Errorf("Pricing = %+v, want beta-corp {0.5 2 0.05} (primer alfabético CON cost)", pr)
	}
	if pr.InputUSDPerM == 9 {
		t.Error("fallback tomó gamma-corp (NO alfabético-primero)")
	}
	assertHasWarning(t, p.Warnings, "fallback alfabético")
	assertWarningsClean(t, p.Warnings, "fallback alfabético")

	// orCat/orShared solo participa como shape real de referencia del
	// catálogo OR (no usado en este caso): evita unused sin asumir contrato.
	_ = orCat
}

// TestMirror_NoCostWarning: NINGÚN provider de models.dev con el ID tiene
// cost → sin Pricing + warning, sin error (P6 fail-soft).
func TestMirror_NoCostWarning(t *testing.T) {
	mdRaw := `{
  "alpha-corp": {"name": "Alpha", "models": {"deepseek-flash": {"limit": {"context": 128000, "output": 8192}}}},
  "beta-corp": {"name": "Beta", "models": {"deepseek-flash": {"reasoning": true, "tool_call": true}}}
}`
	md := mustParseCatalog(t, mdRaw)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge (P6: fail-soft): %v", err)
	}

	p := planFor(t, plan, "zen-acc")
	if len(p.Pricing) != 0 {
		t.Errorf("Pricing = %v, want vacío (ningún provider con cost)", keysOf(p.Pricing))
	}
	assertHasWarning(t, p.Warnings, "sin cost")
	assertWarningsClean(t, p.Warnings, "sin cost")
}

// ---------------------------------------------------------------------------
// B8 — P7: passthrough fiel + descarte de tiers/cache_write/context_over_200k.
// ---------------------------------------------------------------------------

// TestMerge_PricingPassthrough: cost 1:1 sin conversión (unidades ya USD/M,
// verificado contra el config vivo — spec D5): input/output/cache_read.
func TestMerge_PricingPassthrough(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "zen-acc")
	pr := p.Pricing["glm-5.2"]
	want := config.PricingConfig{InputUSDPerM: 1.4, OutputUSDPerM: 4.4, CacheHitUSDPerM: 0.26}
	if pr != want {
		t.Errorf("Pricing = %+v, want passthrough 1:1 %+v", pr, want)
	}
}

// TestMerge_PricingTiersDiscardWarning: el espejo (opencode-go/qwen-shape)
// trae cost.cache_write ≠ base + tiers + context_over_200k → warning de
// descarte en ProviderPlan.Warnings y el valor BASE queda (PricingConfig es
// plano; P7: NUNCA inventar tiering).
//
// Detectabilidad (documentada en el header): modelsdev.Cost expone
// cache_write; tiers/context_over_200k NO están tipados en modelsdev pero
// SÍ en el YAML crudo del espejo → GREEN debe detectarlos del espejo sin
// mutar modelsdev (o con la extensión aditiva de la decisión B10).
func TestMerge_PricingTiersDiscardWarning(t *testing.T) {
	mdRaw := `{
  "opencode": {
    "name": "OpenCode",
    "models": {
      "qwen3.7-plus": {
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 262144, "output": 16384},
        "cost": {"input": 0.4, "output": 1.6, "cache_read": 0.04, "cache_write": 0.5,
                 "tiers": [{"input": 1.2, "output": 4.8, "cache_read": 0.12, "cache_write": 1.5, "tier": {"type": "context", "size": 256000}}],
                 "context_over_200k": {"input": 1.2, "output": 4.8, "cache_read": 0.12, "cache_write": 1.5}}
      }
    }
  }
}`
	md := mustParseCatalog(t, mdRaw)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"qwen3.7-plus"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "zen-acc")
	// El valor BASE queda (P7).
	pr := p.Pricing["qwen3.7-plus"]
	want := config.PricingConfig{InputUSDPerM: 0.4, OutputUSDPerM: 1.6, CacheHitUSDPerM: 0.04}
	if pr != want {
		t.Errorf("Pricing = %+v, want base %+v (tiers descartados, no inventados)", pr, want)
	}
	assertHasWarning(t, p.Warnings, "descarte tiers/cache_write/context_over_200k")
	assertWarningsClean(t, p.Warnings, "descartes")
}

// ---------------------------------------------------------------------------
// B9 — P8: metadata derivada (ContextWindow/MaxOutput/Modality).
// ---------------------------------------------------------------------------

// TestMerge_MetadataDerivation: ContextWindow=limit.context,
// MaxOutput=limit.output; Modality=Join(input,"+")+"->"+Join(output,"+")
// (formato exacto que parsea splitModality, proxy.go).
func TestMerge_MetadataDerivation(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	meta := planFor(t, plan, "zen-acc").Metadata["glm-5.2"]
	if meta.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000 (limit.context)", meta.ContextWindow)
	}
	if meta.MaxOutput != 128000 {
		t.Errorf("MaxOutput = %d, want 128000 (limit.output)", meta.MaxOutput)
	}
	if meta.Modality != "text+image->text" {
		t.Errorf("Modality = %q, want %q (Join(+)+->+Join(+))", meta.Modality, "text+image->text")
	}
	if meta.ThinkingDefault != "" {
		t.Errorf("ThinkingDefault = %q, want vacío (P9: JAMÁS aparece en el IR)", meta.ThinkingDefault)
	}
}

// TestMerge_ModalityFallback: modalities ausente en el espejo pero
// architecture.modality presente en OR → fallback literal "text->text";
// caso todo ausente → campo omitido (""), sin error (P8).
func TestMerge_ModalityFallback(t *testing.T) {
	mdRaw := `{
  "solo-or": {"name": "SoloOR", "models": {"or-model": {"cost": {"input": 1, "output": 2}}}},
  "nada": {"name": "Nada", "models": {"bare-model": {"cost": {"input": 1, "output": 2}}}}
}`
	md := mustParseCatalog(t, mdRaw)
	orCat := mustParseOR(t, `{
  "data": [
    {"id": "vendor/or-model", "canonical_slug": "vendor/or-model", "name": "OR Model",
     "architecture": {"modality": "text->text"}}
  ]
}`)

	providers := []config.ProviderConfig{
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"vendor/or-model", "bare-model"}},
	}

	plan, err := Merge(md, nil, nil, orCat, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "or-acc")
	if got := p.Metadata["vendor/or-model"].Modality; got != "text->text" {
		t.Errorf("Modality fallback = %q, want %q (architecture.modality de OR, formato literal)", got, "text->text")
	}
	if got := p.Metadata["bare-model"].Modality; got != "" {
		t.Errorf("Modality sin ninguna fuente = %q, want vacío (campo omitido, P8)", got)
	}
}

// ---------------------------------------------------------------------------
// B10 — P9: Thinking solo de effort.values.
// ---------------------------------------------------------------------------

// TestMerge_ThinkingFromEffortValues: reasoning_options con
// {"type":"effort","values":[...]} → Thinking = values tal cual;
// {"type":"toggle"}/{"type":"budget_tokens"} sin values → sin levels;
// ThinkingDefault SIEMPRE vacío (P9/I5).
//
// RED híbrido (decisión B10 en el header): modelsdev no expone
// reasoning_options hoy; el fixture usa el shape REAL de
// claude-sonnet-4-6/glm-4.7 del catálogo vivo (capturado 2026-09-11).
func TestMerge_ThinkingFromEffortValues(t *testing.T) {
	mdRaw := `{
  "opencode": {
    "name": "OpenCode",
    "models": {
      "claude-sonnet-4-6": {
        "reasoning": true, "tool_call": true,
        "limit": {"context": 200000, "output": 64000},
        "cost": {"input": 3, "output": 15, "cache_read": 0.3},
        "reasoning_options": [
          {"type": "effort", "values": ["low", "medium", "high", "max"]},
          {"type": "budget_tokens", "min": 1024}
        ]
      },
      "glm-4.7": {
        "reasoning": true, "tool_call": true,
        "limit": {"context": 204800, "output": 131072},
        "cost": {"input": 0.6, "output": 2.2, "cache_read": 0.11},
        "reasoning_options": [{"type": "toggle"}]
      },
      "qwen3.7-max": {
        "reasoning": true, "tool_call": true,
        "limit": {"context": 262144, "output": 65536},
        "cost": {"input": 0.7, "output": 2.8, "cache_read": 0.07},
        "reasoning_options": [{"type": "toggle"}, {"type": "budget_tokens", "max": 262144}]
      }
    }
  }
}`
	md := mustParseCatalog(t, mdRaw)
	zenList := mustParseList(t, zenListShared)

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K",
			Models: []string{"claude-sonnet-4-6", "glm-4.7", "qwen3.7-max"}},
	}

	plan, err := Merge(md, zenList, nil, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	p := planFor(t, plan, "zen-acc")

	wantEffort := []string{"low", "medium", "high", "max"}
	if got := p.Metadata["claude-sonnet-4-6"].Thinking; !reflect.DeepEqual(got, wantEffort) {
		t.Errorf("Thinking(effort) = %v, want %v tal cual (P9)", got, wantEffort)
	}
	if got := p.Metadata["glm-4.7"].Thinking; len(got) != 0 {
		t.Errorf("Thinking(toggle) = %v, want vacío (toggle sin values NO deriva levels, P9)", got)
	}
	if got := p.Metadata["qwen3.7-max"].Thinking; len(got) != 0 {
		t.Errorf("Thinking(toggle+budget) = %v, want vacío (P9)", got)
	}
	for _, id := range []string{"claude-sonnet-4-6", "glm-4.7", "qwen3.7-max"} {
		if got := p.Metadata[id].ThinkingDefault; got != "" {
			t.Errorf("%s: ThinkingDefault = %q, want SIEMPRE vacío (P9/I5)", id, got)
		}
	}
}

// ---------------------------------------------------------------------------
// B11 — P10: prioridad de SupportedParameters (OR > derivación > omitir).
// ---------------------------------------------------------------------------

// TestMerge_SupportedParametersPriority: 3 ramas de P10 — OR tal cual;
// derivación de capabilities de models.dev (tool_call→tools,
// structured_output→structured_outputs, temperature→temperature,
// reasoning→reasoning; solo presentes, ordenados); nada → omitir (nil).
//
// Detectabilidad: structured_output NO está expuesto por modelsdev.Model
// hoy (schema variable) → la rama derivada solo puede confirmar
// temperature/reasoning/tool_call con los campos expuestos; el fixture
// ejercita la rama completa y GREEN decidirá extensión aditiva (misma
// clase que la decisión B10) o enmienda I7 vía HITL.
func TestMerge_SupportedParametersPriority(t *testing.T) {
	mdRaw := `{
  "opencode": {
    "name": "OpenCode",
    "models": {
      "cap-full": {"reasoning": true, "tool_call": true, "temperature": true, "structured_output": true,
                   "cost": {"input": 1, "output": 2}},
      "cap-partial": {"tool_call": true, "reasoning": true,
                      "cost": {"input": 1, "output": 2}},
      "cap-nada": {"cost": {"input": 1, "output": 2}}
    }
  }
}`
	md := mustParseCatalog(t, mdRaw)
	zenList := mustParseList(t, zenListShared)
	orCat := mustParseOR(t, orShared) // trae supported_parameters ["seed","stop","temperature","tools"]

	providers := []config.ProviderConfig{
		{ID: "zen-acc", BaseURL: upstream.ZenURL, APIKeyEnv: "K",
			Models: []string{"cap-full", "cap-partial", "cap-nada"}},
		{ID: "or-acc", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K",
			Models: []string{"z-ai/glm-5.3-flash"}},
	}

	plan, err := Merge(md, zenList, nil, orCat, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Rama 1: OR presente → TAL CUAL (aunque difiera del vocabulario derivable).
	if got := planFor(t, plan, "or-acc").Metadata["z-ai/glm-5.3-flash"].SupportedParameters; !reflect.DeepEqual(got, []string{"seed", "stop", "temperature", "tools"}) {
		t.Errorf("OR SupportedParameters = %v, want tal cual upstream", got)
	}
	// Rama 2: derivación de models.dev, ordenada, solo presentes.
	if got := planFor(t, plan, "zen-acc").Metadata["cap-full"].SupportedParameters; !reflect.DeepEqual(got, []string{"structured_outputs", "temperature", "reasoning", "tools"}) {
		t.Errorf("deriva cap-full = %v, want vocabulario derivado ordenado", got)
	}
	if got := planFor(t, plan, "zen-acc").Metadata["cap-partial"].SupportedParameters; !reflect.DeepEqual(got, []string{"reasoning", "tools"}) {
		t.Errorf("deriva cap-partial = %v, want [reasoning tools] (solo presentes, ordenados)", got)
	}
	// Rama 3: nada → omitir (nil, P2 de 010-001).
	if got := planFor(t, plan, "zen-acc").Metadata["cap-nada"].SupportedParameters; got != nil {
		t.Errorf("cap-nada SupportedParameters = %v, want nil (omitir)", got)
	}
}

// ---------------------------------------------------------------------------
// B12 — P11: determinismo byte a byte.
// ---------------------------------------------------------------------------

// TestMerge_Deterministic: dos corridas con igual entrada → Plan
// deep-equal idéntico + asserts de orden explícitos (Providers por
// ProviderID, Models alfabético con dedup, Warnings ordenado y dedup).
func TestMerge_Deterministic(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared) // orden NO alfabético
	goList := mustParseList(t, goListShared)
	orCat := mustParseOR(t, orShared)

	providers := []config.ProviderConfig{
		{ID: "b-zen", BaseURL: upstream.ZenURL, APIKeyEnv: "K",
			Models: []string{"minimax-m3", "glm-5.2", "minimax-m3"}}, // desorden + dup
		{ID: "a-go", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
		{ID: "c-or", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"z-ai/glm-5.3-flash"}},
	}

	plan1, err := Merge(md, zenList, goList, orCat, providers)
	if err != nil {
		t.Fatalf("Merge 1: %v", err)
	}
	plan2, err := Merge(md, zenList, goList, orCat, providers)
	if err != nil {
		t.Fatalf("Merge 2: %v", err)
	}

	if !reflect.DeepEqual(plan1, plan2) {
		t.Fatal("dos corridas con igual entrada producen Plan distinto (P11)")
	}

	// Providers ordenados por ProviderID.
	gotIDs := make([]string, 0, len(plan1.Providers))
	for _, p := range plan1.Providers {
		gotIDs = append(gotIDs, p.ProviderID)
	}
	if !sort.StringsAreSorted(gotIDs) {
		t.Errorf("Providers sin ordenar por ProviderID: %v", gotIDs)
	}
	// Models alfabético con dedup (la entrada traía "minimax-m3" 2×).
	zp := planFor(t, plan1, "b-zen")
	if !reflect.DeepEqual(zp.Models, []string{"glm-5.2", "minimax-m3"}) {
		t.Errorf("Models = %v, want ordenado+dedup [glm-5.2 minimax-m3]", zp.Models)
	}
	// Warnings ordenado y dedup en todos los planes.
	assertWarningsClean(t, plan1.Warnings, "Plan")
	for _, p := range plan1.Providers {
		assertWarningsClean(t, p.Warnings, "ProviderPlan "+p.ProviderID)
	}
}

// ---------------------------------------------------------------------------
// B13 — P12: fail-soft por fuente; todas nil → error; SourcesUsed.
// ---------------------------------------------------------------------------

// TestMerge_FailSoftPerSource: cada fuente nil por turno (4 subtests) —
// el provider afectado queda degradado con warning, el resto intacto.
func TestMerge_FailSoftPerSource(t *testing.T) {
	providers := []config.ProviderConfig{
		{ID: "p-zen", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
		{ID: "p-go", BaseURL: upstream.GoURL, APIKeyEnv: "K", Models: []string{"deepseek-flash"}},
		{ID: "p-or", BaseURL: upstream.OpenRouterURL, APIKeyEnv: "K", Models: []string{"z-ai/glm-5.3-flash"}},
		{ID: "p-md", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKeyEnv: "K", Models: []string{"glm-5.2"}},
	}

	t.Run("zen_nil", func(t *testing.T) {
		plan, err := Merge(mustParseCatalog(t, mdShared), nil, mustParseList(t, goListShared), nil, providers)
		if err != nil {
			t.Fatalf("Merge (P12: fail-soft, sin error): %v", err)
		}
		zp := planFor(t, plan, "p-zen")
		if len(zp.Models) != 0 {
			t.Errorf("zen sin fuente: Models = %v, want nil/vacío (P12a)", zp.Models)
		}
		assertHasWarning(t, zp.Warnings, "zen nil")
		// El resto continúa intacto.
		if got := planFor(t, plan, "p-go"); !reflect.DeepEqual(got.Models, []string{"deepseek-flash"}) {
			t.Errorf("p-go afectado por zen nil: Models = %v", got.Models)
		}
	})

	t.Run("go_nil", func(t *testing.T) {
		plan, err := Merge(mustParseCatalog(t, mdShared), mustParseList(t, zenListShared), nil, nil, providers)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		gp := planFor(t, plan, "p-go")
		if len(gp.Models) != 0 {
			t.Errorf("go sin fuente: Models = %v, want nil/vacío (P12a)", gp.Models)
		}
		assertHasWarning(t, gp.Warnings, "go nil")
		if got := planFor(t, plan, "p-zen"); !reflect.DeepEqual(got.Models, []string{"glm-5.2"}) {
			t.Errorf("p-zen afectado por go nil: Models = %v", got.Models)
		}
	})

	t.Run("openrouter_nil", func(t *testing.T) {
		plan, err := Merge(mustParseCatalog(t, mdShared), mustParseList(t, zenListShared), mustParseList(t, goListShared), nil, providers)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		op := planFor(t, plan, "p-or")
		if got := op.Metadata["z-ai/glm-5.3-flash"].SupportedParameters; got != nil {
			t.Errorf("OR nil: SupportedParameters = %v, want nil (P12b: pasos 2/3 de D6)", got)
		}
		// Pricing del espejo (models.dev manda directo para openrouter) intacto.
		if _, ok := op.Pricing["z-ai/glm-5.3-flash"]; !ok {
			t.Error("OR nil: Pricing ausente (debe resolverse por espejo/fallback P6)")
		}
	})

	t.Run("modelsdev_nil", func(t *testing.T) {
		plan, err := Merge(nil, mustParseList(t, zenListShared), mustParseList(t, goListShared), mustParseOR(t, orShared), providers)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		zp := planFor(t, plan, "p-zen")
		if !reflect.DeepEqual(zp.Models, []string{"glm-5.2"}) {
			t.Errorf("modelsdev nil: Models = %v, want lista de acceso intacta (P12c)", zp.Models)
		}
		if len(zp.Pricing) != 0 || len(zp.Metadata) != 0 {
			t.Errorf("modelsdev nil: Pricing/Metadata = %v/%v, want vacíos (P12c)", keysOf(zp.Pricing), len(zp.Metadata))
		}
		assertHasWarning(t, zp.Warnings, "modelsdev nil")
		assertHasWarning(t, plan.Warnings, "Plan: modelsdev nil")
	})
}

// TestMerge_NoSourcesError: TODAS las fuentes nil → error descriptivo
// (P12d/D8: nada que mergear — único caso de error de Merge).
func TestMerge_NoSourcesError(t *testing.T) {
	providers := []config.ProviderConfig{
		{ID: "p-zen", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
	}

	plan, err := Merge(nil, nil, nil, nil, providers)
	if err == nil {
		t.Fatal("todas las fuentes nil: err == nil, want error descriptivo (P12d)")
	}
	if plan.Providers != nil || plan.SourcesUsed != nil {
		t.Errorf("con error, Plan = %+v, want zero-value", plan)
	}
}

// TestMerge_SourcesUsed: SourcesUsed refleja SOLO las fuentes no-nil
// (P12), con las 4 keys canónicas.
func TestMerge_SourcesUsed(t *testing.T) {
	md := mustParseCatalog(t, mdShared)
	zenList := mustParseList(t, zenListShared)
	goList := mustParseList(t, goListShared)
	orCat := mustParseOR(t, orShared)

	providers := []config.ProviderConfig{
		{ID: "p-zen", BaseURL: upstream.ZenURL, APIKeyEnv: "K", Models: []string{"glm-5.2"}},
	}

	// Las 4 fuentes presentes → las 4 en true.
	plan, err := Merge(md, zenList, goList, orCat, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, src := range []string{"modelsdev", "zen", "go", "openrouter"} {
		assertSourceUsed(t, plan.SourcesUsed, src, true)
	}

	// Sin OR (nil) → openrouter NO marcado (solo no-nil, P12).
	plan, err = Merge(md, zenList, goList, nil, providers)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	assertSourceUsed(t, plan.SourcesUsed, "modelsdev", true)
	assertSourceUsed(t, plan.SourcesUsed, "zen", true)
	assertSourceUsed(t, plan.SourcesUsed, "go", true)
	assertSourceUsed(t, plan.SourcesUsed, "openrouter", false)
}
