// Package registry — RED 020-001-registry-cost-model (etapa 3.1), nivel writer.
//
// B7 del test-audit aprobado (P6): compatibilidad de lectura con líneas
// JSONL históricas + emisión de los campos nuevos a nivel writer.
package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPostcondition6_LineaHistoricaParsea verifica P6: una línea JSONL
// histórica (esquema 014-001, sin los campos nuevos) parsea con
// `model=""`, `cost_usd_src=""`, `cost_usd_up=null`.
func TestPostcondition6_LineaHistoricaParsea(t *testing.T) {
	// Línea histórica literal (audit §6): esquema 014-001 sin model,
	// cost_usd_src ni cost_usd_up.
	const historical = `{"type":"terminal","request_id":"r1","ts":"2026-08-18T10:00:00.000Z","client":"c","outcome":"success","error_code":"","status":200,"final_provider":"up1","tokens":{"prompt":200,"completion":80,"cache":1000,"reasoning":0},"cost_usd":0.00128,"stream":false}`

	var ev TerminalEvent
	if err := json.Unmarshal([]byte(historical), &ev); err != nil {
		t.Fatalf("línea histórica no parsea (P6): %v", err)
	}
	if ev.Model != "" {
		t.Fatalf("Model = %q, want %q para línea histórica (P6)", ev.Model, "")
	}
	if ev.CostUSDSrc != "" {
		t.Fatalf("CostUSDSrc = %q, want %q para línea histórica (P6)", ev.CostUSDSrc, "")
	}
	if ev.CostUSDUp != nil {
		t.Fatalf("CostUSDUp = %v, want null para línea histórica (P6)", *ev.CostUSDUp)
	}
	if ev.CostUSD != 0.00128 {
		t.Fatalf("CostUSD = %v, want 0.00128 (P6: costo histórico intacto)", ev.CostUSD)
	}
}

// TestPostcondition1_2_3_WriterEmiteCamposNuevos verifica P1/P2/P3 a nivel
// writer: el Terminal escrito incluye `model`, `cost_usd_src` y
// `cost_usd_up` (incluyendo 0.0 legítimo serializado, no null).
func TestPostcondition1_2_3_WriterEmiteCamposNuevos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	upstreamCost := 0.0042
	ev := TerminalEvent{
		Type:          "terminal",
		RequestID:     "req-020001",
		Ts:            "2026-09-17T10:00:00.000Z",
		Client:        "test",
		Outcome:       "success",
		ErrorCode:     "",
		Status:        200,
		FinalProvider: "up1",
		Model:         "m",
		Tokens:        Tokens{Prompt: 200, Completion: 80, Cache: 1000, Reasoning: 0},
		CostUSD:       upstreamCost,
		CostUSDSrc:    "upstream",
		CostUSDUp:     &upstreamCost,
	}
	if err := w.Terminal(ev); err != nil {
		t.Fatalf("Terminal: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("línea no es JSON válido: %v", err)
	}
	if m["model"] != "m" {
		t.Fatalf("model = %v, want %q (P1)", m["model"], "m")
	}
	if m["cost_usd_src"] != "upstream" {
		t.Fatalf("cost_usd_src = %v, want %q (P2)", m["cost_usd_src"], "upstream")
	}
	up, ok := m["cost_usd_up"].(float64)
	if !ok || up != upstreamCost {
		t.Fatalf("cost_usd_up = %v, want %v serializado (P3, 0.0 legítimo nunca null)", m["cost_usd_up"], upstreamCost)
	}
}
