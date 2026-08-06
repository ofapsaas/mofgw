package config

import (
	"strings"
	"testing"
)

// 007-001 external review (Major): thinking_default con thinking vacío → error.
func TestExternalReview_ThinkingDefaultConThinkingVacio(t *testing.T) {
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
    thinking_default: "high"
`
	_, err := LoadFile(writeTemp(t, bad))
	if err == nil {
		t.Fatal("thinking_default con thinking vacío debe fallar")
	}
	if !strings.Contains(err.Error(), "thinking está vacío") {
		t.Fatalf("error inesperado: %v", err)
	}
}

// 008-003 external review (Major): max_sessions_retained desde config.
func TestExternalReview_MaxSessionsRetainedEnConfig(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	good := `
server:
  addr: "127.0.0.1:3369"
  max_sessions_retained: 42
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
`
	cfg, err := LoadFile(writeTemp(t, good))
	if err != nil {
		t.Fatalf("config válida no debe fallar: %v", err)
	}
	if cfg.Server.MaxSessionsRetained != 42 {
		t.Fatalf("MaxSessionsRetained = %d, want 42", cfg.Server.MaxSessionsRetained)
	}
}

func TestExternalReview_MaxSessionsRetainedDefault(t *testing.T) {
	if got := defaults().Server.MaxSessionsRetained; got != 100 {
		t.Fatalf("default MaxSessionsRetained = %d, want 100", got)
	}
}

func TestExternalReview_MaxSessionsRetainedNegativo(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	bad := `
server:
  addr: "127.0.0.1:3369"
  max_sessions_retained: -1
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_TEST_A
    models: ["m"]
`
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Fatal("max_sessions_retained negativo debe fallar")
	}
}

// 006-002 external review (hallazgo del resp.json truncado): precios
// .inf/.nan en YAML pasaban la validación `< 0` (NaN/Inf no son < 0) →
// costos NaN/Inf en metrics. Ahora se rechazan como no-finitos.
func TestExternalReview_PricingNaNInfRechazado(t *testing.T) {
	for _, precio := range []string{".inf", ".nan"} {
		t.Setenv("MOFGW_TEST_A", "k1")
		bad := "server: {addr: \"127.0.0.1:3369\"}\n" +
			"providers:\n  - id: provider-a\n    base_url: \"http://localhost:1/v1\"\n    api_key_env: MOFGW_TEST_A\n    models: [\"m\"]\n" +
			"pricing:\n  m:\n    input_usd_per_m: " + precio + "\n"
		if _, err := LoadFile(writeTemp(t, bad)); err == nil {
			t.Fatalf("pricing %s debe fallar (no finito)", precio)
		} else if !strings.Contains(err.Error(), "finitos") {
			t.Fatalf("pricing %s: error inesperado: %v", precio, err)
		}
	}
	// -.inf cae en la validación de negativos (también rechazado).
	t.Setenv("MOFGW_TEST_A", "k1")
	badNeg := "server: {addr: \"127.0.0.1:3369\"}\n" +
		"providers:\n  - id: provider-a\n    base_url: \"http://localhost:1/v1\"\n    api_key_env: MOFGW_TEST_A\n    models: [\"m\"]\n" +
		"pricing:\n  m:\n    input_usd_per_m: -.inf\n"
	if _, err := LoadFile(writeTemp(t, badNeg)); err == nil {
		t.Fatal("pricing -.inf debe fallar (negativo)")
	}
}
