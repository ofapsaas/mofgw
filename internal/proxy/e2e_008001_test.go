// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 008-001-rechazo-por-ventana: rechazo temprano
// (400) de requests cuyo prompt estimado excede la ventana de contexto
// del modelo. Verifican postcondiciones P1-P5 del spec
// docs/specs/008-001-rechazo-por-ventana/spec.md.
//
// Contrato observable: 400 OpenAI-compatible sin intentos upstream para
// prompts que exceden context_window (con margen); 200 normal para los
// que caben. Cero red externa.

package proxy_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
)

// bigBody arma un body de chat con un mensaje de n chars.
func bigBody(n int) map[string]any {
	return map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": strings.Repeat("x", n)}},
	}
}

// TestRED_RechazoPorVentana: modelo con context_window 1000, margin 0 →
// body de ~5000 chars (estimado ~1250 tokens > 1000) → 400, cero
// intentos al upstream (C1).
func TestRED_RechazoPorVentana(t *testing.T) {
	ups := upstreamOK("m", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	resp := h.chatAs(t, "k1", bigBody(5000))
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var errBody map[string]any
	_ = json.Unmarshal(body, &errBody)
	errObj, _ := errBody["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Fatalf("error.type = %v, want invalid_request_error: %s", errObj["type"], body)
	}
	if !strings.Contains(strings.ToLower(errObj["message"].(string)), "context") {
		t.Fatalf("mensaje sin 'context': %s", errObj["message"])
	}
	// CERO intentos al upstream (el body del upstream fake quedó vacío)
	if ups.gotBody != "" {
		t.Fatalf("hubo intento upstream tras rechazo por ventana: %s", ups.gotBody)
	}
}

// TestRED_RequestQueCabe: mismo modelo, body de ~2000 chars (estimado
// ~500 tokens ≤ 1000) → 200, un intento al upstream (C2).
func TestRED_RequestQueCabe(t *testing.T) {
	ups := upstreamOK("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	resp := h.chatAs(t, "k1", bigBody(2000))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if ups.gotBody == "" {
		t.Fatal("request que cabe no llegó al upstream")
	}
}

// TestRED_SinMetadataSinRechazo: modelo sin metadata + body grande → 200
// (backward compatible, C3).
func TestRED_SinMetadataSinRechazo(t *testing.T) {
	ups := upstreamOK("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	// sin SetModelMetadata

	resp := h.chatAs(t, "k1", bigBody(5000))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (sin metadata no hay rechazo)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if ups.gotBody == "" {
		t.Fatal("sin metadata el request debe llegar al upstream")
	}
}

// TestRED_MaxTokensCuentaEnVentana: prompt que cabe pero prompt+max_tokens
// excede la ventana → 400 sin intentos upstream (cierra el gap del 502
// upstream 04:49: prompt 1,016,660 + max_tokens 32,000 = 1,048,660 >
// límite real 1,048,576; el chequeo viejo solo miraba el prompt).
func TestRED_MaxTokensCuentaEnVentana(t *testing.T) {
	ups := upstreamOK("m", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	// window 1000, margin 0 → límite exacto 1000
	h.proxySrv.SetContextMargin(0)
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	body := bigBody(3000) // estimado ~750 tokens ≤ 1000 → el prompt cabe
	body["max_tokens"] = 400
	resp := h.chatAs(t, "k1", body)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (750+400 > 1000)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if ups.gotBody != "" {
		t.Fatalf("hubo intento upstream tras rechazo por ventana con max_tokens: %s", ups.gotBody)
	}
}

// TestRED_MaxTokensQueCabe: prompt + max_tokens dentro de la ventana → 200.
func TestRED_MaxTokensQueCabe(t *testing.T) {
	ups := upstreamOK("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetContextMargin(0)
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	body := bigBody(3000) // estimado ~750 tokens
	body["max_tokens"] = 200 // 750+200 = 950 ≤ 1000 → cabe
	resp := h.chatAs(t, "k1", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (750+200 ≤ 1000)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if ups.gotBody == "" {
		t.Fatal("request que cabe no llegó al upstream")
	}
}

// TestRED_MargenRespetado: margin 0.5 y context_window 1000 → request
// estimado 1250 tokens ≤ 1000×1.5=1500 → 200 (C4).
func TestRED_MargenRespetado(t *testing.T) {
	ups := upstreamOK("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	// margin global 0.5 (por config — FIXME: la feature expone el margen
	// vía SetContextMargin o el Server lo lee de config; el test define
	// el contrato)
	h.proxySrv.SetContextMargin(0.5)
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	// 5000 chars → estimado 1250 tokens; 1000 × 1.5 = 1500 → cabe
	resp := h.chatAs(t, "k1", bigBody(5000))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (margen 0.5 cubre 1250 ≤ 1500)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
}
