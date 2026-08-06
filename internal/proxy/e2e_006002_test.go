// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 006-002-costos: tabla de precios configurable +
// cálculo de costo estimado por request, acumulado por cliente. Verifican
// postcondiciones P1-P5 del spec docs/specs/006-002-costos/spec.md.
//
// Contrato observable: mofgw_cost_usd_total + mofgw_cost_usd{client,...}
// en /metrics. Cero red externa: providers fake en httptest.

package proxy_test

import (
	"io"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/proxy"
)

// upstreamCostOK responde 200 con usage para un cálculo de costo exacto:
// prompt 2_000_000 (1M miss + 1M cached), completion 500_000.
func upstreamCostOK(model string) *upstream {
	return &upstream{
		status: 200,
		model:  model,
		body:   `{"id":"chatcmpl-cost","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2000000,"completion_tokens":500000,"total_tokens":2500000,"prompt_tokens_details":{"cached_tokens":1000000}}}`,
	}
}

// buildPricing igual que buildMultiClient pero expone el proxy.Server
// para configurar la tabla de precios (006-002).
type pricingHarness struct {
	*harness
	proxy *proxy.Server
}

func buildPricing(t *testing.T, ups []*upstream, clientKeys ...string) *pricingHarness {
	t.Helper()
	h := buildMultiClient(t, ups, clientKeys...)
	return &pricingHarness{harness: h, proxy: h.proxySrv}
}

// TestRED_CostoExacto: con pricing m (0.14/0.28/0.028), request con
// 1M miss + 1M cached + 500K completion → costo = 0.14 + 0.14 + 0.028
// = 0.308 (C1).
func TestRED_CostoExacto(t *testing.T) {
	ups := upstreamCostOK("m")
	h := buildPricing(t, []*upstream{ups}, "k1")
	h.proxy.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 0.14, OutputUSDPerM: 0.28, CacheHitUSDPerM: 0.028},
	})

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_cost_usd_total 0.308") {
		t.Fatalf("cost_usd_total mal:\n%s", s)
	}
}

// TestRED_CostoSinPricingCero: modelo sin entrada en pricing → costo 0,
// sin error (C2).
func TestRED_CostoSinPricingCero(t *testing.T) {
	ups := upstreamCostOK("m")
	h := buildPricing(t, []*upstream{ups}, "k1")
	// sin SetPricing → sin tabla

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (sin pricing no debe fallar)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_cost_usd_total 0") {
		t.Fatalf("sin pricing debe dar 0:\n%s", s)
	}
}

// TestRED_CostoAcumulaPorCliente: 2 requests del mismo cliente →
// costo doble, desagregado por cliente presente (C3).
func TestRED_CostoAcumulaPorCliente(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, cached 1000 → miss 200
	h := buildPricing(t, []*upstream{ups}, "k1")
	h.proxy.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
	})

	for i := 0; i < 2; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	s := h.metricsBody(t)
	// por request: miss 200/1M = 0.0002; completion 80/1M = 0.00008;
	// cached 1000/1M = 0.001 → 0.00128; ×2 = 0.00256
	if !strings.Contains(s, "mofgw_cost_usd_total 0.00256") {
		t.Fatalf("cost_usd_total (2 req) mal:\n%s", s)
	}
	if !strings.Contains(s, `client="client-k1"`) {
		t.Fatalf("sin desagregado por cliente:\n%s", s)
	}
}

// TestRED_CostoSinUsageCero: request sin usage → costo 0 (C4).
func TestRED_CostoSinUsageCero(t *testing.T) {
	ups := upstreamNoUsage("m", "hola")
	h := buildPricing(t, []*upstream{ups}, "k1")
	h.proxy.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
	})

	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	s := h.metricsBody(t)
	if !strings.Contains(s, "mofgw_cost_usd_total 0") {
		t.Fatalf("sin usage debe dar 0:\n%s", s)
	}
}
