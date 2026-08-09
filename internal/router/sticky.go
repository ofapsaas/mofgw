// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package router

import (
	"sync"
	"time"
)

// defaultStickyMaxEntries es el tope por defecto del AffinityStore
// (009-002 P3): coincide con el default de server.max_sessions_retained
// (008-003) — el operador gobierna retención de sesiones y afinidad con
// el MISMO knob (decisión D4 del spec).
const defaultStickyMaxEntries = 100

// affinityEntry es el valor del mapa del AffinityStore: el provider
// ganador de la clave sticky y el momento del último uso (para la
// evicción LRU).
type affinityEntry struct {
	ProviderID string
	LastUsed   time.Time
}

// AffinityStore guarda la afinidad sticky (009-002 P3): mapa
// stickyKey → provider ganador del último request exitoso. Efímero por
// diseño (restart → frío; patrón CooldownStore / 008-003 I4). Cap =
// tope de entradas (0 = default 100). Evicción LRU por LastUsed: Set y
// Get refrescan LastUsed (un cliente activo no se evicta — C12).
// Seguro para uso concurrente (mutex).
type AffinityStore struct {
	mu  sync.Mutex
	m   map[string]affinityEntry
	now func() time.Time // inyectable (fake clock en tests)
	cap int
}

// NewAffinityStore construye el store; now es inyectable (fake clock en
// tests). cap 0 = default (100).
func NewAffinityStore(now func() time.Time, cap int) *AffinityStore {
	if now == nil {
		now = time.Now
	}
	if cap <= 0 {
		cap = defaultStickyMaxEntries
	}
	return &AffinityStore{m: map[string]affinityEntry{}, now: now, cap: cap}
}

// Set registra (upsert) el provider ganador de una clave y refresca su
// LastUsed. Si el mapa excede el cap, evicta la entrada con LastUsed más
// viejo (LRU). Nunca excede cap.
func (s *AffinityStore) Set(key, providerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = affinityEntry{ProviderID: providerID, LastUsed: s.now()}
	if len(s.m) > s.cap {
		s.evictLRU()
	}
}

// Get devuelve el provider de la clave y refresca su LastUsed (un uso
// cuenta como actividad para el LRU). ("", false) si la clave no existe.
func (s *AffinityStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return "", false
	}
	e.LastUsed = s.now()
	s.m[key] = e
	return e.ProviderID, true
}

// Len reporta cuántas claves hay (para tests/observabilidad).
func (s *AffinityStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// evictLRU elimina la entrada con LastUsed más viejo. Requiere el lock.
func (s *AffinityStore) evictLRU() {
	oldestKey := ""
	var oldest time.Time
	first := true
	for k, e := range s.m {
		if first || e.LastUsed.Before(oldest) {
			oldestKey, oldest, first = k, e.LastUsed, false
		}
	}
	delete(s.m, oldestKey)
}
