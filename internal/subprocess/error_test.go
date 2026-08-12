// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// PART B — tests RED de espera cancelable, taxonomía de errores, refusal,
// sanitize y fail-closed (P9..P13, criterios C10..C14). Compilan contra el
// skeleton y fallan por ASSERCIÓN: el skeleton devuelve errors.New(...), que
// NO es *provider.ErrUpstream, así que todos los errors.As fallan.
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
)

// T_wait_cancellable (P9, C10): un request en cola esperando el lock de su
// sesión es cancelable por su context; cancelar aborta la espera y devuelve
// un *ErrUpstream (no el error crudo de contexto); un request posterior de
// la misma sesión completa.
func TestT_WaitCancellable(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_LOCK_FILE", h.lockFile)
	t.Setenv("STUB_SLEEP_MS", "800")

	// Request 1: lento, posee el lock de la sesión.
	var err1 error
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_, err1 = h.p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "A"}))
	}()

	waitForStubStart(t, h.lockFile) // request 1 ya spawneó y tiene el lock

	// Request 2: en cola esperando el lock, cancelable.
	ctx2, cancel := context.WithCancel(clientCtx("client-a"))
	errCh := make(chan error, 1)
	go func() {
		_, e := h.p.Complete(ctx2, chatBody("m1", map[string]any{"role": "user", "content": "B"}))
		errCh <- e
	}()
	cancel()

	select {
	case e := <-errCh:
		var ue *provider.ErrUpstream
		if !errors.As(e, &ue) {
			t.Fatalf("espera cancelada devolvió error crudo (%T %v), want *ErrUpstream", e, e) // RED
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("la espera cancelada no abortó en tiempo razonable")
	}

	<-done1
	_ = err1

	// Request 3: la sesión sigue usable tras la cancelación.
	if _, err := h.p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "C"})); err != nil {
		t.Fatalf("request posterior a cancelación falló: %v", err)
	}
}

// T_err_spawn_network (P10, C11): un fallo de spawn/exec se normaliza a
// *ErrUpstream{Type:"network"}.
func TestT_ErrSpawnNetwork(t *testing.T) {
	backend := &stubBackend{scriptPath: filepath.Join(t.TempDir(), "does-not-exist.sh")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := subprocess.NewProvider("stub", []string{"m1"}, 4096, t.TempDir(), nil, backend, logger)

	_, err := p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("spawn fallido no se normalizó a *ErrUpstream (got %T %v)", err, err) // RED
	}
	if ue.Type != "network" {
		t.Fatalf("spawn fallido debe ser Type==network, got %q", ue.Type)
	}
}

// T_err_nonezero_exit (P10, C11): un exit no-cero se normaliza a un
// *ErrUpstream con mensaje saneado.
func TestT_ErrNonezeroExit(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_EXIT_CODE", "1")
	t.Setenv("STUB_STDERR", "boom")

	_, err := h.p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("exit no-cero no se normalizó a *ErrUpstream (got %T %v)", err, err) // RED
	}
	if ue.Message == "" {
		t.Fatalf("message vacío para un exit no-cero")
	}
}

// T_err_timeout_retryable (P10, C11): el deadline del contexto se convierte
// en *ErrUpstream{Type:"timeout"} (retryable por router.classify).
func TestT_ErrTimeoutRetryable(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_SLEEP_MS", "3000")

	ctx, cancel := context.WithTimeout(clientCtx("client-a"), 50*time.Millisecond)
	defer cancel()

	_, err := h.p.Complete(ctx, chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("timeout no se normalizó a *ErrUpstream (got %T %v)", err, err) // RED
	}
	if ue.Type != "timeout" {
		t.Fatalf("timeout debe ser Type==timeout, got %q", ue.Type)
	}
}

// T_never_leak_ctx_err (P10, C11): ningún error crudo de contexto
// (context.Canceled / DeadlineExceeded, que router.classify marcaría
// no-retryable) se filtra al router; siempre *ErrUpstream.
func TestT_NeverLeakCtxErr(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_SLEEP_MS", "3000")

	ctx, cancel := context.WithCancel(clientCtx("client-a"))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, err := h.p.Complete(ctx, chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("se filtró error de contexto (got %T %v), want *ErrUpstream", err, err) // RED
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error crudo de contexto filtrado al router: %v", err)
	}
}

// T_refusal_not_retryable (P11, C12): una negativa/refusal de policy del CLI
// se normaliza a *ErrUpstream{Type:"invalid_request_error", StatusCode:400}
// (no-retryable por router.classify).
func TestT_RefusalNotRetryable(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_EXIT_CODE", "1")
	t.Setenv("STUB_STDERR", "refused due to policy")

	_, err := h.p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("refusal no se normalizó a *ErrUpstream (got %T %v)", err, err) // RED
	}
	if ue.Type != "invalid_request_error" {
		t.Fatalf("refusal debe ser Type==invalid_request_error, got %q", ue.Type)
	}
	if ue.StatusCode != 400 {
		t.Fatalf("refusal debe ser StatusCode==400, got %d", ue.StatusCode)
	}
}

// T_sanitize_no_leak (P12, C13): el Message de todo ErrUpstream no contiene
// paths/argv/screts internos y tiene longitud <= 300 caracteres.
func TestT_SanitizeNoLeak(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	secret := "/home/secret/private.key"
	t.Setenv("STUB_EXIT_CODE", "1")
	t.Setenv("STUB_STDERR", "config "+secret+" argv "+h.scriptPath)

	_, err := h.p.Complete(clientCtx("client-a"), chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("error no normalizado a *ErrUpstream (got %T %v)", err, err) // RED
	}
	msg := ue.Message
	if strings.Contains(msg, "/home/secret") || strings.Contains(msg, "private.key") || strings.Contains(msg, h.scriptPath) {
		t.Fatalf("ErrUpstream.Message filtra material sensible: %q", msg)
	}
	if len(msg) > 300 {
		t.Fatalf("ErrUpstream.Message demasiado largo: %d chars", len(msg))
	}
}

// T_empty_clientid_fail_closed (P13, C14): un request sin clientID resoluble
// (auth.ClientIDFrom == "") falla con un *ErrUpstream no-retryable (4xx) y
// NO spawnea el CLI (no comparte sesión con otro cliente).
func TestT_EmptyClientIDFailClosed(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	t.Setenv("STUB_LOCK_FILE", h.lockFile)
	t.Setenv("STUB_STDIN_FILE", h.stdinFile)

	_, err := h.p.Complete(makeEmptyCtx(), chatBody("m1", map[string]any{"role": "user", "content": "HELLO"}))
	var ue *provider.ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("sin clientID no devolvió *ErrUpstream (got %T %v)", err, err) // RED
	}
	if ue.StatusCode < 400 || ue.StatusCode >= 500 {
		t.Fatalf("fail-closed debe ser 4xx no-retryable, got %d", ue.StatusCode)
	}
	// No debe haber spawneado: el lock no tiene "start" y el stdin no existe.
	if starts, _ := countInflight(h.lockFile); starts != 0 {
		t.Fatalf("fail-closed spawneó el CLI (start markers=%d)", starts)
	}
	if _, err := os.Stat(h.stdinFile); err == nil {
		t.Fatalf("fail-closed escribió stdin (spawneó el CLI)")
	}
}
