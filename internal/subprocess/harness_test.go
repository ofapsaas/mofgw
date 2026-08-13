// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package subprocess_test

// Harness hermético (D6/I3): un stub CLI POSIX ejecutable + un test-double
// Backend. Cero binario `claude` real, cero red externa.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// buildStubCLI escribe un script POSIX sh que simula el CLI de un backend
// subprocess. Devuelve la ruta del script.
//
// Comportamiento del stub:
//   - copia su stdin a STUB_STDIN_FILE (si está seteado);
//   - escribe "start" en STUB_LOCK_FILE al arrancar y "end" al salir
//     (para contar spawns concurrentes);
//   - duerme STUB_SLEEP_MS ms si está seteado;
//   - emite STUB_STDOUT como stdout, o un JSON OpenAI-compatible canned;
//   - emite STUB_STDERR como stderr si está seteado;
//   - sale con STUB_EXIT_CODE (default 0).
func buildStubCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "stub-cli.sh")
	const body = `#!/bin/sh
if [ -n "$STUB_LOCK_FILE" ]; then
  echo start >> "$STUB_LOCK_FILE"
  trap 'echo end >> "$STUB_LOCK_FILE"' EXIT
fi
if [ -n "$STUB_STDIN_FILE" ]; then
  cat > "$STUB_STDIN_FILE"
else
  cat > /dev/null
fi
if [ -n "$STUB_ENV_FILE" ]; then
  env > "$STUB_ENV_FILE"
fi
if [ -n "$STUB_SLEEP_MS" ]; then
  _sec=$(( (STUB_SLEEP_MS + 999) / 1000 ))
  sleep "$_sec"
fi
if [ -n "$STUB_LONG_LINE" ]; then
  dd if=/dev/zero bs=1048577 count=1 2>/dev/null | tr '\000' 'A'
  printf '\n'
fi
if [ -n "$STUB_STDOUT" ]; then
  printf '%s' "$STUB_STDOUT"
else
  printf '%s' '{"id":"chatcmpl-stub","object":"chat.completion","created":0,"model":"stub","choices":[{"index":0,"message":{"role":"assistant","content":"stub ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}'
fi
printf '\n'
if [ -n "$STUB_STDERR" ]; then
  printf '%s\n' "$STUB_STDERR" >&2
fi
exit "${STUB_EXIT_CODE:-0}"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub cli: %v", err)
	}
	return script
}

// stubBackend es un test-double de subprocess.Backend que registra los
// modelos pasados a Args y traduce el body/out de forma mínima.
type stubBackend struct {
	scriptPath string

	mu     sync.Mutex
	models []string // modelos registrados por Args (en orden de llamada)
}

func (b *stubBackend) Name() string { return "stub" }

func (b *stubBackend) Args(s *subprocess.Session, model string, flags []string) []string {
	b.mu.Lock()
	b.models = append(b.models, model)
	b.mu.Unlock()
	return []string{b.scriptPath, "--model", model}
}

// TranslateReq extrae el contenido del ÚLTIMO mensaje role=="user".
func (b *stubBackend) TranslateReq(body []byte) (string, error) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content, nil
		}
	}
	return "", errors.New("no user message")
}

// TranslateOut parsea raw como ChatResponse.
func (b *stubBackend) TranslateOut(raw []byte, model string) (*provider.ChatResponse, error) {
	var resp provider.ChatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// TranslateStreamOut reenvía cada línea como evento Data, guardando el send
// con select sobre ctx.Done() (D2). El stub SÍ guarda — el RED de ctx-guard
// viene del adapter claude (claude.go), no del stub.
func (b *stubBackend) TranslateStreamOut(ctx context.Context, lines <-chan string, ch chan<- provider.StreamEvent, model string) {
	for ln := range lines {
		select {
		case ch <- provider.StreamEvent{Data: ln}:
		case <-ctx.Done():
			return
		}
	}
}

// IsRefusal reporta si el stderr del CLI señala una negativa de
// policy/safety (P11). Señal genérica: contiene "refused", "policy" o
// "safety" (case-insensitive), p.ej. "refused due to policy".
func (b *stubBackend) IsRefusal(stderr string) bool {
	s := strings.ToLower(stderr)
	return hasStr(s, "refused", "policy", "safety")
}

// IsSessionNotFound reporta si el stderr indica que la sesión no existe.
func (b *stubBackend) IsSessionNotFound(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "no conversation found")
}

// recordedModels devuelve una copia de los modelos registrados por Args.
func (b *stubBackend) recordedModels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.models...)
}

// providerCfg y providerOpt: opciones funcionales para newTestProvider.
type providerCfg struct {
	id         string
	models     []string
	maxTokens  int64
	sessionDir string
}

type providerOpt func(*providerCfg)

func withModels(models ...string) providerOpt {
	return func(c *providerCfg) { c.models = models }
}

func withMaxTokens(n int64) providerOpt {
	return func(c *providerCfg) { c.maxTokens = n }
}

func withID(id string) providerOpt {
	return func(c *providerCfg) { c.id = id }
}

// testHarness agrupa el provider bajo test + el backend/stub para
// inspeccionar salidas.
type testHarness struct {
	p          *subprocess.Provider
	backend    *stubBackend
	scriptPath string
	stdinFile  string
	lockFile   string
	sessionDir string // base de sesiones (para tests TTL P5/P6)
}

// newTestProvider construye el harness completo: stub CLI + stubBackend +
// subprocess.NewProvider. Devuelve el harness para inspeccionar salidas.
func newTestProvider(t *testing.T, opts ...providerOpt) *testHarness {
	t.Helper()
	cfg := providerCfg{
		id:         "stub",
		models:     []string{"m1"},
		maxTokens:  4096,
		sessionDir: t.TempDir(),
	}
	for _, o := range opts {
		o(&cfg)
	}

	scriptPath := buildStubCLI(t)
	backend := &stubBackend{scriptPath: scriptPath}
	stdinFile := filepath.Join(t.TempDir(), "stdin")
	lockFile := filepath.Join(t.TempDir(), "lock")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := subprocess.NewProvider(cfg.id, cfg.models, cfg.maxTokens, cfg.sessionDir, nil, backend, logger)

	return &testHarness{p: p, backend: backend, scriptPath: scriptPath, stdinFile: stdinFile, lockFile: lockFile, sessionDir: cfg.sessionDir}
}

// clientCtx produce un context que transporta un clientID (vía auth.Wrap,
// el único canal público para inyectar la identidad del cliente, ya que la
// key de ctx es privada en auth).
func clientCtx(clientID string) context.Context {
	key := "sk-" + clientID
	authz := auth.New([]auth.Client{{ID: clientID, KeySHA256: auth.HashKey(key)}})

	var out context.Context
	handler := authz.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out = r.Context()
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return out
}

// makeEmptyCtx devuelve un context SIN identidad de cliente (no pasó por
// auth.Wrap), de modo que auth.ClientIDFrom devuelve "" (P13 fail-closed).
func makeEmptyCtx() context.Context {
	return context.Background()
}

// chatBody arma un body chat-completions mínimo con los mensajes dados.
func chatBody(model string, messages ...map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": messages,
	})
	return raw
}

// hasStr reporta si s contiene algún substring de la lista.
func hasStr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
