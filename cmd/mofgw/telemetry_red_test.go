// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-000-request-telemetry — wiring de main (R8)
// y muestreo (P9/C9).
//
// buildTelemetryFromConfig(cfg *config.Config) (*slog.Logger, io.Closer, error)
// — helper NUEVO en main.go (patrón newHTTPServer de SEC-001), definido por
// el test-writer contra R8 del audit:
//   - cfg.Telemetry.Enabled == false → (nil, nil, nil) sin abrir archivo.
//   - cfg.Telemetry.Enabled == true && File != "" → (logger, closer, nil).
//   - cfg.Telemetry.Enabled == true && File == "" → error.
//
// El implementer DEBE respetar esta firma.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// test_postcondition_2_MainWiringTelemetry (R8): las tres ramas de
// buildTelemetryFromConfig + no-creación de archivo con enabled=false
// (C1) e independencia de --verbose (C11, I6).
func TestPostcondition2_MainWiringTelemetry(t *testing.T) {
	// enabled=false → (nil, nil, nil), sin archivo (C1/C11)
	noFile := filepath.Join(t.TempDir(), "telemetry.jsonl")
	cfgOff := &config.Config{Telemetry: config.TelemetryConfig{Enabled: false, SampleRate: 1, File: noFile}}
	tl, closer, err := buildTelemetryFromConfig(cfgOff)
	if err != nil {
		t.Fatalf("disabled: %v", err)
	}
	if tl != nil || closer != nil {
		t.Fatalf("disabled: telemetryLogger/closer = %v/%v, want nil/nil", tl, closer)
	}
	if _, statErr := os.Stat(noFile); !os.IsNotExist(statErr) {
		t.Fatalf("enabled=false NO debe crear telemetry.jsonl (C1/C11): %v", statErr)
	}

	// enabled=true + file → logger + closer, archivo creado y escribible
	teleFile := filepath.Join(t.TempDir(), "telemetry.jsonl")
	cfgOn := &config.Config{Telemetry: config.TelemetryConfig{Enabled: true, SampleRate: 1, File: teleFile}}
	tlOn, closerOn, err := buildTelemetryFromConfig(cfgOn)
	if err != nil {
		t.Fatalf("enabled: %v", err)
	}
	if tlOn == nil || closerOn == nil {
		t.Fatal("enabled: telemetryLogger/closer no deben ser nil")
	}
	tlOn.Info("smoke")
	if err := closerOn.Close(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	b, readErr := os.ReadFile(teleFile)
	if readErr != nil || !strings.Contains(string(b), "smoke") {
		t.Fatalf("telemetry.jsonl no escrito: %v %q", readErr, b)
	}

	// enabled=true + file="" → error (C10, I5)
	cfgBad := &config.Config{Telemetry: config.TelemetryConfig{Enabled: true, SampleRate: 1, File: ""}}
	if _, _, err := buildTelemetryFromConfig(cfgBad); err == nil {
		t.Fatal("enabled=true con file vacío debe fallar (C10)")
	}
}

// test_postcondition_9_MuestreoBanda (C9, R1 enmendado): con
// sample_rate=0.5 y 100 requests → entre 40 y 60 emiten evento.
//
// DESVÍO del AUDIT §7: este test vive acá (cmd), no en internal/proxy —
// proxy.New no expone sample_rate (P2); la única vía pública con la tasa
// es buildTelemetryFromConfig(cfg). Justificación en el reporte RED §4.
func TestPostcondition9_MuestreoBanda(t *testing.T) {
	teleFile := filepath.Join(t.TempDir(), "telemetry.jsonl")
	cfg := &config.Config{Telemetry: config.TelemetryConfig{Enabled: true, SampleRate: 0.5, File: teleFile}}
	tl, closer, err := buildTelemetryFromConfig(cfg)
	if err != nil {
		t.Fatalf("buildTelemetryFromConfig: %v", err)
	}
	defer func() { _ = closer.Close() }()

	// harness proxy mínimo con telemetría (mismo patrón que internal/proxy)
	ups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-up","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`)
	}))
	defer ups.Close()
	cl := provider.NewClient("up1", ups.URL, "sk-upstream", []string{"m"}, 4096, nil)
	providers := []provider.Provider{cl}
	specs := []router.ProviderSpec{{Provider: cl}}
	r := router.New(specs, 2, 0, 0, 30*time.Second, nil)
	authz := auth.New([]auth.Client{{ID: "test", KeySHA256: auth.HashKey("sk-test-1")}})
	m := metrics.New()
	s := proxy.New(r, providers, authz, m, nil, tl, 10<<20, nil, nil, 0)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for i := 0; i < 100; i++ {
		raw, _ := json.Marshal(map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer sk-test-1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
	}

	n := waitStableLineCount(t, teleFile, 2*time.Second)
	if n < 40 || n > 60 {
		t.Fatalf("muestreo: %d eventos de 100 con sample_rate=0.5, want 40..60 (C9, R1)", n)
	}
}

func countJSONLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// waitStableLineCount espera a que el conteo de líneas se estabilice
// (flush async del archivo) o agote el timeout.
func waitStableLineCount(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := countJSONLines(t, path)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		cur := countJSONLines(t, path)
		if cur == last {
			return cur
		}
		last = cur
	}
	return countJSONLines(t, path)
}
