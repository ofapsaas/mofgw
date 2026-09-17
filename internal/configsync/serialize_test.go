// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-004-atomic-write-validate — Serialize (etapa
// estructural del sync). Cubren C1-C8, C13 (postcondiciones P1-P7, P11, P16).
//
// Contrato a materializar por GREEN (definido por el test-writer, precedente
// buildTelemetryFromConfig/newHTTPServer — el implementer DEBE respetarlo):
//
//	func Serialize(raw []byte, plan catalogmerge.Plan) ([]byte, Report, error)
//	type Report struct {
//		Applied  bool     // P8/P10 (lo llena Apply; Serialize deja false)
//		Skipped  bool     // P10 (lo llena Apply)
//		Digest   string   // sha256 hex del candidato (siempre)
//		Warnings []string // passthrough VERBATIM de plan.Warnings (P11)
//	}
//
// Serialize es PURO (I1): raw + Plan entran, candidato + reporte salen.
// RED por compilación (precedente 001/002/003): Serialize/Report no existen.
package configsync

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/catalogmerge"
	"github.com/ofapsaas/mofgw/internal/config"

	"gopkg.in/yaml.v3"
)

// goldenUpdate materializa el fixture golden (D8: self-consistente). SIN
// materializar en RED — no hay serializer todavía; el flag existe para el
// protocolo de GREEN-1 con aprobación HITL (test-audit §5).
var goldenUpdate = flag.Bool("update", false, "materializar el golden config-roundtrip.yaml vía round-trip (D8, con aprobación HITL)")

// ---- fixtures y helpers (puros, side del TEST) ----

// fixtureRaw lee el seed sintético (D8). go test corre con cwd = paquete.
func fixtureRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "config-roundtrip.yaml"))
	if err != nil {
		t.Fatalf("leyendo fixture golden: %v", err)
	}
	return raw
}

// validRaw004: fixture inline VÁLIDO sin clients_file (test-audit §4-C) para
// los tests que parsean el candidato con config.ParseForValidation (P7/P13).
const validRaw004 = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: zen-acc
    base_url: "https://opencode.ai/zen/v1"
    api_key_env: "MOFGW_ZEN_KEY"
    models: ["glm-5.2", "minimax-m3"]
    max_tokens: 8192
pricing:
  glm-5.2:
    input_usd_per_m: 1.4
    output_usd_per_m: 4.4
model_metadata:
  glm-5.2:
    context_window: 200000
`

// declaredProvider: extractor mínimo del YAML crudo (lado del test; NUNCA
// usa config.Parse — el fixture tiene clients_file inerte y el round-trip
// no debe depender de parseo de Config).
type declaredProvider struct {
	ID     string   `yaml:"id"`
	Models []string `yaml:"models"`
}

type declaredFixture struct {
	Providers []declaredProvider `yaml:"providers"`
}

func declaredOf(t *testing.T, raw []byte) []declaredProvider {
	t.Helper()
	var f declaredFixture
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal de providers declarados: %v", err)
	}
	return f.Providers
}

// planNoOp construye el plan no-op de P6: Models == declarados (VERBATIM, sin
// reordenar — el plan no-op no debe tocar ni siquiera el orden), sin cambios
// en pricing/metadata.
func planNoOp(t *testing.T, raw []byte) catalogmerge.Plan {
	t.Helper()
	decl := declaredOf(t, raw)
	providers := make([]catalogmerge.ProviderPlan, 0, len(decl))
	for _, d := range decl {
		models := append([]string(nil), d.Models...)
		providers = append(providers, catalogmerge.ProviderPlan{ProviderID: d.ID, Models: models})
	}
	return catalogmerge.Plan{Providers: providers}
}

// planSortedChanged: igual que planNoOp pero con Models sorted+dedup y
// extendidos (un miembro nuevo) para go-cuenta-1 — fuerza un cambio REAL en
// el nodo models (discriminante: candidato != raw).
func planWithGoModelChange(t *testing.T, raw []byte) catalogmerge.Plan {
	plan := planNoOp(t, raw)
	for i := range plan.Providers {
		if plan.Providers[i].ProviderID == "go-cuenta-1" {
			plan.Providers[i].Models = []string{"deepseek-flash", "glm-5.2", "glm-5.3-flash"}
		}
	}
	return plan
}

// ---- helpers de yaml.Node ----

func mustNode(t *testing.T, raw []byte) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &doc
}

func topLevelMapping(doc *yaml.Node) *yaml.Node { return doc.Content[0] }

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func providerNode(t *testing.T, raw []byte, id string) *yaml.Node {
	t.Helper()
	providers := mapValue(topLevelMapping(mustNode(t, raw)), "providers")
	if providers == nil {
		t.Fatal("sección providers ausente en el raw")
	}
	for _, p := range providers.Content {
		if v := mapValue(p, "id"); v != nil && v.Value == id {
			return p
		}
	}
	t.Fatalf("provider %q no encontrado en el YAML", id)
	return nil
}

// providerIDsInOrder devuelve el orden de providers[] del documento.
func providerIDsInOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	providers := mapValue(topLevelMapping(mustNode(t, raw)), "providers")
	if providers == nil {
		t.Fatal("providers ausente")
	}
	ids := make([]string, 0, len(providers.Content))
	for _, p := range providers.Content {
		ids = append(ids, mapValue(p, "id").Value)
	}
	return ids
}

// scalarOf devuelve el float de un scalar de una entry (pricing/metadata).
func scalarFloat(t *testing.T, entry *yaml.Node, key string) float64 {
	t.Helper()
	v := mapValue(entry, key)
	if v == nil {
		t.Fatalf("campo %q ausente en la entry", key)
	}
	f, err := strconv.ParseFloat(v.Value, 64)
	if err != nil {
		t.Fatalf("scalar %q de %q no es float: %v", key, v.Value, err)
	}
	return f
}

// sectionEntries devuelve los pares (keyNode, valueNode) de un mapa top-level.
func sectionEntries(t *testing.T, raw []byte, section string) []struct {
	Key   string
	Value *yaml.Node
} {
	t.Helper()
	m := mapValue(topLevelMapping(mustNode(t, raw)), section)
	if m == nil {
		t.Fatalf("sección %q ausente", section)
	}
	var entries []struct {
		Key   string
		Value *yaml.Node
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		entries = append(entries, struct {
			Key   string
			Value *yaml.Node
		}{m.Content[i].Value, m.Content[i+1]})
	}
	return entries
}

// sectionKeyOrder devuelve el orden de las keys de un mapa top-level.
func sectionKeyOrder(t *testing.T, raw []byte, section string) []string {
	t.Helper()
	entries := sectionEntries(t, raw, section)
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	return keys
}

// planFor es el equivalente local del helper de catalogmerge_test.go.
func planFor(t *testing.T, plan catalogmerge.Plan, providerID string) catalogmerge.ProviderPlan {
	t.Helper()
	for _, p := range plan.Providers {
		if p.ProviderID == providerID {
			return p
		}
	}
	t.Fatalf("ProviderPlan %q ausente", providerID)
	return catalogmerge.ProviderPlan{}
}

// ---- B1 (C1, P1) — asociación IR→YAML 1:1 ----

// TestSerialize_Association congela P1: todo ProviderPlan.ProviderID machea
// exactamente un nodo providers[].id del raw; provider en el YAML sin plan →
// error descriptivo; ProviderID del plan sin nodo → error ("machea exactamente
// uno" es bidireccional); id duplicado en el raw → error.
func TestSerialize_Association(t *testing.T) {
	raw := fixtureRaw(t)
	full := planNoOp(t, raw)

	t.Run("asociacion_completa_ok", func(t *testing.T) {
		if _, _, err := Serialize(raw, full); err != nil {
			t.Fatalf("Serialize con plan 1:1 completo: %v", err)
		}
	})

	t.Run("provider_yaml_sin_plan_error", func(t *testing.T) {
		plan := planNoOp(t, raw)
		kept := plan.Providers[:0]
		for _, p := range plan.Providers {
			if p.ProviderID != "auto-sin-match" {
				kept = append(kept, p)
			}
		}
		plan.Providers = kept
		_, _, err := Serialize(raw, plan)
		if err == nil {
			t.Fatal("provider en el YAML sin plan correspondiente debería dar error (P1)")
		}
		if !strings.Contains(err.Error(), "auto-sin-match") {
			t.Errorf("error debería ser descriptivo (nombrar el provider), got: %v", err)
		}
	})

	t.Run("plan_con_id_inexistente_error", func(t *testing.T) {
		plan := planNoOp(t, raw)
		plan.Providers = append(plan.Providers, catalogmerge.ProviderPlan{
			ProviderID: "provider-fantasma",
			Models:     []string{"m"},
		})
		_, _, err := Serialize(raw, plan)
		if err == nil {
			t.Fatal("ProviderID del plan sin nodo en el raw debería dar error (machea exactamente UNO)")
		}
		if !strings.Contains(err.Error(), "provider-fantasma") {
			t.Errorf("error debería nombrar el ProviderID, got: %v", err)
		}
	})

	t.Run("id_duplicado_en_raw_error", func(t *testing.T) {
		dup := []byte(`
providers:
  - id: dup
    base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
  - id: dup
    base_url: "https://y/v1"
    api_key_env: "K"
    models: ["m"]
`)
		plan := catalogmerge.Plan{Providers: []catalogmerge.ProviderPlan{
			{ProviderID: "dup", Models: []string{"m"}},
		}}
		_, _, err := Serialize(dup, plan)
		if err == nil {
			t.Fatal("id duplicado en el raw debería dar error (machea exactamente UNO)")
		}
		if !strings.Contains(err.Error(), "dup") {
			t.Errorf("error debería nombrar el id duplicado, got: %v", err)
		}
	})
}

// ---- B2 (C2, P2+D3) — orden vivo preservado ----

// TestSerialize_InPlaceOrder congela P2/D3: el orden de los providers en el
// candidato es IDÉNTICO al del raw (que es la cadena de fallback), NUNCA el
// orden alfabético del IR. Discriminante: el plan llega en orden alfabético.
func TestSerialize_InPlaceOrder(t *testing.T) {
	raw := fixtureRaw(t)
	rawOrder := providerIDsInOrder(t, raw)

	plan := planWithGoModelChange(t, raw)
	// el plan se recorre en orden ALFABÉTICO de ProviderID (determinismo del
	// IR de 003) — distinto del orden del documento.
	sort.Slice(plan.Providers, func(i, j int) bool {
		return plan.Providers[i].ProviderID < plan.Providers[j].ProviderID
	})
	planOrder := make([]string, 0, len(plan.Providers))
	for _, p := range plan.Providers {
		planOrder = append(planOrder, p.ProviderID)
	}

	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	candOrder := providerIDsInOrder(t, candidate)

	if !reflect.DeepEqual(candOrder, rawOrder) {
		t.Fatalf("orden vivo violado: candidato %v, raw %v (plan llegó como %v)", candOrder, rawOrder, planOrder)
	}
	if reflect.DeepEqual(rawOrder, planOrder) {
		t.Fatal("fixture inválido: el orden del raw coincide con el del plan — el test no discrimina")
	}
}

// ---- B3 (C2, P2) — todo campo del provider salvo models byte-intacto ----

// TestSerialize_ProviderFieldsUntouched congela P2: solo el nodo models de
// cada provider macheado se reemplaza; TODO otro campo (base_url, api_key_env,
// max_tokens, thinking_path, opencode_session, knobs sync_source/sync_mirror,
// type/backend/command/clients/backend_flags/session_dir) queda byte-intacto.
func TestSerialize_ProviderFieldsUntouched(t *testing.T) {
	raw := fixtureRaw(t)
	plan := planWithGoModelChange(t, raw)
	// sub-acc también tocado (mismo models → sin cambio semántico, pero pasa
	// por el path de reemplazo): Models == declarado, así el cambio queda
	// concentrado en go-cuenta-1.
	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	for _, tc := range []struct {
		id     string
		tocado bool
	}{
		{id: "go-cuenta-1", tocado: true},
		{id: "sub-acc", tocado: true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			rawNode := providerNode(t, raw, tc.id)
			candNode := providerNode(t, candidate, tc.id)

			rawMap := rawNode
			for i := 0; i+1 < len(rawMap.Content); i += 2 {
				key := rawMap.Content[i].Value
				if key == "models" {
					continue // único nodo tocable por el plan (P2)
				}
				rawV := rawMap.Content[i+1]
				candV := mapValue(candNode, key)
				if candV == nil {
					t.Errorf("campo %q del provider %q desapareció en el candidato", key, tc.id)
					continue
				}
				// Oracle: re-encode del subárbol (incluye estilo y comentarios).
				rawEnc, err := yaml.Marshal(rawV)
				if err != nil {
					t.Fatalf("encode raw %q: %v", key, err)
				}
				candEnc, err := yaml.Marshal(candV)
				if err != nil {
					t.Fatalf("encode candidato %q: %v", key, err)
				}
				if !bytes.Equal(rawEnc, candEnc) {
					t.Errorf("campo %q del provider %q modificado:\nraw:  %s\ncand: %s", key, tc.id, rawEnc, candEnc)
				}
			}
			// claves NUEVAS inventadas por el serializer → I4 (no inventar)
			for i := 0; i+1 < len(candNode.Content); i += 2 {
				key := candNode.Content[i].Value
				if key == "models" {
					continue
				}
				if mapValue(rawNode, key) == nil {
					t.Errorf("el candidato agregó el campo %q al provider %q (P2: solo models cambia)", key, tc.id)
				}
			}
		})
	}
}

// ---- B4 (C3, P3) — provider degradado byte-idéntico ----

// TestSerialize_DegradedProviderUntouched congela P3: ProviderPlan.Models ==
// nil → el nodo del provider queda byte-idéntico al raw (ni models ni nada).
// Discriminante: el plan NO está vacío (otros providers tocados), así que un
// impl que "toque por las dudas" falla.
//
// Oracle coherente con D2 (HITL): la normalización cosmética de estilo
// (p.ej. el espaciado de columna de un LineComment en el re-encode del
// documento) es ACEPTADA por D2 — el contrato byte-fiel se define sobre el
// fixture normalizado. El subárbol del degradado se compara bajo re-encode
// (como B3), que conserva dientes completos: un touch semántico (models:
// null, valor cambiado, quoting cambiado, item reordenado, comentario
// perdido) cambia el encode → test rojo.
func TestSerialize_DegradedProviderUntouched(t *testing.T) {
	raw := fixtureRaw(t)
	plan := planWithGoModelChange(t, raw)
	// Path real de P3 (003 P12a): fuente de acceso ausente → el IR entrega
	// Models nil. planWithGoModelChange viene de planNoOp con Models verbatim
	// (non-nil) — seteamos nil EXPLÍCITO para ejercitar el path nil, no el
	// "models declaradas = declaradas" (más débil; ese caso queda cubierto
	// por el golden B8).
	for i := range plan.Providers {
		if plan.Providers[i].ProviderID == "auto-sin-match" {
			plan.Providers[i].Models = nil
		}
	}

	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if bytes.Equal(candidate, raw) {
		t.Fatal("el candidato completo es idéntico al raw: el plan no aplicó — test no discrimina")
	}

	rawEnc, err := yaml.Marshal(providerNode(t, raw, "auto-sin-match"))
	if err != nil {
		t.Fatalf("encode del subárbol raw: %v", err)
	}
	candEnc, err := yaml.Marshal(providerNode(t, candidate, "auto-sin-match"))
	if err != nil {
		t.Fatalf("encode del subárbol del candidato: %v", err)
	}
	if !bytes.Equal(rawEnc, candEnc) {
		t.Errorf("P3: el subárbol del provider degradado %q difiere del raw:\n--- raw ---\n%s\n--- candidato ---\n%s", "auto-sin-match", rawEnc, candEnc)
	}
}

// ---- B5 (C4, P4 a/b/d/e/f) — merge de pricing/model_metadata ----

// TestSerialize_PricingMetadataMerge congela P4: (a) campo presente →
// sobrescribe; (b) campo ausente/zero-value → preserva; (d) entry nueva en
// posición alfabética con SOLO los campos provistos; (e) entry no cubierta
// byte-intacta; (f) NINGUNA key se borra. Keys por ID de acceso OR
// (z-ai/glm-5.3-flash) y cortas coexistiendo (C4).
func TestSerialize_PricingMetadataMerge(t *testing.T) {
	raw := fixtureRaw(t)

	t.Run("a_sobrescribe_campo_presente_preserva_ausente", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Pricing = map[string]config.PricingConfig{
			"glm-5.2": {InputUSDPerM: 2.5}, // output/cache_hit zero → preserva
		}
		plan.Providers[0] = pp // go-cuenta-1 es el primero del fixture

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		entries := sectionEntries(t, candidate, "pricing")
		var glm *yaml.Node
		for i := range entries {
			if entries[i].Key == "glm-5.2" {
				glm = entries[i].Value
			}
		}
		if glm == nil {
			t.Fatal("pricing[glm-5.2] desapareció (P4f)")
		}
		if got := scalarFloat(t, glm, "input_usd_per_m"); got != 2.5 {
			t.Errorf("input_usd_per_m = %v, want 2.5 (P4a: presente en plan → sobrescribe)", got)
		}
		if got := scalarFloat(t, glm, "output_usd_per_m"); got != 4.4 {
			t.Errorf("output_usd_per_m = %v, want 4.4 (P4b: ausente en plan → preserva)", got)
		}
		if got := scalarFloat(t, glm, "cache_hit_usd_per_m"); got != 0.26 {
			t.Errorf("cache_hit_usd_per_m = %v, want 0.26 (P4b)", got)
		}
	})

	t.Run("b_plan_zero_preserva_entry_completa", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Pricing = map[string]config.PricingConfig{
			"minimax-m3": {}, // zero-value completo → entry intocada
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		for _, e := range sectionEntries(t, candidate, "pricing") {
			if e.Key != "minimax-m3" {
				continue
			}
			if got := scalarFloat(t, e.Value, "input_usd_per_m"); got != 0.3 {
				t.Errorf("input_usd_per_m = %v, want 0.3 (P4b: zero-value preserva)", got)
			}
			if got := scalarFloat(t, e.Value, "output_usd_per_m"); got != 1.2 {
				t.Errorf("output_usd_per_m = %v, want 1.2 (P4b)", got)
			}
			if got := scalarFloat(t, e.Value, "cache_hit_usd_per_m"); got != 0.03 {
				t.Errorf("cache_hit_usd_per_m = %v, want 0.03 (P4b)", got)
			}
		}
	})

	t.Run("d_entry_nueva_posicion_alfabetica_solo_campos_provistos", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Pricing = map[string]config.PricingConfig{
			"deepseek-flash": {InputUSDPerM: 0.11},
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		keys := sectionKeyOrder(t, candidate, "pricing")
		// posición alfabética: deepseek-flash < glm-5.2 → PRIMERO;
		// las keys existentes NO se reordenan (P4e).
		want := []string{"deepseek-flash", "glm-5.2", "minimax-m3", "z-ai/glm-5.3-flash", "qwen3.7-plus"}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("orden de pricing = %v, want %v (P4d: posición alfabética, sin reordenar existentes)", keys, want)
		}
		for _, e := range sectionEntries(t, candidate, "pricing") {
			if e.Key != "deepseek-flash" {
				continue
			}
			if len(e.Value.Content) != 2 { // UNA key → un par key/value
				t.Errorf("entry nueva tiene %d hijos, want 2 (solo los campos que el plan provee)", len(e.Value.Content))
			}
			if got := scalarFloat(t, e.Value, "input_usd_per_m"); got != 0.11 {
				t.Errorf("input_usd_per_m entry nueva = %v, want 0.11", got)
			}
		}
	})

	t.Run("e_entry_no_cubierta_byte_intacta", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		// solo toca la entry OR (a2) — qwen3.7-plus (corta) debe quedar intacta
		pp.Pricing = map[string]config.PricingConfig{
			"z-ai/glm-5.3-flash": {InputUSDPerM: 0.3},
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		for _, e := range sectionEntries(t, candidate, "pricing") {
			switch e.Key {
			case "qwen3.7-plus":
				if got := scalarFloat(t, e.Value, "input_usd_per_m"); got != 0.4 {
					t.Errorf("qwen3.7-plus input = %v, want 0.4 (P4e: key corta coexistente intacta)", got)
				}
				if got := scalarFloat(t, e.Value, "output_usd_per_m"); got != 1.6 {
					t.Errorf("qwen3.7-plus output = %v, want 1.6 (P4e)", got)
				}
			case "z-ai/glm-5.3-flash":
				if got := scalarFloat(t, e.Value, "input_usd_per_m"); got != 0.3 {
					t.Errorf("z-ai input = %v, want 0.3 (key OR de acceso tocada)", got)
				}
				if got := scalarFloat(t, e.Value, "output_usd_per_m"); got != 1.1 {
					t.Errorf("z-ai output = %v, want 1.1 (P4b: preserva)", got)
				}
			}
		}
	})

	t.Run("f_ninguna_key_se_borra", func(t *testing.T) {
		rawKeys := sectionKeyOrder(t, raw, "pricing")
		rawMeta := sectionKeyOrder(t, raw, "model_metadata")

		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Pricing = map[string]config.PricingConfig{"deepseek-flash": {InputUSDPerM: 0.11}}
		pp.Metadata = map[string]config.ModelMetadata{
			"deepseek-v4-flash": {ContextWindow: 1000000},
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		candKeys := sectionKeyOrder(t, candidate, "pricing")
		if len(candKeys) != len(rawKeys)+1 {
			t.Errorf("pricing: %d keys tras el merge, want %d (raw + la nueva; P4f: nunca se borra)", len(candKeys), len(rawKeys)+1)
		}
		candMeta := sectionKeyOrder(t, candidate, "model_metadata")
		if len(candMeta) != len(rawMeta)+1 {
			t.Errorf("model_metadata: %d keys tras el merge, want %d (P4f)", len(candMeta), len(rawMeta)+1)
		}
		// todas las keys del raw siguen presentes
		for _, k := range rawKeys {
			found := false
			for _, ck := range candKeys {
				if ck == k {
					found = true
				}
			}
			if !found {
				t.Errorf("key de pricing %q del raw fue BORRADA (P4f)", k)
			}
		}
	})

	t.Run("f_metadata_entry_nueva_alfabetica", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Metadata = map[string]config.ModelMetadata{
			"deepseek-v4-flash": {ContextWindow: 1000000},
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		keys := sectionKeyOrder(t, candidate, "model_metadata")
		// deepseek-v4-flash < glm-5.2 → posición alfabética = primera
		if len(keys) == 0 || keys[0] != "deepseek-v4-flash" {
			t.Fatalf("model_metadata keys = %v, want deepseek-v4-flash primero (P4d: posición alfabética)", keys)
		}
		for _, e := range sectionEntries(t, candidate, "model_metadata") {
			if e.Key == "deepseek-v4-flash" {
				if len(e.Value.Content) != 2 {
					t.Errorf("entry nueva con %d pares key/value, want 1 (solo context_window provisto)", len(e.Value.Content)/2)
				}
			}
		}
	})
}

// ---- B6 (C5, P4c) — thinking_default JAMÁS se toca ----

// TestSerialize_ThinkingMergeBack congela P4c: thinking_default existente se
// PRESERVA SIEMPRE (aunque la entry sea tocada — jamás derivable, 003 P9);
// los niveles thinking: presente en el plan → sobrescribe (toggle minimax),
// ausente → preserva.
func TestSerialize_ThinkingMergeBack(t *testing.T) {
	raw := fixtureRaw(t)

	t.Run("thinking_presente_sobrescribe_default_preserva", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Metadata = map[string]config.ModelMetadata{
			"glm-5.2": {Thinking: []string{"medium", "max"}}, // toggle de niveles
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		for _, e := range sectionEntries(t, candidate, "model_metadata") {
			if e.Key != "glm-5.2" {
				continue
			}
			if v := mapValue(e.Value, "thinking"); v == nil {
				t.Fatal("thinking desapareció")
			} else {
				var got []string
				for _, it := range v.Content {
					got = append(got, it.Value)
				}
				if want := []string{"medium", "max"}; !reflect.DeepEqual(got, want) {
					t.Errorf("thinking = %v, want %v (P4a: presente en plan sobrescribe)", got, want)
				}
			}
			if v := mapValue(e.Value, "thinking_default"); v == nil || v.Value != "low" {
				t.Errorf("thinking_default = %v, want \"low\" (P4c: JAMÁS se toca)", v)
			}
			if got := scalarFloat(t, e.Value, "context_window"); got != 200000 {
				t.Errorf("context_window = %v, want 200000 (P4b: campo ausente en plan preserva)", got)
			}
		}
	})

	t.Run("thinking_ausente_en_plan_preserva", func(t *testing.T) {
		plan := planWithGoModelChange(t, raw)
		pp := planFor(t, plan, "go-cuenta-1")
		pp.Metadata = map[string]config.ModelMetadata{
			"glm-5.2": {SupportedParameters: []string{"tools"}}, // solo eso provisto
		}
		plan.Providers[0] = pp

		candidate, _, err := Serialize(raw, plan)
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		for _, e := range sectionEntries(t, candidate, "model_metadata") {
			if e.Key != "glm-5.2" {
				continue
			}
			if v := mapValue(e.Value, "thinking"); v == nil || len(v.Content) != 2 || v.Content[0].Value != "low" {
				t.Errorf("thinking = %v, want [low high] preservado (P4b)", v)
			}
			if v := mapValue(e.Value, "thinking_default"); v == nil || v.Value != "low" {
				t.Errorf("thinking_default = %v, want \"low\" (P4c)", v)
			}
			if v := mapValue(e.Value, "supported_parameters"); v == nil || len(v.Content) != 1 || v.Content[0].Value != "tools" {
				t.Errorf("supported_parameters = %v, want [tools] (P4a: presente → sobrescribe/agrega)", v)
			}
		}
	})
}

// ---- B7 (C6, P5) — preservación de comentarios ----

// TestSerialize_CommentsPreserved congela P5: comentarios Head/Line/Foot de
// nodos no tocados byte-idénticos; comentarios de nodos tocados sobreviven
// (LineComment sobre la key cuyo scalar cambia; nota-block dentro del provider
// cuya models cambian; HeadComment/FootComment de documento y mapas).
func TestSerialize_CommentsPreserved(t *testing.T) {
	raw := fixtureRaw(t)

	plan := planWithGoModelChange(t, raw) // toca go-cuenta-1 models
	pp := planFor(t, plan, "go-cuenta-1")
	pp.Pricing = map[string]config.PricingConfig{
		"glm-5.2": {InputUSDPerM: 2.5}, // entry tocada con LineComment en el scalar
	}
	plan.Providers[0] = pp

	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	cand := string(candidate)

	for _, tc := range []struct {
		name, marker string
	}{
		{"head_comment_documento", "HeadComment nivel documento"},
		{"foot_comment_documento", "FootComment nivel documento"},
		{"line_comment_key_server", "LineComment nivel key"},
		{"line_comment_models_provider_tocado", "LineComment de la lista models"},
		{"comentario_intermedio_provider", "comentario intermedio DENTRO del provider"},
		{"head_comment_entry_pricing", "nota de verificación de precio OR"},
		{"line_comment_scalar_pricing_tocado", "precio verificado 2026-09"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(cand, tc.marker) {
				t.Errorf("comentario %q (marcador %q) NO sobrevivió al round-trip con plan tocado (P5)", tc.name, tc.marker)
			}
		})
	}
}

// ---- B8 (C7, P6+D8) — golden byte-fiel con plan no-op ----

// TestRoundTrip_Golden congela P6: RoundTrip(fixture, planNoOp) == fixture
// BYTE A BYTE — congela que server, fallback, registry, telemetry, context,
// embeddings, client_config, clients_file y cualquier sección futura pasan
// por el round-trip SIN mutación semántica ni cosmética (D2: el contrato
// byte-fiel se define sobre el fixture normalizado, D8).
//
// Protocolo D8: el golden se MATERIALIZA vía el round-trip del propio
// serializer (self-consistente): `go test ./internal/configsync -run
// TestRoundTrip_Golden -update` con aprobación HITL (test-audit §5). En RED
// el test falla por compilación (Serialize no existe).
func TestRoundTrip_Golden(t *testing.T) {
	raw := fixtureRaw(t)
	plan := planNoOp(t, raw)

	got, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize round-trip: %v", err)
	}

	if *goldenUpdate {
		golden := filepath.Join("testdata", "config-roundtrip.yaml")
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("materializando golden: %v", err)
		}
		return
	}

	if !bytes.Equal(got, raw) {
		t.Errorf("round-trip NO es byte-fiel (P6):\n--- fixture (%d bytes) ---\n%s\n--- candidato (%d bytes) ---\n%s",
			len(raw), raw, len(got), got)
	}
}

// ---- B9 (C8, P16) — round-trip del config vivo, opt-in ----

// TestRoundTrip_LiveConfig congela P16: canary de la decisión HITL (D2) en el
// entorno real — el round-trip SIN mutación del config REAL debe ser byte a
// byte. Skip silencioso si el archivo no existe (nunca depende de /home/ofap
// para CI). El I/O es SOLO del test (el paquete puro no hace I/O, I1).
func TestRoundTrip_LiveConfig(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("sin home detectable: %v (opt-in P16)", err)
	}
	path := filepath.Join(home, ".config", "mofgw", "config.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("config vivo ausente en %s: %v (opt-in P16)", path, err)
	}

	plan := planNoOp(t, raw)
	got, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize del config vivo: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Errorf("round-trip del config vivo NO es byte-fiel (canary D2 — si yaml.v3 normaliza algo cosmético, este test lo DETECTA):\n--- primeros diffs ---")
		rawLines := strings.Split(string(raw), "\n")
		gotLines := strings.Split(string(got), "\n")
		n := len(rawLines)
		if len(gotLines) < n {
			n = len(gotLines)
		}
		shown := 0
		for i := 0; i < n && shown < 10; i++ {
			if rawLines[i] != gotLines[i] {
				t.Errorf("línea %d:\nraw:  %q\ncand: %q", i+1, rawLines[i], gotLines[i])
				shown++
			}
		}
	}
}

// ---- B10 (C9, P7) — el candidato validado refleja el plan ----

// TestSerialize_ValidatedCandidate congela P7: ParseForValidation del
// candidato exita 0-error; el Config refleja Models/Pricing/Metadata del plan
// y todo lo demás idéntico al Config del raw. Usa fixture inline SIN
// clients_file (test-audit §4-C). RED: doble — configsync.ParseForValidation
// del candidato y config.ParseForValidation no existen.
func TestSerialize_ValidatedCandidate(t *testing.T) {
	raw := []byte(validRaw004)
	plan := catalogmerge.Plan{
		Providers: []catalogmerge.ProviderPlan{
			{
				ProviderID: "zen-acc",
				Models:     []string{"deepseek-flash", "glm-5.2", "minimax-m3"},
				Pricing: map[string]config.PricingConfig{
					"glm-5.2":        {InputUSDPerM: 2.0},
					"deepseek-flash": {InputUSDPerM: 0.11}, // entry nueva
				},
				Metadata: map[string]config.ModelMetadata{
					"deepseek-v4-flash": {ContextWindow: 1000000}, // entry nueva
				},
			},
		},
	}

	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	cfgRaw, err := config.ParseForValidation(raw)
	if err != nil {
		t.Fatalf("ParseForValidation(raw): %v", err)
	}
	cfgCand, err := config.ParseForValidation(candidate)
	if err != nil {
		t.Fatalf("ParseForValidation(candidato) debería exitar (D5): %v", err)
	}

	// I5/P13b: la validación NO resuelve env → APIKey vacía.
	if cfgCand.Providers[0].APIKey != "" {
		t.Errorf("APIKey del candidato validado = %q, want \"\" (I5: keys jamás pasan por el sync)", cfgCand.Providers[0].APIKey)
	}

	// Models refleja el plan.
	if want := plan.Providers[0].Models; !reflect.DeepEqual(cfgCand.Providers[0].Models, want) {
		t.Fatalf("Models = %v, want %v (P7)", cfgCand.Providers[0].Models, want)
	}

	// Providers idénticos salvo Models.
	bp := append([]config.ProviderConfig(nil), cfgRaw.Providers...)
	ap := append([]config.ProviderConfig(nil), cfgCand.Providers...)
	for i := range bp {
		bp[i].Models = nil
		ap[i].Models = nil
	}
	if !reflect.DeepEqual(bp, ap) {
		t.Errorf("Providers (salvo Models) alterados: raw=%+v cand=%+v (P7)", bp, ap)
	}

	// Pricing refleja el plan: touched sobrescribe, nueva agregada, resto igual.
	if got := cfgCand.Pricing["glm-5.2"].InputUSDPerM; got != 2.0 {
		t.Errorf("Pricing[glm-5.2].InputUSDPerM = %v, want 2.0 (P7)", got)
	}
	if got := cfgCand.Pricing["glm-5.2"].OutputUSDPerM; got != 4.4 {
		t.Errorf("Pricing[glm-5.2].OutputUSDPerM = %v, want 4.4 (P4b vía P7)", got)
	}
	if got := cfgCand.Pricing["deepseek-flash"].InputUSDPerM; got != 0.11 {
		t.Errorf("Pricing[deepseek-flash] nueva = %+v, want input 0.11 (P4d vía P7)", cfgCand.Pricing["deepseek-flash"])
	}
	if len(cfgRaw.Pricing) != 1 || len(cfgCand.Pricing) != 2 {
		t.Errorf("conteo pricing: raw=%d cand=%d, want 1→2 (P4f vía P7)", len(cfgRaw.Pricing), len(cfgCand.Pricing))
	}

	// Metadata refleja la entry nueva y preserva la existente.
	if got := cfgCand.ModelMetadata["deepseek-v4-flash"].ContextWindow; got != 1000000 {
		t.Errorf("Metadata[deepseek-v4-flash].ContextWindow = %d, want 1000000 (P4d vía P7)", got)
	}
	if got := cfgCand.ModelMetadata["glm-5.2"].ContextWindow; got != 200000 {
		t.Errorf("Metadata[glm-5.2].ContextWindow = %d, want 200000 (preserva)", got)
	}

	// Todo lo demás del Config idéntico (zero-eando lo tocado).
	if !reflect.DeepEqual(cfgRaw.Server, cfgCand.Server) {
		t.Errorf("Server alterado: %+v → %+v (P7)", cfgRaw.Server, cfgCand.Server)
	}
	if !reflect.DeepEqual(cfgRaw.Fallback, cfgCand.Fallback) {
		t.Errorf("Fallback alterado: %+v → %+v (P7)", cfgRaw.Fallback, cfgCand.Fallback)
	}
	if !reflect.DeepEqual(cfgRaw.Clients, cfgCand.Clients) {
		t.Errorf("Clients alterado: %+v → %+v (P7)", cfgRaw.Clients, cfgCand.Clients)
	}
}

// ---- B11 (C13, P11+D9) — determinismo byte a byte ----

// TestSerialize_Deterministic congela P11: dos llamadas Serialize(raw, plan)
// con iguales entradas → bytes idénticos; keys nuevas en orden alfabético
// (herencia D9/P11 de 003); warnings del reporte = passthrough VERBATIM del
// plan (ya llega sorted+dedup del IR — el serializer NO reordena).
func TestSerialize_Deterministic(t *testing.T) {
	raw := fixtureRaw(t)
	plan := planWithGoModelChange(t, raw)
	pp := planFor(t, plan, "go-cuenta-1")
	pp.Pricing = map[string]config.PricingConfig{
		"deepseek-flash": {InputUSDPerM: 0.11}, // key NUEVA → riesgo de range sin sort
	}
	plan.Providers[0] = pp
	plan.Warnings = []string{"warn-alfa", "warn-beta"} // como llega del IR: sorted, dedup

	c1, r1, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize #1: %v", err)
	}
	c2, r2, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize #2: %v", err)
	}

	if !bytes.Equal(c1, c2) {
		t.Error("dos corridas con iguales entradas producen bytes DISTINTOS (P11/D9)")
	}
	if !reflect.DeepEqual(r1.Warnings, plan.Warnings) {
		t.Errorf("Report.Warnings = %v, want passthrough verbatim %v (P11: sin reordenar)", r1.Warnings, plan.Warnings)
	}
	if !reflect.DeepEqual(r1.Warnings, r2.Warnings) {
		t.Errorf("Report.Warnings no determinístico: %v vs %v", r1.Warnings, r2.Warnings)
	}
	if r1.Digest != r2.Digest || len(r1.Digest) != 64 {
		t.Errorf("Digest = %q / %q, want hex sha256 de 64 chars estable", r1.Digest, r2.Digest)
	}
}
