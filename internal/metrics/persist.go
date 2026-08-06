// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Persistencia del registro de accounting (TECHDEBT #13).
//
// Todo el estado de Metrics vive en memoria; un restart perdía el
// histórico de sesiones/costos. SaveState/LoadState serializan ese
// estado a JSON (escritura atómica: temp + rename) y lo restauran en
// el arranque, habilitando reporting histórico sin cambiar el
// comportamiento en memoria (los incrementos siguen siendo atómicos).
//
// Convenciones:
//   - El archivo es un snapshot de acumulados; LoadState REEMPLAZA el
//     estado actual (no merge). Si no existe o está corrupto, LoadState
//     devuelve error y el caller decide (en main: log + arranque limpio).
//   - maxSessionsRetained NO se persiste: viene del config. Al restaurar,
//     si el mapa excede el tope vigente se evictan las más viejas.
//   - lastProvider no se persiste (es transitorio, observabilidad).
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// stateVersion identifica el formato del snapshot. Incrementar ante
// cambios de schema (LoadState rechaza versiones mayores).
const stateVersion = 1

// persistedState es la representación serializable de Metrics.
type persistedState struct {
	Version       int                       `json:"version"`
	SavedAt       int64                     `json:"saved_at"`
	Requests      int64                     `json:"requests"`
	Streams       int64                     `json:"streams"`
	Errors        int64                     `json:"errors"`
	Failovers     int64                     `json:"failovers"`
	CooldownHits  int64                     `json:"cooldown_hits"`
	Rejected      map[string]int64          `json:"rejected"`
	CacheHit      map[string]int64          `json:"cache_hit"`
	CacheMiss     map[string]int64          `json:"cache_miss"`
	Reasoning     map[string]int64          `json:"reasoning"`
	PromptTokens  map[string]int64          `json:"prompt_tokens"`
	CompletionTok map[string]int64          `json:"completion_tokens"`
	TotalTokens   map[string]int64          `json:"total_tokens"`
	Cost          map[string]float64        `json:"cost_usd"`
	Sessions      map[string]*SessionRecord `json:"sessions"`
}

// snapshot captura todo el estado bajo locks en un persistedState.
func (m *Metrics) snapshot() persistedState {
	s := persistedState{
		Version:      stateVersion,
		SavedAt:      time.Now().Unix(),
		Requests:     m.requests.Load(),
		Streams:      m.streams.Load(),
		Errors:       m.errors.Load(),
		Failovers:    m.failovers.Load(),
		CooldownHits: m.cooldownHits.Load(),
	}
	m.rejectedMu.Lock()
	s.Rejected = cloneIntMap(m.rejected)
	m.rejectedMu.Unlock()
	m.tokensMu.Lock()
	s.CacheHit = cloneIntMap(m.cacheHit)
	s.CacheMiss = cloneIntMap(m.cacheMiss)
	s.Reasoning = cloneIntMap(m.reasoning)
	m.tokensMu.Unlock()
	m.usageMu.Lock()
	s.PromptTokens = cloneIntMap(m.promptTokens)
	s.CompletionTok = cloneIntMap(m.completionTok)
	s.TotalTokens = cloneIntMap(m.totalTokens)
	m.usageMu.Unlock()
	m.costMu.Lock()
	s.Cost = make(map[string]float64, len(m.cost))
	for k, v := range m.cost {
		s.Cost[k] = v
	}
	m.costMu.Unlock()
	m.sessionsMu.Lock()
	s.Sessions = make(map[string]*SessionRecord, len(m.sessions))
	for k, r := range m.sessions {
		cp := *r
		s.Sessions[k] = &cp
	}
	m.sessionsMu.Unlock()
	return s
}

// restore carga un persistedState bajo locks, reemplazando el estado.
func (m *Metrics) restore(s persistedState) {
	m.requests.Store(s.Requests)
	m.streams.Store(s.Streams)
	m.errors.Store(s.Errors)
	m.failovers.Store(s.Failovers)
	m.cooldownHits.Store(s.CooldownHits)
	m.rejectedMu.Lock()
	m.rejected = cloneIntMap(s.Rejected)
	m.rejectedMu.Unlock()
	m.tokensMu.Lock()
	m.cacheHit = cloneIntMap(s.CacheHit)
	m.cacheMiss = cloneIntMap(s.CacheMiss)
	m.reasoning = cloneIntMap(s.Reasoning)
	m.tokensMu.Unlock()
	m.usageMu.Lock()
	m.promptTokens = cloneIntMap(s.PromptTokens)
	m.completionTok = cloneIntMap(s.CompletionTok)
	m.totalTokens = cloneIntMap(s.TotalTokens)
	m.usageMu.Unlock()
	m.costMu.Lock()
	m.cost = make(map[string]float64, len(s.Cost))
	for k, v := range s.Cost {
		m.cost[k] = v
	}
	m.costMu.Unlock()
	m.sessionsMu.Lock()
	m.sessions = make(map[string]*SessionRecord, len(s.Sessions))
	for k, r := range s.Sessions {
		cp := *r
		m.sessions[k] = &cp
	}
	// Si el snapshot trae más sesiones que el tope vigente (config),
	// evictar las más viejas hasta encajar.
	for len(m.sessions) > m.maxSessionsRetained {
		m.evictOldestLocked()
	}
	m.sessionsMu.Unlock()
}

// SaveState serializa el estado completo a path con escritura atómica
// (temp + rename en el mismo directorio). Crea el directorio padre si
// falta. Devuelve error si el marshal o el write fallan.
func (m *Metrics) SaveState(path string) error {
	data, err := json.Marshal(m.snapshot())
	if err != nil {
		return fmt.Errorf("save state: marshal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("save state: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("save state: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op si el rename ya ocurrió
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("save state: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("save state: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save state: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save state: rename: %w", err)
	}
	return nil
}

// LoadState lee path y restaura el estado en m. Rechaza snapshots con
// version > stateVersion (schema más nuevo que este binario). Devuelve
// error si el archivo no existe, está corrupto o es de versión mayor;
// en ese caso el estado de m queda intacto (no se toca nada hasta
// validar el JSON completo).
func (m *Metrics) LoadState(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	var s persistedState
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("load state: parse: %w", err)
	}
	if s.Version > stateVersion {
		return fmt.Errorf("load state: version %d > soportada %d (binario viejo?)", s.Version, stateVersion)
	}
	m.restore(s)
	return nil
}

// cloneIntMap copia un map[string]int64 (nil-safe).
func cloneIntMap(src map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
