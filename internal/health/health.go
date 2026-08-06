// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package health implementa el sondeo proactivo de disponibilidad de
// providers (feature 002-002-health). Un solo ticker recorre los
// providers registrados y mide latencia de un request liviano de health
// (GET {base_url}/models con auth mínima). El estado vive en memoria:
// { healthy, lastCheck, lastLatency, consecutiveFailures }, protegido
// por un mutex propio (NO comparte el del cooldown del router — evita
// contención entre el ticker y el hot path).
//
// El health check NUNCA decide por sí solo: el router de 001-003
// consume el estado para saltear providers unhealthy (soft: si no hay
// otro disponible, igual los intenta). Los fallos del health check NO
// ensucian el cooldown de 001-003.
package health

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// State es el estado en memoria de un provider (expuesto en /healthz).
type State struct {
	Healthy             bool
	LastCheck           time.Time
	LastLatency         time.Duration
	ConsecutiveFailures int
}

// Checker es la interfaz que un provider debe implementar para ser
// sondeado: ID() y Health(ctx) que devuelve la latencia del check.
type Checker interface {
	ID() string
	Health(ctx context.Context) (time.Duration, error)
}

// Store mantiene el estado de salud de todos los providers registrados.
// Un solo goroutine (ticker) recorre los providers; la granularidad de
// intervalo por provider se respeta con el lastCheck de cada entrada.
type Store struct {
	mu       sync.Mutex
	states   map[string]*State
	checkers map[string]Checker
	interval time.Duration
	timeout  time.Duration
	logger   *slog.Logger
	now      func() time.Time
}

// New construye un Store vacío. interval es el período global de sondeo;
// timeout el TTFB máximo de cada check. now es inyectable (tests).
func New(interval, timeout time.Duration, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		states:   map[string]*State{},
		checkers: map[string]Checker{},
		interval: interval,
		timeout:  timeout,
		logger:   logger,
		now:      time.Now,
	}
}

// Register agrega un checker al sondeo. Si ya existía, lo reemplaza y
// resetea su estado.
func (s *Store) Register(c Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers[c.ID()] = c
	s.states[c.ID()] = &State{}
}

// Start lanza el ticker de sondeo. Se detiene al cancelar ctx.
// Devuelve un chan que se cierra cuando el goroutine termina.
func (s *Store) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.CheckDue()
			}
		}
	}()
	return done
}

// CheckDue sondea los providers cuyo intervalo venció (lastCheck + interval).
func (s *Store) CheckDue() {
	s.mu.Lock()
	due := make([]Checker, 0, len(s.checkers))
	now := s.now()
	for id, c := range s.checkers {
		st := s.states[id]
		if now.Sub(st.LastCheck) >= s.interval {
			due = append(due, c)
		}
	}
	s.mu.Unlock()
	for _, c := range due {
		s.checkOne(c)
	}
}

// CheckNow sondea TODOS los providers inmediatamente (tests, o
// arranque: primer check sin esperar el intervalo).
func (s *Store) CheckNow() {
	s.mu.Lock()
	all := make([]Checker, 0, len(s.checkers))
	for _, c := range s.checkers {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		s.checkOne(c)
	}
}

// checkOne ejecuta un check individual y actualiza el estado.
func (s *Store) checkOne(c Checker) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	start := s.now()
	latency, err := c.Health(ctx)
	latency = s.now().Sub(start)

	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[c.ID()]
	if st == nil {
		st = &State{}
		s.states[c.ID()] = st
	}
	st.LastCheck = s.now()
	st.LastLatency = latency
	if err != nil {
		st.ConsecutiveFailures++
		st.Healthy = false
		s.logger.Warn("health check fallido", "provider", c.ID(), "err", err, "consecutive", st.ConsecutiveFailures)
		return
	}
	st.ConsecutiveFailures = 0
	st.Healthy = true
	s.logger.Debug("health check ok", "provider", c.ID(), "latency", latency.String())
}

// IsHealthy reporta si el provider está sano. Providers desconocidos
// (no registrados o sin check aún) se consideran healthy (cold start:
// no saltear providers que nunca fueron sondeados).
func (s *Store) IsHealthy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[id]
	if !ok {
		return true
	}
	return st.Healthy
}

// Snapshot devuelve una copia del estado de todos los providers
// (para /healthz, sin locks durante la serialización).
func (s *Store) Snapshot() map[string]State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]State, len(s.states))
	for id, st := range s.states {
		out[id] = *st
	}
	return out
}

// Len reporta cuántos providers están registrados (tests).
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.checkers)
}
