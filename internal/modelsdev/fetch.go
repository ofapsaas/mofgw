// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelsdev

// Fetch HTTP contra models.dev (D1/D9): GET anónimo con solo User-Agent
// propio (I7), timeout de 10s por intento sobre el ctx del llamador (D9),
// retry transitorio de exactamente 2 intentos totales (5xx/network;
// timeout y 4xx no reintentan, P4/HITL 2026-09-11) y knob de
// deshabilitación leído POR LLAMADA (D5/R4).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ofapsaas/mofgw/internal/build"
)

const (
	// FetchTimeout es el tope de CADA intento HTTP (D9); se aplica como
	// deadline interno sobre el ctx del llamador.
	FetchTimeout = 10 * time.Second

	// maxFetchAttempts: exactamente 2 intentos totales (1 reintento, D9).
	maxFetchAttempts = 2

	// retryBackoff: pausa entre intentos transitorios (D9, ~500ms).
	retryBackoff = 500 * time.Millisecond

	// maxBodySize cota la lectura del body (payload real ~4.6 MB, D1).
	maxBodySize = 64 << 20

	// defaultBaseURL es el upstream real (D1).
	defaultBaseURL = "https://models.dev/api.json"
)

// KnobDisableModelsFetch es la env var que deshabilita el fetch (D5).
const KnobDisableModelsFetch = "MOFGW_DISABLE_MODELS_FETCH"

var (
	// ErrFetchDisabled indica que el knob MOFGW_DISABLE_MODELS_FETCH=1
	// está activo: no se emitió ningún request (D5). Comparable con
	// errors.Is.
	ErrFetchDisabled = errors.New("modelsdev: fetch disabled (MOFGW_DISABLE_MODELS_FETCH=1)")

	// ErrLockBusy indica que el flock del cache lo sostiene otro proceso
	// (D8). Comparable con errors.Is.
	ErrLockBusy = errors.New("modelsdev: cache lock busy")
)

// FetchError es el error clasificado de un intento fallido, a la manera
// ErrUpstream de provider.go (D9): timeout / network / status / parse.
type FetchError struct {
	Attempt int
	Type    string // "timeout" | "network" | "canceled" | "status" | "parse"
	Status  int    // solo Type == "status"; 0 en el resto
	Message string
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("modelsdev: fetch: intento %d: %s (status %d)", e.Attempt, e.Type, e.Status)
	}
	return fmt.Sprintf("modelsdev: fetch: intento %d: %s: %s", e.Attempt, e.Type, e.Message)
}

// Options son las opciones de Fetch/NewStore, resueltas por llamada (E1).
type Options struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// Option modifica Options (patrón provider.NewClient/WithLogger, D3).
type Option func(*Options)

// WithBaseURL apunta Fetch a otro upstream (hook de test, D3).
func WithBaseURL(url string) Option {
	return func(o *Options) { o.baseURL = url }
}

// WithClient inyecta el http.Client (hook de test, D3). nil → default.
func WithClient(hc *http.Client) Option {
	return func(o *Options) {
		if hc != nil {
			o.client = hc
		}
	}
}

// WithLogger inyecta el logger operativo (D3/D7). nil → slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.logger = l
		}
	}
}

// resolveOptions aplica los defaults: baseURL upstream real, client sin
// Timeout propio (el deadline por intento lo pone fetchWith sobre el ctx)
// y slog.Default() como logger.
func resolveOptions(opts []Option) Options {
	o := Options{baseURL: defaultBaseURL, client: &http.Client{}, logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}
	if o.baseURL == "" {
		o.baseURL = defaultBaseURL
	}
	if o.client == nil {
		o.client = &http.Client{}
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Fetch hace GET al upstream y devuelve el catálogo parseado + el body
// crudo byte-idéntico al servido (P3). Ante fallo transitorio reintenta
// una vez con backoff ~500ms (D9); 4xx no reintenta (P4). Con el knob
// MOFGW_DISABLE_MODELS_FETCH=1 devuelve ErrFetchDisabled sin ningún
// request, leyendo la env en CADA llamada (D5/R4).
func Fetch(ctx context.Context, opts ...Option) (*Catalog, []byte, error) {
	return fetchWith(ctx, resolveOptions(opts))
}

// fetchWith es el motor de fetch, compartido entre Fetch y Store.Refresh.
// Eventos de log (D7): fetch_ok{bytes,duration}, fetch_failed{attempt,err}.
func fetchWith(ctx context.Context, o Options) (*Catalog, []byte, error) {
	if fetchDisabled() {
		return nil, nil, fmt.Errorf("modelsdev: fetch: %w", ErrFetchDisabled)
	}
	start := time.Now()
	for attempt := 1; ; attempt++ {
		cat, raw, fe := attemptFetch(ctx, o, attempt)
		if fe == nil {
			o.logger.Info("fetch_ok", "bytes", len(raw), "duration", time.Since(start))
			return cat, raw, nil
		}
		o.logger.Warn("fetch_failed", "attempt", attempt, "err", fe)
		if attempt >= maxFetchAttempts || !retryable(ctx, fe) {
			return nil, nil, fe
		}
		select {
		case <-time.After(retryBackoff):
		case <-ctx.Done():
			return nil, nil, &FetchError{
				Attempt: attempt,
				Type:    "timeout",
				Message: "cancelado durante backoff: " + ctx.Err().Error(),
			}
		}
	}
}

// attemptFetch ejecuta UN intento con deadline propio de FetchTimeout
// sobre el ctx del llamador (D9) y clasifica el resultado.
func attemptFetch(ctx context.Context, o Options, attempt int) (*Catalog, []byte, *FetchError) {
	attemptCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, o.baseURL, nil)
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: "network", Message: "build request: " + err.Error()}
	}
	req.Header.Set("User-Agent", build.UserAgent) // P3/I7: nunca Go-http-client
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: classifyTransport(attemptCtx), Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: classifyTransport(attemptCtx), Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &FetchError{Attempt: attempt, Type: "status", Status: resp.StatusCode, Message: "upstream " + resp.Status}
	}
	cat, err := ParseCatalog(raw)
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: "parse", Message: err.Error()}
	}
	return cat, raw, nil
}

// classifyTransport clasifica el error de transporte del intento (#5):
// DeadlineExceeded → timeout (deadline del intento vencido), Canceled →
// canceled (cancelación del llamador; precisión semántica hacia 019-004;
// no reintenta — queda en el default de retryable), resto → network.
func classifyTransport(attemptCtx context.Context) string {
	err := attemptCtx.Err()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "network"
}

// retryable decide si un intento fallido justifica el reintento. Decisión
// HITL 2026-09-11 (B1/P4, review 019-001): timeout NUNCA reintenta — un
// intento colgado ya consumió su presupuesto (reintentarlo duplicaría la
// latencia a 2×10s); D9 se reinterpreta: solo errores de transporte
// network y 5xx son transitorios. 4xx y errores de parseo no; un ctx ya
// cancelado tampoco.
func retryable(callerCtx context.Context, fe *FetchError) bool {
	if callerCtx.Err() != nil {
		return false
	}
	switch fe.Type {
	case "status":
		return fe.Status >= 500
	case "network":
		return true
	case "timeout":
		return false // P4/HITL 2026-09-11: presupuesto de intento ya consumido
	default: // "parse", "canceled" y futuros: no transitorios
		return false
	}
}

// fetchDisabled lee el knob POR LLAMADA (R4): solo "1" deshabilita
// (P13/I8: vacía o "0" → comportamiento normal).
func fetchDisabled() bool {
	return os.Getenv(KnobDisableModelsFetch) == "1"
}
