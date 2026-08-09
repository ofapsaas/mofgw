// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-001-context-composition — P2/P3/P4
// (C5-C11, C14). Verifican el registro de composición de contexto en
// metrics: acumulación por session y por cliente, aislamiento, ring
// buffer de history, evicción FIFO y las dos fases de
// prompt_tokens_actual.
//
// API definida por el test-writer contra el contrato P2/P3/P4 del spec:
//
//	func (m *Metrics) RecordContext(clientID, sessionID, requestID, model string, res *composition.WalkResult, nowUnix int64)
//	func (m *Metrics) UpdateContextUsage(clientID, sessionID, requestID string, promptTokens int64)
//	func (m *Metrics) ContextSnapshot(clientID, sessionID string) (ContextSnapshot, bool) // session "" = record de cliente
//	func (m *Metrics) SetContextHistoryPerSession(n int)
//
// Keying (P2): `client|session` cuando hay sesión; `client|` para el
// record de cliente. TODO request actualiza SIEMPRE el record de cliente
// y, si hay X-Session-Id, además el record de sesión. Los records por
// sesión se evictan FIFO por last_request_at con el MISMO tope
// max_sessions_retained; el record de cliente NUNCA se evicta.
package metrics

import (
	"math"
	"testing"

	"github.com/ofapsaas/mofgw/internal/composition"
)

// testContextWalkResult es un WalkResult de un request con system+user.
func testContextWalkResult() *composition.WalkResult {
	return &composition.WalkResult{
		Roles: []string{"system", "user"},
		PartTypes: []composition.PartType{
			{Role: "system", Type: "text", Bytes: 100, EstTokens: 25},
			{Role: "user", Type: "text", Bytes: 200, EstTokens: 50},
		},
		NumTools:   1,
		TotalBytes: 400,
	}
}

// testContextWalkResult2 es un WalkResult de un request con user+assistant.
func testContextWalkResult2() *composition.WalkResult {
	return &composition.WalkResult{
		Roles: []string{"user", "assistant"},
		PartTypes: []composition.PartType{
			{Role: "user", Type: "text", Bytes: 300, EstTokens: 75},
			{Role: "assistant", Type: "tool_use", Bytes: 80, EstTokens: 20},
		},
		NumTools:   1,
		TotalBytes: 500,
	}
}

func findRoleAgg(aggs []RoleAgg, role string) (RoleAgg, bool) {
	for _, a := range aggs {
		if a.Role == role {
			return a, true
		}
	}
	return RoleAgg{}, false
}

func findPartAgg(aggs []PartAgg, typ string) (PartAgg, bool) {
	for _, a := range aggs {
		if a.Type == typ {
			return a, true
		}
	}
	return PartAgg{}, false
}

func approxEqual(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// test_postcondition_2_ContextRecord_BasicAccumulation (C5): 2 requests
// con la misma sesión → ContextSnapshot muestra requests:2, roles/part
// types agregados con est_tokens y pct, history 2 entries (más nuevo
// primero) y latest = el segundo request.
func TestContextRecord_BasicAccumulation(t *testing.T) {
	m := New()
	m.SetContextHistoryPerSession(50)
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.RecordContext("client1", "s1", "req2", "m", testContextWalkResult2(), 2000)

	snap, ok := m.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("ContextSnapshot(session) ok=false, want true")
	}
	if snap.Client != "client1" || snap.Scope != "session" || snap.Session != "s1" {
		t.Fatalf("snap = %+v, want client1/session/s1", snap)
	}
	if snap.Requests != 2 {
		t.Fatalf("Requests = %d, want 2", snap.Requests)
	}

	sum := snap.Summary
	if sum.PromptTokensEstimated != 170 { // 25+50+75+20
		t.Fatalf("PromptTokensEstimated = %d, want 170", sum.PromptTokensEstimated)
	}
	if sum.NumToolsTotal != 2 {
		t.Fatalf("NumToolsTotal = %d, want 2", sum.NumToolsTotal)
	}
	if sum.FirstRequestAt != 1000 || sum.LastRequestAt != 2000 {
		t.Fatalf("First/LastRequestAt = %d/%d, want 1000/2000", sum.FirstRequestAt, sum.LastRequestAt)
	}

	// Roles agregados por role (count de mensajes, est_tokens, pct).
	if sys, ok := findRoleAgg(sum.Roles, "system"); !ok || sys.Messages != 1 || sys.EstTokens != 25 {
		t.Fatalf("role system = %+v (ok=%v), want 1 msg/25 est", sys, ok)
	}
	user, ok := findRoleAgg(sum.Roles, "user")
	if !ok || user.Messages != 2 || user.EstTokens != 125 {
		t.Fatalf("role user = %+v (ok=%v), want 2 msg/125 est", user, ok)
	}
	if !approxEqual(user.Pct, 125.0/170, 1e-9) {
		t.Fatalf("role user pct = %f, want %f", user.Pct, 125.0/170)
	}
	if asst, ok := findRoleAgg(sum.Roles, "assistant"); !ok || asst.Messages != 1 || asst.EstTokens != 20 {
		t.Fatalf("role assistant = %+v (ok=%v), want 1 msg/20 est", asst, ok)
	}

	// Part types agregados (count, bytes, est_tokens).
	text, ok := findPartAgg(sum.PartTypes, "text")
	if !ok || text.Count != 3 || text.Bytes != 600 || text.EstTokens != 150 {
		t.Fatalf("part text = %+v (ok=%v), want 3/600/150", text, ok)
	}
	toolUse, ok := findPartAgg(sum.PartTypes, "tool_use")
	if !ok || toolUse.Count != 1 || toolUse.Bytes != 80 || toolUse.EstTokens != 20 {
		t.Fatalf("part tool_use = %+v (ok=%v), want 1/80/20", toolUse, ok)
	}

	// History: 2 entries, más nuevo primero; latest = el segundo request.
	if len(snap.History) != 2 {
		t.Fatalf("History len = %d, want 2", len(snap.History))
	}
	if snap.History[0].RequestID != "req2" || snap.History[1].RequestID != "req1" {
		t.Fatalf("History order = [%s %s], want [req2 req1] (más nuevo primero)", snap.History[0].RequestID, snap.History[1].RequestID)
	}
	if snap.Latest == nil || snap.Latest.RequestID != "req2" {
		t.Fatalf("Latest = %+v, want req2", snap.Latest)
	}
	if snap.Latest.PromptTokensActual != nil {
		t.Fatalf("Latest.PromptTokensActual = %v, want nil (fase 1, aún sin usage)", *snap.Latest.PromptTokensActual)
	}
	if snap.Latest.Model != "m" {
		t.Fatalf("Latest.Model = %q, want m", snap.Latest.Model)
	}

	// El record de cliente acumula TODO (los 2 requests tenían sesión).
	snapC, okC := m.ContextSnapshot("client1", "")
	if !okC {
		t.Fatal("ContextSnapshot(client) ok=false, want true")
	}
	if snapC.Scope != "client" || snapC.Session != "" {
		t.Fatalf("snapC scope/session = %q/%q, want client/\"\"", snapC.Scope, snapC.Session)
	}
	if snapC.Requests != 2 {
		t.Fatalf("record de cliente Requests = %d, want 2", snapC.Requests)
	}
}

// test_postcondition_2_ContextRecord_ClientIsolation (C6): 2 clientes con
// el MISMO session id "s1" → cada uno ve SOLO su propio record.
func TestContextRecord_ClientIsolation(t *testing.T) {
	m := New()
	m.RecordContext("clientA", "s1", "reqA1", "m", testContextWalkResult(), 1000)
	m.RecordContext("clientB", "s1", "reqB1", "m", testContextWalkResult2(), 2000)

	snapA, okA := m.ContextSnapshot("clientA", "s1")
	if !okA {
		t.Fatal("clientA ok=false")
	}
	snapB, okB := m.ContextSnapshot("clientB", "s1")
	if !okB {
		t.Fatal("clientB ok=false")
	}
	if snapA.Requests != 1 || snapA.Latest == nil || snapA.Latest.RequestID != "reqA1" {
		t.Fatalf("clientA = %+v, want solo reqA1", snapA)
	}
	if snapB.Requests != 1 || snapB.Latest == nil || snapB.Latest.RequestID != "reqB1" {
		t.Fatalf("clientB = %+v, want solo reqB1", snapB)
	}
	// Aislamiento: el agregado de A no incluye el tráfico de B.
	if snapA.Summary.PromptTokensEstimated != 75 { // 25+50, NO 170/95
		t.Fatalf("clientA PromptTokensEstimated = %d, want 75 (sin tráfico de B — I3)", snapA.Summary.PromptTokensEstimated)
	}
	if snapB.Summary.PromptTokensEstimated != 95 { // 75+20
		t.Fatalf("clientB PromptTokensEstimated = %d, want 95", snapB.Summary.PromptTokensEstimated)
	}

	// Records de cliente también aislados.
	if cA, _ := m.ContextSnapshot("clientA", ""); cA.Requests != 1 {
		t.Fatalf("record de cliente A Requests = %d, want 1", cA.Requests)
	}
	if cB, _ := m.ContextSnapshot("clientB", ""); cB.Requests != 1 {
		t.Fatalf("record de cliente B Requests = %d, want 1", cB.Requests)
	}
}

// test_postcondition_2_ContextRecord_ClientAggregatesAll (C7): 3 requests
// (2 con sesión, 1 sin) → el record de cliente muestra requests:3; el de
// la sesión, requests:2.
func TestContextRecord_ClientAggregatesAll(t *testing.T) {
	m := New()
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.RecordContext("client1", "s1", "req2", "m", testContextWalkResult2(), 2000)
	m.RecordContext("client1", "", "req3", "m", testContextWalkResult(), 3000)

	snapC, okC := m.ContextSnapshot("client1", "")
	if !okC {
		t.Fatal("record de cliente ok=false")
	}
	if snapC.Requests != 3 {
		t.Fatalf("record de cliente Requests = %d, want 3 (acumula TODO — C7)", snapC.Requests)
	}
	if snapC.Summary.PromptTokensEstimated != 245 { // 75+95+75
		t.Fatalf("record de cliente PromptTokensEstimated = %d, want 245", snapC.Summary.PromptTokensEstimated)
	}

	sess, ok := m.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("sesión s1 ok=false")
	}
	if sess.Requests != 2 {
		t.Fatalf("sesión s1 Requests = %d, want 2", sess.Requests)
	}
}

// test_postcondition_2_ContextRecord_RingBuffer (C11): history_per_session
// 2 → tras 3 requests la history tiene 2 entries (ring buffer); el
// agregado sigue contando los 3.
func TestContextRecord_RingBuffer(t *testing.T) {
	m := New()
	m.SetContextHistoryPerSession(2)
	for i := 0; i < 3; i++ {
		m.RecordContext("client1", "s1", "req"+string(rune('0'+i)), "m", testContextWalkResult(), int64(1000+i))
	}

	snap, ok := m.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("ok=false")
	}
	if snap.Requests != 3 {
		t.Fatalf("Requests = %d, want 3 (los agregados NO se descartan — C11)", snap.Requests)
	}
	if len(snap.History) != 2 {
		t.Fatalf("History len = %d, want 2 (ring buffer)", len(snap.History))
	}
	if snap.History[0].RequestID != "req2" || snap.History[1].RequestID != "req1" {
		t.Fatalf("History = [%s %s], want [req2 req1] (req0 evictada)", snap.History[0].RequestID, snap.History[1].RequestID)
	}
	if snap.Latest == nil || snap.Latest.RequestID != "req2" {
		t.Fatalf("Latest = %+v, want req2", snap.Latest)
	}
}

// test_postcondition_2_ContextRecord_EvictionFIFO (C14): max_sessions_retained
// 1 con 3 sesiones distintas → el record de cliente sobrevive (requests:3);
// la sesión más vieja → 404 (ok=false).
func TestContextRecord_EvictionFIFO(t *testing.T) {
	m := New()
	m.SetMaxSessionsRetained(1)
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.RecordContext("client1", "s2", "req2", "m", testContextWalkResult2(), 2000)
	m.RecordContext("client1", "s3", "req3", "m", testContextWalkResult(), 3000)

	snapC, okC := m.ContextSnapshot("client1", "")
	if !okC {
		t.Fatal("record de cliente ok=false")
	}
	if snapC.Requests != 3 {
		t.Fatalf("record de cliente Requests = %d, want 3 (nunca se evicta — C14)", snapC.Requests)
	}

	if _, ok := m.ContextSnapshot("client1", "s1"); ok {
		t.Fatal("s1 no evictada (la más vieja — FIFO)")
	}
	if _, ok := m.ContextSnapshot("client1", "s2"); ok {
		t.Fatal("s2 no evictada (FIFO por last_request_at)")
	}
	s3, ok := m.ContextSnapshot("client1", "s3")
	if !ok {
		t.Fatal("s3 evictada, want que sobreviva (la más nueva)")
	}
	if s3.Requests != 1 {
		t.Fatalf("s3 Requests = %d, want 1", s3.Requests)
	}
}

// test_postcondition_4_ContextRecord_UpdateUsage (C8): UpdateContextUsage
// por request_id → prompt_tokens_actual reflejado en la entry y en el
// agregado (dos fases).
func TestContextRecord_UpdateUsage(t *testing.T) {
	m := New()
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.UpdateContextUsage("client1", "s1", "req1", 1234)

	snap, ok := m.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("ok=false")
	}
	if snap.Latest == nil || snap.Latest.PromptTokensActual == nil {
		t.Fatalf("Latest.PromptTokensActual = %v, want set (fase 2 — C8)", snap.Latest)
	}
	if *snap.Latest.PromptTokensActual != 1234 {
		t.Fatalf("Latest.PromptTokensActual = %d, want 1234", *snap.Latest.PromptTokensActual)
	}
	if snap.Summary.PromptTokensActual != 1234 {
		t.Fatalf("Summary.PromptTokensActual = %d, want 1234", snap.Summary.PromptTokensActual)
	}
	// El record de cliente también acumula el actual (todo request lo toca).
	if snapC, _ := m.ContextSnapshot("client1", ""); snapC.Summary.PromptTokensActual != 1234 {
		t.Fatalf("record de cliente PromptTokensActual = %d, want 1234", snapC.Summary.PromptTokensActual)
	}
}

// test_postcondition_4_ContextRecord_UpdateUsageMissingRequest:
// UpdateContextUsage con request_id desconocido → no crash, sin cambios.
func TestContextRecord_UpdateUsageMissingRequest(t *testing.T) {
	m := New()
	m.RecordContext("client1", "s1", "req1", "m", testContextWalkResult(), 1000)
	m.UpdateContextUsage("client1", "s1", "nonexistent", 99) // debe ser no-op

	snap, ok := m.ContextSnapshot("client1", "s1")
	if !ok {
		t.Fatal("ok=false")
	}
	if snap.Latest == nil || snap.Latest.PromptTokensActual != nil {
		t.Fatalf("Latest.PromptTokensActual = %v, want nil (matcheo exacto por request_id)", snap.Latest)
	}
	if snap.Summary.PromptTokensActual != 0 {
		t.Fatalf("Summary.PromptTokensActual = %d, want 0", snap.Summary.PromptTokensActual)
	}
}

// test_postcondition_6_ContextSnapshot_Disabled (C10): sin records →
// ContextSnapshot devuelve snapshot cero/vacío con ok=false (nunca nil).
func TestContextSnapshot_Disabled(t *testing.T) {
	m := New()

	sess, ok := m.ContextSnapshot("client1", "s1")
	if ok {
		t.Fatal("session ok=true sin records, want false")
	}
	if sess.Requests != 0 || len(sess.History) != 0 || sess.Latest != nil {
		t.Fatalf("snapshot session no vacío: %+v", sess)
	}
	if len(sess.Summary.Roles) != 0 || len(sess.Summary.PartTypes) != 0 ||
		sess.Summary.PromptTokensEstimated != 0 || sess.Summary.PromptTokensActual != 0 {
		t.Fatalf("summary session no vacío: %+v", sess.Summary)
	}

	client, okC := m.ContextSnapshot("client1", "")
	if okC {
		t.Fatal("record de cliente ok=true sin records, want false")
	}
	if client.Requests != 0 || len(client.History) != 0 || client.Latest != nil {
		t.Fatalf("snapshot client no vacío: %+v", client)
	}
}
