// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED (e2e) de la feature 014-001-registro-unificado — criterios
// C2, C4-C17 a través del handler HTTP. Verifican el registro JSONL de
// accounting + outcome: un evento `attempt` por visita a provider de la
// cadena + un evento `terminal` por request en routing, con esquema exacto,
// correlación de request_id, privacidad y resiliencia.
//
// RED por compilación: el paquete internal/registry (NewWriter, Writer),
// proxy.Server.SetRegistry y router.Router.SetRegistry NO existen aún → el
// paquete no compila en RED. El implementer materializa el contrato en
// GREEN (§2.1/§cambios de la spec).
//
// Reusa los fakes y helpers del paquete proxy_test (upstreamOK, upstreamFail,
// upstreamUsageOK, upstreamStreamUsageOK, harness.chat, parseJSONLines,
// chatWithHeaders) sin redefinirlos.

package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/registry"
	"github.com/ofapsaas/mofgw/internal/router"
)

// fakeUpstream: cualquier fake (upstream, upstreamFailOnce, ...) que sepa
// atender HTTP como upstream OpenAI-compatible.
type fakeUpstream interface {
	handler() http.HandlerFunc
}

// ---- harness con registry ----

// buildRegistry construye el harness con el registro habilitado: crea el
// writer registry.NewWriter(regPath), lo cablea en el router (attempts) y
// en el proxy (terminales). Devuelve harness, writer (para tests de
// resiliencia P16) y la ruta del archivo.
func buildRegistry(t *testing.T, ups []fakeUpstream, clientKey string, regPath string, o router.Options) (*harness, *registry.Writer, string) {
	t.Helper()
	w, err := registry.NewWriter(regPath)
	if err != nil {
		t.Fatalf("registry.NewWriter(%s): %v", regPath, err)
	}
	t.Cleanup(func() { _ = w.Close() })

	h := &harness{key: clientKey}
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
	if o.Health != nil {
		for _, cl := range clients {
			o.Health.Register(cl)
		}
		o.Health.CheckNow()
	}
	r := router.NewWithOptions(specs, o)
	r.SetRegistry(w) // emisión de eventos attempt por visita (P7/P8/P9)

	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, o.Logger, nil, 10<<20, nil, nil, 0)
	s.SetRegistry(w) // emisión de eventos terminal (P10/P12/P13)

	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, w, regPath
}

// readRegistry lee el contenido del archivo del registro ("" si no existe).
func readRegistry(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// waitRegistryLines espera (hasta 2s) a que el archivo tenga al menos n
// líneas; devuelve lo que haya (para reportar en el fallo).
func waitRegistryLines(t *testing.T, path string, n int) []jsonLogLine {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lines := parseJSONLines(t, readRegistry(t, path)); len(lines) >= n {
			return lines
		}
		time.Sleep(10 * time.Millisecond)
	}
	return parseJSONLines(t, readRegistry(t, path))
}

// waitDegradedTerminal espera (hasta 2s) a que aparezca un terminal con
// status 503 (degraded_limit) y lo devuelve; nil si no aparece. Se usa en
// el escenario degradado (C13), donde el conteo total de líneas es variable
// porque el request que aún camina la cadena emite attempts → no se puede
// fiar de un umbral de líneas para detectar el terminal degradado.
func waitDegradedTerminal(t *testing.T, path string) jsonLogLine {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range parseJSONLines(t, readRegistry(t, path)) {
			if l["type"] == "terminal" && l["status"] == float64(503) {
				return l
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// lineByTypeAndRID: primera línea con `type` y `request_id` dados (para
// correlación P11).
func lineByTypeAndRID(lines []jsonLogLine, typ, rid string) jsonLogLine {
	for _, l := range lines {
		if l["type"] == typ && l["request_id"] == rid {
			return l
		}
	}
	return nil
}

// ---- C2 — registry off (aditivo) ----

// Test014001_P2_OffDefaultSinArchivo verifica C2/P2: con registry NO
// cableado (nil/off), el tráfico normal funciona idéntico (200 con el
// contenido del upstream) y no se crea archivo alguno en una ruta
// descartable.
func Test014001_P2_OffDefaultSinArchivo(t *testing.T) {
	u := upstreamOK("m", "hola")
	h := build(t, []*upstream{u}, "sk-test-1")
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (registry off no altera el tráfico, C2/P2)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hola") {
		t.Fatalf("respuesta no contiene el contenido del upstream: %s", body)
	}
	// El proxy no crea archivo con registry off: una ruta descartable no
	// debe existir (I3/P2).
	thrown := filepath.Join(t.TempDir(), "registry-off.jsonl")
	if _, err := os.Stat(thrown); err == nil {
		t.Fatalf("registry off no debe crear archivo (C2/P2): %s existe", thrown)
	}
}

// ---- C4 — happy path no-stream ----

// Test014001_P7_HappyPathNoStream verifica C4/P7/P12: un provider responde
// 200 (con usage) ⇒ 1 attempt ok + 1 terminal success con tokens == usage
// real, mismo request_id.
func Test014001_P7_HappyPathNoStream(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageOK("m")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (C4)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 2)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (1 attempt + 1 terminal, C4):\n%s", len(lines), readRegistry(t, regPath))
	}
	var at jsonLogLine
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "attempt" {
			at = l
		}
		if l["type"] == "terminal" {
			te = l
		}
	}
	if at == nil || te == nil {
		t.Fatalf("faltan attempt/terminal (C4): %v", lines)
	}
	if at["outcome"] != "ok" || at["cause"] != "" || at["status"] != float64(200) {
		t.Fatalf("attempt outcome/cause/status = %v/%v/%v, want ok/''/200 (C4)", at["outcome"], at["cause"], at["status"])
	}
	if at["attempt"] != float64(1) || at["retries"] != float64(0) {
		t.Fatalf("attempt/retries = %v/%v, want 1/0 (C4)", at["attempt"], at["retries"])
	}
	if at["provider"] != "up1" || at["model"] != "m" || at["client"] != "test" {
		t.Fatalf("provider/model/client = %v/%v/%v (C4)", at["provider"], at["model"], at["client"])
	}
	if te["outcome"] != "success" || te["error_code"] != "" || te["status"] != float64(200) {
		t.Fatalf("terminal outcome/error_code/status = %v/%v/%v (C4)", te["outcome"], te["error_code"], te["status"])
	}
	if te["final_provider"] != "up1" || te["stream"] != false {
		t.Fatalf("final_provider/stream = %v/%v (C4)", te["final_provider"], te["stream"])
	}
	tk, _ := te["tokens"].(map[string]any)
	if tk["prompt"] != float64(1200) || tk["completion"] != float64(80) || tk["cache"] != float64(1000) || tk["reasoning"] != float64(30) {
		t.Fatalf("tokens = %v, want {1200,80,1000,30} (C4)", tk)
	}
	if cost, _ := te["cost_usd"].(float64); cost < 0 {
		t.Fatalf("cost_usd = %v, want >= 0 (tolerante, C4)", te["cost_usd"])
	}
	// correlación P11: mismo request_id no vacío en ambos eventos.
	rid := at["request_id"].(string)
	if rid == "" || te["request_id"] != rid {
		t.Fatalf("request_id attempt=%q terminal=%v deben ser el mismo no vacío (P11/C4)", rid, te["request_id"])
	}
}

// ---- C5 — fallback consolidado ----

// Test014001_P8_P14_FallbackConsolidado verifica C5/P8/P14: p1 falla 500
// retryable, p2 responde ⇒ 2 attempts (p1 fallback, p2 ok) + terminal
// success con final_provider p2.
func Test014001_P8_P14_FallbackConsolidado(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamFail(500), upstreamOK("m", "rescate")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (fallback transparente, C5)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 3)
	if len(lines) != 3 {
		t.Fatalf("líneas = %d, want 3 (2 attempts + 1 terminal, C5):\n%s", len(lines), readRegistry(t, regPath))
	}
	var ats []jsonLogLine
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "attempt" {
			ats = append(ats, l)
		} else {
			te = l
		}
	}
	if len(ats) != 2 {
		t.Fatalf("attempts = %d, want 2 (C5)", len(ats))
	}
	if ats[0]["outcome"] != "fallback" || ats[0]["attempt"] != float64(1) || ats[0]["status"] != float64(500) {
		t.Fatalf("attempt1 outcome/attempt/status = %v/%v/%v, want fallback/1/500 (C5)", ats[0]["outcome"], ats[0]["attempt"], ats[0]["status"])
	}
	if c, _ := ats[0]["cause"].(string); c == "" {
		t.Fatalf("attempt1 cause vacío, debe ser no vacío (C5/D6)")
	}
	if ats[1]["outcome"] != "ok" || ats[1]["attempt"] != float64(2) || ats[1]["status"] != float64(200) {
		t.Fatalf("attempt2 outcome/attempt/status = %v/%v/%v, want ok/2/200 (C5)", ats[1]["outcome"], ats[1]["attempt"], ats[1]["status"])
	}
	if c, _ := ats[1]["cause"].(string); c != "" {
		t.Fatalf("attempt2 cause = %q, want \"\" (outcome ok, C5)", c)
	}
	if te["outcome"] != "success" || te["final_provider"] != "up2" {
		t.Fatalf("terminal outcome/final_provider = %v/%v, want success/up2 (C5/P14)", te["outcome"], te["final_provider"])
	}
}

// ---- C6 — retry del mismo provider ----

// upstreamFailOnce: falla 500 en la PRIMERA llamada y luego responde 200
// (para P9/C6 — retry_same_provider 002-001 colapsa a una visita con
// retries=1).
type upstreamFailOnce struct {
	model   string
	gotBody string
	calls   int
}

func (u *upstreamFailOnce) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		u.gotBody = string(raw)
		u.calls++
		if u.calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"message":"boom once","type":"server_error"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"id":"chatcmpl-r","object":"chat.completion","model":"`+u.model+`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
	}
}

// Test014001_P9_RetryMismoProvider verifica C6/P9: el provider falla en el
// try 1 y acierta en el retry ⇒ UNA sola visita filmada con outcome ok y
// retries=1 + terminal success del mismo provider.
func Test014001_P9_RetryMismoProvider(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	failOnce := &upstreamFailOnce{model: "m"}
	h, _, _ := buildRegistry(t, []fakeUpstream{failOnce}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
		Retry: router.RetryConfig{MaxAttempts: 2, BackoffBase: time.Millisecond, BackoffMax: 2 * time.Millisecond},
	})
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (retry ok, C6)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 2)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (1 attempt + 1 terminal, C6):\n%s", len(lines), readRegistry(t, regPath))
	}
	var at jsonLogLine
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "attempt" {
			at = l
		} else {
			te = l
		}
	}
	if at["outcome"] != "ok" || at["retries"] != float64(1) || at["attempt"] != float64(1) {
		t.Fatalf("attempt outcome/retries/attempt = %v/%v/%v, want ok/1/1 (C6/P9)", at["outcome"], at["retries"], at["attempt"])
	}
	if te["outcome"] != "success" || te["final_provider"] != "up1" {
		t.Fatalf("terminal outcome/final_provider = %v/%v, want success/up1 (C6)", te["outcome"], te["final_provider"])
	}
}

// ---- C7 — chain agotado ----

// Test014001_P8_P10_P13_ChainAgotado verifica C7/P8/P10/P13: ambos providers
// fallan retryable ⇒ 2 attempts fallback + terminal error chain_exhausted
// status 502, final_provider = último intentado, tokens/cost 0.
func Test014001_P8_P10_P13_ChainAgotado(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	// MaxRetries: 1 ⇒ maxAttempts = MaxRetries+1 = 2 (semántica 002-001),
	// con 2 providers la cadena visita exactamente up1→up2 (2 attempts) y
	// se agota — C7: 2 attempts + 1 terminal error chain_exhausted.
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamFail(500), upstreamFail(503)}, "sk-test-1", regPath, router.Options{
		MaxRetries: 1, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502 (C7)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 3)
	if len(lines) != 3 {
		t.Fatalf("líneas = %d, want 3 (2 attempts + 1 terminal, C7):\n%s", len(lines), readRegistry(t, regPath))
	}
	var ats []jsonLogLine
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "attempt" {
			ats = append(ats, l)
		} else {
			te = l
		}
	}
	if len(ats) != 2 {
		t.Fatalf("attempts = %d, want 2 (C7)", len(ats))
	}
	for i, at := range ats {
		if at["outcome"] != "fallback" {
			t.Fatalf("attempt %d outcome = %v, want fallback (C7)", i+1, at["outcome"])
		}
		if c, _ := at["cause"].(string); c == "" {
			t.Fatalf("attempt %d cause vacío, want no vacío (C7)", i+1)
		}
	}
	if te["outcome"] != "error" || te["error_code"] != "chain_exhausted" || te["status"] != float64(502) {
		t.Fatalf("terminal outcome/error_code/status = %v/%v/%v, want error/chain_exhausted/502 (C7)", te["outcome"], te["error_code"], te["status"])
	}
	if te["final_provider"] != "up2" {
		t.Fatalf("final_provider = %v, want up2 (último intentado, C7/P13)", te["final_provider"])
	}
	tk, _ := te["tokens"].(map[string]any)
	if tk["prompt"] != float64(0) || tk["completion"] != float64(0) || tk["cache"] != float64(0) || tk["reasoning"] != float64(0) {
		t.Fatalf("tokens error = %v, want todos 0 (C7/P13)", tk)
	}
	if te["cost_usd"] != float64(0) {
		t.Fatalf("cost_usd error = %v, want 0 (C7/P13)", te["cost_usd"])
	}
}

// ---- C8 — model_not_found ----

// Test014001_P10_P13_ModelNotFound verifica C8/P10/P13: ningún provider sirve
// el modelo ⇒ 0 attempts + 1 terminal error model_not_found status 404,
// final_provider "", tokens/cost 0.
func Test014001_P10_P13_ModelNotFound(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamOK("m", "x")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := h.chat(t, map[string]any{"model": "no-such", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404 (C8)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 1)
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "terminal" {
			te = l
		}
		if l["type"] == "attempt" {
			t.Fatalf("model_not_found NO debe emitir attempts (0 intentos, C8/P13)")
		}
	}
	if te == nil {
		t.Fatalf("sin terminal para model_not_found (C8): %s", readRegistry(t, regPath))
	}
	if te["outcome"] != "error" || te["error_code"] != "model_not_found" || te["status"] != float64(404) {
		t.Fatalf("terminal outcome/error_code/status = %v/%v/%v, want error/model_not_found/404 (C8)", te["outcome"], te["error_code"], te["status"])
	}
	if te["final_provider"] != "" {
		t.Fatalf("final_provider = %v, want \"\" (ninguno, C8/P13)", te["final_provider"])
	}
	tk, _ := te["tokens"].(map[string]any)
	if tk["prompt"] != float64(0) || tk["completion"] != float64(0) || tk["cache"] != float64(0) || tk["reasoning"] != float64(0) {
		t.Fatalf("tokens = %v, want todos 0 (C8)", tk)
	}
	if te["cost_usd"] != float64(0) {
		t.Fatalf("cost_usd = %v, want 0 (C8)", te["cost_usd"])
	}
}

// ---- C9 — streaming OK ----

// Test014001_P7_P12_StreamingOK verifica C9/P7/P12: request stream:true con
// primer byte OK ⇒ 1 attempt ok + 1 terminal success con stream:true y
// tokens del chunk final de usage.
func Test014001_P7_P12_StreamingOK(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamStreamUsageOK("m")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := h.chat(t, map[string]any{"model": "m", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (C9)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 2)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (C9):\n%s", len(lines), readRegistry(t, regPath))
	}
	var at jsonLogLine
	var te jsonLogLine
	for _, l := range lines {
		if l["type"] == "attempt" {
			at = l
		} else {
			te = l
		}
	}
	if at["outcome"] != "ok" {
		t.Fatalf("attempt outcome = %v, want ok (C9)", at["outcome"])
	}
	if te["outcome"] != "success" || te["stream"] != true || te["final_provider"] != "up1" {
		t.Fatalf("terminal outcome/stream/final_provider = %v/%v/%v, want success/true/up1 (C9)", te["outcome"], te["stream"], te["final_provider"])
	}
	tk, _ := te["tokens"].(map[string]any)
	if tk["prompt"] != float64(500) || tk["completion"] != float64(20) || tk["cache"] != float64(400) {
		t.Fatalf("tokens = %v, want {500,20,400,...} (C9)", tk)
	}
	if r, _ := tk["reasoning"].(float64); r < 0 {
		t.Fatalf("reasoning = %v, want >= 0 (tolerante, C9)", tk["reasoning"])
	}
}

// ---- C13 — degraded fast-fail ----

// Test014001_P10_P13_DegradedFastFail verifica C13/P10/P13: con todos los
// providers down y degradación activa, el request degradado → 503 sin
// attempts para su request_id y con un terminal error degraded_limit.
// Semántica establecida (TestDegradationLimit503): el 1er request camina la
// cadena (no es fast-fail), el fast-fail #1 da 502 all_providers_down y el
// fast-fail #2 da 503 degraded_limit.
func Test014001_P10_P13_DegradedFastFail(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamFail(500)}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Degradation: router.DegradationConfig{MaxRequests: 1, CooldownGrace: 0},
	})
	// Semántica de degradación establecida (TestDegradationLimit503):
	// - el primer request, con el provider FRESCO, camina la cadena
	//   (no es fast-fail → NO cuenta para degradedCnt) → 502 chain_exhausted
	//   tras emitir sus attempts.
	// - fast-fail #1 → cnt=1, `1 > 1` false → 502 all_providers_down.
	// - fast-fail #2 → cnt=2 > 1 → 503 degraded_limit.
	// El 503 degradado se observa en el TERCER request.
	r1 := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	io.Copy(io.Discard, r1.Body)
	// Primer fast-fail → 502 all_providers_down (no degradado aún).
	r2 := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if r2.StatusCode != 502 {
		t.Fatalf("segundo request status = %d, want 502 (primer fast-fail all_providers_down, C13)", r2.StatusCode)
	}
	io.Copy(io.Discard, r2.Body)
	// Segundo fast-fail → 503 degraded_limit (C13).
	r3 := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if r3.StatusCode != 503 {
		t.Fatalf("tercer request status = %d, want 503 (degradado degraded_limit, C13)", r3.StatusCode)
	}
	io.Copy(io.Discard, r3.Body)
	lastTerm := waitDegradedTerminal(t, regPath)
	if lastTerm == nil {
		t.Fatalf("sin terminal degradado (C13): %s", readRegistry(t, regPath))
	}
	if lastTerm["status"] != float64(503) {
		t.Fatalf("terminal status = %v, want 503 (C13)", lastTerm["status"])
	}
	code, _ := lastTerm["error_code"].(string)
	if code != "degraded_limit" {
		t.Fatalf("error_code = %q, want degraded_limit (C13)", code)
	}
	rid, _ := lastTerm["request_id"].(string)
	if rid == "" {
		t.Fatalf("terminal sin request_id (C13)")
	}
	// El request degradado NO emite attempts (P13/C13).
	for _, l := range parseJSONLines(t, readRegistry(t, regPath)) {
		if l["type"] == "attempt" && l["request_id"] == rid {
			t.Fatalf("el request degradado emitió attempt: %v (C13)", l)
		}
	}
}

// ---- C11 — correlación y orden ----

// Test014001_P11_CorrelacionYOrden verifica C11/P11: 2 requests seguidos
// ⇒ 2 request_id distintos, cada uno con su terminal, y en el orden de
// archivo el terminal del 1º aparece antes que el primer intento del 2º.
func Test014001_P11_CorrelacionYOrden(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamOK("m", "a"), upstreamOK("m", "b")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	for i := 0; i < 2; i++ {
		resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d status = %d, want 200 (C11)", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	lines := waitRegistryLines(t, regPath, 4)
	if len(lines) != 4 {
		t.Fatalf("líneas = %d, want 4 (2 attempts + 2 terminals, C11):\n%s", len(lines), readRegistry(t, regPath))
	}
	// request_ids distintos y no vacíos.
	rids := map[string]bool{}
	for _, l := range lines {
		r, _ := l["request_id"].(string)
		if r == "" {
			t.Fatalf("evento sin request_id (C11/P11/I8): %v", l)
		}
		rids[r] = true
	}
	if len(rids) != 2 {
		t.Fatalf("request_id distintos = %d, want 2 (C11)", len(rids))
	}
	// cada request_id tiene un terminal.
	for rid := range rids {
		if lineByTypeAndRID(lines, "terminal", rid) == nil {
			t.Fatalf("request_id %q sin terminal (P10/C11)", rid)
		}
	}
	// orden: terminal del 1º antes que el primer intento del 2º.
	firstTermIdx := -1
	var firstRID string
	for i, l := range lines {
		if l["type"] == "terminal" {
			firstTermIdx = i
			firstRID = l["request_id"].(string)
			break
		}
	}
	secondAttemptIdx := -1
	for i, l := range lines {
		if l["type"] == "attempt" && l["request_id"] != firstRID {
			secondAttemptIdx = i
			break
		}
	}
	if firstTermIdx == -1 || secondAttemptIdx == -1 {
		t.Fatalf("no se hallaron los eventos esperados (C11)")
	}
	if firstTermIdx > secondAttemptIdx {
		t.Fatalf("el terminal del 1º (idx %d) debe aparecer antes que el 1er intento del 2º (idx %d) — orden de archivo (C11)", firstTermIdx, secondAttemptIdx)
	}
}

// ---- C15 — privacidad ----

// Test014001_P15_PrivacidadGrepNegativo verifica C15/P15: strings distintivos
// LARGOS en content/arguments/header/Authorization y la respuesta del
// upstream JAMÁS aparecen en el registro (el writer solo serializa el
// esquema, D1).
func Test014001_P15_PrivacidadGrepNegativo(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamOK("m", "RESPUESTA-SECRETA-777")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	body := map[string]any{
		"model": "m",
		"messages": []map[string]any{
			{"role": "user", "content": "PRIVACY-REGISTRY-PROMPT-9911"},
			{"role": "assistant", "content": "ok", "tool_calls": []map[string]any{{"function": map[string]any{"name": "f", "arguments": "PRIVACY-REGISTRY-ARGS-8822"}}}},
		},
	}
	headers := map[string]string{"X-Custom": "PRIVACY-REGISTRY-HEADER-4455"}
	resp := chatWithHeaders(t, h, "sk-test-1", headers, body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (C15)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	lines := waitRegistryLines(t, regPath, 2)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (C15):\n%s", len(lines), readRegistry(t, regPath))
	}
	content := readRegistry(t, regPath)
	for _, secret := range []string{
		"PRIVACY-REGISTRY-PROMPT-9911", // prompt
		"PRIVACY-REGISTRY-ARGS-8822",   // argumentos de tool
		"RESPUESTA-SECRETA-777",        // respuesta del upstream
		"PRIVACY-REGISTRY-HEADER-4455", // header custom
		"sk-test-1",                    // Authorization
	} {
		if strings.Contains(content, secret) {
			t.Fatalf("PRIVACIDAD: %q aparece en el registro (C15/P15):\n%s", secret, content)
		}
	}
}

// ---- C16 (e2e) — resiliencia del writer ----

// Test014001_P16_RequestContinua verifica C16/P16 a nivel e2e: con un
// writer cuyo fd se cierra (falla al escribir), el request responde con su
// resultado normal (status/body intactos) — la falla de escritura NUNCA
// rompe el request.
func Test014001_P16_RequestContinua(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, w, _ := buildRegistry(t, []fakeUpstream{upstreamOK("m", "x")}, "sk-test-1", regPath, router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	// Fuerza falla de escritura: cierro el writer antes del request; el
	// proxy/router siguen apuntando al writer clausurado (best-effort).
	if err := w.Close(); err != nil {
		t.Fatalf("w.Close: %v", err)
	}
	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (writer fallible no rompe el request, C16/P16)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"content":"x"`) {
		t.Fatalf("body del cliente incompleto pese al writer fallible (C16): %s", body)
	}
}
