// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPersistRoundTrip: un Metrics con datos (contadores atómicos,
// rejected, tokens, usage, cost, sesiones) → SaveState → Metrics nuevo
// → LoadState → mismo estado en /metrics y en las vistas.
func TestPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	m := New()
	m.IncRequests()
	m.IncRequests()
	m.IncStreams()
	m.IncErrors()
	m.IncFailovers()
	m.IncCooldownHit()
	m.IncRejected("zot", "client_limit")
	m.IncRejected("zot", "global_timeout")
	m.IncCacheTokens("go-1", "deepseek-v4-flash", 100, 20, 5)
	m.IncUsage("zot", "go-1", "deepseek-v4-flash", 120, 30, 150)
	m.IncCost("zot", "go-1", "deepseek-v4-flash", 0.25)
	m.RecordSession("zot", "sess-1", 50, 10, 60, 0.1, 1000)
	m.RecordSession("zot", "sess-1", 10, 5, 15, 0.02, 1005)
	m.RecordSession("zot", "sess-2", 7, 3, 10, 0.01, 1010)
	m.SetLastProvider("go-1")

	if err := m.SaveState(path); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file no existe: %v", err)
	}

	m2 := New()
	if err := m2.LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Contadores atómicos.
	if got := m2.requests.Load(); got != 2 {
		t.Errorf("requests: got %d, want 2", got)
	}
	if got := m2.streams.Load(); got != 1 {
		t.Errorf("streams: got %d, want 1", got)
	}
	if got := m2.errors.Load(); got != 1 {
		t.Errorf("errors: got %d, want 1", got)
	}
	if got := m2.failovers.Load(); got != 1 {
		t.Errorf("failovers: got %d, want 1", got)
	}
	if got := m2.cooldownHits.Load(); got != 1 {
		t.Errorf("cooldownHits: got %d, want 1", got)
	}

	// rejected.
	if got := m2.rejected["zot|client_limit"]; got != 1 {
		t.Errorf("rejected client_limit: got %d, want 1", got)
	}
	if got := m2.rejected["zot|global_timeout"]; got != 1 {
		t.Errorf("rejected global_timeout: got %d, want 1", got)
	}

	// tokens de cache.
	if got := m2.cacheHit["go-1|deepseek-v4-flash"]; got != 100 {
		t.Errorf("cacheHit: got %d, want 100", got)
	}
	if got := m2.cacheMiss["go-1|deepseek-v4-flash"]; got != 20 {
		t.Errorf("cacheMiss: got %d, want 20", got)
	}
	if got := m2.reasoning["go-1|deepseek-v4-flash"]; got != 5 {
		t.Errorf("reasoning: got %d, want 5", got)
	}

	// usage y cost via snapshot del cliente.
	snap := m2.SnapshotClient("zot")
	if snap.PromptTokens != 120 || snap.CompletionTokens != 30 || snap.TotalTokens != 150 {
		t.Errorf("usage: prompt=%d completion=%d total=%d, want 120/30/150", snap.PromptTokens, snap.CompletionTokens, snap.TotalTokens)
	}
	if snap.CostUSD != 0.25 {
		t.Errorf("cost: got %f, want 0.25", snap.CostUSD)
	}
	if len(snap.ByModel) != 1 || snap.ByModel[0].Model != "deepseek-v4-flash" {
		t.Errorf("byModel: %+v, want [deepseek-v4-flash]", snap.ByModel)
	}

	// sesiones acumuladas.
	s1, ok := m2.SessionSnapshot("zot", "sess-1")
	if !ok {
		t.Fatal("sess-1 no existe tras load")
	}
	if s1.Requests != 2 || s1.PromptTokens != 60 || s1.TotalTokens != 75 || s1.FirstRequestAt != 1000 || s1.LastRequestAt != 1005 {
		t.Errorf("sess-1: %+v", s1)
	}
	list := m2.SessionsList("zot")
	if len(list) != 2 {
		t.Fatalf("SessionsList len: got %d, want 2", len(list))
	}
	// orden: last_request_at desc → sess-2 (1010) primero.
	if list[0].SessionID != "sess-2" || list[1].SessionID != "sess-1" {
		t.Errorf("orden sesiones: %+v", list)
	}

	// Render incluye los acumulados (total cost = 0.25).
	rendered := m2.Render()
	if !strings.Contains(rendered, "mofgw_cost_usd_total 0.250000") {
		t.Errorf("render no incluye costo restaurado:\n%s", rendered)
	}
}

// TestPersistEmpty: guardar/restaurar un Metrics vacío no falla y el
// render sigue siendo válido (totales 0).
func TestPersistEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	m := New()
	if err := m.SaveState(path); err != nil {
		t.Fatalf("SaveState vacío: %v", err)
	}
	m2 := New()
	if err := m2.LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := m2.requests.Load(); got != 0 {
		t.Errorf("requests: got %d, want 0", got)
	}
	if !strings.Contains(m2.Render(), "mofgw_cost_usd_total 0.000000") {
		t.Error("render vacío sin costo 0")
	}
}

// TestPersistCorrupt: un archivo inválido devuelve error y NO toca el
// estado del Metrics (arranque con estado limpio, sin pánico).
func TestPersistCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.IncRequests()
	if err := m.LoadState(path); err == nil {
		t.Fatal("LoadState corrupto: esperaba error")
	}
	if got := m.requests.Load(); got != 1 {
		t.Errorf("estado tocado tras load corrupto: requests=%d, want 1", got)
	}
}

// TestPersistMissing: archivo inexistente → error (caller decide), no pánico.
func TestPersistMissing(t *testing.T) {
	m := New()
	if err := m.LoadState(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadState de archivo inexistente: esperaba error")
	}
}

// TestPersistVersionReject: snapshot con versión mayor → error y estado intacto.
func TestPersistVersionReject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	m := New()
	m.IncRequests()
	if err := m.SaveState(path); err != nil {
		t.Fatal(err)
	}
	// Subir la versión en el archivo.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Replace(string(data), `"version":1`, `"version":99`, 1)
	if err := os.WriteFile(path, []byte(up), 0o600); err != nil {
		t.Fatal(err)
	}
	m2 := New()
	m2.IncStreams()
	if err := m2.LoadState(path); err == nil {
		t.Fatal("LoadState con version mayor: esperaba error")
	}
	if got := m2.streams.Load(); got != 1 {
		t.Errorf("estado tocado tras reject: streams=%d, want 1", got)
	}
}

// TestPersistSessionEviction: si el tope vigente (config) es menor que
// las sesiones del snapshot, se evictan las más viejas al restaurar.
func TestPersistSessionEviction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	m := New()
	for i := 0; i < 5; i++ {
		m.RecordSession("zot", string(rune('a'+i)), 1, 1, 2, 0, int64(1000+i))
	}
	if err := m.SaveState(path); err != nil {
		t.Fatal(err)
	}
	m2 := New()
	m2.SetMaxSessionsRetained(3)
	if err := m2.LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	list := m2.SessionsList("zot")
	if len(list) != 3 {
		t.Fatalf("sesiones tras eviction: got %d, want 3", len(list))
	}
	// Las 3 más recientes (c, d, e).
	for _, s := range list {
		if s.SessionID == "a" || s.SessionID == "b" {
			t.Errorf("sesión vieja no evictada: %s", s.SessionID)
		}
	}
}

// TestPersistAtomicWrite: SaveState escribe atómicamente (no quedan
// archivos temporales y el contenido es JSON válido).
func TestPersistAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	m := New()
	m.IncRequests()
	if err := m.SaveState(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file residual: %s", e.Name())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s persistedState
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("state no es JSON válido: %v", err)
	}
	if s.Version != stateVersion {
		t.Errorf("version: got %d, want %d", s.Version, stateVersion)
	}
	if s.Requests != 1 {
		t.Errorf("requests en state: got %d, want 1", s.Requests)
	}
}
