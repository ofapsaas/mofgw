// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// B17 (C14, D4/D5/D7) — canary opt-in del entorno REAL.
//
// TRIPLE GUARDIA (test-audit §4-Q2, ratificada por HITL): este test
// REINICIA el servicio mofgw real — opt-in explícito OBLIGATORIO:
//
//  1. MOFGW_SYNC_LIVE=1   (sin esta var, SIEMPRE skip)
//  2. MOFGW_SYNC_VERIFY_KEY seteada (key de cliente registrada en clients.yaml)
//  3. systemctl --user is-active mofgw.service ejecutable (deploy real)
//
// En CI/sandbox: skip silencioso (jamás depende de /home/ofap — patrón
// P16/D8 de 004). Usa las implementaciones REALES cableadas por el binario
// (tipos en main.go, Q3 ratificado): execSystemdCtl, httpProber, realClock,
// osFS — RED por compilación hasta que GREEN los materialice.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ofapsaas/mofgw/internal/reloadsig"
)

func TestReloadSig_LiveEnvironment(t *testing.T) {
	if os.Getenv("MOFGW_SYNC_LIVE") != "1" {
		t.Skip("opt-in explícito requerido (MOFGW_SYNC_LIVE=1) — reinicia el servicio real")
	}
	key := os.Getenv("MOFGW_SYNC_VERIFY_KEY")
	if key == "" {
		t.Skip("MOFGW_SYNC_VERIFY_KEY no seteada — canary requiere key de cliente (D7)")
	}
	if out, err := exec.Command("systemctl", "--user", "is-active", "mofgw.service").CombinedOutput(); err != nil {
		t.Skipf("servicio mofgw.service no activo/ausente en este host: %v (%s)", err, out)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("sin home detectable: %v", err)
	}
	configPath := filepath.Join(home, ".config", "mofgw", "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Skipf("config vivo ausente en %s: %v", configPath, err)
	}

	// Ciclo real: Applied=true (004 ya escribió el config vigente), stash =
	// los bytes leídos (rollback restore-only del entorno real, D9).
	code := reloadsig.Run(reloadsig.Input{
		Stash:      raw,
		ConfigPath: configPath,
		Applied:    true,
		Unit:       "mofgw.service",
		VerifyKey:  key,
		Systemd:    execSystemdCtl{},
		Prober:     httpProber{},
		FS:         osFS{},
		Clock:      realClock{},
		Logger:     nil, // slog.Default() (R3)
	})
	// El canary no fuerza un veredicto único: exit 0 = reload verificado;
	// exit 3 = rollback ejecutado (el entorno real puede no estar listo) —
	// ambos son contratos válidos; jamás 1/2 en este path (P11).
	if code != 0 && code != 3 {
		t.Errorf("canary live = %d, want 0 (reload OK) o 3 (rollback del entorno real, D10)", code)
	}
}
