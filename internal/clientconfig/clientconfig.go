// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package clientconfig define el core del endpoint /v1/client-config
// (feature 016-001-config-renderer-core): la representación intermedia (IR)
// tipada que transporta la metadata del catálogo real, la interfaz Renderer
// y el registry público de renderers por clientID.
//
// En esta feature el registry está VACÍO en producción (los adapters
// opencode/openclaw/zot son 016-002/003/004), pero registrar/consultar
// renderers y transportar la metadata del catálogo ES la API pública del
// paquete (spec P10). El registry opera sobre el mapa exportado `Renderers`:
// registrar y consultar no es mock sobre plumbing, es el contrato del paquete.
//
// Invariante I1: el IR transporta `KeyEnvRef` (referencia a una variable de
// entorno), jamás el valor del secret. mofgw no resuelve ni emite el valor.
package clientconfig

import "sort"

// ConfigIR es la representación intermedia que recibe todo Renderer.
type ConfigIR struct {
	ClientID  string       // id del cliente autenticado (auth.ClientIDFrom)
	BaseURL   string       // knob client_config.base_url — vivo en el IR
	KeyEnvRef string       // p.ej. "MOFGW_KEY" — NUNCA el valor del secret
	Models    []ModelEntry // metadata COMPLETA del catálogo, no formato mínimo
}

// ModelEntry transporta la metadata del catálogo (007-002/010-001),
// reusando modelCatalogEntry (misma fuente de verdad en memoria).
type ModelEntry struct {
	ID   string
	Meta map[string]any // objeto /v1/models enriquecido (id, object, created,
	// owned_by, context_length, capabilities, thinking,
	// supported_parameters, modality, architecture, pricing…)
}

// Renderer serializa el IR al formato del cliente.
type Renderer interface {
	Render(ir ConfigIR) ([]byte, error)
}

// Renderers es el registry mutable de renderers por clientID.
// Inspectable/registrable por tests a través de la API pública (P10).
var Renderers = map[string]Renderer{}

// Register asocia un renderer a un clientID.
func Register(clientID string, r Renderer) {
	Renderers[clientID] = r
}

// SupportedList devuelve los clientIDs registrados (para el 404),
// ordenados para una respuesta estable.
func SupportedList() []string {
	ids := make([]string, 0, len(Renderers))
	for id := range Renderers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Lookup devuelve el renderer de un clientID, o nil.
func Lookup(clientID string) Renderer {
	return Renderers[clientID]
}