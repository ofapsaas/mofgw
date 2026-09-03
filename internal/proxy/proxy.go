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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ofapsaas/mofgw/internal/absorb"
	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/clamp"
	"github.com/ofapsaas/mofgw/internal/composition"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/embeddings"
	"github.com/ofapsaas/mofgw/internal/limiter"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/registry"
	"github.com/ofapsaas/mofgw/internal/respcache"
	"github.com/ofapsaas/mofgw/internal/router"
	"github.com/ofapsaas/mofgw/internal/singleflight"
	"github.com/ofapsaas/mofgw/internal/stream"
	"github.com/ofapsaas/mofgw/internal/websearch"
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
	// telemetryLogger: destino dedicado de la telemetría de descubrimiento
	// (009-000). nil = telemetría deshabilitada (C1/C12). Inmutable post-New.
	telemetryLogger *slog.Logger
	maxBody         int64

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

	// contextAnalysis: análisis estructural del contexto habilitado
	// (009-001 P6). Seteado pre-tráfico por SetContextAnalysis (patrón
	// SetContextMargin); false = sin records ni memoria (C10).
	contextAnalysis bool

	// stickyRouting: afinidad de provider por sesión/cliente habilitada
	// (009-002 P1). Seteado pre-tráfico por SetStickyRouting; false
	// (default) → el proxy usa Complete/Stream legacy (cero cambio
	// observable, P8) y el store de afinidad queda vacío (C11).
	stickyRouting bool

	// budgets: límite de consumo por clientID (008-002-budget). keyed
	// por clientID (como están en config.clients[].ID). Inmutable tras
	// SetBudget.
	budgets map[string]config.BudgetConfig

	// singleFlightEnabled: coalescing de requests idénticos concurrentes
	// (010-001 P0). Inmutable tras SetSingleFlight (antes del tráfico);
	// false (default) → el flujo legacy queda intacto (cero cambio
	// observable).
	singleFlightEnabled bool
	flights             *singleflight.Group

	// responseCacheEnabled: cache exact-match de respuestas (010-002 P1).
	// Inmutable tras SetResponseCache (antes del tráfico); false (default)
	// → flujo legacy intacto (cero cambio observable).
	responseCacheEnabled bool
	responseCache        *respcache.Cache

	// Concurrencia (004-001/004-002): global + keyed por cliente/agente.
	globalLimiter       *limiter.Limiter
	keyedLimiter        *limiter.Keyed
	backpressureTimeout time.Duration

	// webSearchEnabled + webSearchClient: grounded search server-side
	// (011-005 P1/D5). Inmutables tras SetWebSearch (antes del tráfico);
	// enabled=false (default) → `web_search_preview` conserva el 400 de D3
	// de 003 (sin regresión). webSearchMaxResults: tope de resultados
	// inyectados (P3/P4, web_search.max_results; 0 = default 3).
	webSearchEnabled    bool
	webSearchClient     websearch.Client
	webSearchMaxResults int

	// embeddingsClient + embeddingsModels: forward de embeddings a Ollama
	// (011-006 P1/P3/D3). embeddingsClient es el cliente HTTP dedicado
	// apuntado a base_url+"/embeddings" (I2: nunca api.openai.com); nil =
	// embeddings no configurados → 502 upstream (P9). embeddingsModels es el
	// modelo de embeddings forzado por cliente (P3), keyed por clientID;
	// cliente sin modelo → 400 (P4). Ambos inmutables tras
	// SetEmbeddings/SetClientEmbeddingsModel (antes del tráfico, patrón
	// SetBudget).
	embeddingsClient embeddings.Client
	embeddingsModels map[string]string

	// registry: writer del registro unificado de accounting + outcome
	// (014-001). nil = off (sin emisión de eventos terminal, I3/I6).
	// Seteado pre-tráfico vía SetRegistry; lectura concurrente segura.
	registry *registry.Writer

	// clientConfigBaseURL + clientConfigKeyEnv: knobs del endpoint
	// /v1/client-config (016-001). Inmutables tras SetClientConfig (antes
	// del tráfico, patrón SetContextAnalysis). BaseURL vacío → el endpoint
	// responde 503 en runtime (I4: no fallback silencioso, no fail-fast de
	// arranque). KeyEnv es la REFERENCIA (nombre de env var), nunca el valor.
	clientConfigBaseURL string
	clientConfigKeyEnv  string
}

// New construye el Server. Los parámetros de concurrencia se pasan ya
// construidos (nil globalLimiter = sin límite global; keyedLimiter nil =
// sin aislamiento por cliente — backward compatible con EPIC-001/002/005).
// telemetryLogger nil = telemetría deshabilitada (009-000 P2).
func New(r *router.Router, providers []provider.Provider, a *auth.Authenticator, m *metrics.Metrics, logger *slog.Logger, telemetryLogger *slog.Logger, maxBodyBytes int64, globalLimiter *limiter.Limiter, keyedLimiter *limiter.Keyed, backpressureTimeout time.Duration) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if m == nil {
		m = metrics.New()
	}
	return &Server{router: r, providers: providers, auth: a, metrics: m, logger: logger, telemetryLogger: telemetryLogger, maxBody: maxBodyBytes, globalLimiter: globalLimiter, keyedLimiter: keyedLimiter, backpressureTimeout: backpressureTimeout, contextMargin: 0.1, flights: singleflight.New(512), webSearchMaxResults: 3}
}

// SetSingleFlight configura el coalescing de requests idénticos
// concurrentes (010-001 P0). Debe llamarse antes del tráfico
// (patrón SetStickyRouting). enabled=false (default) → flujo legacy
// intacto. maxFlights <= 0 → default 512.
func (s *Server) SetSingleFlight(enabled bool, maxFlights int) {
	s.singleFlightEnabled = enabled
	s.flights = singleflight.New(maxFlights)
}

// SetResponseCache configura el cache exact-match de respuestas
// (010-002 P1). Debe llamarse antes del tráfico (patrón SetStickyRouting).
// enabled=false (default) → flujo legacy intacto. maxEntries <= 0 → 512;
// ttl <= 0 → 5m (defaults del paquete respcache).
func (s *Server) SetResponseCache(enabled bool, maxEntries int, ttl time.Duration) {
	s.responseCacheEnabled = enabled
	s.responseCache = respcache.New(maxEntries, ttl)
}

// SetWebSearch configura el grounded search server-side (011-005 P1/D5).
// Debe llamarse antes del tráfico (patrón SetResponseCache). enabled=false
// (default) → `web_search_preview` conserva el 400 de D3 de 003 (sin
// regresión); enabled=true + client → flujo web search activo.
//
// client es la interfaz tipada `websearch.Client`: el mock del test
// (proxy_test) devuelve `[]websearch.Result` — el tipo de producción — y así
// la satisface estructuralmente. Sin `any` ni reflexión (Make Illegal States
// Unrepresentable, Fail Loud).
func (s *Server) SetWebSearch(enabled bool, client websearch.Client) {
	s.webSearchEnabled = enabled
	s.webSearchClient = client
}

// SetWebSearchMaxResults configura el tope de resultados inyectados por
// request web search (011-005 P3/P4, web_search.max_results). Debe llamarse
// antes del tráfico. n <= 0 → default 3.
func (s *Server) SetWebSearchMaxResults(n int) {
	if n > 0 {
		s.webSearchMaxResults = n
	}
}

// SetEmbeddings configura el cliente HTTP de embeddings (011-006 P1/D3):
// apunta el forward a `baseURL+"/embeddings"` (nunca api.openai.com, I2).
// apiKey es opcional (Ollama local sin key por default). Debe llamarse antes
// del tráfico (patrón SetWebSearch); con baseURL vacía no se configura
// (embeddingsClient queda nil → 502 upstream, P9).
func (s *Server) SetEmbeddings(baseURL, apiKey string) {
	if baseURL == "" {
		return
	}
	s.embeddingsClient = embeddings.New(baseURL, apiKey, 0)
}

// SetClientEmbeddingsModel configura el modelo de embeddings forzado por
// cliente (011-006 P3): `model` es el que mofgw manda a Ollama (ignorando el
// `model` de Odoo) y el que aparece en el envelope de respuesta. Cliente sin
// modelo configurado → 400 "no embeddings model configured for client" (P4).
// Debe llamarse antes del tráfico (mapa inmutable después, patrón SetBudget).
func (s *Server) SetClientEmbeddingsModel(clientID, model string) {
	if s.embeddingsModels == nil {
		s.embeddingsModels = make(map[string]string)
	}
	s.embeddingsModels[clientID] = model
}

// embeddingsModelFor devuelve el modelo de embeddings configurado para el
// clientID (P3), o "" si no tiene (→ 400, P4). Sin default global silencioso.
func (s *Server) embeddingsModelFor(clientID string) string {
	return s.embeddingsModels[clientID]
}

// responseCacheKey arma la key del cache exact-match (010-002 P1):
// sha256 de clientID + JSON canónico del body (mapa re-marshalado con
// claves ordenadas — json.Marshal de map ordena claves, canónico
// independiente del orden de llegada del cliente). clientID en la key =
// partición por cliente: cero fuga entre clientes aunque los prompts
// sean idénticos. Un body no-JSON no puede cachearse (error → miss).
func responseCacheKey(clientID string, body []byte) (string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(clientID))
	h.Write([]byte{0})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// singleFlightKey arma la key de coalescing (010-001 P0): sha256 de
// método + path + body crudo (canónico). Body idéntico al mismo endpoint
// → misma key. Sin clientID: la respuesta de un request determinístico es
// función del request, no del cliente — el coalescing no filtra datos
// (el follower recibe una respuesta válida para SU request idéntico).
func singleFlightKey(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("|"))
	h.Write([]byte(path))
	h.Write([]byte("|"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// storeResponseCache guarda la respuesta en el cache exact-match
// (010-002 P1). No-op si el cache está deshabilitado o la key es ""
// (request no elegible o body no-JSON). La entrada replica el body
// completo (incl. usage) y el createdAt para X-Mofgw-Cache-Age.
func (s *Server) storeResponseCache(key, clientID string, status int, contentType string, body []byte) {
	if !s.responseCacheEnabled || key == "" {
		return
	}
	s.responseCache.Set(key, &respcache.Entry{
		StatusCode:  status,
		ContentType: contentType,
		Body:        body,
		CreatedAt:   time.Now(),
	})
	s.metrics.IncRespCacheStore(clientID)
}

// telemetryHeaderAllowlist: únicos headers capturados en el evento
// request_telemetry (009-000 P5).
var telemetryHeaderAllowlist = map[string]bool{
	"X-Session-Id":       true,
	"X-Agent-Id":         true,
	"X-Session-Affinity": true,
	"User-Agent":         true,
	"Content-Length":     true,
	"Content-Type":       true,
	"Accept":             true,
}

// emitRequestTelemetry emite el evento request_telemetry (009-000 P3-P5):
// un evento por request con body válido, con el schema exacto de P4 (sin
// otros campos, R10). El request_id lo adjunta logging.WithRequest desde
// el context. Best-effort: nunca falla el request (I7).
func (s *Server) emitRequestTelemetry(r *http.Request, req *provider.ChatRequest, clientID string, body []byte, logger *slog.Logger) {
	if s.telemetryLogger == nil {
		return
	}
	keyPaths, detected, _ := composition.Walk(body, 10) // default maxDepth (P6/I4)
	paths := make([]string, len(keyPaths))
	for i, p := range keyPaths {
		paths[i] = string(p)
	}
	roles := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		var msg struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(m, &msg) == nil && msg.Role != "" {
			roles = append(roles, msg.Role)
		}
	}
	var rawTools struct {
		Tools []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &rawTools)

	logging.WithRequest(s.telemetryLogger, r.Context()).Info("request_telemetry",
		"ts", time.Now().Format(time.RFC3339),
		"client_id", clientID,
		"model", req.Model,
		"stream", req.Stream,
		"headers", s.captureTelemetryHeaders(r, logger),
		"key_paths", paths,
		"num_messages", len(req.Messages),
		"num_tools", len(rawTools.Tools),
		"roles", roles,
		"detected_ids", detected,
	)
}

// captureTelemetryHeaders captura solo los headers de la allowlist
// presentes (P5). Content-Length se lee de r.ContentLength (R2: Go no lo
// expone en r.Header; el contrato es la longitud del body). Todo header
// X-* fuera de la allowlist se niega y dispara un warn al logger
// OPERATIVO con el NOMBRE (nunca el valor, I2). Los headers prohibidos
// (Authorization, Cookie, X-Forwarded-For, X-Api-Key, X-Real-Ip, X-Host,
// Forwarded, Via) jamás se capturan, aunque estén presentes.
func (s *Server) captureTelemetryHeaders(r *http.Request, logger *slog.Logger) map[string]string {
	headers := make(map[string]string)
	for _, name := range []string{"X-Session-Id", "X-Agent-Id", "X-Session-Affinity", "User-Agent", "Content-Type", "Accept"} {
		if v := r.Header.Get(name); v != "" {
			headers[name] = v
		}
	}
	if r.ContentLength > 0 {
		headers["Content-Length"] = strconv.FormatInt(r.ContentLength, 10)
	}
	for name := range r.Header {
		if strings.HasPrefix(name, "X-") && !telemetryHeaderAllowlist[name] {
			logger.Warn("telemetry header negado", "header", name)
		}
	}
	return headers
}

// Handler devuelve el mux con auth aplicada a /v1/* (healthz y metrics
// quedan públicos, loopback).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)   // 011-001
	mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings) // 011-006
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("GET /v1/context", s.handleContext)
	mux.HandleFunc("GET /v1/client-config", s.handleClientConfig) // 016-001
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
// antes del tráfico, mapa inmutable después. También propaga la metadata
// al router (010-002 P4): la inyección per-attempt del thinking_default
// vive en el router y necesita la misma fuente (un solo call-site).
func (s *Server) SetModelMetadata(meta map[string]config.ModelMetadata) {
	s.modelMeta = meta
	s.router.SetModelMeta(meta)
}

// SetContextMargin configura el margen del rechazo por ventana
// (008-001 P3): fracción 0-1 sobre context_window. 0 = sin margen.
// Debe llamarse antes del tráfico (lectura concurrente segura después).
func (s *Server) SetContextMargin(margin float64) {
	s.contextMargin = margin
}

// SetContextAnalysis configura el análisis estructural del contexto
// (009-001 P6): enabled=false (default) → sin records en memoria y
// /v1/context responde 200 con ceros/vacío. historyPerSession se propaga
// al ring buffer de Metrics (patrón SetMaxSessionsRetained). Debe
// llamarse antes del tráfico.
func (s *Server) SetContextAnalysis(enabled bool, historyPerSession int) {
	s.contextAnalysis = enabled
	s.metrics.SetContextHistoryPerSession(historyPerSession)
}

// SetStickyRouting configura la afinidad de provider por sesión/cliente
// (009-002 P1). Debe llamarse antes del tráfico (lectura concurrente
// segura después). false (default) → el proxy usa Complete/Stream legacy
// y el store de afinidad queda vacío (P8/C11).
func (s *Server) SetStickyRouting(enabled bool) {
	s.stickyRouting = enabled
}

// SetRegistry configura el writer del registro unificado de accounting
// (014-001). nil = off (sin emisión de eventos terminal, I3/I6: cero cambio
// al request path). Debe llamarse antes del tráfico (patrón SetStickyRouting).
func (s *Server) SetRegistry(w *registry.Writer) {
	s.registry = w
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
// El margen se aplica como context_window × (1 + margin). maxTokens es el
// max_tokens pedido por el cliente (0 si ausente): el upstream cuenta
// prompt + completion contra la ventana real, así que el chequeo debe
// incluirlo (TECHDEBT #23 — 502 upstream 04:32/04:49: prompt 1,016,660 +
// max_tokens 32,000 = 1,048,660 > límite real 1,048,576).
func (s *Server) exceedsContextWindow(model string, promptTokens, maxTokens int) bool {
	md, ok := s.modelMeta[model]
	if !ok || md.ContextWindow <= 0 {
		return false
	}
	limit := float64(md.ContextWindow) * (1 + s.contextMargin)
	return float64(promptTokens+maxTokens) > limit
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
	// X-Session-Id: solo lectura, NUNCA upstream (008-003 I1) — se usa
	// para correlacionar requests de una sesión lógica del runtime.
	sessionID := r.Header.Get("X-Session-Id")
	// Telemetría de descubrimiento (009-000 P3): UN evento por request con
	// body válido, emitido ANTES de los chequeos de limiter/budget/ventana/
	// upstream para que el descubrimiento vea TODO el tráfico (stream y
	// no-stream, incluidos los rechazos). JSON malformado (400 de parseo)
	// no llega acá (R4). Best-effort: un error de emisión no falla (I7).
	s.emitRequestTelemetry(r, req, clientID, body, logger)
	// Fase 1 del análisis de contexto (009-001 P4): mismo gate que la
	// telemetría (body válido, stream y no-stream, incluidos los rechazos
	// por limiter/budget/ventana/upstream). Best-effort: un error de
	// análisis jamás falla el request (I7/I2, P8).
	if s.contextAnalysis {
		if res, err := composition.Analyze(body, 10); err == nil {
			s.metrics.RecordContext(clientID, sessionID, logging.RequestID(r.Context()), req.Model, res, time.Now().Unix())
		}
	}
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

	// 2.5 Determinismo (010-001 P0 / 010-002 P1): un request es
	// determinístico si NO es streaming y tiene temperature == 0 o seed
	// presente. Se computa UNA sola vez acá y lo consumen single-flight
	// (coalescing concurrente) y el response cache (exact-match). Con las
	// features off por default, isDeterministic queda false y los flujos
	// legacy intactos.
	isDeterministic := false
	if !req.Stream {
		var sampling struct {
			Temperature *float64 `json:"temperature"`
			Seed        *int64   `json:"seed"`
		}
		if json.Unmarshal(body, &sampling) == nil {
			isDeterministic = (sampling.Temperature != nil && *sampling.Temperature == 0) ||
				(sampling.Seed != nil && *sampling.Seed != 0)
		}
	}

	// 2.6 Response cache exact-match (010-002 P1): chequeo ANTES de la
	// ventana de contexto (008-001) — un HIT responde desde el cache y
	// saltea el chequeo (la respuesta ya existe y fue generada
	// válidamente). Un HIT no consume provider ni toca cooldowns (el
	// cache vive antes de la cadena). Transparencia: headers
	// X-Mofgw-Cache: HIT|MISS + X-Mofgw-Cache-Age — el cliente SIEMPRE
	// puede ver que vino del cache. Streaming nunca se cachea (§4.3).
	var respCacheKey string
	if s.responseCacheEnabled && isDeterministic {
		if k, err := responseCacheKey(clientID, body); err == nil {
			respCacheKey = k
			if entry, ok := s.responseCache.Get(k); ok {
				s.metrics.IncRespCacheHit(clientID)
				age := time.Since(entry.CreatedAt).Seconds()
				w.Header().Set("Content-Type", entry.ContentType)
				w.Header().Set("X-Mofgw-Cache", "HIT")
				w.Header().Set("X-Mofgw-Cache-Age", strconv.Itoa(int(age)))
				w.WriteHeader(entry.StatusCode)
				_, _ = w.Write(entry.Body)
				logger.Debug("response_cache_hit", "model", req.Model)
				return
			}
			s.metrics.IncRespCacheMiss(clientID)
			w.Header().Set("X-Mofgw-Cache", "MISS")
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

	// 4.4 Clamp por modelo pre-routing (010-002 P1/P2): si el modelo
	// destino tiene metadata con max_output > 0, el body se clampea a ese
	// techo ANTES del chequeo de ventana y del ruteo (mata el 400→502: un
	// max_tokens mayor al techo real hace fallar al upstream no-retryable).
	// El router aplica después su clamp por provider por intento (001-004)
	// → el efectivo es min(max_output_modelo, MaxTokens_provider) (P2/I3).
	// Sin metadata (o max_output == 0) → body intacto (P8). El response
	// cache (2.6) ya keyeó sobre el body crudo del cliente — el cache
	// exact-match no se ve afectado.
	if md, ok := s.modelMeta[req.Model]; ok && md.MaxOutput > 0 {
		clamped, err := clamp.Request(body, int64(md.MaxOutput))
		if err != nil {
			openAIError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
			return
		}
		body = clamped
	}

	// 4.5 Rechazo temprano por ventana de contexto (008-001 P1 + 010-002
	// P3): si el prompt estimado + max_tokens excede la ventana del modelo
	// (con margen), 400 sin gastar intentos de la cadena. Con la regla
	// fiel, el max_tokens que cuenta es el EFECTIVO post-clamp por modelo:
	// min(max_tokens del cliente, max_output del modelo) — el cuerpo ya fue
	// clampeado en 4.4 pero ChatRequest.MaxTokens queda stale (solo parsea
	// max_tokens, no max_completion_tokens), así que el efectivo se computa
	// acá. Sin metadata (o max_output == 0) → crudo (008-001 actual,
	// TECHDEBT #23 intacto: el upstream sigue siendo la autoridad final).
	maxTokens := 0
	if req.MaxTokens != nil {
		maxTokens = int(*req.MaxTokens)
	}
	if md, ok := s.modelMeta[req.Model]; ok && md.MaxOutput > 0 && maxTokens > md.MaxOutput {
		maxTokens = md.MaxOutput
	}
	if promptTokens := estimatePromptTokens(body); s.exceedsContextWindow(req.Model, promptTokens, maxTokens) {
		s.metrics.IncRejected(clientID, "context_window")
		msg := fmt.Sprintf("estimated prompt tokens %d + max_tokens %d exceed context window %d for model %q", promptTokens, maxTokens, s.modelMeta[req.Model].ContextWindow, req.Model)
		openAIError(w, http.StatusBadRequest, msg, "invalid_request_error")
		logger.Debug("context_window_rejected", "model", req.Model, "estimated", promptTokens, "max_tokens", maxTokens)
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

	// 018-001 D6: el session id (X-Session-Id entrante) y el client_id
	// autenticado viajan al provider por RequestMeta en el context. El
	// provider.Client solo usa el valor si el knob opencode_session de
	// ESE provider está activo (I1/I2); los demás endpoints (responses,
	// embeddings, health) no pueblan meta → solo reciben el UA (P11).
	ctx = provider.WithMeta(ctx, provider.RequestMeta{SessionID: sessionID, ClientID: clientID})

	// 009-002 P2: clave sticky = clientID + "|" + sessionID (X-Session-Id
	// solo lectura, NUNCA upstream). sessionID "" → afinidad por cliente
	// (clientID|). clientID nunca es vacío (viene del token autenticado)
	// → la clave nunca es "". sticky off → clave "" → legacy (P8).
	stickyKey := ""
	if s.stickyRouting {
		stickyKey = clientID + "|" + sessionID
	}

	// 5. Streaming o no-stream
	if req.Stream {
		s.metrics.IncStreams()
		providerID, streamUsage := s.handleStream(ctx, w, r, req, body, logger, stickyKey)
		lastUsage = streamUsage
		if providerID != "" {
			s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, providerID, req.Model, streamUsage)
			s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, providerID, req.Model, streamUsage, true)
		}
		return
	}

	// 6. No-stream: router.Complete/CompleteFor (sticky si está on)
	complete := func() (*provider.CompleteResult, error) {
		if stickyKey != "" {
			return s.router.CompleteFor(ctx, req, body, stickyKey)
		}
		return s.router.Complete(ctx, req, body)
	}

	// 6.5 Single-flight (010-001 P0): dedupe de requests idénticos
	// CONCURRENTES no-stream determinísticos. El líder ejecuta el flujo
	// upstream normal y comparte su respuesta; los followers esperan el
	// mismo vuelo y reescriben el blob compartido (sin llamada upstream
	// propia, IncDeduped). Key = sha256(method|path|body). Off por
	// default (singleFlightEnabled=false) → el bloque no se ejecuta y el
	// flujo legacy de abajo queda intacto.
	if s.singleFlightEnabled && isDeterministic {
		// leader distingue a quien ejecutó fn (el líder) de los followers
		// que esperan el vuelo compartido: el líder ya factura y expone
		// usage dentro de fn; el follower debe hacerlo en su propio
		// request (010-001 P0 / 006-001 P2 / 007-003 P1).
		leader := false
		sfRes, shared := s.flights.Do(singleFlightKey(r.Method, r.URL.Path, body), func() *singleflight.Result {
			leader = true
			cres, cerr := complete()
			if cerr != nil {
				// Líder: responde su error como en el flujo legacy; el
				// vuelo termina sin resultado compartido.
				s.handleChainError(w, cerr, logger, logging.RequestID(r.Context()), clientID, req.Stream)
				return nil
			}
			providerID = cres.ProviderID
			s.metrics.SetLastProvider(cres.ProviderID)
			s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, cres.ProviderID, req.Model, &cres.Response.Usage)
			s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, cres.ProviderID, req.Model, &cres.Response.Usage, false)
			lastUsage = &cres.Response.Usage
			// headers de usage (007-003 P1): el cliente ve su consumo por request
			s.setUsageHeaders(w.Header(), cres.ProviderID, req.Model, &cres.Response.Usage)
			// 010-002 P1: almacenar la respuesta en el cache exact-match
			// (no-op si cache off o request no elegible). El líder guarda;
			// los followers reescriben el blob compartido sin re-guardar.
			s.storeResponseCache(respCacheKey, clientID, http.StatusOK, "application/json", rewriteResponseModel(cres.Raw, req.Model))
			// reescribir model → el que pidió el cliente (no el alias interno)
			return &singleflight.Result{
				StatusCode:  http.StatusOK,
				ContentType: "application/json",
				Body:        rewriteResponseModel(cres.Raw, req.Model),
				ProviderID:  cres.ProviderID,
				Usage: singleflight.Usage{
					PromptTokens:     cres.Response.Usage.PromptTokens,
					CompletionTokens: cres.Response.Usage.CompletionTokens,
					TotalTokens:      cres.Response.Usage.TotalTokens,
					CachedTokens:     cres.Response.Usage.CachedTokens,
					ReasoningTokens:  cres.Response.Usage.ReasoningTokens,
				},
			}
		})
		if sfRes == nil {
			// Líder con error ya respondido (handleChainError dentro de
			// fn), o vuelo fallido/panic recuperado por Group.Do (nada
			// escrito aún). El follower de un vuelo fallido y el líder
			// paniqueado reciben error genérico — nunca cuelgan.
			if !sr.wrote {
				absorb.Respond(w, absorb.ErrAllProvidersDown)
			}
			return
		}
		if shared {
			s.metrics.IncDeduped(clientID)
		}
		// El follower reutiliza el blob compartido del líder pero es un
		// request propio: factura el consumo a SU clientID y expone
		// X-Usage-* en SU response. Sin esto, el dedupe eximía al
		// follower de facturar y sus headers de usage quedaban en cero
		// (bug: solo corrían dentro de fn, en el líder).
		if !leader {
			usage := &provider.Usage{
				PromptTokens:     sfRes.Usage.PromptTokens,
				CompletionTokens: sfRes.Usage.CompletionTokens,
				TotalTokens:      sfRes.Usage.TotalTokens,
				CachedTokens:     sfRes.Usage.CachedTokens,
				ReasoningTokens:  sfRes.Usage.ReasoningTokens,
			}
			lastUsage = usage
			s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, sfRes.ProviderID, req.Model, usage)
			s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, sfRes.ProviderID, req.Model, usage, false)
			s.setUsageHeaders(w.Header(), sfRes.ProviderID, req.Model, usage)
		}
		// Líder y follower escriben el mismo blob compartido (status,
		// content-type, body). El líder ya seteó usage headers y side
		// effects dentro de fn.
		w.Header().Set("Content-Type", sfRes.ContentType)
		w.WriteHeader(sfRes.StatusCode)
		_, _ = w.Write(sfRes.Body)
		return
	}

	res, err := complete()
	if err != nil {
		s.handleChainError(w, err, logger, logging.RequestID(r.Context()), clientID, req.Stream)
		return
	}
	providerID = res.ProviderID
	s.metrics.SetLastProvider(res.ProviderID)
	s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, res.ProviderID, req.Model, &res.Response.Usage)
	s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, res.ProviderID, req.Model, &res.Response.Usage, false)
	lastUsage = &res.Response.Usage
	// headers de usage (007-003 P1): el cliente ve su consumo por request
	s.setUsageHeaders(w.Header(), res.ProviderID, req.Model, &res.Response.Usage)
	// reescribir model → el que pidió el cliente (no el alias interno)
	out := rewriteResponseModel(res.Raw, req.Model)
	// 010-002 P1: almacenar la respuesta en el cache exact-match (no-op
	// si cache off o request no elegible).
	s.storeResponseCache(respCacheKey, clientID, http.StatusOK, "application/json", out)
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
// P1-P3), el costo estimado (006-002 P2-P3), el registro por sesión
// (008-003 P2) y la fase 2 del análisis de contexto (009-001 P4:
// UpdateContextUsage con Usage.PromptTokens post-response, stream y
// no-stream). miss = prompt - cached (los proveedores OpenAI-compatibles
// no reportan miss explícito; el estándar es
// prompt_tokens_details.cached_tokens). Nunca crashea con usage ausente
// ni con modelo sin pricing.
func (s *Server) recordCacheTokens(requestID, clientID, sessionID, providerID, model string, u *provider.Usage) {
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
	cost := s.estimateCost(model, miss, completion, hit)
	s.metrics.IncCacheTokens(providerID, model, hit, miss, reasoning)
	s.metrics.IncUsage(clientID, providerID, model, prompt, completion, total)
	s.metrics.IncCost(clientID, providerID, model, cost)
	s.metrics.RecordSession(clientID, sessionID, prompt, completion, total, cost, time.Now().Unix())
	// Fase 2 del análisis de contexto (P4): el usage upstream setea el
	// prompt_tokens_actual de la entry (matcheo exacto por request_id) y
	// acumula el agregado. Best-effort: sin usage no se toca nada.
	if s.contextAnalysis && u != nil {
		s.metrics.UpdateContextUsage(clientID, sessionID, requestID, int64(u.PromptTokens))
	}
	// 009-002 P6: afinidad sticky del provider GANADOR (el que respondió o
	// arrancó el stream, no necesariamente el preferido). Este método solo
	// se llama post-éxito (stream que arrancó o no-stream respondido) con
	// providerID no vacío; un request sin éxito upstream no pasa por acá →
	// la afinidad previa queda intacta (C9). Gated por s.stickyRouting:
	// sticky off → store vacío, cero memoria (P8/C11). Best-effort: un
	// error de registro jamás falla el request (I5 009-000).
	if s.stickyRouting {
		s.router.Affinity().Set(clientID+"|"+sessionID, providerID)
	}
}

// emitTerminalSuccess filma UN evento terminal success (014-001 P12) junto
// al call-site de recordCacheTokens: post-respuesta, con los tokens del
// usage real del provider y cost_usd = estimateCost(model, miss, completion,
// hit) (D9 — MISMA fórmula que recordCacheTokens para que registro y
// /metrics concuerden). stream = flag real del request. Best-effort
// (D10/I4): un error de escritura se loguea y el request sigue; nil writer o
// request sin request_id → no-op (I3/D11).
func (s *Server) emitTerminalSuccess(requestID, clientID, providerID, model string, u *provider.Usage, stream bool) {
	if s.registry == nil {
		return
	}
	if requestID == "" {
		return // D11: sin request_id no se registra
	}
	prompt, completion, hit, reasoning := int64(0), int64(0), int64(0), int64(0)
	if u != nil {
		prompt = int64(u.PromptTokens)
		completion = int64(u.CompletionTokens)
		hit = int64(u.CachedTokens)
		reasoning = int64(u.ReasoningTokens)
	}
	miss := prompt - hit
	if miss < 0 {
		miss = 0
	}
	ev := registry.TerminalEvent{
		Type:          "terminal",
		RequestID:     requestID,
		Ts:            time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Client:        clientID,
		Outcome:       "success",
		Status:        http.StatusOK,
		FinalProvider: providerID,
		Tokens: registry.Tokens{
			Prompt:     int(prompt),
			Completion: int(completion),
			Cache:      int(hit),
			Reasoning:  int(reasoning),
		},
		CostUSD: s.estimateCost(model, miss, completion, hit),
		Stream:  stream,
	}
	if err := s.registry.Terminal(ev); err != nil {
		s.logger.Warn("registry: terminal success no emitido", "request_id", requestID, "err", err)
	}
}

// emitTerminalError filma UN evento terminal error (014-001 P13): cuando la
// cadena falla. error_code = ChainError.Code semántico (D8); status = HTTP
// FINAL enviado al cliente; final_provider = ChainError.ProviderID (último
// provider intentado; "" si ninguno, p.ej. model_not_found). tokens/cost 0.
// Best-effort (D10/I4): un error de escritura no altera la respuesta.
func (s *Server) emitTerminalError(requestID, clientID string, ce *router.ChainError, stream bool) {
	if s.registry == nil {
		return
	}
	if requestID == "" {
		return // D11: sin request_id no se registra
	}
	status := http.StatusBadGateway
	code := "upstream_unavailable"
	providerID := ""
	if ce != nil {
		status = ce.Status
		code = ce.Code
		providerID = ce.ProviderID
	}
	ev := registry.TerminalEvent{
		Type:          "terminal",
		RequestID:     requestID,
		Ts:            time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Client:        clientID,
		Outcome:       "error",
		ErrorCode:     code,
		Status:        status,
		FinalProvider: providerID,
		Stream:        stream,
	}
	if err := s.registry.Terminal(ev); err != nil {
		s.logger.Warn("registry: terminal error no emitido", "request_id", requestID, "err", err)
	}
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

// handleStream: router.Stream/StreamFor + passthrough SSE con frontera
// primer-byte. stickyKey "" → Stream legacy; con clave → StreamFor
// (afinidad sticky 009-002 P5). Devuelve el ID del provider que sirvió
// el stream ("" si falló) y el usage capturado del chunk final (003-001
// P4; nil si el upstream no lo mandó o el stream falló).
func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, r *http.Request, req *provider.ChatRequest, body []byte, logger *slog.Logger, stickyKey string) (string, *provider.Usage) {
	var res *router.StreamResult
	var err error
	if stickyKey != "" {
		res, err = s.router.StreamFor(ctx, req, body, stickyKey)
	} else {
		res, err = s.router.Stream(ctx, req, body)
	}
	if err != nil {
		s.handleChainError(w, err, logger, logging.RequestID(r.Context()), auth.ClientIDFrom(r.Context()), req.Stream)
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
// requestID/clientID/stream permiten emitir el evento terminal error del
// registro (014-001 P13) junto al manejo del error de cadena.
func (s *Server) handleChainError(w http.ResponseWriter, err error, logger *slog.Logger, requestID, clientID string, stream bool) {
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
		s.emitTerminalError(requestID, clientID, ce, stream)
	} else {
		s.metrics.IncErrors()
		logger.Error("error interno", "err", err)
		s.emitTerminalError(requestID, clientID, nil, stream)
	}
	absorb.Respond(w, err)
}

// handleModels lista los modelos de todos los providers configurados
// (deduplicados).
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	var models []map[string]any
	for _, p := range s.providers {
		// 013-003 (D3/P6): type-assert de interfaz para que los providers
		// subprocess (que implementan Models()) también aparezcan en el
		// catálogo, además de *provider.Client (HTTP).
		if c, ok := p.(interface{ Models() []string }); ok {
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
// capabilities.reasoning, thinking.levels/default. La feature
// 010-001-catalogo-fiel agrega supported_parameters, modality,
// architecture (derivada), top_provider y max_output_tokens — siempre
// bajo la fallback rule (P2): campo emitido solo si su fuente declarada
// no es zero-value (ausente ≠ 0/[]/{}).
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
		// 010-001-catalogo-fiel: campos nuevos, todos con fallback rule
		// (P2 — ausente ≠ 0/[]/{}).
		if len(md.SupportedParameters) > 0 {
			params := make([]string, len(md.SupportedParameters))
			copy(params, md.SupportedParameters)
			entry["supported_parameters"] = params
		}
		if md.Modality != "" {
			entry["modality"] = md.Modality
			if ins, outs, ok := splitModality(md.Modality); ok {
				entry["architecture"] = map[string]any{
					"modality":          md.Modality,
					"input_modalities":  ins,
					"output_modalities": outs,
				}
			}
		}
		if cw > 0 || md.MaxOutput > 0 {
			tp := map[string]any{}
			if cw > 0 {
				tp["context_length"] = cw
			}
			if md.MaxOutput > 0 {
				tp["max_completion_tokens"] = md.MaxOutput
			}
			entry["top_provider"] = tp
		}
		if md.MaxOutput > 0 {
			entry["max_output_tokens"] = md.MaxOutput
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

// splitModality deriva las modalidades de architecture desde la cadena
// modality declarada (010-001 P4/P5). Mecánica pura: inputs = lado
// izquierdo de "->", outputs = lado derecho; cada lado partido por "+"
// descartando elementos vacíos. Sin "->" o con algún lado vacío (ej.
// "text", "text->") → no parseable, architecture AUSENTE (P5).
func splitModality(modality string) (inputs, outputs []string, ok bool) {
	sides := strings.Split(modality, "->")
	if len(sides) != 2 || sides[0] == "" || sides[1] == "" {
		return nil, nil, false
	}
	split := func(side string) []string {
		var parts []string
		for _, p := range strings.Split(side, "+") {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return parts
	}
	return split(sides[0]), split(sides[1]), true
}

// handleUsage expone los agregados de consumo del cliente autenticado
// (007-003 P3): GET /v1/usage con auth Bearer → {client, totals,
// by_model, sessions}. Con ?session=<id> devuelve las stats de esa sesión
// (008-003 P3) o 404 si no existe para el cliente. Cada cliente ve SOLO
// lo suyo (aislamiento por clientID).
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	clientID := auth.ClientIDFrom(r.Context())

	// ?session=<id>: stats de una sesión (008-003 P3).
	if session := r.URL.Query().Get("session"); session != "" {
		snap, ok := s.metrics.SessionSnapshot(clientID, session)
		if !ok {
			openAIError(w, http.StatusNotFound, "session not found", "not_found_error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session":           snap.SessionID,
			"client":            clientID,
			"requests":          snap.Requests,
			"prompt_tokens":     snap.PromptTokens,
			"completion_tokens": snap.CompletionTok,
			"total_tokens":      snap.TotalTokens,
			"cost_usd":          snap.CostUSD,
			"first_request_at":  snap.FirstRequestAt,
			"last_request_at":   snap.LastRequestAt,
		})
		return
	}

	// Sin query: totals del cliente + sesiones (007-003 + 008-003).
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
	sessions := make([]map[string]any, 0, 8)
	for _, ss := range s.metrics.SessionsList(clientID) {
		sessions = append(sessions, map[string]any{
			"id":              ss.SessionID,
			"requests":        ss.Requests,
			"total_tokens":    ss.TotalTokens,
			"cost_usd":        ss.CostUSD,
			"last_request_at": ss.LastRequestAt,
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
		"sessions": sessions,
	})
}

// handleContext expone la composición estructural del contexto del cliente
// autenticado (009-001 P3): GET /v1/context con auth Bearer (aislamiento
// por clientID). Con ?session=<id> → 200 con el record client|<id> (404 si
// no existe para el cliente, consistente 008-003 P5); sin session → 200
// scope:"client" con el agregado client| (ceros/vacío sin data, nunca nil).
// SOLO metadata estructural: roles, part types, bytes, tokens, conteos —
// jamás contenido (P7/I1).
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	clientID := auth.ClientIDFrom(r.Context())

	if session := r.URL.Query().Get("session"); session != "" {
		snap, ok := s.metrics.ContextSnapshot(clientID, session)
		if !ok {
			openAIError(w, http.StatusNotFound, "session not found", "not_found_error")
			return
		}
		s.writeContext(w, snap)
		return
	}

	snap, _ := s.metrics.ContextSnapshot(clientID, "")
	s.writeContext(w, snap)
}

// writeContext serializa la vista de un ContextSnapshot (P3): shape
// {client, scope, session, requests, summary, latest, history} con keys
// snake_case. `summary.prompt_tokens_actual` y los entry
// `prompt_tokens_actual` son null mientras la fase 2 no corrió.
func (s *Server) writeContext(w http.ResponseWriter, snap metrics.ContextSnapshot) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{
		"client":   snap.Client,
		"scope":    snap.Scope,
		"requests": snap.Requests,
		"summary": map[string]any{
			"roles":                   roleAggsToJSON(snap.Summary.Roles),
			"part_types":              partAggsToJSON(snap.Summary.PartTypes),
			"num_tools_total":         snap.Summary.NumToolsTotal,
			"prompt_tokens_estimated": snap.Summary.PromptTokensEstimated,
			"prompt_tokens_actual":    contextActualJSON(snap),
			"first_request_at":        snap.Summary.FirstRequestAt,
			"last_request_at":         snap.Summary.LastRequestAt,
		},
		"latest":  contextEntryToJSON(snap.Latest),
		"history": contextEntriesToJSON(snap.History),
	}
	if snap.Scope == "session" {
		out["session"] = snap.Session
	}
	_ = json.NewEncoder(w).Encode(out)
}

// contextActualJSON devuelve el prompt_tokens_actual agregado del summary,
// o null si la fase 2 nunca corrió (sin entries con actual set y sin
// agregado — C9). Un 0 real post-phase-2 se reporta como 0.
func contextActualJSON(snap metrics.ContextSnapshot) any {
	if snap.Summary.PromptTokensActual != 0 {
		return snap.Summary.PromptTokensActual
	}
	for i := range snap.History {
		if snap.History[i].PromptTokensActual != nil {
			return snap.Summary.PromptTokensActual
		}
	}
	return nil
}

func roleAggsToJSON(aggs []metrics.RoleAgg) []map[string]any {
	out := make([]map[string]any, 0, len(aggs))
	for _, a := range aggs {
		out = append(out, map[string]any{
			"role":       a.Role,
			"messages":   a.Messages,
			"est_tokens": a.EstTokens,
			"pct":        a.Pct,
		})
	}
	return out
}

func partAggsToJSON(aggs []metrics.PartAgg) []map[string]any {
	out := make([]map[string]any, 0, len(aggs))
	for _, a := range aggs {
		out = append(out, map[string]any{
			"type":       a.Type,
			"count":      a.Count,
			"bytes":      a.Bytes,
			"est_tokens": a.EstTokens,
		})
	}
	return out
}

func contextEntryToJSON(e *metrics.ContextEntry) map[string]any {
	if e == nil {
		return nil
	}
	return map[string]any{
		"request_id":              e.RequestID,
		"model":                   e.Model,
		"roles":                   e.Roles,
		"part_types":              e.PartTypes,
		"num_tools":               e.NumTools,
		"total_bytes":             e.TotalBytes,
		"prompt_tokens_estimated": e.PromptTokensEstimated,
		"prompt_tokens_actual":    e.PromptTokensActual,
		"request_at":              e.RequestAt,
	}
}

func contextEntriesToJSON(entries []metrics.ContextEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for i := range entries {
		out = append(out, contextEntryToJSON(&entries[i]))
	}
	return out
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
