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
	"io"
	"log/slog"
	"math/rand"
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
	"github.com/ofapsaas/mofgw/internal/providerfactory"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
	"github.com/ofapsaas/mofgw/internal/websearch"
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
	// 013-003 (D2): la construcción del provider (HTTP o subprocess) la hace
	// el factory importable; main.go es solo el adapter fino.
	specs := make([]router.ProviderSpec, 0, len(cfg.Providers))
	providers := make([]provider.Provider, 0, len(cfg.Providers))
	var healthCheckers []health.Checker // providers que implementan health.Checker
	for _, pc := range cfg.Providers {
		p, err := providerfactory.BuildProvider(pc, logger)
		if err != nil {
			return err
		}
		providers = append(providers, p)
		if hc, ok := p.(health.Checker); ok {
			healthCheckers = append(healthCheckers, hc)
		}
		specs = append(specs, router.ProviderSpec{
			Provider: p,
			Cooldown: pc.Cooldown,
			Timeout:  pc.Timeout,
			// 029-001 (TECHDEBT #29): override per-provider del tope TTFB
			// de streaming (nil = usa fallback.first_token_timeout).
			FirstTokenTimeout: pc.FirstTokenTimeout,
			// 010-002: path declarativo del provider para la inyección del
			// thinking_default prescriptivo ("" | "zen" | "bailian"; "" =
			// sin inyección, fallback al ID del provider en el router).
			ThinkingPath: pc.ThinkingPath,
			// 013-003 (D5): allowlist de clientID con acceso al provider
			// subprocess; vacía = sin restricción (P9).
			AllowedClients: pc.Clients,
		})
		// 013-003 P9: allowlist vacía en un provider subprocess = sin
		// restricción (warning de arranque, C10).
		if pc.Type == "subprocess" && len(pc.Clients) == 0 {
			logger.Warn("provider subprocess sin restricción de clientes (clients vacío)", "provider", pc.ID)
		}
	}

	// EPIC-002 wiring: health store, retry y degradación se activan
	// desde config (002-002 / 002-001 / 002-004).
	var hs *health.Store
	if cfg.Fallback.Health.Interval > 0 {
		hs = health.New(cfg.Fallback.Health.Interval, cfg.Fallback.Health.Timeout, logger)
		for _, cl := range healthCheckers {
			hs.Register(cl) // solo providers que implementan health.Checker (los subprocess no)
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
		// 009-002 P3: tope del AffinityStore = max_sessions_retained
		// (mismo knob del config, decisión D4 del spec).
		StickyMaxEntries: cfg.Server.MaxSessionsRetained,
	})

	authClients := make([]auth.Client, 0, len(cfg.Clients))
	for _, cc := range cfg.Clients {
		authClients = append(authClients, auth.Client{ID: cc.ID, KeySHA256: cc.KeySHA256})
	}
	authz := auth.New(authClients)
	m := metrics.New()

	// TECHDEBT #13: persistencia del registro de accounting. Si el
	// snapshot existe y es válido, se restaura (los contadores pasan a
	// ser acumulados entre restarts). Corrupto/ausente → log + estado
	// limpio, nunca bloquea el arranque.
	if cfg.Server.StateFile != "" {
		if err := m.LoadState(cfg.Server.StateFile); err != nil {
			logger.Warn("state file no restaurado (arranque con estado limpio)", "path", cfg.Server.StateFile, "err", err)
		} else {
			logger.Info("state restaurado", "path", cfg.Server.StateFile)
		}
	}

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

	// Telemetría de descubrimiento (009-000 P2/R8): canal dedicado
	// telemetry.jsonl. Gestiona AMBOS closers (operativo + telemetría).
	telemetryLogger, telemetryCloser, err := buildTelemetryFromConfig(cfg)
	if err != nil {
		return err
	}
	if telemetryCloser != nil {
		defer telemetryCloser.Close()
	}

	srv := proxy.New(r, providers, authz, m, logger, telemetryLogger, cfg.Server.MaxBodyBytes, globalLimiter, keyedLimiter, cfg.Server.BackpressureTimeout)

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
	if cfg.Server.MaxSessionsRetained > 0 {
		// 008-003 P4: tope de retención de sesiones desde config
		// (external review 008-003: setter era dead code sin wiring).
		m.SetMaxSessionsRetained(cfg.Server.MaxSessionsRetained)
	}
	// Análisis estructural del contexto (009-001 P6): setter pre-tráfico
	// (patrón SetContextMargin) + ring buffer de history en metrics
	// (patrón SetMaxSessionsRetained). enabled=false → sin records.
	srv.SetContextAnalysis(cfg.Context.Analysis.Enabled, cfg.Context.Analysis.HistoryPerSession)
	m.SetContextHistoryPerSession(cfg.Context.Analysis.HistoryPerSession)
	// Afinidad de provider por sesión/cliente (009-002 P1): setter
	// pre-tráfico (patrón SetContextAnalysis). enabled=false (default) →
	// el proxy usa Complete/Stream legacy (P8).
	srv.SetStickyRouting(cfg.Fallback.StickyRouting.Enabled)
	// Coalescing de requests idénticos concurrentes (010-001 P0): setter
	// pre-tráfico (patrón SetStickyRouting). enabled=false (default) →
	// flujo legacy intacto (cambio de semántica opt-in).
	srv.SetSingleFlight(cfg.Efficiency.SingleFlight.Enabled, cfg.Efficiency.SingleFlight.MaxFlights)
	// Cache exact-match de respuestas (010-002 P1): setter pre-tráfico
	// (patrón SetStickyRouting). enabled=false (default) → flujo legacy
	// intacto (cambio de semántica opt-in). max_entries/ttl <= 0 →
	// defaults del paquete respcache (512 / 5m).
	srv.SetResponseCache(cfg.Efficiency.ResponseCache.Enabled, cfg.Efficiency.ResponseCache.MaxEntries, cfg.Efficiency.ResponseCache.TTL)
	// Web search server-side (011-005 P1/D5): grounded search emulado con
	// DDG. enabled=false (default) → `web_search_preview` conserva el 400
	// de D3 de 003 (sin regresión). enabled=true → se inyecta el cliente
	// DDG (scrape html.duckduckgo.com + fallback Instant Answer) y el tope
	// max_results (0 = default 3).
	if cfg.WebSearch.Enabled {
		srv.SetWebSearch(true, websearch.New(cfg.WebSearch.MaxResults, cfg.WebSearch.Timeout))
	} else {
		srv.SetWebSearch(false, nil)
	}
	srv.SetWebSearchMaxResults(cfg.WebSearch.MaxResults)
	// Forward de embeddings a Ollama (011-006 P1/D3): base_url global (un
	// Ollama por instancia) + modelo forzado POR CLIENTE (P3). Sin
	// embeddings.base_url → cliente no configurado (endpoint → 502 upstream,
	// P9). El modelo forzado se setea solo para clientes que lo declaran;
	// un cliente sin él → 400 (P4).
	if cfg.Embeddings.BaseURL != "" {
		srv.SetEmbeddings(cfg.Embeddings.BaseURL, cfg.Embeddings.APIKey)
	}
	for _, cc := range cfg.Clients {
		if cc.Embeddings != nil && cc.Embeddings.Model != "" {
			srv.SetClientEmbeddingsModel(cc.ID, cc.Embeddings.Model)
		}
	}

	httpServer := newHTTPServer(cfg.Server, srv.Handler())

	// Guardado periódico del state (TECHDEBT #13). Intervalo 0 = solo
	// shutdown; StateFile vacío = persistencia deshabilitada.
	var stateTicker *time.Ticker
	var stateStop, stateDone chan struct{}
	if cfg.Server.StateFile != "" {
		saveState := func(reason string) {
			if err := m.SaveState(cfg.Server.StateFile); err != nil {
				logger.Error("state save falló", "reason", reason, "err", err)
			} else {
				logger.Info("state guardado", "reason", reason, "path", cfg.Server.StateFile)
			}
		}
		if cfg.Server.StateSaveInterval > 0 {
			stateTicker = time.NewTicker(cfg.Server.StateSaveInterval)
			stateStop = make(chan struct{})
			stateDone = make(chan struct{})
			go func() {
				defer close(stateDone)
				for {
					select {
					case <-stateTicker.C:
						saveState("interval")
					case <-stateStop:
						return
					}
				}
			}()
		}
		defer func() {
			if stateTicker != nil {
				stateTicker.Stop()
			}
			if stateStop != nil {
				close(stateStop) // señal de stop al goroutine
				<-stateDone      // esperar salida (evita doble write)
			}
			saveState("shutdown")
		}()
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

// newHTTPServer construye el http.Server con los timeouts del config y
// los hardening de SEC-001 P3: ReadHeaderTimeout (5s, mitiga Slowloris)
// y MaxHeaderBytes (1MB, explícito). Extraído para testear la
// configuración de seguridad.
func newHTTPServer(sc config.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              sc.Addr,
		Handler:           handler,
		ReadTimeout:       sc.ReadTimeout,
		WriteTimeout:      sc.WriteTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// buildTelemetryFromConfig construye el logger de telemetría desde config
// (009-000 P2/P8, R8 del audit — patrón newHTTPServer de SEC-001):
//   - enabled=false → (nil, nil, nil) sin abrir archivo (C1/C11).
//   - enabled=true + file → (logger, closer, nil).
//   - enabled=true + file="" → error (fail-fast, C10/I5).
//
// Con sample_rate < 1 el logger muestrea la EMISIÓN del evento (P9): un
// handler descarta una fracción 1-rate de los records con aleatorio
// (mecanismo a elección del implementer; C9 valida la banda resultante).
func buildTelemetryFromConfig(cfg *config.Config) (*slog.Logger, io.Closer, error) {
	if cfg == nil || !cfg.Telemetry.Enabled {
		return nil, nil, nil
	}
	if cfg.Telemetry.File == "" {
		return nil, nil, fmt.Errorf("telemetry: enabled=true requiere file")
	}
	logger, closer, err := logging.BuildTelemetry(cfg.Telemetry.File)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Telemetry.SampleRate < 1 {
		logger = slog.New(samplingHandler{next: logger.Handler(), keep: cfg.Telemetry.SampleRate})
	}
	return logger, closer, nil
}

// samplingHandler descarta una fracción de records (muestreo de la
// emisión, 009-000 P9). Aleatorio (math/rand global, seguro para uso
// concurrente); keep = probabilidad de emitir (sample_rate).
type samplingHandler struct {
	next slog.Handler
	keep float64
}

func (h samplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h samplingHandler) Handle(ctx context.Context, r slog.Record) error {
	if rand.Float64() >= h.keep {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h samplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return samplingHandler{next: h.next.WithAttrs(attrs), keep: h.keep}
}

func (h samplingHandler) WithGroup(name string) slog.Handler {
	return samplingHandler{next: h.next.WithGroup(name), keep: h.keep}
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
