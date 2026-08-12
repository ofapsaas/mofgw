// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package subprocess implementa el motor genérico para providers que
// ejecutan el CLI de un backend de IA como subproceso (feature 013-001).
//
// Este archivo es el SKELETON de superficie de contrato (RED): declara el
// tipo Session, el helper de key de sesión, la interfaz Backend y el tipo
// Provider implementando provider.Provider con stubs que devuelven valores
// vacíos. La lógica real la rellena el implementer en GREEN.
package subprocess

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// Session modela una sesión de cliente que vive en el proceso del CLI
// subprocess. ID se deriva determinísticamente del clientID (o
// clientID|model en modo client+model); Dir es la base de la sesión.
type Session struct {
	ID       string
	Dir      string
	ClientID string
}

// SessionKeyMode selecciona la derivación de la key de sesión.
type SessionKeyMode int

const (
	// SessionKeyClient deriva la key solo del clientID (default, D2).
	SessionKeyClient SessionKeyMode = iota
	// SessionKeyClientModel deriva la key de clientID|model (override).
	SessionKeyClientModel
)

// NewSessionKey deriva la key de sesión según el modo. RED stub: devuelve
// ""; la lógica de hash (P1/P2) la implementa GREEN.
func NewSessionKey(clientID, model string, mode SessionKeyMode) string {
	return ""
}

// Backend es la frontera de traducción del motor (D1): posee argv,
// traducción de formato y parseo. El motor NO conoce strings
// backend-específicos.
type Backend interface {
	// Name identifica el backend (observabilidad).
	Name() string
	// Args arma el argv del CLI para un request (cwd = Session.Dir).
	// El modelo se pasa per-request; NO participa de la key de sesión.
	Args(s *Session, model string, flags []string) []string
	// TranslateReq traduce el body chat-completions a UN prompt único de
	// texto natural limpio (D3/D4).
	TranslateReq(body []byte) (string, error)
	// TranslateOut traduce stdout (modo no-stream) a ChatResponse OpenAI.
	TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
	// TranslateStreamOut traduce líneas del stdout (streaming) a eventos.
	TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string)
}

// Provider implementa provider.Provider (I1) ejecutando un CLI como
// subproceso. RED stub: campos internos a rellenar en GREEN.
type Provider struct {
	// RED stub: campos internos del motor a rellenar en GREEN.
}

// NewProvider construye el provider subprocess. RED stub: devuelve un
// *Provider vacío; la lógica de sesión/exec/serialización la implementa
// GREEN.
func NewProvider(id string, models []string, maxTokens int64, sessionDir string, backendFlags []string, backend Backend, logger *slog.Logger) *Provider {
	return &Provider{}
}

// var _ garantiza en tiempo de compilación que Provider cumple I1.
var _ provider.Provider = (*Provider)(nil)

// ID devuelve el id del provider. RED stub: "" (lógica en GREEN).
func (p *Provider) ID() string { return "" }

// MaxTokens devuelve el límite de tokens. RED stub: 0 (lógica en GREEN).
func (p *Provider) MaxTokens() int { return 0 }

// Serves reporta si el provider sirve el modelo. RED stub: false siempre
// (lógica en GREEN, P3).
func (p *Provider) Serves(model string) bool { return false }

// Complete ejecuta un chat no-stream. RED stub: error "not implemented";
// la lógica de sesión/prompt/spawn la implementa GREEN.
func (p *Provider) Complete(ctx context.Context, body []byte) (*provider.CompleteResult, error) {
	return nil, errors.New("not implemented")
}

// Stream ejecuta un chat streaming. RED stub: error "not implemented";
// la lógica la implementa GREEN.
func (p *Provider) Stream(ctx context.Context, body []byte) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("not implemented")
}
