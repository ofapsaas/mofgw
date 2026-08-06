// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 007-002-models-endpoint: /v1/models enriquecido
// con capabilities (metadata 007-001 + pricing 006-002). Verifican
// postcondiciones P1-P5 del spec docs/specs/007-002-models-endpoint/spec.md.
//
// Contrato observable: GET /v1/models con auth → objeto JSON por modelo
// con los campos enriquecidos cuando hay metadata/pricing. Cero red
// externa.

package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/proxy"
)

// modelsBody hace GET /v1/models con auth y parsea el body.
func modelsBody(t *testing.T, h *harness) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("models status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body no es JSON: %v", err)
	}
	return out
}

// findModel busca el objeto del modelo en data[].
func findModel(t *testing.T, out map[string]any, id string) map[string]any {
	t.Helper()
	if out["object"] != "list" {
		t.Fatalf("envelope object = %v, want list", out["object"])
	}
	data, ok := out["data"].([]any)
	if !ok {
		t.Fatalf("data no es array: %T", out["data"])
	}
	for _, raw := range data {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["id"] == id {
			return item
		}
	}
	t.Fatalf("modelo %q no encontrado en data", id)
	return nil
}

// TestRED_ModelsEnriquecido: modelo con metadata → campos de contexto y
// capabilities presentes (C1).
func TestRED_ModelsEnriquecido(t *testing.T) {
	ups := upstreamOK("deepseek-v4-flash", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"deepseek-v4-flash": {
			ContextWindow:   1000000,
			MaxOutput:       384000,
			Thinking:        []string{"low", "high", "max"},
			ThinkingDefault: "high",
		},
	})

	item := findModel(t, modelsBody(t, h), "deepseek-v4-flash")
	if item["context_length"] != float64(1000000) {
		t.Fatalf("context_length = %v, want 1000000", item["context_length"])
	}
	if item["max_context_length"] != float64(1000000) {
		t.Fatalf("max_context_length = %v", item["max_context_length"])
	}
	if item["loaded_context_length"] != float64(1000000) {
		t.Fatalf("loaded_context_length = %v", item["loaded_context_length"])
	}
	if item["max_completion_tokens"] != float64(384000) {
		t.Fatalf("max_completion_tokens = %v", item["max_completion_tokens"])
	}
	caps, ok := item["capabilities"].(map[string]any)
	if !ok || caps["reasoning"] != true {
		t.Fatalf("capabilities.reasoning = %v, want true", item["capabilities"])
	}
	think, ok := item["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("sin thinking object: %v", item)
	}
	levels, _ := think["levels"].([]any)
	if len(levels) != 3 || levels[0] != "low" || levels[2] != "max" {
		t.Fatalf("thinking.levels = %v", think["levels"])
	}
	if think["default"] != "high" {
		t.Fatalf("thinking.default = %v, want high", think["default"])
	}
}

// TestRED_ModelsSinMetadataMinimo: modelo sin metadata → formato mínimo,
// sin campos extra (C2).
func TestRED_ModelsSinMetadataMinimo(t *testing.T) {
	ups := upstreamOK("plain-model", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	item := findModel(t, modelsBody(t, h), "plain-model")
	if _, ok := item["context_length"]; ok {
		t.Fatalf("sin metadata no debe haber context_length: %v", item)
	}
	if _, ok := item["capabilities"]; ok {
		t.Fatalf("sin metadata no debe haber capabilities: %v", item)
	}
	if item["object"] != "model" {
		t.Fatalf("object = %v, want model", item["object"])
	}
}

// TestRED_ModelsPricing: modelo con pricing → pricing presente; sin → ausente (C3).
func TestRED_ModelsPricing(t *testing.T) {
	ups1 := upstreamOK("priced", "x")
	ups2 := upstreamOK("unpriced", "x")
	h := buildMultiClient(t, []*upstream{ups1, ups2}, "k1")
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"priced": {InputUSDPerM: 0.14, OutputUSDPerM: 0.28, CacheHitUSDPerM: 0.028},
	})

	item := findModel(t, modelsBody(t, h), "priced")
	p, ok := item["pricing"].(map[string]any)
	if !ok {
		t.Fatalf("sin pricing object en modelo con pricing: %v", item)
	}
	if p["input_usd_per_m"] != 0.14 {
		t.Fatalf("pricing.input = %v", p["input_usd_per_m"])
	}
	if p["output_usd_per_m"] != 0.28 {
		t.Fatalf("pricing.output = %v", p["output_usd_per_m"])
	}
	if p["cache_hit_usd_per_m"] != 0.028 {
		t.Fatalf("pricing.cache_hit = %v", p["cache_hit_usd_per_m"])
	}

	item2 := findModel(t, modelsBody(t, h), "unpriced")
	if _, ok := item2["pricing"]; ok {
		t.Fatalf("sin pricing no debe haber pricing object: %v", item2)
	}
}

// TestRED_ModelsThinkingAlways: kimi con thinking ["always"] →
// capabilities.reasoning true + levels ["always"] (C4).
func TestRED_ModelsThinkingAlways(t *testing.T) {
	ups := upstreamOK("kimi-k2.7-code", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"kimi-k2.7-code": {
			ContextWindow:   262144,
			MaxOutput:       32768,
			Thinking:        []string{"always"},
			ThinkingDefault: "always",
		},
	})

	item := findModel(t, modelsBody(t, h), "kimi-k2.7-code")
	caps, ok := item["capabilities"].(map[string]any)
	if !ok || caps["reasoning"] != true {
		t.Fatalf("kimi capabilities.reasoning = %v, want true", item["capabilities"])
	}
	think, ok := item["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("kimi sin thinking object")
	}
	levels, _ := think["levels"].([]any)
	if len(levels) != 1 || levels[0] != "always" {
		t.Fatalf("kimi thinking.levels = %v, want [always]", think["levels"])
	}
}
