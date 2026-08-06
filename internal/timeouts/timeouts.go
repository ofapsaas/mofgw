// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package timeouts centraliza la semántica de timeout por intento
// (feature 001-006). Tres estados para el valor configurado:
//
//   - ausente (nil)     -> se usa el timeout global del fallback
//   - 0 explícito       -> sin timeout (el intento puede durar lo que tarde)
//   - N (positivo)      -> timeout de N para ese intento
//
// El paquete es puro: sin I/O, sin estado global.
package timeouts

import (
	"context"
	"errors"
	"time"
)

// ErrTimeout marca un intento que excedió su timeout (TTFB).
var ErrTimeout = errors.New("mofgw: attempt timeout")

// For resuelve los tres estados contra el default global.
func For(perProvider *time.Duration, global time.Duration) time.Duration {
	if perProvider == nil {
		return global
	}
	return *perProvider
}

// Attempt deriva un contexto con la semántica de tres estados.
// timeout == 0 (o negativo) significa "sin timeout": devuelve el ctx
// padre sin deadline. Devuelve también la función de cancelación.
func Attempt(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

// IsTimeout reporta si err es (o envuelve) ErrTimeout.
func IsTimeout(err error) bool {
	return errors.Is(err, ErrTimeout)
}

// Classify normaliza el error de un intento: si el contexto expiró
// (context.DeadlineExceeded), devuelve ErrTimeout para que el router lo
// trate como retryable. Cualquier otro error pasa intacto.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	return err
}
