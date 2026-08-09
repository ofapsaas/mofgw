// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-002-sticky-session — C2-C12 (e2e).
// Verifican el wiring del proxy con SetStickyRouting(true): handleChat
// llama CompleteFor/StreamFor con key = clientID + "|" + sessionID y
// recordCacheTokens registra al ganador en Affinity(). Upstreams fake
// (httptest) con contador de requests; cero red externa. La afinidad se
// inspecciona vía la API pública del router (Affinity(), spec D2).
//
// RED por compilación (patrón 009-001): proxy.SetStickyRouting,
// router.Options.StickyMaxEntries y router.Affinity() aún no existen.
// Firmas que fijan (spec §Contrato — firmas):
//
//	func (s *Server) SetStickyRouting(enabled bool)
//	Options.StickyMaxEntries int
//	func (r *Router) Affinity() *AffinityStore
//
// keying: sessionID != "" → clientID|sessionID; sessionID == "" → clientID| (P2).

package proxy_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// stickyUpstream envuelve un upstream y cuenta requests (para verificar
// "1 solo intento" y qué provider se probó — C2/C3/C6).
type stickyUpstream struct {
	ups   *upstream
	mu    sync.Mutex
	count int
}

func (s *stickyUpstream) handler() http.HandlerFunc {
	inner := s.ups.handler()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.count++
		s.mu.Unlock()
		inner(w, r)
	}
}

func (s *stickyUpstream) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// buildSticky construye el harness con sticky ON (SetStickyRouting(true)),
// un router con las Options dadas (StickyMaxEntries del caller; 0 → 100) y
// upstreams contadores. Devuelve el harness, el router (para inspeccionar
// la afinidad vía Affinity()) y los upstreams contadores.
func buildSticky(t *testing.T, ups []*upstream, clientKey string, o router.Options) (*harness, *router.Router, []*stickyUpstream) {
	t.Helper()
	h := &harness{ups: ups, key: clientKey}
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	clients := make([]*provider.Client, len(ups))
	sups := make([]*stickyUpstream, len(ups))
	for i, u := range ups {
		su := &stickyUpstream{ups: u}
		sups[i] = su
		srv := httptest.NewServer(su.handler())
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
	if o.StickyMaxEntries == 0 {
		o.StickyMaxEntries = 100
	}
	r := router.NewWithOptions(specs, o)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey(clientKey)}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, o.Logger, nil, 10<<20, nil, nil, 0)
	s.SetStickyRouting(true)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, r, sups
}

// ---- C2 — afinidad por sesión ----

func TestE2E009002_AfinidadPorSesion(t *testing.T) {
	h, r, sups := buildSticky(t, []*upstream{upstreamOK("m", "de A"), upstreamOK("m", "de B")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})

	// req1 de s1 (sin afinidad) → A (orden de config); afinidad test|s1 = A.
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[0].calls() != 1 || sups[1].calls() != 0 {
		t.Fatalf("req1: calls = A:%d B:%d, want A:1 B:0 (orden de config)", sups[0].calls(), sups[1].calls())
	}
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true) (C2)", got, ok)
	}

	// req2 de s1 → A (preferido primero), 1 solo intento (sin fallback).
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola de nuevo"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[0].calls() != 2 || sups[1].calls() != 0 {
		t.Fatalf("req2: calls = A:%d B:%d, want A:2 B:0 (preferido primero, sin fallback — C2)", sups[0].calls(), sups[1].calls())
	}
}

// ---- C3 — fallback con cooldown actualiza la afinidad al ganador ----

func TestE2E009002_FallbackCooldownActualizaAfinidad(t *testing.T) {
	h, r, sups := buildSticky(t, []*upstream{upstreamOK("m", "de A"), upstreamOK("m", "de B")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
	})

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true)", got, ok)
	}

	// A entra en cooldown → request de s1 va a B; el ganador B se vuelve
	// el preferido (P6: se registra al ganador post-fallback).
	r.Cooldowns().Set("up1", 24*time.Hour)
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[1].calls() != 1 {
		t.Fatalf("req2: B calls = %d, want 1 (preferido A en cooldown → B)", sups[1].calls())
	}
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up2" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up2,true) — el ganador post-fallback se vuelve preferido (C3)", got, ok)
	}

	// Request siguiente → B (preferido primero).
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req3 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[0].calls() != 1 || sups[1].calls() != 2 {
		t.Fatalf("req3: calls = A:%d B:%d, want A:1 B:2 (B preferido — C3)", sups[0].calls(), sups[1].calls())
	}
}

// ---- C4 — preferido unhealthy no aplica ----

func TestE2E009002_PreferidoUnhealthyNoAplica(t *testing.T) {
	up1 := upstreamOK("m", "de A")
	up1.modelsStatus = 500 // el health check ve a A caído
	up2 := upstreamOK("m", "de B")
	hs := health.New(time.Second, 5*time.Second, nil)
	h, r, sups := buildSticky(t, []*upstream{up1, up2}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Health: hs,
	})
	// Afinidad previa a A (como si hubiera ganado antes).
	r.Affinity().Set("test|s1", "up1")

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[0].calls() != 0 {
		t.Fatalf("A calls = %d, want 0 (unhealthy, preferido no aplica — C4)", sups[0].calls())
	}
	if sups[1].calls() != 1 {
		t.Fatalf("B calls = %d, want 1", sups[1].calls())
	}
	// El ganador B se registra (P6).
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up2" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up2,true)", got, ok)
	}
}

// ---- C6 — aislamiento de sesiones ----

func TestE2E009002_AislamientoDeSesiones(t *testing.T) {
	h, r, sups := buildSticky(t, []*upstream{upstreamOK("m", "de A"), upstreamOK("m", "de B")}, "sk-test-1", router.Options{
		MaxRetries: 1, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	r.Affinity().Set("test|s1", "up1")
	r.Affinity().Set("test|s2", "up2")

	chat := func(session string) {
		t.Helper()
		resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": session}, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "hola"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("%s status = %d, want 200", session, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	chat("s1")
	if sups[0].calls() != 1 || sups[1].calls() != 0 {
		t.Fatalf("s1: calls = A:%d B:%d, want A:1 B:0 (C6)", sups[0].calls(), sups[1].calls())
	}
	chat("s2")
	if sups[0].calls() != 1 || sups[1].calls() != 1 {
		t.Fatalf("s2: calls = A:%d B:%d, want A:1 B:1 (sin contaminación — C6)", sups[0].calls(), sups[1].calls())
	}
}

// ---- C7 — cliente sin sesión (openclaw/zot): clave client| ----

func TestE2E009002_ClienteSinSesion(t *testing.T) {
	h, r, sups := buildSticky(t, []*upstream{upstreamOK("m", "de A"), upstreamOK("m", "de B")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})

	chat := func() {
		t.Helper()
		resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]any{{"role": "user", "content": "hola"}}})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	chat()
	if got, ok := r.Affinity().Get("test|"); !ok || got != "up1" {
		t.Fatalf("afinidad test| = (%q,%v), want (up1,true) (C7)", got, ok)
	}
	chat()
	if sups[0].calls() != 2 || sups[1].calls() != 0 {
		t.Fatalf("2 requests sin sesión: calls = A:%d B:%d, want A:2 B:0 (mismo provider preferido — C7)", sups[0].calls(), sups[1].calls())
	}

	// Un request CON sesión nueva no usa la afinidad test| del cliente.
	r.Affinity().Set("test|", "up2") // la afinidad del cliente ahora apunta a B
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "sx"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if sups[0].calls() != 3 || sups[1].calls() != 0 {
		t.Fatalf("sesión sx: calls = A:%d B:%d, want A:3 B:0 (la clave test|sx no usa la afinidad test| — C7)", sups[0].calls(), sups[1].calls())
	}
}

// ---- C8 — streaming: fallback pre-primer-byte con preferencia + sin
// reconexión post-primer-byte ----

func TestE2E009002_StreamingFallbackPrePrimerByte(t *testing.T) {
	// A devuelve 429 pre-primer-byte; B stream OK. Afinidad s1 → A.
	h, r, sups := buildSticky(t, []*upstream{upstreamFail(429), upstreamStreamOK("m")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	r.Affinity().Set("test|s1", "up1") // afinidad previa a A

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "data: {") || !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("SSE malformado: %q", s)
	}
	if sups[0].calls() != 1 {
		t.Fatalf("A calls = %d, want 1 (preferido probado primero, 429 pre-primer-byte)", sups[0].calls())
	}
	if sups[1].calls() != 1 {
		t.Fatalf("B calls = %d, want 1 (fallback pre-primer-byte con preferencia — C8)", sups[1].calls())
	}
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up2" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up2,true) — el ganador del stream se registra (P6)", got, ok)
	}
}

func TestE2E009002_SinReconexionPostPrimerByte(t *testing.T) {
	// A arranca el stream (primer byte) y muere a mitad: el stream queda
	// pinneado a A; NO reconecta a B (regla 001-003 §B, sin cambios — P5).
	upA := &upstream{status: 200, stream: true, model: "m", body: "data: {\"id\":\"chatcmpl-a\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\n"}
	upB := upstreamStreamOK("m")
	h, r, sups := buildSticky(t, []*upstream{upA, upB}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	r.Affinity().Set("test|s1", "up1")

	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ho") {
		t.Fatalf("stream no contiene el chunk de A: %q", body)
	}
	if sups[1].calls() != 0 {
		t.Fatalf("B calls = %d, want 0 (sin reconexión post-primer-byte — C8)", sups[1].calls())
	}
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true) — stream que arrancó registra al ganador (P6)", got, ok)
	}
}

// ---- C9 — el registro post-éxito no pisa la afinidad en fallos ----

func TestE2E009002_FalloTotalNoActualizaAfinidad(t *testing.T) {
	upA := upstreamOK("m", "de A")
	h, r, _ := buildSticky(t, []*upstream{upA, upstreamFail(500)}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	// req1: A responde → afinidad test|s1 = A.
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true)", got, ok)
	}

	// Ahora AMBOS fallan → 502; la afinidad NO se actualiza (P6).
	upA.status = 500
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 502 {
		t.Fatalf("req2 status = %d, want 502 (todos fallan)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up1,true) — el fallo total NO actualiza (C9)", got, ok)
	}

	// Request rechazado por budget (429, pre-router) → afinidad intacta.
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{
		"m": {InputUSDPerM: 1e9, OutputUSDPerM: 1e9, CacheHitUSDPerM: 1e9},
	})
	h.proxySrv.SetBudget(map[string]config.BudgetConfig{
		"test": {CostUSDMax: 0.01},
	})
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 429 {
		t.Fatalf("req3 status = %d, want 429 (budget excedido)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("afinidad tras 429 = (%q,%v), want (up1,true) — rechazos pre-router no tocan afinidad (C9)", got, ok)
	}
}

// ---- C10 — transparencia estricta: byte-identical + sin headers X-* +
// log sticky_applied ----

func TestE2E009002_TransparenciaByteIdentical(t *testing.T) {
	// Body de referencia: harness legacy (sticky off) con el mismo par de
	// upstreams — el ganador es up2 (A falla 500 → B).
	legacyBody := func() []byte {
		h := build(t, []*upstream{upstreamFail(500), upstreamOK("m", "RESPUESTA-FIJA-009002")}, "sk-test-1")
		resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]any{{"role": "user", "content": "hola"}}})
		if resp.StatusCode != 200 {
			t.Fatalf("legacy status = %d, want 200", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		return b
	}()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h, r, _ := buildSticky(t, []*upstream{upstreamFail(500), upstreamOK("m", "RESPUESTA-FIJA-009002")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, Logger: logger,
	})

	// req1: A falla 500 → gana B; afinidad test|s1 = up2.
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req1 status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up2" {
		t.Fatalf("afinidad test|s1 = (%q,%v), want (up2,true)", got, ok)
	}

	// req2: reorder real (up2 al frente) → respuesta byte-identical al legacy.
	resp = chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("req2 status = %d, want 200", resp.StatusCode)
	}
	stickyBody, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(stickyBody, legacyBody) {
		t.Fatalf("body sticky != legacy (byte-identical — C10):\nsticky: %s\nlegacy: %s", stickyBody, legacyBody)
	}
	// Sin headers X-* nuevos (la única diferencia es la elección interna).
	for k := range resp.Header {
		if strings.HasPrefix(k, "X-") && k != "X-Request-Id" {
			t.Fatalf("header X-* nuevo en respuesta sticky: %q (C10/I1)", k)
		}
	}
	// Log debug sticky_applied cuando el reorder se aplicó (D6).
	if !waitForSubstring(t, &buf, "sticky_applied") {
		t.Fatalf("sin sticky_applied en logs debug (C10):\n%s", buf.String())
	}
}

// ---- C11 — sticky off (default) → legacy ----

func TestE2E009002_StickyOffLegacy(t *testing.T) {
	// sticky off (default): los requests usan Complete/Stream legacy — el
	// store de afinidad nunca se consulta ni se llena (P8).
	u := upstreamOK("m", "de A")
	h, r := buildWithOptions(t, []*upstream{u}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second,
	})
	resp := chatWithHeaders(t, h, "sk-test-1", map[string]string{"X-Session-Id": "s1"}, map[string]any{
		"model":    "m",
		"messages": []map[string]any{{"role": "user", "content": "hola"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (C11)", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	if r.Affinity() == nil {
		t.Fatal("Affinity() = nil, want store (creado pero sin consultar)")
	}
	if r.Affinity().Len() != 0 {
		t.Fatalf("Affinity().Len() = %d, want 0 (sticky off: el store queda vacío — C11)", r.Affinity().Len())
	}
}

// ---- C12 — evicción LRU con cap acotado ----

func TestE2E009002_EviccionLRU(t *testing.T) {
	// max_sessions_retained: 2 (StickyMaxEntries) y 3 sesiones → la más
	// vieja por last-used se evicta; Len <= cap siempre (P3/P9).
	h, r, _ := buildSticky(t, []*upstream{upstreamOK("m", "de A")}, "sk-test-1", router.Options{
		MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second, StickyMaxEntries: 2,
	})
	chat := func(session string) {
		t.Helper()
		hdrs := map[string]string{}
		if session != "" {
			hdrs["X-Session-Id"] = session
		}
		resp := chatWithHeaders(t, h, "sk-test-1", hdrs, map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "hola"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	chat("s1") // test|s1
	chat("s2") // test|s2
	if r.Affinity().Len() != 2 {
		t.Fatalf("Len = %d, want 2", r.Affinity().Len())
	}
	chat("s3") // excede cap → evicta test|s1 (la más vieja por last-used)
	if r.Affinity().Len() != 2 {
		t.Fatalf("Len = %d, want 2 (nunca excede cap — C12)", r.Affinity().Len())
	}
	if got, ok := r.Affinity().Get("test|s1"); ok || got != "" {
		t.Fatalf("Get(test|s1) = (%q,%v), want evictada (C12)", got, ok)
	}
	// Re-request de la sesión evictada → sin afinidad (orden de config) y
	// se re-registra.
	chat("s1")
	if got, ok := r.Affinity().Get("test|s1"); !ok || got != "up1" {
		t.Fatalf("Get(test|s1) = (%q,%v), want (up1,true) tras re-registro (C12)", got, ok)
	}
}
