// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// PART A — tests RED de sesión, prompt y multi-modelo (P1..P5).
// Compilan contra el skeleton y fallan por ASSERCIÓN (NewSessionKey
// devuelve "", Serves false, Complete error sin spawn).
package subprocess_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// T_session_same_client (P1, C2): mismo clientID ⇒ mismo Session.ID.
func TestT_SessionSameClient(t *testing.T) {
	id1 := subprocess.NewSessionKey("client-a", "", subprocess.SessionKeyClient)
	id2 := subprocess.NewSessionKey("client-a", "", subprocess.SessionKeyClient)
	if id1 == "" {
		t.Fatal("session ID vacío para clientID conocido") // RED: stub devuelve ""
	}
	if id1 != id2 {
		t.Fatalf("mismo clientID ⇒ distinta sesión: %q != %q", id1, id2)
	}
}

// T_session_diff_client (P1, C2): clientIDs distintos ⇒ IDs distintos.
func TestT_SessionDiffClient(t *testing.T) {
	idA := subprocess.NewSessionKey("client-a", "", subprocess.SessionKeyClient)
	idB := subprocess.NewSessionKey("client-b", "", subprocess.SessionKeyClient)
	if idA == idB {
		t.Fatalf("clientIDs distintos compartieron sesión: %q", idA) // RED
	}
}

// T_session_key_client_model (P2, C3): derivación client+model.
func TestT_SessionKeyClientModel(t *testing.T) {
	k1 := subprocess.NewSessionKey("c", "model-a", subprocess.SessionKeyClientModel)
	k2 := subprocess.NewSessionKey("c", "model-a", subprocess.SessionKeyClientModel)
	k3 := subprocess.NewSessionKey("c", "model-b", subprocess.SessionKeyClientModel)
	k4 := subprocess.NewSessionKey("c", "", subprocess.SessionKeyClient)
	if k1 == "" {
		t.Fatal("key client+model vacía") // RED: stub devuelve ""
	}
	if k1 != k2 {
		t.Fatalf("mismo (client,model) ⇒ distinta key: %q != %q", k1, k2)
	}
	if k1 == k3 {
		t.Fatalf("modelos distintos ⇒ misma key: %q", k1)
	}
	if k1 == k4 {
		t.Fatalf("modo client+model coincide con modo client: %q", k1)
	}
}

// T_multi_model_serves (P3, C4): una instancia sirve TODOS sus modelos.
func TestT_MultiModelServes(t *testing.T) {
	h := newTestProvider(t, withModels("m1", "m2"))
	if !h.p.Serves("m1") || !h.p.Serves("m2") {
		t.Fatalf("Serves debe servir todos los modelos configurados: m1=%v m2=%v",
			h.p.Serves("m1"), h.p.Serves("m2")) // RED: stub devuelve false
	}
}

// T_multi_model_same_session (P3, C4): el modelo NO altera la key en modo
// client (default).
func TestT_MultiModelSameSession(t *testing.T) {
	id1 := subprocess.NewSessionKey("client-a", "m1", subprocess.SessionKeyClient)
	id2 := subprocess.NewSessionKey("client-a", "m2", subprocess.SessionKeyClient)
	if id1 == "" {
		t.Fatal("session ID vacío") // RED
	}
	if id1 != id2 {
		t.Fatalf("el modelo alteró la key de sesión en modo client: %q != %q", id1, id2)
	}
}

// T_model_passed_per_request (P3, C4): el modelo llega a Backend.Args.
func TestT_ModelPassedPerRequest(t *testing.T) {
	h := newTestProvider(t, withModels("m1", "m2"))
	body := chatBody("m1", map[string]any{"role": "user", "content": "HELLO"})
	_, _ = h.p.Complete(clientCtx("client-a"), body)
	models := h.backend.recordedModels()
	if len(models) == 0 || models[0] != "m1" {
		t.Fatalf("Args no registró el modelo per-request: %v", models) // RED
	}
}

// T_single_last_user_prompt (P4, C5): el stdin del CLI = solo el último
// mensaje user.
func TestT_SingleLastUserPrompt(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	body := chatBody("m1",
		map[string]any{"role": "user", "content": "PREVIOUS"},
		map[string]any{"role": "user", "content": "HELLO"},
	)
	t.Setenv("STUB_STDIN_FILE", h.stdinFile)
	_, _ = h.p.Complete(clientCtx("client-a"), body)
	stdin, err := os.ReadFile(h.stdinFile)
	if err != nil {
		t.Fatalf("el motor no spawneó el CLI ni escribió stdin (RED): %v", err)
	}
	if string(stdin) != "HELLO" {
		t.Fatalf("stdin = %q, want solo el último mensaje user %q", string(stdin), "HELLO")
	}
}

// T_prompt_clean_no_tools (P5, C6): el prompt al CLI es texto natural limpio,
// sin tool_calls ni bloques de tool ni tools.
func TestT_PromptCleanNoTools(t *testing.T) {
	h := newTestProvider(t, withModels("m1"))
	raw, _ := json.Marshal(map[string]any{
		"model": "m1",
		"messages": []map[string]any{
			{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "f", "arguments": "{}"}}}},
			{"role": "tool", "tool_call_id": "call_1", "content": "tool result"},
			{"role": "user", "content": "HELLO"},
		},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "f", "parameters": map[string]any{}}}},
	})
	t.Setenv("STUB_STDIN_FILE", h.stdinFile)
	_, _ = h.p.Complete(clientCtx("client-a"), raw)

	stdin, err := os.ReadFile(h.stdinFile)
	if err != nil {
		t.Fatalf("el motor no spawneó el CLI ni escribió stdin (RED): %v", err)
	}
	s := string(stdin)
	for _, forbidden := range []string{"tool_calls", `"role":"tool"`, "tool result", `"tools"`, `"function"`, "call_1"} {
		if hasStr(s, forbidden) {
			t.Fatalf("prompt al CLI contiene material de tools %q: %q", forbidden, s)
		}
	}
	if s != "HELLO" {
		t.Fatalf("stdin = %q, want texto limpio del último user %q", s, "HELLO")
	}
}
