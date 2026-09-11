// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package upstream fetchea y cachea las listas de acceso de opencode
// (Zen y Go) y el catálogo público de OpenRouter para la epic
// 019-provider-sync-automation (spec 019-002). Solo parsea y persiste: no
// filtra IDs, no resuelve aliases "~", no normaliza precios ni
// vocabularios (I2 — eso es 019-003).
//
// Todas las fuentes corren sobre el motor genérico internal/modelscache
// (D8): cada archivo compone su Spec (URL, parser tolerante, knob) y
// expone Fetch + Store + DefaultCachePath de su fuente. Los errores son
// aliases de los del motor: misma identidad, errors.Is simétrico.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ofapsaas/mofgw/internal/modelscache"
)

const (
	// ZenURL es el endpoint de la lista de modelos de opencode Zen (D3).
	ZenURL = "https://opencode.ai/zen/v1/models"

	// KnobDisableUpstreamFetch es la env var que deshabilita el fetch de
	// TODAS las fuentes de este paquete (D6). Cada Spec lleva su KnobEnv:
	// aislado del knob de modelsdev (MOFGW_DISABLE_MODELS_FETCH) en ambas
	// direcciones (I13).
	KnobDisableUpstreamFetch = "MOFGW_DISABLE_UPSTREAM_FETCH"
)

// Errores compartidos: los del motor, misma identidad (D8 — alias de la
// misma var, no wrappers).
var (
	ErrFetchDisabled = modelscache.ErrFetchDisabled
	ErrLockBusy      = modelscache.ErrLockBusy
)

// ModelList es la lista OpenAI-shape de Zen/Go (D2): {object, data[]},
// items {id, object:"model", created(unix), owned_by}.
type ModelList struct {
	Object string     `json:"object"`
	Models []ModelRef `json:"data"`
}

// ModelRef es un item de la lista (value semantics, P14 de 001): campo
// ausente → zero-value, campo desconocido → ignorado sin error (P6/I9).
type ModelRef struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	Created int64  `json:"created"`
}

// ParseModelList parsea el payload crudo de Zen/Go (P6): data ausente o
// null → Models nil sin error; campos extra desconocidos → ignorados sin
// error; object top-level ausente → zero-value. Solo JSON inválido → error.
func ParseModelList(raw []byte) (ModelList, error) {
	var list ModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return ModelList{}, fmt.Errorf("upstream: parse: %w", err)
	}
	return list, nil
}

// specZen compone el Spec de la fuente zen sobre el motor (D8).
func specZen() modelscache.Spec {
	return modelscache.Spec{
		BaseURL: ZenURL,
		Parse:   func(raw []byte) (any, error) { return ParseModelList(raw) },
		AuthEnv: "", // D4: Zen anónimo (200 verificado sin auth)
		KnobEnv: KnobDisableUpstreamFetch,
		Source:  "zen",
	}
}

// DefaultZenCachePath devuelve el path default del cache de zen (D5):
// <base>/zen-models.json bajo la regla de base de 001 (MOFGW_CACHE_DIR >
// os.UserCacheDir()/mofgw > os.TempDir()/mofgw).
func DefaultZenCachePath() string {
	return modelscache.DefaultCachePath("zen-models.json")
}

// FetchZen fetchea la lista de modelos de opencode Zen (P4): ModelList
// fiel por índice + raw byte-idéntico al servido + User-Agent propio.
func FetchZen(ctx context.Context, opts ...modelscache.Option) (ModelList, []byte, error) {
	return modelscache.Fetch[ModelList](ctx, specZen(), opts...)
}

// NewZenStore construye el Store de zen. path vacío → DefaultZenCachePath();
// ttl <= 0 → modelscache.DefaultCacheTTL (D8).
func NewZenStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[ModelList] {
	if path == "" {
		path = DefaultZenCachePath()
	}
	return modelscache.NewStore[ModelList](specZen(), path, ttl, lock, opts...)
}
