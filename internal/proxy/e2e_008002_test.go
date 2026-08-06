// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 008-002-budget-por-cliente: límite de
// tokens/costo por cliente con rechazo 429. Verifican postcondiciones
// P1-P5 del spec docs/specs/008-002-budget-por-cliente/spec.md.
//
// Contrato observable: 429 rate_limit_exceeded cuando el cliente supera
// su budget; 200 normal antes del límite y para clientes sin budget.
// Cero red externa.

package proxy_test

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/proxy"
)

// upstreamUsageOK da prompt 1200/completion 80/cached 1000 por request →
// con pricing 1.0, costo 0.00128 por request.

// TestRED_BudgetCosto: cliente con cost_usd_max 0.01 → tras 8 requests
// (8 × 0.00128 = 0.01024 ≥ 0.01) → el request 9 da 429 (el chequeo ve el
// acumulado de los 8 previos); antes → 200 (C1).
func TestRED_BudgetCosto(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
	})
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"client-k1": {CostUSDMax: 0.01},
	})

	// requests 1-8: costo acumulado 0.00128..0.01024 — el chequeo del
	// request N ve los N-1 previos. El request 8 ve 0.00896 < 0.01 → 200.
	for i := 0; i < 8; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200 (costo acumulado bajo)", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	// request 9: ve el acumulado 0.01024 ≥ 0.01 → 429
	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 429 {
		t.Fatalf("request 9: status = %d, want 429 (budget de costo excedido)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var errBody map[string]any
	_ = json.Unmarshal(body, &errBody)
	errObj, _ := errBody["error"].(map[string]any)
	if errObj["type"] != "rate_limit_exceeded" {
		t.Fatalf("error.type = %v, want rate_limit_exceeded: %s", errObj["type"], body)
	}
}

// TestRED_BudgetTokens: cliente con tokens_max 3000 → tras 3 requests
// (3 × 1280 = 3840 ≥ 3000) → el request 4 da 429; antes → 200 (C2).
func TestRED_BudgetTokens(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"client-k1": {TokensMax: 3000},
	})

	for i := 0; i < 3; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	// request 4: ve el acumulado 3840 ≥ 3000 → 429
	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 429 {
		t.Fatalf("request 4: status = %d, want 429 (budget de tokens excedido)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
}

// TestRED_SinBudgetNunca429: cliente sin budget + consumo alto → 200
// siempre (C3).
func TestRED_SinBudgetNunca429(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{}) // vacío

	for i := 0; i < 10; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200 (sin budget)", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
}

// TestRED_BudgetAislamiento: cliente con budget agotado → 429; otro sin
// budget → 200 (C4).
func TestRED_BudgetAislamiento(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1", "k2")
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
	})
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"client-k1": {CostUSDMax: 0.01},
	})

	// k1: 8 requests → agota budget
	for i := 0; i < 8; i++ {
		resp := h.chatAs(t, "k1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("k1 request %d: status = %d, want 200", i, resp.StatusCode)
		}
	}
	// k1 request 9 → 429
	resp := h.chatAs(t, "k1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 429 {
		t.Fatalf("k1: status = %d, want 429", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	// k2 sin budget → 200
	resp2 := h.chatAs(t, "k2", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp2.StatusCode != 200 {
		t.Fatalf("k2: status = %d, want 200 (sin budget)", resp2.StatusCode)
	}
	io.Copy(io.Discard, resp2.Body)
}
