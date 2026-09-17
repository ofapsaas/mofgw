// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-005-reload-signal — nivel binario (B1, B2).
// Cubren C1 (trigger de la fase reload) y C8 (matriz de exit codes 0/1/2/3).
//
// Convención de convivencia 004/005 (test-audit §2.4, ratificada por HITL):
// la fase de reload corre post-004 dentro de run(); los campos NUEVOS de
// runOpts quedan en zero-value y zero-value = fase DESHABILITADA — los
// tests de 004 (main_test.go, byte-intacto) congelan EXACTAMENTE la fase
// 004 sin tocar systemctl ni siquiera en el path de skip.
//
// Contrato de wiring del binario (el implementer lo materializa en GREEN):
//
//	type runOpts struct { // extensión ADITIVA
//		ConfigPath string
//		NoFetch    bool
//		CachePaths cachePaths
//		NoReload   bool        // --no-reload (D12)
//		Reload     reloadHooks // zero-value = fase deshabilitada (P1)
//	}
//	type reloadHooks struct {
//		Systemd   reloadsig.SystemdCtl // nil ⇒ run() jamás invoca reloadsig.Run
//		Prober    reloadsig.Prober
//		Clock     reloadsig.Clock
//		VerifyKey string // "" ⇒ el binario la resuelve con os.LookupEnv (D7)
//		FS        reloadsig.FS // nil ⇒ osFS{} real
//	}
//
// main() SIEMPRE cablea hooks no-nil en producción (el nil-disabled es
// exclusivo de tests — riesgo R3 del audit, verificado en review).
//
// RED por compilación: runOpts no tiene aún NoReload/Reload y las fakes de
// este archivo implementan interfaces de internal/reloadsig (paquete
// inexistente). Los `undefined` SON el RED.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/reloadsig"
)

// ---- fakes de cmd (test-audit §6.2) — contrato observable de coordinación ----

type rCall struct {
	Op   string
	Unit string
}

// rFakeSystemd: scriptable, implementación de reloadsig.SystemdCtl.
type rFakeSystemd struct {
	AvailableErr error
	RestartErr   error
	IsActives    []bool // script por llamada; al agotarse repite el último

	Calls []rCall
	hi    int
}

func (f *rFakeSystemd) record(op, unit string) {
	f.Calls = append(f.Calls, rCall{op, unit})
}

func (f *rFakeSystemd) Available() error {
	f.record("available", "")
	return f.AvailableErr
}

func (f *rFakeSystemd) Restart(unit string) error {
	f.record("restart", unit)
	return f.RestartErr
}

func (f *rFakeSystemd) IsActive(unit string) bool {
	f.record("is-active", unit)
	if len(f.IsActives) == 0 {
		return false
	}
	if f.hi >= len(f.IsActives) {
		f.hi = len(f.IsActives) - 1
	}
	v := f.IsActives[f.hi]
	f.hi++
	return v
}

func (f *rFakeSystemd) count(op string) int {
	n := 0
	for _, c := range f.Calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

func (f *rFakeSystemd) restartCalls() []string {
	var units []string
	for _, c := range f.Calls {
		if c.Op == "restart" {
			units = append(units, c.Unit)
		}
	}
	return units
}

// rProberResp / rFakeProber: implementación de reloadsig.Prober (shapes
// re-declarados en package main — los del paquete reloadsig no son
// importables desde un test de cmd).
type rProberResp struct {
	status int
	body   []byte
	err    error
}

type rFakeProber struct {
	Healthz []rProberResp
	Models  rProberResp

	Calls []struct {
		URL    string
		Bearer string
	}
	hi int
}

func (f *rFakeProber) Get(url, bearer string) (int, []byte, error) {
	f.Calls = append(f.Calls, struct {
		URL    string
		Bearer string
	}{url, bearer})
	var r rProberResp
	switch {
	case strings.HasSuffix(url, "/healthz"):
		if len(f.Healthz) == 0 {
			return 200, []byte("ok"), nil
		}
		if f.hi >= len(f.Healthz) {
			f.hi = len(f.Healthz) - 1
		}
		r = f.Healthz[f.hi]
		f.hi++
	case strings.HasSuffix(url, "/v1/models"):
		r = f.Models
	default:
		return 0, nil, fmt.Errorf("rFakeProber: url inesperada %q", url)
	}
	if r.err != nil {
		return 0, nil, r.err
	}
	return r.status, r.body, nil
}

func (f *rFakeProber) countPath(suffix string) int {
	n := 0
	for _, c := range f.Calls {
		if strings.HasSuffix(c.URL, suffix) {
			n++
		}
	}
	return n
}

// rFakeClock: Sleep avanza el reloj lógico, JAMÁS duerme (R1 del audit).
type rFakeClock struct {
	base time.Time
}

func (c *rFakeClock) Now() time.Time { return c.base }
func (c *rFakeClock) Sleep(d time.Duration) {
	c.base = c.base.Add(d)
}

// rTamperFS: FS que devuelve bytes TAMPERADOS al leer el config — inyecta
// el mismatch de la defensa M-2 a nivel binario (B2d). Inyección real vía
// el contrato de interfaz (precedente del FS de 004).
type rTamperFS struct {
	reloadsig.FS
	configPath string
}

func (f rTamperFS) ReadFile(name string) ([]byte, error) {
	b, err := f.FS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if name == f.configPath {
		return append([]byte("# tamperado por rTamperFS\n"), b...), nil
	}
	return b, nil
}

// rModelsBody: body /v1/models list-shape real con created variable y
// campo extra (no participan del veredicto, D6).
func rModelsBody(t *testing.T, ids []string) []byte {
	t.Helper()
	type entry struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Extra   string `json:"campo_extra"`
	}
	data := make([]entry, 0, len(ids))
	for i, id := range ids {
		data = append(data, entry{ID: id, Created: int64(1000 + i)})
	}
	body, err := json.Marshal(map[string]any{"object": "list", "data": data})
	if err != nil {
		t.Fatalf("fake body: %v", err)
	}
	return body
}

// rHappyHooks: hooks armados con fakes OK (healthz 2xx a la primera;
// paridad del set de syncConfigTemplate; VerifyKey vacío = degradación).
func rHappyHooks(t *testing.T) (reloadHooks, *rFakeSystemd, *rFakeProber) {
	t.Helper()
	sys := &rFakeSystemd{IsActives: []bool{true}}
	p := &rFakeProber{
		Healthz: []rProberResp{{status: 200, body: []byte("ok")}},
		Models:  rProberResp{status: 200, body: rModelsBody(t, []string{"glm-5.2", "minimax-m3"})},
	}
	return reloadHooks{
		Systemd: sys,
		Prober:  p,
		Clock:   &rFakeClock{base: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)},
	}, sys, p
}

// setupSyncDir: dir con config + caches seedeadas (la fase 004 corre real).
func setupSyncDir(t *testing.T, template string) (configPath, mdPath, zenPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = writeSyncConfig(t, dir, template)
	mdPath = filepath.Join(dir, "cache", "modelsdev.json")
	zenPath = filepath.Join(dir, "cache", "zen.json")
	seedModelsDevCache(t, mdPath)
	seedZenCache(t, zenPath)
	return configPath, mdPath, zenPath
}

// ---- B1 (C1, P1+D3+D12+P15) — trigger de la fase de reload ----

// TestRun_ReloadTrigger congela P1 a nivel binario: restart ejecutado iff
// Applied && !--no-reload; Skipped → cero llamadas a SystemdCtl (solo M-2);
// 004 falla → cero llamadas; unit constante "mofgw.service" (P15).
func TestRun_ReloadTrigger(t *testing.T) {
	envKeySet(t) // MOFGW_ZEN_KEY para config.Parse del vigente (fase 004)

	t.Run("applied_hooks_restart_1_vez_exit_0", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		_, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("applied + reload feliz = %d, want 0", code)
		}
		restarts := sys.restartCalls()
		if len(restarts) != 1 || restarts[0] != "mofgw.service" {
			t.Errorf("restarts = %v, want exactamente [mofgw.service] (D4/P15)", restarts)
		}
		if got := p.countPath("/healthz"); got == 0 {
			t.Error("healthz no verificado tras el restart (P5)")
		}
	})

	t.Run("skipped_hooks_cero_restart_m2_corre", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		logger1 := captureLoggerOnly(t)
		// 1ra corrida: applied (hooks nil → sin reload). 2da: skip → M-2.
		if code := run(runOpts{ConfigPath: configPath, NoFetch: true, CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath}}, logger1); code != 0 {
			t.Fatalf("1ra corrida (applied, hooks nil) = %d, want 0", code)
		}
		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("2da corrida (skip + M-2 match) = %d, want 0 (P2)", code)
		}
		if len(sys.Calls) != 0 {
			t.Errorf("SystemdCtl llamado en skip (P1/P2: jamás, ni siquiera is-active): %+v", sys.Calls)
		}
		if !strings.Contains(buf.String(), "m2_check") {
			t.Errorf("log sin phase=m2_check (P2: el chequeo corre en skip): %s", buf.String())
		}
		if got := p.countPath("/healthz"); got != 0 {
			t.Errorf("Prober en skip (P2): %d", got)
		}
	})

	t.Run("applied_no_reload_cero_llamadas", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		_, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			NoReload:   true,
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("--no-reload + applied = %d, want 0 (P1: exit de 004)", code)
		}
		if len(sys.Calls) != 0 || len(p.Calls) != 0 {
			t.Errorf("llamadas con --no-reload (P1/D12: NINGUNA fase corre): sys=%+v prober=%+v", sys.Calls, p.Calls)
		}
	})

	t.Run("config_invalido_cero_llamadas_exit_1", func(t *testing.T) {
		dir := t.TempDir()
		badPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(badPath, []byte("server: [broken"), 0o600); err != nil {
			t.Fatalf("config inválido: %v", err)
		}
		hooks, sys, p := rHappyHooks(t)
		_, logger := captureLogs()
		code := run(runOpts{ConfigPath: badPath, Reload: hooks}, logger)
		if code != 1 {
			t.Fatalf("config inválido con hooks = %d, want 1 (P1: reload solo si 004 exit 0)", code)
		}
		if len(sys.Calls) != 0 || len(p.Calls) != 0 {
			t.Errorf("llamadas con 004 fallando: sys=%+v prober=%+v", sys.Calls, p.Calls)
		}
	})
}

// ---- B2 (C8, P11) — matriz de exit codes 0/1/2/3 ----

// TestRun_ExitCodesReload extiende TestRun_ExitCodes (004) con la fase de
// reload: congela el mapa completo sobre combinaciones reales (004 corre
// con fixtures reales; reload con hooks inyectados).
func TestRun_ExitCodesReload(t *testing.T) {
	envKeySet(t)

	t.Run("applied_reload_ok_key_unset_exit_0_con_warning", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (P9: degradación sin key)", code)
		}
		if !strings.Contains(buf.String(), "verify_key") {
			t.Errorf("log sin warning de degradación (P9): %s", buf.String())
		}
		_ = sys
		_ = p
	})

	t.Run("applied_reload_ok_key_set_paridad_ok_exit_0", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, _, p := rHappyHooks(t)
		t.Setenv("MOFGW_SYNC_VERIFY_KEY", "verify-k3y") // paridad automatizada (D7)
		_, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (P11: applied + reload + paridad OK)", code)
		}
		// la key del env llegó como Bearer del /v1/models (D7) — el binario
		// la resuelve cuando hooks.VerifyKey queda "".
		for _, c := range p.Calls {
			if strings.HasSuffix(c.URL, "/v1/models") {
				if c.Bearer != "verify-k3y" {
					t.Errorf("bearer de paridad = %q, want el valor de MOFGW_SYNC_VERIFY_KEY (D7)", c.Bearer)
				}
				if !strings.Contains(c.URL, "127.0.0.1:3369") {
					t.Errorf("URL de paridad = %q, want derivada del config escrito (D5: addr default del template)", c.URL)
				}
			}
		}
	})

	t.Run("skip_m2_match_exit_0", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		if code := run(runOpts{ConfigPath: configPath, NoFetch: true, CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath}}, captureLoggerOnly(t)); code != 0 {
			t.Fatalf("1ra corrida = %d, want 0", code)
		}
		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 0 {
			t.Fatalf("skip + M-2 match = %d, want 0 (P2)", code)
		}
		if len(sys.Calls) != 0 {
			t.Errorf("SystemdCtl en skip (P2): %+v", sys.Calls)
		}
		if got := p.countPath("/healthz"); got != 0 {
			t.Errorf("Prober en skip (P2): %d", got)
		}
		if !strings.Contains(buf.String(), "m2_check") {
			t.Errorf("log sin phase=m2_check: %s", buf.String())
		}
	})

	t.Run("skip_m2_mismatch_exit_1_sin_restart", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		if code := run(runOpts{ConfigPath: configPath, NoFetch: true, CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath}}, captureLoggerOnly(t)); code != 0 {
			t.Fatalf("1ra corrida = %d, want 0", code)
		}
		hooks, sys, p := rHappyHooks(t)
		hooks.FS = rTamperFS{configPath: configPath} // M-2 ve bytes tamperados
		_, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 1 {
			t.Fatalf("skip + M-2 mismatch = %d, want 1 (P2/D8)", code)
		}
		if len(sys.Calls) != 0 {
			t.Errorf("SystemdCtl llamado tras mismatch (P2: jamás restart): %+v", sys.Calls)
		}
	})

	t.Run("no_reload_applied_y_skipped_exit_0", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		hooks, sys, p := rHappyHooks(t)
		opts := runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			NoReload:   true,
			Reload:     hooks,
		}
		if code := run(opts, captureLoggerOnly(t)); code != 0 {
			t.Fatalf("--no-reload + applied = %d, want 0", code)
		}
		if code := run(opts, captureLoggerOnly(t)); code != 0 {
			t.Fatalf("--no-reload + skipped = %d, want 0 (P1: exit de 004)", code)
		}
		if len(sys.Calls) != 0 || len(p.Calls) != 0 {
			t.Errorf("llamadas con --no-reload (P1/D12): sys=%+v prober=%+v", sys.Calls, p.Calls)
		}
	})

	t.Run("no_reload_malformado_parseArgs_error", func(t *testing.T) {
		if _, err := parseArgs([]string{"--no-reload=x"}); err == nil {
			t.Fatal("parseArgs(--no-reload=x) debería dar error (P11: exit 2 vía main)")
		}
	})

	t.Run("applied_restart_error_rollback_exit_3", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		pre := readBytes(t, configPath)
		hooks, _, _ := rHappyHooks(t)
		hooks.Systemd = &rFakeSystemd{RestartErr: fmt.Errorf("exit 5")} // restart falla
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, captureLoggerOnly(t))
		if code != 3 {
			t.Fatalf("restart cmd error con rollback = %d, want 3 (P11: jamás 0)", code)
		}
		if got := readBytes(t, configPath); !bytes.Equal(got, pre) {
			t.Errorf("config no restaurado a los bytes previos (P7/D9 vía binario)\npre:  %q\npost: %q", pre, got)
		}
	})

	t.Run("applied_systemd_unavailable_exit_3", func(t *testing.T) {
		configPath, mdPath, zenPath := setupSyncDir(t, syncConfigTemplate)
		pre := readBytes(t, configPath)
		hooks, _, _ := rHappyHooks(t)
		hooks.Systemd = &rFakeSystemd{AvailableErr: fmt.Errorf("no user session")}
		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
			Reload:     hooks,
		}, logger)
		if code != 3 {
			t.Fatalf("systemd ausente = %d, want 3 (D11)", code)
		}
		if got := readBytes(t, configPath); bytes.Equal(got, pre) {
			t.Error("config sigue en bytes previos — P11: el estado NUEVO debe permanecer en disco (cero rollback)")
		}
		if !strings.Contains(buf.String(), "systemd") {
			t.Errorf("log sin descripción del estado: %s", buf.String())
		}
	})

	t.Run("exit_1_de_004_con_hooks_cero_llamadas", func(t *testing.T) {
		dir := t.TempDir()
		badPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(badPath, []byte("server: [broken"), 0o600); err != nil {
			t.Fatalf("config: %v", err)
		}
		hooks, sys, p := rHappyHooks(t)
		code := run(runOpts{ConfigPath: badPath, Reload: hooks}, captureLoggerOnly(t))
		if code != 1 {
			t.Fatalf("004 inválido con hooks = %d, want 1", code)
		}
		if len(sys.Calls) != 0 || len(p.Calls) != 0 {
			t.Errorf("llamadas con 004 fallido (P1): sys=%+v prober=%+v", sys.Calls, p.Calls)
		}
	})
}
