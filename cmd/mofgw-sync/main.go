// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizza
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command mofgw-sync materializa config.yaml desde el catálogo upstream
// (feature 019-004-atomic-write-validate, ciclo --once único — el
// scheduling/timer es de 006): fetch (o cache-only con --no-fetch) →
// catalogmerge.Merge → Serialize → validación pre-commit → escritura
// atómica con skip byte-idéntico por digest sidecar. Adapter fino (D1.3):
// el binario es el ÚNICO que toca disco en el sync (I2) y el único que
// loguea (I1). Los warnings del plan son fail-soft (P12); los errores de
// datos son fail-loud sin escribir (P15).
//
//	mofgw-sync [-config <path>] [--no-fetch] [--once]
//
// Exit codes (P15): 0 = escrito OK o skipped byte-idéntico; 1 = fail-loud
// (config vigente inválido, validación del candidato falla, Merge sin
// fuentes, I/O, YAML inválido); 2 = flags/uso inválido.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	syncsnapshot "github.com/ofapsaas/mofgw/cmd/mofgw-sync/snapshot"
	"github.com/ofapsaas/mofgw/internal/catalogmerge"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/configsync"
	"github.com/ofapsaas/mofgw/internal/modelsdev"
	"github.com/ofapsaas/mofgw/internal/reloadsig"
	"github.com/ofapsaas/mofgw/internal/upstream"
)

// runOpts es la inyección del ciclo (contrato del test-writer, main_test.go).
// Extensión ADITIVA de 005 (test-audit §2.4): los campos nuevos en
// zero-value DESHABILITAN la fase de reload (los tests de 004 congelan solo
// la fase 004); main() siempre cablea hooks reales en producción.
// Extensión ADITIVA de 007 (test-audit §2.4): Snapshot zero-value
// (Available=false) ⇒ comportamiento pre-007 idéntico.
type runOpts struct {
	ConfigPath string      // -config (precedencia idéntica a config.Load)
	NoFetch    bool        // --no-fetch: cache-only, sin red (P14)
	CachePaths cachePaths  // inyección de paths de cache por fuente (tests)
	NoReload   bool        // --no-reload (D12): aplica config sin restartear
	Reload     reloadHooks // inyección de la fase reload (tests); nil hooks = disabled
	Snapshot   runSnapshot // fallback offline inyectado (tests tiny; prod desde Embedded)
}

// runSnapshot: catálogo models.dev de último recurso (019-007 D3).
type runSnapshot struct {
	Raw       []byte
	FetchedAt time.Time
	SHA256    string
	Available bool
}

// reloadHooks: implementaciones de la fase de reload (019-005). Zero-value
// (Systemd nil) ⇒ NINGUNA fase de reload/verificación/rollback corre (P1)
// — ni siquiera el chequeo M-2. VerifyKey "" → resuelta de env (D7).
type reloadHooks struct {
	Systemd   reloadsig.SystemdCtl
	Prober    reloadsig.Prober
	Clock     reloadsig.Clock
	VerifyKey string
	FS        reloadsig.FS
}

// cachePaths inyecta el path de cache por fuente; vacío → Default*CachePath.
type cachePaths struct {
	ModelsDev, Zen, Go, OpenRouter string
}

// osFS es la implementación os-backed del FS inyectado de configsync: el
// único I/O de disco del ciclo (I2 — único escritor de config.yaml).
type osFS struct{}

func (osFS) CreateTemp(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }
func (osFS) Rename(oldpath, newpath string) error             { return os.Rename(oldpath, newpath) }
func (osFS) Stat(name string) (os.FileInfo, error)            { return os.Stat(name) }
func (osFS) ReadFile(name string) ([]byte, error)             { return os.ReadFile(name) }
func (osFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (osFS) Remove(name string) error                  { return os.Remove(name) }
func (osFS) Chmod(name string, mode os.FileMode) error { return os.Chmod(name, mode) }

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "mofgw-sync:", err)
		os.Exit(2) // P15: flags/uso inválido
	}
	// main() SIEMPRE cablea hooks reales (nil-disabled es exclusivo de
	// tests — test-audit §2.4/R3). --no-reload lo respeta run() por opts.
	opts.Reload = realReloadHooks()
	// 019-007 (P9): poblar el fallback desde el snapshot embebido (solo el
	// binario sync crece; la librería y el server quedan livianos, I1).
	if raw, meta, ok := syncsnapshot.Embedded(); ok {
		opts.Snapshot = runSnapshot{
			Raw:       raw,
			FetchedAt: meta.FetchedAt,
			SHA256:    meta.SHA256,
			Available: true,
		}
	}
	os.Exit(run(opts, slog.Default()))
}

// parseArgs parsea -config/--no-fetch/--once/--no-reload con ContinueOnError
// (el mapeo a exit 2 vive en main; el flag pkg no vuelca usage a stderr).
func parseArgs(args []string) (runOpts, error) {
	fs := flag.NewFlagSet("mofgw-sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path explícito de config.yaml (flag > ~/.config/mofgw/config.yaml > /etc/mofgw/config.yaml)")
	noFetch := fs.Bool("no-fetch", false, "cache-only: usar los caches de disco sin red (P14)")
	// --once es no-op (D1.3): el ciclo completo es el ÚNICO modo de 004
	// (el timer/scheduling es 006), así que el flag es redundante con el
	// default — se acepta por paridad de uso y no cambia el comportamiento.
	_ = fs.Bool("once", false, "no-op: el ciclo es once por diseño (D1.3, el scheduling es 006)")
	// --no-reload (D12 de 019-005): aplica el config pero NO restartea ni
	// verifica ni rolea back (streams en curso / testing / 006).
	noReload := fs.Bool("no-reload", false, "no restartear mofgw tras aplicar el config (D12)")
	if err := fs.Parse(args); err != nil {
		return runOpts{}, fmt.Errorf("mofgw-sync: flags inválidos: %w", err)
	}
	return runOpts{ConfigPath: *configPath, NoFetch: *noFetch, NoReload: *noReload}, nil
}

// run ejecuta el ciclo --once (P15). logger nil → slog.Default(). Retorna el
// exit code: 0 = escrito OK o skipped byte-idéntico; 1 = fail-loud (en
// CUALQUIER 1 el config queda byte-intacto).
func run(opts runOpts, logger *slog.Logger) int {
	if logger == nil {
		logger = slog.Default()
	}
	raw, configPath, err := loadRawConfig(opts.ConfigPath)
	if err != nil {
		logger.Error("mofgw-sync: config no cargado (fail-loud)", "error", err)
		return 1
	}
	// Fail-loud (P15): el config VIGENTE inválido aborta antes de tocar
	// nada (el mismo []byte alimenta Parse→Merge y la edición, P1).
	cfg, err := config.Parse(raw)
	if err != nil {
		logger.Error("mofgw-sync: config vigente inválido (fail-loud, sin escribir)", "error", err)
		return 1
	}

	catalog, zenList, goList, orCatalog, fromSnapshot := loadSources(opts)
	plan, err := catalogmerge.Merge(catalog, zenList, goList, orCatalog, cfg.Providers)
	if err != nil {
		logger.Error("mofgw-sync: merge de catálogos", "error", err)
		return 1
	}

	// 019-007 (D5): si el catálogo vino del snapshot (flag propagado por
	// loadSources — review F1: sin re-stat, sin ventana TOCTOU), visibilidad
	// fail-soft: evento + warning sintético post-Merge (binario-added, NO
	// derivado del IR) + warn de staleness si age > 30d. NUNCA cambia el
	// exit code (I7 de 007).
	if fromSnapshot {
		ageDays := snapshotAgeDays(opts.Snapshot)
		logger.Warn("mofgw-sync: snapshot_fallback — catálogo models.dev servido desde el snapshot embebido",
			"source", "modelsdev",
			"fetched_at", opts.Snapshot.FetchedAt.UTC().Format(time.RFC3339),
			"sha256", opts.Snapshot.SHA256,
			"age_days", ageDays)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"models.dev: catálogo snapshot embebido (fetched_at %s, %d días) — sin red",
			opts.Snapshot.FetchedAt.UTC().Format("2006-01-02"), ageDays))
		if ageDays > 30 {
			logger.Warn("mofgw-sync: snapshot stale — el snapshot supera 30 días; regenerar con scripts/fetch-snapshot.sh",
				"source", "modelsdev",
				"age_days", ageDays)
		}
	}

	// P12: warnings del plan (el IR ya los trae sorted+dedup, incluidos los
	// per-provider), logueados UNO POR warning ANTES de escribir.
	for _, w := range plan.Warnings {
		logger.Warn("mofgw-sync: warning del plan", "warning", w)
	}

	report, err := configsync.Apply(raw, plan, configPath, osFS{})
	if err != nil {
		logger.Error("mofgw-sync: apply", "error", err)
		return 1
	}
	logger.Info("mofgw-sync: ciclo completo",
		"applied", report.Applied,
		"skipped", report.Skipped,
		"digest", report.Digest,
		"sources", sortedSourceNames(plan.SourcesUsed),
	)

	// ---- Fase de reload (019-005, P1) ----
	// Solo si 004 terminó con exit 0 (este punto) y hooks no-nil (tests de
	// 004 con hooks zero-value jamás llegan acá). --no-reload → NINGUNA
	// fase (ni siquiera M-2); exit = exit de 004 (P1).
	if opts.NoReload || opts.Reload.Systemd == nil {
		return 0
	}
	// Wiring de defaults (D7/D9): VerifyKey del env si quedó vacía; FS y
	// Clock reales si no fueron inyectados.
	hooks := opts.Reload
	if hooks.VerifyKey == "" {
		if v, ok := os.LookupEnv("MOFGW_SYNC_VERIFY_KEY"); ok {
			hooks.VerifyKey = v
		}
	}
	if hooks.FS == nil {
		hooks.FS = osFS{}
	}
	if hooks.Clock == nil {
		hooks.Clock = realClock{}
	}
	return reloadsig.Run(reloadsig.Input{
		Stash:      raw, // P15: los bytes leídos al inicio del run (un solo read, P1 de 004)
		ConfigPath: configPath,
		Applied:    report.Applied,
		Skipped:    report.Skipped,
		Digest:     report.Digest,
		Unit:       "mofgw.service", // D4: constante
		VerifyKey:  hooks.VerifyKey,
		Systemd:    hooks.Systemd,
		Prober:     hooks.Prober,
		FS:         hooks.FS,
		Clock:      hooks.Clock,
		Logger:     logger,
	})
}

// loadRawConfig lee el config RAW (jamás parseado acá) con la precedencia
// idéntica a config.Load: flag > ~/.config/mofgw/config.yaml >
// /etc/mofgw/config.yaml. El path devuelto es el que SIEMPRE se escribe
// (D1.3).
func loadRawConfig(explicit string) ([]byte, string, error) {
	if explicit != "" {
		raw, err := os.ReadFile(explicit)
		if err != nil {
			return nil, "", fmt.Errorf("mofgw-sync: leer config %s: %w", explicit, err)
		}
		return raw, explicit, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "mofgw", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, "", fmt.Errorf("mofgw-sync: leer config %s: %w", p, err)
			}
			return raw, p, nil
		}
	}
	p := "/etc/mofgw/config.yaml"
	if _, err := os.Stat(p); err == nil {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("mofgw-sync: leer config %s: %w", p, err)
		}
		return raw, p, nil
	}
	return nil, "", fmt.Errorf("mofgw-sync: no se encontró config.yaml (busqué ~/.config/mofgw/config.yaml y %s)", p)
}

// loadSources resuelve las 4 fuentes: --no-fetch → Get puro de los caches de
// disco (sin red, P14); modo fetch → Refresh fail-soft + Get. Cache ausente
// → fuente nil (fail-soft de 003: degradación + warning en el IR). Con
// path inyectado vacío → Default*CachePath.
//
// Retorna además `fromSnapshot`: true IFF el catálogo vino del snapshot
// (019-007 D4/D5). El flag se PROPAGA (no se re-chequea en el llamante —
// review F1: el re-stat posterior abriría una ventana TOCTOU donde el mundo
// exterior —otro mofgw-sync del timer, el operador— cambia el archivo entre
// ambos stats y el fallback quedaría silencioso).
func loadSources(opts runOpts) (*modelsdev.Catalog, *upstream.ModelList, *upstream.ModelList, *upstream.OpenRouterCatalog, bool) {
	ctx := context.Background()

	mdStore := modelsdev.NewStore(pathOr(opts.CachePaths.ModelsDev, modelsdev.DefaultCachePath()), 0, false)
	zenStore := upstream.NewZenStore(pathOr(opts.CachePaths.Zen, upstream.DefaultZenCachePath()), 0, false)
	goStore := upstream.NewGoStore(pathOr(opts.CachePaths.Go, upstream.DefaultGoCachePath()), 0, false)
	orStore := upstream.NewOpenRouterStore(pathOr(opts.CachePaths.OpenRouter, upstream.DefaultOpenRouterCachePath()), 0, false)

	if !opts.NoFetch {
		// Refresh por fuente; el error es fail-soft (I6 de 001/002): el
		// cache stale sigue disponible y la fuente nil degrada con warning.
		_, _ = mdStore.Refresh(ctx, false)
		_, _ = zenStore.Refresh(ctx, false)
		_, _ = goStore.Refresh(ctx, false)
		_, _ = orStore.Refresh(ctx, false)
	}

	catalog, err := mdStore.Get()
	fromSnapshot := false
	if err != nil {
		catalog = nil
		// 019-007 (D4): fallback SOLO por ausencia (cache inexistente).
		// Archivo presente pero corrupto (parse error) → error de corruption,
		// NO cae a snapshot (P5: el snapshot no enmascara corrupción).
		mdPath := pathOr(opts.CachePaths.ModelsDev, modelsdev.DefaultCachePath())
		if _, statErr := os.Stat(mdPath); os.IsNotExist(statErr) && opts.Snapshot.Available {
			if parsed, perr := modelsdev.ParseCatalog(opts.Snapshot.Raw); perr == nil {
				catalog = parsed
				fromSnapshot = true
			}
		}
	}
	zen, err := zenStore.Get()
	var zenList *upstream.ModelList
	if err == nil {
		zenList = &zen
	}
	gol, err := goStore.Get()
	var goList *upstream.ModelList
	if err == nil {
		goList = &gol
	}
	orc, err := orStore.Get()
	var orCatalog *upstream.OpenRouterCatalog
	if err == nil {
		orCatalog = &orc
	}
	return catalog, zenList, goList, orCatalog, fromSnapshot
}

func pathOr(injected, def string) string {
	if injected != "" {
		return injected
	}
	return def
}

// snapshotAgeDays: edad del snapshot en días enteros (0 si futuro).
func snapshotAgeDays(snap runSnapshot) int {
	d := int(time.Since(snap.FetchedAt).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// realReloadHooks: hooks de producción (main() los cablea siempre — el
// nil-disabled de runOpts es exclusivo de tests, test-audit §2.4/R3).
func realReloadHooks() reloadHooks {
	return reloadHooks{
		Systemd: execSystemdCtl{},
		Prober:  httpProber{},
		Clock:   realClock{},
		FS:      osFS{},
	}
}

// execSystemdCtl: implementación real de reloadsig.SystemdCtl vía
// systemctl --user (D4: deploy user unit verificado en el discovery).
type execSystemdCtl struct{}

func (execSystemdCtl) Available() error {
	// Detección de systemd user manager (P10/D11): distinguir "manager
	// disponible" de "unit activo" — is-system-running falla solo si no hay
	// bus de usuario; "degraded" (exit 1) es un manager SANO con servicios
	// fallando → disponible para restart. El review F1 (019-005) identificó
	// la versión previa (is-active del unit) como misdiagnóstico: un unit
	// stopped/failed con systemd sano es restarteable, no "sin systemd".
	out, err := exec.Command("systemctl", "--user", "is-system-running").Output()
	if err == nil {
		return nil
	}
	if strings.Contains(strings.TrimSpace(string(out)), "degraded") {
		return nil // manager vivo, solo hay servicios en estado failed
	}
	return fmt.Errorf("mofgw-sync: systemd user manager: %s (%v)", strings.TrimSpace(string(out)), err)
}

func (execSystemdCtl) Restart(unit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // TimeoutStartSec=30 del unit real
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "--user", "restart", unit).Run()
}

func (execSystemdCtl) IsActive(unit string) bool {
	out, err := exec.Command("systemctl", "--user", "is-active", unit).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

// httpProber: implementación real de reloadsig.Prober (net/http). Los
// timeouts de request (2s healthz / 5s paridad) viven en el wiring (R2 del
// audit: el Prober no lleva timeout por llamada — el client genérico usa 5s,
// suficiente para ambos paths; el discriminante exacto queda en review/C14).
type httpProber struct {
	Client *http.Client
}

func (p httpProber) Get(url, bearer string) (int, []byte, error) {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("mofgw-sync: request %s: %w", url, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("mofgw-sync: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("mofgw-sync: leer body %s: %w", url, err)
	}
	return resp.StatusCode, body, nil
}

// realClock: reloj real (Sleep duerme — solo producción; los tests inyectan
// fakeClock que avanza el reloj lógico, R1).
type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

func sortedSourceNames(used map[string]bool) []string {
	out := make([]string, 0, len(used))
	for name := range used {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
