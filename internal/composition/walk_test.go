// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-000-request-telemetry — P6/P7 (greenfield).
//
// API definida por el test-writer contra el contrato P6/P7 del spec. El
// implementer DEBE respetar esta firma (paquete NUEVO internal/composition,
// archivo walk.go):
//
//	package composition
//
//	type KeyPath string // path en notación dots, e.g. "messages[].role"
//
//	type DetectedID struct {
//		Path  string `json:"path"`
//		Value string `json:"value"`
//	}
//
//	// Walk recorre un body JSON crudo ([]byte ya en memoria, sin re-parsear
//	// el request — P6) y devuelve:
//	//   - keyPaths: paths de TODAS las claves del body con notación de dots
//	//     ("model", "messages[].role", "metadata.session_id"). Orden ESTABLE:
//	//     orden de aparición en el documento (primera ocurrencia),
//	//     deduplicado ("único" del spec P4). Los arrays se anotan "name[]"
//	//     y se recorre cada elemento (los paths de hijos se deduplican entre
//	//     elementos). NUNCA incluye valores.
//	//   - detected: valores id-like (prefijos ses_, msg_, agent:) hallados
//	//     SOLO en campos seguros: metadata.*, *.session_id, *.thread_id,
//	//     role, name, tool_call_id (P7/I3). Nunca inspecciona content, text,
//	//     description, input, arguments.
//	//   - err: solo si body no es JSON válido.
//	//
//	// maxDepth acota la recursión (default 10); exceder la profundidad
//	// trunca esa rama sin error ni panic (I4).
//	func Walk(body []byte, maxDepth int) (keyPaths []KeyPath, detected []DetectedID, err error)
package composition_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/composition"
)

func containsKeyPath(paths []composition.KeyPath, want string) bool {
	for _, p := range paths {
		if string(p) == want {
			return true
		}
	}
	return false
}

func hasDetected(detected []composition.DetectedID, want composition.DetectedID) bool {
	for _, d := range detected {
		if d == want {
			return true
		}
	}
	return false
}

// test_postcondition_6_WalkPathsConDots: paths con dots y [] en orden
// EXACTO (R5) y único (sin duplicados entre elementos de array).
func TestPostcondition6_WalkPathsConDots(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","name":"agent:h","content":"x"}],"metadata":{"session_id":"ses_1","thread_id":"msg_t1"}}`)
	paths, _, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{
		"model",
		"messages[].role",
		"messages[].content",
		"messages[].name",
		"metadata.session_id",
		"metadata.thread_id",
	}
	if len(paths) != len(want) {
		t.Fatalf("keyPaths = %v, want %v (orden exacto, único)", paths, want)
	}
	for i, w := range want {
		if string(paths[i]) != w {
			t.Fatalf("keyPaths[%d] = %q, want %q (orden estable R5)", i, paths[i], w)
		}
	}
}

// test_postcondition_6_WalkMaxDepthTrunca: 15 niveles de anidamiento →
// trunca sin error ni panic (C8, I4).
func TestPostcondition6_WalkMaxDepthTrunca(t *testing.T) {
	deep := map[string]any{"leaf": "v"}
	for i := 0; i < 14; i++ {
		deep = map[string]any{"next": deep}
	}
	raw, err := json.Marshal(deep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	paths, _, err := composition.Walk(raw, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("Walk sin paths")
	}
	for _, p := range paths {
		segments := strings.Count(string(p), ".") + 1
		if segments > 11 {
			t.Fatalf("path %q excede maxDepth=10 (%d segmentos) — el walk debe truncar (I4)", p, segments)
		}
	}
}

// test_postcondition_6_WalkSinValores: el output contiene solo NOMBRES de
// paths, jamás valores (P6).
func TestPostcondition6_WalkSinValores(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"S3CRETO-CONTENIDO"}],"metadata":{"session_id":"ses_1"}}`)
	paths, _, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if !containsKeyPath(paths, "messages[].content") {
		t.Fatalf("el path del contenido debe aparecer: %v", paths)
	}
	for _, p := range paths {
		s := string(p)
		if strings.Contains(s, "S3CRETO") || strings.Contains(s, ":") || strings.Contains(s, `"`) || strings.Contains(s, " ") {
			t.Fatalf("keyPath con valor/ruido: %q", p)
		}
	}
}

// test_postcondition_7_DetectedIdsSafeFields: metadata.*, *.session_id,
// *.thread_id, role, name, tool_call_id son safe fields; valores id-like
// (ses_, msg_, agent:) se reportan como {path, value} (C6).
func TestPostcondition7_DetectedIdsSafeFields(t *testing.T) {
	body := []byte(`{"metadata":{"session_id":"ses_abc","thread_id":"msg_t1"},"messages":[{"role":"user","name":"agent:h","tool_call_id":"msg_c9","content":"hi"}]}`)
	_, detected, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []composition.DetectedID{
		{Path: "metadata.session_id", Value: "ses_abc"},
		{Path: "metadata.thread_id", Value: "msg_t1"},
		{Path: "messages[].name", Value: "agent:h"},
		{Path: "messages[].tool_call_id", Value: "msg_c9"},
	}
	for _, w := range want {
		if !hasDetected(detected, w) {
			t.Fatalf("detected_ids sin %+v: %+v", w, detected)
		}
	}
	// role es safe field pero "user" no es id-like → no detectado
	for _, d := range detected {
		if d.Path == "messages[].role" {
			t.Fatalf("role 'user' no es id-like, no debe detectarse: %+v", detected)
		}
	}
}

// test_postcondition_7_SafeFieldNoIdLike: valor en safe field que NO es
// id-like → su path está en key_paths pero ausente de detected_ids.
func TestPostcondition7_SafeFieldNoIdLike(t *testing.T) {
	body := []byte(`{"metadata":{"session_id":"plain123"},"messages":[{"role":"user","name":"helper"}]}`)
	paths, detected, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, p := range []string{"metadata.session_id", "messages[].role", "messages[].name"} {
		if !containsKeyPath(paths, p) {
			t.Fatalf("path %q ausente: %v", p, paths)
		}
	}
	if len(detected) != 0 {
		t.Fatalf("detected = %+v, want vacío (valores no id-like)", detected)
	}
}

// test_postcondition_7_NuncaInspeccionaContent: content/text/description/
// input/arguments aportan SOLO su path, jamás su valor (C7, I3).
func TestPostcondition7_NuncaInspeccionaContent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"ses_LEAK1","text":"msg_LEAK2","description":"agent:LEAK3","input":"ses_LEAK4","arguments":"agent:LEAK5"}],"metadata":{"session_id":"ses_OK"}}`)
	paths, detected, err := composition.Walk(body, 10)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, p := range []string{"messages[].content", "messages[].text", "messages[].description", "messages[].input", "messages[].arguments"} {
		if !containsKeyPath(paths, p) {
			t.Fatalf("path %q ausente (el campo debe aportar su path): %v", p, paths)
		}
	}
	if len(detected) != 1 || detected[0] != (composition.DetectedID{Path: "metadata.session_id", Value: "ses_OK"}) {
		t.Fatalf("detected = %+v, want solo {metadata.session_id ses_OK} (I3)", detected)
	}
}
