// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Endpoint POST /v1/responses (011-001-responses-endpoint): el Responses
// API de OpenAI que Odoo 19 usa en _request_llm_openai_helper. mofgw
// traduce el body Responses (usa `input`) a chat-completions y lo delega
// en la MISMA pipeline del router (P4); traduce la respuesta upstream de
// vuelta a `output[]`. La traducción vive SOLO en esta capa — el router y
// los providers siguen hablando chat-completions intactos (I1).
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/clamp"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/provider"
)

// responsesRequestBody es el body crudo del Responses API de Odoo (usa
// `input`, no `messages`). Solo los campos necesarios para traducir y
// rechazar (P6-P9); el resto (store, max_output_tokens) se ignora.
type responsesRequestBody struct {
	Model       string               `json:"model"`
	Input       []responsesInputItem `json:"input"`
	Temperature *float64             `json:"temperature"`
	Stream      *bool                `json:"stream"`
	Tools       []json.RawMessage    `json:"tools"`
	Text        *struct {
		Format *json.RawMessage `json:"format"`
	} `json:"text"`
}

type responsesInputItem struct {
	Role    string          `json:"role"`
	Content []responsesPart `json:"content"`
}

type responsesPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesChatMessage es un mensaje chat-completions traducido de un
// item de `input` (P3): role preservado, content = concatenación de los
// text de las parts input_text.
type responsesChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// responsesCacheKey arma la key del cache exact-match para /v1/responses
// (P12): mismo esquema que responseCacheKey pero con namespace propio, de
// modo que una responses-response jamás se sirve como chat-response ni a
// la inversa.
func responsesCacheKey(clientID string, body []byte) (string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("responses"))
	h.Write([]byte{0})
	h.Write([]byte(clientID))
	h.Write([]byte{0})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// responsesID genera el id estable/determinístico del response y del item
// message (P11): mismo clientID + body → mismo id. Habilita que Odoo
// reutilice el id de item como call_id en el ciclo tool-calling (003).
func responsesID(clientID string, body []byte) string {
	h := sha256.New()
	h.Write([]byte("responses"))
	h.Write([]byte{0})
	h.Write([]byte(clientID))
	h.Write([]byte{0})
	h.Write(body)
	return "resp_" + hex.EncodeToString(h.Sum(nil))[:24]
}

// handleResponses es el endpoint POST /v1/responses, paralelo a handleChat
// pero operando sobre el body Responses (input) en lugar de messages.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	logger := logging.WithRequest(s.logger, r.Context())

	// 1. Leer body con límite (413 si excede).
	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBody+1))
	if err != nil {
		openAIError(w, http.StatusBadRequest, "error reading request body", "invalid_request_error")
		return
	}
	if int64(len(body)) > s.maxBody {
		openAIError(w, http.StatusRequestEntityTooLarge, "request body too large", "invalid_request_error")
		return
	}

	// 2. Parsear el body Responses (P2: body no-JSON → 400).
	var rb responsesRequestBody
	if err := json.Unmarshal(body, &rb); err != nil {
		openAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}

	// 3. Rechazo explícito de features futuras (P6-P9).
	if rb.Stream != nil && *rb.Stream {
		openAIError(w, http.StatusBadRequest, "streaming not supported yet", "invalid_request_error")
		return
	}
	if len(rb.Tools) > 0 {
		openAIError(w, http.StatusBadRequest, "tool calling not yet supported", "invalid_request_error")
		return
	}
	if rb.Text != nil && rb.Text.Format != nil {
		openAIError(w, http.StatusBadRequest, "structured output not yet supported", "invalid_request_error")
		return
	}
	for _, item := range rb.Input {
		for _, p := range item.Content {
			if p.Type == "input_file" || p.Type == "input_image" {
				openAIError(w, http.StatusBadRequest, "file attachments not yet supported", "invalid_request_error")
				return
			}
		}
	}

	// P2: input usable para traducir a messages.
	if rb.Model == "" || len(rb.Input) == 0 {
		openAIError(w, http.StatusBadRequest, "missing required field: model or input", "invalid_request_error")
		return
	}

	// 4. Traducir input → messages (P3).
	messages := make([]responsesChatMessage, 0, len(rb.Input))
	for _, item := range rb.Input {
		var sb strings.Builder
		for _, p := range item.Content {
			if p.Type == "input_text" {
				sb.WriteString(p.Text)
			}
		}
		messages = append(messages, responsesChatMessage{Role: item.Role, Content: sb.String()})
	}
	chatBodyMap := map[string]any{"model": rb.Model, "messages": messages}
	if rb.Temperature != nil {
		chatBodyMap["temperature"] = *rb.Temperature
	}
	chatBody, err := json.Marshal(chatBodyMap)
	if err != nil {
		openAIError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error")
		return
	}

	req := &provider.ChatRequest{Model: rb.Model}
	clientID := auth.ClientIDFrom(r.Context())

	// 5. Pipeline compartida (P4): limiter, cache, clamp, ventana, budget —
	// replicado del camino no-stream de handleChat.

	// Limites de concurrencia (004-001/004-002).
	if s.globalLimiter != nil {
		ctx, cancel := context.WithTimeout(r.Context(), s.backpressureTimeout)
		if !s.globalLimiter.Acquire(ctx) {
			cancel()
			s.metrics.IncRejected(clientID, "global_timeout")
			openAIError(w, http.StatusTooManyRequests, "server busy: concurrency limit reached", "rate_limit_exceeded")
			return
		}
		cancel()
		defer s.globalLimiter.Release()
	}
	if s.keyedLimiter != nil {
		if release, ok := s.keyedLimiter.AcquireClient(clientID); !ok {
			s.metrics.IncRejected(clientID, "client_limit")
			openAIError(w, http.StatusTooManyRequests, "client concurrency limit reached", "rate_limit_exceeded")
			return
		} else if release != nil {
			defer release()
		}
	}

	// Cache exact-match con key separada por endpoint (P12). Solo
	// determinístico (temperature == 0) → elegible; store se ignora (P13).
	isDeterministic := rb.Temperature != nil && *rb.Temperature == 0
	var respCacheKey string
	if s.responseCacheEnabled && isDeterministic {
		if k, err := responsesCacheKey(clientID, body); err == nil {
			respCacheKey = k
			if entry, ok := s.responseCache.Get(k); ok {
				s.metrics.IncRespCacheHit(clientID)
				age := time.Since(entry.CreatedAt).Seconds()
				w.Header().Set("Content-Type", entry.ContentType)
				w.Header().Set("X-Mofgw-Cache", "HIT")
				w.Header().Set("X-Mofgw-Cache-Age", strconv.Itoa(int(age)))
				w.WriteHeader(entry.StatusCode)
				_, _ = w.Write(entry.Body)
				return
			}
			s.metrics.IncRespCacheMiss(clientID)
			w.Header().Set("X-Mofgw-Cache", "MISS")
		}
	}

	// Clamp por modelo pre-routing (4.4): misma regla que chat.
	chatReqBody := chatBody
	if md, ok := s.modelMeta[rb.Model]; ok && md.MaxOutput > 0 {
		clamped, err := clamp.Request(chatBody, int64(md.MaxOutput))
		if err != nil {
			openAIError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
			return
		}
		chatReqBody = clamped
	}

	// Rechazo temprano por ventana de contexto (4.5 / P4a).
	maxTokens := 0
	if md, ok := s.modelMeta[rb.Model]; ok && md.MaxOutput > 0 {
		maxTokens = md.MaxOutput
	}
	if promptTokens := estimatePromptTokens(chatReqBody); s.exceedsContextWindow(rb.Model, promptTokens, maxTokens) {
		s.metrics.IncRejected(clientID, "context_window")
		msg := fmt.Sprintf("estimated prompt tokens %d + max_tokens %d exceed context window %d for model %q", promptTokens, maxTokens, s.modelMeta[rb.Model].ContextWindow, rb.Model)
		openAIError(w, http.StatusBadRequest, msg, "invalid_request_error")
		logger.Debug("context_window_rejected", "model", rb.Model, "estimated", promptTokens, "max_tokens", maxTokens)
		return
	}

	// Budget por cliente (4.6).
	if reason := s.budgetExceeded(clientID); reason != "" {
		s.metrics.IncRejected(clientID, reason)
		openAIError(w, http.StatusTooManyRequests, "budget exceeded: "+reason, "rate_limit_exceeded")
		return
	}

	// 6. Delegar en la pipeline de chat (router.Complete).
	res, err := s.router.Complete(r.Context(), req, chatReqBody)
	if err != nil {
		s.handleChainError(w, err, logger)
		return
	}
	providerID := res.ProviderID
	s.metrics.SetLastProvider(providerID)
	sessionID := r.Header.Get("X-Session-Id")
	s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, providerID, rb.Model, &res.Response.Usage)
	s.setUsageHeaders(w.Header(), providerID, rb.Model, &res.Response.Usage)

	// 7. Traducir chat → output[] (P10) con id estable (P11).
	content := ""
	if len(res.Response.Choices) > 0 {
		content = res.Response.Choices[0].Message.Content
	}
	stableID := responsesID(clientID, body)
	envelope := map[string]any{
		"id":     stableID,
		"object": "response",
		"output": []any{
			map[string]any{
				"type":   "message",
				"id":     stableID,
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": content},
				},
			},
		},
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		openAIError(w, http.StatusBadGateway, "error building response", "server_error")
		return
	}

	// P5: forzar model a nivel raíz (modelo del cliente) con
	// rewriteResponseModel — el modelo que Odoo ve == el que resolvió el
	// router para el cliente.
	out = rewriteResponseModel(out, rb.Model)

	s.storeResponseCache(respCacheKey, clientID, http.StatusOK, "application/json", out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
