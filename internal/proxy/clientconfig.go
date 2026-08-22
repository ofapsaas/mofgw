// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Endpoint GET /v1/client-config (feature 016-001-config-renderer-core).
//
// Devuelve el fragmento de configuración del provider mofgw listo para
// insertar en un cliente soportado (opencode.json, openclaw.conf, zot…),
// serializado por el Renderer registrado para ese clientID. Esta feature
// entrega el CORE: el registry de renderers está VACÍO en producción (los
// adapters opencode/openclaw/zot son 016-002/003/004), así que con el
// default (base_url vacío o cliente no registrado) el endpoint responde
// 503/404 OpenAI-compatible.
package proxy

import (
	"net/http"
	"strings"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/clientconfig"
)

// SetClientConfig configura los knobs del endpoint /v1/client-config
// (016-001 P7). Debe llamarse una sola vez antes del tráfico (patrón
// SetContextAnalysis): baseURL vacío → 503 en runtime (I4), keyEnv es la
// REFERENCIA a la variable de entorno (I1/P8), nunca su valor.
func (s *Server) SetClientConfig(baseURL, keyEnv string) {
	s.clientConfigBaseURL = baseURL
	s.clientConfigKeyEnv = keyEnv
}

// handleClientConfig atiende GET /v1/client-config?client=<id>.
//
// Orden de precedencia (observable, definido por los tests RED P2/P4/P8/P9):
//  1. client ausente/vacío → 400 invalid_request_error.
//  2. base_url vacío → 503 server_error "client_config.base_url not set"
//     (sin fallback a otro base_url, I4).
//  3. cliente no registrado → 404 not_found_error con "supported: <lista>".
//  4. Renderer.Render(ir) error → 500 server_error.
//  5. ok → 200 con los bytes del renderer.
//
// La ruta queda autenticada por estar bajo /v1/ y fuera de publicPrefixes
// (auth.Wrap; sin Bearer válido → 401 invalid_api_key, emitido por Wrap).
func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. client ausente/vacío → 400.
	client := r.URL.Query().Get("client")
	if client == "" {
		openAIError(w, http.StatusBadRequest, "missing required query param: client", "invalid_request_error")
		return
	}

	// 2. base_url vacío → 503 (I4, sin fallback silencioso).
	if s.clientConfigBaseURL == "" {
		openAIError(w, http.StatusServiceUnavailable, "client_config.base_url not set", "server_error")
		return
	}

	// 3. cliente no registrado → 404 con la lista del registry (P3/P10).
	renderer := clientconfig.Lookup(client)
	if renderer == nil {
		supported := strings.Join(clientconfig.SupportedList(), ",")
		openAIError(w, http.StatusNotFound, "unsupported client \""+client+"\"; supported: "+supported, "not_found_error")
		return
	}

	// 4. Construcción del IR reusando la misma iteración/dedupe que
	// handleModels (I2/P6): mismo seen-map, mismo modelCatalogEntry.
	ir := clientconfig.ConfigIR{
		ClientID:  auth.ClientIDFrom(r.Context()),
		BaseURL:   s.clientConfigBaseURL,
		KeyEnvRef: s.clientConfigKeyEnv,
		Models:    s.configCatalogModels(),
	}

	out, err := renderer.Render(ir)
	if err != nil {
		openAIError(w, http.StatusInternalServerError, "client config render error", "server_error")
		return
	}

	// 5. 200.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// configCatalogModels devuelve la metadata de TODO el catálogo en memoria,
// con la MISMA iteración/dedupe que handleModels (I2/P6): mismo seen-map
// sobre s.providers (interface{ Models() []string }) y mismo
// modelCatalogEntry(model). Sin duplicación de la lógica de catálogo.
func (s *Server) configCatalogModels() []clientconfig.ModelEntry {
	seen := map[string]bool{}
	var entries []clientconfig.ModelEntry
	for _, p := range s.providers {
		if c, ok := p.(interface{ Models() []string }); ok {
			for _, m := range c.Models() {
				if !seen[m] {
					seen[m] = true
					entries = append(entries, clientconfig.ModelEntry{
						ID:   m,
						Meta: s.modelCatalogEntry(m),
					})
				}
			}
		}
	}
	return entries
}