// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-002-sticky-session — P1 (C1).
// FallbackConfig gana StickyRouting StickyRoutingConfig{Enabled bool} bajo
// `fallback.sticky_routing.enabled`. Default false (ausente en YAML → false);
// `enabled: "si"` (no bool) → error de arranque (unmarshal falla, fail-fast).
// RED por compilación: StickyRoutingConfig / FallbackConfig.StickyRouting
// aún no existen. Firmas que fija (spec §Contrato — firmas):
//
//	type StickyRoutingConfig struct {
//		Enabled bool `yaml:"enabled"` // default false
//	}
//	type FallbackConfig struct {
//		...
//		StickyRouting StickyRoutingConfig `yaml:"sticky_routing"` // NUEVO
//	}

package config

import (
	"strings"
	"testing"
)

// stickyYAML es un config mínimo con el bloque fallback.sticky_routing.
const stickyYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  sticky_routing:
    enabled: true
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
clients:
  - id: agent-main
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`

// test_postcondition_1_StickyEnabledTrue (C1): YAML con
// fallback.sticky_routing.enabled: true → parse OK y flag true.
func TestPostcondition1_StickyEnabledTrue(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, stickyYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cfg.Fallback.StickyRouting.Enabled {
		t.Fatal("StickyRouting.Enabled = false, want true (C1)")
	}
}

// test_postcondition_1_StickyAusenteDefaults (C1): bloque ausente → false
// (default; cero cambio de comportamiento — P1/P8).
func TestPostcondition1_StickyAusenteDefaults(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Fallback.StickyRouting.Enabled {
		t.Fatal("StickyRouting.Enabled = true sin bloque, want false (default C1)")
	}
}

// test_postcondition_1_StickyNoBoolError (C1): enabled con valor no
// booleano → error de arranque (fail-fast, patrón config).
func TestPostcondition1_StickyNoBoolError(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	bad := strings.Replace(stickyYAML, "enabled: true", `enabled: "si"`, 1)
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("LoadFile con enabled no-bool: se esperaba error de arranque (C1)")
	}
}
