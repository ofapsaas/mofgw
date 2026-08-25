// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 017-001-auth-source-refactor (capa clientregistry).
// Paquete NUEVO internal/clientregistry: holder de snapshot inmutable del
// registro de clientes. Fijan C5 (P4 snapshot auth), C6 (P5 effects
// derivados de snapshot.Clients) y C7 (P6 snapshot inmutable + vista auth
// idéntica a auth.New) del spec.
//
// RED-especial de compilación (precedente 011-005/014-001): el paquete
// internal/clientregistry NO existe aún; estos tests referencian Source,
// Registry, Snapshot, New, Current, Authenticate, ClientByID → compile fail
// del paquete = RED válido. Los únicos símbolos ya existentes que tocan son
// auth.New/auth.Client/auth.HashKey y config.ClientConfig (001-007/008).

package clientregistry

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/config"
)

// writeClientsFile escribe un archivo de registro de clientes (clients_file)
// con el contenido dado y devuelve su path.
func writeClientsFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "clients.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// buildRegistry construye un Registry desde un archivo clients_file con un
// cliente válido (id "ofap-core", key "sk-017"). Devuelve registry + key.
func buildRegistry(t *testing.T, content string) (*Registry, string) {
	t.Helper()
	if content == "" {
		content = "- id: ofap-core\n  key_sha256: \"" + auth.HashKey("sk-017") + "\"\n"
	}
	src := &Source{Path: writeClientsFile(t, content)}
	reg, err := New(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return reg, "sk-017"
}

func bearerRequest(t *testing.T, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://mofgw/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// ---- C5 (P4): Snapshot autentica (key del registry → id; desconocida/ausente → error) ----

func Test017001_C5_SnapshotAuthenticate(t *testing.T) {
	reg, key := buildRegistry(t, "")
	snap := reg.Current()
	if snap == nil {
		t.Fatal("Current() devolvió nil")
	}

	if id, err := snap.Authenticate(bearerRequest(t, key)); err != nil || id != "ofap-core" {
		t.Fatalf("Authenticate(key válida) = %q, %v; want ofap-core, nil (P4)", id, err)
	}
	if _, err := snap.Authenticate(bearerRequest(t, "wrong-key")); err == nil {
		t.Fatal("Authenticate(key desconocida) debe fallar (P4)")
	}
	if _, err := snap.Authenticate(bearerRequest(t, "")); err == nil {
		t.Fatal("Authenticate(sin Bearer) debe fallar (P4)")
	}
}

// ---- C6 (P5): snapshot.Clients lleva TODO lo necesario para derivar efectos ----

func Test017001_C6_SnapshotClientsDerivanEfectos(t *testing.T) {
	content := `
- id: ofap-budget
  key_sha256: "` + auth.HashKey("sk-017-budget") + `"
  max_concurrent_requests: 5
  budget:
    tokens_max: 15
  embeddings:
    model: "emb"
`
	src := &Source{Path: writeClientsFile(t, content)}
	reg, err := New(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	snap := reg.Current()

	cc, ok := snap.ClientByID("ofap-budget")
	if !ok {
		t.Fatal("ClientByID(ofap-budget) no encontró el cliente cargado del archivo (P5/P6)")
	}
	// Efectos derivados (P5): los consumidores sacan de snapshot.Clients los
	// mismos campos que hoy toma main.go de cfg.Clients (budget → SetBudget,
	// embeddings.model → SetClientEmbeddingsModel, max_concurrent → keyed limiter).
	if cc.Budget == nil || cc.Budget.TokensMax != 15 {
		t.Fatalf("Budget derivado = %+v, want TokensMax=15 (P5)", cc.Budget)
	}
	if cc.Embeddings == nil || cc.Embeddings.Model != "emb" {
		t.Fatalf("Embeddings derivado = %+v, want Model=emb (P5)", cc.Embeddings)
	}
	if cc.MaxConcurrentRequests != 5 {
		t.Fatalf("MaxConcurrentRequests = %d, want 5 (aislamiento, P5)", cc.MaxConcurrentRequests)
	}

	// cliente ausente del registry → sin efectos (P5).
	if _, ok := snap.ClientByID("ghost"); ok {
		t.Fatal("ClientByID(ghost) = true; cliente ausente no debe existir (P5)")
	}
}

// ---- C7 (P6): Snapshot inmutable + ClientByID + vista auth idéntica a auth.New ----

func Test017001_C7_SnapshotInmutableYVistaAuth(t *testing.T) {
	reg, key := buildRegistry(t, "")
	snap := reg.Current()
	if snap == nil {
		t.Fatal("Current() devolvió nil")
	}

	// Inmutabilidad: Current() devuelve el MISMO snapshot publicado (el holder
	// no regenera ni muta el snapshot por consumo).
	if snap != reg.Current() {
		t.Fatal("Current() devolvió un snapshot distinto — el holder debe publicar el mismo snapshot inmutable (P6)")
	}

	// ClientByID cubre ID + KeySHA256 (vista mínima para auth.Client).
	cc, ok := snap.ClientByID("ofap-core")
	if !ok {
		t.Fatal("ClientByID(ofap-core) no encontró el cliente")
	}
	if cc.ID != "ofap-core" || cc.KeySHA256 != auth.HashKey(key) {
		t.Fatalf("ClientByID = %+v, want ofap-core/%s", cc, auth.HashKey(key))
	}

	// Vista auth funcionalmente idéntica a auth.New (P4/P6): el mismo request
	// da el mismo resultado (id y error) que el Authenticator legacy.
	authClients := make([]auth.Client, 0, len(snap.Clients))
	for _, c := range snap.Clients {
		authClients = append(authClients, auth.Client{ID: c.ID, KeySHA256: c.KeySHA256})
	}
	legacy := auth.New(authClients)

	valid := bearerRequest(t, key)
	idSnap, errSnap := snap.Authenticate(valid)
	idLegacy, errLegacy := legacy.Authenticate(valid)
	if idSnap != idLegacy || (errSnap != nil) != (errLegacy != nil) {
		t.Fatalf("vista auth (válida) difiere de auth.New: snap=(%q,%v) legacy=(%q,%v)", idSnap, errSnap, idLegacy, errLegacy)
	}

	unknown := bearerRequest(t, "zzz")
	idSnap2, errSnap2 := snap.Authenticate(unknown)
	idLegacy2, errLegacy2 := legacy.Authenticate(unknown)
	if idSnap2 != idLegacy2 || (errSnap2 != nil) != (errLegacy2 != nil) {
		t.Fatalf("vista auth (desconocida) difiere de auth.New: snap=(%q,%v) legacy=(%q,%v)", idSnap2, errSnap2, idLegacy2, errLegacy2)
	}
}

// ---- guard: el snapshot expone Clients completo (registro) para los consumidores ----

func Test017001_SnapshotExponeClientsCompleto(t *testing.T) {
	reg, _ := buildRegistry(t, "")
	snap := reg.Current()
	if len(snap.Clients) != 1 || snap.Clients[0].ID != "ofap-core" {
		t.Fatalf("snapshot.Clients = %+v, want el registro completo (P6)", snap.Clients)
	}
	for _, c := range snap.Clients {
		if c.ID == "" || c.KeySHA256 == "" {
			t.Fatalf("snapshot.Clients con campos vacíos: %+v", c)
		}
	}
}

// import de config mantenido: el contrato de este paquete devuelve
// []config.ClientConfig (Source.Load) y el owner lo requiere para la
// derivación de efectos (P5).
var _ = config.ClientConfig{}