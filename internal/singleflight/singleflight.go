// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package singleflight implementa coalescing de requests idénticos
// concurrentes (EPIC-003 eficiencia, 010-001 P0).
//
// A diferencia de golang.org/x/sync/singleflight, el resultado compartido
// es un blob opaco (status + body + metadata) listo para reescribirse a
// N clientes, y el grupo tiene un tope de vuelos simultáneos (anti-DoS).
//
// Semántica: SOLO dedupe de requests CONCURRENTES. Al completar el vuelo
// se elimina del mapa — un request idéntico que llega después arranca un
// vuelo nuevo (el cache persistente exact-match, si existe, es otra capa).
package singleflight

import (
	"fmt"
	"sync"
)

// Result es la respuesta compartida de un vuelo. Body es opaco para este
// paquete: el llamador decide cómo serializarlo (JSON o SSE).
type Result struct {
	StatusCode  int
	ContentType string
	Body        []byte
	ProviderID  string
	Usage       Usage
}

// Usage replica el mínimo de usage necesario para que un follower pueda
// exponer headers X-Usage-* sin depender de los tipos del provider
// (evita ciclos de import: proxy importa singleflight, no al revés).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int
}

type flight struct {
	done   chan struct{}
	result *Result
	err    error
}

// Group coalesce llamadas con la misma key mientras están en vuelo.
// Las llamadas que llegan con la key ya en vuelo esperan el mismo
// resultado (shared=true). El líder ejecuta fn fuera del lock; un
// panic del líder se convierte en error para todos (ningún follower
// queda colgado).
type Group struct {
	mu      sync.Mutex
	flights map[string]*flight
	max     int // tope de vuelos simultáneos; 0 = default 512
}

// New construye un Group. maxFlights <= 0 → 512.
func New(maxFlights int) *Group {
	if maxFlights <= 0 {
		maxFlights = 512
	}
	return &Group{flights: make(map[string]*flight), max: maxFlights}
}

// Do ejecuta fn una sola vez por key concurrente y devuelve el resultado
// compartido. shared=true indica que el resultado fue producido por OTRO
// llamador (este llamador esperó y reescribe el mismo blob). Si el grupo
// está lleno, fn se ejecuta sin coalescing (shared=false).
func (g *Group) Do(key string, fn func() *Result) (*Result, bool) {
	g.mu.Lock()
	if f, ok := g.flights[key]; ok {
		g.mu.Unlock()
		<-f.done
		if f.err != nil {
			return nil, true
		}
		return f.result, true
	}
	if len(g.flights) >= g.max {
		g.mu.Unlock()
		return fn(), false
	}
	f := &flight{done: make(chan struct{})}
	g.flights[key] = f
	g.mu.Unlock()

	// Líder: ejecuta fuera del lock. Un panic no debe colgar followers.
	func() {
		defer func() {
			if p := recover(); p != nil {
				f.err = fmt.Errorf("singleflight panic: %v", p)
			}
		}()
		f.result = fn()
	}()
	close(f.done)

	g.mu.Lock()
	delete(g.flights, key)
	g.mu.Unlock()

	if f.err != nil {
		return nil, false
	}
	return f.result, false
}
