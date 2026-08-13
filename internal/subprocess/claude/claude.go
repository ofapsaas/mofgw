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
	"context"
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
//
// VERIFICADO empíricamente contra el CLI real v2.1.229 (12 Ago 2026): usa
// SESIONES NOMBRADAS — `-n <nombre>` crea la sesión, `--resume <nombre>`
// la continúa (persiste historial y reutiliza el prompt-cache, ~8x más
// barato). El nombre = clientID saneado (legible, determinista). Evita el
// hack del `--session-id` UUID y el "already in use" de reusar un id. El
// CLI exige `--verbose` con `--print` + `--output-format=stream-json`.
// `s.New` lo setea el motor (dir recién creado → primera vez del cliente).
func (b *Backend) Args(s *subprocess.Session, model string, flags []string) []string {
	bin := b.bin
	if bin == "" {
		bin = "claude"
	}
	name := sessionName(s)
	argv := []string{bin, "-p"}
	if s.New {
		argv = append(argv, "-n", name)
	} else {
		argv = append(argv, "--resume", name)
	}
	argv = append(argv, "--model", model, "--output-format", "stream-json", "--verbose")
	return append(argv, flags...)
}

// sessionName deriva el nombre de la sesión nombrada del CLI claude: el
// clientID saneado a [a-zA-Z0-9_-] (chars seguros para `claude -n`). Si el
// clientID queda vacío o produce un nombre vacío (defensivo — el motor hace
// fail-closed sin clientID), cae al session key sha256 hex del motor (ya
// alfanumérico, seguro y determinista por cliente).
func sessionName(s *subprocess.Session) string {
	name := sanitizeName(s.ClientID)
	if name == "" {
		name = s.ID // sha256 hex del motor: seguro y determinista
	}
	return name
}

// sanitizeName deja solo [a-zA-Z0-9_-], reemplazando el resto por '-', y
// trima guiones de los extremos (el CLI rechaza nombres con guiones
// iniciales/finales).
func sanitizeName(s string) string {
	var b []byte
	lastHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if ok {
			b = append(b, c)
			lastHyphen = c == '-'
		} else {
			if !lastHyphen && len(b) > 0 {
				b = append(b, '-')
				lastHyphen = true
			}
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	return string(b)
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
// {type,text}, en message_delta es {stop_reason,stop_sequence}. `message`
// es el bloque del evento `assistant` (message.content[]).
type claudeEvent struct {
	Type    string          `json:"type"`
	Delta   json.RawMessage `json:"delta"`
	Message json.RawMessage `json:"message"`
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

// messageText extrae el texto concatenado de los bloques content[].type=="text"
// de un evento `assistant` (message.content[]). Devuelve "" si no hay texto.
func messageText(raw json.RawMessage) string {
	var m struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
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
		case "assistant":
			// El CLI real a veces emite el evento `assistant` con el mensaje
			// completo (content[].type=="text") en vez de deltas.
			if t := messageText(ev.Message); t != "" {
				content.WriteString(t)
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
// agrega el motor). Devuelve cuando `lines` se cierra o el ctx se cancela.
// P4 (D2): todo send a ch se guarda con select sobre ctx.Done(); ante
// cancelación retorna sin bloquear ni colgar el lock.
func (b *Backend) TranslateStreamOut(ctx context.Context, lines <-chan string, ch chan<- provider.StreamEvent, model string) {
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
		// P4 (D2): guard del send sobre ctx.Done(). El kill+reap del proceso
		// lo hace el motor; el adapter solo deja de enviar y retorna.
		select {
		case ch <- provider.StreamEvent{Data: string(payload)}:
		case <-ctx.Done():
			return
		}
	}
}

// IsRefusal reporta si el stderr del CLI señala una negativa de policy (P9):
// contiene algún marker del set (case-insensitive) sobre stderr solamente.
// P8 (D5): se removió el marker ancho "cannot" para evitar el falso positivo
// sobre errores de sistema genéricos; se conserva el vocabulario específico.
func (b *Backend) IsRefusal(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, marker := range []string{"refus", "not able to", "policy", "responsible use", "safety"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// IsSessionNotFound reporta si el stderr del CLI indica que la sesión a
// reanudar no existe ("No conversation found with session ID"). El motor
// reintenta con creación (-n) en ese caso (verificado en el smoke real: el
// estado `New` por dir no es confiable).
func (b *Backend) IsSessionNotFound(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "no conversation found")
}

// Assert de compilación (P10): Backend implementa subprocess.Backend.
var _ subprocess.Backend = (*Backend)(nil)
