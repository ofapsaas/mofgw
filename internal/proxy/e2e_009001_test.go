// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-001-context-composition — P2/P3/P4/P6/P7
// (C5-C10, C13, C15). Verifican el endpoint GET /v1/context con
// aislamiento por clientID y el wiring del proxy: fase 1 RecordContext en
// handleChat (incluidos los rechazos), fase 2 UpdateContextUsage post-
// response (stream y no-stream), setter SetContextAnalysis (disabled por
// default) y privacidad. Cero red externa (upstreams fake en httptest).
//
// El endpoint responde (P3): con ?session=<id> → 200 con el record
// client|<id> (404 si no existe); sin session → 200 scope:"client" con el
// agregado client| (ceros/vacío sin data, nunca nil). El JSON usa keys
// snake_case: {client, scope, session, requests, summary, latest,
// history}; summary = {roles, part_types, num_tools_total,
// prompt_tokens_estimated, prompt_tokens_actual, first_request_at,
// last_request_at}; cada entry = {request_id, model, roles, part_types,
// num_tools, total_bytes, prompt_tokens_estimated, prompt_tokens_actual,
// request_at} (prompt_tokens_actual null = fase 2 no corrió).

package proxy_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- harness con análisis de contexto ----

// buildContextAnalysis construye el harness con análisis de contexto
// habilitado (009-001 P6 wiring): SetContextAnalysis(true, 50) sobre el
// proxy y SetContextHistoryPerSession(50) sobre metrics (C5-C9, C13, C15).
func buildContextAnalysis(t *testing.T, ups []*upstream, clientKey string) *harness {
	t.Helper()
	h := &harness{ups: ups, key: clientKey}
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, nil, 10<<20, nil, nil, 0)
	s.SetContextAnalysis(true, 50)
	m.SetContextHistoryPerSession(50)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// buildContextMultiClient construye un proxy con VARIOS clientes auth
// (C6 aislamiento). Devuelve el server compartido y un harness por
// cliente; el caller asigna h.key = <key plana del cliente i> para emitir
// requests como ese cliente (auth.Client solo expone el hash).
func buildContextMultiClient(t *testing.T, ups []*upstream, clients []auth.Client) (*httptest.Server, []*harness) {
	t.Helper()
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, nil, 10<<20, nil, nil, 0)
	s.SetContextAnalysis(true, 50)
	m.SetContextHistoryPerSession(50)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	hs := make([]*harness, len(clients))
	for i := range clients {
		hs[i] = &harness{srv: srv, ups: ups, clients: clients, proxySrv: s}
	}
	return srv, hs
}

// ---- helpers del endpoint ----

// getContext GETea /v1/context con la key del cliente; session "" = sin
// query. Devuelve status y body parseado.
func getContext(t *testing.T, srvURL, key, session string) (int, map[string]any) {
	t.Helper()
	u := srvURL + "/v1/context"
	if session != "" {
		u += "?session=" + url.QueryEscape(session)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", u, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// waitContextBody espera (hasta 2s) a que /v1/context cumpla pred (fase 2
// de usage es post-respuesta; el stream puede terminar de escribir justo
// después de que el cliente lea el body). Devuelve el último body leído.
func waitContextBody(t *testing.T, srvURL, key, session string, pred func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, body := getContext(t, srvURL, key, session)
		if pred(body) {
			return body
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, body := getContext(t, srvURL, key, session)
	return body
}

func findRoleAgg(roles []any, role string) map[string]any {
	for _, r := range roles {
		m, ok := r.(map[string]any)
		if ok && m["role"] == role {
			return m
		}
	}
	return nil
}

func findPartAgg(parts []any, typ string) map[string]any {
	for _, p := range parts {
		m, ok := p.(map[string]any)
		if ok && m["type"] == typ {
			return m
		}
	}
	return nil
}

// ---- upstreams con usage.prompt_tokens (C8) ----

// upstreamUsagePrompt responde no-stream con usage.prompt_tokens explícito.
func upstreamUsagePrompt(model string, promptTokens int) *upstream {
	return &upstream{
		status: 200,
		model:  model,
		body: fmt.Sprintf(`{"id":"chatcmpl-up","object":"chat.completion","model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"total_tokens":%d}}`,
			model, promptTokens, promptTokens),
	}
}

// upstreamStreamUsage responde SSE con usage.prompt_tokens en un chunk
// final (formato include_usage que CaptureUsage extrae — 003-001 P4).
func upstreamStreamUsage(model string, promptTokens int) *upstream {
	body := fmt.Sprintf("data: {\"id\":\"chatcmpl-s1\",\"model\":\"%s\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\ndata: {\"id\":\"chatcmpl-s1\",\"model\":\"%s\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"total_tokens\":%d}}\n\ndata: [DONE]\n\n",
		model, model, promptTokens, promptTokens)
	return &upstream{status: 200, stream: true, model: model, body: body}
}

// ---- C5 — acumulación por sesión ----

// test_postcondition_3_SessionAccumulation (C5): 2 requests con
// X-Session-Id "s1" y cuerpos distintos → GET /v1/context?session=s1 →
// 200 con requests:2, summary con roles agregados (est_tokens y pct
// computables) y history con 2 entries (más nuevo primero) con latest =
// el segundo request.
func TestE2E009001_SessionAccumulation(t *testing.T) {
	h := buildContextAnalysis(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// req1: system "system prompt" (13→3) + user "first message" (13→3) = est 6
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "system", "content": "system prompt"},
			{"role": "user", "content": "first message"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// req2: user "second question" (15→3) + assistant "answer" (6→1) = est 4
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "user", "content": "second question"},
			{"role": "assistant", "content": "answer"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	status, body := getContext(t, h.srv.URL, "sk-test-1", "s1")
	if status != 200 {
		t.Fatalf("GET /v1/context?session=s1 status = %d, want 200", status)
	}
	if body["client"] != "test" || body["scope"] != "session" || body["session"] != "s1" {
		t.Fatalf("body client/scope/session = %v/%v/%v", body["client"], body["scope"], body["session"])
	}
	if body["requests"] != float64(2) {
		t.Fatalf("requests = %v, want 2 (C5)", body["requests"])
	}

	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary ausente: %v", body)
	}
	if summary["num_tools_total"] != float64(0) {
		t.Fatalf("num_tools_total = %v, want 0", summary["num_tools_total"])
	}
	if summary["prompt_tokens_estimated"] != float64(10) {
		t.Fatalf("prompt_tokens_estimated = %v, want 10 (6+4)", summary["prompt_tokens_estimated"])
	}

	roles, _ := summary["roles"].([]any)
	sys := findRoleAgg(roles, "system")
	if sys == nil || sys["messages"] != float64(1) || sys["est_tokens"] != float64(3) {
		t.Fatalf("role system = %v, want 1 msg/3 est", sys)
	}
	user := findRoleAgg(roles, "user")
	if user == nil || user["messages"] != float64(2) || user["est_tokens"] != float64(6) {
		t.Fatalf("role user = %v, want 2 msg/6 est", user)
	}
	if pct, ok := user["pct"].(float64); !ok || pct < 0.599 || pct > 0.601 {
		t.Fatalf("role user pct = %v, want ~0.6 (6/10)", user["pct"])
	}
	asst := findRoleAgg(roles, "assistant")
	if asst == nil || asst["messages"] != float64(1) || asst["est_tokens"] != float64(1) {
		t.Fatalf("role assistant = %v, want 1 msg/1 est", asst)
	}

	parts, _ := summary["part_types"].([]any)
	text := findPartAgg(parts, "text")
	if text == nil || text["count"] != float64(4) || text["bytes"] != float64(47) || text["est_tokens"] != float64(10) {
		t.Fatalf("part text = %v, want count 4/bytes 47/est 10", text)
	}

	history, _ := body["history"].([]any)
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2 (C5)", len(history))
	}
	// Más nuevo primero: req2 (est 4) primero, req1 (est 6) segundo.
	h0, ok0 := history[0].(map[string]any)
	h1, ok1 := history[1].(map[string]any)
	if !ok0 || !ok1 {
		t.Fatalf("history entries mal tipadas: %v", history)
	}
	if h0["prompt_tokens_estimated"] != float64(4) || h1["prompt_tokens_estimated"] != float64(6) {
		t.Fatalf("history order = [%v %v], want [req2(4) req1(6)]", h0["prompt_tokens_estimated"], h1["prompt_tokens_estimated"])
	}
	if h0["request_id"] == h1["request_id"] || h0["request_id"] == "" {
		t.Fatalf("request_ids inválidos: %v %v", h0["request_id"], h1["request_id"])
	}

	latest, ok := body["latest"].(map[string]any)
	if !ok {
		t.Fatalf("latest ausente: %v", body)
	}
	if latest["prompt_tokens_estimated"] != float64(4) {
		t.Fatalf("latest.prompt_tokens_estimated = %v, want 4 (el segundo request — C5)", latest["prompt_tokens_estimated"])
	}
	if latest["model"] != "m" {
		t.Fatalf("latest.model = %v, want m", latest["model"])
	}
}

// ---- C6 — aislamiento por cliente ----

// test_postcondition_3_ClientIsolation (C6): 2 clientes con el MISMO
// session id "s1" → cada uno ve SOLO su propio record.
func TestE2E009001_ClientIsolation(t *testing.T) {
	keys := []string{"sk-alice-009", "sk-bob-009"}
	clients := []auth.Client{
		{ID: "alice", KeySHA256: auth.HashKey(keys[0])},
		{ID: "bob", KeySHA256: auth.HashKey(keys[1])},
	}
	srv, hs := buildContextMultiClient(t, []*upstream{upstreamOK("m", "x")}, clients)
	hs[0].key = keys[0]
	hs[1].key = keys[1]

	// alice: 1 request con sesión s1, content 12 chars → est 3.
	resp := chatWithHeaders(t, hs[0], keys[0], map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "alice prompt"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("alice status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// bob: 1 request con sesión s1, content 25 chars → est 6.
	resp = chatWithHeaders(t, hs[1], keys[1], map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "bob prompt longer content"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("bob status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// Cada cliente ve SOLO lo suyo (I3).
	statusA, bodyA := getContext(t, srv.URL, keys[0], "s1")
	if statusA != 200 || bodyA["requests"] != float64(1) {
		t.Fatalf("alice session = %d/%v, want 200/1", statusA, bodyA["requests"])
	}
	statusB, bodyB := getContext(t, srv.URL, keys[1], "s1")
	if statusB != 200 || bodyB["requests"] != float64(1) {
		t.Fatalf("bob session = %d/%v, want 200/1", statusB, bodyB["requests"])
	}

	// El agregado de alice NO incluye el tráfico de bob.
	sumA := bodyA["summary"].(map[string]any)
	if sumA["prompt_tokens_estimated"] != float64(3) {
		t.Fatalf("alice prompt_tokens_estimated = %v, want 3 (sin tráfico de bob — C6)", sumA["prompt_tokens_estimated"])
	}
	sumB := bodyB["summary"].(map[string]any)
	if sumB["prompt_tokens_estimated"] != float64(6) {
		t.Fatalf("bob prompt_tokens_estimated = %v, want 6", sumB["prompt_tokens_estimated"])
	}

	// Records de cliente también aislados.
	statusCA, bodyCA := getContext(t, srv.URL, keys[0], "")
	if statusCA != 200 || bodyCA["requests"] != float64(1) || bodyCA["scope"] != "client" {
		t.Fatalf("alice client = %d/%v, want 200/1", statusCA, bodyCA["requests"])
	}
	statusCB, bodyCB := getContext(t, srv.URL, keys[1], "")
	if statusCB != 200 || bodyCB["requests"] != float64(1) {
		t.Fatalf("bob client = %d/%v, want 200/1", statusCB, bodyCB["requests"])
	}
}

// ---- C7 — el record de cliente acumula TODO ----

// test_postcondition_3_ClientAggregatesAll (C7): 3 requests (2 con sesión,
// 1 sin) → GET /v1/context (sin session) muestra requests:3; el de la
// sesión existente, requests:2.
func TestE2E009001_ClientAggregatesAll(t *testing.T) {
	h := buildContextAnalysis(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	for i := 0; i < 2; i++ {
		resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "with session"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("req con sesión %d status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "without session"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req sin sesión status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// Sin query: el record de cliente acumula TODO (C7).
	status, body := getContext(t, h.srv.URL, "sk-test-1", "")
	if status != 200 {
		t.Fatalf("GET /v1/context status = %d, want 200", status)
	}
	if body["scope"] != "client" {
		t.Fatalf("scope = %v, want client", body["scope"])
	}
	if body["requests"] != float64(3) {
		t.Fatalf("client requests = %v, want 3 (acumula TODO — C7)", body["requests"])
	}

	// Con la sesión existente: solo los 2 requests con esa sesión.
	statusS, sessBody := getContext(t, h.srv.URL, "sk-test-1", "s1")
	if statusS != 200 {
		t.Fatalf("GET /v1/context?session=s1 status = %d, want 200", statusS)
	}
	if sessBody["requests"] != float64(2) {
		t.Fatalf("session s1 requests = %v, want 2 (C7)", sessBody["requests"])
	}
}

// ---- C8 — prompt_tokens_actual en dos fases ----

// test_postcondition_4_TwoPhaseUsage (C8): upstream fake devuelve
// usage.prompt_tokens:N → la entry (matcheada por request_id) y
// summary.prompt_tokens_actual reflejan N. Stream y no-stream.
func TestE2E009001_TwoPhaseUsage(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		h := buildContextAnalysis(t, []*upstream{upstreamUsagePrompt("m", 42)}, "sk-test-1")
		resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		body := waitContextBody(t, h.srv.URL, "sk-test-1", "s1", func(b map[string]any) bool {
			latest, ok := b["latest"].(map[string]any)
			return ok && latest["prompt_tokens_actual"] != nil
		})
		latest := body["latest"].(map[string]any)
		if latest["prompt_tokens_actual"] != float64(42) {
			t.Fatalf("latest.prompt_tokens_actual = %v, want 42 (C8 no-stream)", latest["prompt_tokens_actual"])
		}
		summary := body["summary"].(map[string]any)
		if summary["prompt_tokens_actual"] != float64(42) {
			t.Fatalf("summary.prompt_tokens_actual = %v, want 42", summary["prompt_tokens_actual"])
		}
	})

	t.Run("stream", func(t *testing.T) {
		h := buildContextAnalysis(t, []*upstream{upstreamStreamUsage("m", 77)}, "sk-test-1")
		resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model":    "m",
			"stream":   true,
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		body := waitContextBody(t, h.srv.URL, "sk-test-1", "s1", func(b map[string]any) bool {
			latest, ok := b["latest"].(map[string]any)
			return ok && latest["prompt_tokens_actual"] != nil
		})
		latest := body["latest"].(map[string]any)
		if latest["prompt_tokens_actual"] != float64(77) {
			t.Fatalf("latest.prompt_tokens_actual = %v, want 77 (C8 stream)", latest["prompt_tokens_actual"])
		}
		summary := body["summary"].(map[string]any)
		if summary["prompt_tokens_actual"] != float64(77) {
			t.Fatalf("summary.prompt_tokens_actual = %v, want 77", summary["prompt_tokens_actual"])
		}
	})
}

// ---- C9 — request rechazado ----

// test_postcondition_4_RejectedRequest (C9): request rechazado por
// context_window → la entry existe en history con est_tokens y SIN
// prompt_tokens_actual (fase 2 nunca corre).
func TestE2E009001_RejectedRequest(t *testing.T) {
	h := buildContextAnalysis(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, bigBody(5000))
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (ventana excedida)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	status, body := getContext(t, h.srv.URL, "sk-test-1", "s1")
	if status != 200 {
		t.Fatalf("GET /v1/context?session=s1 status = %d, want 200 (el rechazo igual registra — P4)", status)
	}
	latest, ok := body["latest"].(map[string]any)
	if !ok {
		t.Fatalf("latest ausente: %v", body)
	}
	if latest["prompt_tokens_estimated"] != float64(1250) {
		t.Fatalf("latest.prompt_tokens_estimated = %v, want 1250 (5000/4 — C9)", latest["prompt_tokens_estimated"])
	}
	if latest["prompt_tokens_actual"] != nil {
		t.Fatalf("latest.prompt_tokens_actual = %v, want null (sin respuesta upstream — C9)", latest["prompt_tokens_actual"])
	}
	summary := body["summary"].(map[string]any)
	if summary["prompt_tokens_actual"] != nil {
		t.Fatalf("summary.prompt_tokens_actual = %v, want null", summary["prompt_tokens_actual"])
	}
}

// ---- C10 — disabled por default ----

// test_postcondition_6_DisabledByDefault (C10): analysis ausente o
// enabled=false (default) → GET /v1/context responde 200 con ceros/vacío;
// ?session=unknown → 404; sin records en memoria.
func TestE2E009001_DisabledByDefault(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1") // build() NO habilita el análisis

	// El tráfico normal funciona igual (P8 no invasivo).
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// Sin data → 200 con ceros/vacío (nunca nil, consistente con
	// UsageSnapshot).
	status, body := getContext(t, h.srv.URL, "sk-test-1", "")
	if status != 200 {
		t.Fatalf("GET /v1/context status = %d, want 200 (C10)", status)
	}
	if body["requests"] != float64(0) {
		t.Fatalf("requests = %v, want 0 (sin records en memoria)", body["requests"])
	}
	if body["scope"] != "client" {
		t.Fatalf("scope = %v, want client", body["scope"])
	}
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary ausente: %v", body)
	}
	if roles, _ := summary["roles"].([]any); len(roles) != 0 {
		t.Fatalf("roles = %v, want vacío", roles)
	}
	if history, _ := body["history"].([]any); len(history) != 0 {
		t.Fatalf("history = %v, want vacío", history)
	}
	if body["latest"] != nil {
		t.Fatalf("latest = %v, want nil", body["latest"])
	}

	// ?session=unknown → 404 (P3/008-003 P5).
	statusS, _ := getContext(t, h.srv.URL, "sk-test-1", "unknown")
	if statusS != 404 {
		t.Fatalf("GET /v1/context?session=unknown status = %d, want 404 (C10)", statusS)
	}
}

// ---- C13 — privacidad ----

// test_postcondition_7_PrivacyGrepNegative (C13): strings distintivos en
// content/arguments → grep negativo en /v1/context (session y client).
func TestE2E009001_PrivacyGrepNegative(t *testing.T) {
	h := buildContextAnalysis(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	secrets := []string{"PRIVACY-CONTEXT-PROMPT-0001", "PRIVACY-CONTEXT-ARGS-0002"}

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": secrets[0]}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model": "m",
		"messages": []map[string]any{{
			"role":       "user",
			"content":    "hi",
			"tool_calls": []map[string]any{{"function": map[string]any{"name": "f", "arguments": secrets[1]}}},
		}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	assertNoSecrets := func(t *testing.T, body map[string]any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		for _, secret := range secrets {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("PRIVACIDAD: %q en /v1/context (C13/I1):\n%s", secret, raw)
			}
		}
	}

	_, sessBody := getContext(t, h.srv.URL, "sk-test-1", "s1")
	assertNoSecrets(t, sessBody)
	_, clientBody := getContext(t, h.srv.URL, "sk-test-1", "")
	assertNoSecrets(t, clientBody)
}

// ---- C15 — clientes sin sesión (openclaw/zot) ----

// test_postcondition_3_NoSessionClients (C15): requests SIN sesión →
// GET /v1/context muestra la composición agregada por client_id;
// GET /v1/context?session=<cualquier id> → 404.
func TestE2E009001_NoSessionClients(t *testing.T) {
	h := buildContextAnalysis(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")

	// 2 requests sin X-Session-Id (openclaw/zot): content 10 chars → est 2
	// cada uno; total est 4.
	for i := 0; i < 2; i++ {
		resp := h.chat(t, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "hello oclaw"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("req %d status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	status, body := getContext(t, h.srv.URL, "sk-test-1", "")
	if status != 200 {
		t.Fatalf("GET /v1/context status = %d, want 200", status)
	}
	if body["scope"] != "client" || body["requests"] != float64(2) {
		t.Fatalf("body scope/requests = %v/%v, want client/2 (C15)", body["scope"], body["requests"])
	}
	summary := body["summary"].(map[string]any)
	if summary["prompt_tokens_estimated"] != float64(4) {
		t.Fatalf("prompt_tokens_estimated = %v, want 4 (composición agregada por client_id)", summary["prompt_tokens_estimated"])
	}
	roles, _ := summary["roles"].([]any)
	if u := findRoleAgg(roles, "user"); u == nil || u["messages"] != float64(2) {
		t.Fatalf("role user = %v, want 2 msg", u)
	}

	// Sin sesión que consultar → 404 (P3/008-003 P5).
	statusS, _ := getContext(t, h.srv.URL, "sk-test-1", "whatever")
	if statusS != 404 {
		t.Fatalf("GET /v1/context?session=whatever status = %d, want 404 (C15)", statusS)
	}
}
