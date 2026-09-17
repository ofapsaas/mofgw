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
//	mofgw-sync [-config <path>] [--no-fetch]
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
	"os"
	"path/filepath"
	"sort"

	"github.com/ofapsaas/mofgw/internal/catalogmerge"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/configsync"
	"github.com/ofapsaas/mofgw/internal/modelsdev"
	"github.com/ofapsaas/mofgw/internal/upstream"
)

// runOpts es la inyección del ciclo (contrato del test-writer, main_test.go).
type runOpts struct {
	ConfigPath string     // -config (precedencia idéntica a config.Load)
	NoFetch    bool       // --no-fetch: cache-only, sin red (P14)
	CachePaths cachePaths // inyección de paths de cache por fuente (tests)
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
	os.Exit(run(opts, slog.Default()))
}

// parseArgs parsea -config/--no-fetch con ContinueOnError (el mapeo a exit 2
// vive en main; el flag pkg no vuelca usage a stderr).
func parseArgs(args []string) (runOpts, error) {
	fs := flag.NewFlagSet("mofgw-sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path explícito de config.yaml (flag > ~/.config/mofgw/config.yaml > /etc/mofgw/config.yaml)")
	noFetch := fs.Bool("no-fetch", false, "cache-only: usar los caches de disco sin red (P14)")
	if err := fs.Parse(args); err != nil {
		return runOpts{}, fmt.Errorf("mofgw-sync: flags inválidos: %w", err)
	}
	return runOpts{ConfigPath: *configPath, NoFetch: *noFetch}, nil
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

	catalog, zenList, goList, orCatalog := loadSources(opts)
	plan, err := catalogmerge.Merge(catalog, zenList, goList, orCatalog, cfg.Providers)
	if err != nil {
		logger.Error("mofgw-sync: merge de catálogos", "error", err)
		return 1
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
	return 0
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
func loadSources(opts runOpts) (*modelsdev.Catalog, *upstream.ModelList, *upstream.ModelList, *upstream.OpenRouterCatalog) {
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
	if err != nil {
		catalog = nil
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
	return catalog, zenList, goList, orCatalog
}

func pathOr(injected, def string) string {
	if injected != "" {
		return injected
	}
	return def
}

func sortedSourceNames(used map[string]bool) []string {
	out := make([]string, 0, len(used))
	for name := range used {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
