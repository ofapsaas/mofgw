// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package zot implementa el adapter concreto del epic mofgw-client-config
// (feature 016-004-adapter-zot): un clientconfig.Renderer que serializa un
// clientconfig.ConfigIR en el fragmento JSON del provider "mofgw" listo para
// insertar/mergear bajo el UserModelsFile de zot (`patriceckhart/zot`).
//
// Es serialización pura (entrada ConfigIR, salida []byte JSON válido). A
// diferencia de los adapters opencode/openclaw (016-002/003), el formato de
// zot NO elige campo `apiKey` en absoluto: models.json jamás almacena secrets
// y la key se resuelve en runtime vía la env derivada `UPPER_SNAKE(provider)_API_KEY`
// (= `MOFGW_API_KEY`). Por tanto este adapter NO emite template alguno
// (`${VAR}`/`{env:VAR}`) y `ir.KeyEnvRef` NO se emite ni es obligatorio
// (vacío NO es error) — desviación vs. invariante I1 de 016-002/003.
//
// Shape de salida: top-level `providers` (NO "models", NO "mode") con el
// provider bajo `providers.mofgw.baseUrl` apuntando al IR y `api == "openai"`;
// los modelos se listan como un ARRAY de objetos {id, name?, contextWindow?,
// maxTokens?} derivados exclusivamente del IR (NUNCA ids extraños). El output
// usa un mapa con claves estables que encoding/json ordena alfabéticamente,
// por lo que el resultado es determinista byte a byte (postcondición P11).
package zot

import (
	"encoding/json"
	"fmt"

	"github.com/ofapsaas/mofgw/internal/clientconfig"
)

const (
	// providerKey es el key del mapa de providers (ref = `mofgw/<id>`).
	providerKey = "mofgw"
	// apiOpenAI es el adapter para endpoints OpenAI-compatibles.
	apiOpenAI = "openai"
	// limitKeyContext y limitKeyOutput son claves primarias sobre las que se
	// resuelve el límite del modelo; cada una tiene un fallback alternativo.
	limitKeyContext   = "context_length"
	limitKeyContextF  = "max_context_length"
	limitKeyOutput    = "max_output_tokens"
	limitKeyOutputF   = "max_completion_tokens"
	nameKey           = "display_name"
)

// Renderer serializa un ConfigIR al fragmento config del provider zot.
type Renderer struct{}

// Render emite el objeto JSON `{"providers": {"mofgw": {...}}}` a partir de ir.
// Devuelve error no-nil solo si ir.BaseURL está vacío (nada de fragmento
// incompleto silencioso, P9). Desviación I1: KeyEnvRef no interviene.
func (Renderer) Render(ir clientconfig.ConfigIR) ([]byte, error) {
	if ir.BaseURL == "" {
		return nil, fmt.Errorf("zot: BaseURL es obligatorio (BaseURL=%q)", ir.BaseURL)
	}

	models := make([]any, 0, len(ir.Models))
	for _, m := range ir.Models {
		models = append(models, modelFragment(m))
	}

	mofgw := map[string]any{
		"baseUrl": ir.BaseURL,
		"api":     apiOpenAI,
		"models":  models,
	}

	return json.Marshal(map[string]any{
		"providers": map[string]any{
			providerKey: mofgw,
		},
	})
}

// modelFragment construye la entrada `models[]` a partir de una ModelEntry.
// Emite "contextWindow"/"maxTokens" solo si la metadata resuelta está presente
// con valor > 0 (fallback-rule: ausente ≠ 0/[]/{}, P5/P8; un valor presente
// pero <= 0 se trata como ausente, P12), y "name" solo si Meta["display_name"]
// aporta un string no vacío. Un modelo sin límites aporta un objeto con "id"
// únicamente.
func modelFragment(m clientconfig.ModelEntry) map[string]any {
	frag := map[string]any{"id": m.ID}

	if name, ok := metaString(m.Meta, nameKey); ok && name != "" {
		frag["name"] = name
	}

	var hasContext, hasOutput bool
	var contextVal, outputVal int64

	if v, ok := metaInt(m.Meta, limitKeyContext); ok && v > 0 {
		hasContext = true
		contextVal = v
	} else if v, ok := metaInt(m.Meta, limitKeyContextF); ok && v > 0 {
		hasContext = true
		contextVal = v
	}

	if v, ok := metaInt(m.Meta, limitKeyOutput); ok && v > 0 {
		hasOutput = true
		outputVal = v
	} else if v, ok := metaInt(m.Meta, limitKeyOutputF); ok && v > 0 {
		hasOutput = true
		outputVal = v
	}

	if hasContext {
		frag["contextWindow"] = contextVal
	}
	if hasOutput {
		frag["maxTokens"] = outputVal
	}

	return frag
}

// metaString devuelve el valor string de m[key] si existe y es string.
func metaString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// metaInt devuelve el valor numérico de m[key] si existe y es numérico.
// Acepta float64 (json) e int64/int (en-memoria) por robustez.
func metaInt(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}