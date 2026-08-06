// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package proxy arma el servidor HTTP OpenAI-compatible único (feature
// 001-001-endpoint): POST /v1/chat/completions (no-stream y stream),
// GET /v1/models, GET /healthz y GET /metrics. Conecta los paquetes:
// auth (001-007) protege /v1/*, el router (001-003) hace fallback,
// clamp (001-004) reescribe max_tokens, stream (001-005) pasa SSE,
// timeouts (001-006) acotan cada intento.
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ofapsaas/mofgw/internal/absorb"
	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/limiter"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/router"
	"github.com/ofapsaas/mofgw/internal/stream"
)

// ModelPricing es la tabla de precios por millón de tokens de un modelo
// (006-002-costos). Valores en USD por 1M tokens. Cero = sin precio para
// esa componente (contribuye 0 al costo).
type ModelPricing struct {
	InputUSDPerM    float64
	OutputUSDPerM   float64
	CacheHitUSDPerM float64
}

// Server es el proxy HTTP. Los campos son inmutables post-New (seguro
// para uso concurrente).
type Server struct {
	router    *router.Router
	providers []provider.Provider // para /v1/models
	auth      *auth.Authenticator
	metrics   *metrics.Metrics
	logger    *slog.Logger
	maxBody   int64

	// pricing: tabla de precios por modelo (006-002-costos). Inmutable
	// tras SetPricing (antes del tráfico); lectura concurrente segura
	// (se asigna una sola vez, nunca se muta el mapa después).
	pricing map[string]ModelPricing

	// modelMeta: capabilities por modelo (007-001-model-metadata).
	// Inmutable tras SetModelMetadata; mismo patrón que pricing.
	modelMeta map[string]config.ModelMetadata

	// contextMargin: fracción de margen sobre context_window para el
	// rechazo por ventana (008-001 P3). 0 = sin margen (rechazo en el
	// límite exacto). Default 0.1 (seteado en New).
	contextMargin float64

	// budgets: límite de consumo por clientID (008-002-budget). keyed
	// por clientID (como están en config.clients[].ID). Inmutable tras
	// SetBudget.
	budgets map[string]config.BudgetConfig

	// Concurrencia (004-001/004-002): global + keyed por cliente/agente.
	globalLimiter       *limiter.Limiter
	keyedLimiter        *limiter.Keyed
	backpressureTimeout time.Duration
}

// New construye el Server. Los parámetros de concurrencia se pasan ya
// construidos (nil globalLimiter = sin límite global; keyedLimiter nil =
// sin aislamiento por cliente — backward compatible con EPIC-001/002/005).
func New(r *router.Router, providers []provider.Provider, a *auth.Authenticator, m *metrics.Metrics, logger *slog.Logger, maxBodyBytes int64, globalLimiter *limiter.Limiter, keyedLimiter *limiter.Keyed, backpressureTimeout time.Duration) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if m == nil {
		m = metrics.New()
	}
	return &Server{router: r, providers: providers, auth: a, metrics: m, logger: logger, maxBody: maxBodyBytes, globalLimiter: globalLimiter, keyedLimiter: keyedLimiter, backpressureTimeout: backpressureTimeout, contextMargin: 0.1}
}

// Handler devuelve el mux con auth aplicada a /v1/* (healthz y metrics
// quedan públicos, loopback).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	// rutas no declaradas en el mux → 404 OpenAI-compatible
	return s.auth.Wrap(withRequestID(mux), "/healthz", "/metrics")
}

// SetPricing configura la tabla de precios por modelo (006-002-costos).
// Debe llamarse una sola vez antes del tráfico; el mapa no se muta
// después (lecturas concurrentes seguras).
func (s *Server) SetPricing(pricing map[string]ModelPricing) {
	s.pricing = pricing
}

// SetModelMetadata configura las capabilities por modelo
// (007-001-model-metadata). Mismo contrato que SetPricing: una sola vez
// antes del tráfico, mapa inmutable después.
func (s *Server) SetModelMetadata(meta map[string]config.ModelMetadata) {
	s.modelMeta = meta
}

// SetContextMargin configura el margen del rechazo por ventana
// (008-001 P3): fracción 0-1 sobre context_window. 0 = sin margen.
// Debe llamarse antes del tráfico (lectura concurrente segura después).
func (s *Server) SetContextMargin(margin float64) {
	s.contextMargin = margin
}

// SetBudget configura los límites de consumo por cliente (008-002 P1).
// Mismo contrato que SetPricing: antes del tráfico, mapa inmutable
// después.
func (s *Server) SetBudget(budgets map[string]config.BudgetConfig) {
	s.budgets = budgets
}

// budgetExceeded evalúa si el consumo acumulado del cliente excede su
// budget (008-002 P2-P4). Devuelve la razón del exceso ("" = no excede).
// Sin budget → "". Costo y tokens del snapshot (misma fuente que
// /v1/usage, 006-001/006-002).
func (s *Server) budgetExceeded(clientID string) string {
	b, ok := s.budgets[clientID]
	if !ok {
		return ""
	}
	snap := s.metrics.SnapshotClient(clientID)
	if b.CostUSDMax > 0 && snap.CostUSD >= b.CostUSDMax {
		return "budget_cost"
	}
	if b.TokensMax > 0 && snap.TotalTokens >= b.TokensMax {
		return "budget_tokens"
	}
	return ""
}

// estimatePromptTokens estima los tokens del prompt de un body crudo:
// len/4 (~4 chars/token promedio). Estimación rough documentada (008-001
// P2) — el margen de seguridad cubre la imprecisión.
func estimatePromptTokens(body []byte) int {
	return len(body) / 4
}

// exceedsContextWindow decide si un request excede la ventana de contexto
// del modelo destino (008-001 P1/P3). Sin metadata → false (sin rechazo).
// El margen se aplica como context_window × (1 + margin).
func (s *Server) exceedsContextWindow(model string, promptTokens int) bool {
	md, ok := s.modelMeta[model]
	if !ok || md.ContextWindow <= 0 {
		return false
	}
	limit := float64(md.ContextWindow) * (1 + s.contextMargin)
	return float64(promptTokens) > limit
}

// withRequestID inyecta un id de request por request (observabilidad).
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := logging.WithRequestID(r.Context(), newRequestID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// openAIError escribe un error en formato OpenAI-compatible.
func openAIError(w http.ResponseWriter, status int, message, typ string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": message, "type": typ, "code": nil},
	})
}

// handleChat es el endpoint principal.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger := logging.WithRequest(s.logger, r.Context())
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = sr

	var providerID string
	model := ""
	var lastUsage *provider.Usage
	defer func() {
		s.emitRequestEnd(logger, start, sr.status, providerID, model, lastUsage)
	}()

	s.metrics.IncRequests()

	// 1. Leer body con límite (413 si excede)
	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBody+1))
	if err != nil {
		openAIError(w, http.StatusBadRequest, "error reading request body", "invalid_request_error")
		return
	}
	if int64(len(body)) > s.maxBody {
		openAIError(w, http.StatusRequestEntityTooLarge, "request body too large", "invalid_request_error")
		return
	}

	// 2. Validar + vista de ruteo
	req, err := provider.ParseChatRequest(body)
	if err != nil {
		openAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	model = req.Model

	// 2.5. Límites de concurrencia (004-001/004-002): adquirir slots
	// DESPUÉS de auth (clientID viene del context) y parsing, ANTES de
	// tocar el router. El global espera con timeout (backpressure); los
	// límites por cliente/agente son fast-fail (aislamiento: un cliente
	// que excede su budget no hace esperar a los demás).
	clientID := auth.ClientIDFrom(r.Context())
	if s.globalLimiter != nil {
		ctx, cancel := context.WithTimeout(r.Context(), s.backpressureTimeout)
		if !s.globalLimiter.Acquire(ctx) {
			cancel()
			s.metrics.IncRejected(clientID, "global_timeout")
			openAIError(w, http.StatusTooManyRequests, "server busy: concurrency limit reached", "rate_limit_exceeded")
			return
		}
		cancel()
		defer s.globalLimiter.Release()
	}
	if s.keyedLimiter != nil {
		if release, ok := s.keyedLimiter.AcquireClient(clientID); !ok {
			s.metrics.IncRejected(clientID, "client_limit")
			openAIError(w, http.StatusTooManyRequests, "client concurrency limit reached", "rate_limit_exceeded")
			return
		} else if release != nil {
			defer release()
		}
		agentID := r.Header.Get("X-Agent-Id")
		if release, ok := s.keyedLimiter.AcquireAgent(clientID, agentID); !ok {
			rejectedLabel := clientID
			if agentID != "" {
				rejectedLabel = clientID + "/" + agentID
			}
			s.metrics.IncRejected(rejectedLabel, "agent_limit")
			openAIError(w, http.StatusTooManyRequests, "agent concurrency limit reached", "rate_limit_exceeded")
			return
		} else if release != nil {
			defer release()
		}
	}

	// 3. Contar tools del body crudo (nunca loguear contenido)
	var rawTools struct {
		Tools []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &rawTools)

	// 4. Evento request_start (debug)
	logger.Debug("request_start",
		"method", r.Method,
		"path", r.URL.Path,
		"stream", req.Stream,
		"model", req.Model,
		"num_messages", len(req.Messages),
		"num_tools", len(rawTools.Tools),
	)

	// 4.5 Rechazo temprano por ventana de contexto (008-001 P1): si el
	// prompt estimado excede la ventana del modelo (con margen), 400 sin
	// gastar intentos de la cadena. Sin metadata → sin rechazo (P4).
	if promptTokens := estimatePromptTokens(body); s.exceedsContextWindow(req.Model, promptTokens) {
		s.metrics.IncRejected(clientID, "context_window")
		msg := fmt.Sprintf("estimated prompt tokens %d exceed context window %d for model %q", promptTokens, s.modelMeta[req.Model].ContextWindow, req.Model)
		openAIError(w, http.StatusBadRequest, msg, "invalid_request_error")
		logger.Debug("context_window_rejected", "model", req.Model, "estimated", promptTokens)
		return
	}

	// 4.6 Budget por cliente (008-002 P2): si el consumo acumulado del
	// cliente excede su límite, 429 sin tocar router ni upstream.
	if reason := s.budgetExceeded(clientID); reason != "" {
		s.metrics.IncRejected(clientID, reason)
		openAIError(w, http.StatusTooManyRequests, "budget exceeded: "+reason, "rate_limit_exceeded")
		logger.Debug("budget_rejected", "client", clientID, "reason", reason)
		return
	}

	ctx := r.Context()

	// 5. Streaming o no-stream
	if req.Stream {
		s.metrics.IncStreams()
		providerID, streamUsage := s.handleStream(ctx, w, r, req, body, logger)
		lastUsage = streamUsage
		if providerID != "" {
			s.recordCacheTokens(clientID, providerID, req.Model, streamUsage)
		}
		return
	}

	// 6. No-stream: router.Complete
	res, err := s.router.Complete(ctx, req, body)
	if err != nil {
		s.handleChainError(w, err, logger)
		return
	}
	providerID = res.ProviderID
	s.metrics.SetLastProvider(res.ProviderID)
	s.recordCacheTokens(clientID, res.ProviderID, req.Model, &res.Response.Usage)
	lastUsage = &res.Response.Usage
	// headers de usage (007-003 P1): el cliente ve su consumo por request
	s.setUsageHeaders(w.Header(), res.ProviderID, req.Model, &res.Response.Usage)
	// reescribir model → el que pidió el cliente (no el alias interno)
	out := rewriteResponseModel(res.Raw, req.Model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// setUsageHeaders expone el consumo del request en headers X-Usage-*
// (007-003 P1/P2). No-stream: valores exactos. Stream: se llama antes
// del primer byte con el usage en curso (0 si aún no llegó).
func (s *Server) setUsageHeaders(h http.Header, providerID, model string, u *provider.Usage) {
	prompt, completion, total, cacheHit := 0, 0, 0, 0
	cost := 0.0
	if u != nil {
		prompt = u.PromptTokens
		completion = u.CompletionTokens
		total = u.TotalTokens
		cacheHit = u.CachedTokens
		miss := u.PromptTokens - u.CachedTokens
		if miss < 0 {
			miss = 0
		}
		cost = s.estimateCost(model, int64(miss), int64(u.CompletionTokens), int64(u.CachedTokens))
	}
	h.Set("X-Usage-Prompt-Tokens", strconv.Itoa(prompt))
	h.Set("X-Usage-Completion-Tokens", strconv.Itoa(completion))
	h.Set("X-Usage-Total-Tokens", strconv.Itoa(total))
	h.Set("X-Usage-Cache-Hit-Tokens", strconv.Itoa(cacheHit))
	h.Set("X-Usage-Cost-USD", fmt.Sprintf("%.6f", cost))
}

// recordCacheTokens acumula los tokens de cache/reasoning de un request
// en /metrics (003-001 P3), los tokens totales por cliente (006-001
// P1-P3) y el costo estimado (006-002 P2-P3). miss = prompt - cached
// (los proveedores OpenAI-compatibles no reportan miss explícito; el
// estándar es prompt_tokens_details.cached_tokens). Nunca crashea con
// usage ausente ni con modelo sin pricing.
func (s *Server) recordCacheTokens(clientID, providerID, model string, u *provider.Usage) {
	hit, miss, reasoning := int64(0), int64(0), int64(0)
	prompt, completion, total := int64(0), int64(0), int64(0)
	if u != nil {
		hit = int64(u.CachedTokens)
		if d := int64(u.PromptTokens - u.CachedTokens); d > 0 {
			miss = d
		}
		reasoning = int64(u.ReasoningTokens)
		prompt = int64(u.PromptTokens)
		completion = int64(u.CompletionTokens)
		total = int64(u.TotalTokens)
	}
	s.metrics.IncCacheTokens(providerID, model, hit, miss, reasoning)
	s.metrics.IncUsage(clientID, providerID, model, prompt, completion, total)
	s.metrics.IncCost(clientID, providerID, model, s.estimateCost(model, miss, completion, hit))
}

// estimateCost calcula el costo estimado en USD de un request usando la
// tabla de precios (006-002 P2). miss/completion/hit en tokens → /1e6 ×
// precio. Modelo sin entrada en pricing → 0. Nunca devuelve negativo.
func (s *Server) estimateCost(model string, miss, completion, hit int64) float64 {
	p, ok := s.pricing[model]
	if !ok {
		return 0
	}
	cost := float64(miss)/1e6*p.InputUSDPerM +
		float64(completion)/1e6*p.OutputUSDPerM +
		float64(hit)/1e6*p.CacheHitUSDPerM
	if cost < 0 {
		return 0
	}
	return cost
}

// emitRequestEnd loguea el evento request_end al finalizar un handler.
// Incluye los campos de cache/reasoning (003-001 P4) y los totales
// prompt/completion/total (006-001 P4) cuando el usage upstream está
// disponible.
func (s *Server) emitRequestEnd(logger *slog.Logger, start time.Time, statusCode int, providerID, model string, u *provider.Usage) {
	logger.Debug("request_end",
		"status_code", statusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"provider", providerID,
		"model", model,
		"prompt_tokens", tokensOrZero(u, "prompt"),
		"completion_tokens", tokensOrZero(u, "completion"),
		"total_tokens", tokensOrZero(u, "total"),
		"cache_hit_tokens", cacheTokensOrZero(u, true),
		"cache_miss_tokens", cacheTokensOrZero(u, false),
		"reasoning_tokens", reasoningTokensOrZero(u),
	)
}

// tokensOrZero devuelve prompt/completion/total del usage (0 si nil).
func tokensOrZero(u *provider.Usage, kind string) int {
	if u == nil {
		return 0
	}
	switch kind {
	case "prompt":
		return u.PromptTokens
	case "completion":
		return u.CompletionTokens
	default:
		return u.TotalTokens
	}
}

func cacheTokensOrZero(u *provider.Usage, hit bool) int {
	if u == nil {
		return 0
	}
	if hit {
		return u.CachedTokens
	}
	if d := u.PromptTokens - u.CachedTokens; d > 0 {
		return d
	}
	return 0
}

func reasoningTokensOrZero(u *provider.Usage) int {
	if u == nil {
		return 0
	}
	return u.ReasoningTokens
}

// statusRecorder captura el código de estado HTTP real de la respuesta.
// Implementa http.Flusher delegando al ResponseWriter subyacente para que
// el streaming SSE funcione (stream.NewWriter hace type assertion a
// http.Flusher).
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// Flush delega al ResponseWriter subyacente si implementa http.Flusher
// (necesario para el streaming SSE).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handleStream: router.Stream + passthrough SSE con frontera primer-byte.
// Devuelve el ID del provider que sirvió el stream ("" si falló) y el
// usage capturado del chunk final (003-001 P4; nil si el upstream no lo
// mandó o el stream falló).
func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, r *http.Request, req *provider.ChatRequest, body []byte, logger *slog.Logger) (string, *provider.Usage) {
	res, err := s.router.Stream(ctx, req, body)
	if err != nil {
		s.handleChainError(w, err, logger)
		return "", nil
	}
	s.metrics.SetLastProvider(res.Provider.ID())
	sw, err := stream.NewWriter(w)
	if err != nil {
		openAIError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return "", nil
	}
	// headers de usage ANTES del primer byte (007-003 P4): el usage del
	// chunk final no puede ir en headers (ya flusheados) — va en el body
	// SSE (003-001 lo pasa). Acá van 0/valores del request en curso.
	s.setUsageHeaders(w.Header(), res.Provider.ID(), req.Model, nil)
	events := stream.CaptureUsage(res.Events)
	var usage *provider.Usage
	if err := sw.Copy(events.Events, req.Model); err != nil {
		logger.Warn("stream interrumpido", "provider", res.Provider.ID(), "err", err)
	} else {
		// Solo leer usage en path limpio: el stream se drenó completo
		// (close del canal sincroniza las escrituras de la goroutine).
		// Si Copy retornó por error, la goroutine puede seguir
		// escribiendo cs.usage → no leer (evita race, H1 review).
		usage = events.Usage()
	}
	return res.Provider.ID(), usage
}

// handleChainError traduce errores del router a respuesta
// OpenAI-compatible. 002-003: el cliente NUNCA ve el error crudo del
// provider — absorb sanea el mensaje (sin URLs/keys/stack traces),
// mapea el status (5xx/429 → 502, 401/403 de provider → 502
// provider_auth_error) y responde. El detalle completo queda en logs.
func (s *Server) handleChainError(w http.ResponseWriter, err error, logger *slog.Logger) {
	var ce *router.ChainError
	if errors.As(err, &ce) {
		s.metrics.IncErrors()
		logger.Warn("chain error",
			"status", ce.Status,
			"type", ce.Type,
			"code", ce.Code,
			"provider", ce.ProviderID,
			"detail", ce.Err,
		)
	} else {
		s.metrics.IncErrors()
		logger.Error("error interno", "err", err)
	}
	absorb.Respond(w, err)
}

// handleModels lista los modelos de todos los providers configurados
// (deduplicados).
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	var models []map[string]any
	for _, p := range s.providers {
		if c, ok := p.(*provider.Client); ok {
			for _, m := range c.Models() {
				if !seen[m] {
					seen[m] = true
					models = append(models, s.modelCatalogEntry(m))
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
}

// modelCatalogEntry arma el objeto /v1/models de un modelo, enriquecido
// con la metadata declarativa (007-001) y el pricing (006-002) cuando
// existen (007-002 P1-P3). Sin metadata/pricing → formato mínimo
// (backward compatible, P4). Los campos extra siguen las convenciones
// que leen opencode/openclaw (research §3): context_length,
// max_context_length, loaded_context_length, max_completion_tokens,
// capabilities.reasoning, thinking.levels/default.
func (s *Server) modelCatalogEntry(model string) map[string]any {
	entry := map[string]any{
		"id":       model,
		"object":   "model",
		"created":  time.Now().Unix(),
		"owned_by": "mofgw",
	}
	md, hasMeta := s.modelMeta[model]
	if hasMeta {
		cw := md.ContextWindow
		entry["context_length"] = cw
		entry["context_window"] = cw
		entry["max_context_length"] = cw
		entry["loaded_context_length"] = cw
		entry["max_completion_tokens"] = md.MaxOutput
		caps := map[string]any{"reasoning": len(md.Thinking) > 0}
		entry["capabilities"] = caps
		if len(md.Thinking) > 0 {
			levels := make([]string, len(md.Thinking))
			copy(levels, md.Thinking)
			think := map[string]any{"levels": levels}
			if md.ThinkingDefault != "" {
				think["default"] = md.ThinkingDefault
			}
			entry["thinking"] = think
		}
	}
	if p, ok := s.pricing[model]; ok {
		entry["pricing"] = map[string]any{
			"input_usd_per_m":     p.InputUSDPerM,
			"output_usd_per_m":    p.OutputUSDPerM,
			"cache_hit_usd_per_m": p.CacheHitUSDPerM,
		}
	}
	return entry
}

// handleUsage expone los agregados de consumo del cliente autenticado
// (007-003 P3): GET /v1/usage con auth Bearer → {client, totals,
// by_model}. Cada cliente ve SOLO lo suyo (aislamiento por clientID).
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	clientID := auth.ClientIDFrom(r.Context())
	snap := s.metrics.SnapshotClient(clientID)
	byModel := make([]map[string]any, 0, len(snap.ByModel))
	for _, ms := range snap.ByModel {
		byModel = append(byModel, map[string]any{
			"model":             ms.Model,
			"prompt_tokens":     ms.PromptTokens,
			"completion_tokens": ms.CompletionTokens,
			"total_tokens":      ms.TotalTokens,
			"cost_usd":          ms.CostUSD,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client": clientID,
		"totals": map[string]any{
			"prompt_tokens":     snap.PromptTokens,
			"completion_tokens": snap.CompletionTokens,
			"total_tokens":      snap.TotalTokens,
			"cache_hit_tokens":  snap.CacheHitTokens,
			"cost_usd":          snap.CostUSD,
		},
		"by_model": byModel,
	})
}

// handleHealth es el healthcheck sin auth (loopback). Con health store
// activo (002-002) expone el detalle por provider y 503 si todos están
// unhealthy; sin store (modo EPIC-001) responde el 200 genérico.
// Responde SIEMPRE desde estado en memoria — sin I/O de red on-demand.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	hs := s.router.Health()
	if hs == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		return
	}
	snap := hs.Snapshot()
	if len(snap) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "providers": map[string]any{}})
		return
	}
	providers := make(map[string]any, len(snap))
	allDown := true
	for id, st := range snap {
		// Cold start: un provider nunca sondeado es desconocido, no down.
		healthy := st.LastCheck.IsZero() || st.Healthy
		if healthy {
			allDown = false
		}
		providers[id] = map[string]any{
			"healthy":              healthy,
			"last_check":           st.LastCheck.UTC().Format(time.RFC3339),
			"last_latency_ms":      st.LastLatency.Milliseconds(),
			"consecutive_failures": st.ConsecutiveFailures,
		}
	}
	status := "ok"
	code := http.StatusOK
	if allDown {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "providers": providers})
}

// handleMetrics expone contadores sin auth (loopback).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = io.WriteString(w, s.metrics.Render())
}

// rewriteResponseModel reemplaza el campo model del body crudo de la
// respuesta upstream por el modelo que pidió el cliente.
func rewriteResponseModel(raw []byte, model string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return raw
	}
	m["model"] = modelJSON
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
