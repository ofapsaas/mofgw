// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package provider define la interfaz Provider (contrato cross-feature
// del plan de epics) y un cliente OpenAI-compatible por upstream.
//
// El cliente habla HTTP con un provider real o fake (httptest en tests).
// El body del request se transporta CRUDO ([]byte): un proxy debe ser
// transparente — campos como tools, top_p o stream_options no se pierden
// en un round-trip por struct. ChatRequest es solo la vista parseada que
// el proxy usa para rutear (model/stream/max_tokens).
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Provider es la interfaz compartida por 001-003/004/005/006 (plan.md).
// Refinamiento de implementación (04 Ago): el body viaja crudo para no
// perder campos del contrato OpenAI (tools, top_p, ...) — ver plan.md §
// Contratos cross-feature.
type Provider interface {
	ID() string
	Complete(ctx context.Context, body []byte) (*CompleteResult, error)
	Stream(ctx context.Context, body []byte) (<-chan StreamEvent, error)
	MaxTokens() int
	// Serves reporta si el provider sirve el modelo pedido.
	Serves(model string) bool
}

// ChatRequest es la vista parseada del request del cliente (para rutear
// y validar). No es el transporte del body. Messages son RawMessage
// para que content como string, array o null no rompa el parseo de
// ruteo (el proxy es transparente al contenido).
type ChatRequest struct {
	Model     string            `json:"model"`
	Messages  []json.RawMessage `json:"messages"`
	Stream    bool              `json:"stream,omitempty"`
	MaxTokens *int64            `json:"max_tokens,omitempty"`
}

// ParseChatRequest extrae la vista de ruteo de un body crudo. Un body
// sin model o sin messages es inválido (error 400 en el proxy).
func ParseChatRequest(body []byte) (*ChatRequest, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}
	if req.Model == "" {
		return nil, errors.New("missing required field: model")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("missing required field: messages")
	}
	return &req, nil
}

// ChatToolCall tipa un elemento de choices[0].message.tool_calls (salida
// del ciclo tool-calling de 011-003): id/type/function{name,arguments}.
type ChatToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Function agrupa name/arguments del tool_call upstream; arguments es el
	// string JSON crudo (I2 de 003), no un objeto.
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatMessage es un mensaje del array messages.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls trae los tool_calls de un message assistant (choices[]);
	// ausente cuando el provider no devuelve tool_calls (camino P7 message).
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
}

// Choice es una opción de respuesta (chat.completion.choices[]).
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatResponse es la respuesta no-stream parseada (para tests y
// observabilidad). El cliente recibe Raw (el body crudo upstream).
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Usage agrupa los contadores de tokens de un response. Además de los
// campos estándar (prompt/completion/total), expone los campos de cache
// de providers (003-001): CachedTokens y CacheCreationTokens provienen
// de usage.prompt_tokens_details, ReasoningTokens de
// usage.completion_tokens_details. El parseo es tolerante: campos
// ausentes, null o no numéricos → 0, sin error.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	// 003-001: campos de cache/reasoning (anidados en el JSON upstream,
	// aplanados acá para observabilidad).
	CachedTokens        int `json:"-"`
	CacheCreationTokens int `json:"-"`
	ReasoningTokens     int `json:"-"`
}

// UnmarshalJSON parsea usage con los campos anidados de cache (formato
// OpenAI estándar que devuelven zen y bailian) de forma tolerante.
func (u *Usage) UnmarshalJSON(data []byte) error {
	type plain Usage
	var p struct {
		plain
		PromptTokensDetails *struct {
			CachedTokens        json.RawMessage `json:"cached_tokens"`
			CacheCreationTokens json.RawMessage `json:"cache_creation_input_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens json.RawMessage `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*u = Usage(p.plain)
	if p.PromptTokensDetails != nil {
		u.CachedTokens = usageInt(p.PromptTokensDetails.CachedTokens)
		u.CacheCreationTokens = usageInt(p.PromptTokensDetails.CacheCreationTokens)
	}
	if p.CompletionTokensDetails != nil {
		u.ReasoningTokens = usageInt(p.CompletionTokensDetails.ReasoningTokens)
	}
	return nil
}

// usageInt convierte un RawMessage a int tolerante: ausente/null/no
// numérico → 0.
func usageInt(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}

// CompleteResult ata la respuesta parseada al body crudo upstream.
type CompleteResult struct {
	Response   *ChatResponse
	Raw        []byte
	ProviderID string // provider que respondió (observabilidad)
}

// StreamEvent es un evento SSE ya parseado: Data es el payload JSON crudo
// de un `data: ...` (sin el prefijo), Err indica un fallo del stream
// (nunca se envía Err y Data juntos; el canal se cierra después de Err).
type StreamEvent struct {
	Data string
	Err  error
}

// ErrUpstream es el error normalizado de un intento fallido, con la
// información que el router necesita para clasificar (retryable o no)
// sin exponer keys ni URLs internas al cliente.
type ErrUpstream struct {
	StatusCode int
	Type       string        // "upstream_error" | "invalid_request_error" | "timeout" | "network"
	Message    string        // saneado: sin keys ni URLs internas
	RetryAfter time.Duration // del header Retry-After de un 429 (0 = ausente)
}

func (e *ErrUpstream) Error() string {
	return fmt.Sprintf("provider upstream error: %s (status %d)", e.Message, e.StatusCode)
}

// NewErrUpstream construye un ErrUpstream con mensaje saneado.
func NewErrUpstream(status int, typ, message string) *ErrUpstream {
	return &ErrUpstream{StatusCode: status, Type: typ, Message: sanitize(message)}
}

// IsNetwork reporta si el error es de red (conexión, DNS, etc).
func IsNetwork(err error) bool {
	var ue *ErrUpstream
	return errors.As(err, &ue) && ue.Type == "network"
}

// sanitize remueve material sensible de mensajes de error upstream
// (URLs internas, tokens, paths del server) antes de que puedan llegar
// al cliente.
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	for _, marker := range []string{"http://", "https://", "localhost", "127.0.0.1", "Bearer "} {
		if idx := strings.Index(s, marker); idx >= 0 {
			s = s[:idx] + "[redacted]"
			break
		}
	}
	return s
}

// Client es un Provider concreto: habla con un upstream OpenAI-compatible.
type Client struct {
	id        string
	baseURL   string // ej: https://api.openai.com/v1
	apiKey    string
	models    []string
	maxTokens int64
	http      *http.Client
}

// NewClient construye el cliente HTTP del provider.
func NewClient(id, baseURL, apiKey string, models []string, maxTokens int64, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 300 * time.Second}
	}
	return &Client{id: id, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, models: models, maxTokens: maxTokens, http: hc}
}

func (c *Client) ID() string     { return c.id }
func (c *Client) MaxTokens() int { return int(c.maxTokens) }
func (c *Client) Serves(model string) bool {
	for _, m := range c.models {
		if m == model {
			return true
		}
	}
	return false
}

// Models devuelve los modelos configurados (para GET /v1/models).
func (c *Client) Models() []string { return append([]string(nil), c.models...) }

// Complete ejecuta un chat no-stream con el body crudo y devuelve la
// respuesta parseada + el body crudo upstream.
func (c *Client) Complete(ctx context.Context, body []byte) (*CompleteResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &ErrUpstream{Type: "network", Message: "build request: " + err.Error()}
	}
	c.setHeaders(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &ErrUpstream{Type: "timeout", Message: "request cancelled or timed out"}
		}
		return nil, &ErrUpstream{Type: "network", Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, &ErrUpstream{Type: "network", Message: err.Error()}
	}
	if resp.StatusCode >= 400 {
		ue := NewErrUpstream(resp.StatusCode, classifyStatus(resp.StatusCode), extractMessage(raw))
		if resp.StatusCode == http.StatusTooManyRequests {
			ue.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return nil, ue
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &ErrUpstream{Type: "network", Message: "invalid upstream response: " + err.Error()}
	}
	return &CompleteResult{Response: &out, Raw: raw}, nil
}

// Stream ejecuta un chat streaming con el body crudo y devuelve un canal
// de eventos SSE. El canal se cierra al terminar; un error pre-primer-
// byte llega como error de retorno (no por el canal).
func (c *Client) Stream(ctx context.Context, body []byte) (<-chan StreamEvent, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &ErrUpstream{Type: "network", Message: "build request: " + err.Error()}
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &ErrUpstream{Type: "timeout", Message: "request cancelled or timed out"}
		}
		return nil, &ErrUpstream{Type: "network", Message: err.Error()}
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		ue := NewErrUpstream(resp.StatusCode, classifyStatus(resp.StatusCode), extractMessage(raw))
		if resp.StatusCode == http.StatusTooManyRequests {
			ue.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return nil, ue
	}

	ch := make(chan StreamEvent, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			ch <- StreamEvent{Data: payload}
			if payload == "[DONE]" {
				return
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Err: &ErrUpstream{Type: "network", Message: "stream read: " + err.Error()}}
		}
	}()
	return ch, nil
}

// Health implementa health.Checker (002-002): un GET liviano al
// endpoint de models del provider con auth mínima. Devuelve la latencia
// del check. NO cuenta como request de cliente (no toca contadores de
// fallback ni cooldown).
func (c *Client) Health(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return 0, &ErrUpstream{Type: "network", Message: "build health request: " + err.Error()}
	}
	c.setHeaders(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return time.Since(start), &ErrUpstream{Type: "network", Message: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return time.Since(start), &ErrUpstream{StatusCode: resp.StatusCode, Type: "upstream_error", Message: fmt.Sprintf("health check status %d", resp.StatusCode)}
	}
	return time.Since(start), nil
}

func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.apiKey)
}

// parseRetryAfter interpreta el header Retry-After (segundos o fecha
// HTTP). Devuelve 0 si no es parseable.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

// classifyStatus mapea un status HTTP a un tipo de error.
func classifyStatus(status int) string {
	switch {
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "upstream_error"
	}
}

// extractMessage saca el mensaje del body de error OpenAI-compatible si
// es parseable; si no, usa el status genérico.
func extractMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return "upstream error"
}
