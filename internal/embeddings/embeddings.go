// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package embeddings implementa el cliente HTTP dedicado del endpoint
// POST /v1/embeddings (feature 011-006-embeddings): forward a Ollama.
// El `provider.Client` NO es reusable para embeddings (Complete/Stream
// hardcodean /chat/completions, I7) — este cliente hace
// `POST base_url+"/embeddings"` y devuelve el body OpenAI-compatible de
// respuesta. Nunca habla con api.openai.com (I2): solo con el Ollama
// configurado en `embeddings.base_url`.
//
// Interfaz tipada, sin reflexión (patrón websearch.Client de 011-005,
// ADR-005): el contrato observable es `Client.Embed(body) → raw`, y el
// concreto Ollama la satisface estructuralmente.
package embeddings

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ofapsaas/mofgw/internal/build"
)

// Client es el contrato del forward de embeddings (011-006): Embed envía el
// body crudo ya armado (model forzado + input + dimensions/encoding_format,
// P3/P5) a `base_url+"/embeddings"` y devuelve el body de respuesta crudo si
// el upstream responde 2xx. Un fallo de red, timeout o status no-2xx devuelve
// error (P9: 429 de Ollama no-reintentable → fallo upstream). Interfaz tipada
// (Make Illegal States Unrepresentable, Fail Loud).
type Client interface {
	Embed(ctx context.Context, body []byte) ([]byte, error)
}

// Ollama es el cliente de producción hacia el Ollama configurado en
// `embeddings.base_url` (un Ollama por instancia mofgw, D3). apiKey es
// opcional (Ollama local sin key por default). Timeout acota cada intento
// (un fallo de red → error, P9).
type Ollama struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New construye el cliente Ollama. timeout <= 0 → default 30s.
func New(baseURL, apiKey string, timeout time.Duration) *Ollama {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Embed envía el body a `base_url+"/embeddings"` y devuelve el body de
// respuesta crudo si el upstream responde 2xx. Ante fallo de red, timeout o
// status no-2xx → error (P9: 502 al cliente, no se reenvía el request).
func (o *Ollama) Embed(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// 018-001 P5/P11: User-Agent propio también en embeddings. NO se
	// inyecta x-opencode-session acá (P11: este cliente no recibe meta
	// de sesión).
	req.Header.Set("User-Agent", build.UserAgent)
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings upstream: status %d", resp.StatusCode)
	}
	return raw, nil
}
