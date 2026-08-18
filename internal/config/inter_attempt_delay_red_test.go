// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"
)

// ==== 015-001-inter-attempt-delay: P1 (config) ====
//
// Contrato: `fallback.inter_attempt_delay` se parsea y valida como duración,
// y su default es 0 (off).
//
// NOTA RED-especial de compilación: el campo tipado
// `cfg.Fallback.InterAttemptDelay` no existe todavía (la feature no está
// implementada). En Go tipado no se puede asertar un campo que no existe →
// este RED falla por compilación (precedente 011-005 "SetWebSearch
// undefined"), y las aserciones internas verifican el contrato observable
// una vez que GREEN agrega el campo. El implementer en GREEN agrega
// `Fallback.InterAttemptDelay time.Duration` (default 0, negativo rechazado).

func Test015001_P1_DefaultInterAttemptDelayZero(t *testing.T) {
	t.Setenv("MOFGW_015001_KEY", "k1")
	cfg, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_015001_KEY"
    models: ["m"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Fallback.InterAttemptDelay != 0 {
		t.Fatalf("InterAttemptDelay default = %s, want 0", cfg.Fallback.InterAttemptDelay)
	}
}

func Test015001_P1_ExplicitInterAttemptDelay(t *testing.T) {
	t.Setenv("MOFGW_015001_KEY", "k1")
	cfg, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  inter_attempt_delay: 5s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_015001_KEY"
    models: ["m"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Fallback.InterAttemptDelay != 5*time.Second {
		t.Fatalf("InterAttemptDelay = %s, want 5s", cfg.Fallback.InterAttemptDelay)
	}
}

func Test015001_P1_NegativeInterAttemptDelayRejected(t *testing.T) {
	t.Setenv("MOFGW_015001_KEY", "k1")
	_, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  inter_attempt_delay: -5s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_015001_KEY"
    models: ["m"]
`))
	if err == nil {
		t.Fatal("inter_attempt_delay negativo debería fallar la validación")
	}
}
