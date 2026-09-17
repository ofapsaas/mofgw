// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-004-atomic-write-validate — Apply (validación
// pre-commit + atomic write + digest skip). Cubren C10-C12 (P8, P9, P10).
//
// Contrato a materializar por GREEN (resuelto por HITL en el sign-off del
// test-audit §4-A: la pureza I1 se respeta con filesystem INYECTADO — el
// paquete nunca llama os.*, el binario cablea el FS real):
//
//	func Apply(raw []byte, plan catalogmerge.Plan, configPath string, fs FS) (Report, error)
//	type FS interface {
//		CreateTemp(dir, pattern string) (*os.File, error)
//		Rename(oldpath, newpath string) error
//		Stat(name string) (os.FileInfo, error)
//		ReadFile(name string) ([]byte, error)
//		WriteFile(name string, data []byte, perm os.FileMode) error
//		Remove(name string) error
//		Chmod(name string, mode os.FileMode) error
//	}
//
// Semántica D5/D6: Serialize → ParseForValidation del candidato → abort SIN
// escribir si falla (P8) → digest skip contra el sidecar <config>.sha256
// comparado SOLO contra el CANDIDATO (P10) → sidecar-primero + temp+rename
// (P9). RED por compilación: Apply/Report/FS no existen.
package configsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/catalogmerge"
)

// realFS: implementación REAL del FS inyectado sobre disco (t.TempDir).
// Los errores de fallo se inyectan con el sistema de archivos real
// (directorio read-only), nunca con mocks de plumbing (AP-14).
type realFS struct{}

func (realFS) CreateTemp(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }
func (realFS) Rename(oldpath, newpath string) error             { return os.Rename(oldpath, newpath) }
func (realFS) Stat(name string) (os.FileInfo, error)            { return os.Stat(name) }
func (realFS) ReadFile(name string) ([]byte, error)             { return os.ReadFile(name) }
func (realFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (realFS) Remove(name string) error                  { return os.Remove(name) }
func (realFS) Chmod(name string, mode os.FileMode) error { return os.Chmod(name, mode) }

func digestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeValidConfig escribe un config.yaml válido y devuelve su path.
func writeValidConfig(t *testing.T, dir string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("escribiendo config: %v", err)
	}
	return path
}

// writeSidecar pre-crea el sidecar de digest (patrón modelsdev_test.go).
func writeSidecar(t *testing.T, configPath, digest string) {
	t.Helper()
	if err := os.WriteFile(configPath+".sha256", []byte(digest), 0o600); err != nil {
		t.Fatalf("escribiendo sidecar: %v", err)
	}
}

func readBytesOrEmpty(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("leyendo %s: %v", path, err)
	}
	return b
}

// applyPlanGoModelChange: plan que produce un candidato DISTINTO del raw
// (models de go-cuenta-1 extendidos) — reutiliza el helper del fixture.
func applyPlanChanged(t *testing.T) catalogmerge.Plan {
	return planWithGoModelChange(t, fixtureRaw(t))
}

// planBreaksCandidate: plan que produce un candidato INVÁLIDO (mecanismo
// aprobado por HITL, test-audit §4-B): Models non-nil VACÍA → candidato con
// models: [] → validate() rechaza ("al menos un model es obligatorio",
// congelado por TestLoadMissingProviderFields caso "models vacio").
func planBreaksCandidate(t *testing.T, raw []byte) catalogmerge.Plan {
	plan := planNoOp(t, raw)
	for i := range plan.Providers {
		if plan.Providers[i].ProviderID == "go-cuenta-1" {
			plan.Providers[i].Models = []string{}
		}
	}
	return plan
}

// ---- B12 (C10, P8+D5) — abort sin escribir, fail-loud ----

// TestApply_AbortWithoutWrite congela P8: si la validación del candidato
// falla (o el raw es YAML inválido) → NO se escribe config.yaml NI sidecar;
// el error sube descriptivo; el config vigente queda byte-intacto.
func TestApply_AbortWithoutWrite(t *testing.T) {
	t.Run("yaml_invalido_en_entrada", func(t *testing.T) {
		dir := t.TempDir()
		raw := []byte("server: [broken")
		configPath := writeValidConfig(t, dir, []byte("# config vigente intacto\nserver:\n  addr: \"127.0.0.1:3369\"\n"))

		_, err := Apply(raw, catalogmerge.Plan{}, configPath, realFS{})
		if err == nil {
			t.Fatal("raw YAML inválido debería dar error (P8)")
		}
		if got := readBytesOrEmpty(t, configPath); !strings.Contains(string(got), "config vigente intacto") {
			t.Errorf("config vigente fue modificado tras el abort (P8): %q", got)
		}
		if side := readBytesOrEmpty(t, configPath+".sha256"); side != nil {
			t.Errorf("sidecar creado tras el abort (P8): %q", side)
		}
	})

	t.Run("candidato_roto_forzado", func(t *testing.T) {
		dir := t.TempDir()
		raw := fixtureRaw(t)
		configPath := writeValidConfig(t, dir, []byte("# config vigente intacto\n"+string(raw)))

		_, err := Apply(raw, planBreaksCandidate(t, raw), configPath, realFS{})
		if err == nil {
			t.Fatal("candidato roto (models: []) debería fallar la validación (P8/D5)")
		}
		if !strings.Contains(string(readBytesOrEmpty(t, configPath)), "config vigente intacto") {
			t.Error("config vigente modificado tras la falla de validación (P8)")
		}
		if side := readBytesOrEmpty(t, configPath+".sha256"); side != nil {
			t.Errorf("sidecar escrito tras la falla de validación (contrato epic: 005 solo corre si 004 validó): %q", side)
		}
	})
}

// ---- B13 (C11, P9+D6) — escritura atómica ----

// TestApply_AtomicWrite congela P9: escritura = temp en el MISMO directorio +
// fsync + rename. Si la escritura del temp (o el rename) falla, el config
// vigente queda byte-intacto; los permisos del config se preservan.
//
// Inyección real del fallo: directorio read-only → el CreateTemp EN ESE
// directorio falla (un impl que creara el temp en otro lado haría pasar el
// test — discriminante de "temp en el mismo directorio"). El caso rename
// fallido comparte el mismo outcome (config intacto) y no es inyectable de
// forma portable sin wrappers: queda para la capa de review.
func TestApply_AtomicWrite(t *testing.T) {
	t.Run("fallo_de_temp_deja_config_intacto", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("como root el chmod read-only no bloquea la escritura — caso no determinístico")
		}
		dir := t.TempDir()
		raw := fixtureRaw(t)
		configPath := writeValidConfig(t, dir, []byte("# config vigente intacto\n"+string(raw)))

		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("chmod read-only: %v", err)
		}

		_, err := Apply(raw, applyPlanChanged(t), configPath, realFS{})
		if err == nil {
			t.Fatal("escritura con directorio read-only debería fallar (temp no creable en el mismo dir)")
		}
		if got := readBytesOrEmpty(t, configPath); !strings.Contains(string(got), "config vigente intacto") {
			t.Error("config vigente truncado/parcial tras fallo del temp (P9: byte-intacto)")
		}
		// NOTA (test-audit §5): P9 solo garantiza el config intacto; el estado
		// del sidecar en el camino de fallo del temp no es una postcondición
		// del spec → no se congela acá (queda para review: un sidecar escrito
		// ANTES de que el body falle crearía un skip-trap de auto-heal).
	})

	t.Run("permisos_del_config_preservados", func(t *testing.T) {
		dir := t.TempDir()
		raw := fixtureRaw(t)
		configPath := writeValidConfig(t, dir, []byte("# config vigente\n"+string(raw)))
		if err := os.Chmod(configPath, 0o640); err != nil {
			t.Fatalf("chmod config: %v", err)
		}

		if _, err := Apply(raw, applyPlanChanged(t), configPath, realFS{}); err != nil {
			t.Fatalf("Apply exitoso: %v", err)
		}
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("mode del config tras el write = %o, want 640 (P9: mode del archivo vigente)", got)
		}
	})
}

// ---- B14 (C12, P10) — skip byte-idéntico ----

// TestApply_DigestSkip congela P10 rama skip: sha256(candidato) == contenido
// del sidecar → NI config NI sidecar se reescriben (mtime intacto); reporte
// Applied=false, Skipped=true con el digest. Incluye el discriminante de
// semántica: sidecar == sha256(candidato) PERO el config en disco fue
// modificado a mano → AUN ASÍ skip (el digest del sidecar solo compara contra
// el CANDIDATO actual, no contra el config vigente). Y el caso sidecar
// ausente → escribe ambos.
func TestApply_DigestSkip(t *testing.T) {
	raw := fixtureRaw(t)
	plan := applyPlanChanged(t)

	// candidato esperado: determinístico (P11) — mismo Serialize que Apply usa.
	candidate, _, err := Serialize(raw, plan)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	wantDigest := digestHex(candidate)

	t.Run("skip_byte_identico_mtime_intacto", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeValidConfig(t, dir, candidate)
		if err := os.WriteFile(configPath+".sha256", []byte(wantDigest), 0o600); err != nil {
			t.Fatalf("sidecar: %v", err)
		}
		past := time.Now().Add(-time.Hour)
		for _, p := range []string{configPath, configPath + ".sha256"} {
			if err := os.Chtimes(p, past, past); err != nil {
				t.Fatalf("Chtimes %s: %v", p, err)
			}
		}

		rep, err := Apply(raw, plan, configPath, realFS{})
		if err != nil {
			t.Fatalf("Apply con skip esperado: %v", err)
		}
		if rep.Skipped != true || rep.Applied != false {
			t.Errorf("reporte = {Applied: %v, Skipped: %v}, want {false, true} (P10)", rep.Applied, rep.Skipped)
		}
		if rep.Digest != wantDigest {
			t.Errorf("Digest = %q, want %q (P10: digest del candidato en el reporte)", rep.Digest, wantDigest)
		}
		statAfter, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("stat post-Apply: %v", err)
		}
		if !statAfter.ModTime().Equal(past) {
			t.Errorf("mtime del config cambió en el skip: %v != %v (P10: skip NO reescribe)", statAfter.ModTime(), past)
		}
		sideAfter, err := os.Stat(configPath + ".sha256")
		if err != nil {
			t.Fatalf("stat sidecar post-Apply: %v", err)
		}
		if !sideAfter.ModTime().Equal(past) {
			t.Errorf("mtime del sidecar cambió en el skip (P10: NI sidecar se reescribe)")
		}
	})

	t.Run("skip_aun_con_config_editado_a_mano", func(t *testing.T) {
		// Discriminante P10: sidecar == sha256(candidato) pero el config vivo
		// difiere del candidato (editado a mano). El digest del sidecar SOLO
		// compara contra el candidato → skip; la edición a mano sobrevive.
		dir := t.TempDir()
		handEdited := append([]byte("# editado a mano — el digest no me ve\n"), candidate...)
		configPath := writeValidConfig(t, dir, handEdited)
		if err := os.WriteFile(configPath+".sha256", []byte(wantDigest), 0o600); err != nil {
			t.Fatalf("sidecar: %v", err)
		}

		rep, err := Apply(raw, plan, configPath, realFS{})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if rep.Skipped != true {
			t.Errorf("Skipped = %v, want true (el sidecar solo compara contra el CANDIDATO, P10)", rep.Skipped)
		}
		if got := readBytesOrEmpty(t, configPath); !bytes.Equal(got, handEdited) {
			t.Error("el config editado a mano fue reescrito a pesar del skip (P10: la comparación es contra el candidato)")
		}
	})

	t.Run("sidecar_ausente_escribe_ambos", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeValidConfig(t, dir, []byte("# config viejo\n"+string(raw)))

		rep, err := Apply(raw, plan, configPath, realFS{})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if rep.Applied != true || rep.Skipped != false {
			t.Errorf("reporte = {Applied: %v, Skipped: %v}, want {true, false} (P10: sidecar ausente → escribe)", rep.Applied, rep.Skipped)
		}
		if got := readBytesOrEmpty(t, configPath); !bytes.Equal(got, candidate) {
			t.Error("config tras Apply != candidato")
		}
		if got := readBytesOrEmpty(t, configPath+".sha256"); string(got) != wantDigest {
			t.Errorf("sidecar = %q, want %q", got, wantDigest)
		}
	})
}

// ---- B15 (C12, P10) — auto-cura por digest mismatch ----

// TestApply_SidecarAutoHeal congela P10 rama mismatch: sidecar con el digest
// de un output PREVIO (≠ candidato actual) → se escribe el candidato nuevo
// (sidecar PRIMERO, config después — el orden es del punto de commit D6; el
// test congela el RESULTADO). Espejo del discriminante de B14: si el config
// en disco YA es igual al candidato pero el sidecar está stale, se escribe
// IGUAL (un impl que comparara contra el config vivo skipearía erróneamente).
func TestApply_SidecarAutoHeal(t *testing.T) {
	raw := fixtureRaw(t)
	planC := applyPlanChanged(t) // candidato C

	// plan B distinto → candidato B distinto (para un sidecar stale realista).
	planB := applyPlanChanged(t)
	for i := range planB.Providers {
		if planB.Providers[i].ProviderID == "go-cuenta-1" {
			planB.Providers[i].Models = []string{"deepseek-flash", "glm-5.2"} // sin el miembro nuevo
		}
	}
	candB, _, err := Serialize(raw, planB)
	if err != nil {
		t.Fatalf("Serialize planB: %v", err)
	}

	candC, _, err := Serialize(raw, planC)
	if err != nil {
		t.Fatalf("Serialize planC: %v", err)
	}
	digestC := digestHex(candC)

	t.Run("config_editado_sidecar_stale_escribe_candidato", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeValidConfig(t, dir, append([]byte("# editado a mano\n"), candB...))
		if err := os.WriteFile(configPath+".sha256", []byte(digestHex(candB)), 0o600); err != nil {
			t.Fatalf("sidecar: %v", err)
		}

		rep, err := Apply(raw, planC, configPath, realFS{})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if rep.Applied != true || rep.Skipped != false {
			t.Errorf("reporte = {Applied: %v, Skipped: %v}, want {true, false} (P10: mismatch → auto-cura)", rep.Applied, rep.Skipped)
		}
		if got := readBytesOrEmpty(t, configPath); !bytes.Equal(got, candC) {
			t.Error("auto-cura no escribió el candidato nuevo (P10)")
		}
		if got := readBytesOrEmpty(t, configPath+".sha256"); string(got) != digestC {
			t.Errorf("sidecar tras auto-cura = %q, want %q", got, digestC)
		}
	})

	t.Run("config_igual_al_candidato_sidecar_stale_igual_escribe", func(t *testing.T) {
		// Discriminante espejo: config en disco == candidato C, sidecar stale.
		// Un impl que comparara el digest del CONFIG VIVO (no del sidecar)
		// skipearía erróneamente — debe escribir (P10).
		dir := t.TempDir()
		configPath := writeValidConfig(t, dir, candC)
		if err := os.WriteFile(configPath+".sha256", []byte(digestHex(candB)), 0o600); err != nil {
			t.Fatalf("sidecar: %v", err)
		}

		rep, err := Apply(raw, planC, configPath, realFS{})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if rep.Applied != true {
			t.Errorf("Applied = %v, want true (sidecar stale aunque el config coincida con el candidato)", rep.Applied)
		}
		if got := readBytesOrEmpty(t, configPath+".sha256"); string(got) != digestC {
			t.Errorf("sidecar tras auto-cura = %q, want %q", got, digestC)
		}
	})
}
