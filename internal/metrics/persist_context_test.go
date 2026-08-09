// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-001-context-composition — P5 (C12).
// Verifican el bump de stateVersion 1→2 con backward compat:
//   - nuevo binario lee snapshot v1 (sin `contexts`) sin error, contexts
//     vacío (arranque limpio).
//   - round-trip save/load preserva `contexts` con su history completa.
//   - el snapshot guardado tiene version:2.
//   - snapshot con version > stateVersion → LoadState error (check
//     existente persist.go:183, "binario viejo").
package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// test_postcondition_5_PersistContextV2_BackwardCompatV1 (C12): snapshot
// v1 (sin `contexts`) → LoadState OK y contexts vacíos.
func TestPersistContextV2_BackwardCompatV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	v1 := `{"version":1,"saved_at":1000,"requests":5,"streams":0,"errors":0,"failovers":0,"cooldown_hits":0,"rejected":{},"cache_hit":{},"cache_miss":{},"reasoning":{},"prompt_tokens":{},"completion_tokens":{},"total_tokens":{},"cost_usd":{},"sessions":{}}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.LoadState(path); err != nil {
		t.Fatalf("LoadState v1: %v (el nuevo binario DEBE leer v1 — C12)", err)
	}
	if got := m.requests.Load(); got != 5 {
		t.Fatalf("requests tras load v1 = %d, want 5 (resto del estado intacto)", got)
	}
	m.contextsMu.Lock()
	if len(m.contexts) != 0 {
		m.contextsMu.Unlock()
		t.Fatalf("contexts tras load v1 = %d, want 0 (contexts ausente = vacío, arranque limpio)", len(m.contexts))
	}
	m.contextsMu.Unlock()
}

// test_postcondition_5_PersistContextV2_RoundTrip (C12): save con
// contexts → load → contexts preservados con history completa.
func TestPersistContextV2_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	m := New()
	m.SetContextHistoryPerSession(50)
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.RecordContext("client1", "s1", "req2", "m", testContextWalkResult2(), 2000)
	m.RecordContext("client1", "", "req3", "m", testContextWalkResult(), 3000)
	if err := m.SaveState(path); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m2 := New()
	if err := m2.LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Record de sesión preservado con su history completa.
	sess, ok := m2.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("sesión s1 no restaurada (C12)")
	}
	if sess.Requests != 2 {
		t.Fatalf("s1 Requests = %d, want 2", sess.Requests)
	}
	if len(sess.History) != 2 {
		t.Fatalf("s1 History len = %d, want 2 (history completa)", len(sess.History))
	}
	if sess.History[0].RequestID != "req2" || sess.History[0].Model != "m" ||
		sess.History[0].PromptTokensEstimated != 95 || sess.History[0].RequestAt != 2000 {
		t.Fatalf("History[0] = %+v, want req2/m/95/2000", sess.History[0])
	}
	if sess.History[0].PromptTokensActual != nil {
		t.Fatalf("History[0].PromptTokensActual = %v, want nil (fase 2 no corrió)", *sess.History[0].PromptTokensActual)
	}

	// Record de cliente preservado (acumula todo, incl. request sin sesión).
	client, okC := m2.ContextSnapshot("client1", "")
	if !okC {
		t.Fatal("record de cliente no restaurado (C12)")
	}
	if client.Requests != 3 {
		t.Fatalf("record de cliente Requests = %d, want 3", client.Requests)
	}
}

// test_postcondition_5_PersistContextV2_VersionBump (C12): el snapshot
// guardado tiene version:2 (stateVersion bump) e incluye `contexts`.
func TestPersistContextV2_VersionBump(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	m := New()
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	if err := m.SaveState(path); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":2`) {
		t.Fatalf("snapshot sin version:2 (stateVersion debe bumpear a 2 — C12):\n%s", data)
	}
	if strings.Contains(string(data), `"version":1`) {
		t.Fatalf("snapshot aún con version:1:\n%s", data)
	}
	if !strings.Contains(string(data), `"contexts"`) {
		t.Fatalf("snapshot sin key contexts (contexts debe persistirse):\n%s", data)
	}
}

// test_postcondition_5_PersistContextV2_RejectFutureVersion (C12):
// snapshot con version:3 → LoadState error (check existente, binario
// viejo no puede leer v2+).
func TestPersistContextV2_RejectFutureVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.LoadState(path); err == nil {
		t.Fatal("LoadState con version 3: esperaba error (check persist.go:183)")
	}
}
