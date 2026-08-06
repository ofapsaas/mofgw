// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stream

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// ---- SEC-001 P1: SSE errors saneados ----

// TestSEC001_MidStreamErrorSaneado: cuando el stream se corta con un
// error del upstream cuyo mensaje contiene detalle interno (hosts,
// TLS, rutas), el cliente via SSE NO debe recibir ese detalle — solo un
// mensaje genérico. El error crudo queda para logs (Copy lo devuelve).
// RED: hoy Copy hace "upstream stream interrupted: "+ev.Err.Error() crudo
// (stream.go:184). GREEN: pasa por el mecanismo de saneamiento (absorb).
func TestSEC001_MidStreamErrorSaneado(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := NewWriter(rec)
	events := make(chan provider.StreamEvent, 2)
	events <- provider.StreamEvent{Data: `{"choices":[{"delta":{"content":"parcial"}}]}`}
	// error con detalle interno sensible
	events <- provider.StreamEvent{Err: errors.New("dial tcp internal.corp.local:443: tls handshake error: x509: certificate signed by unknown authority")}
	close(events)

	err := w.Copy(events, "gpt-4o-mini")
	if err == nil {
		t.Fatal("Copy debería devolver el error del upstream (para logs)")
	}
	body := rec.Body.String()
	// el detalle interno NO debe filtrarse al cliente
	for _, sensitive := range []string{"internal.corp.local", "tls handshake", "x509", "unknown authority"} {
		if strings.Contains(body, sensitive) {
			t.Fatalf("SEC-001 P1: detalle interno filtrado al cliente via SSE (%q):\n%s", sensitive, body)
		}
	}
	// el cliente recibe un mensaje genérico + el tipo de error
	if !strings.Contains(body, "upstream_error") {
		t.Fatalf("SEC-001 P1: falta tipo upstream_error en SSE:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("SEC-001 P1: error a mitad de stream debe cerrar con [DONE]:\n%s", body)
	}
}
