// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizza
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package configsync materializa el Plan de 019-003 en config.yaml
// (019-004): Serialize edita estructuralmente el YAML vivo a yaml.Node
// (mutando únicamente los nodos alcanzables por el Plan, D2-D4) y Apply
// valida el candidato pre-commit y lo escribe atómicamente con skip
// byte-idéntico por digest sidecar (D5/D6).
//
// Pureza (I1): sin I/O (el único os.* permitido son los TIPOS de la
// interface FS — el contrato firmado por HITL en test-audit §4-A), sin red,
// sin log, sin reloj. raw []byte + Plan entran; candidato + Report salen.
// El binario cmd/mofgw-sync es el único que toca disco y loguea.
package configsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/ofapsaas/mofgw/internal/catalogmerge"
	"github.com/ofapsaas/mofgw/internal/config"

	"gopkg.in/yaml.v3"
)

// Report es el resultado observable de Serialize/Apply (contrato del
// test-writer, test-audit §3): Applied/Skipped los llena Apply (Serialize
// deja false); Digest es el sha256 hex del candidato (siempre); Warnings es
// plan.Warnings VERBATIM al inicio (P11/P12 — ya llega sorted+dedup del IR
// de 003, sin reordenar) + warnings de merge-back appendeados después
// (P4c E3: default stale omitido).
type Report struct {
	Applied  bool
	Skipped  bool
	Digest   string
	Warnings []string
}

// Serialize produce el candidato de config.yaml: decodifica el YAML vivo a
// yaml.Node y muta ÚNICAMENTE los nodos alcanzables por el Plan (D2): el
// nodo secuencia `models` de cada provider macheado y las entries de los
// mapas pricing/model_metadata (merge-back por campo, P4). Puro (I1).
//
// Asociación 1:1 (P1, bidireccional); orden vivo in-place (P2/D3: recorrido
// en el orden del documento, jamás el alfabético del IR); provider con
// Models nil no tocado (P3); comentarios preservados (P5: al reemplazar un
// nodo se transfieren Head/Line/Foot del nodo viejo al nuevo); determinismo
// byte a byte (P11/D9: iteración de mapas siempre sortada). Si el plan no
// cambia ningún nodo, el candidato es el raw VERBATIM (P6/P16: round-trip
// sin mutación semántica NI cosmética).
func Serialize(raw []byte, plan catalogmerge.Plan) ([]byte, Report, error) {
	report := Report{Warnings: plan.Warnings}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, Report{}, fmt.Errorf("configsync: serialize: YAML inválido: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, Report{}, fmt.Errorf("configsync: serialize: el documento no es un mapping top-level")
	}
	root := doc.Content[0]

	providersNode := mapNodeGet(root, "providers")
	if err := checkAssociation(providersNode, plan); err != nil {
		return nil, Report{}, err
	}

	planByID := make(map[string]*catalogmerge.ProviderPlan, len(plan.Providers))
	for i := range plan.Providers {
		planByID[plan.Providers[i].ProviderID] = &plan.Providers[i]
	}

	mutated := false
	if providersNode != nil {
		// D3: recorrido en el ORDEN DEL DOCUMENTO (cadena de fallback).
		for _, prov := range providersNode.Content {
			pp := planByID[mapNodeGet(prov, "id").Value]
			if pp.Models == nil {
				continue // P3: degradado (fuente de acceso ausente) no tocado
			}
			if replaceModels(prov, pp.Models) {
				mutated = true
			}
		}
	}

	m, w, err := mergeKeyedSection(root, "pricing", pricingEntries(plan))
	if err != nil {
		return nil, Report{}, err
	}
	report.Warnings = append(report.Warnings, w...)
	mutated = mutated || m
	m, w, err = mergeKeyedSection(root, "model_metadata", metadataEntries(plan))
	if err != nil {
		return nil, Report{}, err
	}
	report.Warnings = append(report.Warnings, w...)
	mutated = mutated || m

	if !mutated {
		report.Digest = sha256Hex(raw)
		return raw, report, nil // P6/P16: round-trip byte-fiel sin mutación
	}

	candidate, err := encodeNode(&doc)
	if err != nil {
		return nil, Report{}, fmt.Errorf("configsync: serialize: encode: %w", err)
	}
	report.Digest = sha256Hex(candidate)
	return candidate, report, nil
}

// ---- P1: asociación IR→YAML 1:1 (bidireccional) ----

// checkAssociation verifica que todo ProviderPlan.ProviderID machea
// exactamente un nodo providers[].id del raw y que todo provider del raw
// tiene plan (error descriptivo nombrando el id, en ambas direcciones).
func checkAssociation(providersNode *yaml.Node, plan catalogmerge.Plan) error {
	counts := map[string]int{}
	if providersNode != nil {
		if providersNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("configsync: serialize: `providers` no es una secuencia (P1)")
		}
		for _, prov := range providersNode.Content {
			idNode := mapNodeGet(prov, "id")
			if idNode == nil || idNode.Value == "" {
				return fmt.Errorf("configsync: serialize: provider sin `id` en el raw (P1: asociación 1:1)")
			}
			counts[idNode.Value]++
		}
	}
	for _, id := range sortedIntKeys(counts) {
		if n := counts[id]; n > 1 {
			return fmt.Errorf("configsync: serialize: provider id %q aparece %d veces en el raw (P1: debe machear exactamente uno)", id, n)
		}
	}
	inPlan := make(map[string]bool, len(plan.Providers))
	for _, pp := range plan.Providers {
		if inPlan[pp.ProviderID] {
			return fmt.Errorf("configsync: serialize: ProviderPlan %q duplicado en el plan (P1)", pp.ProviderID)
		}
		inPlan[pp.ProviderID] = true
		if n := counts[pp.ProviderID]; n != 1 {
			return fmt.Errorf("configsync: serialize: ProviderPlan %q machea %d nodos providers[].id del raw, want exactamente 1 (P1)", pp.ProviderID, n)
		}
	}
	for _, id := range sortedIntKeys(counts) {
		if !inPlan[id] {
			return fmt.Errorf("configsync: serialize: provider %q del YAML sin ProviderPlan correspondiente (P1: asociación 1:1)", id)
		}
	}
	return nil
}

// ---- P2/P3/P5/D3: edición in-place de providers ----

// replaceModels reemplaza el nodo secuencia `models` de un provider
// macheado (único campo tocable, P2). Si el plan replica el contenido
// declarado (mismo orden y valores) no hay mutación — round-trip byte-fiel
// (P6). El estilo (flow/block) y los comentarios del nodo viejo se
// transfieren al nuevo (P5b); los scalars existentes se reutilizan por
// valor para preservar su quoting.
func replaceModels(provider *yaml.Node, models []string) bool {
	for i := 0; i+1 < len(provider.Content); i += 2 {
		if provider.Content[i].Value != "models" {
			continue
		}
		old := provider.Content[i+1]
		if seqEqualsValues(old, models) {
			return false // plan == declarado → byte-fiel (P6)
		}
		provider.Content[i+1] = buildSequence(old, models)
		return true
	}
	// Provider sin key models en el raw (edge no cubierto por el IR real):
	// se agrega al final del mapping del provider.
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, m := range models {
		seq.Content = append(seq.Content, strScalar(m))
	}
	provider.Content = append(provider.Content,
		keyScalar("models"), seq)
	return true
}

// buildSequence construye la secuencia reemplazo en el estilo del nodo
// viejo (P5b: comentarios transferidos; reutilización de scalars por valor
// preserva el quoting original de los miembros que sobreviven).
func buildSequence(old *yaml.Node, models []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if old != nil && old.Kind == yaml.SequenceNode {
		seq.Style = old.Style
		seq.HeadComment, seq.LineComment, seq.FootComment = old.HeadComment, old.LineComment, old.FootComment
		byValue := make(map[string]*yaml.Node, len(old.Content))
		for _, s := range old.Content {
			if s.Kind == yaml.ScalarNode && byValue[s.Value] == nil {
				byValue[s.Value] = s
			}
		}
		for _, m := range models {
			if s, ok := byValue[m]; ok {
				seq.Content = append(seq.Content, s)
				continue
			}
			seq.Content = append(seq.Content, strScalar(m))
		}
		return seq
	}
	for _, m := range models {
		seq.Content = append(seq.Content, strScalar(m))
	}
	return seq
}

// seqEqualsValues compara el contenido declarado de una secuencia contra el
// plan (mismo orden, mismos valores).
func seqEqualsValues(seq *yaml.Node, models []string) bool {
	if seq == nil || seq.Kind != yaml.SequenceNode || len(seq.Content) != len(models) {
		return false
	}
	for i, m := range models {
		if seq.Content[i].Kind != yaml.ScalarNode || seq.Content[i].Value != m {
			return false
		}
	}
	return true
}

// ---- P4: merge-back de pricing/model_metadata ----

// kvField es un campo provisto por el plan para una entry.
type kvField struct {
	key   string
	value *yaml.Node
}

// planEntry es una entry keyed (por ID de acceso) con SOLO los campos que
// el plan provee (non-zero), ya ordenados alfabéticamente (P11/D9).
type planEntry struct {
	id     string
	fields []kvField
}

// pricingEntries aplana plan.Pricing de todos los providers (el IR llega
// sorted por ProviderID → orden de aplicación determinístico, D9) en entries
// ordenadas por ID (P11).
func pricingEntries(plan catalogmerge.Plan) []planEntry {
	out := make([]planEntry, 0, len(plan.Providers))
	index := map[string]int{}
	for i := range plan.Providers {
		pp := &plan.Providers[i]
		for _, id := range sortedStrings(mapKeys(pp.Pricing)) {
			fields := pricingFields(pp.Pricing[id])
			if idx, ok := index[id]; ok {
				out[idx].fields = mergeFields(out[idx].fields, fields)
				continue
			}
			index[id] = len(out)
			out = append(out, planEntry{id: id, fields: fields})
		}
	}
	return out
}

// metadataEntries aplana plan.Metadata (P4). thinking_default JAMÁS se
// escribe: preserve-only (P4c — jamás derivable, 003 P9).
func metadataEntries(plan catalogmerge.Plan) []planEntry {
	out := make([]planEntry, 0, len(plan.Providers))
	index := map[string]int{}
	for i := range plan.Providers {
		pp := &plan.Providers[i]
		for _, id := range sortedStrings(mapKeys(pp.Metadata)) {
			fields := metadataFields(pp.Metadata[id])
			if idx, ok := index[id]; ok {
				out[idx].fields = mergeFields(out[idx].fields, fields)
				continue
			}
			index[id] = len(out)
			out = append(out, planEntry{id: id, fields: fields})
		}
	}
	return out
}

// pricingFields aplana una entry de pricing a pares key→scalar SOLO con los
// campos non-zero (P4: presente sobrescribe, ausente/zero preserva), en
// orden alfabético (D9).
func pricingFields(pc config.PricingConfig) []kvField {
	var out []kvField
	if pc.InputUSDPerM != 0 {
		out = append(out, kvField{"input_usd_per_m", floatScalar(pc.InputUSDPerM)})
	}
	if pc.OutputUSDPerM != 0 {
		out = append(out, kvField{"output_usd_per_m", floatScalar(pc.OutputUSDPerM)})
	}
	if pc.CacheHitUSDPerM != 0 {
		out = append(out, kvField{"cache_hit_usd_per_m", floatScalar(pc.CacheHitUSDPerM)})
	}
	return out
}

// metadataFields aplana una entry de metadata (mismo criterio P4/D9). Los
// slices zero-value preservan (P4b); thinking_default JAMÁS se escribe (P4c).
func metadataFields(md config.ModelMetadata) []kvField {
	var out []kvField
	if md.ContextWindow != 0 {
		out = append(out, kvField{"context_window", intScalar(md.ContextWindow)})
	}
	if md.MaxOutput != 0 {
		out = append(out, kvField{"max_output", intScalar(md.MaxOutput)})
	}
	if len(md.Thinking) > 0 {
		out = append(out, kvField{"thinking", strSeqScalar(md.Thinking)})
	}
	if len(md.SupportedParameters) > 0 {
		out = append(out, kvField{"supported_parameters", strSeqScalar(md.SupportedParameters)})
	}
	if md.Modality != "" {
		out = append(out, kvField{"modality", strScalar(md.Modality)})
	}
	return out
}

// mergeFields combina los campos de una entry aportados por dos providers
// (el IR llega sorted por ProviderID → determinístico; el valor del último
// provider gana para el mismo campo).
func mergeFields(base, add []kvField) []kvField {
	out := append([]kvField(nil), base...)
	for _, a := range add {
		replaced := false
		for i := range out {
			if out[i].key == a.key {
				out[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// mergeKeyedSection aplica el merge-back de las entries del plan sobre la
// sección keyed del raw (pricing | model_metadata), P4: (a) campo presente
// sobrescribe (solo si el valor cambia — byte-stable, P6); (b) campo
// ausente/zero preserva; (c) thinking_default jamás tocado (no llega del
// plan) SALVO la excepción de consistencia E3 (abajo); (d) entry nueva con
// solo los campos provistos, insertada en posición alfabética; (e) entry no
// cubierta intocada; (f) NINGUNA key se borra. Comentarios de keys/entries
// sobreviven (P5: las keys no se reemplazan; los scalars transferidos llevan
// los comentarios del nodo viejo).
// Retorna además los warnings de merge-back (P4c E3) para el reporte.
func mergeKeyedSection(root *yaml.Node, section string, entries []planEntry) (bool, []string, error) {
	mutated := false
	var warns []string
	if len(entries) == 0 {
		return false, nil, nil
	}
	sectionNode := mapNodeGet(root, section)
	if sectionNode == nil {
		// Sección ausente en el raw: crearla al final (P4d).
		sectionNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, keyScalar(section), sectionNode)
		mutated = true
	}
	if sectionNode.Kind != yaml.MappingNode {
		return mutated, nil, fmt.Errorf("configsync: serialize: sección %q no es un mapping", section)
	}
	for _, e := range entries {
		if len(e.fields) == 0 {
			continue // P4b: entry zero-value → nada que escribir (preservar)
		}
		idx := keyIndex(sectionNode, e.id)
		if idx < 0 {
			// P4d: entry nueva con SOLO los campos provistos, posición
			// alfabética dentro del mapa (las existentes NO se reordenan).
			// (Las entries nuevas jamás traen thinking_default: no llega
			// del plan — sin chequeo de consistencia acá.)
			entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for _, f := range e.fields {
				entry.Content = append(entry.Content, keyScalar(f.key), f.value)
			}
			pos := insertIndex(sectionNode, e.id)
			sectionNode.Content = splicePair(sectionNode.Content, pos, keyScalar(e.id), entry)
			mutated = true
			continue
		}
		entryNode := sectionNode.Content[idx+1]
		if entryNode.Kind != yaml.MappingNode {
			return mutated, nil, fmt.Errorf("configsync: serialize: entry %q de %q no es un mapping (P4)", e.id, section)
		}
		touchedThinking := false
		for _, f := range e.fields {
			if section == "model_metadata" && f.key == "thinking" {
				touchedThinking = true
			}
			vi := keyIndex(entryNode, f.key)
			if vi < 0 {
				// P4a: campo derivable presente en el plan y ausente en la
				// entry → agregar en posición alfabética.
				pos := insertIndex(entryNode, f.key)
				entryNode.Content = splicePair(entryNode.Content, pos, keyScalar(f.key), f.value)
				mutated = true
				continue
			}
			old := entryNode.Content[vi+1]
			if valuesEqual(old, f.value) {
				continue // valor idéntico → sin mutación (round-trip fiel)
			}
			// P4a: sobrescribe; P5b: los comentarios del nodo viejo
			// (Head/Line/Foot) sobreviven al reemplazo del scalar.
			neu := *f.value
			neu.HeadComment, neu.LineComment, neu.FootComment = old.HeadComment, old.LineComment, old.FootComment
			if old.Style != 0 {
				neu.Style = old.Style
			}
			entryNode.Content[vi+1] = &neu
			mutated = true
		}
		// E3 (P4c consistencia): si el plan proveyó `thinking` para esta
		// entry y el `thinking_default` preservado YA NO está en esos
		// niveles → el default es stale y produciría un candidato que
		// validate() rechaza (caso real qwen3.8-flash: niveles nuevos sin
		// el default manual): se OMITE + warning. Si el plan no proveyó
		// thinking → el default se preserva como antes (era válido).
		if section == "model_metadata" && touchedThinking {
			if di := keyIndex(entryNode, "thinking_default"); di >= 0 {
				def := entryNode.Content[di+1].Value
				levels := seqValues(mapNodeGet(entryNode, "thinking"))
				if !containsString(levels, def) {
					entryNode.Content = removePair(entryNode.Content, di)
					warns = append(warns, fmt.Sprintf("model_metadata %s: thinking_default %q fuera de thinking %v — omitido (default stale, enmienda P4c E3)", e.id, def, levels))
					mutated = true
				}
			}
		}
	}
	return mutated, warns, nil
}

// seqValues: valores string de un nodo secuencia (o nil si no es secuencia).
func seqValues(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, it := range n.Content {
		out = append(out, it.Value)
	}
	return out
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// removePair elimina el par key/value en el índice par di de un mapping.
func removePair(content []*yaml.Node, di int) []*yaml.Node {
	return append(content[:di], content[di+2:]...)
}

// ---- helpers de yaml.Node ----

func keyScalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func strScalar(v string) *yaml.Node {
	return keyScalar(v)
}

func floatScalar(f float64) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(f, 'g', -1, 64)}
}

func intScalar(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

func strSeqScalar(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, strScalar(v))
	}
	return seq
}

// keyIndex devuelve el índice (par key) de key en un mapping, o -1.
func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// mapNodeGet devuelve el valor del key en un mapping, o nil.
func mapNodeGet(m *yaml.Node, key string) *yaml.Node {
	i := keyIndex(m, key)
	if i < 0 {
		return nil
	}
	return m.Content[i+1]
}

// insertIndex devuelve la posición de par donde insertar key en orden
// alfabético (antes del primer key > key; al final si ninguno).
func insertIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value > key {
			return i
		}
	}
	return len(m.Content)
}

// splicePair inserta un par key/value en la posición pos.
func splicePair(content []*yaml.Node, pos int, key, value *yaml.Node) []*yaml.Node {
	out := make([]*yaml.Node, 0, len(content)+2)
	out = append(out, content[:pos]...)
	out = append(out, key, value)
	return append(out, content[pos:]...)
}

// valuesEqual compara semánticamente dos nodos (Kind/Tag/Value y contenido
// recursivo; el estilo es cosmético y no cuenta como diferencia).
func valuesEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Value != b.Value {
		return false
	}
	if a.Kind != yaml.SequenceNode && a.Tag != b.Tag {
		return false
	}
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !valuesEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// encodeNode re-encodifica el documento editado (SetIndent 2 = indent del
// config de referencia; el round-trip Node preserva estilos y comentarios).
func encodeNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mapKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedStrings(in []string) []string {
	sort.Strings(in)
	return in
}

func sortedIntKeys(m map[string]int) []string {
	return sortedStrings(mapKeys(m))
}
