// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 014-001-registro-unificado — P1 (criterio C1).
// config.RegistryConfig{Enabled, File} bajo `registry:` top-level, default
// {false, ""}; enabled=true con file="" → error de validación en Parse
// (fail-fast, patrón `telemetry` 009-000 P8). RED por compilación:
// Config.Registry / RegistryConfig aún no existen.

package config

import "testing"

// Test014001_P1_DefaultsRegistry verifica C1: config sin bloque `registry:`
// → cfg.Registry == RegistryConfig{false, ""} (off por default, P1).
func Test014001_P1_DefaultsRegistry(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := RegistryConfig{Enabled: false, File: ""}
	if cfg.Registry != want {
		t.Fatalf("Registry = %+v, want %+v (defaults P1/C1)", cfg.Registry, want)
	}
}

// Test014001_P1_BlockValidoSeCarga verifica P1: bloque `registry:` presente
// → se carga tipado (enabled=true, file leído).
func Test014001_P1_BlockValidoSeCarga(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	raw := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_PROVIDER_A_KEY
    models: ["m"]
registry:
  enabled: true
  file: "/tmp/reg.jsonl"
`
	cfg, err := LoadFile(writeTemp(t, raw))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cfg.Registry.Enabled {
		t.Fatal("Registry.Enabled = false, want true (P1)")
	}
	if cfg.Registry.File != "/tmp/reg.jsonl" {
		t.Fatalf("Registry.File = %q, want /tmp/reg.jsonl (P1)", cfg.Registry.File)
	}
}

// Test014001_P1_EnabledConFileVacio verifica C1: enabled=true + file="" →
// error de validación en config.Parse (el proceso no levanta, fail-fast D2).
func Test014001_P1_EnabledConFileVacio(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	bad := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_PROVIDER_A_KEY
    models: ["m"]
registry:
  enabled: true
  file: ""
`
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("enabled=true con file vacío debe fallar la validación (C1/P1)")
	}
}
