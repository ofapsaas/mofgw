// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-006-systemd-timer — golden de las unidades
// systemd commiteadas (spec P1/P2, C1-C3) + canary opt-in del entorno real
// (C10).
//
// Contrato lockeado por el spec (D1.3): los unit files son la ÚNICA fuente
// de verdad y viven commiteados en scripts/systemd/; estos tests los leen
// del repo root (resuelto subiendo hasta go.mod — go test fija CWD al dir
// del paquete) y validan CONTENIDO: campos positivos exactos + negativos
// explícitos. RED por comportamiento: os.ReadFile falla mientras los
// archivos no existan, o las aserciones de contenido fallan si están
// incompletos (nunca un RED trivial).
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot sube desde el CWD del paquete hasta el dir que contiene go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró go.mod subiendo desde el paquete")
		}
		dir = parent
	}
}

// readUnit lee scripts/systemd/<name> desde el repo root (RED: archivo
// ausente → error).
func readUnit(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "scripts", "systemd", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leyendo unit %s: %v (RED: el archivo debe existir commiteado)", path, err)
	}
	return string(b)
}

// assertHas verifica que CADA fragmento aparece en el contenido (positivos).
func assertHas(t *testing.T, content string, fragments []string, ctx string) {
	t.Helper()
	for _, f := range fragments {
		if !strings.Contains(content, f) {
			t.Errorf("%s: falta el fragmento %q", ctx, f)
		}
	}
}

// assertNotHas: negativos — NINGÚN fragmento puede aparecer (spec C1/C2).
func assertNotHas(t *testing.T, content string, fragments []string, ctx string) {
	t.Helper()
	for _, f := range fragments {
		if strings.Contains(content, f) {
			t.Errorf("%s: fragmento PROHIBIDO presente: %q", ctx, f)
		}
	}
}

// ---- B1 (C1, P1) — contenido exacto de mofgw-sync.service ----

// TestSyncServiceUnit congela P1: Type=oneshot, ExecStart del bin sync,
// EnvironmentFile tolerante con prefix -, journal; SIN Restart, SIN
// [Install], SIN TimeoutStartSec, SIN RemainAfterExit (I1/D3).
func TestSyncServiceUnit(t *testing.T) {
	content := readUnit(t, "mofgw-sync.service")

	assertHas(t, content, []string{
		"[Unit]",
		"Description=mofgw-sync - sincronización del catálogo de providers de mofgw (epic 019)",
		"[Service]",
		"Type=oneshot",
		"ExecStart=%h/.local/bin/mofgw-sync",
		"EnvironmentFile=-%h/.config/mofgw/env",
		"StandardOutput=journal",
		"StandardError=journal",
	}, "mofgw-sync.service")
	assertNotHas(t, content, []string{
		"Restart=",
		"[Install]",
		"TimeoutStartSec",
		"RemainAfterExit",
		"WantedBy",
	}, "mofgw-sync.service (negativos I1/D3: el timer es el único scheduler)")
}

// ---- B2 (C2) — contenido exacto de mofgw-sync.timer ----

// TestSyncTimerUnit congela P2: OnBootSec=10min + OnUnitActiveSec=60min +
// WantedBy=timers.target; sin OnCalendar/Persistent/AccuracySec/Unit= (D2).
func TestSyncTimerUnit(t *testing.T) {
	content := readUnit(t, "mofgw-sync.timer")

	assertHas(t, content, []string{
		"[Unit]",
		"Description=mofgw-sync timer - sincronización del catálogo cada 60 minutos",
		"[Timer]",
		"OnBootSec=10min",
		"OnUnitActiveSec=60min",
		"[Install]",
		"WantedBy=timers.target",
	}, "mofgw-sync.timer")

	assertNotHas(t, content, []string{
		"OnCalendar=",
		"Persistent=",
		"AccuracySec=",
		"Unit=",
	}, "mofgw-sync.timer")
}

// ---- B3 (C3) — emparejamiento por nombre + descripciones ----

// TestSyncUnitPairing congela C3: el emparejamiento timer→service se DERIVA
// por nombre de archivo (review F3: sin literales duplicados). Nota honesta:
// la verdadera garantía del emparejamiento es B2 (ausencia de `Unit=` ⇒
// systemd deriva mofgw-sync.timer → mofgw-sync.service por nombre); acá se
// congela que AMBOS archivos existen con el mismo stem (derivado de cada
// nombre, jamás literales repetidos) + Description en cada uno.
func TestSyncUnitPairing(t *testing.T) {
	service, timer := pairNames(t)
	assertHas(t, readUnit(t, service), []string{"Description="}, "service Description")
	assertHas(t, readUnit(t, timer), []string{"Description="}, "timer Description")
}

// pairNames localiza en scripts/systemd/ el par timer/service con el mismo
// stem (review F3: el emparejamiento se deriva del FS, no de strings
// hardcodeadas en el test).
func pairNames(t *testing.T) (service, timer string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "scripts", "systemd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("leyendo %s: %v", dir, err)
	}
	byStem := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if cut, ok := cutSuffix(name, ".service"); ok {
			byStem[cut] = append(byStem[cut], name)
		}
		if cut, ok := cutSuffix(name, ".timer"); ok {
			byStem[cut] = append(byStem[cut], name)
		}
	}
	names, ok := byStem["mofgw-sync"]
	if !ok || len(names) != 2 {
		t.Fatalf("par timer/service para el stem mofgw-sync ausente o incompleto en %s: %v", dir, byStem)
	}
	for _, n := range names {
		if len(n) >= 8 && n[len(n)-8:] == ".service" {
			service = n
		} else {
			timer = n
		}
	}
	return service, timer
}

func cutSuffix(s, suffix string) (string, bool) {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// ---- B4 (C10) — canary opt-in del entorno real ----

// TestUnits_LiveEnvironment congela C10: TRIPLE GUARDIA (MOFGW_SYNC_LIVE=1 +
// systemctl disponible + timer instalado); asserts de agenda real: is-enabled
// == enabled y list-timers muestra el timer con próximo vencimiento. Skip
// silencioso en CI/sandbox (jamás depende del deploy real para pasar).
// Autocontenido (os/exec directo): no requiere helpers de main.go.
func TestUnits_LiveEnvironment(t *testing.T) {
	if os.Getenv("MOFGW_SYNC_LIVE") != "1" {
		t.Skip("opt-in explícito requerido (MOFGW_SYNC_LIVE=1) — activa el timer real")
	}
	out, err := exec.Command("systemctl", "--user", "is-enabled", "mofgw-sync.timer").Output()
	if err != nil || strings.TrimSpace(string(out)) != "enabled" {
		t.Skipf("timer no instalado/enabled en este host: is-enabled=%q err=%v", out, err)
	}
	// Agenda: list-timers --no-legend muestra NEXT (próximo vencimiento).
	list, err := exec.Command("systemctl", "--user", "list-timers", "mofgw-sync.timer", "--no-legend").Output()
	if err != nil || strings.TrimSpace(string(list)) == "" {
		t.Fatalf("timer no visible en list-timers: %q err=%v", list, err)
	}
}
