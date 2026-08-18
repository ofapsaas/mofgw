// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package registry implementa el registro unificado de accounting + outcome
// (feature 014-001-registro-unificado): un archivo JSONL append-only en
// disco con UN evento por intento (cada visita a un provider de la cadena)
// + UN evento terminal por request en routing. Es la fuente de verdad con
// resolución temporal que telemetry.jsonl (009-000) y /metrics no cubren.
//
// El esquema del evento es el contrato del epic EXTERNO de
// consolidación/visualización (fuera de mofgw): se serializa SOLO los campos
// del esquema con json.Marshal (structs tipadas, D1), una línea JSON por
// evento terminada en "\n", sin envoltorio de slog. Privacidad por
// construcción (I2): el writer solo serializa el esquema; los emisores nunca
// pasan body/headers/keys.
//
// Off por default (I3): nil writer en router/proxy = sin emisión ni costo.
// Fail-fast solo al arranque (P3): un archivo no abrible impide el arranque;
// en runtime los errores de escritura son best-effort (P16/I4).
package registry

import (
	"encoding/json"
	"os"
	"sync"
)

// Tokens es el desglose de tokens de un request (D9/D12). Cero = ausente.
type Tokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Cache      int `json:"cache"`     // cached/hit tokens (003-001)
	Reasoning  int `json:"reasoning"` // reasoning tokens (003-001)
}

// AttemptEvent es UN evento por visita a un provider de la cadena (D5/D6):
// filmado al CONCLUSIÓN de la visita, con outcome ya conocido. `attempt` es
// el ordinal global de la visita (1-based, secuencial en el request);
// `retries` es el nº de reintentos 002-001 ejecutados sobre ese provider en
// esa visita (P9 serializa el retry como una sola visita). `cause` es el typ
// de la clasificación del router (vacío en outcome ok); `status` es el HTTP
// del fallo tal como lo vio la cadena (200 en outcome ok, 502 para
// timeout/network, D6).
type AttemptEvent struct {
	Type      string `json:"type"` // "attempt"
	RequestID string `json:"request_id"`
	Ts        string `json:"ts"` // RFC3339 UTC con milisegundos (D4)
	Client    string `json:"client"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Outcome   string `json:"outcome"` // "ok" | "fallback"
	Cause     string `json:"cause"`   // "" si ok
	Status    int    `json:"status"`  // 200 si ok; HTTP del fallo
	Attempt   int    `json:"attempt"` // ordinal global 1-based
	Retries   int    `json:"retries"` // reintentos en esta visita
}

// TerminalEvent es UN evento por request en routing (P10): success (post-
// respuesta, en los call-sites de recordCacheTokens) o error (cuando la
// cadena falla). error_code = ChainError.Code semántico (D8); status = HTTP
// FINAL enviado al cliente; final_provider = provider ganador o último
// intentado ("" si ninguno, p.ej. model_not_found). tokens/cost_usd son 0 en
// outcome error (P13).
type TerminalEvent struct {
	Type          string  `json:"type"` // "terminal"
	RequestID     string  `json:"request_id"`
	Ts            string  `json:"ts"` // RFC3339 UTC con milisegundos (D4)
	Client        string  `json:"client"`
	Outcome       string  `json:"outcome"` // "success" | "error"
	ErrorCode     string  `json:"error_code"`
	Status        int     `json:"status"` // 200 / 502 / 503 / 404...
	FinalProvider string  `json:"final_provider"`
	Tokens        Tokens  `json:"tokens"`
	CostUSD       float64 `json:"cost_usd"`
	Stream        bool    `json:"stream"`
}

// Writer escribe los eventos del registro como JSONL append-only (D10/I1).
// Thread-safe: escrituras serializadas por mutex (P4/I5). El archivo solo se
// abre O_APPEND — nunca se trunca ni reescribe, sobrevive restarts (I1).
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// NewWriter abre file en modo append (O_APPEND|O_CREATE|O_WRONLY, 0o640).
// Fail-fast (P3): un archivo no abrible impide el arranque (patrón
// logging.BuildTelemetry). El archivo es append-only: una línea ya escrita
// nunca se modifica y el registro continúa sobre restarts del proceso.
func NewWriter(file string) (*Writer, error) {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Close cierra el archivo subyacente. Idempotente: un Close posterior a un
// fd ya cerrado es no-op.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// write serializa ev como UNA línea JSON válida terminada en "\n" y la
// appendea bajo el mutex (líneas completas, sin intercalado, P4/I5).
// Best-effort en runtime (D10/I4/P16): devuelve el error; el request sigue
// su curso. Un fd cerrado devuelve error sin panic (P16).
func (w *Writer) write(ev any) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return os.ErrClosed
	}
	_, err = w.f.Write(line)
	return err
}

// Attempt registra un evento de intento (P5).
func (w *Writer) Attempt(ev AttemptEvent) error {
	return w.write(ev)
}

// Terminal registra un evento terminal (P6).
func (w *Writer) Terminal(ev TerminalEvent) error {
	return w.write(ev)
}
