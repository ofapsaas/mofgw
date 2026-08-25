// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-000-request-telemetry — P8.
// config.TelemetryConfig{Enabled, SampleRate, File} bajo `telemetry:`
// top-level. Defaults {false, 1, ""}; validate() rechaza sample_rate<=0,
// sample_rate>1 y enabled=true con file vacío. RED por compilación:
// Config.Telemetry aún no existe.

package config

import (
	"fmt"
	"testing"
)

// telemetryYAML es un config mínimo con el bloque telemetry.
const telemetryYAML = `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
telemetry:
  enabled: true
  sample_rate: 0.5
  file: "/tmp/telemetry.jsonl"
`

// test_postcondition_8_DefaultsTelemetry: config sin bloque telemetry →
// defaults {Enabled:false, SampleRate:1, File:""} (P8).
func TestPostcondition8_DefaultsTelemetry(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := TelemetryConfig{Enabled: false, SampleRate: 1, File: ""}
	if cfg.Telemetry != want {
		t.Fatalf("Telemetry = %+v, want %+v (defaults P8)", cfg.Telemetry, want)
	}
}

// test_postcondition_8_BlockValidoSeCarga: bloque telemetry presente → se
// carga tipado (P8).
func TestPostcondition8_BlockValidoSeCarga(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, telemetryYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cfg.Telemetry.Enabled {
		t.Fatal("Telemetry.Enabled = false, want true")
	}
	if cfg.Telemetry.SampleRate != 0.5 {
		t.Fatalf("Telemetry.SampleRate = %v, want 0.5", cfg.Telemetry.SampleRate)
	}
	if cfg.Telemetry.File != "/tmp/telemetry.jsonl" {
		t.Fatalf("Telemetry.File = %q, want /tmp/telemetry.jsonl", cfg.Telemetry.File)
	}
}

// test_postcondition_8_SinBloqueDefaults: config mínimo sin telemetry →
// defaults (sin error) (P8).
func TestPostcondition8_SinBloqueDefaults(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	minimal := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
`
	cfg, err := LoadFile(writeTemp(t, minimal))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := TelemetryConfig{Enabled: false, SampleRate: 1, File: ""}
	if cfg.Telemetry != want {
		t.Fatalf("Telemetry = %+v, want %+v (defaults P8)", cfg.Telemetry, want)
	}
}

// test_postcondition_8_SampleRateInvalido: sample_rate <= 0 o > 1 →
// error de validación (P8).
func TestPostcondition8_SampleRateInvalido(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cases := []struct{ name, rate string }{
		{"cero", "0"},
		{"negativo", "-0.1"},
		{"mayor_que_uno", "1.5"},
	}
	for _, tc := range cases {
		bad := fmt.Sprintf(`
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
telemetry:
  enabled: true
  sample_rate: %s
  file: "/tmp/telemetry.jsonl"
`, tc.rate)
		if _, err := LoadFile(writeTemp(t, bad)); err == nil {
			t.Fatalf("sample_rate=%s debe fallar la validación", tc.rate)
		}
	}
}

// test_postcondition_8_EnabledConFileVacio: enabled=true + file="" → error
// de validación al arrancar (C10, I5).
func TestPostcondition8_EnabledConFileVacio(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	bad := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
telemetry:
  enabled: true
  file: ""
`
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("enabled=true con file vacío debe fallar (C10)")
	}
}
