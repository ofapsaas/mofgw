// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package catalogmerge mergea los catálogos upstream ya parseados
// (models.dev, zen, go, openrouter) con los providers de config.yaml y
// produce el IR de sync (Plan) que 019-004 serializará en config.yaml.
// Puro (D9/P13): sin red, sin disco, sin config.Load — todo inyectado.
package catalogmerge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/modelsdev"
	"github.com/ofapsaas/mofgw/internal/upstream"
)

// Plan es el IR de sync (D1/D7: todo ordenado y determinístico).
type Plan struct {
	Providers   []ProviderPlan
	SourcesUsed map[string]bool
	Warnings    []string
}

// ProviderPlan es el plan de un provider de config.yaml (D1).
type ProviderPlan struct {
	ProviderID string
	Source     string
	Models     []string
	Pricing    map[string]config.PricingConfig
	Metadata   map[string]config.ModelMetadata
	Warnings   []string
}

// Merge produce el Plan para los providers dados. nil = fuente ausente
// (fail-soft, D8/P12). Error SOLO si TODAS las fuentes son nil (P12d).
func Merge(catalog *modelsdev.Catalog, zen, goList *upstream.ModelList, or *upstream.OpenRouterCatalog, providers []config.ProviderConfig) (Plan, error) {
	if catalog == nil && zen == nil && goList == nil && or == nil {
		return Plan{}, fmt.Errorf("catalogmerge: sin ninguna fuente (models.dev, zen, go, openrouter todas nil) — nada que mergear")
	}
	plan := Plan{
		SourcesUsed: map[string]bool{},
		Warnings:    nil,
	}
	if catalog != nil {
		plan.SourcesUsed["modelsdev"] = true
	}
	if zen != nil {
		plan.SourcesUsed["zen"] = true
	}
	if goList != nil {
		plan.SourcesUsed["go"] = true
	}
	if or != nil {
		plan.SourcesUsed["openrouter"] = true
	}
	if catalog == nil {
		plan.Warnings = append(plan.Warnings, "models.dev: fuente ausente — sin pricing/metadata")
	}
	plan.Providers = make([]ProviderPlan, 0, len(providers))
	for _, prov := range providers {
		plan.Providers = append(plan.Providers, buildProvider(prov, catalog, zen, goList, or))
	}
	sort.Slice(plan.Providers, func(i, j int) bool {
		return plan.Providers[i].ProviderID < plan.Providers[j].ProviderID
	})
	all := plan.Warnings
	for _, pp := range plan.Providers {
		all = append(all, pp.Warnings...)
	}
	plan.Warnings = sortedDedup(all)
	return plan, nil
}

// buildProvider resuelve el ProviderPlan de un provider (P1-P14).
func buildProvider(prov config.ProviderConfig, catalog *modelsdev.Catalog, zen, goList *upstream.ModelList, or *upstream.OpenRouterCatalog) ProviderPlan {
	source := sourceForProvider(prov)
	pp := ProviderPlan{
		ProviderID: prov.ID,
		Source:     source,
		Pricing:    map[string]config.PricingConfig{},
		Metadata:   map[string]config.ModelMetadata{},
		Warnings:   nil,
	}
	// P12a: la lista de acceso de zen/go ausente (nil) degrada el provider
	// con Models nil + warning; el resto continúa intacto.
	if (source == "zen" && zen == nil) || (source == "go" && goList == nil) {
		pp.Warnings = append(pp.Warnings, prov.ID+": lista de acceso "+source+" ausente (nil) — modelos omitidos")
		return pp
	}
	accessList := prov.Models
	if source == "openrouter" {
		accessList = resolveOpenRouterAccess(prov, or, &pp)
	}
	pp.Models = sortedDedup(accessList)

	if catalog == nil {
		pp.Warnings = append(pp.Warnings, prov.ID+": models.dev ausente — sin pricing/metadata")
		return pp
	}

	mirror := mirrorFor(prov, source, catalog, &pp)
	for _, accID := range pp.Models {
		shortKey := accID
		if source == "openrouter" {
			shortKey = orShortKey(accID, or)
		}
		m, pc, warns, found := lookupModel(catalog, mirror, shortKey)
		var orModel *upstream.OpenRouterModel
		if source == "openrouter" && or != nil {
			orModel = findORModel(or, accID)
		}
		if !found {
			// P10/D6: openrouter es auto-suficiente para METADATA — el modelo
			// puede no estar en models.dev pero el catálogo OR lo describe;
			// pricing queda ausente (models.dev es la única fuente de cost).
			if source == "openrouter" && orModel != nil {
				md := buildMetadata(modelsdev.Model{}, source, orModel, shortKey)
				md.SupportedParameters = buildSupported(source, orModel, modelsdev.Model{})
				pp.Metadata[accID] = md
				continue
			}
			pp.Warnings = append(pp.Warnings, prov.ID+"/"+accID+": no encontrado en models.dev — sin pricing/metadata")
			continue
		}
		pp.Warnings = append(pp.Warnings, warns...)
		md := buildMetadata(m, source, orModel, shortKey)
		md.SupportedParameters = buildSupported(source, orModel, m)
		if costPresent(m.Cost) {
			pp.Pricing[accID] = pc
		}
		pp.Metadata[accID] = md
	}
	pp.Warnings = sortedDedup(pp.Warnings)
	return pp
}

// sourceForProvider asocia provider→fuente (P1/D2): knob gana; auto
// determinístico por base_url (trim "/" y sufijo "/models") contra las
// consts de upstream; sin match → modelsdev; type subprocess → modelsdev.
func sourceForProvider(p config.ProviderConfig) string {
	if p.SyncSource != "" {
		return p.SyncSource
	}
	if p.Type == "subprocess" {
		return "modelsdev"
	}
	base := strings.TrimSuffix(p.BaseURL, "/")
	base = strings.TrimSuffix(base, "/models")
	switch base {
	case strings.TrimSuffix(upstream.ZenURL, "/models"):
		return "zen"
	case strings.TrimSuffix(upstream.GoURL, "/models"):
		return "go"
	case strings.TrimSuffix(upstream.OpenRouterURL, "/models"):
		return "openrouter"
	}
	return "modelsdev"
}

// resolveOpenRouterAccess filtra aliases sin alias_target de la lista de
// acceso de un provider openrouter (P4/D3c): alias sin target → omitido +
// warning (fail-soft).
func resolveOpenRouterAccess(prov config.ProviderConfig, or *upstream.OpenRouterCatalog, pp *ProviderPlan) []string {
	var kept []string
	for _, accID := range prov.Models {
		if !strings.HasPrefix(accID, "~") {
			kept = append(kept, accID)
			continue
		}
		if o := findORModel(or, accID); o != nil && o.AliasTargetSlug != "" {
			kept = append(kept, accID)
			continue
		}
		pp.Warnings = append(pp.Warnings, prov.ID+"/"+accID+": alias sin alias_target — omitido del plan")
	}
	return kept
}

// orShortKey devuelve la key corta de lookup en models.dev para un modelo
// openrouter (P3/P4): alias → strip vendor del alias_target.slug; resto →
// strip vendor del ID de acceso. El ID del plan NO se altera (I2).
func orShortKey(accID string, or *upstream.OpenRouterCatalog) string {
	if strings.HasPrefix(accID, "~") {
		if o := findORModel(or, accID); o != nil && o.AliasTargetSlug != "" {
			return stripVendor(o.AliasTargetSlug)
		}
	}
	return stripVendor(accID)
}

// findORModel busca un modelo openrouter por su ID (tal cual, incl. "~").
func findORModel(or *upstream.OpenRouterCatalog, id string) *upstream.OpenRouterModel {
	if or == nil {
		return nil
	}
	for i := range or.Models {
		if or.Models[i].ID == id {
			return &or.Models[i]
		}
	}
	return nil
}

// stripVendor descarta el prefijo vendor de un id "a/b" → "b" (P3).
func stripVendor(id string) string {
	if i := strings.Index(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// mirrorFor resuelve el provider espejo de models.dev (P6/D4): sync_mirror
// gana si existe; defaults zen→opencode, go→opencode-go; openrouter/
// modelsdev → sin default ("" → fallback alfabético a nivel lookup).
func mirrorFor(prov config.ProviderConfig, source string, catalog *modelsdev.Catalog, pp *ProviderPlan) string {
	if prov.SyncMirror != "" {
		if _, ok := catalog.Providers[prov.SyncMirror]; ok {
			return prov.SyncMirror
		}
		pp.Warnings = append(pp.Warnings, prov.ID+": sync_mirror "+prov.SyncMirror+" ausente de models.dev — fallback a default")
	}
	switch source {
	case "zen":
		return "opencode"
	case "go":
		return "opencode-go"
	}
	return ""
}

// lookupModel resuelve un modelo corto en models.dev vía espejo (P6/D4):
// mirror path → fallback al primer provider alfabético con cost → si nadie
// tiene cost, primer provider con el ID (metadata sin pricing) → not found.
func lookupModel(catalog *modelsdev.Catalog, mirrorID, shortKey string) (modelsdev.Model, config.PricingConfig, []string, bool) {
	var warnings []string
	if mirrorID != "" {
		if p, ok := catalog.Providers[mirrorID]; ok {
			if mm, ok := p.Models[shortKey]; ok {
				pc, w := pricingOf(mm)
				return mm, pc, append(warnings, w...), true
			}
		}
	}
	for _, pid := range sortedProviderIDs(catalog) {
		p := catalog.Providers[pid]
		mm, ok := p.Models[shortKey]
		if !ok {
			continue
		}
		if costPresent(mm.Cost) {
			warnings = append(warnings, fmt.Sprintf("mirror %q sin/ausente el ID %q → fallback alfabético a %q", mirrorID, shortKey, pid))
			pc, w := pricingOf(mm)
			return mm, pc, append(warnings, w...), true
		}
	}
	for _, pid := range sortedProviderIDs(catalog) {
		if mm, ok := catalog.Providers[pid].Models[shortKey]; ok {
			warnings = append(warnings, fmt.Sprintf("%s: sin cost en ningún provider de models.dev → pricing omitido", shortKey))
			return mm, config.PricingConfig{}, warnings, true
		}
	}
	return modelsdev.Model{}, config.PricingConfig{}, warnings, false
}

// pricingOf hace passthrough 1:1 de cost (P7): input/output/cache_read;
// cache_write (≠ base) se descarta con warning.
func pricingOf(m modelsdev.Model) (config.PricingConfig, []string) {
	pc := config.PricingConfig{
		InputUSDPerM:    m.Cost.Input,
		OutputUSDPerM:   m.Cost.Output,
		CacheHitUSDPerM: m.Cost.CacheRead,
	}
	var warnings []string
	if m.Cost.CacheWrite != 0 {
		warnings = append(warnings, "cache_write descartado (PricingConfig plano, P7)")
	}
	return pc, warnings
}

// costPresent reporta si el modelo expone cost real (P6/D4): no todo cero.
func costPresent(c modelsdev.Cost) bool {
	return c.Input != 0 || c.Output != 0 || c.CacheRead != 0 || c.CacheWrite != 0
}

// buildMetadata deriva la metadata (P8): ContextWindow=limit.context,
// MaxOutput=limit.output (fallback OR top_provider.max_completion_tokens),
// Modality=Join(input,"+")+"->"+Join(output,"+") (fallback OR
// architecture.modality); Thinking=effort.values; ThinkingDefault JAMÁS (P9).
func buildMetadata(m modelsdev.Model, source string, orModel *upstream.OpenRouterModel, _ string) config.ModelMetadata {
	md := config.ModelMetadata{
		ContextWindow: m.Limit.Context,
		MaxOutput:     m.Limit.Output,
		Thinking:      m.ReasoningEffort,
	}
	if md.MaxOutput == 0 && source == "openrouter" && orModel != nil {
		md.MaxOutput = orModel.TopProvider.MaxCompletionTokens
	}
	if len(m.Modalities.Input) > 0 || len(m.Modalities.Output) > 0 {
		md.Modality = strings.Join(m.Modalities.Input, "+") + "->" + strings.Join(m.Modalities.Output, "+")
	} else if source == "openrouter" && orModel != nil && orModel.Architecture.Modality != "" {
		md.Modality = orModel.Architecture.Modality
	}
	return md
}

// buildSupported aplica la prioridad de SupportedParameters (P10/D6):
// openrouter → tal cual; openrouter sin OR → nil (omitir); resto → derivación
// de models.dev (structured_outputs/temperature/reasoning/tools), solo
// presentes.
func buildSupported(source string, orModel *upstream.OpenRouterModel, m modelsdev.Model) []string {
	if source == "openrouter" {
		if orModel != nil && len(orModel.SupportedParameters) > 0 {
			return orModel.SupportedParameters
		}
		return nil
	}
	var out []string
	if m.StructuredOutput {
		out = append(out, "structured_outputs")
	}
	if m.Temperature {
		out = append(out, "temperature")
	}
	if m.Reasoning {
		out = append(out, "reasoning")
	}
	if m.ToolCall {
		out = append(out, "tools")
	}
	return out
}

// sortedDedup ordena y deduplica una colección de strings (P11/D7).
// Entrada vacía → nil.
func sortedDedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// sortedProviderIDs devuelve los ids de provider de models.dev ordenados
// alfabéticamente (P11/D7).
func sortedProviderIDs(catalog *modelsdev.Catalog) []string {
	ids := make([]string, 0, len(catalog.Providers))
	for id := range catalog.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
