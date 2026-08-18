// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 014-001-registro-unificado — writer dedicado.
// Verifican P3 (append-only + fail-fast de arranque), P4 (thread-safety),
// P5/P6 (esquema exacto de attempt/terminal), P16 (best-effort runtime,
// nivel unitario) y los criterios C3, C10, C12, C16.
//
// RED por compilación: el paquete internal/registry (Writer, NewWriter,
// AttemptEvent, TerminalEvent, Tokens) aún no existe → el paquete no
// compila en RED. El implementer materializa el contrato en GREEN (§2.1
// de la spec: structs tipadas, mutex + json.Marshal + "\n", O_APPEND).

package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// sortedKeys devuelve las keys de un map[string]any ordenadas (para
// comparar sets de keys del esquema como sets, no como multisets).
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// keysIguales: el set de keys de `got` es EXACTAMENTE `want` (sin extras,
// sin faltantes) — P5/P6/C10. Compara ambos lados como SETS: ordena una
// copia de `want` (que se pasa en orden legible del spec) para comparar
// contra el `got` ya ordenado por sortedKeys.
func keysIguales(got map[string]any, want []string) bool {
	g := sortedKeys(got)
	w := make([]string, len(want))
	copy(w, want)
	sort.Strings(w)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// readAllLines lee el archivo y devuelve cada línea no vacía.
func readAllLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// attemptFixture: evento attempt válido de un request en routing.
func attemptFixture() AttemptEvent {
	return AttemptEvent{
		Type:      "attempt",
		RequestID: "req-0001",
		Ts:        "2026-08-18T12:00:00.123Z",
		Client:    "test",
		Provider:  "up1",
		Model:     "m",
		Outcome:   "ok",
		Cause:     "",
		Status:    200,
		Attempt:   1,
		Retries:   0,
	}
}

// terminalFixture: evento terminal success de un request en routing.
func terminalFixture() TerminalEvent {
	return TerminalEvent{
		Type:          "terminal",
		RequestID:     "req-0001",
		Ts:            "2026-08-18T12:00:01.500Z",
		Client:        "test",
		Outcome:       "success",
		ErrorCode:     "",
		Status:        200,
		FinalProvider: "up1",
		Tokens:        Tokens{Prompt: 1200, Completion: 80, Cache: 1000, Reasoning: 30},
		CostUSD:       0.00123,
		Stream:        false,
	}
}

// Test014001_P3_NewWriterFailFast verifica C3/P3: NewWriter con una ruta no
// abrible (directorio inexistente) → error no-nil (fail-fast de arranque).
func Test014001_P3_NewWriterFailFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "x.jsonl")
	w, err := NewWriter(path)
	if err == nil {
		_ = w.Close()
		t.Fatalf("NewWriter(%q) debe fallar (C3/P3): directorio no abrible", path)
	}
}

// Test014001_P3_AppendOnly verifica C3/C11/P3: abrir dos veces el MISMO
// archivo (dos "procesos"/restarts) preserva las líneas previas — nunca
// trunca; el archivo es append-only y sobrevive restarts.
func Test014001_P3_AppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")

	w1, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter #1: %v", err)
	}
	if err := w1.Attempt(attemptFixture()); err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}

	// "restart": abrir de nuevo el mismo archivo → append, no truncar.
	w2, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter #2: %v", err)
	}
	if err := w2.Terminal(terminalFixture()); err != nil {
		t.Fatalf("Terminal: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}

	lines := readAllLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (append preservado tras restart, C11/P3)", len(lines))
	}
}

// Test014001_P5_AttemptSchemaExact verifica C10/P5: tras escribir un attempt,
// la línea parsea y su set de keys es EXACTAMENTE el del esquema (sin
// extras), con tipos correctos.
func Test014001_P5_AttemptSchemaExact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Attempt(attemptFixture()); err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	lines := readAllLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("líneas = %d, want 1", len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("línea no es JSON válido: %v", err)
	}
	want := []string{"type", "request_id", "ts", "client", "provider", "model", "outcome", "cause", "status", "attempt", "retries"}
	if !keysIguales(m, want) {
		t.Fatalf("keys attempt = %v, want exactamente %v (P5/C10)", sortedKeys(m), want)
	}
	if m["type"] != "attempt" || m["outcome"] != "ok" || m["cause"] != "" {
		t.Fatalf("type/outcome/cause = %v/%v/%v", m["type"], m["outcome"], m["cause"])
	}
	// status/attempt/retries son números (float64 al venir de JSON).
	for _, k := range []string{"status", "attempt", "retries"} {
		if _, ok := m[k].(float64); !ok {
			t.Fatalf("%s = %v (%T), want número (float64)", k, m[k], m[k])
		}
	}
}

// Test014001_P6_TerminalSchemaExact verifica C10/P6: tras escribir un
// terminal, la línea parsea y su set de keys es EXACTAMENTE el del esquema
// (sin extras); `tokens` es un objeto anidado con exactamente {prompt,
// completion, cache, reasoning}.
func Test014001_P6_TerminalSchemaExact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Terminal(terminalFixture()); err != nil {
		t.Fatalf("Terminal: %v", err)
	}

	lines := readAllLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("líneas = %d, want 1", len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("línea no es JSON válido: %v", err)
	}
	want := []string{"type", "request_id", "ts", "client", "outcome", "error_code", "status", "final_provider", "tokens", "cost_usd", "stream"}
	if !keysIguales(m, want) {
		t.Fatalf("keys terminal = %v, want exactamente %v (P6/C10)", sortedKeys(m), want)
	}
	tk, ok := m["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens no es objeto anidado: %v (%T)", m["tokens"], m["tokens"])
	}
	if !keysIguales(tk, []string{"prompt", "completion", "cache", "reasoning"}) {
		t.Fatalf("keys tokens = %v, want exactamente {prompt,completion,cache,reasoning} (P6)", sortedKeys(tk))
	}
	if _, ok := m["cost_usd"].(float64); !ok {
		t.Fatalf("cost_usd = %v (%T), want float64", m["cost_usd"], m["cost_usd"])
	}
	if _, ok := m["stream"].(bool); !ok {
		t.Fatalf("stream = %v (%T), want bool", m["stream"], m["stream"])
	}
	if m["type"] != "terminal" || m["outcome"] != "success" || m["error_code"] != "" {
		t.Fatalf("type/outcome/error_code = %v/%v/%v", m["type"], m["outcome"], m["error_code"])
	}
}

// Test014001_P3_JSONLValid verifica C3/P5/P6: tras escribir un attempt + un
// terminal, cada línea es JSON válido y las líneas se distinguen por `type`.
func Test014001_P3_JSONLValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Attempt(attemptFixture()); err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if err := w.Terminal(terminalFixture()); err != nil {
		t.Fatalf("Terminal: %v", err)
	}

	lines := readAllLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (P3)", len(lines))
	}
	types := make(map[string]bool)
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("línea no es JSON válido (P3): %v", err)
		}
		types[m["type"].(string)] = true
	}
	if !types["attempt"] || !types["terminal"] {
		t.Fatalf("deben existir una línea attempt y una terminal, got types=%v", types)
	}
}

// Test014001_P4_ThreadSafety verifica C12/P4: 20 goroutines escribiendo 2
// eventos cada una por el MISMO writer → exactamente 40 líneas JSON
// completas, sin intercalado ni corrupción.
func Test014001_P4_ThreadSafety(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	const goroutines = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			at := attemptFixture()
			at.RequestID = "req-conc-" + string(rune('a'+n))
			_ = w.Attempt(at)
			te := terminalFixture()
			te.RequestID = at.RequestID
			_ = w.Terminal(te)
		}(i)
	}
	wg.Wait()

	lines := readAllLines(t, path)
	if len(lines) != goroutines*2 {
		t.Fatalf("líneas = %d, want %d (C12/P4)", len(lines), goroutines*2)
	}
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("línea corrupta/intercalada (C12): %v", err)
		}
		typ, _ := m["type"].(string)
		if typ != "attempt" && typ != "terminal" {
			t.Fatalf("línea con type inválido %q (C12)", typ)
		}
	}
}

// Test014001_P16_FallibleWriteNoPanic verifica C16/P16 a nivel unitario:
// tras cerrar el writer, un write posterior devuelve error y NO panica.
func Test014001_P16_FallibleWriteNoPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Attempt(attemptFixture()); err != nil {
		t.Fatalf("Attempt (escritura sana): %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// El fd está cerrado → el write debe fallar con error no-nil, sin panic.
	if err := w.Attempt(attemptFixture()); err == nil {
		t.Fatalf("write sobre fd cerrado debe devolver error no-nil (C16/P16)")
	}
}
