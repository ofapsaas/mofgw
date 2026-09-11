// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package modelscache es el motor genérico de fetch+cache extraído por
// refactor mecánico de internal/modelsdev (spec 019-002, D1/D8, precedente
// ADR-011): loop de intentos con backoff y cancelación, clasificación
// FetchError, política de retry (timeout NUNCA reintenta — HITL B1),
// Store en disco con flock exclusivo no bloqueante, digest sha256 en
// sidecar con commit sidecar-primero (HITL B2), TTL por mtime,
// fail-soft/fail-loud y escritura atómica temp+rename.
//
// El motor es parametrizable por Spec (URL base, parser tolerante,
// AuthEnv, KnobEnv, Source); las fuentes (modelsdev, upstream zen/go/
// openrouter) componen su Spec y exponen su API pública como facade con
// aliases de tipo — cero duplicación de mecanismo (R1 de 002).
package modelscache

import (
	"log/slog"
	"net/http"
)

// Spec es la configuración declarativa de UNA fuente upstream (D8 de 002):
// qué upstream (BaseURL), cómo parsearlo (Parse tolerante), con qué auth
// (AuthEnv), con qué knob se deshabilita (KnobEnv) y cómo se firma en los
// logs (Source).
type Spec struct {
	// BaseURL es el upstream de la fuente. Precedencia: Option
	// WithBaseURL del llamador > este campo (D8). Si ambos quedan
	// vacíos, el fetch falla (fail fast).
	BaseURL string

	// Parse es el parser tolerante de la fuente: campo ausente →
	// zero-value, campo desconocido → ignorado sin error; solo payload
	// inválido → error (I9 de 001).
	Parse func([]byte) (any, error)

	// AuthEnv es el nombre de la env var con la API key; seteada y no
	// vacía → cada request lleva "Authorization: Bearer <valor>". La env
	// se lee POR LLAMADA (D4 de 002) y el valor jamás se loguea ni
	// persiste (P11/I12). Vacío = fuente sin auth.
	AuthEnv string

	// KnobEnv es el nombre de la env var que deshabilita el fetch (solo
	// "1" deshabilita; vacía o "0" → fetch-on, P13/I8 de 001). Vacío =
	// fuente sin knob. Cada Spec lleva el suyo: aislamiento de knobs
	// (I13 de 002).
	KnobEnv string

	// Source es el atributo "source" agregado a los eventos de log del
	// motor SOLO si no es vacío (D9/P14 de 002): las fuentes lo firman
	// ("zen", "go", "openrouter"); modelsdev lo deja vacío para conservar
	// sus logs byte-idénticos a los de 001 (P17 de 001, gate).
	Source string
}

// Options son las opciones de Fetch/NewStore, resueltas por llamada (E1
// de 001).
type Options struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// Option modifica Options (patrón provider.NewClient/WithLogger, D3 de 001).
type Option func(*Options)

// WithBaseURL apunta Fetch a otro upstream (hook de test, D3 de 001). La
// precedencia es options > Spec.BaseURL (D8 de 002); un override vacío
// cae al default de la fuente.
func WithBaseURL(url string) Option {
	return func(o *Options) { o.baseURL = url }
}

// WithClient inyecta el http.Client (hook de test, D3 de 001). nil → default.
func WithClient(hc *http.Client) Option {
	return func(o *Options) {
		if hc != nil {
			o.client = hc
		}
	}
}

// WithLogger inyecta el logger operativo (D3/D7 de 001). nil → slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.logger = l
		}
	}
}

// resolveOptions aplica los defaults: baseURL de la fuente si el llamador
// no overrideó, client sin Timeout propio (el deadline por intento lo
// pone attemptFetch sobre el ctx) y slog.Default() como logger.
func resolveOptions(spec Spec, opts []Option) Options {
	o := Options{client: &http.Client{}, logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}
	if o.baseURL == "" {
		o.baseURL = spec.BaseURL // precedencia options > spec (D8)
	}
	if o.client == nil {
		o.client = &http.Client{}
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}
