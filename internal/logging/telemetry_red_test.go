// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-000-request-telemetry — P1.
// logging.BuildTelemetry(file string) (*slog.Logger, io.Closer, error):
// logger JSON nivel Info sobre el archivo (jsonl), constructor SEPARADO de
// Build(); fail-fast si el archivo no se abre; permisos 0o640; devuelve
// closer. RED: la función no existe → error de compilación.

package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// test_postcondition_1_BuildTelemetryEscribeJSONL: cada Info es UNA línea
// JSON; Debug no sale (nivel Info); close-then-read (R6).
func TestPostcondition1_BuildTelemetryEscribeJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	logger, closer, err := BuildTelemetry(path)
	if err != nil {
		t.Fatalf("BuildTelemetry: %v", err)
	}

	logger.Info("evt-a")
	logger.Debug("debug-no-sale")
	logger.Info("evt-b")

	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got := readLogFile(t, path)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (una por Info, jsonl): %q", len(lines), got)
	}
	for i, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("línea %d no es JSON: %q: %v", i, ln, err)
		}
	}
	if !strings.Contains(got, "evt-a") || !strings.Contains(got, "evt-b") {
		t.Fatalf("eventos ausentes: %q", got)
	}
	if strings.Contains(got, "debug-no-sale") {
		t.Fatalf("Debug no debe emitirse a nivel Info: %q", got)
	}
}

// test_postcondition_1_BuildTelemetryFileVacioError: file=="" → error (P1).
func TestPostcondition1_BuildTelemetryFileVacioError(t *testing.T) {
	if _, _, err := BuildTelemetry(""); err == nil {
		t.Fatal("BuildTelemetry(\"\") debe devolver error")
	}
}

// test_postcondition_1_BuildTelemetryFailFast: archivo no abrible → error
// (fail-fast, mismo patrón que Build(); I5).
func TestPostcondition1_BuildTelemetryFailFast(t *testing.T) {
	if _, _, err := BuildTelemetry("/nonexistent-dir/telemetry.jsonl"); err == nil {
		t.Fatal("BuildTelemetry con path no abrible debe devolver error (fail-fast)")
	}
}

// test_postcondition_1_BuildTelemetryPermisos0640: el archivo se crea con
// permisos 0o640 (P1, mismo patrón que TestSEC001_LogFilePermisos0640).
func TestPostcondition1_BuildTelemetryPermisos0640(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	logger, closer, err := BuildTelemetry(path)
	if err != nil {
		t.Fatalf("BuildTelemetry: %v", err)
	}
	defer func() { _ = closer.Close() }()
	logger.Info("test") // asegurar que el archivo existe

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat telemetry file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o640 {
		t.Fatalf("telemetry file permisos = %o, want 640", perm)
	}
}

// test_postcondition_1_BuildTelemetryCloserCierra: el closer cierra sin
// error (P1).
func TestPostcondition1_BuildTelemetryCloserCierra(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	_, closer, err := BuildTelemetry(path)
	if err != nil {
		t.Fatalf("BuildTelemetry: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close(): %v", err)
	}
}
