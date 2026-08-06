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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/ofapsaas/mofgw/internal/clamp"
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
	Retry          RetryConfig
	Degradation    DegradationConfig
	Health         *health.Store
	Logger         *slog.Logger
}

// ProviderSpec ata un provider a sus overrides de cooldown/timeout/retry
// (nil = usar el global del config).
type ProviderSpec struct {
	Provider provider.Provider
	Cooldown *time.Duration
	Timeout  *time.Duration
	Retry    *RetryConfig
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
	specs       []ProviderSpec
	maxAttempts int // max_retries + 1
	cooldown    time.Duration
	jitter      time.Duration
	globalTO    time.Duration
	retryCfg    RetryConfig
	degradCfg   DegradationConfig
	cooldowns   *CooldownStore
	health      *health.Store
	logger      *slog.Logger
	randSource  *rand.Rand // jitter determinista en tests

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
		specs:       specs,
		maxAttempts: o.MaxRetries + 1,
		cooldown:    o.Cooldown,
		jitter:      o.CooldownJitter,
		globalTO:    o.GlobalTimeout,
		retryCfg:    o.Retry,
		degradCfg:   o.Degradation,
		cooldowns:   NewCooldownStore(nil, 30*time.Second),
		health:      o.Health,
		logger:      o.Logger,
		randSource:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Cooldowns expone el store (para tests y observabilidad).
func (r *Router) Cooldowns() *CooldownStore { return r.cooldowns }

// Health expone el store de salud (para /healthz del proxy).
func (r *Router) Health() *health.Store { return r.health }

// candidates devuelve los índices de providers que sirven el modelo, en
// orden de config, sin los que están en cooldown. Con health activo
// (002-002): saltea providers unhealthy salvo que no haya ningún otro
// disponible (marca SOFT — un provider unhealthy puede estar degradado
// pero responder). serveAny distingue "ninguno sirve el modelo" de
// "todos en cooldown/unhealthy".
func (r *Router) candidates(model string) (ready []int, serveAny bool) {
	var soft []int
	for i := range r.specs {
		if r.specs[i].Provider.Serves(model) {
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
	ready, serveAny := r.candidates(model)
	if len(ready) > 0 || !serveAny {
		return ready, nil
	}
	// Todos en cooldown/unhealthy: grace para cooldowns cortos.
	if r.degradCfg.CooldownGrace > 0 {
		if wait := r.shortestCooldown(model); wait > 0 && wait <= r.degradCfg.CooldownGrace {
			r.logger.Debug("degraded: waiting for cooldown expiry", "wait", wait.String())
			if sleepCtx(ctx, wait) {
				ready, _ = r.candidates(model)
				if len(ready) > 0 {
					return ready, nil
				}
			}
		}
	}
	return nil, r.fastFail()
}

// Complete recorre la cadena para un request no-stream. Devuelve la
// respuesta del primer provider exitoso, o ChainError.
func (r *Router) Complete(ctx context.Context, req *provider.ChatRequest, body []byte) (*provider.CompleteResult, error) {
	ready, err := r.resolveReady(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		r.logger.Debug("decision", "motivo", "no provider serves model", "model", req.Model)
		return nil, &ChainError{Status: http.StatusNotFound, Type: "model_not_found", Code: "model_not_found", Message: fmt.Sprintf("no provider serves model %q", req.Model)}
	}

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
// el primer intento que produce un primer byte.
func (r *Router) Stream(ctx context.Context, req *provider.ChatRequest, body []byte) (*StreamResult, error) {
	ready, err := r.resolveReady(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		r.logger.Debug("decision", "motivo", "no provider serves model", "model", req.Model)
		return nil, &ChainError{Status: http.StatusNotFound, Type: "model_not_found", Code: "model_not_found", Message: fmt.Sprintf("no provider serves model %q", req.Model)}
	}

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
				lastChain = &ChainError{Status: http.StatusBadGateway, Type: "upstream_error", Code: "upstream_unavailable", Message: err.Error(), ProviderID: s.Provider.ID(), Err: err}
				r.recordFailure(s)
				break
			}
			actx, cancel := timeouts.Attempt(ctx, timeout)
			ch, err := s.Provider.Stream(actx, attemptBody)
			if err != nil {
				cancel()
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
			// Buffer del primer evento (frontera primer-byte).
			first, ok := <-ch
			if !ok {
				cancel()
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
				cancel()
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
			// Comprometido: re-emitimos el primer evento + el resto del canal.
			merged := make(chan provider.StreamEvent, 8)
			go func() {
				defer close(merged)
				merged <- first
				for ev := range ch {
					merged <- ev
				}
				cancel()
			}()
			r.resetDegraded()
			return &StreamResult{Provider: s.Provider, Events: merged}, nil
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
