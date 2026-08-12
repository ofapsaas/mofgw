// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-004-file-attachments: traducción de las parts de
// archivo del input[] Responses de Odoo (input_file / input_image → content
// array chat-completions) en POST /v1/responses. Reemplaza la rama de rechazo
// P9 de 001 por la traducción real. Verifican las postcondiciones P1-P8 del
// spec docs/specs/011-004-file-attachments/spec.md (D1 decisión por mensaje,
// D2 mapeo de parts, D3 sin gating por modality / 4xx upstream propagado).
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia Server.Handler()
// — status, envelope de error wire, body Responses de respuesta, y el wire
// upstream (upstream.gotBody) para las traducciones de entrada; upstreams fake
// en httptest, cero red externa (I5). No se referencia ningún símbolo interno
// de la feature (handleResponses, struct Responses).
//
// En RED la implementación de 001 aún rechaza input_file/input_image con 400
// (rama P9 vieja) y no construye content-array: los tests de traducción (P1-P6,
// P8) fallan por AssertionError (status != 200 o content no-array). El wire de
// content-array se navega por map vía bodyField (decodeToolWire no aplica:
// tipa Content como *string). En GREEN el implementer cumple el contrato sin
// tocar estos tests.
//
// Mapeo audit (docs/specs/011-004-file-attachments/test-audit.md): 12 tests en
// 6 bloques — A=P1/P2, B=P3, C=P4, D=P5, E=P6, F=P8. P7/P9 los cubren tests
// existentes untouched (espec §P7/§P9).

package proxy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
)

// ---- helpers de construcción de parts de archivo (P1/P2, D2) ----

// stringPtr devuelve un puntero a s (para el detail opcional de imagePart).
func stringPtr(s string) *string { return &s }

// imagePart construye una part input_image COMPLETA (P1/D2): data-URI en
// image_url y detail opcional (nil → no se emite, omitempty). No reutiliza el
// helper inputPart de 001 (produce parts sin los campos reales).
func imagePart(dataURI string, detail *string) map[string]any {
	p := map[string]any{"type": "input_image", "image_url": dataURI}
	if detail != nil {
		p["detail"] = *detail
	}
	return p
}

// filePart construye una part input_file COMPLETA (P2/D2): data-URI en
// file_data y filename. Part estilo OpenAI, sin detail.
func filePart(dataURI, filename string) map[string]any {
	return map[string]any{"type": "input_file", "file_data": dataURI, "filename": filename}
}

// ---- helpers de navegación del wire upstream (content-string vs array) ----

// wireMessages decodifica el body que capturó el upstream y devuelve los
// messages del wire como []map[string]any.
func wireMessages(t *testing.T, gotBody string) []map[string]any {
	t.Helper()
	m := bodyField(t, gotBody)
	raw, ok := m["messages"]
	if !ok {
		t.Fatalf("wire sin messages: %s", gotBody)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("messages no es array en wire: %s", gotBody)
	}
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		msg, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("messages[i] no es objeto: %s", gotBody)
		}
		out = append(out, msg)
	}
	return out
}

// wireContentArray decodifica el content del mensaje idx del wire como array
// de parts; falla si el content es string o está ausente (content-array, P3).
func wireContentArray(t *testing.T, gotBody string, idx int) []map[string]any {
	t.Helper()
	msgs := wireMessages(t, gotBody)
	if idx >= len(msgs) {
		t.Fatalf("messages[%d] ausente: %s", idx, gotBody)
	}
	raw, ok := msgs[idx]["content"].([]any)
	if !ok {
		t.Fatalf("messages[%d].content no es array (content-array, P3): %s", idx, gotBody)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		part, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("messages[%d].content[%d] no es objeto: %s", idx, len(out), gotBody)
		}
		out = append(out, part)
	}
	return out
}

// wireContentString decodifica el content del mensaje idx del wire como
// string; falla si es array (content-string, P3/sin regresión 001).
func wireContentString(t *testing.T, gotBody string, idx int) string {
	t.Helper()
	msgs := wireMessages(t, gotBody)
	if idx >= len(msgs) {
		t.Fatalf("messages[%d] ausente: %s", idx, gotBody)
	}
	s, ok := msgs[idx]["content"].(string)
	if !ok {
		t.Fatalf("messages[%d].content no es string (content-string, P3): %s", idx, gotBody)
	}
	return s
}

// ---- BLOQUE A — traducción de parts de archivo (P1, P2 → CA-1, CA-2) ----

// test_postcondition_1a (P1, CA-1): input_image con detail → content-array
// chat con part {"type":"image_url","image_url":{"url":U,"detail":"low"}},
// url == data-URI (sin re-marshal, I2).
func TestE2E011004_P1_ImageURLDetailPassthrough(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{
		map[string]any{"type": "input_text", "text": "¿qué dice?"},
		imagePart("data:image/png;base64,iVBORw0KGgo=", stringPtr("low")),
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P1); body=%s", code, raw)
	}

	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 2 {
		t.Fatalf("content array len = %d, want 2: %s", len(ca), u.gotBody)
	}
	if ca[0]["type"] != "text" || ca[0]["text"] != "¿qué dice?" {
		t.Fatalf("ca[0] = %+v, want {text, ¿qué dice?} (P-D1)", ca[0])
	}
	iu, _ := ca[1]["image_url"].(map[string]any)
	if ca[1]["type"] != "image_url" || iu == nil {
		t.Fatalf("ca[1] = %+v, want image_url (P1): %s", ca[1], u.gotBody)
	}
	if iu["url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("image_url.url = %v, want data-URI (I2, P1)", iu["url"])
	}
	if iu["detail"] != "low" {
		t.Fatalf("image_url.detail = %v, want low (passthrough, P1)", iu["detail"])
	}
}

// test_postcondition_1b (P1): input_image SIN detail → content-array con part
// image_url sin campo detail (omitempty, misma semántica que strict de 002/003).
func TestE2E011004_P1_ImageURLDetailAusenteOmitEmpty(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{imagePart("data:image/png;base64,iVBORw0KGgo=", nil)}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P1); body=%s", code, raw)
	}

	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 1 {
		t.Fatalf("content array len = %d, want 1: %s", len(ca), u.gotBody)
	}
	iu, _ := ca[0]["image_url"].(map[string]any)
	if iu == nil || iu["url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("image_url = %+v, want url data-URI (P1): %s", iu, u.gotBody)
	}
	if _, ok := iu["detail"]; ok {
		t.Fatalf("image_url.detail se emitió estando ausente (P1/omitempty): %+v", iu)
	}
}

// test_postcondition_2a (P2, CA-2): input_file → content-array chat con part
// {"type":"file","file":{"file_data":D,"filename":F}}, copiados tal cual (I2).
func TestE2E011004_P2_FilePartTraduccion(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{
		map[string]any{"type": "input_text", "text": "resume"},
		filePart("data:application/pdf;base64,JVBERi0xLjc=", "doc.pdf"),
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P2); body=%s", code, raw)
	}

	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 2 {
		t.Fatalf("content array len = %d, want 2: %s", len(ca), u.gotBody)
	}
	file, _ := ca[1]["file"].(map[string]any)
	if ca[1]["type"] != "file" || file == nil {
		t.Fatalf("ca[1] = %+v, want file (P2): %s", ca[1], u.gotBody)
	}
	if file["file_data"] != "data:application/pdf;base64,JVBERi0xLjc=" || file["filename"] != "doc.pdf" {
		t.Fatalf("file = %+v, want file_data/filename copiados tal cual (I2, P2)", file)
	}
	if _, ok := file["detail"]; ok {
		t.Fatalf("file.detail se emitió (P2/D2, part sin detail): %+v", file)
	}
}

// test_postcondition_2b (P2): input_file → NO 400 "file attachments not yet
// supported" (assert negativo de la rama eliminada, P2/P6) y la salida sigue
// siendo un item message normal (P9 intacto).
func TestE2E011004_P2_FileSin400(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{filePart("data:application/pdf;base64,JVBERi0xLjc=", "doc.pdf")}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (rama de rechazo P9 de 001 eliminada, P2/P6); body=%s", code, raw)
	}
	out := decodeResponses(t, raw)
	if len(out.Output) != 1 || out.Output[0].Type != "message" || out.Output[0].Content[0].Text != "hola" {
		t.Fatalf("output inesperado tras traducción de file: %+v", out.Output)
	}
}

// ---- BLOQUE B — decisión por mensaje string-vs-array (P3 → CA-3, CA-4, D1) ----

// test_postcondition_3a (CA-3): solo parts input_text → content STRING
// (sin regresión vs 001/003).
func TestE2E011004_P3_SoloInputTextString(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	code, raw := responsesAs(t, h.srv.URL, h.key, responsesBody("hola"))
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}
	if got := wireContentString(t, u.gotBody, 0); got != "hola" {
		t.Fatalf("content = %q, want %q (string, CA-3/P3)", got, "hola")
	}
}

// test_postcondition_3b (CA-4, I3): input_text + input_image + input_file →
// content-array con 3 items en el MISMO orden de las parts del input.
func TestE2E011004_P3_MixtoArrayOrden(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{
		map[string]any{"type": "input_text", "text": "¿qué dice?"},
		imagePart("data:image/png;base64,iVBORw0KGgo=", nil),
		filePart("data:application/pdf;base64,JVBERi0xLjc=", "doc.pdf"),
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 3 {
		t.Fatalf("content array len = %d, want 3: %s", len(ca), u.gotBody)
	}
	if ca[0]["type"] != "text" {
		t.Fatalf("ca[0].type = %v, want text (orden, I3)", ca[0]["type"])
	}
	if ca[1]["type"] != "image_url" {
		t.Fatalf("ca[1].type = %v, want image_url (orden, I3)", ca[1]["type"])
	}
	if ca[2]["type"] != "file" {
		t.Fatalf("ca[2].type = %v, want file (orden, I3)", ca[2]["type"])
	}
}

// test_postcondition_3c (D1): la decisión string-vs-array es POR MENSAJE — un
// mismo input[] puede tener un mensaje content-string y otro content-array.
func TestE2E011004_P3_MensajesMixtosPorMensaje(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	b["input"] = []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "texto plano"}}},
		map[string]any{"role": "user", "content": []any{imagePart("data:image/png;base64,iVBORw0KGgo=", nil)}},
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	msgs := wireMessages(t, u.gotBody)
	if len(msgs) != 2 {
		t.Fatalf("wire messages = %d, want 2: %s", len(msgs), u.gotBody)
	}
	if got := wireContentString(t, u.gotBody, 0); got != "texto plano" {
		t.Fatalf("messages[0].content = %q, want %q (string, D1)", got, "texto plano")
	}
	ca := wireContentArray(t, u.gotBody, 1)
	if len(ca) != 1 || ca[0]["type"] != "image_url" {
		t.Fatalf("messages[1].content = %+v, want [image_url] (array, D1)", ca)
	}
}

// ---- BLOQUE C — coexistencia con el ciclo tool-calling (P4 → CA-5) ----

// test_postcondition_4 (CA-5): input[] mixto con message-imagen +
// function_call + function_call_output → los items del ciclo se traducen como
// 003 y el message como content-array, en orden; el content-array no afecta a
// los items del ciclo (P4).
func TestE2E011004_P4_MixtoCicloIntacto(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	b["input"] = []any{
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "analizá la imagen"},
				imagePart("data:image/png;base64,iVBORw0KGgo=", nil),
			},
		},
		map[string]any{"type": "function_call", "id": "resp_1", "call_id": "call_a", "name": "get_weather", "arguments": `{"location":"París"}`},
		map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "22°C"},
	}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	msgs := wireMessages(t, u.gotBody)
	if len(msgs) != 3 {
		t.Fatalf("wire messages = %d, want 3 (orden de aparición, P4): %s", len(msgs), u.gotBody)
	}
	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 2 || ca[0]["type"] != "text" || ca[1]["type"] != "image_url" {
		t.Fatalf("messages[0].content = %+v, want [text, image_url] (P3/P4)", ca)
	}
	m1 := msgs[1]
	if m1["role"] != "assistant" {
		t.Fatalf("messages[1].role = %v, want assistant (P4/003)", m1["role"])
	}
	tcs, _ := m1["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("messages[1].tool_calls = %d, want 1 (003 P3): %s", len(tcs), u.gotBody)
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_a" {
		t.Fatalf("tool_call.id = %v, want call_a (P4/003)", tc["id"])
	}
	m2 := msgs[2]
	if m2["role"] != "tool" || m2["tool_call_id"] != "call_a" {
		t.Fatalf("messages[2] = role:%v tool_call_id:%v, want tool/call_a (P4/003)", m2["role"], m2["tool_call_id"])
	}
}

// ---- BLOQUE D — passthrough coexistente (P5) ----

// test_postcondition_5 (P5): tools/parallel_tool_calls/response_format viajan
// tal cual como 003/002 coexistiendo con el content-array de P3.
func TestE2E011004_P5_BodyCoexisteConContentArray(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	schemaRaw := `{"type":"object"}`
	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{imagePart("data:image/png;base64,iVBORw0KGgo=", stringPtr("high"))}
	b["tools"] = []any{map[string]any{"type": "function", "name": "f", "parameters": json.RawMessage(`{"type":"object"}`)}}
	b["parallel_tool_calls"] = true
	b["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "s", "schema": json.RawMessage(schemaRaw)}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	w := bodyField(t, u.gotBody)
	tools, _ := w["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("wire tools = %d, want 1 (P5/003): %s", len(tools), u.gotBody)
	}
	ptc, ok := w["parallel_tool_calls"].(bool)
	if !ok || !ptc {
		t.Fatalf("parallel_tool_calls = %v, want true (P5/003): %s", w["parallel_tool_calls"], u.gotBody)
	}
	rf, _ := w["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_schema" {
		t.Fatalf("response_format = %+v, want json_schema (P5/002): %s", rf, u.gotBody)
	}
	ca := wireContentArray(t, u.gotBody, 0)
	if len(ca) != 1 || ca[0]["type"] != "image_url" {
		t.Fatalf("content = %+v, want [image_url] (P3 coexistente con el body): %s", ca, u.gotBody)
	}
}

// ---- BLOQUE E — rechazo P9 eliminado / 4xx upstream propagado (P6 → CA-6) ----

// test_postcondition_6a (P6): input_file → 200 (sin 400 local, D3) y salida
// item message normal (P9 intacto).
func TestE2E011004_P6_FileNo400UpstreamOk(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{filePart("data:application/pdf;base64,JVBERi0xLjc=", "doc.pdf")}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P6/CA-2, sin 400 de P9 de 001); body=%s", code, raw)
	}
	out := decodeResponses(t, raw)
	if len(out.Output) != 1 || out.Output[0].Content[0].Text != "hola" {
		t.Fatalf("output inesperado: %+v", out.Output)
	}
}

// test_postcondition_6b (CA-6): mock upstream que responde 4xx → se propaga el
// 4xx no-reintentable (D5 de 002), cero reintentos (patrón TestE2E011002_P7).
func TestE2E011004_P6_Upstream4xxPropagado(t *testing.T) {
	c := &countingUpstream011{inner: upstreamFail(400)}
	h := buildCounting011(t, c, "sk-test-1")

	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{imagePart("data:image/png;base64,iVBORw0KGgo=", nil)}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 400 {
		t.Fatalf("status = %d, want 400 (propagado del upstream, P6/D5); body=%s", code, raw)
	}
	assertErrorEnvelope(t, raw)
	if got := c.callCount(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (4xx no-reintentable, P6/D5)", got)
	}
	// El único request incluyó la parte traducida (sin gating local, D3).
	ca := wireContentArray(t, c.inner.gotBody, 0)
	if len(ca) != 1 || ca[0]["type"] != "image_url" {
		t.Fatalf("wire content = %+v, want [image_url] (traducción enviada, P6)", ca)
	}
}

// ---- BLOQUE F — ventana de contexto con data-URI (P8) ----

// test_postcondition_8 (P8): una data-URI grande infla len(body), pero si el
// estimado (len(body)/4, misma heurística que chat) queda dentro de la ventana,
// el request pasa sin rechazo falso y la data-URI viaja intacta al upstream
// (I2). buildMultiClient para que proxySrv esté inicializado (patrón 003 P9).
func TestE2E011004_P8_DataURIDentroVentanaPasa(t *testing.T) {
	ups := upstreamOK("m", "hola")
	h := buildMultiClient(t, []*upstream{ups}, "k1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 100000, MaxOutput: 100},
	})

	dataURI := "data:image/png;base64," + strings.Repeat("A", 8000)
	b := responsesBody("hola")
	item := b["input"].([]any)[0].(map[string]any)
	item["content"] = []any{imagePart(dataURI, nil)}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (data-URI grande pero dentro de window, P8); body=%s", code, raw)
	}
	ca := wireContentArray(t, ups.gotBody, 0)
	iu, _ := ca[0]["image_url"].(map[string]any)
	if iu == nil || iu["url"] != dataURI {
		t.Fatalf("image_url.url no coincide con la data-URI (I2/P8): %+v", iu)
	}
}
