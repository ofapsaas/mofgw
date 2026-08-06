// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 008-003-stats-por-sesion: correlacionar
// requests por X-Session-Id y exponer stats por sesión. Verifican
// postcondiciones P1-P5 del spec docs/specs/008-003-stats-por-sesion/spec.md.
//
// Contrato observable: GET /v1/usage?session=<id> con stats de la sesión;
// /v1/usage lista sesiones; X-Session-Id no llega upstream. Cero red
// externa.

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// chatAsSession envía un chat con X-Session-Id (y key dada).
func (h *harness) chatAsSession(t *testing.T, key, session string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	if session != "" {
		req.Header.Set("X-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// usageBodyQuery hace GET /v1/usage?session=<id> con auth y parsea.
func (h *harness) usageBodyQuery(t *testing.T, key, session string) map[string]any {
	t.Helper()
	url := h.srv.URL + "/v1/usage"
	if session != "" {
		url += "?session=" + session
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("usage status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("usage body no es JSON: %v", err)
	}
	return out
}

// TestRED_SesionStats: 3 requests con X-Session-Id s1 → /v1/usage?session=s1
// con requests 3 y tokens acumulados (C1).
func TestRED_SesionStats(t *testing.T) {
	ups := upstreamUsageOK("m") // prompt 1200, completion 80, total 1280
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	for i := 0; i < 3; i++ {
		resp := h.chatAsSession(t, "k1", "s1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d", i, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}

	u := h.usageBodyQuery(t, "k1", "s1")
	if u["session"] != "s1" {
		t.Fatalf("session = %v, want s1", u["session"])
	}
	if u["requests"] != float64(3) {
		t.Fatalf("requests = %v, want 3", u["requests"])
	}
	if u["total_tokens"] != float64(3840) {
		t.Fatalf("total_tokens = %v, want 3840", u["total_tokens"])
	}
}

// TestRED_SesionAislamiento: 2 clientes con el mismo session id → cada
// uno ve su propia sesión (C2).
func TestRED_SesionAislamiento(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1", "k2")

	// k1: 2 requests en s1; k2: 1 request en s1
	for i := 0; i < 2; i++ {
		resp := h.chatAsSession(t, "k1", "s1", map[string]any{
			"model":    "m",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		io.Copy(io.Discard, resp.Body)
	}
	resp := h.chatAsSession(t, "k2", "s1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	io.Copy(io.Discard, resp.Body)

	u1 := h.usageBodyQuery(t, "k1", "s1")
	if u1["requests"] != float64(2) {
		t.Fatalf("k1 requests = %v, want 2", u1["requests"])
	}
	u2 := h.usageBodyQuery(t, "k2", "s1")
	if u2["requests"] != float64(1) {
		t.Fatalf("k2 requests = %v, want 1", u2["requests"])
	}
}

// TestRED_UsageListaSesiones: /v1/usage sin query lista las sesiones del
// cliente (P3).
func TestRED_UsageListaSesiones(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAsSession(t, "k1", "s1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	io.Copy(io.Discard, resp.Body)

	u := h.usageBody(t, "k1")
	sessions, ok := u["sessions"].([]any)
	if !ok {
		t.Fatalf("sin sessions en /v1/usage: %v", u)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}
	first, _ := sessions[0].(map[string]any)
	if first["id"] != "s1" {
		t.Fatalf("session id = %v, want s1", first["id"])
	}
}

// TestRED_SesionInexistente404: /v1/usage?session=nope → 404 (C4).
func TestRED_SesionInexistente404(t *testing.T) {
	ups := upstreamOK("m", "x")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/usage?session=nope", nil)
	req.Header.Set("Authorization", "Bearer "+h.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("session inexistente = %d, want 404", resp.StatusCode)
	}
}

// TestRED_SessionHeaderNoUpstream: X-Session-Id no llega al upstream
// (C5).
func TestRED_SessionHeaderNoUpstream(t *testing.T) {
	ups := upstreamUsageOK("m")
	h := buildMultiClient(t, []*upstream{ups}, "k1")

	resp := h.chatAsSession(t, "k1", "s1", map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	// el header X-Session-Id NO debe llegar al upstream (solo lectura)
	if ups.gotHeaders.Get("X-Session-Id") != "" {
		t.Fatalf("X-Session-Id filtró al upstream: %v", ups.gotHeaders.Get("X-Session-Id"))
	}
}
