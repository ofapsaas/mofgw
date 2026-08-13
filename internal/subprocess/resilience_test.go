// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 013-004-resilience-ops (hardening de resiliencia y
// operaciones del provider subprocess). Compilan contra el skeleton (firma
// nueva + campos) y fallan por ASERCIÓN: las postcondiciones P1..P6 no están
// implementadas (fillResponse fabrica, env sin allowlist, sin sc.Err, sin
// sweep TTL/eviction). Mapean a la tabla §4.2 del spec aprobado.
package subprocess_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
	"github.com/ofapsaas/mofgw/internal/subprocess/claude"
)

// fakeClock es un reloj mutable para inyectar via SetNow (P5/P6). Se muta
// SOLO entre requests (no concurrente) para no correr contra -race.
type fakeClock struct {
	t time.Time
}

// T_usage_preserved_nonzero (P1, C2): tras un Complete exitoso donde el
// backend proveyó Usage.TotalTokens > 0, la respuesta final conserva los
// conteos tal cual los entregó el backend. El stub default emite usage
// {1,1,2}; el skeleton fabrica y lo pisa ⇒ RED.
func TestT_UsagePreservedNonzero(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	res, err := h.p.Complete(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Complete falló: %v", err)
	}
	u := res.Response.Usage
	if u.PromptTokens != 1 || u.CompletionTokens != 1 || u.TotalTokens != 2 {
		t.Fatalf("usage NO preservado (el motor fabricó sobre el real): got %+v, want {1 1 2}", u)
	}
}

// T_usage_fabricated_when_zero (P1, C2): cuando el backend NO proveyó un
// TotalTokens no-cero, el usage fabricado es no degenerado y consistente
// (Total >= Prompt y Total >= Completion). Guard: pasa en RED (fillResponse
// ya fabrica) y en GREEN (fabrica solo cuando TotalTokens<=0). Preserva la
// cobertura de fabricación tras P1 (nota §4.2 del spec).
func TestT_UsageFabricatedWhenZero(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	// Stub sin objeto usage (cero/ausente).
	t.Setenv("STUB_STDOUT", `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	res, err := h.p.Complete(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Complete falló: %v", err)
	}
	u := res.Response.Usage
	if u.TotalTokens <= 0 {
		t.Fatalf("usage fabricado no es no-degenerado: %+v", u)
	}
	if u.TotalTokens < u.PromptTokens || u.TotalTokens < u.CompletionTokens {
		t.Fatalf("usage fabricado inconsistente: %+v", u)
	}
}

// T_stream_toolongline_surfaces_err (P2, C3): un stream cuyo stdout contiene
// una línea >1MB termina con StreamEvent.Err normalizado (*ErrUpstream,
// Type "upstream_error") y sin finish fabricado [DONE]. El skeleton no
// chequea sc.Err ⇒ el stream termina "limpio" truncado ⇒ RED.
func TestT_StreamTooLongLineSurfacesErr(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_LONG_LINE", "1")
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	ch, err := h.p.Stream(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Stream falló: %v", err)
	}
	gotErr := false
	gotDone := false
	for ev := range ch {
		if ev.Err != nil {
			gotErr = true
			var ue *provider.ErrUpstream
			if !errors.As(ev.Err, &ue) || ue.Type != "upstream_error" {
				t.Fatalf("sc.Err no normalizado a upstream_error: %T %v", ev.Err, ev.Err)
			}
		}
		if ev.Data == "[DONE]" {
			gotDone = true
		}
	}
	if !gotErr {
		t.Fatalf("línea >1MB no produjo StreamEvent.Err (stream truncado en silencio)") // RED
	}
	if gotDone {
		t.Fatalf("stream con sc.Err no debe emitir finish fabricado [DONE]")
	}
}

// T_child_env_allowlist (P3, C4): el env del hijo contiene PATH, HOME y las
// STUB_*, y NO contiene una variable centinela arbitraria del proceso mofgw.
// El skeleton pasa os.Environ() completo ⇒ la centinela llega al hijo ⇒ RED.
func TestT_ChildEnvAllowlist(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	envFile := filepath.Join(t.TempDir(), "child.env")
	t.Setenv("STUB_ENV_FILE", envFile)
	t.Setenv("MOFGW_SENTINEL_013004", "SECRET_VALUE") // arbitraria, no-STUB

	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})
	if _, err := h.p.Complete(clientCtx("client-a"), body); err != nil {
		t.Fatalf("Complete falló: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("el stub no escribió su env: %v", err)
	}
	envStr := string(data)
	for _, required := range []string{"PATH=", "HOME="} {
		if !strings.Contains(envStr, required) {
			t.Fatalf("env del hijo sin %q: %q", required, envStr)
		}
	}
	if !strings.Contains(envStr, "STUB_ENV_FILE=") {
		t.Fatalf("env del hijo sin variable STUB_*: %q", envStr)
	}
	if strings.Contains(envStr, "MOFGW_SENTINEL_013004") {
		t.Fatalf("env del hijo filtra material sensible/arbitrario: %q", envStr) // RED
	}
}

// T_abandoned_stream_releases_lock (P4, C5): abandonar un stream (cancelar
// ctx) y luego emitir un Complete de la MISMA sesión completa dentro de un
// plazo acotado (el lock se liberó). Guard: pasa en RED — el engine ya
// guarda sus sends con ctx.Done() (013-001 P8); P4 mueve el guard al adapter
// sin regresar el desenlace a nivel engine.
func TestT_AbandonedStreamReleasesLock(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_LOCK_FILE", h.lockFile)
	t.Setenv("STUB_SLEEP_MS", "1500")
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	ctx, cancel := context.WithCancel(clientCtx("client-a"))
	ch, err := h.p.Stream(ctx, body)
	if err != nil {
		t.Fatalf("Stream falló: %v", err)
	}
	waitForStubStart(t, h.lockFile) // el stream spawneó y posee el lock
	cancel()                        // el consumidor abandona
	for range ch {                  // drenar hasta cierre

	}

	done := make(chan error, 1)
	go func() {
		_, e := h.p.Complete(clientCtx("client-a"), body)
		done <- e
	}()
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("Complete tras abandonar stream falló: %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("el lock NO se liberó tras abandonar el stream (Complete bloqueó)")
	}
}

// T_ttl_stale_dir_removed (P5, C6): tras un sweep, un dir de cliente cuya
// actividad más reciente predata el TTL es eliminado. El skeleton no hace
// sweep ⇒ el dir stale persiste ⇒ RED.
func TestT_TTLStaleDirRemoved(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	h.p.SetSessionTTL(1 * time.Second)

	staleDir := filepath.Join(h.sessionDir, "clients", "client-stale")
	if err := os.MkdirAll(staleDir, 0o700); err != nil {
		t.Fatalf("mkdir stale dir: %v", err)
	}
	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(staleDir, past, past); err != nil {
		t.Fatalf("chtimes stale dir: %v", err)
	}

	// trigger del sweep: un request de otro cliente (resolveSession barre).
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})
	if _, err := h.p.Complete(clientCtx("client-trigger"), body); err != nil {
		t.Fatalf("Complete de trigger falló: %v", err)
	}

	if _, err := os.Stat(staleDir); err == nil {
		t.Fatalf("dir de cliente stale no fue eliminado tras el sweep (TTL)") // RED
	}
}

// T_ttl_active_dir_kept (P5, C6): un dir con un request in-flight (lock
// sostenido) NO se elimina aunque sea viejo, mientras un dir idle stale de la
// misma pasada sí. El skeleton no barre ⇒ el dir idle queda ⇒ RED (en la
// aserción del dir idle barrido).
func TestT_TTLActiveDirKept(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	h.p.SetSessionTTL(1 * time.Second)
	t.Setenv("STUB_LOCK_FILE", h.lockFile)
	t.Setenv("STUB_SLEEP_MS", "1200")
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})
	past := time.Now().Add(-2 * time.Second)

	// Dir idle stale (debe ser barrido por el sweep).
	idleDir := filepath.Join(h.sessionDir, "clients", "client-idle")
	if err := os.MkdirAll(idleDir, 0o700); err != nil {
		t.Fatalf("mkdir idle dir: %v", err)
	}
	_ = os.Chtimes(idleDir, past, past)

	// Request in-flight que sostiene el lock de client-active.
	doneA := make(chan error, 1)
	go func() {
		_, e := h.p.Complete(clientCtx("client-active"), body)
		doneA <- e
	}()
	waitForStubStart(t, h.lockFile)
	// Hacer stale el dir del request in-flight: el sweep debe NO tocarlo
	// por tener el lock sostenido, no por el mtime.
	activeDir := filepath.Join(h.sessionDir, "clients", "client-active")
	_ = os.Chtimes(activeDir, past, past)

	// trigger del sweep con un tercer cliente.
	if _, err := h.p.Complete(clientCtx("client-trigger"), body); err != nil {
		t.Fatalf("Complete de trigger falló: %v", err)
	}

	if _, err := os.Stat(idleDir); err == nil {
		t.Fatalf("dir idle stale no fue eliminado tras el sweep") // RED
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("dir con request in-flight fue eliminado por el sweep: %v", err)
	}
	<-doneA
}

// T_lock_map_bounded (P6, C7): tras un sweep, las entradas idle más allá del
// TTL y libres son evictadas (LockCount acotado a las activas). El skeleton
// no evicta ⇒ el map conserva todas las entradas idle ⇒ RED.
func TestT_LockMapBounded(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	h.p.SetSessionTTL(1 * time.Second)
	clock := &fakeClock{t: time.Now()}
	h.p.SetNow(func() time.Time { return clock.t })
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	// N sesiones idle (cada Complete deja una entrada en el lock map).
	for _, c := range []string{"c1", "c2", "c3", "c4", "c5"} {
		if _, err := h.p.Complete(clientCtx(c), body); err != nil {
			t.Fatalf("Complete %s falló: %v", c, err)
		}
	}
	if before := h.p.LockCount(); before < 5 {
		t.Fatalf("se esperaban >=5 entradas de lock, got %d", before)
	}

	clock.t = clock.t.Add(2 * time.Second) // todas idle más allá del TTL

	// trigger del sweep: una sesión activa nueva.
	if _, err := h.p.Complete(clientCtx("c6"), body); err != nil {
		t.Fatalf("Complete trigger falló: %v", err)
	}

	if after := h.p.LockCount(); after > 1 {
		t.Fatalf("lock map no acotado: %d entradas tras sweep (idle no evictadas)", after) // RED
	}
}

// T_evicted_session_reusable (P6, C7): tras el sweep, una sesión evictada se
// re-crea al volver a pedir y completa. El skeleton no evicta ⇒ LockCount no
// baja ⇒ RED (en la aserción de eviction).
func TestT_EvictedSessionReusable(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	h.p.SetSessionTTL(1 * time.Second)
	clock := &fakeClock{t: time.Now()}
	h.p.SetNow(func() time.Time { return clock.t })
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	if _, err := h.p.Complete(clientCtx("client-a"), body); err != nil {
		t.Fatalf("Complete inicial falló: %v", err)
	}
	clock.t = clock.t.Add(2 * time.Second) // client-a idle más allá del TTL

	// trigger del sweep.
	if _, err := h.p.Complete(clientCtx("client-b"), body); err != nil {
		t.Fatalf("Complete trigger falló: %v", err)
	}

	// La sesión idle fue evictada (el lock map bajó a las activas).
	if got := h.p.LockCount(); got > 1 {
		t.Fatalf("sesión idle no evictada: LockCount=%d", got) // RED
	}

	// Re-request de la sesión evictada: se re-crea y completa.
	if _, err := h.p.Complete(clientCtx("client-a"), body); err != nil {
		t.Fatalf("sesión evictada no re-creable al volver a pedir: %v", err)
	}
}

// T_session_not_found_retry_creates — regresión smoke real (12 Ago): el
// estado `New` por dir es stale (el dir existe pero la sesión claude no).
// El motor debe reintentar con creación (-n) cuando --resume falla con
// "no conversation found". El stub falla en --resume y funciona en -n.
func TestT_SessionNotFoundRetryCreates(t *testing.T) {
	// Stub que: con --resume en argv → stderr "No conversation found" + exit 1;
	// con -n → stdout claude stream-json válido + exit 0.
	dir := t.TempDir()
	script := filepath.Join(dir, "stub.sh")
	body := `#!/bin/sh
if printf '%s' "$@" | grep -q -- '--resume'; then
  echo "No conversation found with session ID: x" >&2
  exit 1
fi
printf '%s\n' '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"created ok"}}'
printf '%s\n' '{"type":"message_delta","delta":{"stop_reason":"end_turn"}}'
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	backend := claude.New(script)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// New=false (sesión "existe") pero el resume falla → debe reintentar con -n.
	p := subprocess.NewProvider("stub", []string{"m1"}, 4096, t.TempDir(), nil, backend, logger)

	body2 := chatBody("m1", map[string]any{"role": "user", "content": "hi"})
	res, err := p.Complete(clientCtx("client-a"), body2)
	if err != nil {
		t.Fatalf("Complete con resume fallido + retry -n falló: %v", err)
	}
	if got := res.Response.Choices[0].Message.Content; got != "created ok" {
		t.Fatalf("content = %q, want %q (retry -n debió crear)", got, "created ok")
	}
}
