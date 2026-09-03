// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 018-001-opencode-session-header: header
// x-opencode-session (knob declarativo por provider) + User-Agent propio
// en las llamadas upstream. Verifican las postcondiciones P1-P7 y P9 del
// spec docs/specs/018-001-opencode-session-header/spec.md (bloques B1-B8
// del test-audit.md).
//
// Contrato observable (CDAD stage-3-tdd): headers capturados por el
// upstream httptest (u.gotHeaders, patrón e2e_test.go). NUNCA se
// referencian símbolos internos nuevos de la feature (RequestMeta, etc.
// — riesgo 1 del audit): el knob viaja SOLO por config YAML →
// config.LoadFile → providerfactory.BuildProvider (ruta observable de
// main.go), y las aserciones son sobre headers HTTP salientes.
//
// Estrategia anti-compile-error: el parser de config actual ignora la
// clave opencode_session → el knob queda INERTE → el header nunca se
// inyecta → los asserts positivos (P1/P2/P3/P5/P6/P7) fallan por
// AssertionError. Cuando el implementer agregue el campo + wiring, los
// mismos tests pasan sin modificación. Constante de versión asertada
// literal: "mofgw/0.1.0" (D5, riesgo 1 del audit).
//
// B8 (P9 warn): se cubre la ausencia del header; el warn de log queda
// como comportamiento no-aseverado (AP-14: sin mock de plumbing) — ver
// comentario en el test.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/providerfactory"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- helpers: harness construido desde config (ruta observable de main) ----

// wantUA es el User-Agent exacto que todo request upstream debe llevar (D5/P5).
const wantUA = "mofgw/0.1.0"

// ocSessionYAML arma un config con 2 providers: opencode-a (knob
// opencode_session: true, sirve "ses-model") y plain-b (sin knob, sirve
// "plain-model"). I1: el knob es declarativo en config, nunca inferido.
func ocSessionYAML(urlA, urlB string) string {
	return `server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: opencode-a
    base_url: "` + urlA + `"
    api_key_env: MOFGW_018001_KEY_A
    models: ["ses-model"]
    opencode_session: true
  - id: plain-b
    base_url: "` + urlB + `"
    api_key_env: MOFGW_018001_KEY_B
    models: ["plain-model"]
`
}

// buildWithConfigProviders construye el harness con los providers creados
// vía providerfactory.BuildProvider a partir de un config YAML real
// (config.LoadFile — misma ruta observable que cmd/mofgw/main.go). Los
// upstreams se levantan primero para inyectar sus URLs en el YAML.
// Devuelve también los health.Checker (provider.Client) para el subtest
// models de B5. Los clients de auth se pasan para controlar el client_id
// (P2: fallback sanitizado).
func buildWithConfigProviders(t *testing.T, ups []*upstream, clients []auth.Client) (*harness, []health.Checker) {
	t.Helper()

	// 1. Upstreams fake primero (sus URLs van al YAML).
	urls := make([]string, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		urls[i] = srv.URL
	}

	// 2. YAML con el knob declarativo (I1) y carga con el loader real (P10).
	yaml := ocSessionYAML(urls[0], urls[1])
	t.Setenv("MOFGW_018001_KEY_A", "sk-up-a")
	t.Setenv("MOFGW_018001_KEY_B", "sk-up-b")
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(cfg.Providers))
	}

	// 3. Providers vía factory (ruta de main.go) + harness estándar.
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	var checkers []health.Checker
	for i, pc := range cfg.Providers {
		cl, err := providerfactory.BuildProvider(pc, nil)
		if err != nil {
			t.Fatalf("BuildProvider(%s): %v", pc.ID, err)
		}
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
		if hc, ok := cl.(health.Checker); ok {
			checkers = append(checkers, hc)
		}
	}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, nil)
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, nil, 10<<20, nil, nil, 0)
	h := &harness{ups: ups, key: "sk-knob-1", proxySrv: s}
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h, checkers
}

// knobAuthClient es el cliente autenticado con client_id ya válido según
// el charset [A-Za-z0-9._-] (P2: fallback == client_id sin mutación).
var knobAuthClient = auth.Client{ID: "client.k-01", KeySHA256: auth.HashKey("sk-knob-1")}

// chatBody es el body mínimo compartido por los tests de este archivo.
func chatBody(model string) map[string]any {
	return map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
}

// chatRawHeader envía POST /v1/chat/completions autenticado con la key
// dada y un header X-Session-Id opcional (vacío = no mandarlo). Devuelve
// el *http.Response para inspección (headers X-Mofgw-Cache en B7).
func (h *harness) chatRawHeader(t *testing.T, key string, body map[string]any, sessionID string) (*http.Response, error) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	if sessionID != "" {
		req.Header.Set("X-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, nil
}

// ---- B1 / C1 / P1: header con sesión real desde X-Session-Id entrante ----

func TestPostcondition1_SessionHeaderFromInbound(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

	raw, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses_abc")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if raw.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", raw.StatusCode)
	}
	got := ups[0].gotHeaders.Get("X-Opencode-Session")
	if got != "ses_abc" {
		t.Fatalf("x-opencode-session upstream = %q, want %q (P1: knob activo inyecta la sesión entrante)", got, "ses_abc")
	}
}

// ---- B2 / C2 / P2: fallback estable al client_id autenticado ----

func TestPostcondition2_FallbackClientID(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

	// Sin X-Session-Id: fallback = client_id del request autenticado.
	raw, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if raw.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", raw.StatusCode)
	}
	got := ups[0].gotHeaders.Get("X-Opencode-Session")
	if got != "client.k-01" {
		t.Fatalf("x-opencode-session upstream = %q, want %q (P2: fallback client_id)", got, "client.k-01")
	}
}

// ---- B3 / C3 / P3: sanitización (charset) y clamp (128) ----

func TestPostcondition3_SanitizeClamp(t *testing.T) {
	t.Run("caracteres invalidos reemplazados por _", func(t *testing.T) {
		ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
		h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

		if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses abc/def+1"); err != nil {
			t.Fatalf("chat: %v", err)
		}
		got := ups[0].gotHeaders.Get("X-Opencode-Session")
		if got != "ses_abc_def_1" {
			t.Fatalf("x-opencode-session = %q, want %q (P3: fuera de [A-Za-z0-9._-] → _)", got, "ses_abc_def_1")
		}
	})

	t.Run("clamp a 128 caracteres", func(t *testing.T) {
		ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
		h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

		long := strings.Repeat("a", 100) + strings.Repeat("@", 100)
		if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), long); err != nil {
			t.Fatalf("chat: %v", err)
		}
		got := ups[0].gotHeaders.Get("X-Opencode-Session")
		want := strings.Repeat("a", 128)
		if got != want {
			t.Fatalf("x-opencode-session len = %d, want 128 y prefijo valido (P3: clamp)\ngot  = %q\nwant = %q", len(got), got, want)
		}
	})

	t.Run("vacio tras sanitizar se omite", func(t *testing.T) {
		ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
		h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

		// Sesión solo con caracteres inválidos → sanitizada queda vacía →
		// P3: header omitido (el warn es P9/B8).
		if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "@@@ !!!"); err != nil {
			t.Fatalf("chat: %v", err)
		}
		if got := ups[0].gotHeaders.Get("X-Opencode-Session"); got != "" {
			t.Fatalf("x-opencode-session = %q, want AUSENTE (P3: valor que sanitiza a vacío se omite)", got)
		}
	})
}

// ---- B4 / C4 / P4: sin knob → sin header (no leak, I2) ----

func TestPostcondition4_NoLeakWithoutKnob(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

	// Al provider SIN knob (plain-b), aunque el cliente mande X-Session-Id:
	raw, err := h.chatRawHeader(t, "sk-knob-1", chatBody("plain-model"), "ses_abc")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if raw.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", raw.StatusCode)
	}
	if got := ups[1].gotHeaders.Get("X-Opencode-Session"); got != "" {
		t.Fatalf("x-opencode-session en provider SIN knob = %q, want AUSENTE (P4/I2: no leak)", got)
	}

	// Y por-provider: el mismo request al provider CON knob sí la lleva
	// (contraste 1:1 del criterio C4: dos providers en config, assert
	// por-provider).
	if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses_abc"); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if got := ups[0].gotHeaders.Get("X-Opencode-Session"); got != "ses_abc" {
		t.Fatalf("x-opencode-session en provider CON knob = %q, want %q (C4: contraste por-provider)", got, "ses_abc")
	}
}

// ---- B5 / C5 / P5: User-Agent propio en chat, models y embeddings ----

func TestPostcondition5_UserAgent(t *testing.T) {
	t.Run("chat completions", func(t *testing.T) {
		ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
		h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

		if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), ""); err != nil {
			t.Fatalf("chat: %v", err)
		}
		ua := ups[0].gotHeaders.Get("User-Agent")
		if ua != wantUA {
			t.Fatalf("User-Agent upstream = %q, want %q (P5/D5)", ua, wantUA)
		}
		for _, banned := range []string{"Go-http-client", "opencode-cli"} {
			if strings.Contains(ua, banned) {
				t.Fatalf("User-Agent upstream %q contiene %q (P5/I4 prohibido)", ua, banned)
			}
		}
	})

	t.Run("models via health check del provider", func(t *testing.T) {
		ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
		_, checkers := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

		// GET /v1/models upstream vía provider.Client (el upstream fake
		// responde /v1/models y captura gotHeaders).
		hs := health.New(time.Second, 5*time.Second, nil)
		for _, cl := range checkers {
			hs.Register(cl)
		}
		hs.CheckNow()

		ua := ups[0].gotHeaders.Get("User-Agent")
		if ua != wantUA {
			t.Fatalf("User-Agent upstream (GET models) = %q, want %q (P5: todo request HTTP upstream)", ua, wantUA)
		}
	})

	t.Run("embeddings", func(t *testing.T) {
		// Patrón e2e_011006 (riesgo 4 del audit): cliente de embeddings
		// dedicado; P11: cumple P5 (UA) y NO inyecta session header.
		emu := embeddingsOK("e-model", [][]float64{{0.1, 0.2}}, 3)
		h := buildWithEmbeddings(t, emu, "k1", "e-model")
		resp := h.embeddings(t, "k1", embeddingsBody("texto", "e-model", 0, ""))
		if resp.StatusCode != 200 {
			t.Fatalf("embeddings status = %d, want 200", resp.StatusCode)
		}
		ua := emu.gotHeaders.Get("User-Agent")
		if ua != wantUA {
			t.Fatalf("User-Agent upstream (embeddings) = %q, want %q (P5/P11)", ua, wantUA)
		}
		if got := emu.gotHeaders.Get("X-Opencode-Session"); got != "" {
			t.Fatalf("x-opencode-session en embeddings = %q, want AUSENTE (P11: embeddings no inyecta sesión)", got)
		}
	})
}

// ---- B6 / C6 / P6: body upstream intacto (I3: headers only) ----

func TestPostcondition6_BodyIntact(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})

	// Request 1: con sesión (escenario knob activo).
	if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses_abc"); err != nil {
		t.Fatalf("chat 1: %v", err)
	}
	// PRECONDICIÓN del escenario: el knob debe estar activo (header
	// presente). Si falta, el test no está ejercitando el camino de la
	// feature (RED: knob inerte).
	if got := ups[0].gotHeaders.Get("X-Opencode-Session"); got != "ses_abc" {
		t.Fatalf("precondición no cumplida: x-opencode-session = %q, want %q (knob inerte — feature no implementada)", got, "ses_abc")
	}
	bodyConSesion := ups[0].gotBody

	// Request 2: mismo body, sin sesión (baseline sin feature).
	if _, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), ""); err != nil {
		t.Fatalf("chat 2: %v", err)
	}
	bodySinSesion := ups[0].gotBody

	if bodyConSesion != bodySinSesion {
		t.Fatalf("body upstream mutado por la feature (I3/P6):\ncon sesion: %s\nsin sesion: %s", bodyConSesion, bodySinSesion)
	}
}

// ---- B7 / C7 / P7: cache key estable ante distinto X-Session-Id ----

func TestPostcondition7_CacheKeyStable(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{knobAuthClient})
	h.proxySrv.SetResponseCache(true, 512, time.Hour)

	// Request 1: sesión "ses_a".
	resp1, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses_a")
	if err != nil {
		t.Fatalf("chat 1: %v", err)
	}
	// PRECONDICIÓN: knob activo (header inyectado en la llamada 1).
	hdr1 := ups[0].gotHeaders.Get("X-Opencode-Session")
	if hdr1 != "ses_a" {
		t.Fatalf("precondición no cumplida: x-opencode-session = %q, want %q (knob inerte — feature no implementada)", hdr1, "ses_a")
	}
	if resp1.Header.Get("X-Mofgw-Cache") != "MISS" {
		t.Fatalf("1er request: X-Mofgw-Cache = %q, want MISS", resp1.Header.Get("X-Mofgw-Cache"))
	}

	// Request 2: mismo body, distinta sesión → cache HIT exact-match (P7:
	// la key no depende del header inyectado).
	resp2, err := h.chatRawHeader(t, "sk-knob-1", chatBody("ses-model"), "ses_b")
	if err != nil {
		t.Fatalf("chat 2: %v", err)
	}
	if resp2.Header.Get("X-Mofgw-Cache") != "HIT" {
		t.Fatalf("2do request (distinto X-Session-Id, mismo body): X-Mofgw-Cache = %q, want HIT (P7: cache estable)", resp2.Header.Get("X-Mofgw-Cache"))
	}
}

// ---- B8 / C8 / P9: omisión con warn cuando no hay valor ----
// NOTA RED: este test es GUARD y puede pasar trivialmente en RED (hoy
// el knob inerte ya omite el header). La mitad positiva del contrato P9
// (el warn) queda no-aseverada por decisión del audit (riesgo 3/AP-14).

func TestPostcondition9_OmitWithWarn(t *testing.T) {
	ups := []*upstream{upstreamOK("ses-model", "x"), upstreamOK("plain-model", "x")}
	// Cliente autenticado con client_id VACÍO (sin sesión entrante tampoco)
	// → P9: request upstream SIN header + warn operativo.
	anonClient := auth.Client{ID: "", KeySHA256: auth.HashKey("sk-anon-1")}
	h, _ := buildWithConfigProviders(t, ups, []auth.Client{anonClient})

	raw, err := h.chatRawHeader(t, "sk-anon-1", chatBody("ses-model"), "")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if raw.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", raw.StatusCode)
	}
	if got := ups[0].gotHeaders.Get("X-Opencode-Session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want AUSENTE (P9: sin sesión ni client_id → omitir)", got)
	}
	// NOTA (B8, riesgo 3 del audit, AP-14): el warn operativo (sin valor,
	// solo nombre) NO se asevera acá — el harness de logging no expone una
	// aserción de warn establecida para el path del provider y no se
	// inventa mock de plumbing. Comportamiento no-aseverado: warn en log
	// operativo; el reviewer/GREEN lo verifica por inspección.
}
