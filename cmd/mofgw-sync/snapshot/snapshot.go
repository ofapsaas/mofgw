// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package snapshot provee el catálogo models.dev embebido en el binario
// mofgw-sync (019-007-build-snapshot, fallback offline, patrón opencode
// models-snapshot).
//
// Contenido: api.json (body crudo de GET https://models.dev/api.json, SIN
// filtrar — fidelidad manda, D2) + meta.json ({fetched_at, sha256,
// source_url}). Única fuente del fallback; regeneración MANUAL con
// scripts/fetch-snapshot.sh (JAMÁS en build — I2 hermeticidad).
//
// Ubicación lockeada en cmd/mofgw-sync/snapshot/ (decisión HITL 6): SOLO
// crece el binario sync; internal/modelsdev y internal/modelscache (server
// + tests) quedan livianas.
package snapshot

import (
	_ "embed"
	"encoding/json"
	"time"
)

//go:embed api.json
var apiJSON []byte

//go:embed meta.json
var metaJSON []byte

// Meta: trazabilidad del snapshot (P3).
type Meta struct {
	FetchedAt time.Time
	SHA256    string
	SourceURL string
}

// metaFile: shape del meta.json en disco.
type metaFile struct {
	FetchedAt string `json:"fetched_at"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"source_url"`
}

// Embedded devuelve el snapshot embebido. ok=false cuando api.json está
// vacío o meta.json falta/corrompe (P4): el llamante se comporta como si no
// hubiera snapshot (cero fallback, fail-loud sin cache intacto).
func Embedded() (raw []byte, meta Meta, ok bool) {
	if len(apiJSON) == 0 {
		return nil, Meta{}, false
	}
	m, err := parseMeta(metaJSON)
	if err != nil {
		return nil, Meta{}, false
	}
	return apiJSON, m, true
}

// parseMeta parsea y valida meta.json: fetched_at RFC3339 + sha256 presente.
func parseMeta(raw []byte) (Meta, error) {
	var m metaFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, err
	}
	ts, err := time.Parse(time.RFC3339, m.FetchedAt)
	if err != nil {
		return Meta{}, err
	}
	if m.SHA256 == "" {
		return Meta{}, errEmptySHA
	}
	return Meta{FetchedAt: ts, SHA256: m.SHA256, SourceURL: m.SourceURL}, nil
}

var errEmptySHA = errSHA{}

type errSHA struct{}

func (errSHA) Error() string { return "snapshot: meta.json sin sha256" }
