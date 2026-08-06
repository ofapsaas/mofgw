// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package limiter implementa la feature 004-001-concurrencia + 004-002-aislamiento
// (EPIC-004 escala): límites de concurrencia para alta carga multi-agente.
//
// Dos piezas:
//   - Limiter: semáforo global de requests en vuelo (bounded channel).
//     Con n==0 no limita (passthrough). Acquire espera hasta que el
//     context expira o hay slot.
//   - Keyed: semáforos por cliente y por (cliente, agente) para
//     aislamiento. A diferencia del global, los límites por cliente son
//     fast-fail: si el budget del cliente está agotado, el request recibe
//     429 inmediato SIN esperar — un cliente que excede su propio budget
//     no debe hacer esperar a los demás ni acumular goroutines.
//
// El header X-Agent-Id es de SOLO lectura para accounting: nunca se
// reenvía upstream (el proxy no lo copia).
package limiter

import (
	"context"
	"sync"
	"time"
)

// Limiter es un semáforo de slots. n==0 = ilimitado (no bloquea nunca).
// Seguro para uso concurrente.
type Limiter struct {
	slots chan struct{}
}

// New construye un Limiter con n slots. n <= 0 = ilimitado.
func New(n int) *Limiter {
	if n <= 0 {
		return &Limiter{}
	}
	return &Limiter{slots: make(chan struct{}, n)}
}

// Acquire toma un slot. Devuelve false si el context expira antes de
// conseguir slot. Con n==0 devuelve true inmediatamente (ilimitado).
// El caller debe llamar Release() solo si Acquire devolvió true.
func (l *Limiter) Acquire(ctx context.Context) bool {
	if l.slots == nil {
		return true
	}
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// TryAcquire toma un slot sin esperar. Devuelve false si no hay slot
// disponible (fast-fail). Con n==0 devuelve true inmediatamente.
func (l *Limiter) TryAcquire() bool {
	if l.slots == nil {
		return true
	}
	select {
	case l.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release libera un slot adquirido. No-op si n==0 o si no había slots.
func (l *Limiter) Release() {
	if l.slots == nil {
		return
	}
	<-l.slots
}

// Keyed agrupa límites por cliente y por (cliente, agente) para
// 004-002-aislamiento. Los límites se crean lazy bajo mutex; una vez
// creados son inmutables (solo se adquieren/liberan slots).
// Todos los límites son fast-fail (TryAcquire).
type Keyed struct {
	mu sync.Mutex

	// budgets: clientID -> límites de ESE cliente. Un cliente sin entry
	// (no debería pasar: se construye desde config) = ilimitado.
	budgets map[string]ClientBudget

	clientLimits map[string]*Limiter
	// agentLimits: (cliente, agente) -> limiter + lastUsed para TTL
	// (SEC-001 P2: purga de entries inactivas acota cardinalidad).
	agentLimits map[string]*agentEntry

	// agentTTL: inactividad máxima de una entry de agente antes de
	// purgarse (SEC-001 P2). Default 10 min.
	agentTTL time.Duration

	// lastPurge: cuándo se hizo la última purga (SEC-001 P2). La purga
	// es lazy: se ejecuta en AcquireAgent con frecuencia acotada
	// (intervalo = agentTTL/2) para no barrer el mapa en cada request.
	lastPurge time.Time
}

// agentEntry es una entry del mapa de agentes: el limiter + cuándo se
// usó por última vez (para TTL de purga, SEC-001 P2).
type agentEntry struct {
	limiter  *Limiter
	lastUsed time.Time
}

// ClientBudget son los límites de un cliente (004-002). 0 = ilimitado.
type ClientBudget struct {
	MaxConcurrent int // requests simultáneos del cliente
	MaxPerAgent   int // requests simultáneos por (cliente, X-Agent-Id)
}

// NewKeyed construye Keyed con budgets por cliente. clientBudgets con
// clientID ausente = ilimitado.
func NewKeyed(clientBudgets map[string]ClientBudget) *Keyed {
	return &Keyed{
		budgets:      clientBudgets,
		clientLimits: make(map[string]*Limiter),
		agentLimits:  make(map[string]*agentEntry),
		agentTTL:     10 * time.Minute,
	}
}

// SetAgentTTL configura el TTL de inactividad de las entries de agentes
// (SEC-001 P2). Las entries inactivas por más del TTL se purgan en
// PurgeStaleAgents. Default 10 min.
func (k *Keyed) SetAgentTTL(ttl time.Duration) {
	k.mu.Lock()
	k.agentTTL = ttl
	k.mu.Unlock()
}

// PurgeStaleAgents elimina las entries de agentes cuyo lastUsed es
// anterior a now - agentTTL (SEC-001 P2). Acota la cardinalidad de
// agentLimits ante X-Agent-Id arbitrarios (memory leak lento). El
// caller decide cuándo llamarlo (el proxy puede llamarlo periódicamente
// o en Acquire).
func (k *Keyed) PurgeStaleAgents(now time.Time) {
	k.mu.Lock()
	for key, entry := range k.agentLimits {
		if now.Sub(entry.lastUsed) > k.agentTTL {
			delete(k.agentLimits, key)
		}
	}
	k.mu.Unlock()
}

// SetBudget define/actualiza el budget de un cliente (útil en tests).
func (k *Keyed) SetBudget(clientID string, maxConcurrent, maxPerAgent int) {
	k.mu.Lock()
	k.budgets[clientID] = ClientBudget{MaxConcurrent: maxConcurrent, MaxPerAgent: maxPerAgent}
	k.mu.Unlock()
}

// agentKey compone clientID + agentID con separador que no puede
// colisionar con IDs de cliente (los IDs no pueden contener '\x00' en
// config real — el separador es un byte nulo, imposible en strings de
// config YAML normales).
func agentKey(clientID, agentID string) string {
	return clientID + "\x00" + agentID
}

// AcquireClient toma un slot del budget del cliente (fast-fail).
// Devuelve release o nil si el budget está agotado o ilimitado.
func (k *Keyed) AcquireClient(clientID string) (release func(), ok bool) {
	k.mu.Lock()
	budget, exists := k.budgets[clientID]
	if !exists || budget.MaxConcurrent <= 0 {
		k.mu.Unlock()
		return nil, true
	}
	l, lExists := k.clientLimits[clientID]
	if !lExists {
		l = New(budget.MaxConcurrent)
		k.clientLimits[clientID] = l
	}
	k.mu.Unlock()
	if !l.TryAcquire() {
		return nil, false
	}
	return l.Release, true
}

// maxAgentIDLen acota la longitud del X-Agent-Id usado como key del
// limiter (SEC-001 P2). IDs más largos se truncan — los agentes con el
// mismo prefijo de 64 chars comparten limiter (cardinalidad acotada).
const maxAgentIDLen = 64

// AcquireAgent toma un slot del budget por (cliente, agente)
// (fast-fail). Si agentID está vacío, usa el budget del cliente como
// agente único (misma key, sin separación). El agentID se trunca a
// maxAgentIDLen (SEC-001 P2) y la entry se marca como usada (TTL).
func (k *Keyed) AcquireAgent(clientID, agentID string) (release func(), ok bool) {
	k.mu.Lock()
	budget, exists := k.budgets[clientID]
	if !exists || budget.MaxPerAgent <= 0 {
		k.mu.Unlock()
		return nil, true
	}
	if agentID == "" {
		agentID = clientID
	}
	if len(agentID) > maxAgentIDLen {
		agentID = agentID[:maxAgentIDLen]
	}
	// Purga lazy (SEC-001 P2): con frecuencia acotada (agentTTL/2) para
	// acotar la cardinalidad de agentLimits sin barrer el mapa en cada
	// request. Elimina las entries inactivas por más del TTL.
	if time.Since(k.lastPurge) > k.agentTTL/2 {
		now := time.Now()
		for key, entry := range k.agentLimits {
			if now.Sub(entry.lastUsed) > k.agentTTL {
				delete(k.agentLimits, key)
			}
		}
		k.lastPurge = time.Now()
	}
	key := agentKey(clientID, agentID)
	entry, lExists := k.agentLimits[key]
	if !lExists {
		entry = &agentEntry{limiter: New(budget.MaxPerAgent)}
		k.agentLimits[key] = entry
	}
	entry.lastUsed = time.Now()
	l := entry.limiter
	k.mu.Unlock()
	if !l.TryAcquire() {
		return nil, false
	}
	return l.Release, true
}
