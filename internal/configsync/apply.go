// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizza
//
// SPDX-License-Identifier: GPL-3.0-or-later

package configsync

// Apply: validación pre-commit + escritura atómica + skip byte-idéntico
// (D5/D6 de 019-004). El filesystem es INYECTADO (test-audit §4-A resuelto
// por HITL): el paquete jamás llama os.* para hacer I/O (I1) — el binario
// cmd/mofgw-sync le cablea la implementación os-backed y los tests inyectan
// FS reales o de fallo. Los únicos os.* de este archivo son TIPOS (contrato
// de la interface firmado por el test-writer).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ofapsaas/mofgw/internal/catalogmerge"
	"github.com/ofapsaas/mofgw/internal/config"
)

// FS es el filesystem mínimo inyectado (firmas espejo de los helpers de os,
// definidas por el test-writer en apply_test.go — el implementer DEBE
// respetarlas).
type FS interface {
	CreateTemp(dir, pattern string) (*os.File, error)
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	Chmod(name string, mode os.FileMode) error
}

// Apply ejecuta el flujo D5/D6 completo: Serialize → ParseForValidation del
// candidato (fail-loud: si falla, abort SIN escribir config.yaml NI sidecar,
// P8) → skip byte-idéntico: el digest sha256 hex del candidato se compara
// SOLO contra el contenido del sidecar <config>.sha256 (jamás contra el
// config vigente, P10) → mismatch/ausente: sidecar PRIMERO, config después
// (auto-cura, D6), cada uno con temp EN EL MISMO DIRECTORIO + fsync +
// rename (P9) preservando el mode del config vigente.
func Apply(raw []byte, plan catalogmerge.Plan, configPath string, fs FS) (Report, error) {
	candidate, report, err := Serialize(raw, plan)
	if err != nil {
		return Report{}, err // P8: entrada inválida → sin escribir
	}
	if _, err := config.ParseForValidation(candidate); err != nil {
		return Report{}, fmt.Errorf("configsync: apply: validación del candidato falló — abort sin escribir (P8/D5): %w", err)
	}

	digest := sha256Hex(candidate)
	sidecar := configPath + ".sha256"
	if existing, err := fs.ReadFile(sidecar); err == nil &&
		strings.TrimSpace(string(existing)) == digest {
		// P10: skip byte-idéntico — NI config NI sidecar se reescriben
		// (mtime intacto → 005 no reloadée).
		report.Skipped = true
		return report, nil
	}

	mode := os.FileMode(0o600) // default si el config aún no existe
	if info, err := fs.Stat(configPath); err == nil {
		mode = info.Mode().Perm() // P9: preservar mode del archivo vigente
	}
	if err := writeAtomic(fs, sidecar, []byte(digest), 0o600); err != nil {
		return report, fmt.Errorf("configsync: apply: escribir sidecar (commit sidecar-primero, D6): %w", err)
	}
	if err := writeAtomic(fs, configPath, candidate, mode); err != nil {
		return report, fmt.Errorf("configsync: apply: escribir config.yaml: %w", err)
	}
	report.Applied = true
	return report, nil
}

// writeAtomic escribe data en path con temp EN EL MISMO DIRECTORIO + fsync +
// rename (P9/D6 — patrón writeFileAtomic de modelscache reimplementado
// localmente sobre el FS inyectado). El defer Remove limpia el temp si el
// rename falló (no-op después del rename). El mode se aplica al temp ANTES
// del rename (la escritura fallida nunca deja el config truncado/parcial).
func writeAtomic(fs FS, path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := fs.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("temp en %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = fs.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil { // fsync (P9)
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err := fs.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
