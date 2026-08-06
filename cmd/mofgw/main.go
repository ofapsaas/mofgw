// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command mofgw es el proxy de IA con fallback transparente (ver
// README.md). Uso:
//
//	mofgw                        — arranca el proxy (config por precedencia)
//	mofgw -config <path>         — config explícito
//	mofgw hash-key [key]         — imprime el sha256 hex de una API key
//	                               (lee de stdin si no viene como arg)
//
// El config nunca contiene keys en claro: providers usan api_key_env
// (variables de entorno) y clients usan key_sha256 generado con
// `mofgw hash-key`.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/limiter"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-key" {
		runHashKey(os.Args[2:])
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mofgw:", err)
		os.Exit(1)
	}
}

// run carga config, construye el proxy y sirve HTTP con shutdown limpio.
func run() error {
	cfgPath := flag.String("config", "", "path explícito al config.yaml")
	verbose := flag.Bool("verbose", false, "nivel debug (qué recibe/envía/decide el proxy)")
	logFile := flag.String("log-file", "", "archivo de log (append); con --verbose el debug va solo al archivo")
	flag.Parse()

	cfg, usedPath, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger, closer, err := logging.Build(level, *logFile, nil)
	if err != nil {
		return err
	}
	defer closer.Close()
	logger.Info("config cargado", "path", usedPath, "providers", len(cfg.Providers), "clients", len(cfg.Clients))

	// Providers en orden de config (el orden ES la cadena de fallback).
	specs := make([]router.ProviderSpec, 0, len(cfg.Providers))
	providers := make([]provider.Provider, 0, len(cfg.Providers))
	clients := make([]*provider.Client, 0, len(cfg.Providers)) // concretos: health.Checker
	for _, pc := range cfg.Providers {
		cl := provider.NewClient(pc.ID, pc.BaseURL, pc.APIKey, pc.Models, pc.MaxTokens, nil)
		providers = append(providers, cl)
		clients = append(clients, cl)
		specs = append(specs, router.ProviderSpec{
			Provider: cl,
			Cooldown: pc.Cooldown,
			Timeout:  pc.Timeout,
		})
	}

	// EPIC-002 wiring: health store, retry y degradación se activan
	// desde config (002-002 / 002-001 / 002-004).
	var hs *health.Store
	if cfg.Fallback.Health.Interval > 0 {
		hs = health.New(cfg.Fallback.Health.Interval, cfg.Fallback.Health.Timeout, logger)
		for _, cl := range clients {
			hs.Register(cl) // *provider.Client implementa health.Checker
		}
		hctx, hcancel := context.WithCancel(context.Background())
		defer hcancel()
		hs.Start(hctx)
		hs.CheckNow() // primer check sin esperar el intervalo
		logger.Info("health checks activos", "interval", cfg.Fallback.Health.Interval.String(), "providers", len(providers))
	}

	r := router.NewWithOptions(specs, router.Options{
		MaxRetries:     cfg.Fallback.MaxRetries,
		Cooldown:       cfg.Fallback.Cooldown,
		CooldownJitter: cfg.Fallback.CooldownJitter,
		GlobalTimeout:  cfg.Fallback.Timeout,
		Retry: router.RetryConfig{
			MaxAttempts: cfg.Fallback.Retry.MaxAttempts,
			BackoffBase: cfg.Fallback.Retry.BackoffBase,
			BackoffMax:  cfg.Fallback.Retry.BackoffMax,
		},
		Degradation: router.DegradationConfig{
			MaxRequests:   cfg.Fallback.Degradation.MaxRequests,
			CooldownGrace: cfg.Fallback.Degradation.CooldownGrace,
		},
		Health: hs,
		Logger: logger,
	})

	authClients := make([]auth.Client, 0, len(cfg.Clients))
	for _, cc := range cfg.Clients {
		authClients = append(authClients, auth.Client{ID: cc.ID, KeySHA256: cc.KeySHA256})
	}
	authz := auth.New(authClients)
	m := metrics.New()

	// EPIC-004 wiring: límites de concurrencia (004-001 global con
	// backpressure; 004-002 aislamiento por cliente/agente). Todo en 0 =
	// ilimitado (backward compatible).
	var globalLimiter *limiter.Limiter
	if cfg.Server.MaxConcurrentRequests > 0 {
		globalLimiter = limiter.New(cfg.Server.MaxConcurrentRequests)
		logger.Info("límite de concurrencia global activo", "max_concurrent_requests", cfg.Server.MaxConcurrentRequests, "backpressure_timeout", cfg.Server.BackpressureTimeout.String())
	}
	var keyedLimiter *limiter.Keyed
	if anyClientLimit(cfg.Clients) {
		budgets := make(map[string]limiter.ClientBudget, len(cfg.Clients))
		for _, cc := range cfg.Clients {
			budgets[cc.ID] = limiter.ClientBudget{
				MaxConcurrent: cc.MaxConcurrentRequests,
				MaxPerAgent:   cc.MaxConcurrentPerAgent,
			}
		}
		keyedLimiter = limiter.NewKeyed(budgets)
		logger.Info("aislamiento por cliente/agente activo", "clients", len(budgets))
	}
	srv := proxy.New(r, providers, authz, m, logger, cfg.Server.MaxBodyBytes, globalLimiter, keyedLimiter, cfg.Server.BackpressureTimeout)

	// Cableado del catálogo y eficiencia (006-002/007-001/008-002/008-003):
	// pricing, metadata de modelos, budgets por cliente y margen de
	// contexto desde el config. Sin estas secciones en config → sin
	// cambios de comportamiento (backward compatible).
	pricing := make(map[string]proxy.ModelPricing, len(cfg.Pricing))
	for model := range cfg.Pricing {
		p := cfg.Pricing[model]
		pricing[model] = proxy.ModelPricing{
			InputUSDPerM:    p.InputUSDPerM,
			OutputUSDPerM:   p.OutputUSDPerM,
			CacheHitUSDPerM: p.CacheHitUSDPerM,
		}
	}
	srv.SetPricing(pricing)
	meta := make(map[string]config.ModelMetadata, len(cfg.ModelMetadata))
	for model, md := range cfg.ModelMetadata {
		meta[model] = md
	}
	srv.SetModelMetadata(meta)
	budgets := make(map[string]config.BudgetConfig, len(cfg.Clients))
	for _, cc := range cfg.Clients {
		if cc.Budget != nil {
			budgets[cc.ID] = *cc.Budget
		}
	}
	srv.SetBudget(budgets)
	if cfg.Context.Margin >= 0 {
		srv.SetContextMargin(cfg.Context.Margin)
	}

	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Shutdown limpio en SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("mofgw escuchando", "addr", cfg.Server.Addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutdown en curso...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

// anyClientLimit reporta si algún cliente del config tiene límites de
// concurrencia (004-002). Si ninguno, no se construye el Keyed limiter.
func anyClientLimit(clients []config.ClientConfig) bool {
	for _, c := range clients {
		if c.MaxConcurrentRequests > 0 || c.MaxConcurrentPerAgent > 0 {
			return true
		}
	}
	return false
}

// maxClientLimit devuelve el mayor max_concurrent_requests entre
// clientes (para log; el límite real es per-client en el Keyed).
func maxClientLimit(clients []config.ClientConfig) int {
	var m int
	for _, c := range clients {
		if c.MaxConcurrentRequests > m {
			m = c.MaxConcurrentRequests
		}
	}
	return m
}

// maxPerAgentLimit devuelve el mayor max_concurrent_per_agent entre
// clientes (para log).
func maxPerAgentLimit(clients []config.ClientConfig) int {
	var m int
	for _, c := range clients {
		if c.MaxConcurrentPerAgent > m {
			m = c.MaxConcurrentPerAgent
		}
	}
	return m
}

// runHashKey implementa `mofgw hash-key [key]`: imprime el sha256 hex
// sin loguear la key en claro.
func runHashKey(args []string) {
	var key string
	if len(args) > 0 {
		key = args[0]
	} else {
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			key = strings.TrimSpace(sc.Text())
		}
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "mofgw hash-key: falta la key (arg o stdin)")
		os.Exit(2)
	}
	sum := sha256.Sum256([]byte(key))
	fmt.Println(hex.EncodeToString(sum[:]))
}
