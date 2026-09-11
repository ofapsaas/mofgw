// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelsdev

// Facade del motor genérico (019-002 D1/D8, refactor mecánico de 001): el
// mecanismo de fetch/retry/clasificación/knob vive en internal/modelscache;
// este archivo solo compone el Spec de la fuente models.dev y expone la API
// pública de 019-001 vía aliases de tipo (P2: identidad preservada —
// errors.As con *FetchError y errors.Is con ErrFetchDisabled/ErrLockBusy
// siguen funcionando; P3: composición semánticamente equivalente).

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/ofapsaas/mofgw/internal/modelscache"
)

const (
	// FetchTimeout es el tope de CADA intento HTTP (D9 de 001); const del
	// motor, expuesta como alias (P2).
	FetchTimeout = modelscache.FetchTimeout

	// KnobDisableModelsFetch es la env var que deshabilita el fetch (D5 de 001).
	KnobDisableModelsFetch = "MOFGW_DISABLE_MODELS_FETCH"

	// defaultBaseURL es el upstream real de models.dev (D1 de 001).
	defaultBaseURL = "https://models.dev/api.json"
)

// ErrFetchDisabled / ErrLockBusy: los del motor, misma identidad (D8 de
// 002 — alias de la misma var, errors.Is simétrico).
var (
	ErrFetchDisabled = modelscache.ErrFetchDisabled
	ErrLockBusy      = modelscache.ErrLockBusy
)

// FetchError es el error clasificado del motor (P2: alias — identidad
// preservada para errors.As).
type FetchError = modelscache.FetchError

// Option es la opción del motor (alias — WithBaseURL/WithClient/WithLogger
// devuelven el mismo tipo).
type Option = modelscache.Option

// WithBaseURL apunta Fetch a otro upstream (hook de test, D3 de 001).
// Precedencia options > Spec.BaseURL (D8 de 002).
func WithBaseURL(url string) Option { return modelscache.WithBaseURL(url) }

// WithClient inyecta el http.Client (hook de test, D3 de 001). nil → default.
func WithClient(hc *http.Client) Option { return modelscache.WithClient(hc) }

// WithLogger inyecta el logger operativo (D3/D7 de 001). nil → slog.Default().
func WithLogger(l *slog.Logger) Option { return modelscache.WithLogger(l) }

// specModelsDev compone el Spec de la fuente models.dev sobre el motor
// (D8/P3): BaseURL default (overridable por WithBaseURL del llamador),
// parseo tolerante, knob propio, sin auth (I7: GET anónimo) y sin Source
// (los logs quedan byte-idénticos a los de 001, P17/D9).
func specModelsDev() modelscache.Spec {
	return modelscache.Spec{
		BaseURL: defaultBaseURL,
		Parse:   func(raw []byte) (any, error) { return ParseCatalog(raw) },
		AuthEnv: "",
		KnobEnv: KnobDisableModelsFetch,
		Source:  "",
	}
}

// Fetch hace GET al upstream y devuelve el catálogo parseado + el body
// crudo byte-idéntico al servido (P3 de 001). Ante fallo transitorio
// reintenta una vez con backoff ~500ms (D9); 4xx no reintenta (P4). Con
// el knob MOFGW_DISABLE_MODELS_FETCH=1 devuelve un error envolviendo
// ErrFetchDisabled sin ningún request, leyendo la env en CADA llamada
// (D5/R4). Delega en el motor genérico.
func Fetch(ctx context.Context, opts ...Option) (*Catalog, []byte, error) {
	return modelscache.Fetch[*Catalog](ctx, specModelsDev(), opts...)
}
