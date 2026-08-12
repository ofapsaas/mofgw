// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package claude implementa el adapter `claude` de la interfaz
// subprocess.Backend (feature 013-002): arma el argv del CLI de claude,
// traduce el body chat-completions a un prompt único limpio, parsea el
// stream-json de claude (no-stream y stream) a los tipos OpenAI de
// provider, y detecta la negativa de policy en stderr.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
)

// Backend implementa subprocess.Backend (P10) para el CLI de claude.
type Backend struct {
	bin string // ruta del binario (config `command`; default "claude")
}

// New construye el adapter con la ruta del binario. Un bin vacío se
// resuelve al default "claude" en Args (P2/A.2).
func New(bin string) *Backend {
	return &Backend{bin: bin}
}

// Name identifica el backend (P1).
func (b *Backend) Name() string { return "claude" }

// Args arma el argv del CLI para un request (P2): binario, modo print
// (-p), sesión, modelo, wire-format stream-json y los backendFlags opacos
// al final. El prompt NO viaja en argv (P3): lo escribe el motor por stdin.
func (b *Backend) Args(s *subprocess.Session, model string, flags []string) []string {
	bin := b.bin
	if bin == "" {
		bin = "claude"
	}
	argv := []string{
		bin,
		"-p",
		"--session-id", s.ID,
		"--model", model,
		"--output-format", "stream-json",
	}
	return append(argv, flags...)
}

// TranslateReq traduce el body chat-completions a UN prompt único limpio
// (P4/P5): el contenido del ÚLTIMO mensaje role=="user". Si content es
// string se usa directo; si es array se concatenan las partes type=="text"
// en orden. Sin tools/tool_calls/resultados de tool. Error si no hay user.
func (b *Backend) TranslateReq(body []byte) (string, error) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}

	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		text, err := contentToText(m.Content)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", errors.New("empty user message content")
		}
		return text, nil
	}
	return "", errors.New("no user message")
}

// contentToText convierte un content (string o array de partes) al texto
// natural concatenado. Las partes no type=="text" se descartan (C5/I4).
func contentToText(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("invalid content: %w", err)
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), nil
}

// claudeEvent modela un evento del stream-json de claude. `delta` es crudo
// porque su forma varía por tipo de evento: en content_block_delta es
// {type,text}, en message_delta es {stop_reason,stop_sequence}.
type claudeEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta"`
}

// textDelta extrae el text de un delta de content_block_delta (solo si es
// text_delta). Devuelve ("", false) si el delta no es de texto.
func textDelta(raw json.RawMessage) (string, bool) {
	var d struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", false
	}
	if d.Type != "text_delta" {
		return "", false
	}
	return d.Text, true
}

// stopReason extrae el stop_reason del delta de un message_delta.
func stopReason(raw json.RawMessage) string {
	var d struct {
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return ""
	}
	return d.StopReason
}

// TranslateOut traduce el stdout agregado (stream-json) a ChatResponse (P6):
// choices[0] con role assistant y content = concatenación de los text de los
// content_block_delta con delta.type=="text_delta"; finish_reason mapeado del
// stop_reason del message_delta. Error si no hay contenido de texto. El motor
// sobreescribe ID/Object/Created/Model/Usage (fillResponse).
func (b *Backend) TranslateOut(raw []byte, model string) (*provider.ChatResponse, error) {
	var content strings.Builder
	stop := ""

	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // consistente con el motor (líneas largas)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev claudeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerante a líneas no-JSON
		}
		switch ev.Type {
		case "content_block_delta":
			if text, ok := textDelta(ev.Delta); ok {
				content.WriteString(text)
			}
		case "message_delta":
			stop = stopReason(ev.Delta)
		}
	}
	if content.Len() == 0 {
		return nil, errors.New("no text content in claude output")
	}

	resp := &provider.ChatResponse{
		Model: model,
		Choices: []provider.Choice{{
			Index:        0,
			FinishReason: mapStopReason(stop),
			Message: provider.ChatMessage{
				Role:    "assistant",
				Content: content.String(),
			},
		}},
	}
	return resp, nil
}

// mapStopReason mapea stop_reason de claude a finish_reason OpenAI (B-Q2).
func mapStopReason(stop string) string {
	switch stop {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}

// TranslateStreamOut traduce líneas del stdout (streaming) a eventos (P7/P8):
// UN StreamEvent.Data por cada content_block_delta con delta.type=="text_delta"
// con payload de chunk OpenAI. Los eventos de control (message_start,
// content_block_start/stop, message_delta, message_stop) y los deltas de
// thinking/tool_use no generan eventos. Nunca emite usage ni [DONE] (los
// agrega el motor). Devuelve cuando `lines` se cierra.
func (b *Backend) TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string) {
	for ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" {
			continue
		}
		var ev claudeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerante a líneas no-JSON
		}
		if ev.Type != "content_block_delta" {
			continue // control events, thinking, tool_use
		}
		text, ok := textDelta(ev.Delta)
		if !ok {
			continue // thinking_delta, input_json_delta, etc.
		}
		chunk := map[string]any{
			"id":      "chatcmpl-" + model,
			"object":  "chat.completion.chunk",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}},
		}
		payload, err := json.Marshal(chunk)
		if err != nil {
			continue
		}
		ch <- provider.StreamEvent{Data: string(payload)}
	}
}

// IsRefusal reporta si el stderr del CLI señala una negativa de policy (P9):
// contiene algún marker del set (case-insensitive) sobre stderr solamente.
func (b *Backend) IsRefusal(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, marker := range []string{"refus", "cannot", "not able to", "policy", "responsible use", "safety"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Assert de compilación (P10): Backend implementa subprocess.Backend.
var _ subprocess.Backend = (*Backend)(nil)
