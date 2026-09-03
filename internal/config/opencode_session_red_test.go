// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 018-001-opencode-session-header — bloque B9 del
// test-audit.md: parseo/wiring del campo opencode_session (P10/I7/C9).
//
// RED esperado por COMPILACIÓN (precedente aceptado del proyecto:
// metadata_red_test.go referencia cfg.ModelMetadata antes de que exista):
// ProviderConfig.OpenCodeSession aún no existe → build-fail de este
// paquete de tests. Es el único build-fail aceptado (el campo es el
// contrato declarativo que el implementer agrega en GREEN, con yaml tag
// `opencode_session`).

package config

import (
	"testing"
)

// opencodeSessionYAML es un config mínimo con el knob activo en el
// provider (P10: bool opcional declarativo, I1: nunca inferido).
const opencodeSessionYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_018001_TEST_A
    models: ["m"]
    opencode_session: true
`

// opencodeSessionSinCampoYAML es el mismo config SIN el campo nuevo
// (I7: configs existentes cargan idéntico; default false).
const opencodeSessionSinCampoYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_018001_TEST_A
    models: ["m"]
`

// TestPostcondition10_ConfigWiring: config con opencode_session: true
// carga y expone el knob tipado en el provider (P10, C9).
func TestPostcondition10_ConfigWiring(t *testing.T) {
	t.Setenv("MOFGW_018001_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, opencodeSessionYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	if !cfg.Providers[0].OpenCodeSession {
		t.Fatal("OpenCodeSession = false, want true (P10: knob declarativo no parseado)")
	}
}

// TestPostcondition10_ConfigDefaultFalse: config SIN el campo → false,
// sin error, y el resto del provider carga intacto (P10/I7, C9).
func TestPostcondition10_ConfigDefaultFalse(t *testing.T) {
	t.Setenv("MOFGW_018001_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, opencodeSessionSinCampoYAML))
	if err != nil {
		t.Fatalf("LoadFile (I7: config existente debe cargar sin cambios): %v", err)
	}
	p := cfg.Providers[0]
	if p.OpenCodeSession {
		t.Fatal("OpenCodeSession = true, want false (I7: default false)")
	}
	// I7: los campos preexistentes siguen parseando idéntico.
	if p.ID != "provider-a" || p.BaseURL != "http://localhost:1/v1" || p.APIKey != "k1" {
		t.Fatalf("provider mutado por el campo nuevo: id=%q base_url=%q api_key=%q", p.ID, p.BaseURL, p.APIKey)
	}
}
