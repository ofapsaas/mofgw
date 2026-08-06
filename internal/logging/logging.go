// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logging configura el logger estructurado del proxy (spec
// 005-002-logging, versión MVP): slog JSON a stdout con request_id
// propagado. Sin keys ni tokens en los logs (el saneamiento lo hace
// provider; acá solo se configura el formato).
// 005-005-verbose: agrega Build() con fanout por nivel y archivo.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// ctxKey es la clave privada del request_id en el context.
type ctxKey struct{}

// WithRequestID inyecta el request_id en el context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// RequestID extrae el request_id del context ("" si no hay).
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// New construye el logger JSON a stdout (nivel info por defecto).
func New(level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// WithRequest devuelve un logger con el request_id del context adjunto
// a cada entrada.
func WithRequest(logger *slog.Logger, ctx context.Context) *slog.Logger {
	if id := RequestID(ctx); id != "" {
		return logger.With("request_id", id)
	}
	return logger
}

// levelFanoutHandler enruta registros a múltiples handlers según umbrales
// de nivel. Cada destino tiene su propio nivel mínimo.
type levelFanoutHandler struct {
	stdoutHandler slog.Handler
	fileHandler   slog.Handler
	stdoutLevel   slog.Level
	fileLevel     slog.Level
}

func (h *levelFanoutHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.stdoutLevel || level >= h.fileLevel
}

func (h *levelFanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= h.stdoutLevel {
		if err := h.stdoutHandler.Handle(ctx, record); err != nil {
			return err
		}
	}
	if record.Level >= h.fileLevel {
		if err := h.fileHandler.Handle(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (h *levelFanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelFanoutHandler{
		stdoutHandler: h.stdoutHandler.WithAttrs(attrs),
		fileHandler:   h.fileHandler.WithAttrs(attrs),
		stdoutLevel:   h.stdoutLevel,
		fileLevel:     h.fileLevel,
	}
}

func (h *levelFanoutHandler) WithGroup(name string) slog.Handler {
	return &levelFanoutHandler{
		stdoutHandler: h.stdoutHandler.WithGroup(name),
		fileHandler:   h.fileHandler.WithGroup(name),
		stdoutLevel:   h.stdoutLevel,
		fileLevel:     h.fileLevel,
	}
}

// Build construye un logger con nivel y destinos. logFile != "" habilita
// log a archivo (append, fail-fast si no se puede abrir). stdoutWriter es
// inyectable (nil = os.Stdout). Devuelve el logger, un closer (no-op si no
// hay archivo) y error.
//
// Topología de destinos:
//
//	info + sin archivo → info a stdout
//	info + archivo      → info a stdout + archivo
//	debug + sin archivo → debug a stdout
//	debug + archivo     → debug SOLO al archivo; info a stdout + archivo
func Build(level slog.Level, logFile string, stdoutWriter io.Writer) (*slog.Logger, io.Closer, error) {
	if stdoutWriter == nil {
		stdoutWriter = os.Stdout
	}

	stdoutHandler := slog.NewJSONHandler(stdoutWriter, &slog.HandlerOptions{Level: level})

	if logFile == "" {
		return slog.New(stdoutHandler), nopCloser{}, nil
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nopCloser{}, err
	}

	fileHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})

	// Cuando level=debug y hay archivo: stdout solo recibe info+;
	// el archivo recibe todo (debug+).
	stdoutLevel := level
	if level == slog.LevelDebug {
		stdoutLevel = slog.LevelInfo
	}

	handler := &levelFanoutHandler{
		stdoutHandler: stdoutHandler,
		fileHandler:   fileHandler,
		stdoutLevel:   stdoutLevel,
		fileLevel:     level,
	}

	return slog.New(handler), f, nil
}

// nopCloser es un io.Closer que no hace nada.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }
