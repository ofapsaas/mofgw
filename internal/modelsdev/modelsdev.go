// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package modelsdev fetchea y cachea el catálogo público de models.dev
// (https://models.dev/api.json) para la epic 019-provider-sync-automation
// (spec 019-001). Solo parsea y persiste: no filtra IDs, no mapea a
// providers de mofgw, no normaliza precios (I2 — eso es 019-003).
//
// El parseo es tolerante por construcción (P14/I9): el schema upstream es
// variable por modelo (cost.tiers, cost.input_audio, limit.input solo
// aparecen en algunos), así que el parseo interno usa pointers y lo que se
// expone son structs con value semantics: campo ausente → zero-value,
// campo desconocido → ignorado sin error.
package modelsdev

import (
	"encoding/json"
	"fmt"
)

// Modalities son las modalidades soportadas de un modelo (P15).
type Modalities struct {
	Input  []string
	Output []string
}

// Limit son los límites de contexto de un modelo (P15). limit.input puede
// estar ausente upstream (D1) → zero-value.
type Limit struct {
	Context int
	Output  int
	Input   int
}

// Cost es el precio del modelo (P15). cost.cache_write puede estar ausente
// upstream (D1) → zero-value.
type Cost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Model es un modelo del catálogo, con value semantics (P14): campo
// ausente → zero-value, nunca pointer nil.
type Model struct {
	Name       string
	Attachment bool
	Reasoning  bool
	ToolCall   bool
	Modalities Modalities
	Limit      Limit
	Cost       Cost
}

// Provider es un provider del catálogo; Models está keyed por id del
// modelo (P15). Un provider sin models upstream es válido (mapa vacío).
type Provider struct {
	Name   string
	API    string
	NPM    string
	Models map[string]Model
}

// Catalog es el catálogo tipado de models.dev; Providers está keyed por id
// del provider (P15).
type Catalog struct {
	Providers map[string]Provider
}

// rawCatalog y compañía son los structs internos de parseo: pointers para
// distinguir presencia, json tags tolerantes (desconocidos → ignorados por
// default de encoding/json). Lo que se expone son los structs de arriba.
type rawCatalog map[string]rawProvider

type rawProvider struct {
	Name   string              `json:"name"`
	API    string              `json:"api"`
	NPM    string              `json:"npm"`
	Models map[string]rawModel `json:"models"`
}

type rawModel struct {
	Name       string         `json:"name"`
	Attachment *bool          `json:"attachment"`
	Reasoning  *bool          `json:"reasoning"`
	ToolCall   *bool          `json:"tool_call"`
	Modalities *rawModalities `json:"modalities"`
	Limit      *rawLimit      `json:"limit"`
	Cost       *rawCost       `json:"cost"`
}

type rawModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type rawLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
	Input   int `json:"input"`
}

type rawCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// ParseCatalog parsea el payload crudo de models.dev en el catálogo tipado
// (P14/P15): key del mapa = id del provider/modelo, campos ausentes →
// zero-value, desconocidos → ignorados sin error.
func ParseCatalog(raw []byte) (*Catalog, error) {
	var rc rawCatalog
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, fmt.Errorf("modelsdev: parse: %w", err)
	}
	if rc == nil { // JSON null no produce error pero tampoco catálogo
		return nil, fmt.Errorf("modelsdev: parse: catálogo vacío")
	}
	cat := &Catalog{Providers: make(map[string]Provider, len(rc))}
	for id, rp := range rc {
		p := Provider{
			Name:   rp.Name,
			API:    rp.API,
			NPM:    rp.NPM,
			Models: make(map[string]Model, len(rp.Models)),
		}
		for modelID, rm := range rp.Models {
			p.Models[modelID] = rm.toModel()
		}
		cat.Providers[id] = p
	}
	return cat, nil
}

// toModel convierte el parseo interno (pointers) al struct expuesto
// (values, P15).
func (r rawModel) toModel() Model {
	m := Model{
		Name:       r.Name,
		Attachment: deref(r.Attachment),
		Reasoning:  deref(r.Reasoning),
		ToolCall:   deref(r.ToolCall),
	}
	if r.Modalities != nil {
		m.Modalities = Modalities{Input: r.Modalities.Input, Output: r.Modalities.Output}
	}
	if r.Limit != nil {
		m.Limit = Limit{Context: r.Limit.Context, Output: r.Limit.Output, Input: r.Limit.Input}
	}
	if r.Cost != nil {
		m.Cost = Cost{Input: r.Cost.Input, Output: r.Cost.Output, CacheRead: r.Cost.CacheRead, CacheWrite: r.Cost.CacheWrite}
	}
	return m
}

func deref(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
