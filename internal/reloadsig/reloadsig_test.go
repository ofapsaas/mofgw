// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-005-reload-signal — paquete reloadsig (B3-B16).
// Cubren C2-C13 (P2, P3-P10, P12-P14, D6).
//
// Contrato a materializar por GREEN (firmas del test-audit §3, Q1-Q4
// ratificadas por HITL; el implementer DEBE respetarlas):
//
//	func Run(in Input) int
//	type Input struct {
//		Stash      []byte       // bytes previos del config (D9 rollback restore-only)
//		ConfigPath string       // mismo path resuelto por 004 (P15)
//		Applied    bool         // reporte de 004
//		Skipped    bool
//		Digest     string       // sha256 hex del candidato (defensa M-2, D8)
//		Unit       string       // "mofgw.service" (D4/P15)
//		VerifyKey  string       // "" = unset → degradación (P9/D7)
//		Systemd    SystemdCtl
//		Prober     Prober
//		FS         FS
//		Clock      Clock
//		Logger     *slog.Logger // nil → slog.Default() (R3)
//	}
//	type SystemdCtl interface {
//		Available() error            // P10: precede a P3
//		Restart(unit string) error   // timeout de comando 30s (wiring real)
//		IsActive(unit string) bool   // F1
//	}
//	type Prober interface {
//		Get(url string, bearer string) (status int, body []byte, err error)
//	}
//	type FS interface { CreateTemp(dir, pattern string) (*os.File, error); Rename(o, n string) error; Stat(name string) (os.FileInfo, error); ReadFile(name string) ([]byte, error); WriteFile(name string, data []byte, perm os.FileMode) error; Remove(name string) error; Chmod(name string, mode os.FileMode) error }
//	type Clock interface { Now() time.Time; Sleep(d time.Duration) }
//
// RED por compilación: el paquete no tiene archivos de implementación
// todavía (precedente 001-004). Los `undefined` SON el RED.
//
// Desviación documentada del test-audit §6.3 (decisión de RED): el stash de
// B6/B9/B11 es una VARIANTE VÁLIDA del config fixture (addr 5555) en vez de
// un blob no-YAML — la re-verificación F2 del rollback necesita derivar
// server.addr del config restaurado (P8/D9), así que el stash debe parsear;
// el discriminante "bytes leídos jamás derivados" (P7) queda congelado por
// byte-equality contra la variante (un impl que re-serializara produce otro
// YAML distinto y falla).
package reloadsig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- fixtures (test-audit §6.3) ----

// reloadConfigFixture: config escrito post-004, válido para
// ParseForValidation SIN env vars (P13a). addr NO-default 4444 —
// discriminante de derivación (D5/D6). Providers http + subprocess + dedup
// cross-provider (C12).
const reloadConfigFixture = `
server:
  addr: "127.0.0.1:4444"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: zen-acc
    base_url: "https://example.invalid/v1"
    api_key_env: "MOFGW_RELOAD_TEST_KEY"
    models: ["minimax-m3", "glm-5.2"]
    max_tokens: 8192
  - id: sub-acc
    type: subprocess
    backend: claude
    command: "claude"
    session_dir: "/tmp/mofgw-sessions"
    backend_flags: ["--dangerously-skip-permissions"]
    clients: ["me"]
    models: ["claude-sonnet-4"]
  - id: dup-acc
    base_url: "https://example.invalid/v1"
    api_key_env: "MOFGW_RELOAD_TEST_KEY"
    models: ["glm-5.2", "minimax-m3"]
`

// reloadConfigStash: variante VÁLIDA del fixture (addr 5555) — los bytes
// previos que Run restaura en el rollback (D9: bytes leídos, jamás
// derivados). El byte-equality contra ESTA variante es el discriminante:
// cualquier re-serialización del fixture produciría otros bytes.
const reloadConfigStash = `
server:
  addr: "127.0.0.1:5555"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: zen-acc
    base_url: "https://example.invalid/v1"
    api_key_env: "MOFGW_RELOAD_TEST_KEY"
    models: ["minimax-m3", "glm-5.2"]
    max_tokens: 8192
`

// expectedModelSet: unión DEDUP de providers[].models del fixture (D6/C12).
var expectedModelSet = []string{"claude-sonnet-4", "glm-5.2", "minimax-m3"}

const unitConst = "mofgw.service" // D4/P15

const verifyKeyTest = "s3cr3t-k3y" // B15: valor que JAMÁS puede salir en logs

func digestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeBytes escribe bytes en el path del config dentro de dir.
func writeBytes(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("escribiendo config: %v", err)
	}
	return path
}

// ---- fakes (test-audit §6.1) — contrato observable: QUÉ se llamó, no CÓMO ----

type fakeCall struct {
	Op   string
	Unit string
}

// fakeSystemd: scriptable.
type fakeSystemd struct {
	AvailableErr error
	RestartErr   error
	IsActives    []bool // script por llamada; al agotarse repite el último

	Calls []fakeCall
	hi    int // índice de consumo del script is-active
}

func (f *fakeSystemd) record(op, unit string) {
	f.Calls = append(f.Calls, fakeCall{op, unit})
}

func (f *fakeSystemd) Available() error {
	f.record("available", "")
	return f.AvailableErr
}

func (f *fakeSystemd) Restart(unit string) error {
	f.record("restart", unit)
	return f.RestartErr
}

func (f *fakeSystemd) IsActive(unit string) bool {
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

func (f *fakeSystemd) count(op string) int {
	n := 0
	for _, c := range f.Calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

func (f *fakeSystemd) restartCalls() []string {
	var units []string
	for _, c := range f.Calls {
		if c.Op == "restart" {
			units = append(units, c.Unit)
		}
	}
	return units
}

// proberResp: script por llamada.
type proberResp struct {
	status int
	body   []byte
	err    error
}

// fakeProber: enruta por sufijo de URL (/healthz consume el script Healthz;
// /v1/models devuelve Models — F3 es UN solo request, P6).
type fakeProber struct {
	Healthz []proberResp
	Models  proberResp

	Calls []struct {
		URL    string
		Bearer string
	}
	hi int
}

func (f *fakeProber) Get(url, bearer string) (int, []byte, error) {
	f.Calls = append(f.Calls, struct {
		URL    string
		Bearer string
	}{url, bearer})
	var r proberResp
	switch {
	case len(url) >= 8 && url[len(url)-8:] == "/healthz":
		if len(f.Healthz) == 0 {
			return 200, []byte("ok"), nil
		}
		if f.hi >= len(f.Healthz) {
			f.hi = len(f.Healthz) - 1
		}
		r = f.Healthz[f.hi]
		f.hi++
	case len(url) >= 10 && url[len(url)-10:] == "/v1/models":
		r = f.Models
	default:
		return 0, nil, fmt.Errorf("fakeProber: url inesperada %q", url)
	}
	if r.err != nil {
		return 0, nil, r.err
	}
	return r.status, r.body, nil
}

func (f *fakeProber) countPath(suffix string) int {
	n := 0
	for _, c := range f.Calls {
		if len(c.URL) >= len(suffix) && c.URL[len(c.URL)-len(suffix):] == suffix {
			n++
		}
	}
	return n
}

// fakeClock: Sleep NO duerme — avanza el reloj lógico (R1: un impl con
// time.Sleep real cuelga el test de ventana 30s; señal inequívoca del
// discriminante R4).
type fakeClock struct {
	base    time.Time
	Sleeps  int
	LastDur time.Duration
}

func (c *fakeClock) Now() time.Time { return c.base }
func (c *fakeClock) Sleep(d time.Duration) {
	c.Sleeps++
	c.LastDur = d
	c.base = c.base.Add(d)
}

// realFS: réplica exacta de apply_test.go:45-55 de configsync (004).
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

// spyFS: wrapper contable para congelar read-only / escrituras (B4/B13).
type spyFS struct {
	FS                                      // embebido: realFS
	writes, renames, removes, chmods, temps int
}

func (s *spyFS) CreateTemp(dir, pattern string) (*os.File, error) {
	s.temps++
	return s.FS.CreateTemp(dir, pattern)
}
func (s *spyFS) Rename(oldpath, newpath string) error {
	s.renames++
	return s.FS.Rename(oldpath, newpath)
}
func (s *spyFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	s.writes++
	return s.FS.WriteFile(name, data, perm)
}
func (s *spyFS) Remove(name string) error {
	s.removes++
	return s.FS.Remove(name)
}
func (s *spyFS) Chmod(name string, mode os.FileMode) error {
	s.chmods++
	return s.FS.Chmod(name, mode)
}

// capture: buffer + logger para asserts de logs (P12/P13).
func capture() (*bytes.Buffer, *slog.Logger) {
	var buf bytes.Buffer
	return &buf, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func baseTime() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }

// modelsBody arma un body /v1/models list-shape real con created variable
// por entry y campo extra (B7a: JAMÁS participan del veredicto).
func modelsBody(t *testing.T, ids []string) []byte {
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

// okProber: healthz 2xx a la primera; /v1/models con el set dado.
func okProber(t *testing.T, ids []string) *fakeProber {
	t.Helper()
	return &fakeProber{
		Healthz: []proberResp{{status: 200, body: []byte("ok")}},
		Models:  proberResp{status: 200, body: modelsBody(t, ids)},
	}
}

// phaseCount cuenta apariciones del atributo phase=X en el log capturado.
func phaseCount(buf *bytes.Buffer, phase string) int {
	needle := "phase=" + phase
	return bytes.Count(buf.Bytes(), []byte(needle))
}

// runReload: helper de construcción del Input para los paths DEGRADADOS
// (VerifyKey siempre vacío — P9). El parámetro key NO participa acá.
func runReload(configPath string, stash []byte, applied bool, digest string, sys *fakeSystemd, p *fakeProber, fs FS, ck *fakeClock, logger *slog.Logger) int {
	return Run(Input{
		Stash:      stash,
		ConfigPath: configPath,
		Applied:    applied,
		Digest:     digest,
		Unit:       unitConst,
		VerifyKey:  "",
		Systemd:    sys,
		Prober:     p,
		FS:         fs,
		Clock:      ck,
		Logger:     logger,
	})
}

// runReloadKey: ídem con VerifyKey explícito (B7/B15).
func runReloadKey(configPath string, stash []byte, digest string, key string, sys *fakeSystemd, p *fakeProber, fs FS, ck *fakeClock, logger *slog.Logger) int {
	return Run(Input{
		Stash:      stash,
		ConfigPath: configPath,
		Applied:    true,
		Digest:     digest,
		Unit:       unitConst,
		VerifyKey:  key,
		Systemd:    sys,
		Prober:     p,
		FS:         fs,
		Clock:      ck,
		Logger:     logger,
	})
}

// ---- B3 (C2, P2+D8) — defensa M-2 ----

// TestRun_M2Defense congela P2: con Skipped=true, digest en disco ==
// Input.Digest → exit 0 ("skip verificado"); mismatch → exit 1 con AMBOS
// digests en el log, cero restart/is-active/prober.
func TestRun_M2Defense(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)

	t.Run("skip_con_digest_match_exit_0", func(t *testing.T) {
		sys := &fakeSystemd{}
		p := &fakeProber{}
		buf, logger := capture()
		code := runReload(configPath, nil, false, digestHex(raw), sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 0 {
			t.Fatalf("skip con digest match = %d, want 0 (P2)", code)
		}
		if !bytes.Contains(buf.Bytes(), []byte("skip verificado")) {
			t.Errorf("log sin 'skip verificado': %s", buf.String())
		}
		if sys.count("restart") != 0 || sys.count("is-active") != 0 {
			t.Errorf("restart/is-active llamados en skip legítimo (P2: jamás restart): %+v", sys.Calls)
		}
		if len(p.Calls) != 0 {
			t.Errorf("Prober llamado en skip legítimo (P2): %+v", p.Calls)
		}
	})

	t.Run("skip_con_digest_mismatch_exit_1_sin_restart", func(t *testing.T) {
		// config en disco tamperado (bytes distintos al candidato — M-2 lee
		// lo que está en disco, read-only).
		tampered := []byte("# tamperado\n" + reloadConfigFixture)
		if err := os.WriteFile(configPath, tampered, 0o600); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		t.Cleanup(func() { _ = os.WriteFile(configPath, raw, 0o600) })

		sys := &fakeSystemd{}
		p := &fakeProber{}
		buf, logger := capture()
		code := runReload(configPath, nil, false, digestHex(raw), sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 1 {
			t.Fatalf("skip con mismatch = %d, want 1 (P2 fail-loud, D8)", code)
		}
		diskDigest := digestHex(tampered)
		if !bytes.Contains(buf.Bytes(), []byte(diskDigest)) {
			t.Errorf("log sin el digest del disco %q: %s", diskDigest, buf.String())
		}
		if !bytes.Contains(buf.Bytes(), []byte(digestHex(raw))) {
			t.Errorf("log sin el digest del reporte %q: %s", digestHex(raw), buf.String())
		}
		if sys.count("restart") != 0 {
			t.Errorf("restart ejecutado tras mismatch (P2: jamás): %+v", sys.Calls)
		}
		if len(p.Calls) != 0 {
			t.Errorf("Prober llamado tras mismatch (P2): %+v", p.Calls)
		}
	})
}

// readBytesT: helper local (nombre distinto para no chocar con nada).
func readBytesT(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leyendo %s: %v", path, err)
	}
	return b
}

// ---- B4 (C13, P2) — el chequeo M-2 es read-only ----

// TestRun_M2ReadOnly congela C13: en el path de skip (match), cero
// escrituras de NINGÚN tipo (config ni sidecar).
func TestRun_M2ReadOnly(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)

	spy := &spyFS{FS: realFS{}}
	sys := &fakeSystemd{}
	_, logger := capture()
	code := runReload(configPath, nil, false, digestHex(raw), sys, &fakeProber{}, spy, &fakeClock{base: baseTime()}, logger)
	if code != 0 {
		t.Fatalf("skip legítimo = %d, want 0", code)
	}
	if spy.writes != 0 || spy.renames != 0 || spy.removes != 0 || spy.chmods != 0 || spy.temps != 0 {
		t.Errorf("M-2 escribió algo (C13: read-only): writes=%d renames=%d removes=%d chmods=%d temps=%d",
			spy.writes, spy.renames, spy.removes, spy.chmods, spy.temps)
	}
}

// ---- B5 (C3, P3/P4/P5) — orden de fases happy path (degradado, sin key) ----

// TestRun_ReloadPhases congela P3/P4/P5: restart ×1 → is-active (poll 1s,
// script no-activo×2 → activo) → healthz (script 500×2 → 2xx) con URL
// EXACTA derivada del config escrito (addr 4444, NO default) y SIN Bearer
// (/healthz es público, P5); key unset → cero llamadas /v1/models; exit 0.
func TestRun_ReloadPhases(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)

	sys := &fakeSystemd{IsActives: []bool{false, false, true}}
	p := &fakeProber{Healthz: []proberResp{{status: 500}, {status: 500}, {status: 200}}}
	ck := &fakeClock{base: baseTime()}
	_, logger := capture()
	code := runReload(configPath, raw, true, digestHex(raw), sys, p, realFS{}, ck, logger)
	if code != 0 {
		t.Fatalf("reload degradado feliz = %d, want 0 (P9)", code)
	}

	restarts := sys.restartCalls()
	if len(restarts) != 1 || restarts[0] != unitConst {
		t.Errorf("restarts = %v, want exactamente [%s] (P3/D4)", restarts, unitConst)
	}
	// is-active: 3 polls (2 no-activos + activo) con Sleep(1s) entre polls.
	if got := sys.count("is-active"); got != 3 {
		t.Errorf("is-active polls = %d, want 3 (no-activo×2 → activo, P4)", got)
	}
	if ck.Sleeps < 2 || ck.LastDur != time.Second {
		t.Errorf("poll sin Sleep(1s) inter-poll: Sleeps=%d LastDur=%v (P4/P5: poll 1s)", ck.Sleeps, ck.LastDur)
	}
	// healthz: URL exacta y en orden; sin Bearer (público, P5).
	var healthzURLs []string
	for _, c := range p.Calls {
		if len(c.URL) >= 8 && c.URL[len(c.URL)-8:] == "/healthz" {
			healthzURLs = append(healthzURLs, c.URL)
			if c.Bearer != "" {
				t.Errorf("healthz con Bearer (P5: público, jamás Bearer): %q", c.URL)
			}
		}
	}
	if len(healthzURLs) != 3 || healthzURLs[0] != "http://127.0.0.1:4444/healthz" {
		t.Errorf("healthz URLs = %v, want 3× http://127.0.0.1:4444/healthz (D5: addr derivado del config escrito, discriminante 4444)", healthzURLs)
	}
	// key unset → cero /v1/models (P9).
	if got := p.countPath("/v1/models"); got != 0 {
		t.Errorf("/v1/models llamado con key unset (P9): %d llamadas", got)
	}
}

// ---- B6 (C3, P4/P5 → rollback) — timeout de settle dispara rollback ----

// TestRun_SettleTimeoutRollback congela: timeout de F1 o F2 → rollback
// restore-only + re-restart + re-verificación, exit 3 (D9/D10). Con el
// reloj inyectado la ventana de 30s se agota en ~31 polls determinísticos
// (cero sleeps reales — un impl con time.Sleep real CUELGA este test).
func TestRun_SettleTimeoutRollback(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	t.Run("is_active_nunca_activo_rollback_agotado", func(t *testing.T) {
		// is-active false SIEMPRE (script saturado en false): F1 original
		// agota la ventana → restore → re-restart → re-verify F1 también
		// agota → exit 3 (D10).
		sys := &fakeSystemd{IsActives: []bool{false}}
		p := &fakeProber{}
		ck := &fakeClock{base: baseTime()}
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, ck, logger)
		if code != 3 {
			t.Fatalf("F1 timeout agotado = %d, want 3 (D10)", code)
		}
		if got := bytes.Equal(readBytesT(t, configPath), stash); !got {
			t.Errorf("config en disco != stash tras el rollback (D9: restore-only)")
		}
		if got := sys.count("restart"); got != 2 {
			t.Errorf("restarts = %d, want 2 (original + re-restart del rollback, P8)", got)
		}
		if !bytes.Contains(buf.Bytes(), []byte("rollback")) {
			t.Errorf("log sin fases rollback_*: %s", buf.String())
		}
	})

	t.Run("healthz_nunca_2xx_rollback_agotado", func(t *testing.T) {
		sys := &fakeSystemd{IsActives: []bool{true}}           // F1 ok a la primera
		p := &fakeProber{Healthz: []proberResp{{status: 500}}} // 500 forever
		ck := &fakeClock{base: baseTime()}
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, ck, logger)
		if code != 3 {
			t.Fatalf("F2 timeout agotado = %d, want 3 (D10)", code)
		}
		if !bytes.Equal(readBytesT(t, configPath), stash) {
			t.Error("config en disco != stash (D9)")
		}
		if got := sys.count("restart"); got != 2 {
			t.Errorf("restarts = %d, want 2 (P8)", got)
		}
		// key seteada pero F2 nunca pasó → cero /v1/models (F3 no se alcanza).
		if got := p.countPath("/v1/models"); got != 0 {
			t.Errorf("/v1/models llamado sin F2 ok: %d", got)
		}
		_ = buf
		_ = logger
	})
}

// ---- B7 (C4, P6+D6) — paridad de IDs por SET ----

// TestRun_Parity congela P6: set igual → exit 0 (aunque created difiera y
// haya campos extra); faltante/extra/object/JSON/401 → rollback exit 3;
// orden permutado → exit 0 (comparación por SET, no secuencia). F3 es UN
// solo request con bearer == VerifyKey.
func TestRun_Parity(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	cases := []struct {
		name   string
		ids    []string
		object string
		body   []byte
		status int
		want   int
	}{
		{"set_exacto_con_created_distinto_y_campos_extra", expectedModelSet, "list", nil, 200, 0},
		{"orden_permutado_mismo_set", []string{"minimax-m3", "claude-sonnet-4", "glm-5.2"}, "list", nil, 200, 0},
		{"id_faltante_rollback", []string{"glm-5.2", "minimax-m3"}, "list", nil, 200, 3},
		{"id_extra_rollback", append(append([]string{}, expectedModelSet...), "id-extra"), "list", nil, 200, 3},
		{"object_no_list_rollback", expectedModelSet, "no-list", nil, 200, 3},
		{"json_no_parseable_rollback", nil, "", []byte("no-json"), 200, 3},
		{"http_401_rollback", expectedModelSet, "list", nil, 401, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := &fakeSystemd{IsActives: []bool{true}}
			var p *fakeProber
			if tc.body != nil {
				p = &fakeProber{
					Healthz: []proberResp{{status: 200}},
					Models:  proberResp{status: tc.status, body: tc.body},
				}
			} else if tc.object != "list" {
				body, _ := json.Marshal(map[string]any{"object": tc.object, "data": []map[string]any{{"id": tc.ids[0]}}})
				p = &fakeProber{
					Healthz: []proberResp{{status: 200}},
					Models:  proberResp{status: tc.status, body: body},
				}
			} else {
				p = okProber(t, tc.ids)
				if tc.status != 200 {
					p.Models.status = tc.status
				}
			}
			ck := &fakeClock{base: baseTime()}
			code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, ck, loggerNop())
			if code != tc.want {
				t.Fatalf("paridad case %q = %d, want %d", tc.name, code, tc.want)
			}
			// F3: UN solo request (sin retry), bearer == key, URL exacta.
			modelsCalls := 0
			for _, c := range p.Calls {
				if len(c.URL) >= 10 && c.URL[len(c.URL)-10:] == "/v1/models" {
					modelsCalls++
					if c.Bearer != verifyKeyTest {
						t.Errorf("bearer de /v1/models = %q, want la key del Input (D7)", c.Bearer)
					}
					if c.URL != "http://127.0.0.1:4444/v1/models" {
						t.Errorf("URL de paridad = %q, want derivada del config escrito (D5/D6)", c.URL)
					}
				}
			}
			if modelsCalls != 1 {
				t.Errorf("/v1/models = %d llamadas, want 1 (P6: un solo request, sin retry)", modelsCalls)
			}
		})
	}
}

func loggerNop() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

// ---- B8 (C4, P9+D7) — degradación sin key / 401 con key ----

// TestRun_ParityKeyDegradation congela P9: key unset + F1/F2 ok → cero
// /v1/models, UN warning con sufijo `verify_key`, exit 0; key seteada +
// 401 → rollback (NO degradación) → exit 3.
func TestRun_ParityKeyDegradation(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	t.Run("key_unset_degradacion_exit_0", func(t *testing.T) {
		sys := &fakeSystemd{IsActives: []bool{true}}
		p := okProber(t, expectedModelSet)
		buf, logger := capture()
		code := runReload(configPath, stash, true, digestHex(raw), sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 0 {
			t.Fatalf("degradación = %d, want 0 (P9)", code)
		}
		if got := p.countPath("/v1/models"); got != 0 {
			t.Errorf("/v1/models llamado con key unset (P9): %d", got)
		}
		if got := bytes.Count(buf.Bytes(), []byte("verify_key")); got != 1 {
			t.Errorf("warning verify_key = %d apariciones, want 1 (P9/R6)", got)
		}
	})

	t.Run("key_seteada_401_rollback", func(t *testing.T) {
		p := &fakeProber{
			Healthz: []proberResp{{status: 200}},
			Models:  proberResp{status: 401, body: []byte(`{"error":"invalid_api_key"}`)},
		}
		sys := &fakeSystemd{IsActives: []bool{true, true}}
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, captureLoggerOnly(t))
		if code != 3 {
			t.Fatalf("401 con key seteada = %d, want 3 (P9: fallo de verificación → rollback, P8)", code)
		}
		if !bytes.Equal(readBytesT(t, configPath), stash) {
			t.Error("config en disco != stash (D9)")
		}
	})
}

func captureLoggerOnly(t *testing.T) *slog.Logger {
	t.Helper()
	_, l := capture()
	return l
}

// ---- B9 (C5, P7/P8+D9) — rollback restore-only completo ----

// TestRun_RollbackRestoreOnly congela P7/P8: fallo en F3 → config en disco
// == stash BYTE A BYTE; mode preservado; re-restart ×1 + F1/F2 ok → exit 3
// (P8: el veredicto del rollback JAMÁS es 0); log declara rollback.
func TestRun_RollbackRestoreOnly(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	if err := os.Chmod(configPath, 0o640); err != nil {
		t.Fatalf("chmod 640: %v", err)
	}
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	// Paridad FALLA (id faltante) → rollback completo con fakes OK.
	p := okProber(t, []string{"glm-5.2", "minimax-m3"}) // sin claude-sonnet-4
	sys := &fakeSystemd{IsActives: []bool{true, true}}  // F1 y re-F1 ok
	buf, logger := capture()
	code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
	if code != 3 {
		t.Fatalf("rollback completo = %d, want 3 (P8: JAMÁS 0)", code)
	}
	if got := readBytesT(t, configPath); !bytes.Equal(got, stash) {
		t.Errorf("config post-rollback != stash byte-a-byte (P7/D9: bytes leídos jamás derivados)\nstash: %q\ndisk:  %q", stash, got)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode post-rollback = %o, want 640 (P7: mode vigente preservado)", info.Mode().Perm())
	}
	if got := sys.count("restart"); got != 2 {
		t.Errorf("restarts = %d, want 2 (original + re-restart del rollback, P8)", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("rollback")) {
		t.Errorf("log sin declaración de rollback: %s", buf.String())
	}
}

// ---- B10 (C6, P7/D10) — fallo de escritura del restore ----

// TestRun_RollbackWriteFail congela: dir read-only → el temp EN ESE dir
// falla → exit 3 inmediato con el error de I/O en el log, cero re-restart
// (Restart total == 1), config vigente jamás truncado.
func TestRun_RollbackWriteFail(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("como root el chmod read-only no bloquea la escritura — caso no determinístico")
	}
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}

	p := okProber(t, []string{"id-faltante"}) // fuerza la verificación de fallo → rollback
	sys := &fakeSystemd{IsActives: []bool{true}}
	buf, logger := capture()
	code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
	if code != 3 {
		t.Fatalf("fallo de restore = %d, want 3 (D10: salto directo)", code)
	}
	if got := sys.count("restart"); got != 1 {
		t.Errorf("restarts = %d, want 1 (P7: fallo de restore → SIN re-restart)", got)
	}
	if got := readBytesT(t, configPath); !bytes.Equal(got, raw) {
		t.Error("config vigente truncado/modificado en el camino de fallo (P9 heredada)")
	}
	_ = buf
}

// ---- B11 (C5/P8, D10) — rollback agotado con fase exacta en el log ----

// TestRun_RollbackExhausted congela D10: restore OK pero rollback agotado
// (re-restart falla / re-F1 timeout / re-F2 fail) → exit 3 SIEMPRE con la
// fase fallida en el log.
func TestRun_RollbackExhausted(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	t.Run("re_restart_falla", func(t *testing.T) {
		// F3 falla (paridad) → rollback: restore OK, re-restart ERROR.
		sys := &fakeSystemd{RestartErr: fmt.Errorf("systemd rebooting"), IsActives: []bool{true}}
		p := okProber(t, []string{"glm-5.2"})
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 3 {
			t.Fatalf("rollback agotado (re-restart) = %d, want 3", code)
		}
		if !bytes.Contains(buf.Bytes(), []byte("rollback_restart")) {
			t.Errorf("log sin fase rollback_restart: %s", buf.String())
		}
	})

	t.Run("re_verify_is_active_timeout", func(t *testing.T) {
		// is-active true SOLO la primera vez (F1 original), false SIEMPRE
		// en la re-verificación → rollback_verify agotado.
		sys := &fakeSystemd{IsActives: []bool{true, false}}
		p := okProber(t, []string{"glm-5.2"})
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 3 {
			t.Fatalf("rollback re-verify agotado = %d, want 3", code)
		}
		if !bytes.Contains(buf.Bytes(), []byte("rollback_verify")) {
			t.Errorf("log sin fase rollback_verify: %s", buf.String())
		}
		_ = logger
	})
}

// ---- B12 (C11, P14) — el rollback jamás toca el sidecar ----

// TestRun_RollbackSidecarUntouched congela P14: rollback exitoso → sidecar
// byte-intacto (digest del candidato, mtime congelado) y config == stash;
// clients.yaml (archivo inerte en el dir) jamás tocado.
func TestRun_RollbackSidecarUntouched(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	sidecar := configPath + ".sha256"
	sidecarBytes := []byte(digestHex(raw))
	if err := os.WriteFile(sidecar, sidecarBytes, 0o600); err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	past := baseTime().Add(-time.Hour)
	if err := os.Chtimes(sidecar, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	clientsFile := filepath.Join(dir, "clients.yaml")
	if err := os.WriteFile(clientsFile, []byte("- id: x\n  key_sha256: \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n"), 0o600); err != nil {
		t.Fatalf("clients file: %v", err)
	}

	p := okProber(t, []string{"glm-5.2"}) // paridad falla → rollback
	sys := &fakeSystemd{IsActives: []bool{true, true}}
	code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, captureLoggerOnly(t))
	if code != 3 {
		t.Fatalf("rollback = %d, want 3", code)
	}
	if got := readBytesT(t, configPath); !bytes.Equal(got, stash) {
		t.Error("config post-rollback != stash (P7)")
	}
	if got := readBytesT(t, sidecar); !bytes.Equal(got, sidecarBytes) {
		t.Errorf("sidecar modificado por el rollback (P14): %q", got)
	}
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("mtime del sidecar cambió en el rollback (P14): %v != %v", info.ModTime(), past)
	}
}

// ---- B13 (C7, P10) — systemd no disponible ----

// TestRun_SystemdUnavailable congela P10: Available() error → exit 3
// descriptivo, cero restart/is-active/prober, cero escrituras, detección
// ANTES de P3 (orden de llamadas: solo available).
func TestRun_SystemdUnavailable(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	sys := &fakeSystemd{AvailableErr: fmt.Errorf("no user session")}
	spy := &spyFS{FS: realFS{}}
	buf, logger := capture()
	code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, okProber(t, expectedModelSet), spy, &fakeClock{base: baseTime()}, logger)
	if code != 3 {
		t.Fatalf("systemd ausente = %d, want 3 (D11)", code)
	}
	if sys.count("restart") != 0 || sys.count("is-active") != 0 {
		t.Errorf("llamadas tras available-error: %+v (P10: detección precede a P3)", sys.Calls)
	}
	if spy.temps != 0 || spy.renames != 0 || spy.writes != 0 {
		t.Errorf("rollback ejecutado sin systemd (P10: cero rollback): %+v", spy)
	}
	if !bytes.Contains(buf.Bytes(), []byte("systemd")) {
		t.Errorf("log sin descripción del estado systemd: %s", buf.String())
	}
	// el config NUEVO sigue en disco (P11: el log lo declara).
	if got := readBytesT(t, configPath); !bytes.Equal(got, raw) {
		t.Error("config nuevo alterado por el path systemd-unavailable")
	}
}

// ---- B14 (C9, P12) — logging estructurado por fase ----

// TestRun_PhaseLogging congela P12: 1 record por fase con atributo `phase`
// en orden de ejecución real; Info en éxito, Warn en degradación, Error en
// fallo; campos suficientes (unit en restart, url en healthz/parity,
// digests en m2_check).
func TestRun_PhaseLogging(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	t.Run("happy_con_key_fases_en_orden", func(t *testing.T) {
		sys := &fakeSystemd{IsActives: []bool{true}}
		p := okProber(t, expectedModelSet)
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 0 {
			t.Fatalf("happy = %d, want 0", code)
		}
		for _, phase := range []string{"restart", "is_active", "healthz", "parity"} {
			if got := phaseCount(buf, phase); got != 1 {
				t.Errorf("phase=%s: %d records, want 1 (P12: uno por fase)", phase, got)
			}
		}
		if !ordered(buf, "phase=restart", "phase=is_active", "phase=healthz", "phase=parity") {
			t.Errorf("orden de fases violado: %s", buf.String())
		}
	})

	t.Run("degradado_sin_key", func(t *testing.T) {
		sys := &fakeSystemd{IsActives: []bool{true}}
		p := &fakeProber{Healthz: []proberResp{{status: 200}}}
		buf, logger := capture()
		code := runReload(configPath, stash, true, digestHex(raw), sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 0 {
			t.Fatalf("degradado = %d, want 0", code)
		}
		if got := phaseCount(buf, "degraded"); got != 1 {
			t.Errorf("phase=degraded: %d records, want 1", got)
		}
		if got := phaseCount(buf, "parity"); got != 0 {
			t.Errorf("phase=parity en path degradado (P9: no se llama): %d", got)
		}
	})

	t.Run("fallo_con_rollback_fases_error", func(t *testing.T) {
		sys := &fakeSystemd{IsActives: []bool{true, true}}
		p := okProber(t, []string{"glm-5.2"}) // paridad falla
		buf, logger := capture()
		code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
		if code != 3 {
			t.Fatalf("rollback = %d, want 3", code)
		}
		for _, phase := range []string{"rollback_restore", "rollback_restart", "rollback_verify"} {
			if got := phaseCount(buf, phase); got != 1 {
				t.Errorf("phase=%s: %d records, want 1", phase, got)
			}
		}
	})
}

// ordered verifica que los marcadores aparecen en el orden dado.
func ordered(buf *bytes.Buffer, markers ...string) bool {
	pos := 0
	log := buf.Bytes()
	for _, m := range markers {
		i := bytes.Index(log[pos:], []byte(m))
		if i < 0 {
			return false
		}
		pos += i + len(m)
	}
	return true
}

// ---- B15 (C10, P13+I6) — sin secrets en logs ----

// TestRun_NoSecretsInLogs congela P13/I6: con key seteada y 401 forzado
// (con rollback completo), el valor de la key JAMÁS aparece en el log (ni
// truncado) y los headers Authorization jamás se loguean.
func TestRun_NoSecretsInLogs(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	p := okProber(t, []string{"glm-5.2"})
	p.Models.status = 401
	sys := &fakeSystemd{IsActives: []bool{true, true}}
	buf, logger := capture()
	code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, logger)
	if code != 3 {
		t.Fatalf("run = %d, want 3", code)
	}
	if bytes.Contains(buf.Bytes(), []byte(verifyKeyTest)) {
		t.Errorf("la key aparece en el log (P13/I6): %s", buf.String())
	}
	if bytes.Contains(bytes.ToLower(buf.Bytes()), []byte("authorization")) {
		t.Errorf("header Authorization logueado (I6): %s", buf.String())
	}
}

// ---- B16 (C12, D6) — set esperado desde el config escrito ----

// TestRun_ExpectedModelSet congela D6: el set esperado deriva del config
// ESCRITO en disco vía ParseForValidation — unión dedup de providers[].models
// (incluye subprocess; providers "degradados sin models" contribuyen CERO
// ids — no construibles post-004, congelado por TestLoadMissingProviderFields).
func TestRun_ExpectedModelSet(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBytes(t, dir, reloadConfigFixture)
	raw := readBytesT(t, configPath)
	stash := []byte(reloadConfigStash)

	cases := []struct {
		name string
		ids  []string
		want int
	}{
		{"set_exacto_exit_0", expectedModelSet, 0},
		{"sin_subprocess_rollback", []string{"glm-5.2", "minimax-m3"}, 3}, // sin claude-sonnet-4 → el subprocess PARTICIPA
		{"id_extra_rollback", append(append([]string{}, expectedModelSet...), "otro"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys := &fakeSystemd{IsActives: []bool{true, true}}
			p := okProber(t, tc.ids)
			code := runReloadKey(configPath, stash, digestHex(raw), verifyKeyTest, sys, p, realFS{}, &fakeClock{base: baseTime()}, captureLoggerOnly(t))
			if code != tc.want {
				t.Fatalf("%s = %d, want %d (D6: set = unión dedup de TODOS los providers)", tc.name, code, tc.want)
			}
		})
	}
}

// ordered helper para B14 (referenciado arriba).
