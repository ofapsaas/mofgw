// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-001-config-renderer-core — knob config
// `client_config` (aditivo, off-safe).
//
// Contrato (spec 016-001 §2.3):
//   - `ClientConfigConfig` con `BaseURL` (yaml:"base_url") y `KeyEnv`
//     (yaml:"key_env"); default `KeyEnv: "MOFGW_KEY"`, `BaseURL: ""`.
//   - bloque `client_config` ausente/vacío es un estado VÁLIDO (off-safe,
//     I3/I4): NO hay fail-fast por base_url vacío (ese es el 503 de runtime).
//   - `KeyEnvRef` viaja como referencia; no se resuelve ningún env var.
//
// Referencia el tipo nuevo `ClientConfigConfig` y el campo `Config.ClientConfig`
// (will-not-compile hasta GREEN): estos tests son la RED de la feature.

package config

import "testing"

// secretLiteralForConfigTest: invariante P8/I1 — mofgw NUNCA resuelve ni
// emite el valor del secret asociado a key_env; el knob es solo la ref.
const secretLiteralForConfigTest = "sk-literal-never-resolve-016"

// TestRED_ClientConfig_DirectDefaultsType está en el paquete de la feature
// cliente (proxy) usando cfg.ClientConfig; acá testeamos los defaults del
// knob vía defaults() y el parse off-safe.

// TestRED_ClientConfigDefaults_P7 — P7: default key_env="MOFGW_KEY",
// base_url="" (vacío → 503 runtime, NO fail-fast).
func TestRED_ClientConfigDefaults_P7(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")

	// Sin bloque client_config en el YAML (off-safe, I3): los defaults
	// aplicados deben ser MOFGW_KEY / "".
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile() sin client_config debería cargar OK (off-safe): %v", err)
	}
	if cfg.ClientConfig.KeyEnv != "MOFGW_KEY" {
		t.Fatalf("ClientConfig.KeyEnv default = %q, want MOFGW_KEY", cfg.ClientConfig.KeyEnv)
	}
	if cfg.ClientConfig.BaseURL != "" {
		t.Fatalf("ClientConfig.BaseURL default = %q, want \"\" (vacío → 503 runtime)", cfg.ClientConfig.BaseURL)
	}
}

// TestRED_ClientConfig_ExplicitFields — el knob parsea base_url y key_env
// explícitos desde YAML (additivo).
func TestRED_ClientConfig_ExplicitFields(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")

	yamlBody := validYAML + "\nclient_config:\n  base_url: \"https://mofgw.example/v1\"\n  key_env: \"MY_CUSTOM_KEY\"\n"
	cfg, err := LoadFile(writeTemp(t, yamlBody))
	if err != nil {
		t.Fatalf("LoadFile() con client_config explícito: %v", err)
	}
	if cfg.ClientConfig.BaseURL != "https://mofgw.example/v1" {
		t.Fatalf("ClientConfig.BaseURL = %q, want https://mofgw.example/v1", cfg.ClientConfig.BaseURL)
	}
	if cfg.ClientConfig.KeyEnv != "MY_CUSTOM_KEY" {
		t.Fatalf("ClientConfig.KeyEnv = %q, want MY_CUSTOM_KEY", cfg.ClientConfig.KeyEnv)
	}
}

// TestRED_ClientConfigEmptyBlockOffSafe — bloque client_config vacío ("")
// o presente pero sin campos → sigue siendo válido (I4: base_url vacío es
// estado documentado, NO fail-fast de carga).
func TestRED_ClientConfigEmptyBlockOffSafe(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")

	for name, yamlBody := range map[string]string{
		"bloque vacío (yaml null)": validYAML + "\nclient_config:\n",
		"solo base_url vacío":      validYAML + "\nclient_config:\n  base_url: \"\"\n",
		"solo key_env":             validYAML + "\nclient_config:\n  key_env: \"X\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadFile(writeTemp(t, yamlBody))
			if err != nil {
				t.Fatalf("LoadFile() debería ser off-safe (sin fail-fast por base_url vacío): %v", err)
			}
			// En todos los casos base_url vacío es válido; key_env default MOFGW_KEY
			// si no se especifica.
			if cfg.ClientConfig.KeyEnv == "" {
				t.Fatalf("ClientConfig.KeyEnv quedó vacío; el default MOFGW_KEY no se aplicó")
			}
		})
	}
}

// TestRED_ClientConfig_KeyEnvRefNoSeResuelve — P8/I1: el knob key_env es una
// REFERENCIA; mofgw no resuelve el valor del env var. Varios ko/ok asserts
// aquí reflejan que ClientConfigConfig.KeyEnv es la ref (cadena) y nunca se
// rastea un secret.
func TestRED_ClientConfig_KeyEnvRefNoSeResuelve(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")

	yamlBody := validYAML + "\nclient_config:\n  key_env: \"MOFGW_KEY\"\n"
	cfg, err := Parse([]byte(yamlBody))
	if err != nil {
		t.Fatalf("Parse(): %v", err)
	}
	if cfg.ClientConfig.KeyEnv != "MOFGW_KEY" {
		t.Fatalf("ClientConfig.KeyEnv = %q, want MOFGW_KEY (referencia)", cfg.ClientConfig.KeyEnv)
	}
	// El valor NO se resuelve: no debe existir ningún campo con el valor
	// del secret en el repo de config. (Assert de invariante I1.)
	if cfg.ClientConfig.KeyEnv == secretLiteralForConfigTest {
		t.Fatalf("KeyEnv no debe contener el valor del secret: %q", cfg.ClientConfig.KeyEnv)
	}
}