// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package router

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// ==== 015-001-inter-attempt-delay: P3-P6 (router, contrato nuevo) ====
//
// Contrato de comportamiento que el implementer materializa en GREEN.
//
// NOTA RED-especial de compilación: estas tests referencian superficie de
// contrato NUEVA que aún no existe (Options.InterAttemptDelay y
// ProviderSpec.BaseURL). En Go tipado no se puede asertar sobre un campo que
// no existe → este RED falla por compilación (precedente 011-005
// "SetWebSearch undefined / el test-writer definió el contrato que el
// implementer implementa en GREEN"). Las aserciones internas son de
// comportamiento OBSERVABLE (qué provider sirvió + timing), nunca plumbing.
//
// Contrato declarado (para el implementer en GREEN):
//   - Options.InterAttemptDelay time.Duration  (0 = off, default)
//   - ProviderSpec.BaseURL string              (para comparar familia SPOF)
//   - En el loop de la cadena (stream y complete), cuando un intento falla
//     con causa TRANSITORIA de red (timeout/network/connect/IO/EOF pre-byte)
//     y el siguiente candidato COMPARTE BaseURL con el intento que falló, y
//     Options.InterAttemptDelay > 0 → dormir ese delay antes del siguiente
//     intento, ctx-aware (abortar si el ctx del cliente cancela) → P3/P6.
//   - 429 (cuota agotada) NUNCA suma delay → salto inmediato → P4.
//   - BaseURL distinta → SIN delay → salto inmediato → P5.
//
// Representación del error transitorio en el test: ErrUpstream 504 timeout
// (es la clase "timeout/network" del spec; 5xx ya es fallback-worthy en el
// router). El 429 se representa como ErrUpstream 429.

// sameBaseURL: familia de endpoint compartida (SPOF de cuentas del mismo
// proveedor — ej: 5 cuentas del mismo base_url).
const sameBaseURL = "https://accounts.example.com/v1"

// transientTimeoutError: fallo transitorio de red clase "timeout".
func transientTimeoutError() error {
	return &provider.ErrUpstream{
		StatusCode: http.StatusGatewayTimeout,
		Type:       "upstream_error",
		Message:    "upstream timeout (transient, retryable)",
	}
}

// rateLimitError: cuota agotada (429).
func rateLimitError() error {
	return &provider.ErrUpstream{
		StatusCode: http.StatusTooManyRequests,
		Type:       "upstream_error",
		Message:    "rate limited",
	}
}

// failPerm: hace que un provider falle PERMANENTEMENTE con el error dado
// (para forzar el fallback al siguiente candidato en el mismo request).
func failPerm(p *fakeProvider, err error) *fakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failN = 0
	p.errFor["complete"] = err
	return p
}

// Test015001_P3_DelayTransientSameBaseURL verifica P3: con N>0, ante un
// fallo transitorio donde el siguiente candidato comparte base_url, se
// duerme N antes del siguiente intento. RED: hoy sin el contrato, el
// fallback es instantáneo + compile-fail por Options.InterAttemptDelay /
// ProviderSpec.BaseURL.
func Test015001_P3_DelayTransientSameBaseURL(t *testing.T) {
	const delay = 150 * time.Millisecond
	p1 := failPerm(newFake("p1", "m"), transientTimeoutError())
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions([]ProviderSpec{
		{Provider: p1, BaseURL: sameBaseURL},
		{Provider: p2, BaseURL: sameBaseURL},
	}, Options{
		MaxRetries:        2,
		Cooldown:          time.Minute,
		GlobalTimeout:     5 * time.Second,
		InterAttemptDelay: delay,
	})

	start := time.Now()
	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (fallback)", res.ProviderID)
	}
	if elapsed < 120*time.Millisecond {
		t.Fatalf("fallo transitorio + misma base_url debe dormir %s antes del siguiente intento; duró %s", delay, elapsed)
	}
}

// Test015001_P4_NoDelayOn429 verifica P4: ante 429 (cuota agotada) NUNCA se
// añade delay — salto inmediato al siguiente intento, aunque el siguiente
// candidato comparta base_url.
func Test015001_P4_NoDelayOn429(t *testing.T) {
	p1 := failPerm(newFake("p1", "m"), rateLimitError())
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions([]ProviderSpec{
		{Provider: p1, BaseURL: sameBaseURL},
		{Provider: p2, BaseURL: sameBaseURL},
	}, Options{
		MaxRetries:        2,
		Cooldown:          time.Minute,
		GlobalTimeout:     5 * time.Second,
		InterAttemptDelay: 5 * time.Second, // si se aplicara por error, tardaría ~5s
	})

	start := time.Now()
	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (fallback)", res.ProviderID)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("429 (cuota) NUNCA debe añadir delay; duró %s", elapsed)
	}
}

// Test015001_P5_NoDelayDifferentBaseURL verifica P5: cuando el siguiente
// candidato tiene distinta base_url, SIN delay (salto inmediato) — la
// protección SPOF solo aplica dentro de la misma familia de endpoint.
func Test015001_P5_NoDelayDifferentBaseURL(t *testing.T) {
	p1 := failPerm(newFake("p1", "m"), transientTimeoutError())
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions([]ProviderSpec{
		{Provider: p1, BaseURL: "https://g.example.com/v1"},
		{Provider: p2, BaseURL: "https://q.example.com/v1"},
	}, Options{
		MaxRetries:        2,
		Cooldown:          time.Minute,
		GlobalTimeout:     5 * time.Second,
		InterAttemptDelay: 5 * time.Second, // si se aplicara por error, tardaría ~5s
	})

	start := time.Now()
	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (fallback)", res.ProviderID)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("base_url distinta NO debe añadir delay; duró %s", elapsed)
	}
}

// Test015001_P6_CancelDuringSleepReturnsImmediately verifica P6: si el ctx
// del cliente se cancela durante el sleep, el retorno es inmediato (no
// duerme pasada la cancelación).
func Test015001_P6_CancelDuringSleepReturnsImmediately(t *testing.T) {
	p1 := failPerm(newFake("p1", "m"), transientTimeoutError())
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions([]ProviderSpec{
		{Provider: p1, BaseURL: sameBaseURL},
		{Provider: p2, BaseURL: sameBaseURL},
	}, Options{
		MaxRetries:        2,
		Cooldown:          time.Minute,
		GlobalTimeout:     30 * time.Second,
		InterAttemptDelay: 30 * time.Second, // largo: si NO respeta cancel, duraría 30s
	})

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(60*time.Millisecond, cancel)

	start := time.Now()
	_, err := r.Complete(ctx, reqFor("m"), bodyFor("m", nil))
	elapsed := time.Since(start)

	if elapsed >= 2*time.Second {
		t.Fatalf("el delay debe abortar al cancelarse el ctx del cliente; duró %s (durmió pasada la cancelación)", elapsed)
	}
	if err == nil {
		t.Fatal("con el ctx cancelado durante el sleep, Complete no debe completar OK")
	}
}
