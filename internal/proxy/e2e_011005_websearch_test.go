// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 011-005-web-search: grounded search server-side en
// POST /v1/responses — detectar `web_search_preview` (D1) → derivar query del
// último mensaje user (D2) → buscar en DDG (D3) → inyectar system message
// grounded (D4) → remover `web_search_preview` del upstream (D1) → item
// message (P6). Gating por `web_search.enabled` (D5): false (default) → 400
// conservado de D3 de 003 (sin regresión); true → flujo web search.
// Verifican las postcondiciones P1-P8 del spec
// docs/specs/011-005-web-search/spec.md.
//
// Contrato observable (CDAD stage-3-tdd): HTTP público hacia Server.Handler()
// — status, envelope de error wire, body Responses de respuesta, y el wire
// upstream (upstream.gotBody) para la inyección/remoción; upstreams fake en
// httptest, cero red externa (I6). El cliente DDG es una dependencia externa
// inyectada (D3, spec §251-259), por lo que su mock es legítimo (NO plumbing
// interno del handler).
//
// IMPORTANTE — qué NO se modifica: los tests existentes de `web_search_preview`
// → 400 en e2e_011001 (bloque F) y e2e_011003 (P2) siguen verdes porque corren
// contra la config default (`web_search.enabled:false`, P1/D5). Este archivo
// solo agrega los tests del flujo enabled:true.
//
// RED especial: el flujo enabled:true requiere un cliente DDG inyectado que aún
// no existe. Este archivo define la interfaz `webSearchClient` (el contrato del
// cliente DDG que el implementer debe implementar en producción) y el helper
// `buildWithWebSearch`, que inyecta el mock vía un setter `SetWebSearch` en el
// Server. Como ese setter aún no existe, los tests del flujo enabled:true
// fallan EN COMPILACIÓN (RED correcto: la feature no está implementada). El
// contrato del setter está documentado en buildWithWebSearch para el implementer.
//
// Mapeo audit (docs/specs/011-005-web-search/test-audit.md): 10 tests en 8
// bloques — A=P1, B=P2, C=P3, D=P4, E=P5, F=P6, G=P7, H=P8. CA-9 (rechazos
// conservados con enabled:false) lo cubren los tests untouched.

package proxy_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- contrato del cliente DDG (definido por el test-writer, D3/P3) ----

// webSearchClient es la interfaz del buscador DDG que 011-005 inyecta en el
// Server. La define el test-writer para permitir mock; el implementer la
// implementa en el paquete de producción (spec §251-259). El método Search
// recibe la query (P2) y devuelve top-N {title,url,snippet} (P3) o un error
// (que degrada a best-effort, P7).
type webSearchClient interface {
	Search(query string) ([]webSearchResult, error)
}

// webSearchResult es el shape observable de un resultado DDG (P3/P4): cada item
// inyectado en el system message lleva Title/URL/Snippet. El implementer debe
// declarar en producción el MISMO shape (campos Title/URL/Snippet string) para
// que el mock de este archivo satisfaga la interfaz estructuralmente.
type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// ---- mock del cliente DDG ----

// mockWebSearch es un fake de webSearchClient: registra las queries recibidas
// (P2/P7) y devuelve una lista fija de resultados o un error fijo. Thread-safe
// (las peticiones HTTP corren en goroutines del server).
type mockWebSearch struct {
	mu      sync.Mutex
	queries []string
	results []webSearchResult
	err     error
}

func (m *mockWebSearch) Search(query string) ([]webSearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries = append(m.queries, query)
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func (m *mockWebSearch) setResults(r []webSearchResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = r
}

func (m *mockWebSearch) setErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *mockWebSearch) gotQueries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.queries...)
}

// ---- harness con web_search.enabled:true + cliente DDG inyectado ----

// buildWithWebSearch construye el Server con web_search.enabled:true y el
// cliente DDG inyectado, para el flujo activado de 011-005 (P1).
//
// CONTRATO PARA EL IMPLEMENTER (011-005, GREEN):
//   - El Server (internal/proxy) debe exponer un setter
//         func (s *Server) SetWebSearch(enabled bool, client webSearchClient)
//     siguiendo el patrón SetResponseCache/SetPricing/SetModelMetadata
//     (proxy.go:141/296/305). Con enabled:true + `web_search_preview` se activa
//     el flujo web search (P1/D5).
//   - La interfaz `webSearchClient` y el tipo `webSearchResult` definidos arriba
//     son el contrato observable del cliente DDG (D3, P3): el paquete de
//     producción debe declarar la MISMA interfaz (Search(query) []result) y un
//     result con los mismos campos (Title/URL/Snippet string) para que el mock
//     la satisfaga estructuralmente.
//   - En RED este setter NO existe → los tests del flujo enabled:true fallan en
//     compilación (RED correcto: la feature no está implementada).
func buildWithWebSearch(t *testing.T, ups []*upstream, clientKey string, client webSearchClient) *harness {
	t.Helper()
	h := buildMultiClient(t, ups, clientKey)
	h.proxySrv.SetWebSearch(true, client)
	return h
}

// ---- helpers de aserción sobre la inyección del system message (P4) ----

// wireSystemMessage devuelve el content del primer mensaje role:"system" del
// wire upstream y ok=true; ok=false si no hay ningún mensaje system (P7).
func wireSystemMessage(t *testing.T, gotBody string) (string, bool) {
	t.Helper()
	for _, m := range wireMessages(t, gotBody) {
		if m["role"] == "system" {
			content, _ := m["content"].(string)
			return content, true
		}
	}
	return "", false
}

// groundingItemLines devuelve las líneas de item (1..N) del system message
// grounded: todas las líneas no vacías salvo el header `[Web search results
// for "..."]. Devuelve vacío si no hay items.
func groundingItemLines(content string) []string {
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "[Web search results for ") {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// countGroundingItems devuelve cuántos items numerados tiene el system message
// grounded (P4: exactamente N = max_results, ≤ los top-N de P3).
func countGroundingItems(content string) int {
	return len(groundingItemLines(content))
}

// ---- BLOQUE A — gating por web_search.enabled (P1) ----

// test_postcondition_1 (P1): con enabled:true, `web_search_preview` NO dispara
// el 400 de D3 de 003 — activa el flujo (no 400, item message P6); en cambio un
// tool type!="function" no-web-search (code_interpreter) sigue cayendo en el
// 400 en ambos modos.
func TestE2E011005_P1_GatingEnabledTrue(t *testing.T) {
	t.Run("web_search_preview_no_400", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		mock.setResults([]webSearchResult{{Title: "t", URL: "http://u", Snippet: "s"}})
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		b := responsesBody("clima en París")
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		if code != 200 {
			t.Fatalf("status = %d, want 200 (web search activo, P1); body=%s", code, raw)
		}
		out := decodeResponses(t, raw)
		if len(out.Output) != 1 || out.Output[0].Type != "message" {
			t.Fatalf("output = %+v, want un item message (P1/P6)", out.Output)
		}
	})
	t.Run("code_interpreter_400", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		b := responsesBody("hola")
		b["tools"] = []any{map[string]any{"type": "code_interpreter"}}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		assertReject(t, code, raw, "tool calling not yet supported") // P1 (no-web-search no-function)
	})
}

// ---- BLOQUE B — derivación de la query (P2) ----

// test_postcondition_2a (P2, I3): query == concatenación de los text de las
// parts input_text del ÚLTIMO item message con role=="user", y el cliente DDG
// mock recibió exactamente esa query.
func TestE2E011005_P2_QueryUltimoMensajeUser(t *testing.T) {
	u := upstreamOK("m", "hola")
	mock := &mockWebSearch{}
	mock.setResults([]webSearchResult{{Title: "r1", URL: "http://u1", Snippet: "s1"}})
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("")
	b["input"] = []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "clima en"}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "input_text", "text": "intermedio"}}},
		// último item user: dos parts input_text → query == concat "y temperatura"
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "y "},
			map[string]any{"type": "input_text", "text": "temperatura"},
		}},
	}
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P2); body=%s", code, raw)
	}

	qs := mock.gotQueries()
	if len(qs) != 1 {
		t.Fatalf("DDG queries = %d, want 1 (P2)", len(qs))
	}
	// Solo el ÚLTIMO item user (assistant intermedio y primer user descartados).
	if qs[0] != "y temperatura" {
		t.Fatalf("query = %q, want %q (concat del último mensaje user, P2)", qs[0], "y temperatura")
	}
}

// test_postcondition_2b (P2, I3): determinismo — mismo input[] → misma query
// en requests idénticos.
func TestE2E011005_P2_QueryDeterminista(t *testing.T) {
	u := upstreamOK("m", "hola")
	mock := &mockWebSearch{}
	mock.setResults([]webSearchResult{{Title: "r1", URL: "http://u1", Snippet: "s1"}})
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("clima en París")
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	for i := 0; i < 2; i++ {
		if code, raw := responsesAs(t, h.srv.URL, h.key, b); code != 200 {
			t.Fatalf("request %d: status = %d, want 200; body=%s", i+1, code, raw)
		}
	}

	qs := mock.gotQueries()
	if len(qs) != 2 {
		t.Fatalf("DDG queries = %d, want 2 (P2)", len(qs))
	}
	if qs[0] != qs[1] {
		t.Fatalf("query no determinista: %v vs %v (I3, P2)", qs[0], qs[1])
	}
	if qs[0] != "clima en París" {
		t.Fatalf("query = %q, want %q (P2)", qs[0], "clima en París")
	}
}

// ---- BLOQUE C — ejecución DDG (P3) ----

// test_postcondition_3 (P3/P4): el mock devuelve N resultados; el system
// message inyecta exactamente min(N, max_results) items. El fallback de scrape
// HTML → Instant Answer es interno al cliente DDG de producción (no observable
// a través de la interfaz inyectada); lo que el contrato exige — top-N
// consumido con N ≤ max_results — se verifica con el mock.
func TestE2E011005_P3_TopNConsumido(t *testing.T) {
	t.Run("mock_5_resultados_top_3", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		mock.setResults([]webSearchResult{
			{Title: "a", URL: "http://a", Snippet: "sa"},
			{Title: "b", URL: "http://b", Snippet: "sb"},
			{Title: "c", URL: "http://c", Snippet: "sc"},
			{Title: "d", URL: "http://d", Snippet: "sd"},
			{Title: "e", URL: "http://e", Snippet: "se"},
		})
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		b := responsesBody("clima")
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		if code, raw := responsesAs(t, h.srv.URL, h.key, b); code != 200 {
			t.Fatalf("status = %d, want 200 (P3); body=%s", code, raw)
		}
		content, ok := wireSystemMessage(t, u.gotBody)
		if !ok {
			t.Fatalf("sin system message grounded (P4): %s", u.gotBody)
		}
		if !strings.HasPrefix(content, `[Web search results for "clima"]`) {
			t.Fatalf("header ausente/incorrecto: %q (P4)", content)
		}
		// 5 resultados → solo los top-3 (max_results default 3) se consumen.
		if n := countGroundingItems(content); n != 3 {
			t.Fatalf("grounding items = %d, want 3 (top-N, P3/P4): %q", n, content)
		}
		// cada item lleva title | url | snippet.
		for _, line := range groundingItemLines(content) {
			if !strings.Contains(line, " | ") {
				t.Fatalf("item sin title|url|snippet: %q (P4)", line)
			}
		}
	})
	t.Run("mock_1_resultado_menos_que_N", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		mock.setResults([]webSearchResult{{Title: "solo", URL: "http://solo", Snippet: "snip"}})
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		b := responsesBody("clima")
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		if code, raw := responsesAs(t, h.srv.URL, h.key, b); code != 200 {
			t.Fatalf("status = %d, want 200 (P3); body=%s", code, raw)
		}
		content, ok := wireSystemMessage(t, u.gotBody)
		if !ok {
			t.Fatalf("sin system message grounded (P4): %s", u.gotBody)
		}
		// menos de max_results → se consumen los disponibles (N ≤ top-N, P4).
		if n := countGroundingItems(content); n != 1 {
			t.Fatalf("grounding items = %d, want 1 (≤ N, P3/P4): %q", n, content)
		}
	})
}

// ---- BLOQUE D — inyección como system message prependido (P4) ----

// test_postcondition_4 (P4, I5): wire messages[0] es role:"system" prependido
// ANTES del primer mensaje del input[], con header `[Web search results for
// "<query>"]` + exactamente los items disponibles (title|url|snippet), y NO se
// emiten anotaciones url_citation.
func TestE2E011005_P4_InyeccionSystemMessage(t *testing.T) {
	u := upstreamOK("m", "hola")
	mock := &mockWebSearch{}
	mock.setResults([]webSearchResult{
		{Title: "Titulo Uno", URL: "https://u1", Snippet: "Snippet uno"},
		{Title: "Titulo Dos", URL: "https://u2", Snippet: "Snippet dos"},
	})
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("clima en París")
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P4); body=%s", code, raw)
	}

	msgs := wireMessages(t, u.gotBody)
	if len(msgs) == 0 {
		t.Fatalf("wire sin messages: %s", u.gotBody)
	}
	// messages[0] == system prependido antes del primer mensaje del input (D4).
	if msgs[0]["role"] != "system" {
		t.Fatalf("messages[0].role = %v, want system (prependido, D4/P4): %s", msgs[0]["role"], u.gotBody)
	}
	content, _ := msgs[0]["content"].(string)
	if !strings.HasPrefix(content, `[Web search results for "clima en París"]`) {
		t.Fatalf("header incorrecto: %q (P4)", content)
	}
	// exactamente los items disponibles (2).
	if n := countGroundingItems(content); n != 2 {
		t.Fatalf("grounding items = %d, want 2 (P4): %q", n, content)
	}
	// I5/D4: sin anotaciones url_citation en el wire.
	if strings.Contains(u.gotBody, "url_citation") {
		t.Fatalf("url_citation emitido (I5/D4, no se emiten): %s", u.gotBody)
	}
}

// ---- BLOQUE E — remoción de web_search_preview del upstream (P5) ----

// test_postcondition_5 (P5): el wire NO contiene `web_search_preview` en tools;
// los function tools se traducen como 003 P1 (anidados, parameters byte-
// idénticos); si no queda ningún function tool, el campo `tools` no se emite.
func TestE2E011005_P5_RemocionWebSearchPreview(t *testing.T) {
	t.Run("function_se_traduce_web_search_se_remueve", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		mock.setResults([]webSearchResult{{Title: "t", URL: "http://u", Snippet: "s"}})
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		paramsRaw := `{"type":"object","properties":{"location":{"type":"string"}}}`
		b := responsesBody("hola")
		b["tools"] = []any{
			map[string]any{"type": "web_search_preview"},
			map[string]any{"type": "function", "name": "get_weather", "description": "d", "parameters": json.RawMessage(paramsRaw)},
		}
		code, raw := responsesAs(t, h.srv.URL, h.key, b)
		if code != 200 {
			t.Fatalf("status = %d, want 200 (P5); body=%s", code, raw)
		}

		w := decodeToolWire(t, u.gotBody)
		if len(w.Tools) != 1 {
			t.Fatalf("wire tools = %d, want 1 (solo el function, P5): %s", len(w.Tools), u.gotBody)
		}
		// web_search_preview removido; el function se traduce como 003 P1.
		if w.Tools[0].Type != "function" || w.Tools[0].Function.Name != "get_weather" {
			t.Fatalf("tools[0] = %+v, want function/get_weather (P5/003 P1)", w.Tools[0])
		}
		// I3 (003): parameters byte-idénticos al input.
		if string(w.Tools[0].Function.Parameters) != paramsRaw {
			t.Fatalf("parameters no byte-idéntico al input (I3, P5):\nwant %s\ngot  %s", paramsRaw, w.Tools[0].Function.Parameters)
		}
	})
	t.Run("solo_web_search_tools_ausente", func(t *testing.T) {
		u := upstreamOK("m", "hola")
		mock := &mockWebSearch{}
		mock.setResults([]webSearchResult{{Title: "t", URL: "http://u", Snippet: "s"}})
		h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

		b := responsesBody("hola")
		b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		if code, raw := responsesAs(t, h.srv.URL, h.key, b); code != 200 {
			t.Fatalf("status = %d, want 200 (P5); body=%s", code, raw)
		}
		// no queda ningún function tool → tools NO se emite (P5).
		w := bodyField(t, u.gotBody)
		if _, ok := w["tools"]; ok {
			t.Fatalf("wire tools presente, quería ausente (P5): %s", u.gotBody)
		}
	})
}

// ---- BLOQUE F — salida item message (P6) ----

// test_postcondition_6 (P6): output[] con EXACTAMENTE un item message
// assistant/completed, content[0].text == choices[0].message.content, y sin
// items function_call por la web search (la tool fue removida, P5).
func TestE2E011005_P6_SalidaItemMessage(t *testing.T) {
	u := upstreamOK("m", "respuesta grounded")
	mock := &mockWebSearch{}
	mock.setResults([]webSearchResult{{Title: "t", URL: "http://u", Snippet: "s"}})
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("clima")
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (P6); body=%s", code, raw)
	}

	out := decodeResponses(t, raw)
	if len(out.Output) != 1 {
		t.Fatalf("output len = %d, want 1 (P6); output=%+v", len(out.Output), out.Output)
	}
	it := out.Output[0]
	if it.Type != "message" || it.Role != "assistant" || it.Status != "completed" {
		t.Fatalf("item = type:%q role:%q status:%q, want message/assistant/completed (P6)", it.Type, it.Role, it.Status)
	}
	if len(it.Content) != 1 || it.Content[0].Type != "output_text" || it.Content[0].Text != "respuesta grounded" {
		t.Fatalf("content = %+v, want [{output_text, \"respuesta grounded\"}] (P6/P10 de 001)", it.Content)
	}
	// len==1 + type message ⇒ cero items function_call por la web search (P5/P6).
}

// ---- BLOQUE G — fallo best-effort (P7) ----

// test_postcondition_7a (P7, I2): DDG falla (mock error) → NO 400, no se
// inyecta system message, `web_search_preview` igualmente removido (P5), y la
// respuesta es un item message ungrounded. Nunca se traduce un fallo de DDG en
// un 4xx/5xx al cliente.
func TestE2E011005_P7_DDGFallaBestEffort(t *testing.T) {
	u := upstreamOK("m", "ungrounded")
	mock := &mockWebSearch{}
	mock.setErr(errors.New("ddg 5xx timeout"))
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("clima")
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (best-effort, P7/I2); body=%s", code, raw)
	}
	// sin system message inyectado (no grounding).
	if content, ok := wireSystemMessage(t, u.gotBody); ok {
		t.Fatalf("system message inyectado pese al fallo de DDG: %q (P7)", content)
	}
	// web_search_preview igualmente removido (P5).
	if tools, ok := bodyField(t, u.gotBody)["tools"].([]any); ok {
		for _, tw := range tools {
			if tm, ok := tw.(map[string]any); ok && tm["type"] == "web_search_preview" {
				t.Fatalf("web_search_preview presente en wire tras fallo (P5/P7): %s", u.gotBody)
			}
		}
	}
	// respuesta ungrounded: un solo item message (P6).
	out := decodeResponses(t, raw)
	if len(out.Output) != 1 || out.Output[0].Type != "message" {
		t.Fatalf("output = %+v, want un item message ungrounded (P6/P7)", out.Output)
	}
}

// test_postcondition_7b (P7, P2): sin ningún item message user en input[] → no
// hay query → el flujo web search se saltea: no se inyecta system message, DDG
// no se consulta, no 400, web_search_preview removido (P5).
func TestE2E011005_P7_SinMensajeUserSinGrounding(t *testing.T) {
	u := upstreamOK("m", "x")
	mock := &mockWebSearch{}
	mock.setResults([]webSearchResult{{Title: "t", URL: "http://u", Snippet: "s"}})
	h := buildWithWebSearch(t, []*upstream{u}, "sk-test-1", mock)

	b := responsesBody("hola")
	// input sin ningún item message con role user (solo un item del ciclo).
	b["input"] = []any{
		map[string]any{"type": "function_call", "id": "resp_1", "call_id": "call_a", "name": "f", "arguments": `{}`},
	}
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
	code, raw := responsesAs(t, h.srv.URL, h.key, b)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (sin user → skip, P2/P7); body=%s", code, raw)
	}
	// sin grounding: no system message inyectado.
	if content, ok := wireSystemMessage(t, u.gotBody); ok {
		t.Fatalf("sin mensaje user se inyectó system message: %q (P2/P7)", content)
	}
	// DDG no fue consultado (no hay query, P2/P7).
	if qs := mock.gotQueries(); len(qs) != 0 {
		t.Fatalf("DDG consultado sin mensaje user: %v (P2/P7)", qs)
	}
}

// ---- BLOQUE H — pipeline intacta / cache excluido para web search (P8) ----

// test_postcondition_8 (P8, CA-8): un request que activa el flujo web search
// (enabled:true + web_search_preview) NUNCA se sirve del response cache ni se
// almacena, aunque temperature==0 (los resultados DDG cambian en el tiempo).
// Observable: dos requests con el MISMO body (temperature==0) y cache habilitada
// llaman al upstream DOS veces (ambos MISS) y devuelven resultados distintos al
// cambiar el mock DDG de A a B (nunca el cacheado A).
func TestE2E011005_P8_CacheExcluidoParaWebSearch(t *testing.T) {
	// Upstream que cuenta llamadas y responde con el texto de la grounding del
	// primer system message: así el output refleja los resultados DDG (A vs B).
	var mu sync.Mutex
	calls := 0
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		mu.Unlock()
		grounding := ""
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				grounding = m.Content
				break
			}
		}
		if grounding == "" {
			grounding = "(ungrounded)"
		}
		out, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-up", "object": "chat.completion", "model": "m",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": grounding}, "finish_reason": "stop",
			}},
			"usage": map[string]any{"total_tokens": 3},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(out)
	})
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	cl := provider.NewClient("up1", srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
	r := router.New([]router.ProviderSpec{{Provider: cl}}, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey("sk-test-1")}})
	m := metrics.New()
	s := proxy.New(r, []provider.Provider{cl}, authz, m, nil, nil, 10<<20, nil, nil, 0)
	s.SetResponseCache(true, 512, time.Hour)
	mock := &mockWebSearch{}
	s.SetWebSearch(true, mock) // CONTRATO: setter agregado por el implementer (ver buildWithWebSearch)
	hsrv := httptest.NewServer(s.Handler())
	t.Cleanup(hsrv.Close)

	b := cloneMap(responsesBody("clima"))
	b["temperature"] = 0
	b["tools"] = []any{map[string]any{"type": "web_search_preview"}}

	// request 1: mock DDG devuelve resultado A.
	mock.setResults([]webSearchResult{{Title: "ResultadoA", URL: "http://a", Snippet: "snip-a"}})
	code1, raw1 := responsesAs(t, hsrv.URL, "sk-test-1", b)
	if code1 != 200 {
		t.Fatalf("request 1: status = %d, want 200; body=%s", code1, raw1)
	}

	// request 2 (MISMO body, temperature==0): mock DDG devuelve resultado B.
	mock.setResults([]webSearchResult{{Title: "ResultadoB", URL: "http://b", Snippet: "snip-b"}})
	code2, raw2 := responsesAs(t, hsrv.URL, "sk-test-1", b)
	if code2 != 200 {
		t.Fatalf("request 2: status = %d, want 200; body=%s", code2, raw2)
	}

	// nunca se sirvió del cache: ambos requests llegaron al upstream (calls==2).
	// Con cache habilitada y temperature==0, un request NO web search habría
	// hecho el 2do HIT (calls==1).
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("upstream calls = %d, want 2 (web search nunca cacheado, P8/CA-8)", got)
	}

	// outputs distintos (A ≠ B): el request 2 no devolvió el cacheado A.
	out1 := decodeResponses(t, raw1)
	out2 := decodeResponses(t, raw2)
	t1 := out1.Output[0].Content[0].Text
	t2 := out2.Output[0].Content[0].Text
	if t1 == t2 {
		t.Fatalf("outputs iguales (%q): el request web search se sirvió del cache (P8)", t1)
	}
	if !strings.Contains(t1, "ResultadoA") || !strings.Contains(t2, "ResultadoB") {
		t.Fatalf("outputs no reflejan los mocks A/B: %q / %q (P8)", t1, t2)
	}
}
