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
	"io"
	"log/slog"
	"net/http"
	"time"

	"ofap.sh/mofgw/internal/absorb"
	"ofap.sh/mofgw/internal/auth"
	"ofap.sh/mofgw/internal/limiter"
	"ofap.sh/mofgw/internal/logging"
	"ofap.sh/mofgw/internal/metrics"
	"ofap.sh/mofgw/internal/provider"
	"ofap.sh/mofgw/internal/router"
	"ofap.sh/mofgw/internal/stream"
)

// Server es el proxy HTTP. Los campos son inmutables post-New (seguro
// para uso concurrente).
type Server struct {
	router    *router.Router
	providers []provider.Provider // para /v1/models
	auth      *auth.Authenticator
	metrics   *metrics.Metrics
	logger    *slog.Logger
	maxBody   int64

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
	return &Server{router: r, providers: providers, auth: a, metrics: m, logger: logger, maxBody: maxBodyBytes, globalLimiter: globalLimiter, keyedLimiter: keyedLimiter, backpressureTimeout: backpressureTimeout}
}

// Handler devuelve el mux con auth aplicada a /v1/* (healthz y metrics
// quedan públicos, loopback).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	// rutas no declaradas en el mux → 404 OpenAI-compatible
	return s.auth.Wrap(withRequestID(mux), "/healthz", "/metrics")
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
	defer func() {
		s.emitRequestEnd(logger, start, sr.status, providerID, model)
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

	ctx := r.Context()

	// 5. Streaming o no-stream
	if req.Stream {
		s.metrics.IncStreams()
		providerID = s.handleStream(ctx, w, r, req, body, logger)
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
	// reescribir model → el que pidió el cliente (no el alias interno)
	out := rewriteResponseModel(res.Raw, req.Model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// emitRequestEnd loguea el evento request_end al finalizar un handler.
func (s *Server) emitRequestEnd(logger *slog.Logger, start time.Time, statusCode int, providerID, model string) {
	logger.Debug("request_end",
		"status_code", statusCode,
		"duration_ms", time.Since(start).Milliseconds(),
		"provider", providerID,
		"model", model,
	)
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
// Devuelve el ID del provider que sirvió el stream, o vacío si falló.
func (s *Server) handleStream(ctx context.Context, w http.ResponseWriter, r *http.Request, req *provider.ChatRequest, body []byte, logger *slog.Logger) string {
	res, err := s.router.Stream(ctx, req, body)
	if err != nil {
		s.handleChainError(w, err, logger)
		return ""
	}
	s.metrics.SetLastProvider(res.Provider.ID())
	sw, err := stream.NewWriter(w)
	if err != nil {
		openAIError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return ""
	}
	if err := sw.Copy(res.Events, req.Model); err != nil {
		logger.Warn("stream interrumpido", "provider", res.Provider.ID(), "err", err)
	}
	return res.Provider.ID()
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
					models = append(models, map[string]any{
						"id":       m,
						"object":   "model",
						"created":  time.Now().Unix(),
						"owned_by": "mofgw",
					})
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
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
