// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package reloadsig orquesta la fase post-write de mofgw-sync (019-005):
// aplicar el config escrito vía restart systemd, verificar en 3 fases
// (is-active → /healthz → paridad de IDs en /v1/models) y, ante fallo,
// restaurar los bytes previos del config (rollback RESTORE-ONLY) con un
// re-restart verificado.
//
// Pureza (I2): sin os/exec, sin net/http, sin os.* concretos, sin log
// propio ni reloj real — SystemdCtl, Prober, FS y Clock son interfaces
// inyectadas; el binario cmd/mofgw-sync es el único punto de implementación
// real (precedente D9/I1 de 019-004). Imports: internal/config (solo
// ParseForValidation) + stdlib — JAMÁS configsync/catalogmerge/proxy/router.
//
// Única excepción de escritura guardada del epic (I3): el rollback escribe
// config.yaml, pero SOLO los bytes leídos del stash (jamás derivados) con
// el mismo patrón atómico temp+chmod+fsync+rename verificado en 004, y
// jamás toca el sidecar (P14 — auto-cura de 004 intacta).
package reloadsig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ofapsaas/mofgw/internal/config"
)

// Input es la inyección completa de la fase (test-audit §3, HITL Q4).
type Input struct {
	Stash      []byte // bytes previos del config (D9 rollback restore-only)
	ConfigPath string // mismo path resuelto por 004 (P15)
	Applied    bool   // reporte de 004
	Skipped    bool
	Digest     string // sha256 hex del candidato (defensa M-2, D8)
	Unit       string // "mofgw.service" (D4/P15)
	VerifyKey  string // "" = unset → degradación (P9/D7)
	Systemd    SystemdCtl
	Prober     Prober
	FS         FS
	Clock      Clock
	Logger     *slog.Logger // nil → slog.Default() (R3)
}

// SystemdCtl abstrae systemctl (implementación real: exec-backed en el
// binario). El contrato observable de coordinación: QUÉ se llama (Restart/
// IsActive/Available), no CÓMO — la interfaz no expone señales de proceso
// (I5: jamás SIGHUP).
type SystemdCtl interface {
	Available() error
	Restart(unit string) error
	IsActive(unit string) bool
}

// Prober abstrae el HTTP client (implementación real en el binario).
type Prober interface {
	Get(url string, bearer string) (status int, body []byte, err error)
}

// FS: réplica de la interfaz de configsync (004) — inyectada, jamás os.*
// directo dentro del paquete (I2).
type FS interface {
	CreateTemp(dir, pattern string) (*os.File, error)
	Rename(oldpath, newpath string) error
	Stat(name string) (os.FileInfo, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	Chmod(name string, mode os.FileMode) error
}

// Clock abstrae el tiempo: Sleep NO duerme en tests (fake avanza el reloj
// lógico — R1); el polling de ventanas es determinístico.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// Ventanas de settle (D5): poll cada 1s dentro de 30s (espeja
// TimeoutStartSec=30 del unit real y los precedentes install.sh/reduce-
// telemetry).
const (
	settleWindow  = 30 * time.Second
	pollInterval  = time.Second
	healthzSuffix = "/healthz"
	paritySuffix  = "/v1/models"
)

// Run ejecuta la fase de reload (P1-P15) y retorna el exit code.
func Run(in Input) int {
	logger := in.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// P1: con Skipped=true, SOLO la defensa M-2 (D8) — jamás restart.
	if in.Skipped {
		return m2Defense(in, logger)
	}
	if !in.Applied {
		// Defensivo (review F6, I7 hermético): inalcanzable vía binario real
		// (run() garantiza Applied/Skipped post-004 exit 0) pero jamás
		// silencio.
		logger.Warn("mofgw-sync: fase de reload sin Applied ni Skipped — nada que hacer",
			"phase", "degraded", "reason", "input_sin_applied_ni_skipped")
		return 0
	}

	// P10: systemd ausente → fail-loud ANTES de cualquier restart/rollback
	// (D11: el config nuevo es válido y queda en disco; el operador decide).
	if err := in.Systemd.Available(); err != nil {
		logger.Error("mofgw-sync: systemd no disponible — config nuevo escrito y válido en disco, server sin verificar, restart NO intentado; acción sugerida: systemctl --user restart mofgw.service",
			"phase", "degraded", "unit", in.Unit, "error", err)
		return 3
	}

	// D5: addr del config ESCRITO (en disco, post-004). Falla → fail-loud
	// (I7): el server sigue vivo con la config vieja en memoria, pero el
	// estado se declara.
	addrNew, err := serverAddr(in.FS, in.ConfigPath)
	if err != nil {
		logger.Error("mofgw-sync: no se pudo derivar server.addr del config escrito — sin verificación posible",
			"phase", "degraded", "config", in.ConfigPath, "error", err)
		return 3
	}

	// P3: restart.
	logger.Info("mofgw-sync: restart del servicio", "phase", "restart", "unit", in.Unit)
	if err := in.Systemd.Restart(in.Unit); err != nil {
		logger.Error("mofgw-sync: restart falló — rollback restore-only", "phase", "restart", "unit", in.Unit, "error", err)
		return rollback(in, logger, addrNew)
	}

	// P4: F1 is-active (ventana 30s, poll 1s).
	if !settleIsActive(in, logger) {
		return rollback(in, logger, addrNew)
	}
	// P5: F2 healthz (ventana 30s, poll 1s, sin Bearer — público).
	if !settleHealthz(in, logger, addrNew) {
		return rollback(in, logger, addrNew)
	}

	// P9/D7: key unset → degradación fail-soft (F1/F2 únicamente, exit 0).
	if in.VerifyKey == "" {
		logger.Warn("mofgw-sync: verificación de paridad omitida — key no seteada; setear MOFGW_SYNC_VERIFY_KEY para paridad automatizada",
			"phase", "degraded", "reason", "verify_key_unset")
		return 0
	}

	// P6: F3 paridad de IDs (UN request, sin retry — la fase ya garantizó
	// server listo). Fallo → rollback (P7).
	if !parityCheck(in, logger, addrNew) {
		return rollback(in, logger, addrNew)
	}
	logger.Info("mofgw-sync: paridad de IDs verificada post-reload", "phase", "parity", "url", "http://"+addrNew+paritySuffix)
	return 0
}

// m2Defense congela P2/D8: sha256 del config EN DISCO vs Digest del reporte
// de 004 (= digest del candidato). Match → exit 0; mismatch → exit 1 con
// ambos digests en el log, sin restart (fail-loud, no se modifica 004).
func m2Defense(in Input, logger *slog.Logger) int {
	raw, err := in.FS.ReadFile(in.ConfigPath)
	if err != nil {
		logger.Error("mofgw-sync: defensa M-2 no pudo leer el config en disco",
			"phase", "m2_check", "config", in.ConfigPath, "error", err)
		return 1
	}
	disk := digestHex(raw)
	if disk == in.Digest {
		logger.Info("mofgw-sync: skip verificado — config en disco == candidato (sin reload necesario)",
			"phase", "m2_check", "digest_disk", disk, "digest_report", in.Digest)
		return 0
	}
	// D8: sospecha de skip-trap (sidecar-primero de D6 de 004 commiteó el
	// sidecar y el write del body falló) → fail-loud, SIN restart.
	logger.Error("mofgw-sync: skip con config en disco ≠ candidato — sospecha skip-trap sidecar-primero; NO se restartea (fail-loud)",
		"phase", "m2_check", "digest_disk", disk, "digest_report", in.Digest)
	return 1
}

// settleIsActive congela P4: is-active activo dentro de 30s (poll 1s). El
// Clock inyectado avanza 1s por Sleep → ventana determinística.
func settleIsActive(in Input, logger *slog.Logger) bool {
	start := in.Clock.Now()
	for {
		if in.Systemd.IsActive(in.Unit) {
			logger.Info("mofgw-sync: servicio activo post-restart", "phase", "is_active", "unit", in.Unit)
			return true
		}
		if in.Clock.Now().Sub(start) >= settleWindow {
			logger.Error("mofgw-sync: servicio no activo tras la ventana de settle", "phase", "is_active", "unit", in.Unit)
			return false
		}
		in.Clock.Sleep(pollInterval)
	}
}

// settleHealthz congela P5: /healthz 2xx dentro de 30s (poll 1s, request
// timeout 2s — wiring real). Sin Bearer: /healthz es público (P5).
func settleHealthz(in Input, logger *slog.Logger, addr string) bool {
	url := "http://" + addr + healthzSuffix
	start := in.Clock.Now()
	for {
		status, _, err := in.Prober.Get(url, "")
		if err == nil && status >= 200 && status < 300 {
			logger.Info("mofgw-sync: healthz ok post-restart", "phase", "healthz", "url", url, "status", status)
			return true
		}
		if in.Clock.Now().Sub(start) >= settleWindow {
			logger.Error("mofgw-sync: healthz sin 2xx tras la ventana de settle", "phase", "healthz", "url", url)
			return false
		}
		in.Clock.Sleep(pollInterval)
	}
}

// parityCheck congela P6: UN request GET /v1/models con Bearer; HTTP 200 +
// object=="list" + set de data[].id == unión dedup de providers[].models del
// config escrito (comparación por SET — created/extra jamás participan).
func parityCheck(in Input, logger *slog.Logger, addr string) bool {
	url := "http://" + addr + paritySuffix
	status, body, err := in.Prober.Get(url, in.VerifyKey)
	if err != nil {
		logger.Error("mofgw-sync: paridad: request falló", "phase", "parity", "url", url, "error", err)
		return false
	}
	if status != 200 {
		logger.Error("mofgw-sync: paridad: status no-2xx", "phase", "parity", "url", url, "status", status)
		return false
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Error("mofgw-sync: paridad: body no parseable", "phase", "parity", "url", url, "error", err)
		return false
	}
	if resp.Object != "list" {
		logger.Error("mofgw-sync: paridad: object != list", "phase", "parity", "url", url, "object", resp.Object)
		return false
	}
	got := make([]string, 0, len(resp.Data))
	for _, d := range resp.Data {
		got = append(got, d.ID)
	}
	want, err := expectedIDs(in.FS, in.ConfigPath)
	if err != nil {
		logger.Error("mofgw-sync: paridad: derivar set esperado", "phase", "parity", "config", in.ConfigPath, "error", err)
		return false
	}
	if !equalSets(got, want) {
		logger.Error("mofgw-sync: paridad: set de /v1/models ≠ providers[].models del config escrito",
			"phase", "parity", "url", url, "got", strings.Join(got, ","), "want", strings.Join(want, ","))
		return false
	}
	return true
}

// expectedIDs deriva el set esperado del config escrito en disco (D6):
// unión dedup de providers[].models vía ParseForValidation.
func expectedIDs(fs FS, configPath string) ([]string, error) {
	raw, err := fs.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reloadsig: leer config %s: %w", configPath, err)
	}
	cfg, err := config.ParseForValidation(raw)
	if err != nil {
		return nil, fmt.Errorf("reloadsig: ParseForValidation del config escrito: %w", err)
	}
	seen := make(map[string]bool)
	for _, p := range cfg.Providers {
		for _, m := range p.Models {
			seen[m] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// equalSets compara por SET (sorted + dedup) — el orden del catálogo no es
// la cadena de fallback (D6).
func equalSets(a, b []string) bool {
	x := sortedDedup(a)
	y := sortedDedup(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sortedDedup(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	dedup := out[:0]
	for i, v := range out {
		if i == 0 || out[i-1] != v {
			dedup = append(dedup, v)
		}
	}
	return dedup
}

// rollback ejecuta P7-P8 (D9): restaurar los bytes previos del stash por
// write atómico RESTORE-ONLY, re-restart y re-verificar SOLO F1+F2 (liveness
// — los bytes restaurados son la config previa conocida-válida). El
// veredicto del rollback JAMÁS es exit 0 (P8): la config nueva no quedó
// aplicada y el operador debe saberlo.
func rollback(in Input, logger *slog.Logger, addrNew string) int {
	if err := restoreStash(in); err != nil {
		logger.Error("mofgw-sync: rollback agotado — escribir el config previo falló (I/O)", "phase", "rollback_restore", "config", in.ConfigPath, "error", err, "state", "config NUEVO en disco sin verificación posible; server puede quedar caído en loop de Restart=on-failure")
		return 3
	}
	logger.Warn("mofgw-sync: rollback restore-only: config previo restaurado (bytes leídos, jamás derivados)", "phase", "rollback_restore", "config", in.ConfigPath)

	addrOld, err := serverAddr(in.FS, in.ConfigPath)
	if err != nil {
		logger.Error("mofgw-sync: rollback agotado — el config restaurado no parsea", "phase", "rollback_verify", "error", err, "state", "config PREVIO en disco, server sin verificar y posiblemente caído en loop de Restart=on-failure")
		return 3
	}

	logger.Warn("mofgw-sync: rollback: re-restart con la config previa", "phase", "rollback_restart", "unit", in.Unit)
	if err := in.Systemd.Restart(in.Unit); err != nil {
		logger.Error("mofgw-sync: rollback agotado — el re-restart falló", "phase", "rollback_restart", "unit", in.Unit, "error", err, "state", "config previo restaurado en disco, server sin verificar")
		return 3
	}

	// Re-verificación SOLO liveness (F1+F2) — D9.
	start := in.Clock.Now()
	live := false
	for in.Systemd.IsActive(in.Unit) || in.Clock.Now().Sub(start) < settleWindow {
		if in.Systemd.IsActive(in.Unit) {
			live = true
			break
		}
		in.Clock.Sleep(pollInterval)
	}
	if !live {
		logger.Error("mofgw-sync: rollback agotado — el servicio no quedó activo tras el re-restart", "phase", "rollback_verify", "unit", in.Unit, "state", "config previo restaurado, server SIN verificar/posiblemente caído")
		return 3
	}
	url := "http://" + addrOld + healthzSuffix
	hzOK := false
	startHz := in.Clock.Now()
	for in.Clock.Now().Sub(startHz) < settleWindow {
		status, _, err := in.Prober.Get(url, "")
		if err == nil && status >= 200 && status < 300 {
			hzOK = true
			break
		}
		in.Clock.Sleep(pollInterval)
	}
	if !hzOK {
		logger.Error("mofgw-sync: rollback agotado — /healthz no respondió tras el re-restart", "phase", "rollback_verify", "url", url, "state", "config previo restaurado, server activo pero sin healthz")
		return 3
	}
	logger.Warn("mofgw-sync: rollback COMPLETO — config previo restaurado y server verificado; la config nueva NO quedó aplicada (revisar el motivo del fallo antes de re-intentar)",
		"phase", "rollback_verify", "url", url)
	return 3
}

// restoreStash escribe los bytes del stash con el patrón atómico de 004
// (temp en el MISMO directorio + Chmod del mode vigente + fsync + rename —
// P9 de 004). JAMÁS toca el sidecar (P14) ni clients.yaml (I3).
func restoreStash(in Input) error {
	dir := filepath.Dir(in.ConfigPath)
	mode := os.FileMode(0o600)
	if info, err := in.FS.Stat(in.ConfigPath); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := in.FS.CreateTemp(dir, "mofgw-sync-restore-*")
	if err != nil {
		return fmt.Errorf("reloadsig: crear temp en %s (mismo dir que el config): %w", dir, err)
	}
	tmp := f.Name()
	if _, err := f.Write(in.Stash); err != nil {
		_ = f.Close()
		_ = in.FS.Remove(tmp)
		return fmt.Errorf("reloadsig: escribir stash: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = in.FS.Remove(tmp)
		return fmt.Errorf("reloadsig: fsync del temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = in.FS.Remove(tmp)
		return fmt.Errorf("reloadsig: cerrar temp: %w", err)
	}
	if err := in.FS.Chmod(tmp, mode); err != nil {
		_ = in.FS.Remove(tmp)
		return fmt.Errorf("reloadsig: chmod del temp: %w", err)
	}
	if err := in.FS.Rename(tmp, in.ConfigPath); err != nil {
		_ = in.FS.Remove(tmp) // review F3 (019-005): cleanup del temp si el rename falla
		return err
	}
	return nil
}

func digestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// serverAddr deriva server.addr del config en disco (D5) — esquema http
// fijo (el server no sirve TLS). ParseForValidation SIEMPRE materializa el
// default de server.addr (config.go parseCommon → defaults), así que no hay
// rama de fallback: el valor vive solo en internal/config (review F8b —
// acoplamiento deliberado, sin duplicación).
func serverAddr(fs FS, configPath string) (string, error) {
	raw, err := fs.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("reloadsig: leer config %s: %w", configPath, err)
	}
	cfg, err := config.ParseForValidation(raw)
	if err != nil {
		return "", fmt.Errorf("reloadsig: ParseForValidation: %w", err)
	}
	return cfg.Server.Addr, nil
}
