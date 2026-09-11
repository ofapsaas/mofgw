// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelscache

// Fetch HTTP del motor (generalización de modelsdev/fetch.go de 001): GET
// anónimo con solo User-Agent propio (I7), timeout de FetchTimeout por
// intento sobre el ctx del llamador (D9 de 001), retry transitorio de
// exactamente 2 intentos totales (5xx/network; timeout NUNCA reintenta,
// HITL B1; 4xx/parse/canceled no) y knob de deshabilitación leído POR
// LLAMADA (D5/R4 de 001).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ofapsaas/mofgw/internal/build"
)

const (
	// FetchTimeout es el tope de CADA intento HTTP (D9 de 001); se aplica
	// como deadline interno sobre el ctx del llamador.
	FetchTimeout = 10 * time.Second

	// maxFetchAttempts: exactamente 2 intentos totales (1 reintento, D9).
	maxFetchAttempts = 2

	// retryBackoff: pausa entre intentos transitorios (D9, ~500ms).
	retryBackoff = 500 * time.Millisecond

	// maxBodySize cota la lectura del body (catálogo real ~4.6 MB, D1 de 001).
	maxBodySize = 64 << 20
)

var (
	// ErrFetchDisabled indica que el knob de la fuente está activo: no se
	// emitió ningún request (D5 de 001). Comparable con errors.Is; las
	// fuentes lo exponen como alias de la misma var (D8 de 002).
	ErrFetchDisabled = errors.New("modelscache: fetch disabled")

	// ErrLockBusy indica que el flock del cache lo sostiene otro proceso
	// (D8 de 001). Comparable con errors.Is.
	ErrLockBusy = errors.New("modelscache: cache lock busy")
)

// FetchError es el error clasificado de un intento fallido, a la manera
// ErrUpstream de provider.go (D9 de 001): timeout / network / canceled /
// status / parse.
type FetchError struct {
	Attempt int
	Type    string // "timeout" | "network" | "canceled" | "status" | "parse"
	Status  int    // solo Type == "status"; 0 en el resto
	Message string
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("modelscache: fetch: intento %d: %s (status %d)", e.Attempt, e.Type, e.Status)
	}
	return fmt.Sprintf("modelscache: fetch: intento %d: %s: %s", e.Attempt, e.Type, e.Message)
}

// Fetch hace GET al upstream de la fuente y devuelve el valor parseado
// (tipado T) + el body crudo byte-idéntico al servido (P3 de 001). Ante
// fallo transitorio reintenta una vez con backoff ~500ms (D9); 4xx no
// reintenta (P4). Con el knob de la fuente activo devuelve un error
// envolviendo ErrFetchDisabled sin NINGÚN request, leyendo la env en CADA
// llamada (D5/R4).
func Fetch[T any](ctx context.Context, spec Spec, opts ...Option) (T, []byte, error) {
	var zero T
	parsed, raw, err := fetchAny(ctx, spec, opts)
	if err != nil {
		return zero, nil, err
	}
	val, ok := parsed.(T)
	if !ok {
		return zero, nil, fmt.Errorf("modelscache: fetch: el parse de la fuente devolvió %T, want %T", parsed, zero)
	}
	return val, raw, nil
}

// fetchAny es la forma no genérica del fetch (la usa Store.Refresh, que
// solo necesita el raw): resuelve Options y corre el loop de intentos.
func fetchAny(ctx context.Context, spec Spec, opts []Option) (any, []byte, error) {
	return fetchAnyWith(ctx, spec, resolveOptions(spec, opts))
}

// fetchAnyWith es el motor del fetch, compartido entre Fetch y
// Store.Refresh. Eventos de log (D7 de 001): fetch_ok{bytes,duration},
// fetch_failed{attempt,err} — con atributo source solo si Spec.Source no
// es vacío (D9/P14 de 002).
func fetchAnyWith(ctx context.Context, spec Spec, o Options) (any, []byte, error) {
	if o.baseURL == "" {
		return nil, nil, fmt.Errorf("modelscache: fetch: baseURL vacío: Spec.BaseURL u Option WithBaseURL requerido")
	}
	if spec.Parse == nil {
		return nil, nil, fmt.Errorf("modelscache: fetch: Spec.Parse requerido")
	}
	if fetchDisabled(spec) {
		return nil, nil, fmt.Errorf("modelscache: fetch: %w", ErrFetchDisabled)
	}
	start := time.Now()
	for attempt := 1; ; attempt++ {
		parsed, raw, fe := attemptFetch(ctx, spec, o, attempt)
		if fe == nil {
			o.logger.Info("fetch_ok", sourceAttrs(spec, "bytes", len(raw), "duration", time.Since(start))...)
			return parsed, raw, nil
		}
		o.logger.Warn("fetch_failed", sourceAttrs(spec, "attempt", attempt, "err", fe)...)
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
// sobre el ctx del llamador (D9 de 001), agrega la auth de la fuente si
// corresponde (D4 de 002) y clasifica el resultado.
func attemptFetch(ctx context.Context, spec Spec, o Options, attempt int) (any, []byte, *FetchError) {
	attemptCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, o.baseURL, nil)
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: "network", Message: "build request: " + err.Error()}
	}
	req.Header.Set("User-Agent", build.UserAgent) // P3/I7: nunca Go-http-client
	if spec.AuthEnv != "" {
		// D4 de 002: la env se lee POR LLAMADA; solo seteada y no vacía
		// agrega Bearer. El valor jamás entra en logs ni errores (P11/I12).
		if key := os.Getenv(spec.AuthEnv); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
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
	parsed, err := spec.Parse(raw)
	if err != nil {
		return nil, nil, &FetchError{Attempt: attempt, Type: "parse", Message: err.Error()}
	}
	return parsed, raw, nil
}

// sourceAttrs agrega el atributo "source" a los attrs del evento SOLO si
// Spec.Source no es vacío (D9/P14 de 002): modelsdev (Source="") conserva
// sus logs byte-idénticos a los de 001 (P17 de 001, gate).
func sourceAttrs(spec Spec, attrs ...any) []any {
	if spec.Source == "" {
		return attrs
	}
	return append(attrs, "source", spec.Source)
}

// classifyTransport clasifica el error de transporte del intento (#5 de
// 001): DeadlineExceeded → timeout (deadline del intento vencido),
// Canceled → canceled (cancelación del llamador; precisión semántica hacia
// 019-004), resto → network.
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
// latencia a 2×10s); D9 de 001 se reinterpreta: solo errores de transporte
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

// fetchDisabled lee el knob de la fuente POR LLAMADA (R4 de 001): solo
// "1" deshabilita (P13/I8: vacía o "0" → comportamiento normal). Un Spec
// sin KnobEnv ("") jamás deshabilita.
func fetchDisabled(spec Spec) bool {
	return spec.KnobEnv != "" && os.Getenv(spec.KnobEnv) == "1"
}
