// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package providerfactory construye providers y backends según el config
// (feature 013-003-config-wiring, decisión D2 del owner). Es importable y
// testeable con un `command` inyectable (D6: stub CLI, hermético). El
// adapter fino `cmd/mofgw/main.go` lo llama para construir cada provider.
//
// RED skeleton: las tres funciones devuelven un error "not implemented"
// para que los tests de 013-003 compilen y fallen por ASSERTION (no por
// ImportError). La lógica real (switch por pc.Type / backend) la agrega el
// implementer en GREEN; el import del adapter `claude` se incorpora ahí.
package providerfactory

import (
	"errors"
	"log/slog"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// BuildProvider construye el provider correcto según pc.Type (P5): HTTP
// (pc.Type == "" o "http") → provider.NewClient; subprocess →
// subprocess.NewProvider(..., claude backend, ...). RED stub: error.
func BuildProvider(pc config.ProviderConfig, logger *slog.Logger) (provider.Provider, error) {
	return nil, errors.New("not implemented")
}

// BuildBackend construye el backend del motor por nombre (P5): switch sobre
// `backend` ("claude" hoy; gemini/codex a futuro, motor intacto). RED stub.
func BuildBackend(backend, command string) (subprocess.Backend, error) {
	return nil, errors.New("not implemented")
}

// ResolveSessionDir expande `~` (vía os.UserHomeDir) y aplica el default
// config.DefaultSessionDir cuando dir está vacío (P1). RED stub.
func ResolveSessionDir(dir string) (string, error) {
	return "", errors.New("not implemented")
}
