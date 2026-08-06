// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/config"
)

// ---- SEC-001 P3: ReadHeaderTimeout + MaxHeaderBytes ----

// TestSEC001_HTTPServerTieneReadHeaderTimeout: el http.Server construido
// por la app debe setear ReadHeaderTimeout (mitiga Slowloris) y
// MaxHeaderBytes explícito. RED: main.go:185-190 construye el server
// inline sin estos campos. GREEN: se extrae a newHTTPServer(cfg,
// handler) con ReadHeaderTimeout: 5s y MaxHeaderBytes: 1<<20.
func TestSEC001_HTTPServerTieneReadHeaderTimeout(t *testing.T) {
	cfg := config.ServerConfig{
		Addr:         "127.0.0.1:0",
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 300 * time.Second,
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	srv := newHTTPServer(cfg, handler)
	if srv == nil {
		t.Fatal("newHTTPServer devolvió nil")
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s (SEC-001 P3)", srv.ReadHeaderTimeout)
	}
	if srv.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want %d (SEC-001 P3)", srv.MaxHeaderBytes, 1<<20)
	}
	if srv.ReadTimeout != cfg.ReadTimeout {
		t.Fatalf("ReadTimeout = %v, want %v (preservado del config)", srv.ReadTimeout, cfg.ReadTimeout)
	}
	if srv.WriteTimeout != cfg.WriteTimeout {
		t.Fatalf("WriteTimeout = %v, want %v (preservado del config)", srv.WriteTimeout, cfg.WriteTimeout)
	}
}

// TestSEC001_HTTPServerSirve: el server extraído responde (no es solo
// configuración muerta).
func TestSEC001_HTTPServerSirve(t *testing.T) {
	cfg := config.ServerConfig{Addr: "127.0.0.1:0"}
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) })
	srv := newHTTPServer(cfg, handler)

	// no escuchar el puerto real: verificar que el handler está cableado
	// vía httptest con el MISMO Server (el handler es el contrato).
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if !called {
		t.Fatal("handler no fue llamado (server no cableado)")
	}
	if resp.StatusCode != 204 {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}
