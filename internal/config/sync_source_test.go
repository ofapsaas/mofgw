// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-003-merge-provider-catalog — bloque B1 del
// test-audit.md: knobs declarativos sync_source/sync_mirror (P14/P1/D2).
//
// RED HÍBRIDO por COMPILACIÓN (documentado; precedente aceptado del
// proyecto: metadata_red_test.go, opencode_session_red_test.go):
// config.ProviderConfig.SyncSource/SyncMirror aún no existen → este
// paquete de tests NO compila hasta que GREEN agregue los 2 campos:
//
//	SyncSource string `yaml:"sync_source"` // ""|zen|go|openrouter|modelsdev
//	SyncMirror string `yaml:"sync_mirror"` // id de provider de models.dev
//
// más la validación fail-fast de sync_source (P14: otro valor → error de
// carga CLARO; sync_mirror free-string, la validación de existencia es
// warning del merge, no de carga). La lógica de asociación (knob gana,
// auto por base_url) se testea en catalogmerge (B2, TestSourceAssociation):
// aquí solo se congela el CONTRATO DE CARGA (P14).

package config

import (
	"reflect"
	"strings"
	"testing"
)

// syncSourceYAML: config mínimo con los 2 knobs en valores válidos.
const syncSourceYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: acc-zen
    base_url: "https://opencode.ai/zen/v1/models"
    api_key_env: MOFGW_019003_TEST
    models: ["glm-5.2"]
    sync_source: "zen"
    sync_mirror: "opencode"
`

// syncSourceSinKnobsYAML: el MISMO config sin la sección nueva (P14/I7:
// configs existentes cargan idéntico; zero-value "" = auto).
const syncSourceSinKnobsYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: acc-zen
    base_url: "https://opencode.ai/zen/v1/models"
    api_key_env: MOFGW_019003_TEST
    models: ["glm-5.2"]
`

// TestSyncSourceKnob: contrato de carga P14 en 4 aserciones —
// (1) knobs válidos parsean tipados; (2) "bogus" → error de carga claro
// (fail-fast); (3) config sin knobs carga idéntico (deep-equal de la
// config completa Parse con/sin sección); (4) sync_mirror es free-string.
func TestSyncSourceKnob(t *testing.T) {
	t.Run("knobs_validos_parsean", func(t *testing.T) {
		t.Setenv("MOFGW_019003_TEST", "k1")
		cfg, err := LoadFile(writeTemp(t, syncSourceYAML))
		if err != nil {
			t.Fatalf("LoadFile con knobs válidos: %v", err)
		}
		p := cfg.Providers[0]
		if p.SyncSource != "zen" {
			t.Errorf("SyncSource = %q, want %q (P14: knob declarativo no parseado)", p.SyncSource, "zen")
		}
		if p.SyncMirror != "opencode" {
			t.Errorf("SyncMirror = %q, want %q (P14: knob declarativo no parseado)", p.SyncMirror, "opencode")
		}
	})

	t.Run("valor_invalido_error_carga", func(t *testing.T) {
		t.Setenv("MOFGW_019003_TEST", "k1")
		bogusYAML := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: acc-bogus
    base_url: "https://x.example/v1"
    api_key_env: MOFGW_019003_TEST
    models: ["m"]
    sync_source: "bogus"
`
		_, err := LoadFile(writeTemp(t, bogusYAML))
		if err == nil {
			t.Fatal("sync_source: \"bogus\" cargó sin error (P14: fail-fast con error claro)")
		}
		// Error CLARO: menciona el valor y el knob (diagnosticable al arranque).
		for _, want := range []string{"bogus", "sync_source"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q no menciona %q (P14: error de carga claro)", err.Error(), want)
			}
		}
	})

	t.Run("valores_validos_aceptados", func(t *testing.T) {
		// Los 4 valores no-vacíos del vocabulario + "" implícito (subtest 1).
		for _, v := range []string{"zen", "go", "openrouter", "modelsdev"} {
			yaml := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: acc-v
    base_url: "https://x.example/v1"
    api_key_env: MOFGW_019003_TEST
    models: ["m"]
    sync_source: "` + v + `"
`
			t.Setenv("MOFGW_019003_TEST", "k1")
			cfg, err := LoadFile(writeTemp(t, yaml))
			if err != nil {
				t.Errorf("sync_source %q rechazado: %v", v, err)
				continue
			}
			if cfg.Providers[0].SyncSource != v {
				t.Errorf("SyncSource = %q, want %q", cfg.Providers[0].SyncSource, v)
			}
		}
	})

	t.Run("sin_knobs_carga_identico", func(t *testing.T) {
		t.Setenv("MOFGW_019003_TEST", "k1")
		cfgSin, err := LoadFile(writeTemp(t, syncSourceSinKnobsYAML))
		if err != nil {
			t.Fatalf("LoadFile sin knobs (I7: config existente debe cargar): %v", err)
		}
		cfgCon, err := LoadFile(writeTemp(t, syncSourceYAML))
		if err != nil {
			t.Fatalf("LoadFile con knobs: %v", err)
		}

		// P14: SIN la sección, el provider carga con zero-values (auto).
		pSin := cfgSin.Providers[0]
		if pSin.SyncSource != "" || pSin.SyncMirror != "" {
			t.Errorf("sin knobs: SyncSource/SyncMirror = %q/%q, want \"\"/\"\" (default = auto, P14)", pSin.SyncSource, pSin.SyncMirror)
		}

		// Deep-equal del resto del config: quitar los 2 knobs del config
		// con knobs produce EXACTAMENTE el config sin knobs (cero cambios
		// de comportamiento existente, P14).
		cfgConNorm := *cfgCon
		cfgConNorm.Providers = append([]ProviderConfig(nil), cfgCon.Providers...)
		cfgConNorm.Providers[0].SyncSource = ""
		cfgConNorm.Providers[0].SyncMirror = ""
		if !reflect.DeepEqual(cfgSin, &cfgConNorm) {
			t.Error("config sin knobs != config con knobs normalizado (P14: cambio NO puramente aditivo)")
		}
	})

	t.Run("sync_mirror_free_string", func(t *testing.T) {
		// P14: sync_mirror NO se valida contra nada al cargar (la
		// validación de existencia = warning del merge, D4); cualquier
		// string carga.
		t.Setenv("MOFGW_019003_TEST", "k1")
		mirrorYAML := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: acc-mirror
    base_url: "https://x.example/v1"
    api_key_env: MOFGW_019003_TEST
    models: ["m"]
    sync_mirror: "espejo-inexistente-en-modelsdev"
`
		cfg, err := LoadFile(writeTemp(t, mirrorYAML))
		if err != nil {
			t.Fatalf("sync_mirror free-string rechazado al cargar (P14: validación = warning del merge, no de carga): %v", err)
		}
		if cfg.Providers[0].SyncMirror != "espejo-inexistente-en-modelsdev" {
			t.Errorf("SyncMirror = %q, want el valor tal cual", cfg.Providers[0].SyncMirror)
		}
	})
}
