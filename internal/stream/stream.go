// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stream implementa el passthrough SSE del proxy (feature
// 001-005-streaming). La frontera del primer byte ya fue resuelta por el
// router (001-003): acá el stream está COMPROMETIDO — passthrough directo
// con flush inmediato, reescritura de model/id para que el cliente vea
// siempre el modelo que pidió con un id estable mofgw-*, y cierre
// limpio con SSE de error + [DONE] si el upstream muere a mitad.
// NUNCA reconectar a otro provider post-primer-byte.
package stream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// Writer escribe eventos SSE al cliente con flush inmediato.
type Writer struct {
	w   http.ResponseWriter
	fl  http.Flusher
	buf bytes.Buffer
}

// NewWriter prepara el ResponseWriter para SSE (headers + verifica
// Flusher). Devuelve error si el cliente no soporta flush (no se puede
// hacer streaming real).
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("stream: ResponseWriter no soporta Flush")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	return &Writer{w: w, fl: fl}, nil
}

// WriteEvent escribe un evento SSE (`data: <payload>\n\n`) y flushea.
func (s *Writer) WriteEvent(payload string) error {
	s.buf.Reset()
	s.buf.WriteString("data: ")
	s.buf.WriteString(payload)
	s.buf.WriteString("\n\n")
	if _, err := s.w.Write(s.buf.Bytes()); err != nil {
		return err
	}
	s.fl.Flush()
	return nil
}

// WriteError escribe un evento SSE de error bien formado (formato
// OpenAI-compatible) + terminador [DONE].
func (s *Writer) WriteError(message, typ string) error {
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": message, "type": typ},
	})
	if err := s.WriteEvent(string(payload)); err != nil {
		return err
	}
	return s.WriteDone()
}

// WriteDone escribe el terminador `data: [DONE]`.
func (s *Writer) WriteDone() error {
	return s.WriteEvent("[DONE]")
}

// idGenerator provee ids estables mofgw-* (inyectable en tests).
type idGenerator func() string

var counter atomic.Int64

// defaultID genera un id estable único por stream.
func defaultID() string {
	return fmt.Sprintf("mofgw-%d", counter.Add(1))
}

// RewriteModel reemplaza el campo model de un payload SSE JSON. Si el
// payload no es JSON parseable (ej: [DONE]), pasa intacto.
func RewriteModel(payload, model string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}
	rewritten, err := json.Marshal(model)
	if err != nil {
		return payload
	}
	m["model"] = rewritten
	return reencode(m)
}

// RewriteID inyecta/reescribe el campo id de un payload SSE JSON.
func RewriteID(payload, id string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}
	rewritten, err := json.Marshal(id)
	if err != nil {
		return payload
	}
	m["id"] = rewritten
	return reencode(m)
}

// ExtractID devuelve el id del primer evento si el upstream lo expone.
func ExtractID(payload string) string {
	var m struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(payload), &m) == nil && m.ID != "" {
		return m.ID
	}
	return ""
}

func reencode(m map[string]json.RawMessage) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(m)
	return strings.TrimSpace(buf.String())
}

// Copy reenvía el canal de eventos del router al cliente, reescribiendo
// model (modelo pedido por el cliente) e id estable. Si el canal muere
// con error a mitad de stream, emite SSE de error + [DONE]. Devuelve el
// error del upstream (para logging) o nil si terminó limpio.
func (s *Writer) Copy(events <-chan provider.StreamEvent, clientModel string) error {
	var stableID string
	for ev := range events {
		if ev.Err != nil {
			_ = s.WriteError("upstream stream interrupted: "+ev.Err.Error(), "upstream_error")
			return ev.Err
		}
		if ev.Data == "[DONE]" {
			return s.WriteDone()
		}
		if stableID == "" {
			stableID = ExtractID(ev.Data)
			if stableID == "" {
				stableID = defaultID()
			}
		}
		payload := RewriteID(RewriteModel(ev.Data, clientModel), stableID)
		if err := s.WriteEvent(payload); err != nil {
			return io.ErrClosedPipe
		}
	}
	return nil
}
