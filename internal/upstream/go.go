// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package upstream

// Fuente opencode Go (spec 019-002, D3/D5): shape idéntico a Zen (D2,
// verificado — misma OpenAI-list) → reusa ModelList/ModelRef/ParseModelList
// de zen.go. Cache propio go-models.json (D5: lock/sidecar por fuente) y el
// mismo knob MOFGW_DISABLE_UPSTREAM_FETCH (D6). Sin auth (D4).

import (
	"context"
	"time"

	"github.com/ofapsaas/mofgw/internal/modelscache"
)

// GoURL es el endpoint de la lista de modelos de opencode Go (D3).
const GoURL = "https://opencode.ai/zen/go/v1/models"

// specGo compone el Spec de la fuente go sobre el motor (D8).
func specGo() modelscache.Spec {
	return modelscache.Spec{
		BaseURL: GoURL,
		Parse:   func(raw []byte) (any, error) { return ParseModelList(raw) },
		AuthEnv: "", // D4: Go anónimo (200 verificado sin auth)
		KnobEnv: KnobDisableUpstreamFetch,
		Source:  "go",
	}
}

// DefaultGoCachePath devuelve el path default del cache de go (D5):
// <base>/go-models.json bajo la regla de base de 001.
func DefaultGoCachePath() string {
	return modelscache.DefaultCachePath("go-models.json")
}

// FetchGo fetchea la lista de modelos de opencode Go (P5): fiel por
// índice, mismo shape que Zen (D2).
func FetchGo(ctx context.Context, opts ...modelscache.Option) (ModelList, []byte, error) {
	return modelscache.Fetch[ModelList](ctx, specGo(), opts...)
}

// NewGoStore construye el Store de go. path vacío → DefaultGoCachePath();
// ttl <= 0 → modelscache.DefaultCacheTTL (D8).
func NewGoStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[ModelList] {
	if path == "" {
		path = DefaultGoCachePath()
	}
	return modelscache.NewStore[ModelList](specGo(), path, ttl, lock, opts...)
}
