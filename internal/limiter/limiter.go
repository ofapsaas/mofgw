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
	agentLimits  map[string]*Limiter
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
		agentLimits:  make(map[string]*Limiter),
	}
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

// AcquireAgent toma un slot del budget por (cliente, agente)
// (fast-fail). Si agentID está vacío, usa el budget del cliente como
// agente único (misma key, sin separación).
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
	key := agentKey(clientID, agentID)
	l, lExists := k.agentLimits[key]
	if !lExists {
		l = New(budget.MaxPerAgent)
		k.agentLimits[key] = l
	}
	k.mu.Unlock()
	if !l.TryAcquire() {
		return nil, false
	}
	return l.Release, true
}
