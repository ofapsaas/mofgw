// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// ---- SEC-001 P4: log file con permisos 0640 ----

// TestSEC001_LogFilePermisos0640: el archivo de log se crea con permisos
// 0640 (no 0644 — legible por cualquier usuario local). RED: hoy usa
// 0644 (logging.go:115). GREEN: 0640.
func TestSEC001_LogFilePermisos0640(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "mofgw.log")

	logger, closer, err := Build(slog.LevelDebug, logFile, os.Stdout)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if logger == nil {
		t.Fatal("logger nil")
	}
	defer closer.Close()

	// escribir algo para asegurar que el archivo existe
	logger.Info("test")

	fi, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	perm := fi.Mode().Perm()
	if perm != 0o640 {
		t.Fatalf("log file permisos = %o, want 640 (SEC-001 P4)", perm)
	}
}
