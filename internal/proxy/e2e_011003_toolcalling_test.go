// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-003-tool-calling: traducción del ciclo
// tool-calling de Odoo (tools ↔ function_call ↔ function_call_output) en
// POST /v1/responses. Reemplaza la rama de rechazo P7 de 001 por la
// traducción real bidireccional. Verifican las postcondiciones P1-P10 del
// spec docs/specs/011-003-tool-calling/spec.md (D1 call_id=eco del id
// upstream, D2 solo function_call items con tool_calls, D3 type!="function"
// → 400 conservado, I2 arguments string JSON, I3 byte-identidad de parameters,
// I4 round-trip call_id cerrado).
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia
// Server.Handler() — status, envelope de error wire, body Responses de
// respuesta (output[]), y el wire upstream (upstream.gotBody) para las
// traducciones de entrada; upstreams fake en httptest (incluido uno local con
// tool_calls en la salida), cero red externa (I5). No se referencia ningún
// símbolo interno de la feature (handleResponses, struct Responses).
//
// En RED la implementación de 001 aún rechaza `tools` con 400 (rama P7 vieja)
// y traduce la salida solo como item message (ignora tool_calls): P1/P3/P4/P5/
// P6/P8/P9 y el subtest json_schema+tools de P10 fallan por AssertionError.
// P2, P7 y el resto de P10 son guards de ramas conservadas que ya pasan hoy.
//
// Mapeo audit (docs/specs/011-003-tool-calling/test-audit.md): los 10 tests
// se agrupan en 4 bloques (A=P1/P2, B=P3/P4/P5, C=P6/P7/P8, D=P9/P10).

package proxy_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/config"
)

// ---- helpers del wire upstream para tool-calling ----

// toolCallWire tipa el body chat-completions que recibe el provider upstream
// cuando el request de Odoo incluye tools e items del ciclo tool-calling:
// messages con tool_calls/role:"tool", tools anidados y parallel_tool_calls.
// Los parámetros/schema viajan como json.RawMessage para verificar la
// byte-identidad I3 sin re-marshal.
type toolCallWire struct {
	Model             string   `json:"model"`
	Temperature       *float64 `json:"temperature"`
	Tools             []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      *bool           `json:"strict"`
		} `json:"function"`
	} `json:"tools"`
	ParallelToolCalls *bool `json:"parallel_tool_calls"`
	Messages          []struct {
		Role       string  `json:"role"`
		Content    *string `json:"content"`
		ToolCallID string  `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	ResponseFormat struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

// decodeToolWire tipa el body que capturó el upstream y falla si no es JSON
// o no se puede decodificar.
func decodeToolWire(t *testing.T, gotBody string) *toolCallWire {
	t.Helper()
	var w toolCallWire
	if err := json.Unmarshal([]byte(gotBody), &w); err != nil {
		t.Fatalf("body upstream no es JSON: %v\n%s", err, gotBody)
	}
	return &w
}

// toolCallingOutput tipa el body Responses de respuesta cuando el upstream
// devuelve tool_calls: items function_call (call_id/name/arguments/status) e
// items message (role/content) — un solo struct cubre ambos shapes (D2/P7).
type toolCallingOutput struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Model  string `json:"model"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Status    string `json:"status"`
		Role      string `json:"role"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func decodeToolOutput(t *testing.T, raw []byte) *toolCallingOutput {
	t.Helper()
	var out toolCallingOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body responses no es JSON: %v\n%s", err, raw)
	}
	return &out
}

// upstreamToolCalls devuelve un upstream fake cuyo choices[0].message trae
// tool_calls (salida del ciclo tool-calling). Si toolCalls es nil/[] la
// respuesta no incluye tool_calls (camino P7 message-only).
func upstreamToolCalls(model string, toolCalls []map[string]any) *upstream {
	msg := map[string]any{"role": "assistant", "content": nil}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	respBody := map[string]any{
		"id": "chatcmpl-up", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{
			"index": 0, "message": msg, "finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"total_tokens": 3},
	}
	raw, _ := json.Marshal(respBody)
	return &upstream{status: 200, body: string(raw), model: model}
}

// toolCall arma un elemento de choices[0].message.tool_calls (shape upstream).
func toolCall(id, name, arguments string) map[string]any {
	return map[string]any{
		"id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": arguments},
	}
}

// ---- BLOQUE A — traducción de entrada: definiciones de tools (P1, P2 → CA-1, CA-2) ----

// test_postcondition_1 (P1, I3): tools function → tools chat anidado con
// parameters byte-idéntico, strict passthrough/omitempty y parallel_tool_calls
// passthrough.
func TestE2E011003_P1_ToolsFunctionAChatAnidado(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	paramsRaw := `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`
	body := responsesBody("hola")
	body["tools"] = []any{map[string]any{
		"type": "function", "name": "get_weather",
		"description": "Obtiene el clima",
		"parameters":  json.RawMessage(paramsRaw),
		"strict":      true,
	}}
	body["parallel_tool_calls"] = true

	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (traducción a tools chat anidado, P1); body=%s", code, raw)
	}

	w := decodeToolWire(t, u.gotBody)
	if len(w.Tools) != 1 {
		t.Fatalf("wire tools = %d, want 1: %s", len(w.Tools), u.gotBody)
	}
	// mapeo estructural D1 (003): tool plano → anidado bajo `function`.
	if w.Tools[0].Type != "function" {
		t.Fatalf("tools[0].type = %q, want function", w.Tools[0].Type)
	}
	if w.Tools[0].Function.Name != "get_weather" || w.Tools[0].Function.Description != "Obtiene el clima" {
		t.Fatalf("tools[0].function = %+v, want name/description copiados (P1)", w.Tools[0].Function)
	}
	// I3: parameters byte-idénticos al input (no re-marshal).
	if string(w.Tools[0].Function.Parameters) != paramsRaw {
		t.Fatalf("parameters wire no byte-idéntico al input (I3):\nwant %s\ngot  %s", paramsRaw, w.Tools[0].Function.Parameters)
	}
	if w.Tools[0].Function.Strict == nil || !*w.Tools[0].Function.Strict {
		t.Fatalf("tools[0].function.strict no emitido como true (P1): %+v", w.Tools[0].Function)
	}
	if w.ParallelToolCalls == nil || !*w.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls no emitido como true (P1): %s", u.gotBody)
	}

	// strict ausente → no se emite (omitempty, misma semántica que D2 de 002).
	u2 := upstreamOK("m", "hola")
	h2 := build(t, []*upstream{u2}, "sk-test-1")
	b2 := responsesBody("hola")
	b2["tools"] = []any{map[string]any{
		"type": "function", "name": "f", "parameters": json.RawMessage(`{"type":"object"}`),
	}}
	if code, raw := responsesAs(t, h2.srv.URL, h2.key, b2); code != 200 {
		t.Fatalf("sin strict: status = %d, want 200; body=%s", code, raw)
	}
	w2 := decodeToolWire(t, u2.gotBody)
	if w2.Tools[0].Function.Strict != nil {
		t.Fatalf("tools[0].function.strict se emitió estando ausente (P1/omitempty): %+v", w2.Tools[0].Function)
	}
	if w2.ParallelToolCalls != nil {
		t.Fatalf("parallel_tool_calls se emitió estando ausente: %s", u2.gotBody)
	}
}

// test_postcondition_2 (P2, D3): tools con CUALQUIER type!="function" (incluido
// web_search_preview y el caso mixto function+no-function) → HTTP 400
// "tool calling not yet supported" (el chequeo 400 gana antes de traducir).
func TestE2E011003_P2_WebSearchPreview400(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	body := responsesBody("hola")

	t.Run("web_search_preview", func(t *testing.T) {
		b := cloneMap(body)
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P2/D3
	})
	t.Run("tipo_no_function", func(t *testing.T) {
		b := cloneMap(body)
		b["tools"] = []any{map[string]any{"type": "code_interpreter"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P2/D3
	})
	t.Run("mixto_function_y_no_function", func(t *testing.T) {
		b := cloneMap(body)
		b["tools"] = []any{
			map[string]any{"type": "function", "name": "f"},
			map[string]any{"type": "web_search_preview"},
		}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P2/D3
	})
}

// ---- BLOQUE B — traducción de entrada: items del ciclo (P3, P4, P5 → CA-3, CA-4) ----

// test_postcondition_3 (P3): item function_call en input → mensaje assistant
// con tool_calls, id del tool_call == call_id del item (fallback al id), name
// y arguments (string JSON sin re-marshal).
func TestE2E011003_P3_FunctionCallItemAAssistantToolCalls(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	argsRaw := `{"location":"París"}`
	body := responsesBody("hola")
	body["input"] = []any{map[string]any{
		"type": "function_call", "id": "resp_123",
		"call_id": "call_abc", "name": "get_weather", "arguments": argsRaw,
	}}
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (traducción item function_call, P3); body=%s", code, raw)
	}

	w := decodeToolWire(t, u.gotBody)
	if len(w.Messages) != 1 {
		t.Fatalf("wire messages = %d, want 1: %s", len(w.Messages), u.gotBody)
	}
	m := w.Messages[0]
	if m.Role != "assistant" {
		t.Fatalf("messages[0].role = %q, want assistant (P3)", m.Role)
	}
	if m.Content != nil {
		t.Fatalf("messages[0].content = %v, want null (P3)", m.Content)
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("messages[0].tool_calls = %d, want 1: %s", len(m.ToolCalls), u.gotBody)
	}
	tc := m.ToolCalls[0]
	// id del tool_call == call_id del item (asegura que el role:tool subsiguiente matchee).
	if tc.ID != "call_abc" {
		t.Fatalf("tool_call.id = %q, want call_abc (== call_id del item, P3)", tc.ID)
	}
	if tc.Type != "function" {
		t.Fatalf("tool_call.type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call.function.name = %q, want get_weather", tc.Function.Name)
	}
	// arguments string JSON sin re-marshal (P3).
	if tc.Function.Arguments != argsRaw {
		t.Fatalf("tool_call.function.arguments = %q, want %q (string JSON sin re-marshal, P3)", tc.Function.Arguments, argsRaw)
	}

	// Fallback al `id` del item si no trae `call_id` (P3).
	u2 := upstreamOK("m", "hola")
	h2 := build(t, []*upstream{u2}, "sk-test-1")
	b2 := responsesBody("hola")
	b2["input"] = []any{map[string]any{
		"type": "function_call", "id": "resp_xyz", "name": "f", "arguments": `{}`,
	}}
	if code, raw := responsesAs(t, h2.srv.URL, h2.key, b2); code != 200 {
		t.Fatalf("sin call_id: status = %d, want 200; body=%s", code, raw)
	}
	w2 := decodeToolWire(t, u2.gotBody)
	if len(w2.Messages) != 1 || len(w2.Messages[0].ToolCalls) != 1 {
		t.Fatalf("sin call_id: messages inesperadas: %s", u2.gotBody)
	}
	if w2.Messages[0].ToolCalls[0].ID != "resp_xyz" {
		t.Fatalf("tool_call.id = %q, want resp_xyz (fallback al id del item, P3)", w2.Messages[0].ToolCalls[0].ID)
	}
}

// test_postcondition_4 (P4, I4): item function_call_output en input → mensaje
// role:"tool" con tool_call_id; round-trip D1 combinado (function_call +
// function_call_output) con tool_call_id matcheando el id del tool_call.
func TestE2E011003_P4_FunctionCallOutputItemAToolRole(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	body := responsesBody("hola")
	body["input"] = []any{
		map[string]any{
			"type": "function_call", "id": "resp_1", "call_id": "call_abc",
			"name": "get_weather", "arguments": `{"location":"París"}`,
		},
		map[string]any{
			"type": "function_call_output", "call_id": "call_abc", "output": "22°C soleado",
		},
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (round-trip, P4); body=%s", code, raw)
	}

	w := decodeToolWire(t, u.gotBody)
	if len(w.Messages) != 2 {
		t.Fatalf("wire messages = %d, want 2: %s", len(w.Messages), u.gotBody)
	}
	tool := w.Messages[1]
	if tool.Role != "tool" {
		t.Fatalf("messages[1].role = %q, want tool (P4)", tool.Role)
	}
	if tool.ToolCallID != "call_abc" {
		t.Fatalf("messages[1].tool_call_id = %q, want call_abc (round-trip, I4)", tool.ToolCallID)
	}
	if tool.Content == nil || *tool.Content != "22°C soleado" {
		t.Fatalf("messages[1].content = %v, want 22°C soleado (P4)", tool.Content)
	}
	// round-trip cerrado: el assistant tool_call usa el MISMO id (call_abc).
	if len(w.Messages[0].ToolCalls) != 1 || w.Messages[0].ToolCalls[0].ID != "call_abc" {
		t.Fatalf("tool_call del assistant no usa call_abc (round-trip, I4): %s", u.gotBody)
	}
}

// test_postcondition_5 (P5): input[] mixto (message + function_call +
// function_call_output) → mensajes emitidos en orden de aparición; los items
// message se traducen igual que 001 (role preservado, content concatenado).
func TestE2E011003_P5_MessageItemsSinRegresion(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	body := responsesBody("hola")
	body["input"] = []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "¿clima?"}}},
		map[string]any{"type": "function_call", "id": "resp_1", "call_id": "call_a", "name": "f", "arguments": `{"x":1}`},
		map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "ok"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "input_text", "text": "seguimos"}}},
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	w := decodeToolWire(t, u.gotBody)
	// P5: 4 mensajes en orden de aparición.
	if len(w.Messages) != 4 {
		t.Fatalf("wire messages = %d, want 4 (orden de aparición, P5): %s", len(w.Messages), u.gotBody)
	}
	m0 := w.Messages[0]
	if m0.Role != "user" || m0.Content == nil || *m0.Content != "¿clima?" {
		t.Fatalf("messages[0] = role:%q content:%v, want user/¿clima? (P5/001)", m0.Role, m0.Content)
	}
	if w.Messages[1].Role != "assistant" || len(w.Messages[1].ToolCalls) != 1 {
		t.Fatalf("messages[1] no es assistant con tool_calls (P5): %s", u.gotBody)
	}
	if w.Messages[2].Role != "tool" || w.Messages[2].ToolCallID != "call_a" {
		t.Fatalf("messages[2] no es tool con tool_call_id (P5): %s", u.gotBody)
	}
	m3 := w.Messages[3]
	if m3.Role != "assistant" || m3.Content == nil || *m3.Content != "seguimos" {
		t.Fatalf("messages[3] = role:%q content:%v, want assistant/seguimos (P5)", m3.Role, m3.Content)
	}
}

// ---- BLOQUE C — traducción de salida (P6, P7, P8 → CA-5, CA-6, CA-7) ----

// test_postcondition_6 (P6, D1, D2, I2): N tool_calls → EXACTAMENTE N items
// function_call y CERO items message; call_id eco del id upstream, arguments
// parseable, status completed, shape parseable por _request_llm_openai_helper.
func TestE2E011003_P6_ToolCallsASoloFunctionCallItems(t *testing.T) {
	u := upstreamToolCalls("m", []map[string]any{
		toolCall("call_1", "get_weather", `{"location":"París"}`),
		toolCall("call_2", "get_stock", `{"ticker":"AAPL"}`),
	})
	h := build(t, []*upstream{u}, "sk-test-1")

	code, raw := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	out := decodeToolOutput(t, raw)
	// D2: EXACTAMENTE N items function_call y CERO items message.
	if len(out.Output) != 2 {
		t.Fatalf("output len = %d, want 2 (un function_call por tool_call, D2)", len(out.Output))
	}
	for i, it := range out.Output {
		if it.Type != "function_call" {
			t.Fatalf("output[%d].type = %q, want function_call (D2)", i, it.Type)
		}
		if it.Status != "completed" {
			t.Fatalf("output[%d].status = %q, want completed", i, it.Status)
		}
		if it.ID == "" {
			t.Fatalf("output[%d] sin id estable (P8)", i)
		}
	}
	// D1: call_id == eco directo del id upstream.
	if out.Output[0].CallID != "call_1" || out.Output[0].Name != "get_weather" {
		t.Fatalf("output[0] = call_id:%q name:%q, want call_1/get_weather (eco D1)", out.Output[0].CallID, out.Output[0].Name)
	}
	// I2: arguments string JSON parseable por json.loads.
	var parsed any
	if err := json.Unmarshal([]byte(out.Output[0].Arguments), &parsed); err != nil {
		t.Fatalf("output[0].arguments no es JSON parseable (I2): %q (%v)", out.Output[0].Arguments, err)
	}
	if out.Output[1].CallID != "call_2" || out.Output[1].Name != "get_stock" {
		t.Fatalf("output[1] = call_id:%q name:%q, want call_2/get_stock (eco D1)", out.Output[1].CallID, out.Output[1].Name)
	}
}

// test_postcondition_7 (P7): tool_calls vacío/ausente → output[] con un solo
// item message, content[0].text == choices[0].message.content (P10 de 001
// intacto).
func TestE2E011003_P7_SinToolCallsItemMessage(t *testing.T) {
	t.Run("tool_calls_vacio", func(t *testing.T) {
		u := upstreamToolCalls("m", []map[string]any{})
		h := build(t, []*upstream{u}, "sk-test-1")
		code, raw := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
		if code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", code, raw)
		}
		assertSingleMessage(t, raw, "hola")
	})
	t.Run("tool_calls_ausente", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		h := build(t, []*upstream{u}, "sk-test-1")
		code, raw := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
		if code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", code, raw)
		}
		assertSingleMessage(t, raw, "hola")
	})
}

// assertSingleMessage verifica que output[] tiene un único item message
// assistant/completed con content[0].text == wantText (P7 / P10 de 001).
func assertSingleMessage(t *testing.T, raw []byte, wantText string) {
	t.Helper()
	out := decodeToolOutput(t, raw)
	if len(out.Output) != 1 {
		t.Fatalf("output len = %d, want 1 (tool_calls vacío → item message, P7)", len(out.Output))
	}
	it := out.Output[0]
	if it.Type != "message" || it.Role != "assistant" || it.Status != "completed" {
		t.Fatalf("item = type:%q role:%q status:%q, want message/assistant/completed (P7)", it.Type, it.Role, it.Status)
	}
	if len(it.Content) != 1 || it.Content[0].Type != "output_text" || it.Content[0].Text != wantText {
		t.Fatalf("content = %+v, want [{output_text, %q}] (P10 de 001 intacto, P7)", it.Content, wantText)
	}
}

// test_postcondition_8 (P8, D1): mismo clientID + body → el id de los items
// function_call es determinístico y distinto del call_id (eco del upstream).
func TestE2E011003_P8_IdEstableDeterministico(t *testing.T) {
	body := responsesBody("hola")
	toolCalls := []map[string]any{toolCall("call_abc", "get_weather", `{"location":"París"}`)}

	send := func() (id, callID string) {
		u := upstreamToolCalls("m", toolCalls)
		h := build(t, []*upstream{u}, "sk-test-1")
		code, raw := responsesAs(t, h.srv.URL, h.key, body)
		if code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", code, raw)
		}
		out := decodeToolOutput(t, raw)
		if len(out.Output) != 1 {
			t.Fatalf("output len = %d, want 1", len(out.Output))
		}
		return out.Output[0].ID, out.Output[0].CallID
	}

	id1, call1 := send()
	id2, call2 := send()

	// P8: mismo clientID + body → id determinístico (mismo).
	if id1 == "" {
		t.Fatal("item function_call sin id (P8)")
	}
	if id1 != id2 {
		t.Fatalf("id no determinístico: %q != %q (mismo clientID + body, P8)", id1, id2)
	}
	// D1: id estable != call_id (eco no-determinístico del id upstream).
	if id1 == call1 {
		t.Fatalf("id estable == call_id (%q), deben ser distintos (D1/P8)", id1)
	}
	if call1 != "call_abc" || call1 != call2 {
		t.Fatalf("call_id = %q, want call_abc (eco upstream) en ambos (D1)", call1)
	}
}

// ---- BLOQUE D — pipeline / invariantes de integración (P9, P10 → CA-8) ----

// test_postcondition_9 (P9): tools/parallel_tool_calls sobreviven la pipeline
// compartida (inyección de thinking, cache exact-match por endpoint, ventana
// de contexto) sin perderse.
func TestE2E011003_P9_PipelineCompartidaYPassthrough(t *testing.T) {
	paramsRaw := `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`
	tool := map[string]any{"type": "function", "name": "get_weather", "parameters": json.RawMessage(paramsRaw)}

	// bodyWith arma un body Responses determinístico (temperature==0, cache-
	// eligible, P12 de 001) con tools/parallel_tool_calls.
	bodyWith := func() map[string]any {
		b := cloneMap(responsesBody("hola"))
		b["temperature"] = 0
		b["tools"] = []any{tool}
		b["parallel_tool_calls"] = true
		return b
	}

	t.Run("tools_sobreviven_inyeccion_thinking", func(t *testing.T) {
		// path zen (buildRP) + perfil reasoning (metaFlash) → inyecta
		// reasoning_effort (010-002 P4). Los tools/parallel_tool_calls deben
		// sobrevivir la re-encodificación (P9/I2 de 002).
		u := upstreamOK("m", "hola")
		h := buildRP(t, "sk-test-1", rpProvider{id: "zen", model: "m", maxTokens: 384000, ups: u})
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": metaFlash()})

		if code, raw := responsesAs(t, h.srv.URL, h.key, bodyWith()); code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", code, raw)
		}
		w := decodeToolWire(t, u.gotBody)
		if len(w.Tools) != 1 {
			t.Fatalf("tools perdidos en la inyección de thinking (P9): %s", u.gotBody)
		}
		if string(w.Tools[0].Function.Parameters) != paramsRaw {
			t.Fatalf("parameters no byte-idéntico tras la inyección (P9/I3):\nwant %s\ngot  %s", paramsRaw, w.Tools[0].Function.Parameters)
		}
		if w.ParallelToolCalls == nil || !*w.ParallelToolCalls {
			t.Fatalf("parallel_tool_calls perdido tras la inyección de thinking (P9): %s", u.gotBody)
		}
		got := bodyField(t, u.gotBody)
		if got["reasoning_effort"] != "low" {
			t.Fatalf("reasoning_effort = %v, want low (inyección actúa sobre el wire con tools, P9)", got["reasoning_effort"])
		}
	})

	t.Run("tools_participan_de_cache_por_endpoint", func(t *testing.T) {
		c := &countingUpstream011{inner: upstreamOK("m", "hola")}
		h := buildCounting011(t, c, "sk-test-1")
		h.proxySrv.SetResponseCache(true, 512, time.Hour)

		// 1ra vez: MISS → 1 llamada upstream, tools llegan al wire.
		if code, _ := responsesAs(t, h.srv.URL, h.key, bodyWith()); code != 200 {
			t.Fatalf("1ra status = %d, want 200", code)
		}
		if got := c.callCount(); got != 1 {
			t.Fatalf("1ra: upstream calls = %d, want 1 (miss cache)", got)
		}
		// 2da idéntico: HIT → NO re-llama (tools participan de la key, P9).
		if code, _ := responsesAs(t, h.srv.URL, h.key, bodyWith()); code != 200 {
			t.Fatalf("2da status = %d, want 200", code)
		}
		if got := c.callCount(); got != 1 {
			t.Fatalf("2da: upstream calls = %d, want 1 (hit cache, tools en la key)", got)
		}
		// tools distintos → MISS → re-llama (la key cambia con los tools).
		other := bodyWith()
		other["tools"] = []any{map[string]any{"type": "function", "name": "get_other", "parameters": json.RawMessage(`{"type":"object"}`)}}
		if code, _ := responsesAs(t, h.srv.URL, h.key, other); code != 200 {
			t.Fatalf("otra tool status = %d, want 200", code)
		}
		if got := c.callCount(); got != 2 {
			t.Fatalf("otra tool: upstream calls = %d, want 2 (miss, tools distinta → key distinta, P9)", got)
		}
	})

	t.Run("rechazo_por_ventana_con_tools", func(t *testing.T) {
		// P9: con tools presentes, la MISMA pipeline de contexto aplica →
		// body grande → 400 context, cero intentos upstream (P4a de 001).
		// buildMultiClient (no build) para que proxySrv esté inicializado
		// y SetModelMetadata no haga nil-deref (patrón de e2e_008001).
		ups := upstreamOK("m", "x")
		h := buildMultiClient(t, []*upstream{ups}, "k1")
		h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{"m": {ContextWindow: 1000, MaxOutput: 100}})
		b := responsesBody(strings.Repeat("x", 5000))
		b["tools"] = []any{tool}
		code, body := responsesAs(t, h.srv.URL, h.key, b)
		if code != 400 {
			t.Fatalf("status = %d, want 400 (rechazo por ventana con tools, P9/P4a)", code)
		}
		if msg, _ := errorObject(t, body)["message"].(string); !strings.Contains(strings.ToLower(msg), "context") {
			t.Fatalf("mensaje sin 'context': %q", msg)
		}
		if ups.gotBody != "" {
			t.Fatal("hubo intento upstream tras rechazo por ventana (P9)")
		}
	})
}

// test_postcondition_10 (P10): rechazos conservados de 001/002 intactos
// (stream, text.format no-json_schema, parts file/image, body no-JSON/sin
// input, sin Bearer) y json_schema coexiste con tools en el mismo request.
func TestE2E011003_P10_RechazosConservados(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	body := responsesBody("hola")

	t.Run("stream_true_400", func(t *testing.T) {
		b := cloneMap(body)
		b["stream"] = true
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "streaming not supported yet") // P6 de 001
	})
	t.Run("text_format_no_json_schema_400", func(t *testing.T) {
		b := cloneMap(body)
		b["text"] = map[string]any{"format": map[string]any{"type": "object", "name": "x"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "structured output not yet supported") // P4 de 002
	})
	t.Run("part_input_file_400", func(t *testing.T) {
		code, raw := responsesAs(t, h.srv.URL, h.key, inputPart(t, body, "input_file"))
		assertReject(t, code, raw, "file attachments not yet supported") // P9 de 001
	})
	t.Run("part_input_image_400", func(t *testing.T) {
		code, raw := responsesAs(t, h.srv.URL, h.key, inputPart(t, body, "input_image"))
		assertReject(t, code, raw, "file attachments not yet supported") // P9 de 001
	})
	t.Run("body_no_json_400", func(t *testing.T) {
		code, raw := responsesRaw(t, h.srv.URL, h.key, []byte(`not-json`))
		if code != 400 {
			t.Fatalf("status = %d, want 400 (P2 de 001)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("body_sin_input_400", func(t *testing.T) {
		code, raw := responsesAs(t, h.srv.URL, h.key, map[string]any{"model": "m"})
		if code != 400 {
			t.Fatalf("status = %d, want 400 (P2 de 001)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("sin_bearer_401", func(t *testing.T) {
		code, raw := responsesRaw(t, h.srv.URL, "", []byte(`{}`))
		if code != 401 {
			t.Fatalf("status = %d, want 401 (P1 de 001)", code)
		}
		assertErrorEnvelope(t, raw)
	})
	t.Run("json_schema_coexiste_con_tools", func(t *testing.T) {
		// P10/P1 de 002: text.format.json_schema sigue traduciéndose a
		// response_format y coexiste con la traducción de tools (P1 de 003).
		schemaRaw := `{"type":"object"}`
		u := upstreamOK("m", "hola")
		h2 := build(t, []*upstream{u}, "sk-test-1")
		b := cloneMap(responsesBody("hola"))
		b["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "s", "schema": json.RawMessage(schemaRaw)}}
		b["tools"] = []any{map[string]any{"type": "function", "name": "f", "parameters": json.RawMessage(`{"type":"object"}`)}}
		if code, raw := responsesAs(t, h2.srv.URL, h2.key, b); code != 200 {
			t.Fatalf("status = %d, want 200 (json_schema + tools, P10/P1 de 002); body=%s", code, raw)
		}
		w := decodeToolWire(t, u.gotBody)
		if w.ResponseFormat.Type != "json_schema" {
			t.Fatalf("response_format.type = %q, want json_schema (coexistencia, P10): %s", w.ResponseFormat.Type, u.gotBody)
		}
		if len(w.Tools) != 1 {
			t.Fatalf("tools no traducidas junto a response_format (P10): %s", u.gotBody)
		}
	})
}
