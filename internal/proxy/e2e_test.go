// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- upstreams fake ----

// upstream es un provider OpenAI-compatible fake (httptest).
type upstream struct {
	status       int         // status para /chat/completions
	body         string      // body de respuesta
	stream       bool        // responder como SSE
	model        string      // model que el upstream "sirve"
	gotBody      string      // último body recibido
	gotHeaders   http.Header // últimos headers recibidos (008-003 C5)
	modelsStatus int         // status para GET /v1/models (health check); 0 = 200
}

func (u *upstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		u.gotBody = string(raw)
		u.gotHeaders = r.Header.Clone()
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			if u.modelsStatus >= 400 {
				http.Error(w, "health down", u.modelsStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"`+u.model+`"}]}`)
			return
		}
		w.WriteHeader(u.status)
		if u.stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		io.WriteString(w, u.body)
	}
}

func upstreamOK(model, content string) *upstream {
	return &upstream{status: 200, body: `{"id":"chatcmpl-up","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"` + content + `"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`, model: model}
}

func upstreamStreamOK(model string) *upstream {
	return &upstream{status: 200, stream: true, model: model, body: "data: {\"id\":\"chatcmpl-s1\",\"model\":\"" + model + "\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"la\"}}]}\n\ndata: [DONE]\n\n"}
}

func upstreamFail(status int) *upstream {
	return &upstream{status: status, body: `{"error":{"message":"upstream boom","type":"server_error"}}`, model: "m"}
}

// ---- harness ----

type harness struct {
	srv     *httptest.Server
	ups     []*upstream
	clients []auth.Client
	key     string
	// proxySrv expone el Server del proxy para configurar features que
	// se inyectan post-construcción (006-002 SetPricing).
	proxySrv *proxy.Server
}

func build(t *testing.T, ups []*upstream, clientKey string) *harness {
	t.Helper()
	return buildWithLogger(t, ups, clientKey, nil)
}

// buildWithLogger igual que build pero inyecta un logger (debug capturado)
// en el router y el proxy, para tests de eventos 005-005-verbose.
func buildWithLogger(t *testing.T, ups []*upstream, clientKey string, logger *slog.Logger) *harness {
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
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, logger) // cooldown 0 en tests unitarios (cada provider distinto)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, logger, 10<<20, nil, nil, 0)
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// buildWithOptions igual que build pero con Options completos
// (EPIC-002: retry, health, degradación) y retorno del router.
func buildWithOptions(t *testing.T, ups []*upstream, clientKey string, o router.Options) (*harness, *router.Router) {
	t.Helper()
	h := &harness{ups: ups, key: clientKey}
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	clients := make([]*provider.Client, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		clients[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	// Health store (002-002): registra los providers y sondea una vez.
	if o.Health != nil {
		for _, cl := range clients {
			o.Health.Register(cl)
		}
		o.Health.CheckNow()
	}
	r := router.NewWithOptions(specs, o)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, o.Logger, 10<<20, nil, nil, 0)
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, r
}

func (h *harness) healthz(t *testing.T) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func (h *harness) chat(t *testing.T, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// ---- tests ----

func TestE2EHealthzNoAuth(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")
	resp, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestE2EMetricsNoAuth(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")
	resp, err := http.Get(h.srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "mofgw_requests_total") {
		t.Fatalf("metrics body no contiene contadores: %s", body)
	}
}

func TestE2EAuthRequired(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")

	// sin header → 401
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("sin auth: status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_api_key") {
		t.Fatalf("body sin invalid_api_key: %s", body)
	}

	// key desconocida → 401
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("key inválida: status = %d, want 401", resp.StatusCode)
	}
}

func TestE2EChatSuccess(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "hola mundo")}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Choices[0].Message.Content != "hola mundo" {
		t.Fatalf("content = %q", out.Choices[0].Message.Content)
	}
	if out.Model != "m" {
		t.Fatalf("model = %q, want m (el que pidió el cliente)", out.Model)
	}
}

func TestE2EFallbackTransparent(t *testing.T) {
	// provider A caído (500), provider B OK → el cliente ve 200 sin enterarse
	h := build(t, []*upstream{upstreamFail(500), upstreamOK("m", "rescate")}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (fallback transparente)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "rescate") {
		t.Fatalf("respuesta no viene del provider B: %s", body)
	}
	if strings.Contains(string(body), "boom") {
		t.Fatal("el cliente ve el error del provider A — transparencia rota")
	}
}

func TestE2EAllProvidersFail502(t *testing.T) {
	h := build(t, []*upstream{upstreamFail(500), upstreamFail(503)}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "upstream_error") {
		t.Fatalf("body sin upstream_error: %s", body)
	}
	if strings.Contains(string(body), "sk-upstream") || strings.Contains(string(body), "127.0.0.1") {
		t.Fatal("respuesta filtra keys/URLs internas")
	}
}

func TestE2EBadRequest(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m"}) // sin messages
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestE2EModelNotFound(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "no-such", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "model_not_found") {
		t.Fatalf("body: %s", body)
	}
	// ningún intento de red hacia los upstreams
	if h.ups[0].gotBody != "" {
		t.Fatal("se hizo un intento de red sin provider que sirva el modelo")
	}
}

func TestE2EStreamingFallback(t *testing.T) {
	// A falla pre-primer-byte, B stream OK
	h := build(t, []*upstream{upstreamFail(500), upstreamStreamOK("m")}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "data: {") || !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("SSE malformado: %q", s)
	}
	// model reescrito: el cliente ve "m", no el alias del upstream
	if strings.Contains(s, `"model":"`+h.ups[1].model+`"`) && !strings.Contains(s, `"model":"m"`) {
		t.Fatalf("model interno visible: %q", s)
	}
}

func TestE2EClampMaxTokens(t *testing.T) {
	// provider con max_tokens=4096; cliente pide 131072 → clamp a 4096
	u := upstreamOK("m", "x")
	h := build(t, []*upstream{u}, "sk-test-1")
	// el router clamp contra MaxTokens() del provider real (4096 default)
	resp := h.chat(t, map[string]any{
		"model": "m", "max_tokens": 131072,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var reqBody struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(u.gotBody), &reqBody); err != nil {
		t.Fatalf("body upstream inválido: %v", err)
	}
	if reqBody.MaxTokens != 4096 {
		t.Fatalf("max_tokens upstream = %d, want 4096 (clampado)", reqBody.MaxTokens)
	}
}

func TestE2EPreservesExtraFields(t *testing.T) {
	// tools/top_p deben llegar al upstream intactos (transparencia real)
	u := upstreamOK("m", "x")
	h := build(t, []*upstream{u}, "sk-test-1")
	resp := h.chat(t, map[string]any{
		"model": "m", "top_p": 0.5,
		"tools":    []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "parameters": map[string]any{}}}},
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(u.gotBody, `"tools"`) || !strings.Contains(u.gotBody, `"top_p"`) {
		t.Fatalf("campos extra perdidos en el round-trip: %s", u.gotBody)
	}
}

func TestE2EBodyTooLarge(t *testing.T) {
	h := build(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1")
	big := strings.Repeat("x", 11<<20) // 11MB > 10MB
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": big}}})
	if resp.StatusCode != 413 {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestE2EModelsEndpoint(t *testing.T) {
	u := upstreamOK("m", "x")
	h := build(t, []*upstream{u}, "sk-test-1")
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("models status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"m"`) {
		t.Fatalf("models no lista el modelo: %s", body)
	}
}

// ---- 005-005-verbose: privacidad + eventos debug ----

type jsonLogLine map[string]any

func parseJSONLines(t *testing.T, s string) []jsonLogLine {
	t.Helper()
	var lines []jsonLogLine
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m jsonLogLine
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("línea de log no parsea como JSON: %q: %v", ln, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func lineWithMsg(lines []jsonLogLine, msg string) jsonLogLine {
	for _, l := range lines {
		if l["msg"] == msg {
			return l
		}
	}
	return nil
}

// waitForMsg espera (hasta 2s) a que el handler termine de emitir un evento:
// request_end puede loguearse justo después de que el cliente lea el body.
func waitForMsg(t *testing.T, buf *bytes.Buffer, msg string) jsonLogLine {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l := lineWithMsg(parseJSONLines(t, buf.String()), msg); l != nil {
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestE2EDebugPrivacidadYEventos(t *testing.T) {
	// Logger debug capturado: el proxy y el router lo reciben inyectado.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := buildWithLogger(t, []*upstream{upstreamOK("m", "RESPUESTA-SECRETA-789")}, "sk-test-1", logger)

	resp := h.chat(t, map[string]any{
		"model":    "m",
		"stream":   false,
		"messages": []map[string]string{{"role": "user", "content": "S3CRETO-PROMPT-123"}},
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{"name": "t", "description": "TOOL-SECRET-456", "parameters": map[string]any{}}},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// El handler puede loguear request_end justo después del body: esperar.
	end := waitForMsg(t, &buf, "request_end")
	if end == nil {
		t.Fatalf("sin request_end en logs debug:\n%s", buf.String())
	}

	logs := buf.String()

	// PRIVACIDAD (regla dura de la spec): ningún log a ningún nivel contiene
	// contenido de prompt, definición de tools ni contenido de respuesta.
	for _, secret := range []string{"S3CRETO-PROMPT-123", "TOOL-SECRET-456", "RESPUESTA-SECRETA-789"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("PRIVACIDAD: log contiene %q:\n%s", secret, logs)
		}
	}

	// Metadatos debug: request_start con model/stream/num_messages/num_tools.
	start := lineWithMsg(parseJSONLines(t, logs), "request_start")
	if start == nil {
		t.Fatalf("sin request_start en logs debug:\n%s", logs)
	}
	if start["model"] != "m" {
		t.Fatalf("request_start model = %v, want m", start["model"])
	}
	if start["stream"] != false {
		t.Fatalf("request_start stream = %v, want false", start["stream"])
	}
	if start["num_messages"] != float64(1) {
		t.Fatalf("request_start num_messages = %v, want 1", start["num_messages"])
	}
	if start["num_tools"] != float64(1) {
		t.Fatalf("request_start num_tools = %v, want 1", start["num_tools"])
	}

	// request_end con status_code.
	if end["status_code"] != float64(200) {
		t.Fatalf("request_end status_code = %v, want 200", end["status_code"])
	}
}

// ---- 002-002-health: /healthz con detalle por provider ----

func TestE2EHealthzProviderDetail(t *testing.T) {
	// Sin health store → 200 genérico (modo EPIC-001).
	h := build(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1")
	code, body := h.healthz(t)
	if code != 200 {
		t.Fatalf("sin health store: status = %d, want 200", code)
	}
	if body["providers"] != nil {
		t.Fatalf("sin health store no debe haber providers: %v", body)
	}
}

func TestE2EHealthzWithStoreHealthy(t *testing.T) {
	hs := health.New(time.Second, 5*time.Second, nil)
	h, _ := buildWithOptions(t, []*upstream{upstreamOK("m", "hola")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Health: hs,
	})
	code, body := h.healthz(t)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (provider healthy)", code)
	}
	providers, ok := body["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers ausente o mal tipado: %v", body)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
	for id, p := range providers {
		pd := p.(map[string]any)
		if pd["healthy"] != true {
			t.Fatalf("provider %s healthy = %v, want true", id, pd["healthy"])
		}
		if pd["last_check"] == "" {
			t.Fatalf("provider %s sin last_check", id)
		}
	}
}

func TestE2EHealthz503AllDown(t *testing.T) {
	hs := health.New(time.Second, 5*time.Second, nil)
	up := upstreamOK("m", "hola")
	up.modelsStatus = 503 // el health check ve el provider caído
	h, _ := buildWithOptions(t, []*upstream{up}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Health: hs,
	})
	code, body := h.healthz(t)
	if code != 503 {
		t.Fatalf("status = %d, want 503 (todos unhealthy)", code)
	}
	if body["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", body["status"])
	}
	providers := body["providers"].(map[string]any)
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
}

func TestE2EHealthzRecovery(t *testing.T) {
	// 500 → el check marca unhealthy (503) → upstream se recupera →
	// próximo CheckNow lo marca healthy (200).
	hs := health.New(time.Second, 5*time.Second, nil)
	up := upstreamOK("m", "hola")
	up.modelsStatus = 500
	h, _ := buildWithOptions(t, []*upstream{up}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Health: hs,
	})
	if code, _ := h.healthz(t); code != 503 {
		t.Fatalf("status = %d, want 503 mientras upstream caído", code)
	}
	// El upstream se recupera: el próximo sondeo lo marca healthy.
	up.modelsStatus = 0
	hs.CheckNow()
	if code, body := h.healthz(t); code != 200 {
		t.Fatalf("status = %d, want 200 tras recuperación; body=%v", code, body)
	}
}

func TestE2EHealthSkipsDownProvider(t *testing.T) {
	// p1 unhealthy (health) + p2 healthy → el request NO toca p1
	// (002-002: saltear sin esperar timeout).
	hs := health.New(time.Second, 5*time.Second, nil)
	up1 := upstreamOK("m", "de p1")
	up1.modelsStatus = 500
	up2 := upstreamOK("m", "de p2")
	h, _ := buildWithOptions(t, []*upstream{up1, up2}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Health: hs,
	})
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "de p2") {
		t.Fatalf("respuesta no viene de p2 (p1 debería estar salteado): %s", body)
	}
	if up1.gotBody != "" {
		t.Fatal("p1 (unhealthy) recibió un request — el salteo por health no funciona")
	}
}

// ---- 002-003-absorcion: el cliente nunca ve detalles internos ----

func TestE2EAbsorbedErrorSanitized(t *testing.T) {
	// El provider falla con un mensaje que contiene URLs/keys internas:
	// el cliente debe recibir solo el error genérico OpenAI-compatible.
	up := upstreamFail(500)
	up.body = `{"error":{"message":"internal error at http://10.0.0.5:8080 with sk-super-secret","type":"server_error"}}`
	h := build(t, []*upstream{up}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, secret := range []string{"10.0.0.5", "sk-super-secret", "http://"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("el cliente ve el secreto %q: %s", secret, body)
		}
	}
	if !strings.Contains(string(body), "upstream_error") {
		t.Fatalf("body sin upstream_error: %s", body)
	}
}
