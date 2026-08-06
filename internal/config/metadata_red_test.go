// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
)

// ---- 007-001-model-metadata: RED ----

// metadataYAML es un config mínimo con model_metadata (C1).
const metadataYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["deepseek-v4-flash"]
clients:
  - id: agent-main
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
model_metadata:
  deepseek-v4-flash:
    context_window: 1000000
    max_output: 384000
    thinking: ["low", "high", "max"]
    thinking_default: "high"
`

// TestRED_LoadMetadataValida: model_metadata se carga y se accede tipado
// (C1). RED por compilación: Config.ModelMetadata aún no existe.
func TestRED_LoadMetadataValida(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, metadataYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	md, ok := cfg.ModelMetadata["deepseek-v4-flash"]
	if !ok {
		t.Fatal("metadata de deepseek-v4-flash no cargada")
	}
	if md.ContextWindow != 1000000 {
		t.Fatalf("ContextWindow = %d, want 1000000", md.ContextWindow)
	}
	if md.MaxOutput != 384000 {
		t.Fatalf("MaxOutput = %d, want 384000", md.MaxOutput)
	}
	if len(md.Thinking) != 3 || md.Thinking[0] != "low" || md.Thinking[2] != "max" {
		t.Fatalf("Thinking = %v", md.Thinking)
	}
	if md.ThinkingDefault != "high" {
		t.Fatalf("ThinkingDefault = %q, want high", md.ThinkingDefault)
	}
}

// TestRED_LoadSinMetadataVacia: config sin model_metadata → metadata
// vacía, sin error (C2).
func TestRED_LoadSinMetadataVacia(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.ModelMetadata) != 0 {
		t.Fatalf("ModelMetadata = %+v, want vacío", cfg.ModelMetadata)
	}
}

// TestRED_ThinkingDefaultFueraDeLista: thinking_default no incluido en
// thinking → error de validación (C3/P4).
func TestRED_ThinkingDefaultFueraDeLista(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	bad := `
server: {addr: "127.0.0.1:3369"}
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
model_metadata:
  m:
    thinking: ["low", "max"]
    thinking_default: "xhigh"
`
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("thinking_default fuera de thinking debe fallar")
	}
}

// TestRED_ContextWindowNegativo: context_window negativo → error (P4).
func TestRED_ContextWindowNegativo(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	bad := `
server: {addr: "127.0.0.1:3369"}
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
model_metadata:
  m:
    context_window: -1
`
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("context_window negativo debe fallar")
	}
}
