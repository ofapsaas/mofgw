// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package claude implementa el adapter `claude` de la interfaz
// subprocess.Backend (feature 013-002): arma el argv del CLI de claude,
// traduce el body chat-completions a un prompt único limpio, parsea el
// stream-json de claude (no-stream y stream) a los tipos OpenAI de
// provider, y detecta la negativa de policy en stderr.
//
// RED: este archivo es un esqueleto SOLO de compilación. Cada método
// devuelve el cero/error correspondiente para que los tests compilen y
// fallen por ASSERTION (no por error de compilación/import). GREEN
// reemplaza los stubs por la implementación real.
package claude

import (
	"errors"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// Backend implementa subprocess.Backend (P10) para el CLI de claude.
type Backend struct {
	bin string // ruta del binario (config `command`; default "claude")
}

// New construye el adapter con la ruta del binario. RED stub: devuelve el
// struct vacío (los tests fallan por los métodos, no por New).
func New(bin string) *Backend {
	return &Backend{bin: bin}
}

// Name identifica el backend (P1). RED stub: devuelve "" (esperado "claude").
func (b *Backend) Name() string { return "" }

// Args arma el argv del CLI para un request (P2). RED stub: devuelve nil
// (esperado [bin, "-p", "--session-id", s.ID, "--model", model,
// "--output-format", "stream-json", ...flags]).
func (b *Backend) Args(s *subprocess.Session, model string, flags []string) []string {
	return nil
}

// TranslateReq traduce el body chat-completions a UN prompt único (P4/P5).
// RED stub: siempre error (esperado el último mensaje user limpio sin tools).
func (b *Backend) TranslateReq(body []byte) (string, error) {
	return "", errors.New("not implemented")
}

// TranslateOut traduce el stdout agregado (stream-json) a ChatResponse (P6).
// RED stub: siempre error (esperado choices[0].message.content del texto
// concatenado + finish_reason mapeado).
func (b *Backend) TranslateOut(raw []byte, model string) (*provider.ChatResponse, error) {
	return nil, errors.New("not implemented")
}

// TranslateStreamOut traduce líneas del stdout (streaming) a eventos (P7/P8).
// RED stub: drena la entrada pero NO emite eventos (esperado un StreamEvent
// por cada text_delta, sin control events, sin usage ni [DONE]).
func (b *Backend) TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string) {
	for range lines {
	}
}

// IsRefusal reporta si el stderr del CLI señala una negativa de policy (P9).
// RED stub: devuelve false (esperado true ante markers de refusal).
func (b *Backend) IsRefusal(stderr string) bool {
	return false
}

// Assert de compilación (P10): Backend implementa subprocess.Backend.
var _ subprocess.Backend = (*Backend)(nil)
