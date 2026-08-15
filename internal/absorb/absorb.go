// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package absorb centraliza TODO el mapeo de errores salientes del proxy
// (feature 002-003-absorcion). Es la ÚLTIMA capa de absorción: garantiza
// que CUALQUIER situación de fallo total produzca una respuesta de error
// coherente, en formato OpenAI-compatible, saneada (sin URLs, headers,
// keys, IPs, stack traces internos), con el status HTTP correcto, y que
// el operador pueda diagnosticar desde los logs (no desde el mensaje del
// cliente).
//
// Mapeo de status:
//
//	4xx cliente            -> mismo status (formato OpenAI)
//	401/403 de provider    -> 502 provider_auth_error (config del proxy)
//	red/timeout/5xx/429    -> 502 upstream_unavailable
//	panic/bug interno      -> 500 internal_error
//	todo lo demás          -> 502 upstream_unavailable
package absorb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/router"
)

// AbsorbedError es la estructura pública mínima de un error saliente.
// Err conserva el error original SOLO para logs — nunca viaja al cliente.
// RetryAfter (0 = ausente) se traduce al header Retry-After (TECHDEBT
// #30, brazo abstain): el cliente sabe cuándo volver a intentar.
type AbsorbedError struct {
	StatusCode int
	Code       string
	Message    string
	Type       string
	ProviderID string
	RetryAfter time.Duration
	Err        error
}

func (e *AbsorbedError) Error() string {
	return fmt.Sprintf("%s (%d): %s", e.Code, e.StatusCode, e.Message)
}

// Unwrap permite errors.Is/As sobre el error original.
func (e *AbsorbedError) Unwrap() error { return e.Err }

// ErrAllProvidersDown es el error centinela de degradación total
// (002-004-degradacion): 502 con code all_providers_down.
var ErrAllProvidersDown = &AbsorbedError{
	StatusCode: http.StatusBadGateway,
	Code:       "all_providers_down",
	Message:    "no providers available",
	Type:       "upstream_error",
}

// Sanitize convierte un error (del router, del provider, o interno) en
// un AbsorbedError seguro de enviar al cliente. El detalle completo del
// error original queda en Err (para logs del proxy, con provider ID).
func Sanitize(err error) *AbsorbedError {
	if err == nil {
		return nil
	}
	// Ya absorbido: passthrough.
	var ae *AbsorbedError
	if errors.As(err, &ae) {
		return ae
	}
	// Error del router (cadena agotada o error de cliente).
	var ce *router.ChainError
	if errors.As(err, &ce) {
		return sanitizeChain(ce, err)
	}
	// Error crudo de provider (por si llega sin envolver).
	var ue *provider.ErrUpstream
	if errors.As(err, &ue) {
		return sanitizeUpstream(ue, err)
	}
	// Cualquier otra cosa: error interno del proxy.
	return &AbsorbedError{
		StatusCode: http.StatusInternalServerError,
		Code:       "internal_error",
		Message:    "internal server error",
		Type:       "server_error",
		Err:        err,
	}
}

func sanitizeChain(ce *router.ChainError, original error) *AbsorbedError {
	out := &AbsorbedError{
		StatusCode: ce.Status,
		Code:       ce.Code,
		Message:    ce.Message,
		Type:       ce.Type,
		ProviderID: ce.ProviderID,
		RetryAfter: ce.RetryAfter,
		Err:        original,
	}
	// 401/403 de provider = config del proxy, no error del cliente.
	if ce.ProviderID != "" && (ce.Status == http.StatusUnauthorized || ce.Status == http.StatusForbidden) {
		out.StatusCode = http.StatusBadGateway
		out.Code = "provider_auth_error"
		out.Message = "provider authentication failed (check proxy configuration)"
		out.Type = "upstream_error"
		return out
	}
	// 4xx de cliente pasan con su status original (model_not_found,
	// invalid_request). 5xx/timeout/429 agotado -> 502 upstream_unavailable,
	// EXCEPTO los códigos semánticos de 002-004 (all_providers_down,
	// degraded_limit) y el abstain de cadena agotada (chain_exhausted,
	// TECHDEBT #30) que se conservan tal cual para el operador/cliente.
	if ce.Status >= 500 || ce.Status == http.StatusTooManyRequests {
		switch ce.Code {
		case "all_providers_down", "degraded_limit", "chain_exhausted":
			// 002-004: códigos propios, status propio (502/503).
			// chain_exhausted: señal abstain — 502 con código semántico,
			// el cliente distingue "agoté toda la cadena" de un 502 plano.
			out.StatusCode = ce.Status
			out.Message = genericUpstreamMessage(ce.Message)
		default:
			out.StatusCode = http.StatusBadGateway
			out.Code = "upstream_unavailable"
			out.Message = genericUpstreamMessage(ce.Message)
			out.Type = "upstream_error"
		}
	}
	return out
}

func sanitizeUpstream(ue *provider.ErrUpstream, original error) *AbsorbedError {
	out := &AbsorbedError{
		StatusCode: http.StatusBadGateway,
		Code:       "upstream_unavailable",
		Message:    genericUpstreamMessage(ue.Message),
		Type:       "upstream_error",
		Err:        original,
	}
	if ue.StatusCode == http.StatusUnauthorized || ue.StatusCode == http.StatusForbidden {
		out.Code = "provider_auth_error"
		out.Message = "provider authentication failed (check proxy configuration)"
	}
	return out
}

// genericUpstreamMessage reemplaza el mensaje por uno genérico por
// categoría: el mensaje del provider NUNCA viaja al cliente (puede
// contener URLs, IDs internos, detalles de implementación). El detalle
// completo queda en logs.
func genericUpstreamMessage(_ string) string {
	return "upstream provider error"
}

// InternalError construye un 500 internal_error (panic, bugs).
func InternalError(err error) *AbsorbedError {
	return &AbsorbedError{
		StatusCode: http.StatusInternalServerError,
		Code:       "internal_error",
		Message:    "internal server error",
		Type:       "server_error",
		Err:        err,
	}
}

// ClientError construye un 4xx de cliente con formato OpenAI.
func ClientError(status int, message, typ string) *AbsorbedError {
	return &AbsorbedError{
		StatusCode: status,
		Code:       typ,
		Message:    message,
		Type:       typ,
	}
}

// Respond escribe el error absorbido como JSON OpenAI-compatible:
//
//	{"error":{"message":...,"type":...,"code":...}}
//
// Es la ÚNICA ruta por la que un error sale del proxy. Si el error
// lleva RetryAfter (TECHDEBT #30 — abstain de cadena agotada), se
// emite el header Retry-After (segundos enteros) para que el cliente
// haga backoff en vez de reintentar en loop.
func Respond(w http.ResponseWriter, err error) {
	ae := Sanitize(err)
	if ae == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if ae.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(ae.RetryAfter.Seconds()), 10))
	}
	w.WriteHeader(ae.StatusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": ae.Message,
			"type":    ae.Type,
			"code":    ae.Code,
		},
	})
}
