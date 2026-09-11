// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package upstream

// Fuente OpenRouter (spec 019-002, D2/D4): catálogo completo con pricing
// en STRINGS (→ float64 tolerante, P7), la modality dentro de
// architecture (NO top-level, D2), aliases "~" tal cual (I2) y auth
// OPCIONAL (D4/P10): solo si OPENROUTER_API_KEY está seteada y no vacía
// el request lleva "Authorization: Bearer"; la env se lee POR LLAMADA y
// su valor jamás entra en logs ni errores (P11/I12).

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ofapsaas/mofgw/internal/modelscache"
)

const (
	// OpenRouterURL es el endpoint del catálogo de OpenRouter (D3).
	OpenRouterURL = "https://openrouter.ai/api/v1/models"

	// authOpenRouterEnv es la env var de la API key de OpenRouter (D4:
	// mismo nombre que usa el skill). Opcional: el catálogo anónimo está
	// verificado completo (733 KB, HTTP 200).
	authOpenRouterEnv = "OPENROUTER_API_KEY"
)

// OpenRouterCatalog es el catálogo tipado de OpenRouter (D2).
type OpenRouterCatalog struct {
	Models []OpenRouterModel
}

// OpenRouterModel es un modelo del catálogo, con value semantics (P14 de
// 001): campo ausente o null → zero-value, campo desconocido → ignorado
// sin error (P9/I9). Los IDs van TAL CUAL upstream, incluidos los aliases
// "~" (I2: sin resolución ni filtrado — eso es 019-003).
type OpenRouterModel struct {
	ID                  string
	CanonicalSlug       string
	Name                string
	ContextLength       int
	Pricing             OpenRouterPricing
	SupportedParameters []string
	Architecture        OpenRouterArchitecture
	TopProvider         OpenRouterTopProvider
}

// OpenRouterPricing es el precio por token (P7): upstream sirve strings;
// la conversión string→float64 es tolerante (inválido/vacío/ausente → 0
// sin error). Mapeo 1:1 con input_cache_read/input_cache_write.
type OpenRouterPricing struct {
	Prompt     float64
	Completion float64
	CacheRead  float64
	CacheWrite float64
}

// OpenRouterArchitecture es la arquitectura del modelo (D2: la modality
// NO es top-level, vive en architecture).
type OpenRouterArchitecture struct {
	Modality         string
	InputModalities  []string
	OutputModalities []string
}

// OpenRouterTopProvider son los límites que impone el provider (D2).
type OpenRouterTopProvider struct {
	ContextLength       int
	MaxCompletionTokens int
}

// rawOpenRouterCatalog / rawOpenRouterModel y compañía son los structs
// internos de parseo: pointers para distinguir presencia/null, json tags
// tolerantes (desconocidos → ignorados por default de encoding/json). Lo
// que se expone son los structs de arriba (values, P15 de 001). NO se
// tipan: benchmarks, per_request_limits, pricing.overrides,
// default_parameters, hugging_face_id, knowledge_cutoff, expiration_date,
// links (schema variable; 019-003 puede re-parsear del raw — R4).
type rawOpenRouterCatalog struct {
	Models []rawOpenRouterModel `json:"data"`
}

type rawOpenRouterModel struct {
	ID                  string                     `json:"id"`
	CanonicalSlug       string                     `json:"canonical_slug"`
	Name                string                     `json:"name"`
	ContextLength       int                        `json:"context_length"`
	Pricing             *rawOpenRouterPricing      `json:"pricing"`
	SupportedParameters []string                   `json:"supported_parameters"`
	Architecture        *rawOpenRouterArchitecture `json:"architecture"`
	TopProvider         *rawOpenRouterTopProvider  `json:"top_provider"`
}

type rawOpenRouterPricing struct {
	Prompt          *string `json:"prompt"`
	Completion      *string `json:"completion"`
	InputCacheRead  *string `json:"input_cache_read"`
	InputCacheWrite *string `json:"input_cache_write"`
}

type rawOpenRouterArchitecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type rawOpenRouterTopProvider struct {
	ContextLength       *int `json:"context_length"`
	MaxCompletionTokens *int `json:"max_completion_tokens"`
}

// ParseOpenRouterCatalog parsea el payload crudo de OpenRouter (P7-P9):
// pricing strings → float64 (inválido/vacío/null/ausente → 0 sin error),
// campos ausentes o null → zero-values sin error, schema variable
// ignorado sin error. Solo JSON inválido → error.
func ParseOpenRouterCatalog(raw []byte) (OpenRouterCatalog, error) {
	var rc rawOpenRouterCatalog
	if err := json.Unmarshal(raw, &rc); err != nil {
		return OpenRouterCatalog{}, fmt.Errorf("upstream: parse: %w", err)
	}
	cat := OpenRouterCatalog{Models: make([]OpenRouterModel, 0, len(rc.Models))}
	for _, rm := range rc.Models {
		cat.Models = append(cat.Models, rm.toOpenRouterModel())
	}
	return cat, nil
}

// toOpenRouterModel convierte el parseo interno (pointers) al struct
// expuesto (values, P15 de 001).
func (r rawOpenRouterModel) toOpenRouterModel() OpenRouterModel {
	m := OpenRouterModel{
		ID:                  r.ID,
		CanonicalSlug:       r.CanonicalSlug,
		Name:                r.Name,
		ContextLength:       r.ContextLength,
		SupportedParameters: r.SupportedParameters,
	}
	if r.Pricing != nil {
		m.Pricing = OpenRouterPricing{
			Prompt:     parsePricing(r.Pricing.Prompt),
			Completion: parsePricing(r.Pricing.Completion),
			CacheRead:  parsePricing(r.Pricing.InputCacheRead),
			CacheWrite: parsePricing(r.Pricing.InputCacheWrite),
		}
	}
	if r.Architecture != nil {
		m.Architecture = OpenRouterArchitecture{
			Modality:         r.Architecture.Modality,
			InputModalities:  r.Architecture.InputModalities,
			OutputModalities: r.Architecture.OutputModalities,
		}
	}
	if r.TopProvider != nil {
		m.TopProvider = OpenRouterTopProvider{
			ContextLength:       derefInt(r.TopProvider.ContextLength),
			MaxCompletionTokens: derefInt(r.TopProvider.MaxCompletionTokens),
		}
	}
	return m
}

// derefInt devuelve el valor del pointer o 0 si es nil (P9: null/ausente
// → zero-value sin error).
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// parsePricing convierte el precio string de OpenRouter a float64 (P7):
// nil o inválido → 0 SIN error (tolerancia I9/R4).
func parsePricing(s *string) float64 {
	if s == nil {
		return 0
	}
	v, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0
	}
	return v
}

// specOpenRouter compone el Spec de la fuente openrouter sobre el motor.
func specOpenRouter() modelscache.Spec {
	return modelscache.Spec{
		BaseURL: OpenRouterURL,
		Parse:   func(raw []byte) (any, error) { return ParseOpenRouterCatalog(raw) },
		AuthEnv: authOpenRouterEnv, // D4: Bearer condicional (seteada y no vacía)
		KnobEnv: KnobDisableUpstreamFetch,
		Source:  "openrouter",
	}
}

// DefaultOpenRouterCachePath devuelve el path default del cache de
// openrouter (D5): <base>/openrouter-models.json bajo la regla de base
// de 001.
func DefaultOpenRouterCachePath() string {
	return modelscache.DefaultCachePath("openrouter-models.json")
}

// FetchOpenRouter fetchea el catálogo de OpenRouter (P8/P10): catálogo
// tipado fiel (aliases ~ incluidos) + raw byte-idéntico + auth
// condicional leída por llamada.
func FetchOpenRouter(ctx context.Context, opts ...modelscache.Option) (OpenRouterCatalog, []byte, error) {
	return modelscache.Fetch[OpenRouterCatalog](ctx, specOpenRouter(), opts...)
}

// NewOpenRouterStore construye el Store de openrouter. path vacío →
// DefaultOpenRouterCachePath(); ttl <= 0 → modelscache.DefaultCacheTTL (D8).
func NewOpenRouterStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[OpenRouterCatalog] {
	if path == "" {
		path = DefaultOpenRouterCachePath()
	}
	return modelscache.NewStore[OpenRouterCatalog](specOpenRouter(), path, ttl, lock, opts...)
}
