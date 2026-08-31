// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package opencode implementa el adapter concreto del epic mofgw-client-config
// (feature 016-002-adapter-opencode): un clientconfig.Renderer que serializa un
// clientconfig.ConfigIR en el fragmento JSON del provider "mofgw" listo para
// insertar bajo la clave `provider` de opencode.json.
//
// Es serialización pura (entrada ConfigIR, salida []byte JSON válido): apunta
// options.baseURL al IR, referencia la API key por env-var (template STRING
// "{env:<KeyEnvRef>}", jamás el valor del secret — invariante I1) y lista los
// modelos derivados exclusivamente del IR (invariante I2). El output usa un
// mapa con claves estables que encoding/json ordena alfabéticamente, por lo
// que el resultado es determinista byte a byte (postcondición P11).
package opencode

import (
	"encoding/json"
	"fmt"

	"github.com/ofapsaas/mofgw/internal/clientconfig"
)

const (
	npmName = "@ai-sdk/openai-compatible"
	// limitKeyContext y limitKeyOutput son claves primarias sobre las que se
	// resuelve el límite del modelo; cada una tiene un fallback alternativo.
	limitKeyContext  = "context_length"
	limitKeyContextF = "max_context_length"
	limitKeyOutput   = "max_output_tokens"
	limitKeyOutputF  = "max_completion_tokens"
)

// Renderer serializa un ConfigIR al fragmento JSON del provider opencode.
type Renderer struct{}

// Render emite el objeto JSON `{"mofgw": {...}}` a partir de ir. Devuelve
// error no-nil si ir.BaseURL o ir.KeyEnvRef están vacíos (nada de fragmento
// incompleto silencioso, P9).
func (Renderer) Render(ir clientconfig.ConfigIR) ([]byte, error) {
	if ir.BaseURL == "" || ir.KeyEnvRef == "" {
		return nil, fmt.Errorf("opencode: BaseURL y KeyEnvRef son obligatorios (BaseURL=%q KeyEnvRef=%q)", ir.BaseURL, ir.KeyEnvRef)
	}

	models := make(map[string]any, len(ir.Models))
	for _, m := range ir.Models {
		models[m.ID] = modelFragment(m)
	}

	mofgw := map[string]any{
		"npm":     npmName,
		"name":    "mofgw",
		"options": map[string]any{
			"baseURL": ir.BaseURL,
			"apiKey":  "{env:" + ir.KeyEnvRef + "}",
		},
		"models": models,
	}

	return json.Marshal(map[string]any{"mofgw": mofgw})
}

// modelFragment construye la entrada `models.<id>` a partir de una ModelEntry.
// Emite "limit" solo si al menos uno de context/output tiene metadata presentes
// con valor > 0 (fallback-rule: ausente ≠ 0/[]/{}, P5/P8; un valor presente
// pero <= 0 se trata como ausente), y "name" solo si Meta["display_name"]
// aporta un string no vacío.
func modelFragment(m clientconfig.ModelEntry) map[string]any {
	frag := map[string]any{}

	if name, ok := metaString(m.Meta, "display_name"); ok && name != "" {
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

	if hasContext || hasOutput {
		limit := map[string]any{}
		if hasContext {
			limit["context"] = contextVal
		}
		if hasOutput {
			limit["output"] = outputVal
		}
		frag["limit"] = limit
	}

	// cost: pricing.*_usd_per_m → cost.{input,output,cache_read} (solo si >0; Meta trae pricing anidado o flat)
	if cost := buildCost(m.Meta); cost != nil {
		frag["cost"] = cost
	}
	if hasToolCall(m.Meta) {
		frag["tool_call"] = true
	}
	if hasReasoning(m.Meta) {
		frag["reasoning"] = true
	}

	return frag
}

func buildCost(meta map[string]any) map[string]any {
	// soporta Meta["pricing"] como map y también claves flat "input_usd_per_m" etc.
	var p map[string]any
	if v, ok := meta["pricing"]; ok {
		if pm, ok := v.(map[string]any); ok {
			p = pm
		}
	}
	cost := map[string]any{}
	if v, ok := metaFloat(p, "input_usd_per_m"); ok && v > 0 {
		cost["input"] = v
	} else if v, ok := metaFloat(meta, "input_usd_per_m"); ok && v > 0 {
		cost["input"] = v
	}
	if v, ok := metaFloat(p, "output_usd_per_m"); ok && v > 0 {
		cost["output"] = v
	} else if v, ok := metaFloat(meta, "output_usd_per_m"); ok && v > 0 {
		cost["output"] = v
	}
	if v, ok := metaFloat(p, "cache_hit_usd_per_m"); ok && v > 0 {
		cost["cache_read"] = v
	} else if v, ok := metaFloat(meta, "cache_hit_usd_per_m"); ok && v > 0 {
		cost["cache_read"] = v
	}
	if len(cost) == 0 {
		return nil
	}
	return cost
}

func hasToolCall(meta map[string]any) bool {
	if v, ok := meta["supported_parameters"]; ok {
		switch s := v.(type) {
		case []any:
			for _, e := range s {
				if str, ok := e.(string); ok && str == "tools" {
					return true
				}
			}
		case []string:
			for _, e := range s {
				if e == "tools" {
					return true
				}
			}
		}
	}
	return false
}

func hasReasoning(meta map[string]any) bool {
	if v, ok := meta["thinking"]; ok {
		switch s := v.(type) {
		case []any:
			if len(s) > 0 {
				return true
			}
		case []string:
			if len(s) > 0 {
				return true
			}
		}
	}
	if v, ok := meta["capabilities"]; ok {
		if m, ok := v.(map[string]any); ok {
			if r, ok := m["reasoning"]; ok {
				if b, ok := r.(bool); ok && b {
					return true
				}
			}
		}
	}
	return false
}

func metaFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
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