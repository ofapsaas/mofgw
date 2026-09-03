// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package build expone la identidad de build de mofgw (feature
// 018-001-opencode-session-header, D5): la versión y el User-Agent propio
// que TODO request HTTP upstream debe enviar (P5). Un paquete mínimo sin
// dependencias para que cualquier cliente HTTP del binario (provider,
// embeddings) lo importe sin ciclos.
//
// Version es var (no const) para ser overridable por ldflags:
//
//	-ldflags "-X github.com/ofapsaas/mofgw/internal/build.Version=v9.9.9"
//
// UserAgent se deriva de Version en la inicialización del paquete, así que
// un override por ldflags se propaga automáticamente al User-Agent.
package build

// Version es la versión de mofgw (D5). No hay constante previa en el
// proyecto; 0.1.0 es el valor asertado por los tests (P5).
var Version = "0.1.0"

// UserAgent es el User-Agent propio de mofgw: todo request HTTP upstream
// (chat/completions, GET models, embeddings) lo envía (P5). Prohibido
// impersonar el CLI oficial (I4): nunca "opencode-cli".
var UserAgent = "mofgw/" + Version
