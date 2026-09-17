// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizza
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-004-atomic-write-validate — API aditiva
// `ParseForValidation` en internal/config. Cubre C15 (P13a/P13b).
//
// Contrato (P13a, spec §Contexto técnico verificado):
//
//	ParseForValidation(raw []byte) (*Config, error)
//	= unmarshal + chequeo `clients:` inline (anti-dual-source, igual que Parse)
//	+ clients_file + validate() + defaults subprocess — SIN resolveKeys
//	(no toca env vars JAMÁS; I5: las keys no pasan por el sync).
//
// P13c: Parse queda SIN cambio de comportamiento — la suite existente de
// internal/config (56 tests, UNTOUCHED) es el gate.
//
// RED por compilación (precedente 001/002/003): ParseForValidation no existe.
package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// unsetEnv garantiza la ausencia determinística de las env vars de keys
// (TestLoadMissingEnv solo no-las-setea; en una máquina de dev pueden existir).
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		if had {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unsetenv %s: %v", k, err)
			}
			t.Cleanup(func() {
				_ = os.Setenv(k, old)
			})
		}
	}
}

// ---- B20 (C15, P13a) — ParseForValidation NO toca env ----

// TestParseForValidation_NoEnv congela P13a: un raw válido SIN env vars
// resolvibles parsea OK con ParseForValidation (todas las APIKey vacías),
// mientras Parse falla nombrando la var ausente (P13b: única diferencia).
// Caso adicional del contrato P13a: el chequeo anti-dual-source de `clients:`
// inline se mantiene.
func TestParseForValidation_NoEnv(t *testing.T) {
	unsetEnv(t, "MOFGW_PROVIDER_A_KEY", "MOFGW_PROVIDER_B_KEY")
	raw := []byte(validYAML)

	t.Run("sin_env_valida_con_apikeys_vacias", func(t *testing.T) {
		cfg, err := ParseForValidation(raw)
		if err != nil {
			t.Fatalf("ParseForValidation sin env debería exitar (P13a: sin resolveKeys): %v", err)
		}
		if len(cfg.Providers) != 2 {
			t.Fatalf("providers = %d, want 2", len(cfg.Providers))
		}
		for _, p := range cfg.Providers {
			if p.APIKey != "" {
				t.Errorf("APIKey de %q = %q, want \"\" (I5: la validación jamás resuelve keys)", p.ID, p.APIKey)
			}
		}
	})

	t.Run("parse_con_env_ausente_falla_nombrando_la_var", func(t *testing.T) {
		_, err := Parse(raw)
		if err == nil {
			t.Fatal("Parse sin env debería fallar (comportamiento congelado de Parse, P13c)")
		}
		if !strings.Contains(err.Error(), "MOFGW_PROVIDER_A_KEY") {
			t.Errorf("error debería nombrar la var ausente, got: %v", err)
		}
	})

	t.Run("anti_dual_source_se_mantiene", func(t *testing.T) {
		// P13a incluye el "chequeo clients: inline" de Parse: inline +
		// clients_file juntos → error también en ParseForValidation
		// (017-001 P3, single source).
		clientsPath := writeClientsFileYAML(t, validClientsYAML)
		dual := validYAML +
			"clients:\n  - id: x\n    key_sha256: \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n" +
			"clients_file: " + clientsPath + "\n"
		if _, err := ParseForValidation([]byte(dual)); err == nil {
			t.Fatal("inline clients + clients_file juntos debería fallar también en ParseForValidation (P13a)")
		}
	})
}

// ---- B21 (C15, P13b) — Config idéntico a Parse salvo APIKey ----

// TestParseForValidation_MatchesParse congela P13b: para raw válido,
// ParseForValidation(raw) y Parse(raw) producen Configs idénticos salvo
// APIKey (vacía vs resuelta). Tabla: http clásico, 029 con override de
// first_token_timeout, subprocess con defaults (P13a los hereda).
func TestParseForValidation_MatchesParse(t *testing.T) {
	// env para los raws http (subprocess no usa env; setear es inocuo).
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	t.Setenv("MOFGW_P4_KEY", "k1") // raw type:http del suite 013-003

	for _, tc := range []struct {
		name    string
		raw     string
		envKeys []string // keys que Parse DEBE resolver en este raw
	}{
		{name: "http_clasico", raw: validYAML, envKeys: []string{"MOFGW_PROVIDER_A_KEY", "MOFGW_PROVIDER_B_KEY"}},
		{name: "first_token_timeout", raw: validYAML029, envKeys: []string{"MOFGW_PROVIDER_A_KEY"}},
		{name: "subprocess_defaults", raw: subprocessYAML, envKeys: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgP, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			cfgV, err := ParseForValidation([]byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseForValidation: %v", err)
			}

			// APIKey: resuelta vs vacía (la única diferencia permitida).
			if len(tc.envKeys) > 0 {
				hasResolved := false
				for _, p := range cfgP.Providers {
					if p.APIKey != "" {
						hasResolved = true
					}
				}
				if !hasResolved {
					t.Errorf("Parse no resolvió ninguna APIKey (P13c: comportamiento de Parse intacto)")
				}
			}
			for _, p := range cfgV.Providers {
				if p.APIKey != "" {
					t.Errorf("ParseForValidation resolvió APIKey de %q (P13a: sin resolveKeys)", p.ID)
				}
			}

			// DeepEqual del Config completo tras zero-ear APIKey en ambos.
			zeroAPIKey := func(c *Config) *Config {
				cp := *c
				providers := append([]ProviderConfig(nil), c.Providers...)
				for i := range providers {
					providers[i].APIKey = ""
				}
				cp.Providers = providers
				return &cp
			}
			if !reflect.DeepEqual(zeroAPIKey(cfgP), zeroAPIKey(cfgV)) {
				t.Errorf("Configs difieren más allá de APIKey (P13b violado):\nParse:              %+v\nParseForValidation: %+v", zeroAPIKey(cfgP), zeroAPIKey(cfgV))
			}
		})
	}
}
