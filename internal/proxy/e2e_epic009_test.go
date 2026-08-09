// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Test E2E CROSS-FEATURE del epic 009 (mofgw-009-contexto-analisis — Etapa
// E3 de integración del epic): verifica el flujo COMPUESTO, el que ningún
// test individual cubre. Un request con X-Session-Id atraviesa las 3
// features del epic en el proxy:
//
//   - 009-000 telemetría: emitRequestTelemetry emite UN evento
//     request_telemetry con los key paths del body (sin contenido ni keys).
//   - 009-001 composición: fase 1 RecordContext en handleChat (crea el
//     record client|session) + fase 2 UpdateContextUsage en
//     recordCacheTokens (usage.prompt_tokens post-respuesta).
//   - 009-002 sticky: recordCacheTokens registra al provider GANADOR en
//     AffinityStore; el request siguiente de la misma sesión rutea al
//     MISMO provider (reorder post-filtro, 1 solo intento).
//
// El request siguiente de la misma sesión acumula composición en el mismo
// record y sticky rutea al mismo provider; un request SIN sesión
// (openclaw/zot) va al record de cliente (client|) sin contaminar la
// sesión; la telemetría nunca contiene contenido; otro cliente con la
// misma sesión no ve el record del primero.
//
// Mapeo a los criterios de aceptación del epic (plan.md:211-215):
//   - C1 (telemetría sin contenido ni keys): asserts del evento + grep
//     negativo en telemetry.jsonl.
//   - C3 (/v1/context composición con aislamiento por cliente): asserts
//     de acumulación por sesión/cliente + aislamiento entre clientes.
//   - C4 (sticky opcional, transparente): asserts de que el MISMO provider
//     atiende la sesión (reorder post-filtro).
//
// El harness buildEpic009 es NUEVO: telemetría + análisis de contexto +
// sticky TODOS habilitados a la vez (ningún harness individual lo
// construye — E3). Reutiliza los helpers existentes del paquete
// proxy_test: stickyUpstream (contadores, e2e_009002),
// logging.BuildTelemetry (e2e_009000), router.NewWithOptions + auth +
// metrics + proxy.New (e2e_009002), upstreamUsagePrompt/getContext/
// waitContextBody/findRoleAgg/findPartAgg (e2e_009001),
// waitTelemetryLines/readTelemetryFile/containsPath (e2e_009000). Cero
// red externa (upstreams fake en httptest).

package proxy_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// buildEpic009 construye el harness COMPUESTO del epic 009: las 3
// features habilitadas SIMULTÁNEAMENTE (telemetría 009-000 vía
// telemetryLogger en proxy.New, análisis de contexto 009-001 vía
// SetContextAnalysis, sticky routing 009-002 vía SetStickyRouting +
// Options.StickyMaxEntries). Multi-cliente (patrón buildContextMultiClient
// de e2e_009001): devuelve el server compartido, un harness por cliente,
// el router (para inspeccionar la afinidad vía Affinity()), los upstreams
// contadores (stickyUpstream) y la ruta del telemetry.jsonl. El caller
// setea hs[i].key con la key plana del cliente i.
func buildEpic009(t *testing.T, ups []*upstream, clients []auth.Client, keys []string, o router.Options, opLogger *slog.Logger) (*httptest.Server, []*harness, *router.Router, []*stickyUpstream, string) {
	t.Helper()
	if len(clients) != len(keys) {
		t.Fatalf("buildEpic009: %d clients, %d keys", len(clients), len(keys))
	}
	telemetryPath := filepath.Join(t.TempDir(), "telemetry.jsonl")
	tl, closer, err := logging.BuildTelemetry(telemetryPath)
	if err != nil {
		t.Fatalf("BuildTelemetry: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	sups := make([]*stickyUpstream, len(ups))
	for i, u := range ups {
		su := &stickyUpstream{ups: u}
		sups[i] = su
		srv := httptest.NewServer(su.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	if o.StickyMaxEntries == 0 {
		o.StickyMaxEntries = 100
	}
	r := router.NewWithOptions(specs, o)
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, opLogger, tl, 10<<20, nil, nil, 0)
	// Las 3 features ON (009-000 ya está vía telemetryLogger en New):
	s.SetContextAnalysis(true, 50)
	s.SetStickyRouting(true)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	hs := make([]*harness, len(clients))
	for i := range clients {
		hs[i] = &harness{srv: srv, ups: ups, clients: clients, proxySrv: s}
	}
	return srv, hs, r, sups, telemetryPath
}

// TestE2EEpic009_FlujoCompuesto — criterio compuesto del epic (plan.md
// C1+C3+C4): un request con sesión atraviesa telemetría + composición +
// sticky sin pisarse; el siguiente de la misma sesión acumula en el mismo
// record y rutea al mismo provider; el tráfico sin sesión va al record de
// cliente sin contaminar la sesión; privacidad (nunca contenido) y
// aislamiento entre clientes con la misma sesión.
func TestE2EEpic009_FlujoCompuesto(t *testing.T) {
	secrets := []string{
		"EPIC-CROSS-SECRET-PROMPT-0001",   // content del req1 (con sesión)
		"EPIC-CROSS-SECRET-ARGS-0002",     // arguments del tool_call del req2
		"EPIC-CROSS-SECRET-OPENCLAW-0003", // content del req3 (sin sesión)
	}
	keys := []string{"sk-test-1", "sk-other-1"}
	clients := []auth.Client{
		{ID: "test", KeySHA256: auth.HashKey(keys[0])},
		{ID: "other", KeySHA256: auth.HashKey(keys[1])},
	}

	// up1 reporta usage.prompt_tokens=42 (habilita la fase 2 del 009-001
	// en el flujo compuesto); up2 responde genérico. Ambos sirven "m".
	_, hs, r, sups, telemetryPath := buildEpic009(t,
		[]*upstream{upstreamUsagePrompt("m", 42), upstreamOK("m", "de B")},
		clients, keys, router.Options{
			MaxRetries:       2,
			Cooldown:         0,
			GlobalTimeout:    30 * time.Second,
			StickyMaxEntries: 100,
		}, nil)
	hs[0].key = keys[0]
	hs[1].key = keys[1]

	t.Run("request1_telemetria_composicion_sticky", func(t *testing.T) {
		// Request 1 de la sesión s1: atraviesa las 3 features.
		resp := chatWithHeaders(t, hs[0], keys[0], map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": secrets[0]}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		// (a) 009-000: UN evento request_telemetry con key paths del body y
		// el header X-Session-Id; el contenido del prompt NO aparece.
		lines := waitTelemetryLines(t, telemetryPath, 1)
		if len(lines) != 1 {
			t.Fatalf("eventos = %d, want 1 (C2 009-000)", len(lines))
		}
		ev := lines[0]
		if ev["msg"] != "request_telemetry" || ev["client_id"] != "test" {
			t.Fatalf("evento inválido: msg=%v client_id=%v", ev["msg"], ev["client_id"])
		}
		hdrs, ok := ev["headers"].(map[string]any)
		if !ok || hdrs["X-Session-Id"] != "s1" {
			t.Fatalf("evento sin X-Session-Id=s1 en headers (C3 009-000): %v", ev["headers"])
		}
		keyPaths, ok := ev["key_paths"].([]any)
		if !ok {
			t.Fatalf("evento sin key_paths: %v", ev)
		}
		for _, p := range []string{"model", "messages[].role", "messages[].content"} {
			if !containsPath(keyPaths, p) {
				t.Fatalf("key_paths sin %q (P6 009-000): %v", p, keyPaths)
			}
		}
		if strings.Contains(readTelemetryFile(t, telemetryPath), secrets[0]) {
			t.Fatal("PRIVACIDAD (P10 009-000): el contenido del prompt filtró a telemetry.jsonl")
		}

		// (b) 009-001: /v1/context?session=s1 registró el record
		// client|session (fase 1) con composición estructural.
		status, body := getContext(t, hs[0].srv.URL, keys[0], "s1")
		if status != 200 {
			t.Fatalf("GET /v1/context?session=s1 status = %d, want 200", status)
		}
		if body["client"] != "test" || body["scope"] != "session" || body["session"] != "s1" {
			t.Fatalf("body client/scope/session = %v/%v/%v", body["client"], body["scope"], body["session"])
		}
		if body["requests"] != float64(1) {
			t.Fatalf("requests = %v, want 1 (P3 009-001)", body["requests"])
		}
		summary, ok := body["summary"].(map[string]any)
		if !ok {
			t.Fatalf("summary ausente: %v", body)
		}
		roles, _ := summary["roles"].([]any)
		if u := findRoleAgg(roles, "user"); u == nil || u["messages"] != float64(1) {
			t.Fatalf("role user = %v, want 1 msg (P1 009-001)", u)
		}
		parts, _ := summary["part_types"].([]any)
		if tx := findPartAgg(parts, "text"); tx == nil || tx["count"] != float64(1) {
			t.Fatalf("part text = %v, want count 1 (P1 009-001)", tx)
		}
		if latest, ok := body["latest"].(map[string]any); !ok || latest["prompt_tokens_estimated"] != float64(len(secrets[0])/4) {
			t.Fatalf("latest.prompt_tokens_estimated = %v, want %d (est = len(content)/4)", body["latest"], len(secrets[0])/4)
		}

		// (c) 009-002: el provider ganador quedó registrado en la afinidad
		// (P6 009-002 — recordCacheTokens).
		if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
			t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true) (P6 009-002)", got, ok)
		}

		// (d) Fase 2 del 009-001 en el MISMO recordCacheTokens que registró
		// la afinidad: el usage upstream (42) setea prompt_tokens_actual
		// de la entry — las 3 features coexisten en el mismo request.
		wb := waitContextBody(t, hs[0].srv.URL, keys[0], "s1", func(b map[string]any) bool {
			latest, ok := b["latest"].(map[string]any)
			return ok && latest["prompt_tokens_actual"] != nil
		})
		latest := wb["latest"].(map[string]any)
		if latest["prompt_tokens_actual"] != float64(42) {
			t.Fatalf("latest.prompt_tokens_actual = %v, want 42 (P4 009-001)", latest["prompt_tokens_actual"])
		}
	})

	t.Run("request2_sticky_mismo_provider", func(t *testing.T) {
		// Request 2 de la misma sesión s1 (con tool_call para ejercitar el
		// part_type tool_use y el secreto de arguments).
		resp := chatWithHeaders(t, hs[0], keys[0], map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model": "m",
			"messages": []map[string]any{{
				"role":       "user",
				"content":    "second message",
				"tool_calls": []map[string]any{{"function": map[string]any{"name": "f", "arguments": secrets[1]}}},
			}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		// (b) 009-002: el MISMO provider atendió ambos requests (reorder
		// post-filtro al preferido, 1 solo intento — C2 009-002).
		if sups[0].calls() != 2 || sups[1].calls() != 0 {
			t.Fatalf("req2: calls = up1:%d up2:%d, want up1:2 up2:0 (sticky aplicó — C2 009-002)", sups[0].calls(), sups[1].calls())
		}

		// (a) 009-001: la composición acumula en el MISMO record.
		status, body := getContext(t, hs[0].srv.URL, keys[0], "s1")
		if status != 200 || body["requests"] != float64(2) {
			t.Fatalf("session s1 requests = %d/%v, want 2 (C5 009-001)", status, body["requests"])
		}
		summary := body["summary"].(map[string]any)
		if summary["num_tools_total"] != float64(1) {
			t.Fatalf("num_tools_total = %v, want 1 (P1 009-001)", summary["num_tools_total"])
		}
		roles, _ := summary["roles"].([]any)
		if u := findRoleAgg(roles, "user"); u == nil || u["messages"] != float64(2) {
			t.Fatalf("role user = %v, want 2 msg (acumulado — C5 009-001)", u)
		}
		parts, _ := summary["part_types"].([]any)
		if tx := findPartAgg(parts, "text"); tx == nil || tx["count"] != float64(2) {
			t.Fatalf("part text = %v, want count 2 (req1 + req2)", tx)
		}
		if tu := findPartAgg(parts, "tool_use"); tu == nil || tu["count"] != float64(1) {
			t.Fatalf("part tool_use = %v, want count 1 (P1 009-001)", tu)
		}

		// (c) 009-000: un evento por request — 2 eventos totales.
		if lines := waitTelemetryLines(t, telemetryPath, 2); len(lines) != 2 {
			t.Fatalf("eventos = %d, want 2 (un evento por request — C2 009-000)", len(lines))
		}
	})

	t.Run("request3_sin_sesion_openclaw_zot", func(t *testing.T) {
		// Request 3 SIN X-Session-Id (openclaw/zot): va al record de
		// cliente (client|) y a la clave sticky client|.
		resp := hs[0].chat(t, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": secrets[2]}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("req3 status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		// (a) 009-001: el record de cliente acumula TODO (incluye el
		// request sin sesión — C7/C15 009-001).
		status, body := getContext(t, hs[0].srv.URL, keys[0], "")
		if status != 200 || body["scope"] != "client" || body["requests"] != float64(3) {
			t.Fatalf("GET /v1/context = %d/%v/%v, want 200/client/3 (C7 009-001)", status, body["scope"], body["requests"])
		}

		// (b) 009-001: el request sin sesión NO contamina la sesión.
		statusS, sessBody := getContext(t, hs[0].srv.URL, keys[0], "s1")
		if statusS != 200 || sessBody["requests"] != float64(2) {
			t.Fatalf("session s1 requests = %d/%v, want 2 (sin contaminación — C7 009-001)", statusS, sessBody["requests"])
		}

		// (c) 009-002: el request sin sesión usó la clave client| (test|) y
		// la clave de la sesión NO cambió (P2 009-002 — claves separadas).
		if got, ok := r.Affinity().Get("test|"); !ok || got != "up1" {
			t.Fatalf("afinidad test| = (%q,%v), want (up1,true) (C7 009-002)", got, ok)
		}
		if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
			t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true) — no cambió (P2 009-002)", got, ok)
		}
		if sups[0].calls() != 3 || sups[1].calls() != 0 {
			t.Fatalf("req3: calls = up1:%d up2:%d, want up1:3 up2:0", sups[0].calls(), sups[1].calls())
		}

		// 009-000: 3 eventos totales.
		if lines := waitTelemetryLines(t, telemetryPath, 3); len(lines) != 3 {
			t.Fatalf("eventos = %d, want 3", len(lines))
		}
	})

	t.Run("privacidad_grep_negativo", func(t *testing.T) {
		// Criterio 1 del epic: el contenido de los prompts NUNCA aparece en
		// telemetría ni en /v1/context (strings distintivos → grep
		// negativo — C14 009-000, C13 009-001, 005-005).
		telemetryContent := readTelemetryFile(t, telemetryPath)
		for _, secret := range secrets {
			if strings.Contains(telemetryContent, secret) {
				t.Fatalf("PRIVACIDAD: %q en telemetry.jsonl (P10 009-000):\n%s", secret, telemetryContent)
			}
		}

		assertNoSecrets := func(body map[string]any, label string) {
			t.Helper()
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal /v1/context: %v", err)
			}
			for _, secret := range secrets {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("PRIVACIDAD: %q en /v1/context (%s) (P7 009-001):\n%s", secret, label, raw)
				}
			}
		}
		_, sessBody := getContext(t, hs[0].srv.URL, keys[0], "s1")
		assertNoSecrets(sessBody, "session s1")
		_, clientBody := getContext(t, hs[0].srv.URL, keys[0], "")
		assertNoSecrets(clientBody, "client")
	})

	t.Run("aislamiento_otro_cliente_misma_sesion", func(t *testing.T) {
		// Criterio 3 del epic (009-001 I3): otro cliente con el MISMO
		// session id no ve el record del primero.
		resp := chatWithHeaders(t, hs[1], keys[1], map[string]string{"X-Session-Id": "s1"}, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "other client same session"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("other status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)

		// El record de other|s1 es SUYO (1 request), no el de test (2).
		statusO, bodyO := getContext(t, hs[1].srv.URL, keys[1], "s1")
		if statusO != 200 || bodyO["client"] != "other" || bodyO["requests"] != float64(1) {
			t.Fatalf("other session s1 = %d/%v/%v, want 200/other/1 (I3 009-001)", statusO, bodyO["client"], bodyO["requests"])
		}
		// test|s1 sigue en 2: el tráfico de other no contamina.
		statusT, bodyT := getContext(t, hs[0].srv.URL, keys[0], "s1")
		if statusT != 200 || bodyT["requests"] != float64(2) {
			t.Fatalf("test session s1 = %d/%v, want 200/2 (sin contaminación — I3 009-001)", statusT, bodyT["requests"])
		}

		// Records de cliente también aislados.
		_, bodyOC := getContext(t, hs[1].srv.URL, keys[1], "")
		if bodyOC["requests"] != float64(1) {
			t.Fatalf("other client = %v, want 1", bodyOC["requests"])
		}
		_, bodyTC := getContext(t, hs[0].srv.URL, keys[0], "")
		if bodyTC["requests"] != float64(3) {
			t.Fatalf("test client = %v, want 3", bodyTC["requests"])
		}

		// 009-002: el sticky también funciona para el segundo cliente
		// (clave other|s1, independiente de test|s1 — C6 009-002).
		if got, ok := r.Affinity().Get("other|s1"); !ok || got != "up1" {
			t.Fatalf("afinidad other|s1 = (%q,%v), want (up1,true) (C6 009-002)", got, ok)
		}

		// 009-000: la telemetría también cubre al segundo cliente — 4
		// eventos totales, incluido el client_id "other".
		lines := waitTelemetryLines(t, telemetryPath, 4)
		if len(lines) != 4 {
			t.Fatalf("eventos = %d, want 4", len(lines))
		}
		foundOther := false
		for _, l := range lines {
			if l["client_id"] == "other" {
				foundOther = true
			}
		}
		if !foundOther {
			t.Fatalf("sin evento del cliente other en telemetry.jsonl:\n%s", readTelemetryFile(t, telemetryPath))
		}
	})
}
