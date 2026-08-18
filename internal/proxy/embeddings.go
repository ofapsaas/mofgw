// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Endpoint POST /v1/embeddings (011-006-embeddings): el endpoint que Odoo
// 19 enterprise usa para generar embeddings de un texto
// (_request_llm_embedding → POST /v1/embeddings). mofgw actúa como gateway
// hacia Ollama: recibe el request de Odoo, FUERZA el modelo de embeddings
// del cliente (principio "el cliente nunca elige", P3 — ignora el `model`
// que manda Odoo), lo forwardea al Ollama configurado en
// `embeddings.base_url` y devuelve el vector OpenAI-compatible. La lógica
// vive SOLO en esta capa + su cliente HTTP dedicado (embeddings.Client);
// router, providers y provider.Client quedan intactos (I1, I7).
package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/provider"
)

// embeddingsRequestBody tipa el body OpenAI-compatible que manda Odoo
// (spec §Contrato): input (string, array de strings o array de token-arrays),
// model (IGNORADO por mofgw, P3), dimensions y encoding_format. input se
// conserva como json.RawMessage para el passthrough byte-idéntico al
// upstream (P5/I4).
type embeddingsRequestBody struct {
	Input          json.RawMessage `json:"input"`
	Model          string          `json:"model"`
	Dimensions     int             `json:"dimensions"`
	EncodingFormat string          `json:"encoding_format"`
}

// hasInput reporta si el body trae un input usable (P2): presente y no null.
func (b *embeddingsRequestBody) hasInput() bool {
	return len(b.Input) > 0 && string(b.Input) != "null"
}

// handleEmbeddings es el endpoint POST /v1/embeddings (P1-P10). Flujo: leer
// body con límite → parsear (P2) → resolver modelo forzado por cliente
// (P3/P4) → budget (P8) → reescribir `model` → forward al cliente Ollama
// (P9) → parsear respuesta → forzar `model` en el envelope (P3/P6) →
// recordCacheTokens/setUsageHeaders (P7) → emitir.
func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger := logging.WithRequest(s.logger, r.Context())
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = sr

	var providerID string
	var model string
	var lastUsage *provider.Usage
	defer func() {
		s.emitRequestEnd(logger, start, sr.status, providerID, model, lastUsage)
	}()
	s.metrics.IncRequests()

	// 1. Leer body con límite (413 si excede) — mismo contrato que chat.
	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBody+1))
	if err != nil {
		openAIError(w, http.StatusBadRequest, "error reading request body", "invalid_request_error")
		return
	}
	if int64(len(body)) > s.maxBody {
		openAIError(w, http.StatusRequestEntityTooLarge, "request body too large", "invalid_request_error")
		return
	}

	// 2. Parsear el body (P2: body no-JSON → 400).
	var eb embeddingsRequestBody
	if err := json.Unmarshal(body, &eb); err != nil {
		openAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if !eb.hasInput() {
		openAIError(w, http.StatusBadRequest, "missing required field: input", "invalid_request_error")
		return
	}

	clientID := auth.ClientIDFrom(r.Context())
	sessionID := r.Header.Get("X-Session-Id")

	// 3. Modelo forzado por cliente (P3/P4): el `model` de Odoo se ignora por
	// completo; el modelo que va a Ollama (y al envelope) es el configurado
	// para el clientID. Cliente sin `embeddings.model` → 400 fail-fast (D2).
	model = s.embeddingsModelFor(clientID)
	if model == "" {
		openAIError(w, http.StatusBadRequest, "no embeddings model configured for client", "invalid_request_error")
		return
	}

	// 4. Budget por cliente (P8): 429 rate_limit_exceeded si el consumo
	// acumulado del cliente excede su límite (mismo contrato que chat).
	if reason := s.budgetExceeded(clientID); reason != "" {
		s.metrics.IncRejected(clientID, reason)
		openAIError(w, http.StatusTooManyRequests, "budget exceeded: "+reason, "rate_limit_exceeded")
		return
	}

	// Fail loud: embeddings sin configurar (base_url global ausente) no puede
	// forwardear → 502 upstream_error (P9/P10).
	if s.embeddingsClient == nil {
		s.metrics.IncErrors()
		openAIError(w, http.StatusBadGateway, "embeddings upstream not configured", "upstream_error")
		return
	}

	// 5. Armar el body upstream reescribiendo `model` (P3) y pasando `input`
	// byte-idéntico (P5) + dimensions/encoding_format passthrough (P5/D1).
	upMap := map[string]any{"model": model, "input": eb.Input}
	if eb.Dimensions > 0 {
		upMap["dimensions"] = eb.Dimensions
	}
	if eb.EncodingFormat != "" {
		upMap["encoding_format"] = eb.EncodingFormat
	}
	upBody, err := json.Marshal(upMap)
	if err != nil {
		openAIError(w, http.StatusBadGateway, "error building request", "server_error")
		return
	}

	// 6. Forward a Ollama (P9): fallo de red/timeout/no-2xx → 502 upstream.
	raw, err := s.embeddingsClient.Embed(r.Context(), upBody)
	if err != nil {
		s.metrics.IncErrors()
		openAIError(w, http.StatusBadGateway, "embeddings upstream error", "upstream_error")
		return
	}

	providerID = "embeddings"
	// 7. Pricing por tokens del input (P7/D5): mismo mecanismo de
	// recordCacheTokens (IncUsage + IncCost); modelo ausente de `pricing:` →
	// costo 0 por estimateCost. Headers X-Usage-* con setUsageHeaders.
	usage := parseEmbeddingsUsage(raw)
	lastUsage = usage
	s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, providerID, model, usage)
	s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, providerID, model, usage, false)
	s.setUsageHeaders(w.Header(), providerID, model, usage)

	// 8. Forzar `model` del envelope == el forzado por cliente (P3/P6).
	out := rewriteResponseModel(raw, model)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// parseEmbeddingsUsage extrae el usage del body de respuesta OpenAI-compatible
// (P6/P7): prompt_tokens/completion_tokens/total_tokens. Sin usage → usage
// vacío (cero, no crashea — mismo espíritu que recordCacheTokens).
// embeddingsUsageResponse tipa el envelope OpenAI-compatible y reutiliza
// provider.Usage (UnmarshalJSON tolerante: campos ausentes/null → 0).
type embeddingsUsageResponse struct {
	Usage provider.Usage `json:"usage"`
}

func parseEmbeddingsUsage(raw []byte) *provider.Usage {
	var resp embeddingsUsageResponse
	if json.Unmarshal(raw, &resp) != nil {
		return &provider.Usage{}
	}
	return &resp.Usage
}
