// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Registro de composición del contexto por sesión/cliente (009-001 P2/P4):
// un ContextRecord por clave "client|session" (y "client|" para el agregado
// de cliente) acumula metadata estructural de los requests — SOLO roles,
// part types, bytes, est/actual tokens, conteos y timestamps. Nunca
// contenido de prompts/respuestas (P7/I1). Los records por sesión se
// evictan FIFO por last_request_at con el MISMO tope maxSessionsRetained
// (008-003 P4); el record de cliente NUNCA se evicta.
package metrics

import (
	"sort"

	"github.com/ofapsaas/mofgw/internal/composition"
)

// ContextEntry es el detalle de UN request en el history de un record
// (P4): la vista por request, sin contenido (P7). PromptTokensActual nil
// = la fase 2 (usage post-response) no corrió para este request.
type ContextEntry struct {
	RequestID             string
	Model                 string
	Roles                 []string
	PartTypes             []composition.PartType
	NumTools              int
	TotalBytes            int
	PromptTokensEstimated int64
	PromptTokensActual    *int64
	RequestAt             int64
}

// RoleAgg es el agregado de un rol dentro de un record (P2/P3).
type RoleAgg struct {
	Role      string
	Messages  int64
	EstTokens int64
	Pct       float64
}

// PartAgg es el agregado de un part type dentro de un record (P2/P3).
type PartAgg struct {
	Type      string
	Count     int64
	Bytes     int64
	EstTokens int64
}

// ContextRecord es el acumulado de composición de una clave
// client|session (P2). Solo metadata estructural (P7).
type ContextRecord struct {
	ClientID              string
	SessionID             string
	Requests              int64
	NumToolsTotal         int64
	PromptTokensEstimated int64
	PromptTokensActual    int64
	FirstRequestAt        int64
	LastRequestAt         int64
	Roles                 map[string]*RoleAgg
	PartTypes             map[string]*PartAgg
	History               []ContextEntry
}

// ContextSummary es la vista agregada expuesta de un record (P3).
type ContextSummary struct {
	Roles                 []RoleAgg
	PartTypes             []PartAgg
	NumToolsTotal         int64
	PromptTokensEstimated int64
	PromptTokensActual    int64
	FirstRequestAt        int64
	LastRequestAt         int64
}

// ContextSnapshot es la vista expuesta de un record (P3): session "" =
// record de cliente (scope "client"). Sin data → snapshot cero/vacío
// (nunca nil), con Client/Scope seteados.
type ContextSnapshot struct {
	Client   string
	Scope    string
	Session  string
	Requests int64
	Summary  ContextSummary
	Latest   *ContextEntry
	History  []ContextEntry
}

// RecordContext registra la composición de un request (P4 fase 1):
// SIEMPRE actualiza el record de cliente (client|) y, si hay sesión,
// además el record client|session. El entry del history se crea sin
// prompt_tokens_actual (la fase 2 lo setea por request_id).
func (m *Metrics) RecordContext(clientID, sessionID, requestID, model string, res *composition.WalkResult, nowUnix int64) {
	if res == nil {
		return
	}
	est := int64(0)
	for _, p := range res.PartTypes {
		est += int64(p.EstTokens)
	}
	entry := ContextEntry{
		RequestID:             requestID,
		Model:                 model,
		Roles:                 res.Roles,
		PartTypes:             res.PartTypes,
		NumTools:              res.NumTools,
		TotalBytes:            res.TotalBytes,
		PromptTokensEstimated: est,
		RequestAt:             nowUnix,
	}
	m.contextsMu.Lock()
	m.recordContextLocked(clientID, "", entry, res, est, nowUnix)
	if sessionID != "" {
		m.recordContextLocked(clientID, sessionID, entry, res, est, nowUnix)
	}
	m.contextsMu.Unlock()
}

// recordContextLocked acumula un request en el record de la clave
// client|session (requiere contextsMu held).
func (m *Metrics) recordContextLocked(clientID, sessionID string, entry ContextEntry, res *composition.WalkResult, est int64, nowUnix int64) {
	key := clientID + "|" + sessionID
	rec, ok := m.contexts[key]
	if !ok {
		rec = &ContextRecord{
			ClientID:       clientID,
			SessionID:      sessionID,
			Roles:          make(map[string]*RoleAgg),
			PartTypes:      make(map[string]*PartAgg),
			FirstRequestAt: nowUnix,
			LastRequestAt:  nowUnix,
		}
		m.contexts[key] = rec
		// Evicción FIFO SOLO de records por sesión (el de cliente nunca).
		if sessionID != "" && m.sessionContextCountLocked() > m.maxSessionsRetained {
			m.evictOldestContextLocked()
		}
	}
	rec.Requests++
	rec.NumToolsTotal += int64(res.NumTools)
	rec.PromptTokensEstimated += est
	rec.LastRequestAt = nowUnix
	for _, role := range res.Roles {
		agg := rec.Roles[role]
		if agg == nil {
			agg = &RoleAgg{Role: role}
			rec.Roles[role] = agg
		}
		agg.Messages++
	}
	for _, p := range res.PartTypes {
		agg := rec.Roles[p.Role]
		if agg == nil {
			agg = &RoleAgg{Role: p.Role}
			rec.Roles[p.Role] = agg
		}
		agg.EstTokens += int64(p.EstTokens)
		pa := rec.PartTypes[p.Type]
		if pa == nil {
			pa = &PartAgg{Type: p.Type}
			rec.PartTypes[p.Type] = pa
		}
		pa.Count++
		pa.Bytes += int64(p.Bytes)
		pa.EstTokens += int64(p.EstTokens)
	}
	// ring buffer: más nuevo primero, acotado a contextHistoryPerSession
	// (0 = sin history, P3).
	rec.History = append([]ContextEntry{entry}, rec.History...)
	if len(rec.History) > m.contextHistoryPerSession {
		rec.History = rec.History[:m.contextHistoryPerSession]
	}
}

// UpdateContextUsage setea el prompt_tokens_actual de la entry matcheada
// por request_id y acumula el agregado del record (P4 fase 2). Matcheo
// exacto por request_id (evita races entre handlers concurrentes de la
// misma sesión). Request desconocido → no-op. Actualiza el record de
// sesión (si hay) y el de cliente.
func (m *Metrics) UpdateContextUsage(clientID, sessionID, requestID string, promptTokens int64) {
	m.contextsMu.Lock()
	if sessionID != "" {
		m.updateContextUsageLocked(clientID, sessionID, requestID, promptTokens)
	}
	m.updateContextUsageLocked(clientID, "", requestID, promptTokens)
	m.contextsMu.Unlock()
}

// updateContextUsageLocked setea la entry por request_id en el record de
// la clave client|session (requiere contextsMu held).
func (m *Metrics) updateContextUsageLocked(clientID, sessionID, requestID string, promptTokens int64) {
	rec, ok := m.contexts[clientID+"|"+sessionID]
	if !ok {
		return
	}
	for i := range rec.History {
		if rec.History[i].RequestID == requestID {
			v := promptTokens
			rec.History[i].PromptTokensActual = &v
			rec.PromptTokensActual += promptTokens
			return
		}
	}
}

// ContextSnapshot devuelve la vista de un record (P3): session "" = el
// record de cliente (agregado por client_id). ok=false si no existe;
// aún así el snapshot es cero/vacío con Client/Scope seteados (nunca
// nil, consistente con UsageSnapshot).
func (m *Metrics) ContextSnapshot(clientID, sessionID string) (ContextSnapshot, bool) {
	key := clientID + "|" + sessionID
	m.contextsMu.Lock()
	rec, ok := m.contexts[key]
	var snap ContextSnapshot
	if ok {
		snap = buildContextSnapshot(clientID, sessionID, rec)
	} else {
		scope := "client"
		if sessionID != "" {
			scope = "session"
		}
		snap = ContextSnapshot{Client: clientID, Scope: scope, Session: sessionID}
	}
	m.contextsMu.Unlock()
	return snap, ok
}

// buildContextSnapshot arma la vista expuesta de un record (P3): summary
// con roles (pct = est_tokens del role / Σ est_tokens de roles, 0 si el
// total es 0), part_types agregados, latest = entry más nueva, history
// más nuevo primero.
func buildContextSnapshot(clientID, sessionID string, rec *ContextRecord) ContextSnapshot {
	scope := "client"
	if sessionID != "" {
		scope = "session"
	}
	history := make([]ContextEntry, len(rec.History))
	copy(history, rec.History)
	var latest *ContextEntry
	if len(history) > 0 {
		cp := history[0]
		latest = &cp
	}
	roles := make([]RoleAgg, 0, len(rec.Roles))
	var totalEst int64
	for _, a := range rec.Roles {
		totalEst += a.EstTokens
	}
	for _, a := range rec.Roles {
		r := *a
		if totalEst > 0 {
			r.Pct = float64(a.EstTokens) / float64(totalEst)
		}
		roles = append(roles, r)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Role < roles[j].Role })
	parts := make([]PartAgg, 0, len(rec.PartTypes))
	for _, a := range rec.PartTypes {
		parts = append(parts, *a)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Type < parts[j].Type })
	return ContextSnapshot{
		Client:   clientID,
		Scope:    scope,
		Session:  sessionID,
		Requests: rec.Requests,
		Summary: ContextSummary{
			Roles:                 roles,
			PartTypes:             parts,
			NumToolsTotal:         rec.NumToolsTotal,
			PromptTokensEstimated: rec.PromptTokensEstimated,
			PromptTokensActual:    rec.PromptTokensActual,
			FirstRequestAt:        rec.FirstRequestAt,
			LastRequestAt:         rec.LastRequestAt,
		},
		Latest:  latest,
		History: history,
	}
}

// SetContextHistoryPerSession ajusta el tope del ring buffer de history
// por record (P2/P6). Default 50 en New. 0 = sin history (solo agregados).
func (m *Metrics) SetContextHistoryPerSession(n int) {
	m.contextsMu.Lock()
	m.contextHistoryPerSession = n
	m.contextsMu.Unlock()
}

// sessionContextCountLocked cuenta los records por sesión (excluye el
// record de cliente client|) — requiere contextsMu held.
func (m *Metrics) sessionContextCountLocked() int {
	n := 0
	for _, r := range m.contexts {
		if r.SessionID != "" {
			n++
		}
	}
	return n
}

// evictOldestContextLocked descarta el record por sesión con
// last_request_at más viejo (FIFO, P2). El record de cliente nunca se
// evicta. Requiere contextsMu held.
func (m *Metrics) evictOldestContextLocked() {
	var oldestKey string
	var oldestAt int64 = 1<<63 - 1
	for k, r := range m.contexts {
		if r.SessionID == "" {
			continue
		}
		if r.LastRequestAt < oldestAt {
			oldestAt = r.LastRequestAt
			oldestKey = k
		}
	}
	if oldestKey != "" {
		delete(m.contexts, oldestKey)
	}
}
