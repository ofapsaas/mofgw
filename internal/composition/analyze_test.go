// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-001-context-composition — P1 (C1-C4).
//
// API definida por el test-writer contra el contrato P1 del spec. El
// implementer DEBE respetar esta firma (el Walk existente queda intacto;
// Analyze es el superset en UN solo recorrido — review.md Optional #1):
//
//	package composition
//
//	type WalkResult struct {
//		KeyPaths   []KeyPath    // igual que Walk (dedup, orden estable)
//		Detected   []DetectedID // igual que Walk (safe fields, I3)
//		Roles      []string     // rol de cada mensaje, en orden
//		PartTypes  []PartType   // composición por porción (ver PartType)
//		NumTools   int          // cantidad total de entries de tool_calls
//		TotalBytes int          // len(body)
//	}
//
//	type PartType struct {
//		Role      string // system|user|assistant|tool|developer|other
//		Type      string // text|image|tool_result|tool_use|thinking|audio|other
//		Bytes     int    // longitud cruda de la porción (valor JSON, sin wrapper)
//		EstTokens int    // Bytes/4 (división entera, patrón estimatePromptTokens)
//	}
//
//	func Analyze(body []byte, maxDepth int) (*WalkResult, error)
//
// Reglas de mapeo de Type (P1):
//   - content string → `text` (el string como porción).
//   - content array → una porción por elemento: `text`, `image_url` → `image`,
//     `input_audio` → `audio`, `tool_use` → `tool_use`, `tool_result` →
//     `tool_result`, `thinking`/`redacted_thinking` → `thinking`, otro → `other`.
//   - message con `tool_calls` no vacío → una porción `tool_use` por entry
//     (porción = el JSON crudo de `arguments`); `content` null no aporta porción.
//   - message con `role: tool` → porción `tool_result` (content del mensaje).
//   - campo `reasoning_content` no vacío → porción `thinking`.
//   - `maxDepth` acota la recursión (default 10; exceder trunca sin error ni
//     panic, I4 de 009-000).
//   - El resultado NUNCA incluye contenido: valores de content/text/description/
//     input/arguments jamás se reportan (solo tamaños y tipos).
package composition_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/composition"
)

// mustMarshal serializa v (falla el test si no es serializable). Se usa
// para computar el "JSON crudo" de un valor de arguments en el test.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%v): %v", v, err)
	}
	return raw
}

// findPartType devuelve la primera porción con (role, type) dados.
func findPartType(parts []composition.PartType, role, typ string) (composition.PartType, bool) {
	for _, p := range parts {
		if p.Role == role && p.Type == typ {
			return p, true
		}
	}
	return composition.PartType{}, false
}

// test_postcondition_1_StructuralComposition (C1): body con content string +
// content array (text/image) + tool_calls + message role=tool +
// reasoning_content → roles en orden exacto, part types correctos,
// EstTokens = Bytes/4 exacto por porción y NumTools correcto.
func TestAnalyze_StructuralComposition(t *testing.T) {
	body := []byte(`{
  "model": "m",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "hi there"},
    {"role": "user", "content": [
      {"type": "text", "text": "what is in this image?"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}
    ]},
    {"role": "assistant", "content": null, "tool_calls": [
      {"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\": \"Paris\"}"}},
      {"id": "call_2", "type": "function", "function": {"name": "get_time", "arguments": "{\"tz\": \"UTC\"}"}}
    ]},
    {"role": "tool", "tool_call_id": "call_1", "content": "sunny, 25C"},
    {"role": "assistant", "content": "ok", "reasoning_content": "let me think..."}
  ]
}`)

	res, err := composition.Analyze(body, 10)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Roles en orden exacto de mensajes (P1).
	wantRoles := []string{"system", "user", "user", "assistant", "tool", "assistant"}
	if !reflect.DeepEqual(res.Roles, wantRoles) {
		t.Fatalf("Roles = %v, want %v (orden de messages)", res.Roles, wantRoles)
	}

	// NumTools = entries de tool_calls.
	if res.NumTools != 2 {
		t.Fatalf("NumTools = %d, want 2", res.NumTools)
	}

	// TotalBytes = len(body).
	if res.TotalBytes != len(body) {
		t.Fatalf("TotalBytes = %d, want %d", res.TotalBytes, len(body))
	}

	// La porción tool_use es el JSON crudo de `arguments` (con wrapper).
	rawArgs1 := mustMarshal(t, `{"city": "Paris"}`)
	rawArgs2 := mustMarshal(t, `{"tz": "UTC"}`)

	// Las primeras 7 porciones tienen orden DETERMINADO por el documento.
	wantFirst7 := []composition.PartType{
		{Role: "system", Type: "text", Bytes: 28, EstTokens: 7}, // "You are a helpful assistant."
		{Role: "user", Type: "text", Bytes: 8, EstTokens: 2},    // "hi there"
		{Role: "user", Type: "text", Bytes: 22, EstTokens: 5},   // "what is in this image?"
		{Role: "user", Type: "image", Bytes: 26, EstTokens: 6},  // "data:image/png;base64,AAAA"
		{Role: "assistant", Type: "tool_use", Bytes: len(rawArgs1), EstTokens: len(rawArgs1) / 4},
		{Role: "assistant", Type: "tool_use", Bytes: len(rawArgs2), EstTokens: len(rawArgs2) / 4},
		{Role: "tool", Type: "tool_result", Bytes: 10, EstTokens: 2}, // "sunny, 25C"
	}
	if len(res.PartTypes) != 9 {
		t.Fatalf("PartTypes len = %d, want 9: %+v", len(res.PartTypes), res.PartTypes)
	}
	for i, want := range wantFirst7 {
		if res.PartTypes[i] != want {
			t.Fatalf("PartTypes[%d] = %+v, want %+v", i, res.PartTypes[i], want)
		}
	}

	// Las 2 últimas porciones son del mensaje con content + reasoning_content:
	// un `text` (content) y un `thinking` (reasoning_content). El spec no fija
	// su orden relativo → se verifican por presencia con sus tamaños exactos.
	seenText, seenThinking := false, false
	for _, p := range res.PartTypes[7:] {
		if p.Role != "assistant" {
			t.Fatalf("porción del último mensaje con role %q, want assistant: %+v", p.Role, p)
		}
		switch {
		case p.Type == "text" && p.Bytes == 2 && p.EstTokens == 0: // "ok"
			seenText = true
		case p.Type == "thinking" && p.Bytes == 15 && p.EstTokens == 3: // "let me think..."
			seenThinking = true
		}
	}
	if !seenText {
		t.Fatalf("falta porción text del content en el último mensaje: %+v", res.PartTypes[7:])
	}
	if !seenThinking {
		t.Fatalf("falta porción thinking del reasoning_content en el último mensaje: %+v", res.PartTypes[7:])
	}

	// Invariante global: EstTokens = Bytes/4 (división entera) por porción.
	for _, p := range res.PartTypes {
		if p.EstTokens != p.Bytes/4 {
			t.Fatalf("EstTokens = %d, want Bytes/4 = %d para %+v", p.EstTokens, p.Bytes/4, p)
		}
	}
}

// test_postcondition_1_PrivacyNoContent (C2/P1/P7): strings distintivos
// largos en content/arguments/description/reasoning_content NUNCA aparecen
// como valor en el output de Analyze (solo tamaños).
func TestAnalyze_PrivacyNoContent(t *testing.T) {
	secrets := []string{
		"PRIVACY-CONTENT-LONG-STRING-0001-abcdefghijklmnop",
		"PRIVACY-ARGS-LONG-STRING-0002-abcdefghijklmnop",
		"PRIVACY-TOOL-RESULT-LONG-0003",
		"PRIVACY-REASONING-LONG-0004",
		"PRIVACY-DESC-LONG-STRING-0005",
	}
	body := []byte(`{
  "model": "m",
  "messages": [
    {"role": "user", "content": "PRIVACY-CONTENT-LONG-STRING-0001-abcdefghijklmnop"},
    {"role": "assistant", "content": null, "tool_calls": [{"function": {"name": "f", "arguments": "PRIVACY-ARGS-LONG-STRING-0002-abcdefghijklmnop"}}]},
    {"role": "tool", "tool_call_id": "call_1", "content": "PRIVACY-TOOL-RESULT-LONG-0003"},
    {"role": "assistant", "content": "ok", "reasoning_content": "PRIVACY-REASONING-LONG-0004"}
  ],
  "tools": [{"type": "function", "function": {"name": "f", "description": "PRIVACY-DESC-LONG-STRING-0005"}}],
  "metadata": {"session_id": "ses_OK"}
}`)

	res, err := composition.Analyze(body, 10)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// El output serializado NO contiene ningún valor distintivo (C2/I1).
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(res): %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(out), secret) {
			t.Fatalf("PRIVACIDAD: %q aparece en el output de Analyze (C2/I1):\n%s", secret, out)
		}
	}

	// SOLO tamaños: cada porción reporta su Bytes (y EstTokens) del secreto.
	wantSizes := []struct {
		role, typ string
		value     string
	}{
		{"user", "text", secrets[0]},
		{"assistant", "tool_use", string(mustMarshal(t, secrets[1]))}, // JSON crudo de arguments
		{"tool", "tool_result", secrets[2]},
		{"assistant", "thinking", secrets[3]},
	}
	for _, w := range wantSizes {
		p, ok := findPartType(res.PartTypes, w.role, w.typ)
		if !ok {
			t.Fatalf("falta porción (%s, %s): %+v", w.role, w.typ, res.PartTypes)
		}
		if p.Bytes != len(w.value) {
			t.Fatalf("porción (%s, %s): Bytes = %d, want len(%q) = %d", w.role, w.typ, p.Bytes, w.value, len(w.value))
		}
		if p.EstTokens != len(w.value)/4 {
			t.Fatalf("porción (%s, %s): EstTokens = %d, want %d", w.role, w.typ, p.EstTokens, len(w.value)/4)
		}
	}
}

// test_postcondition_1_BackwardCompatWithWalk (C3): para el mismo body,
// Analyze(...).KeyPaths/.Detected == Walk(...) (refactor sin cambio de
// schema — audit 009-000 R1).
func TestAnalyze_BackwardCompatWithWalk(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","name":"agent:h","tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}],"metadata":{"session_id":"ses_1","thread_id":"msg_t1"}}`)

	kp, det, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	res, err := composition.Analyze(body, 10)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !reflect.DeepEqual(res.KeyPaths, kp) {
		t.Fatalf("KeyPaths divergen de Walk (C3):\nAnalyze: %v\nWalk:    %v", res.KeyPaths, kp)
	}
	if !reflect.DeepEqual(res.Detected, det) {
		t.Fatalf("Detected divergen de Walk (C3):\nAnalyze: %v\nWalk:    %v", res.Detected, det)
	}
}

// test_postcondition_1_MaxDepthTruncates (C4): body con anidamiento >
// maxDepth → trunca sin error ni panic (igual C8 009-000/I4).
func TestAnalyze_MaxDepthTruncates(t *testing.T) {
	deep := map[string]any{"leaf": "v"}
	for i := 0; i < 14; i++ {
		deep = map[string]any{"next": deep}
	}
	raw, err := json.Marshal(deep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := composition.Analyze(raw, 10)
	if err != nil {
		t.Fatalf("Analyze: %v (debe truncar sin error — C4/I4)", err)
	}
	if len(res.KeyPaths) == 0 {
		t.Fatal("Analyze sin key paths")
	}
	for _, p := range res.KeyPaths {
		segments := strings.Count(string(p), ".") + 1
		if segments > 11 {
			t.Fatalf("path %q excede maxDepth=10 (%d segmentos) — debe truncar (I4)", p, segments)
		}
	}
}
