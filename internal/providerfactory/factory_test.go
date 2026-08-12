// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de 013-003-config-wiring (P5/C6): el factory construye el
// provider correcto por type. Hermético (D6): sin binario claude real, sin
// red. Contra el skeleton (BuildProvider/BuildBackend devuelven error) los
// tres tests fallan por assertion.
package providerfactory_test

import (
	"log/slog"
	"testing"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/providerfactory"
	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// TestT_BuildProviderSubprocess verifica P5 (C6): type:subprocess → un
// provider.Provider cuyo ID()==id, Serves() true para cada modelo, y
// MaxTokens()==max_tokens del config.
func TestT_BuildProviderSubprocess(t *testing.T) {
	pc := config.ProviderConfig{
		ID:           "sub",
		Type:         "subprocess",
		Backend:      "claude",
		Command:      "claude",
		SessionDir:   t.TempDir(),
		Models:       []string{"m1", "m2"},
		MaxTokens:    8192,
		BackendFlags: []string{"--flag"},
	}
	prov, err := providerfactory.BuildProvider(pc, slog.Default())
	if err != nil {
		t.Fatalf("BuildProvider(subprocess): %v", err)
	}
	if prov.ID() != "sub" {
		t.Fatalf("ID = %q, want sub", prov.ID())
	}
	if !prov.Serves("m1") || !prov.Serves("m2") {
		t.Fatalf("Serves debe ser true para cada modelo configurado, models=%v", pc.Models)
	}
	if prov.MaxTokens() != 8192 {
		t.Fatalf("MaxTokens = %d, want 8192", prov.MaxTokens())
	}
}

// TestT_BuildBackendClaude verifica P5 (C6): buildBackend("claude", cmd)
// devuelve un backend con Name()=="claude" que compila contra
// subprocess.Backend (assert de compilación).
func TestT_BuildBackendClaude(t *testing.T) {
	backend, err := providerfactory.BuildBackend("claude", "claude")
	if err != nil {
		t.Fatalf("BuildBackend(claude): %v", err)
	}
	var _ subprocess.Backend = backend // assert de compilación (C6)
	if backend.Name() != "claude" {
		t.Fatalf("Name = %q, want claude", backend.Name())
	}
}

// TestT_BuildProviderHTTPDefault verifica P5 (C6): type vacío/http →
// *provider.Client (backward compat, cero cambio para configs existentes).
func TestT_BuildProviderHTTPDefault(t *testing.T) {
	pc := config.ProviderConfig{
		ID:        "http",
		BaseURL:   "https://x/v1",
		APIKey:    "k",
		Models:    []string{"m"},
		MaxTokens: 4096,
	}
	prov, err := providerfactory.BuildProvider(pc, slog.Default())
	if err != nil {
		t.Fatalf("BuildProvider(http): %v", err)
	}
	if _, ok := prov.(*provider.Client); !ok {
		t.Fatalf("type = %T, want *provider.Client", prov)
	}
}
