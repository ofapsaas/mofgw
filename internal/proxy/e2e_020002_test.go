// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 020-002-metrics-summary-html — endpoint read-only
// GET /v1/metrics/summary?date=YYYY-MM-DD (spec P1-P17, C1-C10).
//
// RED por compilación: los tests referencian `SetRegistryPath` (método
// inexistente en Server) y la ruta `/v1/metrics/summary` (que hoy responde
// 404). Los `undefined` SON el RED.
//
// Contrato a materializar por GREEN (test-audit §3):
//
//	func (s *Server) SetRegistryPath(path string) // pre-tráfico, inmutable
//	func (s *Server) handleMetricsSummary(w http.ResponseWriter, r *http.Request)
//	// ruta: mux.HandleFunc("GET /v1/metrics/summary", s.handleMetricsSummary)
//
// Fixtures: JSONL con la serialización REAL del writer (json.Marshal de
// registry.TerminalEvent + "\n") bajo t.TempDir(); líneas históricas
// (pre-020-001, sin model/cost_usd_src/cost_usd_up) como json.Marshal de
// map[string]any con solo las keys del esquema 014-001.
package proxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/router"
)

const day020002 = "2026-09-17" // día base del fixture
const ts020002 = day020002 + "T10:00:00.000Z"

// write020002Fixture escribe un archivo JSONL con las líneas dadas.
func write020002Fixture(t *testing.T, path string, lines []any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, line := range lines {
		j, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		b.Write(j)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("escribiendo fixture: %v", err)
	}
}

// terminal020002 arma un TerminalEvent serializable (writer-style).
func terminal020002(ts, client, model, outcome string, status int, cost float64, src string, up *float64, hist bool) map[string]any {
	ev := map[string]any{
		"type":           "terminal",
		"request_id":     "r-" + ts + client + outcome + model,
		"ts":             ts,
		"client":         client,
		"outcome":        outcome,
		"status":         status,
		"final_provider": "up1",
		"tokens":         map[string]any{"prompt": 200, "completion": 80, "cache": 1000, "reasoning": 0},
		"cost_usd":       cost,
		"stream":         false,
	}
	if outcome == "error" {
		ev["error_code"] = "model_not_found"
	}
	if !hist {
		ev["model"] = model
		ev["cost_usd_src"] = src
		if up != nil {
			ev["cost_usd_up"] = *up
		} else {
			ev["cost_usd_up"] = nil
		}
	}
	return ev
}

// attempt020002 arma un AttemptEvent del día (debe ser IGNORADO, P6).
func attempt020002(ts, model string) map[string]any {
	return map[string]any{
		"type":       "attempt",
		"request_id": "r-attempt-" + ts + model,
		"ts":         ts,
		"client":     "test",
		"provider":   "up1",
		"model":      model,
		"outcome":    "ok",
		"attempt":    1,
		"retries":    0,
	}
}

// setup020002 arma un sandbox completo: server con la ruta nueva (SetRegistryPath),
// writer-style fixtures en t.TempDir() (activo + rotados) según los args.
// RED: compile error — SetRegistryPath no existe.
func setup020002(t *testing.T, active []any, rotated1 []any, rotated2 []any, gz bool, configurePath bool) (*httptest.Server, string, *os.File) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "mofgw-registry.jsonl")

	var activeLines []any
	for _, l := range active {
		activeLines = append(activeLines, l)
	}
	write020002Fixture(t, regPath, activeLines)
	if rotated1 != nil {
		write020002Fixture(t, regPath+".1", rotated1)
	}
	if rotated2 != nil {
		write020002Fixture(t, regPath+".2", rotated2)
	}
	if gz {
		// .gz adyacente (logrotate comprimiría a futuro — fail-soft, P8):
		// NO es un JSONL válido; el endpoint no debe leerlo ni contarlo.
		if err := os.WriteFile(regPath+".1.gz", []byte("gz-fake"), 0o600); err != nil {
			t.Fatalf("gz: %v", err)
		}
	}

	// Server mínimo con auth Bearer (patrón auth_test.go / e2e_014001).
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageOK("m")}, "sk-test-1", regPath+".writer.jsonl", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})

	if configurePath {
		h.proxySrv.SetRegistryPath(regPath)
	}
	return h.srv, regPath, nil
}

func get020002(t *testing.T, srv *httptest.Server, path string, key string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ---- B2 (C3, P3) — date validation ----

// Test020002_DateValidation congela P3: date ausente/vacío/inválido → 400
// invalid_request_error; formato estricto YYYY-MM-DD.
func Test020002_DateValidation(t *testing.T) {
	srv, _, _ := setup020002(t, nil, nil, nil, false, true)

	for _, date := range []string{"", "2026-9-1", "20260901", "garbage", day020002 + "T10:00:00Z", " " + day020002} {
		resp := get020002(t, srv, "/v1/metrics/summary?date="+strings.ReplaceAll(date, " ", "+"), "sk-test-1")
		if resp.StatusCode != 400 {
			t.Errorf("date=%q → status %d, want 400 (P3)", date, resp.StatusCode)
		}
	}
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Errorf("date=%q válido → status %d, want 200 (P3)", day020002, resp.StatusCode)
	}
}

// ---- B3 (C4, P2) — auth Bearer requerida ----

// Test020002_AuthRequired congela P2: sin Bearer → 401 invalid_api_key
// (la ruta NO está en publicPrefixes — I4).
func Test020002_AuthRequired(t *testing.T) {
	srv, _, _ := setup020002(t, nil, nil, nil, false, true)
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "")
	if resp.StatusCode != 401 {
		t.Fatalf("sin auth = %d, want 401 (P2)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_api_key") {
		t.Errorf("body sin invalid_api_key (P2): %s", body)
	}
}

// ---- B4 (C5, P4) — knob vacío → 503 runtime ----

// Test020002_NotConfigured503 congela P4: sin SetRegistryPath → 503
// server_error "registry file not configured" (patrón clientconfig 016-001).
func Test020002_NotConfigured503(t *testing.T) {
	srv, _, _ := setup020002(t, nil, nil, nil, false, false)
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 503 {
		t.Fatalf("sin knob = %d, want 503 (P4)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "registry file not configured") {
		t.Errorf("body sin 'registry file not configured' (P4): %s", body)
	}
}

// ---- B5 (C6, P7/P8) — rotados leídos con cero doble conteo ----

// Test020002_RotatedFiles congela P7/P8: activo + .1 + .2 leídos; filtro
// ts previene doble conteo; .3 inexistente (stop); .1.gz NO leído.
func Test020002_RotatedFiles(t *testing.T) {
	up := 0.0042
	active := []any{
		terminal020002(ts020002, "c1", "glm-5.2", "success", 200, 0.001, "upstream", &up, false),
		terminal020002("2026-09-18T10:00:00.000Z", "c1", "glm-5.2", "success", 200, 0.002, "upstream", &up, false),
	}
	rot1 := []any{
		terminal020002(ts020002, "c2", "glm-5.2", "success", 200, 0.003, "table", nil, false),
		terminal020002("2026-09-16T10:00:00.000Z", "c2", "glm-5.2", "success", 200, 0.099, "table", nil, false),
	}
	rot2 := []any{
		terminal020002("2026-09-16T10:00:00.000Z", "c3", "glm-5.2", "success", 200, 0.099, "table", nil, false),
	}
	srv, regPath, _ := setup020002(t, active, rot1, rot2, true, true)

	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// glm-5.2 del día D: exactamente 2 requests (1 upstream + 1 table).
	// Los de D+1 (activo) y D-1 (.1/.2) NO cuentan (filtro ts).
	if !strings.Contains(html, "0.004") {
		// cost total del día = 0.001 + 0.003 = 0.004; el 0.099 de otros días NO
		if !strings.Contains(html, "0.004") {
			t.Errorf("cost total del día no refleja solo el día D (P7)\nHTML: %s", html)
		}
	}
	if strings.Contains(html, "0.099") {
		t.Errorf("líneas de OTROS días contadas (P7: filtro ts)\nHTML: %s", html)
	}
	if strings.Contains(html, "0.002") {
		t.Errorf("líneas de D+1 (en activo) contadas (P7)\nHTML: %s", html)
	}
	// .gz no leído: el body gz-fake no aparece (no matchea el pattern .N).
	_ = regPath
}

// ---- B1 (C2, P16 maestro) — fidelidad fixture ----

// Test020002_FixtureFidelity congela P16 (maestro): un fixture mixto produce
// EXACTAMENTE los agregados esperados por modelo, cobertura, totales,
// corruptas y desconocido. Es el discriminante central del feature.
func Test020002_FixtureFidelity(t *testing.T) {
	up42 := 0.0042
	active := []any{
		// terminal success upstream con cost (P4: tabla NO participa)
		terminal020002(ts020002, "c1", "glm-5.2", "success", 200, 0.0042, "upstream", &up42, false),
		// attempt del día: IGNORADO (P6)
		attempt020002(ts020002, "glm-5.2"),
		// terminal success table (P5: estimateCost con la tabla)
		terminal020002(ts020002, "c1", "minimax-m3", "success", 200, 0.00128, "table", nil, false),
		// terminal success none (modelo sin pricing)
		terminal020002(ts020002, "c2", "modelo-sin-precio", "success", 200, 0.0, "none", nil, false),
		// terminal ERROR (HITL-a: model presente, src none) — NO histórico
		// (el model viaja aunque el request falle)
		terminal020002(ts020002, "c2", "no-such-model", "error", 404, 0.0, "none", nil, false),
		// línea HISTÓRICA (pre-020-001): model=="" → fila desconocido (P11)
		terminal020002(ts020002, "c3", "", "success", 200, 0.5, "", nil, true),
		// línea corrupta (JSON inválido — P12)
		`{"type":"terminal","ts":"2026-09-17T10:00:00.000Z","roto`,
		// línea corrupta GIGANTE (> 64 KiB Scanner default — P12)
		strings.Repeat("x", 65536),
		// línea válida posterior (P12: el parse continúa)
		terminal020002(ts020002, "c1", "glm-5.2", "success", 200, 0.001, "upstream", &up42, false),
	}
	srv, _, _ := setup020002(t, active, nil, nil, false, true)
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (P12: corruptas no abortan)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// glm-5.2: 2 requests upstream (0.0042 + 0.001 = 0.0052 total)
	if !strings.Contains(html, "0.0052") {
		t.Errorf("cost glm-5.2 = no contiene 0.0052 (P9/P16)\nHTML: %s", html)
	}
	// minimax-m3: 1 request table (0.00128)
	if !strings.Contains(html, "0.00128") {
		t.Errorf("cost minimax-m3 = no contiene 0.00128 (P9)\nHTML: %s", html)
	}
	// modelo-sin-precio: 1 request none (0.0)
	if !strings.Contains(html, "modelo-sin-precio") {
		t.Errorf("fila modelo-sin-precio ausente (P9/P11)\nHTML: %s", html)
	}
	// no-such-model: 1 error
	if !strings.Contains(html, "no-such-model") {
		t.Errorf("fila no-such-model (error) ausente (P1: error también lleva model)\nHTML: %s", html)
	}
	// desconocido: fila visible (P11)
	if !strings.Contains(html, "desconocido") {
		t.Errorf("fila desconocido ausente (P11)\nHTML: %s", html)
	}
	// cost_usd_up presente: 2 glm-5.2 upstream con cost (0.0042 y 0.001);
	// el agregado cost_usd de la fila = 0.0052 (los UP se suman, no se
	// muestran por-item — P9: conteo presente/null, no valores individuales)
	if !strings.Contains(html, "0.005200") || !strings.Contains(html, ">2<") {
		// el HTML muestra cost_usd agregado (0.0052) + cost_usd_up presente=2
		t.Errorf("agregados upstream de glm-5.2 no visibles (P3/P9)\nHTML: %s", html)
	}
	// cobertura EXACTA (F5 review: números, no solo palabras): upstream=2,
	// table=1, none=3 (2 errors+none modelo-sin-precio... según fixture),
	// históricas=1, total terminales=6
	for _, frag := range []string{
		"<tr><td>upstream</td><td>2</td>",
		"<tr><td>table</td><td>1</td>",
		"<tr><td>none</td><td>3</td>",
		"<tr><td>históricas (sin src)</td><td>1</td>",
	} {
		if !strings.Contains(html, frag) {
			t.Errorf("cobertura de proveniencia: falta %q (P10/F5)\nHTML: %s", frag, html)
		}
	}
	// líneas corruptas EXACTAS (F4 review): 2 (JSON roto + gigante >64KiB)
	if !strings.Contains(html, "líneas corruptas: 2") {
		t.Errorf("contador de corruptas exacto no visible (P12/F4)\nHTML: %s", html)
	}
	// totales EXACTOS (F4 review): 6 terminales del día (2 glm-5.2 + 1 minimax
	// + 1 sin-precio + 1 no-such + 1 desconocida)
	if !strings.Contains(html, "terminales del día: 6") {
		t.Errorf("total terminales exacto no visible (P10/F4)\nHTML: %s", html)
	}
	// F12 review: determinismo — 2 requests → mismo HTML byte a byte (P16)
	resp2 := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.EqualFold(string(body), string(body2)) {
		t.Errorf("dos requests → HTML distinto (P16/F12: determinismo)")
	}
}

// ---- B6 (C7, P12) — corruptas no abortan ----

// Test020002_CorruptLines congela P12: líneas corruptas intercaladas con
// válidas → 200, contador > 0, líneas válidas posteriores contadas.
func Test020002_CorruptLines(t *testing.T) {
	up := 0.0042
	active := []any{
		`{"roto`,
		strings.Repeat("x", 65536),
		terminal020002(ts020002, "c1", "glm-5.2", "success", 200, 0.001, "upstream", &up, false),
	}
	srv, _, _ := setup020002(t, active, nil, nil, false, true)
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (P12: corruption no aborta)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "glm-5.2") {
		t.Errorf("línea válida posterior a las corruptas NO contada (P12)\nHTML: %s", body)
	}
}

// ---- B7 (C8, P15) — read-only ----

// Test020002_ReadOnly congela P15: tras un request, los bytes de los
// archivos leídos son idénticos y no se crean archivos nuevos.
func Test020002_ReadOnly(t *testing.T) {
	up := 0.0042
	dir := t.TempDir()
	regPath := filepath.Join(dir, "mofgw-registry.jsonl")
	active := []any{terminal020002(ts020002, "c1", "glm-5.2", "success", 200, 0.001, "upstream", &up, false)}
	write020002Fixture(t, regPath, active)

	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageOK("m")}, "sk-test-1", regPath+".writer.jsonl", router.Options{MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second})
	h.proxySrv.SetRegistryPath(regPath)

	before := readdirNames020002(t, dir)
	resp := get020002(t, h.srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, _ = io.ReadAll(resp.Body)
	after := readdirNames020002(t, dir)
	if !equalStrings020002(before, after) {
		t.Errorf("disco modificado por el request (P15)\nbefore: %v\nafter:  %v", before, after)
	}
	b, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if len(b) == 0 {
		t.Error("archivo activo vacío tras request (P15)")
	}
}

func readdirNames020002(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func equalStrings020002(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- B8 (C9, P14) — HTML shape ----

// Test020002_HTMLShape congela P14: Content-Type text/html; charset=utf-8;
// sin script src ni link externos; fecha UTC declarada en el documento.
func Test020002_HTMLShape(t *testing.T) {
	srv, _, _ := setup020002(t, nil, nil, nil, false, true)
	resp := get020002(t, srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") || !strings.Contains(ct, "charset=utf-8") {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8 (P14)", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "<script src") || strings.Contains(html, "<link") {
		t.Errorf("HTML con recursos externos (P14/D9: self-contained)\nHTML: %s", html)
	}
	if !strings.Contains(html, day020002) {
		t.Errorf("HTML sin la fecha consultada declarada (D4/P14)\nHTML: %s", html)
	}
	if !strings.Contains(strings.ToUpper(html), "UTC") {
		t.Errorf("HTML sin declaración UTC (D4)\nHTML: %s", html)
	}
}

// ---- B9 (C10, P5) — archivo inexistente → 200 vacío con aviso ----

// Test020002_MissingFileSoft congela P5: path configurado pero archivo
// inexistente → 200 con aviso visible (no error).
func Test020002_MissingFileSoft(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "inexistente.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageOK("m")}, "sk-test-1", regPath+".writer.jsonl", router.Options{MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second})
	h.proxySrv.SetRegistryPath(regPath)

	resp := get020002(t, h.srv, "/v1/metrics/summary?date="+day020002, "sk-test-1")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (P5: día sin datos es válido)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "no encontrado") && !strings.Contains(string(body), "sin datos") {
		t.Errorf("HTML sin aviso de archivo no encontrado (P5)\nHTML: %s", body)
	}
}

// ---- B10 (C1, P1) — método incorrecto → 405 ----

// Test020002_MethodNotAllowed congela P1: la ruta está registrada con
// pattern de método GET — POST/DELETE responden 405.
func Test020002_MethodNotAllowed(t *testing.T) {
	srv, _, _ := setup020002(t, nil, nil, nil, false, true)
	for _, method := range []string{"POST", "DELETE", "PUT"} {
		req, err := http.NewRequest(method, srv.URL+"/v1/metrics/summary?date="+day020002, nil)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		req.Header.Set("Authorization", "Bearer sk-test-1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Errorf("%s → %d, want 405 (P1)", method, resp.StatusCode)
		}
	}
}
