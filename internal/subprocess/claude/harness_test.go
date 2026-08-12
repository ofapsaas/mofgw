// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package claude_test

// Harness hermético (P11/I3): un stub CLI POSIX que emite stream-json de
// claude + helpers locales replicados del patrón de 013-001
// (internal/subprocess/harness_test.go). Cero binario `claude` real, cero
// red externa.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
	"github.com/ofapsaas/mofgw/internal/subprocess/claude"
)

// claudeStreamLines devuelve el stream-json de claude (formato wire) como
// líneas, con el stop_reason inyectado (default "end_turn"). Es la forma
// canonical del stdout del stub y de los unit tests.
func claudeStreamLines(stopReason string) []string {
	if stopReason == "" {
		stopReason = "end_turn"
	}
	return []string{
		`{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":4,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_sequence":null},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
}

// claudeStreamOut devuelve el stream-json agregado (stdout completo en modo
// Complete) uniendo las líneas con "\n".
func claudeStreamOut(stopReason string) []byte {
	return []byte(strings.Join(claudeStreamLines(stopReason), "\n"))
}

// buildClaudeStubCLI escribe un script POSIX sh que simula el CLI de claude
// y devuelve su ruta. Drena stdin (cat > /dev/null) y emite stream-json de
// claude (una línea por salida). Configurable vía env:
//   - STUB_STOP_REASON (override del stop_reason, default "end_turn")
//   - STUB_STDERR     (emitido a stderr, p.ej. texto de refusal)
//   - STUB_EXIT_CODE  (exit code, default 0)
func buildClaudeStubCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "stub-claude.sh")
	const body = `#!/bin/sh
cat > /dev/null
printf '%s\n' '{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":4,"output_tokens":1}}}'
printf '%s\n' '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'
printf '%s\n' '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}'
printf '%s\n' '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}'
printf '%s\n' '{"type":"content_block_stop","index":0}'
printf '%s\n' '{"type":"message_delta","delta":{"stop_reason":"'"${STUB_STOP_REASON:-end_turn}"'","stop_sequence":null},"usage":{"output_tokens":2}}'
printf '%s\n' '{"type":"message_stop"}'
if [ -n "$STUB_STDERR" ]; then
  printf '%s\n' "$STUB_STDERR" >&2
fi
exit "${STUB_EXIT_CODE:-0}"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub claude cli: %v", err)
	}
	return script
}

// newTestAdapter devuelve el adapter real claude.Backend cableado con el
// stub CLI (argv[0] = stub ⇒ cero claude real, P11/I3).
func newTestAdapter(t *testing.T) *claude.Backend {
	t.Helper()
	return claude.New(buildClaudeStubCLI(t))
}

// newIntegratedProvider devuelve un *subprocess.Provider (motor de 013-001,
// intacto) con el adapter real claude.Backend sobre el stub CLI.
func newIntegratedProvider(t *testing.T) *subprocess.Provider {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return subprocess.NewProvider(
		"claude",
		[]string{"claude-sonnet-4-6"},
		4096,
		t.TempDir(),
		nil, // backendFlags opacos
		claude.New(buildClaudeStubCLI(t)),
		logger,
	)
}

// clientCtx produce un context que transporta un clientID (vía auth.Wrap,
// el único canal público para inyectar la identidad del cliente).
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

// makeEmptyCtx devuelve un context SIN identidad de cliente (fail-closed).
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

// chatClaudeBody es un alias semántico de chatBody para los tests del
// adapter (reuso del patrón de 013-001).
func chatClaudeBody(model string, messages ...map[string]any) []byte {
	return chatBody(model, messages...)
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

// deltaContent extrae choices[0].delta.content de un payload de StreamEvent
// (chunk OpenAI). Devuelve "" si no es un chunk con delta de contenido.
func deltaContent(payload string) string {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &chunk) != nil {
		return ""
	}
	if len(chunk.Choices) == 0 {
		return ""
	}
	return chunk.Choices[0].Delta.Content
}

// collectEvents drena un canal de StreamEvent hasta que se cierra.
func collectEvents(ch <-chan provider.StreamEvent) []provider.StreamEvent {
	var out []provider.StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}
