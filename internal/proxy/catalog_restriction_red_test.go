// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de 013-003-config-wiring (P6/P7, C7/C8): el catálogo /v1/models
// lista los modelos subprocess (no-tools) y un request con tools a un modelo
// subprocess NO entrega tools al CLI. Hermético (D6): stub CLI + adapter
// claude real, cero red externa, sin binario claude.
//
// RED: contra el skeleton (handleModels solo type-assert *provider.Client),
// el provider subprocess NO aparece en /v1/models → los 3 tests de catálogo
// fallan por assertion. T_request_tools_not_reach_cli verifica el request
// path (P7/D4, ya garantizado por 013-002) y es guard de regresión.

package proxy_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
	"github.com/ofapsaas/mofgw/internal/subprocess"
	"github.com/ofapsaas/mofgw/internal/subprocess/claude"
)

// stubCLIRecord escribe argv + stdin del CLI a archivos y emite un
// stream-json de claude canned (para verificar el request path P7). Cero
// binario claude real.
func buildStubCLIRecord(t *testing.T) (script, argvFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	stdinFile = filepath.Join(dir, "stdin")
	script = filepath.Join(dir, "stub.sh")
	body := "#!/bin/sh\n" +
		`printf '%s\n' "$@" > "` + argvFile + `"` + "\n" +
		`cat > "` + stdinFile + `"` + "\n" +
		`printf '%s\n' '{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello from sub"}}'` + "\n" +
		`printf '%s\n' '{"type":"message_delta","delta":{"stop_reason":"end_turn"}}'` + "\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub cli: %v", err)
	}
	return script, argvFile, stdinFile
}

// buildWithSubprocess arma el harness con un provider HTTP (httptest) + un
// provider subprocess real (subprocess.NewProvider con claude.New sobre el
// stub CLI). Devuelve el harness + los archivos de captura del stub para P7.
func buildWithSubprocess(t *testing.T, httpModel, subModel string) (*harness, string, string) {
	t.Helper()
	u := upstreamOK(httpModel, "http")
	srv := httptest.NewServer(u.handler())
	t.Cleanup(srv.Close)
	httpClient := provider.NewClient("http1", srv.URL, "sk-upstream", []string{httpModel}, 4096, nil)

	script, argvFile, stdinFile := buildStubCLIRecord(t)
	backend := claude.New(script)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sub := subprocess.NewProvider("sub1", []string{subModel}, 4096, t.TempDir(), nil, backend, logger)

	providers := []provider.Provider{httpClient, sub}
	specs := []router.ProviderSpec{{Provider: httpClient}, {Provider: sub}}
	r := router.New(specs, 2, 0, 0, 30*1000*1000*1000, logger)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey("sk-test-1")}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, logger, nil, 10<<20, nil, nil, 0)
	h := &harness{srv: httptest.NewServer(s.Handler()), ups: []*upstream{u}, key: "sk-test-1", proxySrv: s}
	t.Cleanup(h.srv.Close)
	return h, argvFile, stdinFile
}

// TestT_CatalogListsSubprocessModels verifica P6 (C7): /v1/models con
// providers HTTP + subprocess incluye cada modelo subprocess, deduplicado.
func TestT_CatalogListsSubprocessModels(t *testing.T) {
	h, _, _ := buildWithSubprocess(t, "http-model", "sub-model")
	out := modelsBody(t, h)
	item := findModel(t, out, "sub-model")
	if item["id"] != "sub-model" {
		t.Fatalf("id = %v, want sub-model", item["id"])
	}
	// el modelo HTTP sigue listado (sin regresión)
	findModel(t, out, "http-model")
}

// TestT_CatalogSubprocessNoTools verifica P6 (C7): un modelo subprocess con
// model_metadata.supported_parameters=["reasoning"] aparece SIN "tools"
// (D3/D4: no-tools, declarativo; nunca se adivina).
func TestT_CatalogSubprocessNoTools(t *testing.T) {
	h, _, _ := buildWithSubprocess(t, "http-model", "sub-model")
	h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata{
		"sub-model": {SupportedParameters: []string{"reasoning"}},
	})
	item := findModel(t, modelsBody(t, h), "sub-model")
	sp, ok := item["supported_parameters"].([]any)
	if !ok {
		t.Fatalf("sin supported_parameters: %v", item)
	}
	for _, p := range sp {
		if p == "tools" {
			t.Fatalf("modelo subprocess lista tools: %v", item)
		}
	}
}

// TestT_CatalogSubprocessNoMetadata verifica P6 (C7): un modelo subprocess
// sin model_metadata aparece en formato mínimo (backward compatible).
func TestT_CatalogSubprocessNoMetadata(t *testing.T) {
	h, _, _ := buildWithSubprocess(t, "http-model", "sub-model")
	item := findModel(t, modelsBody(t, h), "sub-model")
	if _, ok := item["context_length"]; ok {
		t.Fatalf("sin metadata no debe haber context_length: %v", item)
	}
	if item["object"] != "model" {
		t.Fatalf("object = %v, want model", item["object"])
	}
}

// TestT_RequestToolsNotReachCli verifica P7 (C8): un request chat-completions
// con tools a un modelo subprocess completa con éxito y el stub CLI NO recibe
// ningún token tools ni el arreglo tools (ni argv ni el prompt traducido).
// La traducción limpia (D4 de 013-001/002) lo garantiza; este test lo
// verifica en el request path.
func TestT_RequestToolsNotReachCli(t *testing.T) {
	h, argvFile, stdinFile := buildWithSubprocess(t, "http-model", "sub-model")
	resp := h.chat(t, map[string]any{
		"model":    "sub-model",
		"messages": []map[string]any{{"role": "user", "content": "hi from agent"}},
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{
				"name": "search", "description": "TOOL-SECRET", "parameters": map[string]any{},
			}},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)

	// el CLI recibió el prompt traducido (solo el user content), sin tools
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin capture: %v", err)
	}
	if !strings.Contains(string(stdin), "hi from agent") {
		t.Fatalf("el CLI no recibió el prompt traducido: %q", stdin)
	}
	for _, bad := range []string{"search", "TOOL-SECRET", "tools"} {
		if strings.Contains(string(stdin), bad) {
			t.Fatalf("tools llegaron al stdin del CLI (%q): %q", bad, stdin)
		}
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv capture: %v", err)
	}
	for _, bad := range []string{"search", "tools"} {
		if strings.Contains(string(argv), bad) {
			t.Fatalf("tools llegaron al argv del CLI (%q): %q", bad, argv)
		}
	}
}
