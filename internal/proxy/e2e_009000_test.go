// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-000-request-telemetry — P2-P5, P7, P6, P10
// (criterios C1-C8, C12-C14). Verifican el evento request_telemetry en el
// archivo dedicado telemetry.jsonl: wiring del proxy, schema exacto,
// allowlist/denylist de headers, walk de key paths, detección de ids en
// safe fields, privacidad. Cero red externa (upstreams fake en httptest).
//
// El evento se emite con logging.WithRequest(telemetryLogger, ctx); el
// harness abre telemetry.jsonl con logging.BuildTelemetry y lo pasa a
// proxy.New (6º parámetro). Los tests con telemetry OFF usan build() (nil).
//
// NOTA (desvío del AUDIT §7): el test de muestreo C9 se escribió en
// cmd/mofgw/telemetry_red_test.go — proxy.New no expone sample_rate (P2:
// firma de 10 params sin rate); la única vía pública con sample_rate es
// buildTelemetryFromConfig(cfg). Justificación en el reporte RED §4.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- harness con telemetría ----

// buildTelemetry construye el harness con telemetría habilitada:
// logging.BuildTelemetry(telemetry.jsonl) → proxy.New(..., tl, ...).
// Devuelve el harness y la ruta al archivo de telemetría (el test lee
// close-then-read o espera líneas; el closer vive en t.Cleanup — R6).
func buildTelemetry(t *testing.T, ups []*upstream, clientKey string, opLogger *slog.Logger) (*harness, string) {
	t.Helper()
	telemetryPath := filepath.Join(t.TempDir(), "telemetry.jsonl")
	tl, closer, err := logging.BuildTelemetry(telemetryPath)
	if err != nil {
		t.Fatalf("BuildTelemetry: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

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
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, opLogger)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, opLogger, tl, 10<<20, nil, nil, 0)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, telemetryPath
}

// chatWithHeaders envía un chat con headers custom (además de la auth).
func chatWithHeaders(t *testing.T, h *harness, key string, headers map[string]string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readTelemetryFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "" // archivo ausente → sin contenido
	}
	return string(b)
}

func readTelemetryLines(t *testing.T, path string) []jsonLogLine {
	t.Helper()
	return parseJSONLines(t, readTelemetryFile(t, path))
}

// waitTelemetryLines espera (hasta 2s) a que el archivo tenga al menos n
// líneas; devuelve lo que haya (para reportar en el fallo).
func waitTelemetryLines(t *testing.T, path string, n int) []jsonLogLine {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lines := readTelemetryLines(t, path); len(lines) >= n {
			return lines
		}
		time.Sleep(10 * time.Millisecond)
	}
	return readTelemetryLines(t, path)
}

// waitForSubstring espera (hasta 2s) a que el buffer contenga substr.
func waitForSubstring(t *testing.T, buf *bytes.Buffer, substr string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func containsPath(paths []any, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func hasDetectedValue(detected []any, path, value string) bool {
	for _, d := range detected {
		m, ok := d.(map[string]any)
		if ok && m["path"] == path && m["value"] == value {
			return true
		}
	}
	return false
}

// ---- P2 — wiring ----

// test_postcondition_2_TelemetryOffSinEventos (C1): telemetryLogger=nil →
// tráfico normal (C12). La no-creación del archivo con enabled=false se
// verifica en cmd/mofgw (buildTelemetryFromConfig).
func TestPostcondition2_TelemetryOffSinEventos(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (telemetría off no altera el tráfico)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if !strings.Contains(u.gotBody, `"content":"hi"`) {
		t.Fatalf("request no llegó intacto al upstream: %s", u.gotBody)
	}
}

// test_postcondition_2_TelemetryOnEscribeEventos (C2): logger no-nil →
// una línea JSON válida por request con request_id.
func TestPostcondition2_TelemetryOnEscribeEventos(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1 (C2): %s", len(lines), readTelemetryFile(t, path))
	}
	if id, ok := lines[0]["request_id"].(string); !ok || id == "" {
		t.Fatalf("evento sin request_id: %v", lines[0])
	}
}

// ---- P3 — un evento por request, stream/no-stream y rechazos ----

func TestPostcondition3_UnEventoPorRequestNoStream(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want EXACTAMENTE 1 (C2 no-stream)", len(lines))
	}
}

func TestPostcondition3_UnEventoPorRequestStream(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamStreamOK("m")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{"model": "m", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want EXACTAMENTE 1 (C2 stream)", len(lines))
	}
}

// test_postcondition_3_EventoEnRechazoBudget (P3): request rechazado por
// budget (429) igual emite evento.
func TestPostcondition3_EventoEnRechazoBudget(t *testing.T) {
	ups := upstreamUsageOK("m")
	h, path := buildTelemetry(t, []*upstream{ups}, "sk-test-1", nil)
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0},
	})
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"test": {CostUSDMax: 0.01},
	})

	var lastStatus int
	for i := 0; i < 9; i++ {
		resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
		lastStatus = resp.StatusCode
		io.Copy(io.Discard, resp.Body)
	}
	if lastStatus != 429 {
		t.Fatalf("último status = %d, want 429 (budget excedido)", lastStatus)
	}
	lines := waitTelemetryLines(t, path, 9)
	if len(lines) != 9 {
		t.Fatalf("eventos = %d, want 9 (todos los requests emiten, incluido el 429 — P3): %s", len(lines), readTelemetryFile(t, path))
	}
}

// test_postcondition_3_EventoEnRechazoVentana (P3): request rechazado por
// ventana de contexto (400) igual emite evento.
func TestPostcondition3_EventoEnRechazoVentana(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"m": {ContextWindow: 1000, MaxOutput: 100},
	})
	resp := h.chat(t, bigBody(5000))
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (ventana excedida)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1 (el rechazo por ventana emite — P3)", len(lines))
	}
}

// test_postcondition_3_EventoEnFalloUpstream (P3): 502 por upstream caído
// igual emite evento.
func TestPostcondition3_EventoEnFalloUpstream(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamFail(500), upstreamFail(503)}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1 (el fallo upstream emite — P3)", len(lines))
	}
}

// test_postcondition_3_SinEventoBodyInvalido (P3, R4): JSON malformado →
// 400 de parseo, CERO eventos.
func TestPostcondition3_SinEventoBodyInvalido(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 (JSON malformado)", resp.StatusCode)
	}
	time.Sleep(300 * time.Millisecond) // ventana para que NO aparezca el evento
	if lines := readTelemetryLines(t, path); len(lines) != 0 {
		t.Fatalf("JSON malformado NO debe emitir evento (R4): %v", lines)
	}
}

// ---- P4 — schema del evento ----

// test_postcondition_4_SchemaCompleto (R10): el evento tiene EXACTAMENTE
// los campos del schema P4 (sin extras) + ts RFC3339 + msg request_telemetry.
func TestPostcondition4_SchemaCompleto(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	ev := lines[0]
	if ev["msg"] != "request_telemetry" {
		t.Fatalf("msg = %v, want request_telemetry", ev["msg"])
	}
	schemaKeys := []string{"ts", "request_id", "client_id", "model", "stream", "headers", "key_paths", "num_messages", "num_tools", "roles", "detected_ids"}
	for _, k := range schemaKeys {
		if _, ok := ev[k]; !ok {
			t.Fatalf("evento sin campo %q: keys=%v", k, evKeys(ev))
		}
	}
	for k := range ev {
		if k == "time" || k == "level" || k == "msg" {
			continue // envelope de slog, no parte del schema
		}
		found := false
		for _, s := range schemaKeys {
			if k == s {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema lock R10: campo extra en el evento: %q (keys=%v)", k, evKeys(ev))
		}
	}
	ts, ok := ev["ts"].(string)
	if !ok {
		t.Fatalf("ts no es string: %v", ev["ts"])
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Fatalf("ts no es RFC3339: %q: %v", ts, err)
	}
}

func evKeys(ev jsonLogLine) []string {
	var keys []string
	for k := range ev {
		keys = append(keys, k)
	}
	return keys
}

// test_postcondition_4_CamposBase: client_id/model/stream/num_messages/
// num_tools/roles con valores correctos (R12: client_id = "test").
func TestPostcondition4_CamposBase(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{
		"model":  "m",
		"stream": false,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "ok"},
		},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "parameters": map[string]any{}}}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	ev := lines[0]
	if ev["client_id"] != "test" {
		t.Fatalf("client_id = %v, want test (R12)", ev["client_id"])
	}
	if ev["model"] != "m" {
		t.Fatalf("model = %v, want m", ev["model"])
	}
	if ev["stream"] != false {
		t.Fatalf("stream = %v, want false", ev["stream"])
	}
	if ev["num_messages"] != float64(2) {
		t.Fatalf("num_messages = %v, want 2", ev["num_messages"])
	}
	if ev["num_tools"] != float64(1) {
		t.Fatalf("num_tools = %v, want 1", ev["num_tools"])
	}
	roles, ok := ev["roles"].([]any)
	if !ok || len(roles) != 2 || roles[0] != "user" || roles[1] != "assistant" {
		t.Fatalf("roles = %v, want [user assistant] (orden de messages)", ev["roles"])
	}
}

// ---- P5 — headers allowlist + denylist ----

// test_postcondition_5_HeadersAllowlist (C3, R2): los headers de la
// allowlist presentes se capturan con su valor; Content-Length == len(body).
func TestPostcondition5_HeadersAllowlist(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+h.key)
	for k, v := range map[string]string{
		"X-Session-Id": "ses_test_1",
		"X-Agent-Id":   "agent-test",
		"User-Agent":   "opencode/1.18.4",
		"Content-Type": "application/json",
		"Accept":       "application/json",
	} {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	hdrs, ok := lines[0]["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers no es objeto: %v", lines[0]["headers"])
	}
	for k, v := range map[string]string{
		"X-Session-Id": "ses_test_1",
		"X-Agent-Id":   "agent-test",
		"User-Agent":   "opencode/1.18.4",
		"Content-Type": "application/json",
		"Accept":       "application/json",
	} {
		if hdrs[k] != v {
			t.Fatalf("header %s = %v, want %v (C3)", k, hdrs[k], v)
		}
	}
	// R2: Content-Length se expone en r.ContentLength, no en r.Header; el
	// contrato es la longitud del body.
	if hdrs["Content-Length"] != strconv.Itoa(len(raw)) {
		t.Fatalf("Content-Length = %v, want %d (R2)", hdrs["Content-Length"], len(raw))
	}
}

// test_postcondition_5_HeaderXCustomNegadoConWarn (C4, R7): header X-*
// fuera de allowlist → NO en el evento + warn en el logger OPERATIVO con el
// NOMBRE (nunca el valor).
func TestPostcondition5_HeaderXCustomNegadoConWarn(t *testing.T) {
	var opBuf bytes.Buffer
	opLogger := slog.New(slog.NewJSONHandler(&opBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", opLogger)

	headers := map[string]string{"X-Custom-Something": "SECRET-CUSTOM-VALUE"}
	resp := chatWithHeaders(t, h, "sk-test-1", headers, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	hdrs, _ := lines[0]["headers"].(map[string]any)
	if _, ok := hdrs["X-Custom-Something"]; ok {
		t.Fatalf("header fuera de allowlist capturado en el evento: %v", hdrs)
	}
	if !waitForSubstring(t, &opBuf, "X-Custom-Something") {
		t.Fatalf("sin warn con el NOMBRE del header en el log operativo (C4):\n%s", opBuf.String())
	}
	opLog := opBuf.String()
	if strings.Contains(opLog, "SECRET-CUSTOM-VALUE") {
		t.Fatalf("PRIVACIDAD: el VALOR del header filtró al log operativo (R7):\n%s", opLog)
	}
	if strings.Contains(readTelemetryFile(t, path), "SECRET-CUSTOM-VALUE") {
		t.Fatal("PRIVACIDAD: el valor del header filtró a telemetry.jsonl")
	}
}

// test_postcondition_5_HeadersProhibidosNunca (C5): Authorization, Cookie,
// X-Api-Key, X-Forwarded-For jamás en telemetry.jsonl.
func TestPostcondition5_HeadersProhibidosNunca(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	headers := map[string]string{
		"Cookie":          "session=COOKIE-SECRET-1",
		"X-Api-Key":       "XAPIKEY-SECRET-2",
		"X-Forwarded-For": "203.0.113.7",
	}
	resp := chatWithHeaders(t, h, "sk-test-1", headers, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	content := readTelemetryFile(t, path)
	for _, forbidden := range []string{"COOKIE-SECRET-1", "XAPIKEY-SECRET-2", "203.0.113.7", "sk-test-1"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("PRIVACIDAD: %q aparece en telemetry.jsonl (C5):\n%s", forbidden, content)
		}
	}
	hdrs, _ := lines[0]["headers"].(map[string]any)
	for _, k := range []string{"Cookie", "X-Api-Key", "X-Forwarded-For"} {
		if _, ok := hdrs[k]; ok {
			t.Fatalf("header prohibido %s en el evento: %v", k, hdrs)
		}
	}
}

// ---- P7 — detected ids (e2e) ----

// test_postcondition_7_DetectedIdsE2E (C6): metadata.session_id anidado →
// key_paths lo incluye y detected_ids lo reporta con {path, value}.
func TestPostcondition7_DetectedIdsE2E(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"metadata": map[string]any{"session_id": "ses_abc"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	ev := lines[0]
	keyPaths, ok := ev["key_paths"].([]any)
	if !ok || !containsPath(keyPaths, "metadata.session_id") {
		t.Fatalf("key_paths sin metadata.session_id: %v", ev["key_paths"])
	}
	detected, ok := ev["detected_ids"].([]any)
	if !ok || !hasDetectedValue(detected, "metadata.session_id", "ses_abc") {
		t.Fatalf("detected_ids sin {metadata.session_id, ses_abc}: %v (C6)", ev["detected_ids"])
	}
}

// test_postcondition_7_SoloPathsNoValores (C7): content/text/description/
// input/arguments aportan SOLO paths, jamás valores en telemetry.jsonl.
func TestPostcondition7_SoloPathsNoValores(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	body := map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "user", "content": "SECRET-PROMPT-7777", "text": "SECRET-TEXT-8888", "input": "SECRET-INPUT-9999"},
			{"role": "assistant", "content": "ok", "tool_calls": []map[string]any{{"function": map[string]any{"name": "f", "arguments": "SECRET-ARGS-1111"}}}},
		},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "description": "SECRET-DESC-2222", "parameters": map[string]any{}}}},
	}
	resp := h.chat(t, body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
	keyPaths, ok := lines[0]["key_paths"].([]any)
	if !ok {
		t.Fatalf("key_paths ausente: %v", lines[0]["key_paths"])
	}
	for _, p := range []string{"messages[].content", "messages[].text", "messages[].input", "messages[].tool_calls[].function.arguments", "tools[].function.description"} {
		if !containsPath(keyPaths, p) {
			t.Fatalf("key_paths sin %q: %v", p, keyPaths)
		}
	}
	content := readTelemetryFile(t, path)
	for _, secret := range []string{"SECRET-PROMPT-7777", "SECRET-TEXT-8888", "SECRET-INPUT-9999", "SECRET-ARGS-1111", "SECRET-DESC-2222"} {
		if strings.Contains(content, secret) {
			t.Fatalf("PRIVACIDAD: %q en telemetry.jsonl (C7):\n%s", secret, content)
		}
	}
}

// ---- P6 — maxDepth (e2e) ----

// test_postcondition_6_MaxDepthE2E (C8): 15 niveles de anidamiento → sin
// crash, el evento se emite igual y los paths quedan truncados.
func TestPostcondition6_MaxDepthE2E(t *testing.T) {
	deep := map[string]any{"leaf": "v"}
	for i := 0; i < 14; i++ {
		deep = map[string]any{"next": deep}
	}
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"deep":     deep,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (walk truncado sin crash — C8)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitTelemetryLines(t, path, 1)
	if len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1 (el evento se emite igual — C8)", len(lines))
	}
	keyPaths, ok := lines[0]["key_paths"].([]any)
	if !ok {
		t.Fatalf("key_paths ausente: %v", lines[0]["key_paths"])
	}
	for _, p := range keyPaths {
		s, ok := p.(string)
		if !ok {
			continue
		}
		if strings.Count(s, ".") > 11 {
			t.Fatalf("path %q excede maxDepth=10 (C8/I4)", s)
		}
	}
}

// ---- P2/I7 — no invasivo ----

// test_postcondition_2_NoInvasivoConTelemetry (I7, R9): con telemetría ON,
// el request viaja intacto al upstream (mismo comportamiento que
// TestE2EPreservesExtraFields).
func TestPostcondition2_NoInvasivoConTelemetry(t *testing.T) {
	u := upstreamOK("m", "x")
	h, path := buildTelemetry(t, []*upstream{u}, "sk-test-1", nil)
	resp := h.chat(t, map[string]any{
		"model": "m", "top_p": 0.5,
		"tools":    []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "parameters": map[string]any{}}}},
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if !strings.Contains(u.gotBody, `"tools"`) || !strings.Contains(u.gotBody, `"top_p"`) {
		t.Fatalf("campos extra perdidos con telemetría ON (I7): %s", u.gotBody)
	}
	if lines := waitTelemetryLines(t, path, 1); len(lines) != 1 {
		t.Fatalf("eventos = %d, want 1", len(lines))
	}
}

// ---- P10 — privacidad (e2e) ----

// test_postcondition_10_PrivacidadGrepNegativo (C14): strings distintivos
// en content/arguments → cero matches en telemetry.jsonl.
func TestPostcondition10_PrivacidadGrepNegativo(t *testing.T) {
	h, path := buildTelemetry(t, []*upstream{upstreamOK("m", "x")}, "sk-test-1", nil)
	for i, secret := range []string{"PRIVACY-SECRET-PROMPT-0001", "PRIVACY-SECRET-ARGS-0002"} {
		var body map[string]any
		if i == 0 {
			body = map[string]any{"model": "m", "messages": []map[string]any{{"role": "user", "content": secret}}}
		} else {
			body = map[string]any{"model": "m", "messages": []map[string]any{{"role": "user", "content": "hi", "tool_calls": []map[string]any{{"function": map[string]any{"name": "f", "arguments": secret}}}}}}
		}
		resp := h.chat(t, body)
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	lines := waitTelemetryLines(t, path, 2)
	if len(lines) != 2 {
		t.Fatalf("eventos = %d, want 2", len(lines))
	}
	content := readTelemetryFile(t, path)
	for _, secret := range []string{"PRIVACY-SECRET-PROMPT-0001", "PRIVACY-SECRET-ARGS-0002"} {
		if strings.Contains(content, secret) {
			t.Fatalf("PRIVACIDAD: %q en telemetry.jsonl (C14/I1):\n%s", secret, content)
		}
	}
}
