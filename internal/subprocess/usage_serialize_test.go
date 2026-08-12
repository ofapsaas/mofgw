// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// PART B — tests RED de fabricación de usage y serialización por sesión
// (P6..P8, criterios C7..C9). Compilan contra el skeleton y fallan por
// ASSERCIÓN (Complete/Stream devuelven errors.New("not implemented"), que
// no es *ErrUpstream y no spawnea el CLI).
package subprocess_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// countInflight lee el STUB_LOCK_FILE (log de markers "start"/"end" escritos
// por cada invocación del stub CLI) y devuelve cuántos spawns hubo y el
// máximo de procesos en vuelo simultáneo para la sesión.
func countInflight(lockFile string) (starts, maxInflight int) {
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return 0, 0 // archivo ausente ⇒ nunca se spawneó
	}
	cur := 0
	for _, ln := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(ln) {
		case "start":
			cur++
			if cur > maxInflight {
				maxInflight = cur
			}
			starts++
		case "end":
			if cur > 0 {
				cur--
			}
		}
	}
	return starts, maxInflight
}

// waitForStubStart bloquea hasta que el stub CLI haya escrito al menos un
// marker "start" en el lock file (es decir, que el request bajo test ya
// spawneó y por lo tanto posee el lock de sesión). Falla si no ocurre en
// un plazo razonable.
func waitForStubStart(t *testing.T, lockFile string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if starts, _ := countInflight(lockFile); starts > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("el stub del request no arrancó (el lock file %s no recibió 'start'); el motor no spawneó", lockFile)
}

// T_usage_fabricated_complete (P6, C7): tras un Complete exitoso, el Usage
// de la respuesta es no degenerado: PromptTokens/CompletionTokens/TotalTokens
// >= 0, Total >= 0. El motor fabrica los conteos si el CLI no los emite.
func TestT_UsageFabricatedComplete(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	res, err := h.p.Complete(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Complete falló: %v", err) // RED: skeleton devuelve error
	}
	if res == nil || res.Response == nil {
		t.Fatalf("Complete no devolvió Response: %+v", res)
	}
	u := res.Response.Usage
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		t.Fatalf("usage degenerado (no fabricado): %+v", u)
	}
	if u.TotalTokens < u.PromptTokens || u.TotalTokens < u.CompletionTokens {
		t.Fatalf("usage inconsistente: total %d < prompt %d o completion %d",
			u.TotalTokens, u.PromptTokens, u.CompletionTokens)
	}
}

// T_usage_fabricated_stream (P7, C8): drenar el canal de un stream hasta el
// cierre produce un evento final con objeto usage OpenAI-compatible
// (compatible con stream.CaptureUsage), no degenerado.
func TestT_UsageFabricatedStream(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})

	ch, err := h.p.Stream(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Stream falló: %v", err) // RED: skeleton devuelve error
	}

	var last string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("evento de stream con error: %v", ev.Err)
		}
		if ev.Data != "" && ev.Data != "[DONE]" {
			last = ev.Data
		}
	}
	if last == "" {
		t.Fatalf("el stream no emitió ningún evento de datos")
	}
	var chunk struct {
		Usage *provider.Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(last), &chunk); err != nil {
		t.Fatalf("el último chunk del stream no es JSON parseable: %q: %v", last, err)
	}
	if chunk.Usage == nil {
		t.Fatalf("el último chunk del stream no trae objeto usage: %q", last) // RED
	}
	u := chunk.Usage
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		t.Fatalf("usage de stream degenerado: %+v", u)
	}
}

// T_serialize_same_session (P8, C9): dos Completes concurrentes de la MISMA
// sesión se serializan: máximo 1 proceso CLI en vuelo por sesión, y ambos
// requests llegan a spawnear (el segundo espera al primero).
func TestT_SerializeSameSession(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	// El stub duerme lo suficiente para que ambos requests se solapen si el
	// motor NO serializara. El motor debe heredar el env (os.Environ) para
	// que el stub vea STUB_LOCK_FILE/STUB_SLEEP_MS.
	t.Setenv("STUB_LOCK_FILE", h.lockFile)
	t.Setenv("STUB_SLEEP_MS", "600")

	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := h.p.Complete(clientCtx("client-a"), body)
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("request concurrente %d falló: %v", i, err) // RED
		}
	}

	starts, maxInflight := countInflight(h.lockFile)
	if starts != 2 {
		t.Fatalf("se esperaban 2 spawns de la sesión, hubo %d (¿se serializó? ¿se spawneó?)", starts)
	}
	if maxInflight > 1 {
		t.Fatalf("serialización rota: %d procesos en vuelo para la misma sesión (máx 1)", maxInflight)
	}
}
