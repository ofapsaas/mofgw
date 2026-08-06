// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stream

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/provider"
)

func TestNewWriterSetsHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := NewWriter(rec)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if w == nil {
		t.Fatal("writer nil")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("Cache-Control no seteado")
	}
}

func TestWriteEventAndFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	if err := w.WriteEvent(`{"choices":[{"delta":{"content":"hola"}}]}`); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: {") || !strings.Contains(body, "\n\n") {
		t.Fatalf("body SSE malformado: %q", body)
	}
	if !rec.Flushed {
		t.Fatal("no hubo flush")
	}
}

func TestWriteDone(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	_ = w.WriteDone()
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("body = %q, falta [DONE]", rec.Body.String())
	}
}

func TestWriteErrorHasDone(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	if err := w.WriteError("boom", "upstream_error"); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "boom") || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("body = %q, falta mensaje o [DONE]", body)
	}
}

func TestRewriteModel(t *testing.T) {
	payload := `{"id":"chatcmpl-1","model":"qwen-max-internal","choices":[{"delta":{"content":"x"}}]}`
	got := RewriteModel(payload, "gpt-4o-mini")
	if strings.Contains(got, "qwen-max-internal") {
		t.Fatalf("model interno no reescrito: %s", got)
	}
	if !strings.Contains(got, "gpt-4o-mini") {
		t.Fatalf("model pedido no presente: %s", got)
	}
	// otros campos preservados
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("payload reescrito no es JSON: %v", err)
	}
	if !strings.Contains(string(m["choices"]), "delta") {
		t.Fatal("choices no preservado")
	}
}

func TestRewriteModelPassthroughNonJSON(t *testing.T) {
	if got := RewriteModel("[DONE]", "gpt-4o-mini"); got != "[DONE]" {
		t.Fatalf("passthrough roto: %q", got)
	}
}

func TestRewriteIDStable(t *testing.T) {
	ev1 := `{"id":"chatcmpl-abc","choices":[{"delta":{"content":"a"}}]}`
	ev2 := `{"choices":[{"delta":{"content":"b"}}]}`
	id := "mofgw-42"
	got1 := RewriteID(ev1, id)
	got2 := RewriteID(ev2, id)
	if !strings.Contains(got1, "mofgw-42") || !strings.Contains(got2, "mofgw-42") {
		t.Fatalf("id estable no propagado: %s / %s", got1, got2)
	}
}

func TestExtractID(t *testing.T) {
	if got := ExtractID(`{"id":"chatcmpl-xyz","model":"m"}`); got != "chatcmpl-xyz" {
		t.Fatalf("ExtractID = %q", got)
	}
	if got := ExtractID(`{"choices":[]}`); got != "" {
		t.Fatalf("ExtractID sin id = %q, want empty", got)
	}
}

func TestCopyRewritesAndCloses(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	events := make(chan provider.StreamEvent, 3)
	events <- provider.StreamEvent{Data: `{"id":"chatcmpl-up1","model":"qwen","choices":[{"delta":{"content":"ho"}}]}`}
	events <- provider.StreamEvent{Data: `{"choices":[{"delta":{"content":"la"}}]}`}
	events <- provider.StreamEvent{Data: "[DONE]"}
	close(events)

	if err := w.Copy(events, "gpt-4o-mini"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	body := rec.Body.String()
	// model reescrito: el alias interno no debe aparecer como model
	if strings.Contains(body, `"model":"qwen"`) {
		t.Fatal("model interno visible en el stream")
	}
	if !strings.Contains(body, "gpt-4o-mini") {
		t.Fatal("model pedido no reescrito")
	}
	// id estable upstream propagado
	if !strings.Contains(body, "chatcmpl-up1") {
		t.Fatal("id upstream no propagado")
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatal("falta [DONE]")
	}
	if strings.Count(body, "chatcmpl-up1") != 2 {
		t.Fatalf("id no consistente entre eventos: %s", body)
	}
}

func TestCopyMidStreamErrorEmitsSSEError(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	events := make(chan provider.StreamEvent, 2)
	events <- provider.StreamEvent{Data: `{"choices":[{"delta":{"content":"parcial"}}]}`}
	events <- provider.StreamEvent{Err: provider.NewErrUpstream(0, "network", "connection reset")}
	close(events)

	err := w.Copy(events, "gpt-4o-mini")
	if err == nil {
		t.Fatal("Copy debería devolver el error del upstream")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatal("error a mitad de stream debe cerrar con [DONE]")
	}
	if !strings.Contains(body, "upstream_error") {
		t.Fatal("falta tipo de error en SSE")
	}
}

// failWriter es un ResponseWriter que falla en Write después de N bytes,
// simulando un cliente que corta la conexión a mitad del stream.
type failWriter struct {
	h      http.Header
	writes int
}

func (f *failWriter) Header() http.Header { return f.h }
func (f *failWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > 2 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func (f *failWriter) WriteHeader(int) {}
func (f *failWriter) Flush()          {}

func TestCopyClientDisconnect(t *testing.T) {
	// cliente que corta la conexión: el writer falla en Write después de
	// 2 eventos. Copy debe abortar sin panic y devolver io.ErrClosedPipe
	// (el cierre del canal upstream lo hace el router al ver el error).
	fw := &failWriter{h: make(http.Header)}
	w, err := NewWriter(fw)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	events := make(chan provider.StreamEvent, 100)
	for i := 0; i < 100; i++ {
		events <- provider.StreamEvent{Data: `{"choices":[{"delta":{"content":"x"}}]}`}
	}
	close(events)
	err = w.Copy(events, "m")
	if err != io.ErrClosedPipe {
		t.Fatalf("Copy con cliente cortado debería devolver io.ErrClosedPipe, got: %v", err)
	}
}

var _ http.Flusher = (*failWriter)(nil)

// asegura que httptest.ResponseRecorder implementa Flusher (compile-time)
var _ http.Flusher = (*httptest.ResponseRecorder)(nil)
