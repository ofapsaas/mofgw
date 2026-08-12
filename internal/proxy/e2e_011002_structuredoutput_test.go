// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-002-structured-output: traducción de
// text.format.json_schema → response_format en la entrada de POST
// /v1/responses (structured output, epic 011-mofgw-odoo). Verifican las
// postcondiciones P1-P7 del spec docs/specs/011-002-structured-output/spec.md
// (D1 mapeo estructural, D2 strict passthrough, D3 solo json_schema se
// traduce, D4 temperature tal cual, D5 no degradar a json_object).
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia Server.Handler()
// — status, envelope de error wire y body Responses de respuesta; el wire
// upstream (upstream.gotBody) para el response_format traducido; upstreams
// fake en httptest, cero red externa (I5). No se referencia ningún símbolo
// interno de la feature (handleResponses, struct Responses).
//
// En RED la implementación de 001 aún rechaza text.format.json_schema con 400
// (rama P8 vieja): P1/P2/P3/P5/P6/P7 fallan por AssertionError (status != 200,
// upstream no llamado, o calls != esperado). P4 es guard de rama conservada:
// ya pasa hoy (400) porque 001 rechaza todo text.format — el implementer lo
// mantiene verde al traducir SOLO json_schema (D3).

package proxy_test

import (
	"encoding/json"
	"testing"
	"time"
)

// ---- helpers del wire upstream para structured output ----

// responsesWire tipa el body chat-completions que recibe el provider upstream:
// messages traducidos + response_format (mapeo D1). El schema viaja como
// json.RawMessage para verificar la byte-identidad I3 sin re-marshal.
type responsesWire struct {
	Model       string  `json:"model"`
	Temperature *float64 `json:"temperature"`
	Messages    []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
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

// decodeWire tipa el body que capturó el upstream y falla si no es JSON o no
// se puede decodificar.
func decodeWire(t *testing.T, gotBody string) *responsesWire {
	t.Helper()
	var w responsesWire
	if err := json.Unmarshal([]byte(gotBody), &w); err != nil {
		t.Fatalf("body upstream no es JSON: %v\n%s", err, gotBody)
	}
	return &w
}

// structuredBody arma un body Responses con text.format.json_schema a partir
// de un body base y una schema cruda (byte-identidad I3).
func structuredBody(base map[string]any, schema json.RawMessage) map[string]any {
	b := cloneMap(base)
	b["text"] = map[string]any{"format": map[string]any{
		"type":   "json_schema",
		"name":   "json_schema",
		"schema": schema,
	}}
	return b
}

// ---- BLOQUE A — happy path structured output (P1, P2, P3, P5 → CA-1, CA-2) ----

func TestE2E011002_P1_TraduccionJsonSchema(t *testing.T) {
	schemaRaw := `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	body := structuredBody(responsesBody("hola"), json.RawMessage(schemaRaw))
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (traducción a response_format, P1); body=%s", code, raw)
	}

	// P1: el wire upstream contiene response_format estructural (D1).
	w := decodeWire(t, u.gotBody)
	if w.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format.type = %q, want json_schema: %s", w.ResponseFormat.Type, u.gotBody)
	}
	if w.ResponseFormat.JSONSchema.Name != "json_schema" {
		t.Fatalf("json_schema.name = %q, want json_schema", w.ResponseFormat.JSONSchema.Name)
	}
	// I3: schema byte-identidad con el input (CA-1).
	if string(w.ResponseFormat.JSONSchema.Schema) != schemaRaw {
		t.Fatalf("schema wire no byte-idéntico al input (I3):\nwant %s\ngot  %s", schemaRaw, w.ResponseFormat.JSONSchema.Schema)
	}
	// strict NO se aserta acá: structuredBody omite strict (no se manda en el
	// input), y por D2 si strict está ausente no se emite en el wire (nil).
	// El passthrough de strict lo cubre por separado
	// TestE2E011002_P2_StrictPassthrough (strict_true/strict_false/
	// strict_ausente). Asertar strict==true acá sería una contradicción con D2.
}

func TestE2E011002_P2_StrictPassthrough(t *testing.T) {
	schemaRaw := `{"type":"object"}`

	// send arma el wire con un format dado y devuelve el response_format.
	send := func(t *testing.T, extra map[string]any) *responsesWire {
		t.Helper()
		u := upstreamOK("m", "hola")
		h := build(t, []*upstream{u}, "sk-test-1")
		format := map[string]any{"type": "json_schema", "name": "json_schema", "schema": json.RawMessage(schemaRaw)}
		for k, v := range extra {
			format[k] = v
		}
		body := responsesBody("hola")
		body["text"] = map[string]any{"format": format}
		if code, raw := responsesAs(t, h.srv.URL, h.key, body); code != 200 {
			t.Fatalf("status = %d, want 200; body=%s", code, raw)
		}
		return decodeWire(t, u.gotBody)
	}

	t.Run("strict_true", func(t *testing.T) {
		w := send(t, map[string]any{"strict": true})
		if w.ResponseFormat.JSONSchema.Strict == nil || !*w.ResponseFormat.JSONSchema.Strict {
			t.Fatalf("json_schema.strict no emitido como true (P2/D2): %+v", w.ResponseFormat.JSONSchema)
		}
	})
	t.Run("strict_false", func(t *testing.T) {
		w := send(t, map[string]any{"strict": false})
		if w.ResponseFormat.JSONSchema.Strict == nil || *w.ResponseFormat.JSONSchema.Strict {
			t.Fatalf("json_schema.strict no emitido como false (P2/D2): %+v", w.ResponseFormat.JSONSchema)
		}
	})
	t.Run("strict_ausente", func(t *testing.T) {
		w := send(t, nil)
		if w.ResponseFormat.JSONSchema.Strict != nil {
			t.Fatalf("json_schema.strict se emitió estando ausente (D2): %+v", w.ResponseFormat.JSONSchema)
		}
	})
}

func TestE2E011002_P3_TemperatureNoForzado(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")

	body := structuredBody(responsesBody("hola"), json.RawMessage(`{"type":"object"}`))
	body["temperature"] = 0.7
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	// P3/D4: temperature NO se fuerza a 0 por tener structured output.
	w := decodeWire(t, u.gotBody)
	if w.Temperature == nil || *w.Temperature != 0.7 {
		t.Fatalf("upstream temperature = %v, want 0.7 (passthrough P3, no forzado a 0): %s", w.Temperature, u.gotBody)
	}
}

func TestE2E011002_P5_SalidaIntacta(t *testing.T) {
	jsonContent := "<json>" // literal del audit: output viaja como texto normal
	h := build(t, []*upstream{upstreamOK("m", jsonContent)}, "sk-test-1")

	body := structuredBody(responsesBody("hola"), json.RawMessage(`{"type":"object"}`))
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}

	// P5/I4: shape P10 de 001 intacto; content[0].text == content del provider.
	out := decodeResponses(t, raw)
	if len(out.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(out.Output))
	}
	it := out.Output[0]
	if it.Type != "message" || it.Role != "assistant" || it.Status != "completed" {
		t.Fatalf("item = type:%q role:%q status:%q, want message/assistant/completed (P5)", it.Type, it.Role, it.Status)
	}
	if len(it.Content) != 1 || it.Content[0].Type != "output_text" || it.Content[0].Text != jsonContent {
		t.Fatalf("content = %+v, want [{output_text, %q}] (salida intacta, CA-2)", it.Content, jsonContent)
	}
}

// ---- BLOQUE B — rechazo de otros types (P4 → CA-3) ----

func TestE2E011002_P4_TipoNoJsonSchemaRechaza(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	body := responsesBody("hola")

	t.Run("type_object", func(t *testing.T) {
		b := cloneMap(body)
		b["text"] = map[string]any{"format": map[string]any{"type": "object", "name": "x"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "structured output not yet supported") // P4 (rama P8 conservada)
	})
	t.Run("type_text", func(t *testing.T) {
		b := cloneMap(body)
		b["text"] = map[string]any{"format": map[string]any{"type": "text"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "structured output not yet supported") // P4
	})
	t.Run("format_malformado", func(t *testing.T) {
		b := cloneMap(body)
		b["text"] = map[string]any{"format": "no-schema"} // format no es objeto con type legible
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "structured output not yet supported") // P4 (D3)
	})
}

// ---- BLOQUE C — pipeline compartida / cache por schema (P6 → CA-4) ----

func TestE2E011002_P6_CachePorSchema(t *testing.T) {
	schemaA := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	schemaB := json.RawMessage(`{"type":"object","properties":{"b":{"type":"integer"}}}`)

	// bodyWith arma un body Responses determinístico (temperature==0, cache-
	// eligible, P12 de 001) con la schema dada.
	bodyWith := func(schema json.RawMessage) map[string]any {
		body := structuredBody(responsesBody("hola"), schema)
		body["temperature"] = 0
		return body
	}

	c := &countingUpstream011{inner: upstreamOK("m", "hola")}
	h := buildCounting011(t, c, "sk-test-1")
	h.proxySrv.SetResponseCache(true, 512, time.Hour)

	// Misma schema, 1ra vez: MISS → 1 llamada upstream.
	if code, _ := responsesAs(t, h.srv.URL, h.key, bodyWith(schemaA)); code != 200 {
		t.Fatalf("schema A (1ra) status = %d, want 200", code)
	}
	if got := c.callCount(); got != 1 {
		t.Fatalf("schema A (1ra): upstream calls = %d, want 1 (miss cache)", got)
	}
	// Misma schema, 2da vez: HIT → NO re-llama (la schema participa de la key).
	if code, _ := responsesAs(t, h.srv.URL, h.key, bodyWith(schemaA)); code != 200 {
		t.Fatalf("schema A (2da) status = %d, want 200", code)
	}
	if got := c.callCount(); got != 1 {
		t.Fatalf("schema A (2da): upstream calls = %d, want 1 (hit cache por schema, CA-4)", got)
	}
	// Schema distinta: MISS → re-llama.
	if code, _ := responsesAs(t, h.srv.URL, h.key, bodyWith(schemaB)); code != 200 {
		t.Fatalf("schema B status = %d, want 200", code)
	}
	if got := c.callCount(); got != 2 {
		t.Fatalf("schema B: upstream calls = %d, want 2 (miss, schema distinta, CA-4)", got)
	}
}

// ---- BLOQUE D — error upstream sin degradación (P7 → CA-5) ----

func TestE2E011002_P7_Upstream4xxSinDegradar(t *testing.T) {
	c := &countingUpstream011{inner: upstreamFail(400)}
	h := buildCounting011(t, c, "sk-test-1")

	body := structuredBody(responsesBody("hola"), json.RawMessage(`{"type":"object"}`))
	code, raw := responsesAs(t, h.srv.URL, h.key, body)
	if code != 400 {
		t.Fatalf("status = %d, want 400 (propagado del upstream, P7/D5); body=%s", code, raw)
	}
	assertErrorEnvelope(t, raw)

	// No reintenta con fallback: exactamente 1 llamada upstream (D5).
	if got := c.callCount(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (sin retry ni fallback); body=%s", got, raw)
	}
	// El ÚNICO request enviado incluyó response_format: mofgw NO degradó a
	// json_object antes de enviar (D5).
	w := decodeWire(t, c.inner.gotBody)
	if w.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format.type = %q, want json_schema (sin degradar a json_object, D5): %s", w.ResponseFormat.Type, c.inner.gotBody)
	}
}
