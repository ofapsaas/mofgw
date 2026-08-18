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
	"github.com/ofapsaas/mofgw/internal/composition"
	"github.com/ofapsaas/mofgw/internal/logging"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/websearch"
)

// responsesRequestBody es el body crudo del Responses API de Odoo (usa
// `input`, no `messages`). Solo los campos necesarios para traducir y
// rechazar (P6-P9); el resto (store, max_output_tokens) se ignora.
type responsesRequestBody struct {
	Model             string            `json:"model"`
	Input             []json.RawMessage `json:"input"`
	Temperature       *float64          `json:"temperature"`
	Stream            *bool             `json:"stream"`
	Tools             []json.RawMessage `json:"tools"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls"`
	Text              *struct {
		Format *json.RawMessage `json:"format"`
	} `json:"text"`
}

// responsesInputItem tipa un item del `input[]` Responses de forma
// discriminada por `type` (011-003): items message (role+content de parts
// input_text) y items del ciclo tool-calling (`function_call`,
// `function_call_output`). Un solo struct cubre ambos shapes; la rama se
// elige por `Type`.
type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    string          `json:"output"`
	Content   []responsesPart `json:"content"`
}

type responsesPart struct {
	Type     string  `json:"type"`
	Text     string  `json:"text"`
	ImageURL string  `json:"image_url"`
	Detail   *string `json:"detail,omitempty"`
	FileData string  `json:"file_data"`
	Filename string  `json:"filename"`
}

// wireTool tipa un tool chat-completions anidado (P1 de 003): type function
// + function{name,description,parameters,strict}. Parameters viaja como
// RawMessage para preservar byte-identidad (I3); Strict con omitempty →
// ausente no se emite (D2 de 002).
type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

type wireToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

// wireResponseFormat tipa el response_format chat-completions (D1/D2):
// Strict con omitempty resuelve el passthrough — ausente → el campo no se
// emite en el wire. Schema sigue siendo json.RawMessage para preservar
// byte-identidad.
type wireResponseFormat struct {
	Type       string         `json:"type"`
	JSONSchema wireJSONSchema `json:"json_schema"`
}

type wireJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict *bool           `json:"strict,omitempty"`
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

// responsesContentArray traduce las parts de un item message a parts chat para
// el content-array (004 P3/D1): un item por part del input, en orden de
// aparición (I3). Mapea input_text→text, input_image→image_url, input_file→file
// (D2); las parts de tipo desconocido se dropean conservando el orden de las
// conocidas (consistente con el string-content que ignora lo que no es
// input_text).
func responsesContentArray(parts []responsesPart) []any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "input_text":
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "input_image":
			iu := map[string]any{"url": p.ImageURL} // data-URI sin re-marshal (I2)
			if p.Detail != nil {
				iu["detail"] = *p.Detail // omitempty: ausente no se emite (D2)
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": iu})
		case "input_file":
			out = append(out, map[string]any{
				"type": "file",
				"file": map[string]any{
					"file_data": p.FileData,
					"filename":  p.Filename,
				},
			})
		}
	}
	return out
}

// handleResponses es el endpoint POST /v1/responses, paralelo a handleChat
// pero operando sobre el body Responses (input) en lugar de messages.
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger := logging.WithRequest(s.logger, r.Context())
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = sr

	var providerID string
	model := ""
	var lastUsage *provider.Usage
	defer func() {
		s.emitRequestEnd(logger, start, sr.status, providerID, model, lastUsage)
	}()

	s.metrics.IncRequests()

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
	// Tool calling (011-003 + 011-005): reemplaza la rama P7 de 001. D3 de
	// 003: si CUALQUIER tool tiene type != "function" (incluido
	// web_search_preview) → 400 conservado. La feature 005 lo resuelve:
	// `web_search_preview` con web_search.enabled=true activa el flujo web
	// search (P1, no 400, no se agrega a chatTools); con enabled=false
	// conserva el 400 (sin regresión, D5). Un tool no-function no-web-search
	// (code_interpreter) sigue cayendo en el 400 en ambos modos (P1).
	var chatTools []wireTool
	webSearchActive := false // 011-005: activa el flujo web search (P1/D5)
	if len(rb.Tools) > 0 {
		for _, raw := range rb.Tools {
			var t struct {
				Type        string          `json:"type"`
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      *bool           `json:"strict"`
			}
			if err := json.Unmarshal(raw, &t); err != nil {
				openAIError(w, http.StatusBadRequest, "invalid tool definition", "invalid_request_error")
				return
			}
			switch {
			case t.Type == "web_search_preview":
				// 011-005 D1/D5: con enabled=true se marca la activación del
				// flujo (no 400, no se agrega a chatTools — se remueve del
				// upstream, P5). Con enabled=false conserva el 400 de D3.
				if !s.webSearchEnabled {
					openAIError(w, http.StatusBadRequest, "tool calling not yet supported", "invalid_request_error")
					return
				}
				webSearchActive = true
			case t.Type == "function":
				chatTools = append(chatTools, wireTool{
					Type: "function",
					Function: wireToolFunc{
						Name:        t.Name,
						Description: t.Description,
						Parameters:  t.Parameters, // I3: RawMessage, byte-idéntico
						Strict:      t.Strict,     // omitempty: ausente no se emite
					},
				})
			default:
				// P1: tool no-function no-web-search (code_interpreter) → 400.
				openAIError(w, http.StatusBadRequest, "tool calling not yet supported", "invalid_request_error")
				return
			}
		}
	}
	// Structured output (011-002): text.format.json_schema → response_format
	// (D1). Solo type == "json_schema" se traduce (D3); el resto de type o un
	// format malformado conserva la rama de rechazo P8 (400).
	var responseFormat *wireResponseFormat
	if rb.Text != nil && rb.Text.Format != nil {
		var format struct {
			Type   string          `json:"type"`
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict"`
		}
		if err := json.Unmarshal(*rb.Text.Format, &format); err != nil || format.Type != "json_schema" {
			openAIError(w, http.StatusBadRequest, "structured output not yet supported", "invalid_request_error")
			return
		}
		js := wireJSONSchema{Name: format.Name, Schema: format.Schema, Strict: format.Strict} // D2: strict nil → omitempty no emite
		responseFormat = &wireResponseFormat{Type: "json_schema", JSONSchema: js}
	}
	// P2: input usable para traducir a messages.
	if rb.Model == "" || len(rb.Input) == 0 {
		openAIError(w, http.StatusBadRequest, "missing required field: model or input", "invalid_request_error")
		return
	}

	// 4. Traducir input → messages (P3/P4/P5). Items del ciclo tool-calling
	// (function_call, function_call_output) conviven con items message en el
	// mismo input[]; los mensajes se emiten en orden de aparición.
	messages := make([]any, 0, len(rb.Input))
	for _, raw := range rb.Input {
		var item responsesInputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			openAIError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error")
			return
		}
		switch item.Type {
		case "function_call": // P3
			callID := item.CallID
			if callID == "" {
				callID = item.ID // fallback al id del item (P3)
			}
			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{
					map[string]any{
						"id":   callID, // == call_id del item (round-trip D1)
						"type": "function",
						"function": map[string]any{
							"name":      item.Name,
							"arguments": item.Arguments, // string JSON sin re-marshal (P3)
						},
					},
				},
			})
		case "function_call_output": // P4
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID, // matchea el id del tool_call (I4)
				"content":      item.Output,
			})
		default: // P5: item message, traducción como 001 (P3 de 001). 004 (D1):
			// decisión POR MENSAJE — todas las parts input_text → content string
			// (camino de 001/003 sin regresión); si hay input_image/input_file →
			// content-array con un item por part en orden (P3).
			hasFilePart := false
			for _, p := range item.Content {
				if p.Type == "input_file" || p.Type == "input_image" {
					hasFilePart = true
					break
				}
			}
			var content any
			if hasFilePart {
				content = responsesContentArray(item.Content)
			} else {
				var sb strings.Builder
				for _, p := range item.Content {
					if p.Type == "input_text" {
						sb.WriteString(p.Text)
					}
				}
				content = sb.String()
			}
			messages = append(messages, map[string]any{"role": item.Role, "content": content})
		}
	}
	// 4.5 Web search server-side (011-005, P1/D1): si el flujo web search
	// está activo (enabled:true + `web_search_preview`), derivar la query
	// del último mensaje user (P2), ejecutar DDG (P3) e inyectar los
	// resultados como un system message PREPENDIDO antes del primer mensaje
	// del input[] (P4/D4). Fallo de DDG o sin query → best-effort (P7): se
	// prosigue sin grounding. `web_search_preview` ya fue removido del
	// upstream (no se agregó a chatTools, P5).
	if webSearchActive {
		if query := deriveWebSearchQuery(rb.Input); query != "" {
			if results := s.runWebSearch(query); len(results) > 0 {
				system := buildWebSearchSystemMessage(query, results, s.webSearchMaxResults)
				messages = append([]any{map[string]any{"role": "system", "content": system}}, messages...)
			}
		}
	}
	chatBodyMap := map[string]any{"model": rb.Model, "messages": messages}
	if rb.Temperature != nil {
		chatBodyMap["temperature"] = *rb.Temperature
	}
	if len(chatTools) > 0 {
		chatBodyMap["tools"] = chatTools // P1: tools chat anidados (passthrough)
	}
	if rb.ParallelToolCalls != nil {
		chatBodyMap["parallel_tool_calls"] = *rb.ParallelToolCalls // P1: passthrough
	}
	if responseFormat != nil {
		chatBodyMap["response_format"] = responseFormat // passthrough (I2)
	}
	chatBody, err := json.Marshal(chatBodyMap)
	if err != nil {
		openAIError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error")
		return
	}

	// req.Messages se puebla con los mensajes traducidos para que la
	// telemetría (009-000) y el análisis de contexto (009-001) vean los
	// mismos roles que vería chat (el router solo usa req.Model, no
	// req.Messages, así que no altera el ruteo).
	rawMessages := make([]json.RawMessage, 0, len(messages))
	for _, m := range messages {
		b, _ := json.Marshal(m)
		rawMessages = append(rawMessages, b)
	}
	req := &provider.ChatRequest{Model: rb.Model, Messages: rawMessages}
	clientID := auth.ClientIDFrom(r.Context())
	sessionID := r.Header.Get("X-Session-Id")

	// Telemetría de descubrimiento (009-000 P3): UN evento por request con
	// body válido, emitido ANTES de los chequeos de limiter/budget/ventana/
	// upstream (mismo gate que handleChat). Best-effort (I7).
	s.emitRequestTelemetry(r, req, clientID, body, logger)
	// Fase 1 del análisis de contexto (009-001 P4): mismo gate que la
	// telemetría. Best-effort (I7/I2, P8).
	if s.contextAnalysis {
		if res, err := composition.Analyze(body, 10); err == nil {
			s.metrics.RecordContext(clientID, sessionID, logging.RequestID(r.Context()), req.Model, res, time.Now().Unix())
		}
	}

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
		// Límite por agente (004-002): fast-fail, misma semántica que chat.
		agentID := r.Header.Get("X-Agent-Id")
		if release, ok := s.keyedLimiter.AcquireAgent(clientID, agentID); !ok {
			rejectedLabel := clientID
			if agentID != "" {
				rejectedLabel = clientID + "/" + agentID
			}
			s.metrics.IncRejected(rejectedLabel, "agent_limit")
			openAIError(w, http.StatusTooManyRequests, "agent concurrency limit reached", "rate_limit_exceeded")
			return
		} else if release != nil {
			defer release()
		}
	}

	// Cache exact-match con key separada por endpoint (P12). Solo
	// determinístico (temperature == 0) → elegible; store se ignora (P13).
	isDeterministic := rb.Temperature != nil && *rb.Temperature == 0
	var respCacheKey string
	// P8 (011-005): un request web search NUNCA se sirve ni almacena en el
	// response cache, aunque temperature == 0 (los resultados DDG cambian
	// en el tiempo; servir del cache daría resultados obsoletos). Se excluye
	// acá: webSearchActive → respCacheKey queda "" → el HIT no ocurre y
	// storeResponseCache es no-op al final.
	if s.responseCacheEnabled && isDeterministic && !webSearchActive {
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
		s.handleChainError(w, err, logger, logging.RequestID(r.Context()), clientID, false)
		return
	}
	providerID = res.ProviderID
	s.metrics.SetLastProvider(providerID)
	lastUsage = &res.Response.Usage
	model = rb.Model
	s.recordCacheTokens(logging.RequestID(r.Context()), clientID, sessionID, providerID, rb.Model, &res.Response.Usage)
	s.emitTerminalSuccess(logging.RequestID(r.Context()), clientID, providerID, rb.Model, &res.Response.Usage, false)
	s.setUsageHeaders(w.Header(), providerID, rb.Model, &res.Response.Usage)

	// 7. Traducir chat → output[] (P10) con id estable (P11). Si el provider
	// devuelve tool_calls → SOLO items function_call (P6, D2), call_id = eco
	// directo del id upstream (D1), id estable aparte (P8). Sin tool_calls →
	// item message intacto (P7 / P10 de 001).
	stableID := responsesID(clientID, body)
	var toolCalls []provider.ChatToolCall
	if len(res.Response.Choices) > 0 {
		toolCalls = res.Response.Choices[0].Message.ToolCalls
	}
	var output []any
	if len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        stableID,              // P8: id estable, != call_id (D1)
				"call_id":   tc.ID,                 // eco directo del id upstream (D1)
				"name":      tc.Function.Name,      // leído por Odoo (P6)
				"arguments": tc.Function.Arguments, // string JSON parseable (I2)
				"status":    "completed",
			})
		}
	} else {
		content := ""
		if len(res.Response.Choices) > 0 {
			content = res.Response.Choices[0].Message.Content
		}
		output = []any{
			map[string]any{
				"type":   "message",
				"id":     stableID,
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": content},
				},
			},
		}
	}
	envelope := map[string]any{
		"id":     stableID,
		"object": "response",
		"output": output,
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

// deriveWebSearchQuery deriva la query de la búsqueda (011-005 P2/D2): la
// concatenación de los text de las parts input_text del ÚLTIMO item message
// con role=="user" del input[], en orden de aparición (misma regla que P5
// de 001). Determinista: mismo input[] → misma query (I3). Devuelve "" si
// no existe ningún item message user (→ el flujo web search se saltea, P7).
func deriveWebSearchQuery(input []json.RawMessage) string {
	query := ""
	for _, raw := range input {
		var item responsesInputItem
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		switch item.Type {
		case "function_call", "function_call_output":
			continue // items del ciclo tool-calling: no son mensajes user
		}
		if item.Role != "user" {
			continue
		}
		var sb strings.Builder
		for _, p := range item.Content {
			if p.Type == "input_text" {
				sb.WriteString(p.Text)
			}
		}
		query = sb.String() // sobrescribe: al final queda el ÚLTIMO mensaje user
	}
	return query
}

// runWebSearch ejecuta el buscador inyectado (P3) y devuelve los resultados
// como []websearch.Result, o nil ante error o vacío (best-effort, P7: sin
// grounding). El cliente es la interfaz tipada `websearch.Client` — el mock
// del test y el DDG de producción la satisfacen estructuralmente, así que no
// hay despacho reflectivo (Make Illegal States Unrepresentable).
func (s *Server) runWebSearch(query string) []websearch.Result {
	if s.webSearchClient == nil {
		return nil
	}
	rs, err := s.webSearchClient.Search(query)
	if err != nil || len(rs) == 0 {
		return nil
	}
	return rs
}

// buildWebSearchSystemMessage arma el system message grounded (P4/D4):
// header `[Web search results for "<query>"]` + N items numerados
// (title | url | snippet), N = min(max, len(results)) (P3: top-N). Sin
// anotaciones url_citation (I5).
func buildWebSearchSystemMessage(query string, results []websearch.Result, max int) string {
	n := len(results)
	if max > 0 && n > max {
		n = max
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Web search results for %q]", query)
	for i := 0; i < n; i++ {
		r := results[i]
		fmt.Fprintf(&sb, "\n%d. %s | %s | %s", i+1, r.Title, r.URL, r.Snippet)
	}
	return sb.String()
}
