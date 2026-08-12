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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
	"github.com/ofapsaas/mofgw/internal/subprocess/claude"
)

// BuildProvider construye el provider correcto según pc.Type (P5): HTTP
// (pc.Type == "" o "http") → provider.NewClient; subprocess → resuelve el
// backend y el session dir y construye subprocess.NewProvider con el
// adapter claude. Un type no reconocido falla con error descriptivo.
func BuildProvider(pc config.ProviderConfig, logger *slog.Logger) (provider.Provider, error) {
	switch pc.Type {
	case "", "http":
		// P4 backward compat: byte-idéntico a la construcción HTTP actual.
		return provider.NewClient(pc.ID, pc.BaseURL, pc.APIKey, pc.Models, pc.MaxTokens, nil), nil
	case "subprocess":
		backend, err := BuildBackend(pc.Backend, pc.Command)
		if err != nil {
			return nil, err
		}
		sessionDir, err := ResolveSessionDir(pc.SessionDir)
		if err != nil {
			return nil, err
		}
		return subprocess.NewProvider(pc.ID, pc.Models, pc.MaxTokens, sessionDir, pc.BackendFlags, backend, logger), nil
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
}

// BuildBackend construye el backend del motor por nombre (P5): switch sobre
// `backend` ("claude" hoy; gemini/codex a futuro, motor intacto). Un
// backend desconocido devuelve error descriptivo (error defensivo: la
// validación de carga ya lo rechaza — C4/P3).
func BuildBackend(backend, command string) (subprocess.Backend, error) {
	switch backend {
	case "claude":
		return claude.New(command), nil
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
}

// ResolveSessionDir expande `~` (vía os.UserHomeDir) y aplica el default
// config.DefaultSessionDir cuando dir está vacío (P1).
func ResolveSessionDir(dir string) (string, error) {
	if dir == "" {
		dir = config.DefaultSessionDir
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve session dir: %w", err)
		}
		dir = filepath.Join(home, dir[2:])
	}
	return dir, nil
}
