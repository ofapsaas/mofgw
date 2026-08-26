// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package clientregistry es el holder del snapshot inmutable del registro
// de clientes (017-001-auth-source-refactor, epic 017). Punto único de
// acceso para los consumidores (auth, keyed limiter, budget, embeddings) —
// se construye una vez al boot (P3-(a)); 017-002 (reloader) solo agregará
// poller + re-punteo atómico, sin volver a tocar main.go.
//
// No colisiona con internal/registry (accounting 014-001).
package clientregistry

import (
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
)

// Source lee el registro desde el archivo (boot fail-fast; 017-002 lo
// re-usa para fail-soft).
type Source struct {
	Path string
}

// Load lee y valida el archivo de registro de clientes reusando
// config.LoadClientsFile. Path vacío → sin clientes (válido, P2: 0
// clientes = configuración legítima sin registro).
func (s *Source) Load() ([]config.ClientConfig, error) {
	if s.Path == "" {
		return nil, nil
	}
	return config.LoadClientsFile(s.Path)
}

// Snapshot inmutable del registro de clientes. Los consumidores lo leen;
// nadie lo muta después de construirlo. 017-002 lo re-puntea atómicamente
// (atomic.Pointer) sin romper la garantía de no-mutación.
type Snapshot struct {
	Clients     []config.ClientConfig   // registro completo
	ClientsByID map[string]config.ClientConfig
	authByHash  map[string]string // key_sha256 lowercased -> id

	// authenticator: vista auth (auth.New) derivada de Clients, idéntica a
	// la que hoy construye main.go desde cfg.Clients (conserva auth.Client
	// como vista mínima, sin acoplar auth al registro completo).
	authenticator *auth.Authenticator
}

func newSnapshot(clients []config.ClientConfig) *Snapshot {
	byID := make(map[string]config.ClientConfig, len(clients))
	byHash := make(map[string]string, len(clients))
	authClients := make([]auth.Client, 0, len(clients))
	for _, c := range clients {
		byID[c.ID] = c
		byHash[strings.ToLower(c.KeySHA256)] = c.ID
		authClients = append(authClients, auth.Client{ID: c.ID, KeySHA256: c.KeySHA256})
	}
	return &Snapshot{
		Clients:       clients,
		ClientsByID:   byID,
		authByHash:    byHash,
		authenticator: auth.New(authClients),
	}
}

// Authenticate valida el Bearer del request contra la vista auth del
// snapshot (misma semántica que auth.Authenticator, comparación en tiempo
// constante). Devuelve el id del cliente autenticado o un error compatible
// con auth.ErrInvalidKey.
func (s *Snapshot) Authenticate(r *http.Request) (string, error) {
	return s.authenticator.Authenticate(r)
}

// Authenticator devuelve la vista auth (auth.New) derivada del snapshot,
// para que auth.Wrap siga recibiendo el *auth.Authenticator que espera.
func (s *Snapshot) Authenticator() *auth.Authenticator {
	return s.authenticator
}

// ClientByID devuelve el cliente por id (ok=false si no existe).
func (s *Snapshot) ClientByID(id string) (config.ClientConfig, bool) {
	c, ok := s.ClientsByID[id]
	return c, ok
}

// Registry es el holder del snapshot. 017-001: se construye una vez al
// boot. 017-002: agrega Reload()/poller que construye un Snapshot nuevo y
// lo publica atómicamente.
type Registry struct {
	current atomic.Pointer[Snapshot]
	logger  *slog.Logger
}

// New construye el Registry con la carga inicial del source (fail-fast si
// el archivo es inválido/inaccesible). Construye el Snapshot inmutable y lo
// publica.
func New(src *Source, logger *slog.Logger) (*Registry, error) {
	clients, err := src.Load()
	if err != nil {
		return nil, err
	}
	r := &Registry{logger: logger}
	r.current.Store(newSnapshot(clients))
	return r, nil
}

// Current devuelve el Snapshot publicado (inmutable; nunca se regenera ni
// muta por consumo).
func (r *Registry) Current() *Snapshot {
	return r.current.Load()
}