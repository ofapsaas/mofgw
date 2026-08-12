// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package router implementa la cadena de fallback con cooldown por
// provider (feature 001-003-fallback). Es el cerebro del proxy: recorre
// los providers en orden de config, saltea los que están en cooldown,
// clasifica errores (retryable vs no-retryable) y garantiza que el
// cliente reciba una respuesta exitosa si AL MENOS UN provider responde.
//
// EPIC-002 (resiliencia) agrega:
//   - 002-001-retry: reintentos sobre el MISMO provider con backoff
//     exponencial + jitter ±20% (tope backoff_max), antes de ceder al
//     siguiente de la cadena. Solo pre-primer-byte.
//   - 002-002-health: saltea providers unhealthy (soft: si no hay otro
//     disponible, igual los intenta). El estado lo mantiene health.Store.
//   - 002-004-degradacion: fast-fail 502 all_providers_down cuando no
//     hay ningún provider disponible, con grace para cooldowns cortos y
//     degraded_max_requests (503 directo).
//
// Solo si TODOS fallan el cliente ve un error — en formato
// OpenAI-compatible, sin detalles internos (ChainError, absorbido por
// 002-003 en la capa del proxy). En streaming, el fallback solo aplica
// antes del primer byte (spec 001-005): el router bufferiza el primer
// evento y recién entonces compromete el stream. El body viaja crudo
// ([]byte) y se clampea por intento contra el límite del provider
// destino (001-004).
package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/clamp"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/timeouts"
)

// ChainError es el error final que el cliente puede ver (único caso:
// todos los providers fallaron, o error no-retryable de cliente).
// 002-003: Code es un código estable para la capa de absorción;
// ProviderID identifica el provider que originó el error ("" = error
// del proxy/cliente); Err conserva el error original solo para logs.
type ChainError struct {
	Status     int
	Type       string
	Message    string
	Code       string
	ProviderID string
	Err        error
}

func (e *ChainError) Error() string { return fmt.Sprintf("%s: %s", e.Type, e.Message) }

// ErrNoProviderServesModel se envuelve en ChainError 404 cuando ningún
// provider de la cadena sirve el modelo pedido (sin intentos de red).
var ErrNoProviderServesModel = errors.New("no provider serves this model")

// RetryConfig es la política de reintentos por provider (002-001).
// MaxAttempts = intentos totales sobre el mismo provider (2 = 1 intento
// + 1 reintento; 0 = sin reintentos).
type RetryConfig struct {
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

// DegradationConfig es la política de degradación (002-004).
// MaxRequests 0 = ilimitado. CooldownGrace: espera por cooldowns cortos
// antes de declarar all_providers_down.
type DegradationConfig struct {
	MaxRequests   int
	CooldownGrace time.Duration
}

// Options agrupa la configuración del router (EPIC-001 + EPIC-002).
type Options struct {
	MaxRetries     int
	Cooldown       time.Duration
	CooldownJitter time.Duration
	GlobalTimeout  time.Duration
	// FirstTokenTimeout: tope TTFB de streaming (TECHDEBT #29).
	// nil = sin knob (fallback al GlobalTimeout para TTFB, comportamiento
	// histórico); &0 = sin tope de primer token; &N = N.
	FirstTokenTimeout *time.Duration
	Retry             RetryConfig
	Degradation       DegradationConfig
	Health            *health.Store
	Logger            *slog.Logger

	// StickyMaxEntries es el tope del AffinityStore (009-002 P3);
	// 0 = default (100, == server.max_sessions_retained).
	StickyMaxEntries int
}

// ProviderSpec ata un provider a sus overrides de cooldown/timeout/retry
// (nil = usar el global del config).
type ProviderSpec struct {
	Provider provider.Provider
	Cooldown *time.Duration
	Timeout  *time.Duration
	// FirstTokenTimeout: override per-provider del tope TTFB de streaming
	// (TECHDEBT #29). nil = usa el del router (Options); &0 = sin tope
	// para este provider; &N = N.
	FirstTokenTimeout *time.Duration
	Retry             *RetryConfig

	// ThinkingPath: path declarativo del provider para la inyección del
	// thinking_default prescriptivo (010-002-request-path-fiel). Valores:
	// "" (default) | "zen" | "bailian". "" → se usa Provider.ID() como
	// fallback (los tests fijan el path por construcción en el ID; en
	// producción el knob providers[].thinking_path manda — un ID real
	// como "acct1" no está en la tabla verificada → sin inyección).
	ThinkingPath string

	// 013-003-config-wiring (D5): allowlist de clientID con acceso a este
	// provider. Vacío = sin restricción (P9). En GREEN, candidates(model,
	// clientID) excluye el spec cuando len>0 y el clientID del request no
	// está (fallback preservado, I5).
	AllowedClients []string
}

// CooldownStore guarda el estado de cooldown en memoria (map + mutex),
// efímero por diseño: al reiniciar todos arrancan fríos. Limpia
// entradas expiradas con un ticker interno.
type CooldownStore struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
}

// NewCooldownStore construye un store; now es inyectable (fake clock en
// tests). Arranca un ticker de limpieza de entradas expiradas.
func NewCooldownStore(now func() time.Time, cleanupInterval time.Duration) *CooldownStore {
	if now == nil {
		now = time.Now
	}
	cs := &CooldownStore{until: map[string]time.Time{}, now: now}
	if cleanupInterval > 0 {
		go func() {
			t := time.NewTicker(cleanupInterval)
			defer t.Stop()
			for range t.C {
				cs.Cleanup()
			}
		}()
	}
	return cs
}

// Set pone a un provider en cooldown por d a partir de ahora.
func (c *CooldownStore) Set(id string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.until[id] = c.now().Add(d)
}

// IsCooling reporta si el provider está en cooldown.
func (c *CooldownStore) IsCooling(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.until[id]
	return ok && c.now().Before(u)
}

// Remaining devuelve el cooldown restante (0 si no está en cooldown).
// Usado por 002-004 para la grace de cooldowns cortos.
func (c *CooldownStore) Remaining(id string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.until[id]
	if !ok {
		return 0
	}
	d := u.Sub(c.now())
	if d < 0 {
		return 0
	}
	return d
}

// Cleanup remueve entradas expiradas (el mapa no crece indefinidamente).
func (c *CooldownStore) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for id, u := range c.until {
		if !now.Before(u) {
			delete(c.until, id)
		}
	}
}

// Len reporta cuántas entradas hay (para tests/observabilidad).
func (c *CooldownStore) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.until)
}

// Router orquesta la cadena de fallback.
type Router struct {
	specs        []ProviderSpec
	maxAttempts  int // max_retries + 1
	cooldown     time.Duration
	firstTokenTO *time.Duration // nil = TTFB == GlobalTimeout (histórico)
	jitter       time.Duration
	globalTO     time.Duration
	retryCfg     RetryConfig
	degradCfg    DegradationConfig
	cooldowns    *CooldownStore
	affinity     *AffinityStore // afinidad sticky por sesión/cliente (009-002)
	health       *health.Store
	logger       *slog.Logger
	randSource   *rand.Rand // jitter determinista en tests

	// modelMeta: capabilities por modelo (007-001/010-001), usadas para la
	// inyección per-attempt del thinking_default (010-002 P4). Misma fuente
	// que proxy.Server.modelMeta: el proxy la propaga vía SetModelMeta
	// (un solo call-site en main.go). Inmutable tras SetModelMeta (antes
	// del tráfico); nil = sin inyección (P8).
	modelMeta map[string]config.ModelMetadata

	degradedMu  sync.Mutex
	degradedCnt int // requests servidos en degradación (002-004)
}

// New construye el router con la API legacy (EPIC-001, sin reintentos,
// sin health, sin degradación). Ver NewWithOptions para EPIC-002.
func New(specs []ProviderSpec, maxRetries int, cooldown, jitter, globalTimeout time.Duration, logger *slog.Logger) *Router {
	return NewWithOptions(specs, Options{
		MaxRetries:     maxRetries,
		Cooldown:       cooldown,
		CooldownJitter: jitter,
		GlobalTimeout:  globalTimeout,
		Logger:         logger,
	})
}

// NewWithOptions construye el router con la configuración completa
// (EPIC-001 + EPIC-002).
func NewWithOptions(specs []ProviderSpec, o Options) *Router {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Router{
		specs:        specs,
		maxAttempts:  o.MaxRetries + 1,
		cooldown:     o.Cooldown,
		jitter:       o.CooldownJitter,
		globalTO:     o.GlobalTimeout,
		firstTokenTO: o.FirstTokenTimeout,
		retryCfg:     o.Retry,
		degradCfg:    o.Degradation,
		cooldowns:    NewCooldownStore(nil, 30*time.Second),
		affinity:     NewAffinityStore(nil, o.StickyMaxEntries),
		health:       o.Health,
		logger:       o.Logger,
		randSource:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Cooldowns expone el store (para tests y observabilidad).
func (r *Router) Cooldowns() *CooldownStore { return r.cooldowns }

// Affinity expone el store de afinidad sticky (009-002 P3, patrón
// Cooldowns) para observabilidad y tests.
func (r *Router) Affinity() *AffinityStore { return r.affinity }

// SetModelMeta configura las capabilities por modelo (007-001/010-001)
// para la inyección per-attempt del thinking_default (010-002 P4). La
// llama proxy.Server.SetModelMetadata (misma fuente, patrón setter
// pre-tráfico: mapa inmutable después; nil → sin inyección, P8).
func (r *Router) SetModelMeta(meta map[string]config.ModelMetadata) {
	r.modelMeta = meta
}

// Health expone el store de salud (para /healthz del proxy).
func (r *Router) Health() *health.Store { return r.health }

// contains reporta si s contiene el elemento x.
func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// candidates devuelve los índices de providers que sirven el modelo, en
// orden de config, sin los que están en cooldown. 013-003 (D5/I5): un
// provider con AllowedClients no vacío se EXCLUYE de candidatos cuando el
// clientID del request no está en la allowlist (sin 403, fallback
// preservado; el spec excluido no marca serveAny → si ningún otro sirve el
// modelo el cliente recibe 404 model_not_found, P8). Con health activo
// (002-002): saltea providers unhealthy salvo que no haya ningún otro
// disponible (marca SOFT — un provider unhealthy puede estar degradado
// pero responder). serveAny distingue "ninguno sirve el modelo" de
// "todos en cooldown/unhealthy".
func (r *Router) candidates(model, clientID string) (ready []int, serveAny bool) {
	var soft []int
	for i := range r.specs {
		if !r.specs[i].Provider.Serves(model) {
			continue
		}
		if len(r.specs[i].AllowedClients) > 0 && !contains(r.specs[i].AllowedClients, clientID) {
			continue // P8: cliente no autorizado → se excluye de candidatos
		}
		serveAny = true
		if r.cooldowns.IsCooling(r.specs[i].Provider.ID()) {
			continue
		}
		if r.health != nil && !r.health.IsHealthy(r.specs[i].Provider.ID()) {
			soft = append(soft, i)
			continue
		}
		ready = append(ready, i)
	}
	if len(ready) == 0 {
		ready = soft // soft: sin otro disponible, intentar los unhealthy
	}
	return ready, serveAny
}

func (r *Router) specCooldown(s *ProviderSpec) time.Duration {
	if s.Cooldown != nil {
		return *s.Cooldown
	}
	return r.cooldown
}

func (r *Router) specTimeout(s *ProviderSpec) time.Duration {
	if s.Timeout != nil {
		return *s.Timeout
	}
	return r.globalTO
}

// specFirstTokenTimeout resuelve el tope TTFB de streaming para un
// provider (TECHDEBT #29). Semántica de 3 estados en dos niveles:
// provider (s.FirstTokenTimeout) > router (r.firstTokenTO) > global
// (r.globalTO). nil en ambos niveles → TTFB == GlobalTimeout
// (comportamiento histórico, todos los tests existentes). 0 explícito
// en cualquier nivel → sin tope de primer token para ese intento.
func (r *Router) specFirstTokenTimeout(s *ProviderSpec) time.Duration {
	if s.FirstTokenTimeout != nil {
		return *s.FirstTokenTimeout
	}
	if r.firstTokenTO != nil {
		return *r.firstTokenTO
	}
	return r.globalTO
}

func (r *Router) specRetry(s *ProviderSpec) RetryConfig {
	if s.Retry != nil {
		return *s.Retry
	}
	return r.retryCfg
}

// jittered agrega ±jitter al cooldown (anti thundering-herd).
func (r *Router) jittered(d time.Duration) time.Duration {
	if r.jitter <= 0 {
		return d
	}
	half := r.jitter / 2
	return d + time.Duration(r.randSource.Int63n(int64(r.jitter))) - half
}

// recordFailure mete al provider en cooldown tras un fallo retryable.
func (r *Router) recordFailure(s *ProviderSpec) {
	d := r.jittered(r.specCooldown(s))
	r.cooldowns.Set(s.Provider.ID(), d)
	r.logger.Warn("provider en cooldown", "provider", s.Provider.ID(), "dur", d.String())
	r.logger.Debug("cooldown_start", "provider", s.Provider.ID(), "duracion", d.String())
}

// resetDegraded reinicia el contador de degradación (002-004): el proxy
// se recuperó (un request tuvo éxito).
func (r *Router) resetDegraded() {
	r.degradedMu.Lock()
	r.degradedCnt = 0
	r.degradedMu.Unlock()
}

// fastFail implementa el fast-fail de 002-004: responde 502
// all_providers_down (o 503 degradado tras degraded_max_requests) sin
// recorrer la cadena.
func (r *Router) fastFail() error {
	r.degradedMu.Lock()
	r.degradedCnt++
	cnt := r.degradedCnt
	r.degradedMu.Unlock()
	if r.degradCfg.MaxRequests > 0 && cnt > r.degradCfg.MaxRequests {
		r.logger.Warn("degraded: request limit reached", "count", cnt, "max", r.degradCfg.MaxRequests)
		return &ChainError{
			Status:  http.StatusServiceUnavailable,
			Type:    "upstream_error",
			Code:    "degraded_limit",
			Message: "proxy degraded: request limit reached",
		}
	}
	r.logger.Warn("degraded: all providers down", "count", cnt)
	return &ChainError{
		Status:  http.StatusBadGateway,
		Type:    "upstream_error",
		Code:    "all_providers_down",
		Message: "no providers available",
	}
}

// shortestCooldown devuelve el cooldown restante más corto entre los
// providers que sirven el modelo (0 si ninguno está en cooldown).
func (r *Router) shortestCooldown(model string) time.Duration {
	var min time.Duration
	found := false
	for i := range r.specs {
		s := &r.specs[i]
		if !s.Provider.Serves(model) {
			continue
		}
		rem := r.cooldowns.Remaining(s.Provider.ID())
		if rem > 0 && (!found || rem < min) {
			min = rem
			found = true
		}
	}
	if !found {
		return 0
	}
	return min
}

// clampBody aplica el límite del provider destino al body crudo
// (001-004-clamp). Si el provider no tiene límite o el body ya cumple,
// devuelve el body intacto.
func clampBody(body []byte, providerMax int64) ([]byte, error) {
	if providerMax <= 0 {
		return body, nil
	}
	return clamp.Request(body, providerMax)
}

// ensureIncludeUsage garantiza stream_options.include_usage=true en un
// body de STREAMING cuando el cliente no lo especificó (003-001 P2).
// Esto hace que el chunk final del SSE traiga el objeto usage (incluidos
// los campos de cache) aunque el cliente no lo pidió.
//
// Semántica: si el cliente mandó stream_options CON include_usage (true o
// false), se respeta tal cual (el proxy no pisa decisiones explícitas).
// Si el cliente mandó stream_options SIN include_usage (ej. solo otros
// campos), se agrega include_usage=true preservando el resto (H2 review:
// P2 exige include_usage salvo que el cliente lo haya especificado).
// Si no mandó stream_options, se crea con include_usage=true. Para
// requests no-stream no aplica (nunca se llama). Preserva el resto del
// body byte a byte (patrón clamp: re-encode con SetEscapeHTML(false)).
func ensureIncludeUsage(body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("include_usage: body inválido: %w", err)
	}
	rawSO, hasSO := m["stream_options"]
	if hasSO && len(rawSO) > 0 && string(rawSO) != "null" {
		var so map[string]json.RawMessage
		if err := json.Unmarshal(rawSO, &so); err != nil {
			return nil, fmt.Errorf("include_usage: stream_options inválido: %w", err)
		}
		if _, ok := so["include_usage"]; ok {
			return body, nil // el cliente lo especificó: respetar (true o false)
		}
		// stream_options sin include_usage: agregarlo preservando el resto
		so["include_usage"] = json.RawMessage(`true`)
		merged, err := json.Marshal(so)
		if err != nil {
			return nil, fmt.Errorf("include_usage: re-encode stream_options: %w", err)
		}
		m["stream_options"] = merged
	} else {
		m["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("include_usage: re-encode: %w", err)
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// ---- 010-002-request-path-fiel: inyección del thinking_default (P4-P8) ----

// thinkingInjection describe los parámetros de activación de thinking a
// inyectar en un intento (§5.4). Ambos false → sin inyección.
type thinkingInjection struct {
	reasoningEffort bool // inyectar reasoning_effort = ThinkingDefault (string)
	enableThinking  bool // inyectar enable_thinking = true (bool)
}

// thinkingProfile clasifica la familia de un modelo por su metadata
// declarativa (research-token-efficiency.md §5.4, acceso 2026-08-10). La
// metadata codifica la identidad del modelo:
//
//   - "always"   → kimi-k2.7-code: siempre-on, NUNCA inyectar (P6a;
//     `disabled` → error upstream).
//   - "adaptive" → minimax-m3: nativo adaptive == prescriptivo adaptive,
//     sin corrección que aplicar (P6b, documentado).
//   - "high"     → qwen3.7-plus: solo enable_thinking en bailian (P4c).
//   - niveles de effort concretos (low/medium/max) → familia reasoning
//     (deepseek-v4-flash/pro, glm-5.2): reasoning_effort (P4a/b/d).
//
// Cualquier otro perfil → "" (fallback rule P8: no se adivina).
func thinkingProfile(md config.ModelMetadata) string {
	for _, l := range md.Thinking {
		switch l {
		case "always":
			return "" // kimi-k2.7-code
		case "adaptive":
			return "" // minimax-m3
		case "low", "medium", "max":
			return "reasoning" // deepseek-v4-flash/pro, glm-5.2
		}
	}
	if len(md.Thinking) == 1 && md.Thinking[0] == "high" {
		return "qwen" // qwen3.7-plus
	}
	return ""
}

// thinkingInjectionFor devuelve los parámetros de activación verificados
// (§5.4) para un par (path, perfil de modelo). Cero = sin inyección
// (fallback rule: un path o modelo sin parámetro verificado no se
// adivina — P8). minimax/kimi ya cayeron en thinkingProfile ("") y los
// paths no reconocidos caen acá.
func thinkingInjectionFor(path, profile string) thinkingInjection {
	switch profile {
	case "reasoning":
		switch path {
		case "zen":
			return thinkingInjection{reasoningEffort: true}
		case "bailian":
			return thinkingInjection{reasoningEffort: true, enableThinking: true}
		}
	case "qwen":
		if path == "bailian" {
			return thinkingInjection{enableThinking: true}
		}
	}
	return thinkingInjection{}
}

// bodyHasExplicitEffort reporta si el body del CLIENTE declara algún
// parámetro de thinking (P5): la presencia de cualquiera de
// {reasoning_effort, thinking, enable_thinking} — con CUALQUIER valor,
// incluido null — desactiva la inyección en TODOS los intentos. Un body
// no-JSON → false (el parseo previo en el proxy ya lo rechazó).
func bodyHasExplicitEffort(body []byte) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	for _, f := range []string{"reasoning_effort", "thinking", "enable_thinking"} {
		if _, ok := m[f]; ok {
			return true
		}
	}
	return false
}

// injectThinking agrega los parámetros de activación al body de un
// intento. reasoning_effort se inyecta con el valor EXACTO ThinkingDefault
// (string); enable_thinking con true (bool). El resto del body se
// preserva intacto (mismo patrón de re-encode que clamp/ensureIncludeUsage,
// I6: JSON válido, SSE y stream_options.include_usage intactos).
func injectThinking(body []byte, inj thinkingInjection, defaultEffort string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("thinking: body inválido: %w", err)
	}
	if inj.reasoningEffort {
		v, err := json.Marshal(defaultEffort)
		if err != nil {
			return nil, fmt.Errorf("thinking: marshal reasoning_effort: %w", err)
		}
		m["reasoning_effort"] = v
	}
	if inj.enableThinking {
		m["enable_thinking"] = json.RawMessage(`true`)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("thinking: re-encode: %w", err)
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

// injectThinkingForAttempt aplica la inyección del default prescriptivo
// (010-002 P4) al body de UN intento, para el provider destino s. No-op
// cuando: el modelo no tiene metadata (P8), thinking_default vacío (P8),
// o el par (path, modelo) no tiene parámetro de activación verificado
// (§5.4 fallback rule). El respeto al effort explícito (P5) se decide en
// el call-site sobre el body ORIGINAL del cliente (una sola vez, no por
// intento).
func (r *Router) injectThinkingForAttempt(body []byte, model string, s *ProviderSpec) ([]byte, error) {
	md, ok := r.modelMeta[model]
	if !ok || md.ThinkingDefault == "" {
		return body, nil
	}
	path := s.ThinkingPath
	if path == "" {
		// Fallback al ID del provider: los tests fijan el path por
		// construcción (ID "zen"/"bailian"). En producción el knob
		// declarativo providers[].thinking_path manda; un ID real no
		// verificado cae en la fallback rule (sin inyección).
		path = s.Provider.ID()
	}
	inj := thinkingInjectionFor(path, thinkingProfile(md))
	if !inj.reasoningEffort && !inj.enableThinking {
		return body, nil
	}
	return injectThinking(body, inj, md.ThinkingDefault)
}

// classify decide si un error de intento es retryable (se reintenta el
// mismo provider o se prueba el siguiente) o no-retryable (se responde
// al cliente ya).
func classify(err error) (retryable bool, status int, typ, msg string) {
	var ue *provider.ErrUpstream
	if errors.As(err, &ue) {
		switch {
		case ue.StatusCode == http.StatusTooManyRequests:
			return true, ue.StatusCode, ue.Type, ue.Message
		case ue.StatusCode >= 500:
			return true, ue.StatusCode, ue.Type, ue.Message
		case ue.Type == "timeout" || ue.Type == "network":
			return true, http.StatusBadGateway, "upstream_error", ue.Message
		default: // 4xx de cliente (400, 401, 404...)
			return false, ue.StatusCode, ue.Type, ue.Message
		}
	}
	return false, http.StatusBadGateway, "upstream_error", err.Error()
}

// backoffFor calcula el backoff del reintento n (0-based) sobre el mismo
// provider: backoff_base * 2^n con jitter ±20%, tope backoff_max.
// Un 429 con header Retry-After respeta ese valor (tope backoff_max).
func (r *Router) backoffFor(cfg RetryConfig, n int, err error) time.Duration {
	base := cfg.BackoffBase
	if base <= 0 {
		return 0
	}
	d := base * time.Duration(1<<n)
	if d <= 0 {
		d = base
	}
	// jitter ±20%
	if d > 0 {
		j := d / 5
		d = d - j + time.Duration(r.randSource.Int63n(int64(2*j+1)))
	}
	// Retry-After del 429
	var ue *provider.ErrUpstream
	if errors.As(err, &ue) && ue.RetryAfter > 0 {
		d = ue.RetryAfter
	}
	if cfg.BackoffMax > 0 && d > cfg.BackoffMax {
		d = cfg.BackoffMax
	}
	if d < 0 {
		d = 0
	}
	return d
}

// sleepCtx duerme d respetando el context del cliente. Devuelve false si
// el cliente canceló durante la espera.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// resolveReady aplica la política de degradación cuando no hay
// candidatos listos: grace de cooldown corto (002-004) y fast-fail.
// Devuelve ready (posiblemente tras la espera) o un ChainError.
func (r *Router) resolveReady(ctx context.Context, model string) ([]int, error) {
	// 013-003 (D5): el clientID autenticado participa en la selección de
	// candidatos (exclusión de providers con allowlist). El catálogo
	// /v1/models NO filtra por cliente (global, igual que hoy).
	clientID := auth.ClientIDFrom(ctx)
	ready, serveAny := r.candidates(model, clientID)
	if len(ready) > 0 || !serveAny {
		return ready, nil
	}
	// Todos en cooldown/unhealthy: grace para cooldowns cortos.
	if r.degradCfg.CooldownGrace > 0 {
		if wait := r.shortestCooldown(model); wait > 0 && wait <= r.degradCfg.CooldownGrace {
			r.logger.Debug("degraded: waiting for cooldown expiry", "wait", wait.String())
			if sleepCtx(ctx, wait) {
				ready, _ = r.candidates(model, clientID)
				if len(ready) > 0 {
					return ready, nil
				}
			}
		}
	}
	return nil, r.fastFail()
}

// applyStickyReorder aplica la preferencia sticky (009-002 P4): si
// stickyKey tiene afinidad registrada a un provider presente en ready,
// lo mueve al frente preservando el orden relativo del resto (la cadena
// sigue en orden de config). SOLO reordenamiento post-filtro: nunca
// fuerza un provider que candidates/resolveReady ya descartó (cooldown,
// health, Serves). Sin afinidad o clave vacía → slice intacto
// (comportamiento IDÉNTICO a legacy). Emite sticky_applied (D6) cuando
// el reorder se aplica.
func (r *Router) applyStickyReorder(ready []int, stickyKey, model string) []int {
	if stickyKey == "" || r.affinity == nil {
		return ready
	}
	pref, ok := r.affinity.Get(stickyKey)
	if !ok {
		return ready
	}
	for i, idx := range ready {
		if r.specs[idx].Provider.ID() == pref {
			reordered := make([]int, 0, len(ready))
			reordered = append(reordered, idx)
			reordered = append(reordered, ready[:i]...)
			reordered = append(reordered, ready[i+1:]...)
			r.logger.Debug("sticky_applied", "sticky_key", stickyKey, "provider", pref, "model", model)
			return reordered
		}
	}
	return ready
}

// Complete recorre la cadena para un request no-stream. Devuelve la
// respuesta del primer provider exitoso, o ChainError. API legacy (P8):
// comportamiento idéntico a CompleteFor con stickyKey "".
func (r *Router) Complete(ctx context.Context, req *provider.ChatRequest, body []byte) (*provider.CompleteResult, error) {
	return r.complete(ctx, req, body, "")
}

// CompleteFor recorre la cadena para un request no-stream con afinidad
// sticky (009-002 P4): si stickyKey tiene un provider preferido
// disponible, se prueba primero (reordenamiento post-filtro; nunca
// fuerza cooldown/health/Serves). stickyKey "" → idéntico a Complete.
func (r *Router) CompleteFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*provider.CompleteResult, error) {
	return r.complete(ctx, req, body, stickyKey)
}

// complete es el loop compartido de Complete/CompleteFor: recorre la
// cadena para un request no-stream y devuelve la respuesta del primer
// provider exitoso, o ChainError.
func (r *Router) complete(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*provider.CompleteResult, error) {
	// 010-002 P5: el effort explícito del cliente se decide UNA vez sobre
	// el body original (el clamp del proxy solo toca max_tokens, nunca los
	// campos de thinking). Solo se parsea si el modelo tiene metadata
	// (sin metadata la inyección es no-op — P8).
	explicitEffort := false
	if _, ok := r.modelMeta[req.Model]; ok {
		explicitEffort = bodyHasExplicitEffort(body)
	}
	ready, err := r.resolveReady(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		r.logger.Debug("decision", "motivo", "no provider serves model", "model", req.Model)
		return nil, &ChainError{Status: http.StatusNotFound, Type: "model_not_found", Code: "model_not_found", Message: fmt.Sprintf("no provider serves model %q", req.Model)}
	}
	ready = r.applyStickyReorder(ready, stickyKey, req.Model)

	r.logger.Debug("decision", "motivo", "candidatos listos", "model", req.Model, "count", len(ready))

	var lastChain *ChainError
	attempts := 0
	for attempts < r.maxAttempts {
		idx := ready[attempts%len(ready)]
		attempts++
		s := &r.specs[idx]
		retryCfg := r.specRetry(s)
		maxTries := retryCfg.MaxAttempts
		if maxTries < 1 {
			maxTries = 1
		}

		for try := 1; try <= maxTries; try++ {
			timeout := r.specTimeout(s)
			r.logger.Debug("provider_attempt",
				"provider", s.Provider.ID(),
				"attempt", fmt.Sprintf("%d/%d", attempts, len(ready)),
				"try", try,
				"timeout", timeout.String(),
			)

			attemptBody, err := clampBody(body, int64(s.Provider.MaxTokens()))
			if err != nil {
				ce := &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error(), ProviderID: s.Provider.ID(), Err: err}
				lastChain = ce
				r.recordFailure(s)
				break
			}
			// 010-002 P4: inyección per-attempt del default prescriptivo
			// (provider-aware). El body ya pasó el clamp por modelo en el
			// proxy y el clamp por provider acá; la inyección del
			// (path, modelo) verificado (§5.4) va sobre el body del intento.
			// El effort explícito del cliente (P5) desactiva la inyección
			// en TODOS los intentos (explicitEffort, decidido arriba).
			if !explicitEffort {
				attemptBody, err = r.injectThinkingForAttempt(attemptBody, req.Model, s)
				if err != nil {
					ce := &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error(), ProviderID: s.Provider.ID(), Err: err}
					lastChain = ce
					r.recordFailure(s)
					break
				}
			}
			actx, cancel := timeouts.Attempt(ctx, timeout)
			res, err := s.Provider.Complete(actx, attemptBody)
			cancel()
			if err == nil {
				res.ProviderID = s.Provider.ID()
				r.resetDegraded()
				return res, nil
			}
			clErr := timeouts.Classify(err)
			retryable, status, typ, msg := classify(clErr)
			lastChain = &ChainError{Status: status, Type: typ, Code: typ, Message: msg, ProviderID: s.Provider.ID(), Err: clErr}
			if !retryable {
				return nil, lastChain
			}

			// 002-001: reintentar el MISMO provider con backoff.
			if try < maxTries {
				wait := r.backoffFor(retryCfg, try-1, clErr)
				r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String(), "causa", fmt.Sprintf("%d", status))
				if !sleepCtx(ctx, wait) {
					return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
				}
				continue
			}

			// provider_fallback: próximo índice circular
			nextIdx := attempts % len(ready)
			nextProvider := r.specs[ready[nextIdx]].Provider.ID()
			r.logger.Debug("provider_fallback",
				"provider_fallido", s.Provider.ID(),
				"causa", fmt.Sprintf("%d", status),
				"proximo_provider", nextProvider,
			)

			r.recordFailure(s)
			r.logger.Warn("intento fallido", "provider", s.Provider.ID(), "status", status)
			break
		}
	}
	r.logger.Debug("decision", "motivo", "max attempts reached", "attempts", attempts)
	return nil, exhaustedChain(lastChain)
}

// StreamResult es el resultado de Stream: el provider que ganó y el
// canal ya con el primer evento consumido (para que el proxy haga
// passthrough directo post-primer-byte).
type StreamResult struct {
	Provider provider.Provider
	Events   <-chan provider.StreamEvent
}

// Stream recorre la cadena para un request streaming. Bufferiza hasta el
// primer evento: si el intento falla antes del primer byte (429, 5xx,
// timeout, red), lo descarta silenciosamente y prueba el siguiente
// provider (con reintentos 002-001 sobre el mismo). El cliente solo ve
// el primer intento que produce un primer byte. API legacy (P8):
// comportamiento idéntico a StreamFor con stickyKey "".
func (r *Router) Stream(ctx context.Context, req *provider.ChatRequest, body []byte) (*StreamResult, error) {
	return r.stream(ctx, req, body, "")
}

// StreamFor recorre la cadena para un request streaming con afinidad
// sticky (009-002 P5): mismo reorder post-filtro que CompleteFor + el
// loop de Stream. El preferido se prueba primero; si falla antes del
// primer byte, el fallback funciona igual (429 → siguiente). Después del
// primer byte el stream queda pinneado al provider que lo arrancó (regla
// 001-003 §B, sin cambios). stickyKey "" → idéntico a Stream.
func (r *Router) StreamFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*StreamResult, error) {
	return r.stream(ctx, req, body, stickyKey)
}

// stream es el loop compartido de Stream/StreamFor: bufferiza hasta el
// primer evento y prueba los providers en el orden dado (reorder sticky
// aplicado si corresponde).
func (r *Router) stream(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*StreamResult, error) {
	// 010-002 P5: el effort explícito del cliente se decide UNA vez sobre
	// el body original (ensureIncludeUsage solo toca stream_options, nunca
	// los campos de thinking).
	explicitEffort := false
	if _, ok := r.modelMeta[req.Model]; ok {
		explicitEffort = bodyHasExplicitEffort(body)
	}
	// 003-001 P2: garantizar include_usage en streaming para que el chunk
	// final traiga usage (incluidos campos de cache). No aplica a no-stream.
	body, err := ensureIncludeUsage(body)
	if err != nil {
		return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error()}
	}
	ready, err := r.resolveReady(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		r.logger.Debug("decision", "motivo", "no provider serves model", "model", req.Model)
		return nil, &ChainError{Status: http.StatusNotFound, Type: "model_not_found", Code: "model_not_found", Message: fmt.Sprintf("no provider serves model %q", req.Model)}
	}
	ready = r.applyStickyReorder(ready, stickyKey, req.Model)

	r.logger.Debug("decision", "motivo", "candidatos listos", "model", req.Model, "count", len(ready))

	var lastChain *ChainError
	attempts := 0
	for attempts < r.maxAttempts {
		idx := ready[attempts%len(ready)]
		attempts++
		s := &r.specs[idx]
		retryCfg := r.specRetry(s)
		maxTries := retryCfg.MaxAttempts
		if maxTries < 1 {
			maxTries = 1
		}

		for try := 1; try <= maxTries; try++ {
			// 029-001 (TECHDEBT #29): el tope del intento de stream es el
			// FIRST-TOKEN timeout (TTFB), no el timeout global de Complete.
			// Un upstream que no emite primer token en N aborta y rota;
			// después del primer byte el stream lo gobierna write_timeout.
			timeout := r.specFirstTokenTimeout(s)
			r.logger.Debug("provider_attempt",
				"provider", s.Provider.ID(),
				"attempt", fmt.Sprintf("%d/%d", attempts, len(ready)),
				"try", try,
				"ttfb_timeout", timeout.String(),
			)

			attemptBody, err := clampBody(body, int64(s.Provider.MaxTokens()))
			if err != nil {
				lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error(), ProviderID: s.Provider.ID(), Err: err}
				r.recordFailure(s)
				break
			}
			// 010-002 P4: inyección per-attempt del default prescriptivo
			// (provider-aware) sobre el body del intento — que ya incluye
			// stream_options.include_usage (003-001, I6). El effort
			// explícito (P5) desactiva la inyección en todos los intentos.
			if !explicitEffort {
				attemptBody, err = r.injectThinkingForAttempt(attemptBody, req.Model, s)
				if err != nil {
					lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error(), ProviderID: s.Provider.ID(), Err: err}
					r.recordFailure(s)
					break
				}
			}
			// ctx hijo SIN deadline de intento, SOLO cancelable (spec 001-006:
			// el timeout de Stream mide TTFB, no la duración total — después
			// del primer byte el stream lo gobierna el write_timeout del
			// server). NO usar timeouts.Attempt acá: un ctx con deadline
			// cancelaría el body read del HTTP request del provider en un
			// stream largo (incidente 09 Ago 2026, fix 1efef81 revertido).
			// El TTFB se mide con un timer separado (más abajo).
			pctx, pcancel := context.WithCancel(ctx)
			ch, err := s.Provider.Stream(pctx, attemptBody)
			if err != nil {
				pcancel()
				clErr := timeouts.Classify(err)
				retryable, _, _, msg := classify(clErr)
				lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: msg, ProviderID: s.Provider.ID(), Err: clErr}
				if !retryable {
					return nil, lastChain
				}
				if try < maxTries {
					wait := r.backoffFor(retryCfg, try-1, clErr)
					r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String())
					if !sleepCtx(ctx, wait) {
						return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
					}
					continue
				}
				r.logFallback(s, attempts, len(ready), "timeout/network")
				r.recordFailure(s)
				continue
			}

			// commitStream arma la goroutine merged con defer pcancel():
			// re-emite el primer evento + el resto del canal, y libera el
			// context del hijo al terminar el drenado (m5).
			commitStream := func(first provider.StreamEvent) *StreamResult {
				merged := make(chan provider.StreamEvent, 8)
				go func() {
					defer close(merged)
					defer pcancel()
					merged <- first
					for ev := range ch {
						merged <- ev
					}
				}()
				r.resetDegraded()
				return &StreamResult{Provider: s.Provider, Events: merged}
			}

			if timeout > 0 {
				// TTFB: timer que compite contra el primer evento (M2, M4).
				// timeout = first-token timeout (029-001 / TECHDEBT #29).
				ttfb := time.NewTimer(timeout)
				select {
				case <-ttfb.C:
					// ← CRÍTICO (M2): aborta el HTTP request → la goroutine
					// del provider sale → ch se cierra. Sin pcancel() acá se
					// filtra la goroutine/conexión.
					pcancel()
					lastChain = &ChainError{
						Status:     http.StatusBadGateway,
						Type:       "upstream_error",
						Code:       "upstream_unavailable",
						Message:    "ttfb timeout",
						ProviderID: s.Provider.ID(),
						Err:        provider.NewErrUpstream(http.StatusBadGateway, "timeout", "ttfb timeout"),
					}
					if try < maxTries {
						wait := r.backoffFor(retryCfg, try-1, lastChain.Err)
						r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String(), "causa", "ttfb timeout")
						if !sleepCtx(ctx, wait) {
							return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
						}
						continue
					}
					r.logFallback(s, attempts, len(ready), "ttfb timeout")
					r.recordFailure(s)
					continue
				case first, ok := <-ch:
					// drain del timer si ya disparó en la carrera del select
					// (M3): un timer detenido que ya expiró deja valor en su
					// canal; el select elige aleatoriamente si ambos están
					// listos, y en el borde exacto se trata como timeout.
					if !ttfb.Stop() {
						<-ttfb.C
					}
					if !ok {
						pcancel()
						lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "stream closed before first byte", ProviderID: s.Provider.ID()}
						if try < maxTries {
							wait := r.backoffFor(retryCfg, try-1, nil)
							r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String())
							if !sleepCtx(ctx, wait) {
								return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
							}
							continue
						}
						r.logFallback(s, attempts, len(ready), "stream closed before first byte")
						r.recordFailure(s)
						continue
					}
					if first.Err != nil {
						pcancel()
						clErr := timeouts.Classify(first.Err)
						retryable, _, _, msg := classify(clErr)
						lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: msg, ProviderID: s.Provider.ID(), Err: clErr}
						if !retryable {
							return nil, lastChain
						}
						if try < maxTries {
							wait := r.backoffFor(retryCfg, try-1, clErr)
							r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String())
							if !sleepCtx(ctx, wait) {
								return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
							}
							continue
						}
						r.logFallback(s, attempts, len(ready), "first event error")
						r.recordFailure(s)
						continue
					}
					return commitStream(first), nil
				}
			}

			// timeout <= 0 → sin TTFB (M4): espera el primer byte sin límite
			// de tiempo; el stream completo lo gobierna write_timeout.
			// Manejo de !ok/first.Err idéntico al branch timeout>0 (m2).
			first, ok := <-ch
			if !ok {
				pcancel()
				lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "stream closed before first byte", ProviderID: s.Provider.ID()}
				if try < maxTries {
					wait := r.backoffFor(retryCfg, try-1, nil)
					r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String())
					if !sleepCtx(ctx, wait) {
						return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
					}
					continue
				}
				r.logFallback(s, attempts, len(ready), "stream closed before first byte")
				r.recordFailure(s)
				continue
			}
			if first.Err != nil {
				pcancel()
				clErr := timeouts.Classify(first.Err)
				retryable, _, _, msg := classify(clErr)
				lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: msg, ProviderID: s.Provider.ID(), Err: clErr}
				if !retryable {
					return nil, lastChain
				}
				if try < maxTries {
					wait := r.backoffFor(retryCfg, try-1, clErr)
					r.logger.Debug("retry_same_provider", "provider", s.Provider.ID(), "try", try, "wait", wait.String())
					if !sleepCtx(ctx, wait) {
						return nil, &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "request cancelled during retry backoff", ProviderID: s.Provider.ID(), Err: ctx.Err()}
					}
					continue
				}
				r.logFallback(s, attempts, len(ready), "first event error")
				r.recordFailure(s)
				continue
			}
			return commitStream(first), nil
		}
	}
	r.logger.Debug("decision", "motivo", "max attempts reached", "attempts", attempts)
	return nil, exhaustedChain(lastChain)
}

// logFallback registra el evento provider_fallback (compartido por los
// tres puntos de fallback del streaming).
func (r *Router) logFallback(s *ProviderSpec, attempts, total int, causa string) {
	nextIdx := attempts % total
	nextProvider := r.specs[nextIdx].Provider.ID()
	r.logger.Debug("provider_fallback",
		"provider_fallido", s.Provider.ID(),
		"causa", causa,
		"proximo_provider", nextProvider,
	)
}

// exhaustedChain normaliza el error final de una cadena agotada: el
// cliente ve SIEMPRE 502 upstream_error (regla 001-003 §B / 002-003),
// nunca el status crudo del último provider (500/429 son internos del
// upstream). 4xx de cliente no pasan por acá (retornan antes).
// ProviderID y Err se conservan para logs.
func exhaustedChain(last *ChainError) *ChainError {
	if last == nil {
		return &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: "all providers failed"}
	}
	if last.Status >= http.StatusInternalServerError || last.Status == http.StatusTooManyRequests {
		return &ChainError{
			Status:     http.StatusBadGateway,
			Type:       "upstream_error",
			Code:       "upstream_unavailable",
			Message:    "all providers failed",
			ProviderID: last.ProviderID,
			Err:        last.Err,
		}
	}
	return last
}
