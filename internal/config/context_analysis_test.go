// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-001-context-composition — P6 (C10).
// Verifican el bloque `context.analysis` del config:
//   - ausente o enabled=false → defaults (enabled=false,
//     history_per_session=50).
//   - history_per_session < 0 → validate() error.
//   - history_per_session=0 → válido (sin history, solo agregados).
//
// El bloque context existente (config.go:145-147) gana `analysis`:
//
//	type ContextAnalysisConfig struct {
//		Enabled           bool `yaml:"enabled"`
//		HistoryPerSession int  `yaml:"history_per_session"`
//	}
//
// y `ContextConfig` gana `Analysis ContextAnalysisConfig yaml:"analysis"`.
package config

import (
	"strings"
	"testing"
)

// parseContextConfig parsea validYAML (config_test.go) más un bloque de
// context opcional, con las env vars de providers seteadas.
func parseContextConfig(t *testing.T, contextBlock string) (*Config, error) {
	t.Helper()
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	raw := validYAML
	if contextBlock != "" {
		raw += "\n" + contextBlock
	}
	return Parse([]byte(raw))
}

// test_postcondition_6_ContextAnalysisConfig_Defaults (C10): analysis
// ausente o enabled=false → defaults enabled=false, history_per_session=50.
func TestContextAnalysisConfig_Defaults(t *testing.T) {
	// Bloque context/analysis AUSENTE.
	cfg, err := parseContextConfig(t, "")
	if err != nil {
		t.Fatalf("Parse sin context: %v", err)
	}
	if cfg.Context.Analysis.Enabled {
		t.Fatal("Analysis.Enabled = true sin bloque analysis, want false (C10)")
	}
	if cfg.Context.Analysis.HistoryPerSession != 50 {
		t.Fatalf("Analysis.HistoryPerSession = %d, want 50 (default)", cfg.Context.Analysis.HistoryPerSession)
	}

	// context presente pero SIN analysis → mismos defaults.
	cfg2, err := parseContextConfig(t, "context:\n  margin: 0.1\n")
	if err != nil {
		t.Fatalf("Parse con context sin analysis: %v", err)
	}
	if cfg2.Context.Analysis.Enabled {
		t.Fatal("Analysis.Enabled = true con analysis ausente, want false")
	}
	if cfg2.Context.Analysis.HistoryPerSession != 50 {
		t.Fatalf("Analysis.HistoryPerSession = %d, want 50 (default)", cfg2.Context.Analysis.HistoryPerSession)
	}

	// enabled=false explícito + history custom.
	cfg3, err := parseContextConfig(t, "context:\n  analysis:\n    enabled: false\n    history_per_session: 5\n")
	if err != nil {
		t.Fatalf("Parse con analysis enabled=false: %v", err)
	}
	if cfg3.Context.Analysis.Enabled {
		t.Fatal("Analysis.Enabled = true con enabled=false explícito")
	}
	if cfg3.Context.Analysis.HistoryPerSession != 5 {
		t.Fatalf("Analysis.HistoryPerSession = %d, want 5", cfg3.Context.Analysis.HistoryPerSession)
	}
}

// test_postcondition_6_ContextAnalysisConfig_ValidateNegative (C10):
// history_per_session < 0 → validate() error (fail-fast en config).
func TestContextAnalysisConfig_ValidateNegative(t *testing.T) {
	_, err := parseContextConfig(t, "context:\n  analysis:\n    enabled: true\n    history_per_session: -1\n")
	if err == nil {
		t.Fatal("history_per_session negativo: esperaba error (P6/I5)")
	}
	if !strings.Contains(err.Error(), "history_per_session") {
		t.Fatalf("error sin nombrar history_per_session: %v", err)
	}
}

// test_postcondition_6_ContextAnalysisConfig_ValidateZero:
// history_per_session=0 → válido (sin history, solo agregados).
func TestContextAnalysisConfig_ValidateZero(t *testing.T) {
	cfg, err := parseContextConfig(t, "context:\n  analysis:\n    enabled: true\n    history_per_session: 0\n")
	if err != nil {
		t.Fatalf("Parse con history_per_session=0: %v (0 = sin history, válido — P6)", err)
	}
	if !cfg.Context.Analysis.Enabled {
		t.Fatal("Analysis.Enabled = false, want true (bloque explícito)")
	}
	if cfg.Context.Analysis.HistoryPerSession != 0 {
		t.Fatalf("Analysis.HistoryPerSession = %d, want 0 (explícito)", cfg.Context.Analysis.HistoryPerSession)
	}
}
