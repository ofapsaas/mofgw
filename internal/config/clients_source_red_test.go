// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 017-001-auth-source-refactor (capa config).
// El registro de clientes sale del bloque inline `clients:` del config.yaml
// a un archivo dedicado referenciado por `clients_file:` (SINGLE source).
// Estos tests fijan C1-C4 y C9 del spec (P1/P2/P3/P8) y el contrato de
// helpers config.LoadClientsFile / config.ValidateClients (extraídos de
// config.go:600-619 en GREEN).
//
// RED-especial de compilación (precedente 011-005/014-001/015-001): estos
// tests referencian símbolos que el implementer agrega en GREEN —
// Config.ClientsFile, LoadClientsFile, ValidateClients. Hasta entonces el
// paquete config NO compila (build fail = RED válido). En cuanto el paquete
// compile (GREEN), C1/C4 (que solo usan LoadFile) validan su error por
// aserción; C2/C3/C9 validan los nuevos helpers.

package config

import (
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/auth"
)

// ---- C1 (P1): inline `clients:` sin `clients_file` → error de migración ----

func Test017001_C1_InlineSinClientsFileErrorMigracion(t *testing.T) {
	raw := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_KEY
    models: ["m"]
clients:
  - id: agent-main
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`
	t.Setenv("MOFGW_KEY", "k1")
	_, err := LoadFile(writeTemp(t, raw))
	if err == nil {
		t.Fatal("clients inline sin clients_file debe fallar (P1)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "clients") {
		t.Fatalf("el error debe mencionar la migración/eliminación de clients inline, got: %v", err)
	}
}

// ---- C2 (P2): clients_file con registro válido → se carga y valida ----

func Test017001_C2_ClientsFileCargaYValida(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")

	clientsPath := writeClientsFileYAML(t, validClientsYAML)
	cfg, err := LoadFile(writeTemp(t, validYAML+"clients_file: "+clientsPath+"\n"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	// RED 017-001: Config.ClientsFile no existe aún (compile fail en RED),
	// y LoadClientsFile/ValidateClients tampoco. Fija el contrato:
	//   - Config.ClientsFile string yaml:"clients_file"
	//   - config.LoadClientsFile(path) ([]ClientConfig, error)
	//   - config.ValidateClients([]ClientConfig) error
	if cfg.ClientsFile != clientsPath {
		t.Fatalf("ClientsFile = %q, want %q", cfg.ClientsFile, clientsPath)
	}
	if len(cfg.Clients) != 1 || cfg.Clients[0].ID != "ofap-core" {
		t.Fatalf("Clients = %+v, want [ofap-core] poblado desde clients_file (P2)", cfg.Clients)
	}

	clients, err := LoadClientsFile(clientsPath)
	if err != nil {
		t.Fatalf("LoadClientsFile: %v", err)
	}
	if len(clients) != 1 || clients[0].ID != "ofap-core" || clients[0].KeySHA256 == "" {
		t.Fatalf("LoadClientsFile = %+v, want [ofap-core]", clients)
	}
	if err := ValidateClients(clients); err != nil {
		t.Fatalf("ValidateClients: %v (registro válido no debe fallar)", err)
	}
}

// ---- C3 (P2): clients_file ausente/inválido → error fail-fast ----

func Test017001_C3_ClientsFileFailFast(t *testing.T) {
	t.Setenv("MOFGW_KEY", "k1")

	t.Run("archivo_ausente", func(t *testing.T) {
		if _, err := LoadClientsFile("/no/such/clients.yaml"); err == nil {
			t.Fatal("clients_file inexistente debe fallar (P2)")
		}
	})

	t.Run("hash_malformado", func(t *testing.T) {
		p := writeClientsFileYAML(t, "- id: c\n  key_sha256: \"zz\"\n")
		if _, err := LoadClientsFile(p); err == nil {
			t.Fatal("key_sha256 no hex debe fallar la validación (P2)")
		}
	})

	t.Run("id_vacio", func(t *testing.T) {
		p := writeClientsFileYAML(t, "- key_sha256: \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n")
		if _, err := LoadClientsFile(p); err == nil {
			t.Fatal("id vacío debe fallar la validación (P2)")
		}
	})

	t.Run("id_duplicado", func(t *testing.T) {
		p := writeClientsFileYAML(t, `
- id: c
  key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
- id: c
  key_sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
`)
		if _, err := LoadClientsFile(p); err == nil {
			t.Fatal("ids duplicados debe fallar la validación (P2)")
		}
	})

	t.Run("concurrencia_negativa", func(t *testing.T) {
		p := writeClientsFileYAML(t, "- id: c\n  key_sha256: \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n  max_concurrent_requests: -1\n")
		if _, err := LoadClientsFile(p); err == nil {
			t.Fatal("max_concurrent_requests negativo debe fallar la validación (P2)")
		}
	})
}

// ---- C4 (P3): inline `clients:` + `clients_file` juntos → error (anti dual-source) ----

func Test017001_C4_InlineMasClientsFileDualSource(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	clientsPath := writeClientsFileYAML(t, validClientsYAML)
	raw := `
server:
  addr: "127.0.0.1:3369"
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_PROVIDER_A_KEY
    models: ["m"]
clients:
  - id: inline-one
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
clients_file: ` + clientsPath + "\n"

	if _, err := LoadFile(writeTemp(t, raw)); err == nil {
		t.Fatal("inline clients + clients_file juntos debe fallar (P3, anti dual-source)")
	}
}

// ---- C9 (P8): LoadClientsFile acepta el output de auth.HashKey ----

func Test017001_C9_LoadClientsFileAceptaHashKey(t *testing.T) {
	const key = "sk-hash-key-017"
	p := writeClientsFileYAML(t, "- id: c\n  key_sha256: \""+auth.HashKey(key)+"\"\n")
	clients, err := LoadClientsFile(p)
	if err != nil {
		t.Fatalf("LoadClientsFile con hash de auth.HashKey debe ser válido (P8): %v", err)
	}
	if len(clients) != 1 || clients[0].ID != "c" {
		t.Fatalf("clients = %+v, want [c]", clients)
	}
}