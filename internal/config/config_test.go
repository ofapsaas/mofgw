// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata mínima inline (evita depender de cwd en la suite)
const validYAML = `
server:
  addr: "127.0.0.1:3369"
  max_body_bytes: 10485760
  read_timeout: 120s
  write_timeout: 300s
fallback:
  max_retries: 2
  cooldown: 60s
  cooldown_jitter: 5s
  timeout: 120s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"
    models: ["deepseek-v4-flash", "glm-5.2"]
    max_tokens: 8192
  - id: provider-b
    base_url: "https://api.provider-b.example.com/compatible-mode/v1"
    api_key_env: "MOFGW_PROVIDER_B_KEY"
    models: ["qwen3.7-plus"]
    max_tokens: 16384
clients:
  - id: ofap-core
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	cfg, err := LoadFile(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:3369" {
		t.Errorf("Addr = %q", cfg.Server.Addr)
	}
	if cfg.Server.MaxBodyBytes != 10485760 {
		t.Errorf("MaxBodyBytes = %d", cfg.Server.MaxBodyBytes)
	}
	if cfg.Server.ReadTimeout != 120*time.Second {
		t.Errorf("ReadTimeout = %v", cfg.Server.ReadTimeout)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("Providers len = %d, want 2", len(cfg.Providers))
	}
	// el orden del archivo ES el orden de fallback
	if cfg.Providers[0].ID != "provider-a" || cfg.Providers[1].ID != "provider-b" {
		t.Errorf("orden providers = %s,%s", cfg.Providers[0].ID, cfg.Providers[1].ID)
	}
	if cfg.Providers[0].APIKey != "k1" || cfg.Providers[1].APIKey != "k2" {
		t.Error("api_key_env no resuelta")
	}
	if cfg.Providers[0].MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d", cfg.Providers[0].MaxTokens)
	}
	if len(cfg.Clients) != 1 || cfg.Clients[0].ID != "ofap-core" {
		t.Errorf("clients = %+v", cfg.Clients)
	}
	// defaults
	if cfg.Server.Addr == "" {
		t.Error("default addr no aplicado")
	}
	if cfg.Fallback.Cooldown != 60*time.Second {
		t.Errorf("Cooldown = %v", cfg.Fallback.Cooldown)
	}
}

func TestLoadMissingEnv(t *testing.T) {
	// sin setear MOFGW_PROVIDER_A_KEY
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	_, err := LoadFile(writeTemp(t, validYAML))
	if err == nil {
		t.Fatal("LoadFile() debería fallar con env var ausente")
	}
	if !contains(err.Error(), "MOFGW_PROVIDER_A_KEY") {
		t.Errorf("error debería nombrar la var ausente, got: %v", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	_, err := LoadFile(writeTemp(t, "server: [broken"))
	if err == nil {
		t.Fatal("YAML inválido debería fallar")
	}
}

func TestLoadMissingProviderFields(t *testing.T) {
	cases := []struct {
		name, yaml string
	}{
		{"sin id", `
providers:
  - base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
`},
		{"sin base_url", `
providers:
  - id: p
    api_key_env: "K"
    models: ["m"]
`},
		{"sin api_key_env", `
providers:
  - id: p
    base_url: "https://x/v1"
    models: ["m"]
`},
		{"sin models", `
providers:
  - id: p
    base_url: "https://x/v1"
    api_key_env: "K"
`},
		{"models vacio", `
providers:
  - id: p
    base_url: "https://x/v1"
    api_key_env: "K"
    models: []
`},
		{"id duplicado", `
providers:
  - id: p
    base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
  - id: p
    base_url: "https://y/v1"
    api_key_env: "K"
    models: ["m"]
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("K", "key")
			if _, err := LoadFile(writeTemp(t, tc.yaml)); err == nil {
				t.Error("config inválido debería fallar")
			}
		})
	}
}

func TestLoadInvalidClientHash(t *testing.T) {
	// hash no hex
	bad := `
providers:
  - id: p
    base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
clients:
  - id: c
    key_sha256: "zz"
`
	t.Setenv("K", "key")
	if _, err := LoadFile(writeTemp(t, bad)); err == nil {
		t.Error("hash de client inválido debería fallar")
	}
}

func TestLoadEmptyProviders(t *testing.T) {
	_, err := LoadFile(writeTemp(t, "server: {}\nfallback: {}\n"))
	if err == nil {
		t.Fatal("sin providers debería fallar")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---- 010-001-catalogo-fiel: P1 — config extensible y backward compatible ----

// metadata010001YAML: config con las claves NUEVAS (supported_parameters,
// modality) además de las 007-001. El parser actual ignora claves
// desconocidas → carga sin error y las claves 007-001 se leen igual (P1).
const metadata010001YAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: provider-a
    base_url: "http://localhost:1/v1"
    api_key_env: MOFGW_010001_TEST_KEY
    models: ["m"]
clients:
  - id: agent-main
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
model_metadata:
  m:
    context_window: 1000000
    max_output: 384000
    thinking: ["low", "high", "max"]
    thinking_default: "low"
    supported_parameters: ["tools", "reasoning"]
    modality: "text+image+video->text"
`

// Test010001_P1_CargaConClavesNuevas: YAML con las claves nuevas carga sin
// error (P1) y las claves 007-001 se siguen leyendo igual. Guard verde hoy
// (claves ignoradas); el acceso tipado a los campos nuevos queda cubierto
// indirectamente por los E2E RED (P3/P4/P6) que manejan la metadata desde
// este mismo loader.
func Test010001_P1_CargaConClavesNuevas(t *testing.T) {
	t.Setenv("MOFGW_010001_TEST_KEY", "k1")
	cfg, err := LoadFile(writeTemp(t, metadata010001YAML))
	if err != nil {
		t.Fatalf("LoadFile con claves nuevas: %v", err)
	}
	md, ok := cfg.ModelMetadata["m"]
	if !ok {
		t.Fatal("metadata de m no cargada")
	}
	if md.ContextWindow != 1000000 {
		t.Fatalf("ContextWindow = %d, want 1000000", md.ContextWindow)
	}
	if md.MaxOutput != 384000 {
		t.Fatalf("MaxOutput = %d, want 384000", md.MaxOutput)
	}
	if md.ThinkingDefault != "low" {
		t.Fatalf("ThinkingDefault = %q, want low", md.ThinkingDefault)
	}
}

// Test010001_P1_BackwardCompat: config 007-001 (sin claves nuevas) carga
// exactamente igual que antes (P1 — sin validaciones nuevas, sin romper
// configs existentes).
func Test010001_P1_BackwardCompat(t *testing.T) {
	t.Setenv("MOFGW_TEST_A", "k1")
	cfg, err := LoadFile(writeTemp(t, metadataYAML)) // 007-001, sin claves nuevas
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	md, ok := cfg.ModelMetadata["deepseek-v4-flash"]
	if !ok {
		t.Fatal("metadata 007-001 no cargada")
	}
	if md.ContextWindow != 1000000 || md.MaxOutput != 384000 || md.ThinkingDefault != "high" {
		t.Fatalf("metadata 007-001 alterada: %+v", md)
	}
}

// ---- 029-001 (TECHDEBT #29): first_token_timeout ----

// validYAML029: config mínima con env keys seteables para los tests 029.
const validYAML029 = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"
    models: ["m"]
clients:
  - id: ofap-core
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`

func Test029001_DefaultFirstTokenTimeout(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	cfg, err := Parse([]byte(validYAML029))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Fallback.FirstTokenTimeout != 90*time.Second {
		t.Fatalf("first_token_timeout default = %s, want 90s", cfg.Fallback.FirstTokenTimeout)
	}
}

func Test029001_ExplicitFirstTokenTimeout(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	cfg, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
  first_token_timeout: 75s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"
    models: ["m"]
clients:
  - id: ofap-core
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Fallback.FirstTokenTimeout != 75*time.Second {
		t.Fatalf("first_token_timeout = %s, want 75s", cfg.Fallback.FirstTokenTimeout)
	}
}

func Test029001_NegativeFirstTokenTimeoutRejected(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	_, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
  first_token_timeout: -5s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"
    models: ["m"]
clients:
  - id: ofap-core
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`))
	if err == nil {
		t.Fatal("first_token_timeout negativo debería fallar")
	}
}

func Test029001_ProviderOverride(t *testing.T) {
	t.Setenv("MOFGW_PROVIDER_A_KEY", "k1")
	t.Setenv("MOFGW_PROVIDER_B_KEY", "k2")
	cfg, err := Parse([]byte(`
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"
    models: ["m"]
  - id: provider-b
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_B_KEY"
    models: ["m"]
    first_token_timeout: 45s
clients:
  - id: ofap-core
    key_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var found *time.Duration
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "provider-b" {
			found = cfg.Providers[i].FirstTokenTimeout
		}
	}
	if found == nil || *found != 45*time.Second {
		t.Fatalf("provider override = %v, want 45s", found)
	}
}

// ---- 013-003-config-wiring: P1-P4 (config type:subprocess) ----

// subprocessYAML: config mínima con un provider type:subprocess SIN
// base_url/api_key_env (P1/P2). Clients fija la allowlist (D5).
const subprocessYAML = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: sub
    type: subprocess
    backend: claude
    command: "claude"
    session_dir: "/tmp/mofgw-sessions"
    models: ["claude-sonnet-4"]
    max_tokens: 8192
    backend_flags: ["--dangerously-skip-permissions"]
    clients: ["me"]
`

// TestT_ConfigSubprocessValid verifica P1 (C2): un type:subprocess válido
// parsea y valida sin error, con backend obligatorio y los campos
// opcionales/defaults leídos.
func TestT_ConfigSubprocessValid(t *testing.T) {
	cfg, err := LoadFile(writeTemp(t, subprocessYAML))
	if err != nil {
		t.Fatalf("subprocess válido: %v", err)
	}
	p := cfg.Providers[0]
	if p.Type != "subprocess" || p.Backend != "claude" {
		t.Fatalf("type/backend = %q/%q, want subprocess/claude", p.Type, p.Backend)
	}
	if p.Command != "claude" || p.SessionDir != "/tmp/mofgw-sessions" {
		t.Fatalf("command/session_dir = %q/%q", p.Command, p.SessionDir)
	}
	if len(p.BackendFlags) != 1 || p.BackendFlags[0] != "--dangerously-skip-permissions" {
		t.Fatalf("backend_flags = %v", p.BackendFlags)
	}
	if len(p.Clients) != 1 || p.Clients[0] != "me" {
		t.Fatalf("clients = %v", p.Clients)
	}
	if p.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", p.MaxTokens)
	}
}

// TestT_ConfigSubprocessNoKey verifica P2 (C3): un type:subprocess sin
// base_url ni api_key_env es válido (claude usa OAuth/suscripción).
func TestT_ConfigSubprocessNoKey(t *testing.T) {
	cfg, err := LoadFile(writeTemp(t, subprocessYAML))
	if err != nil {
		t.Fatalf("subprocess sin key/base_url: %v", err)
	}
	if cfg.Providers[0].BaseURL != "" || cfg.Providers[0].APIKeyEnv != "" {
		t.Fatalf("subprocess no debe requerir base_url/api_key_env: %+v", cfg.Providers[0])
	}
}

// TestT_ConfigUnknownTypeError verifica P3 (C4): type desconocido → error
// descriptivo ("unknown provider type"), sin arrancar. Se da base_url y
// api_key_env para que el único motivo de fallo sea el type.
func TestT_ConfigUnknownTypeError(t *testing.T) {
	y := `
providers:
  - id: p
    type: bogus
    base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
`
	t.Setenv("K", "k")
	_, err := LoadFile(writeTemp(t, y))
	if err == nil {
		t.Fatal("type desconocido debería fallar")
	}
	if !strings.Contains(err.Error(), "unknown provider type") {
		t.Fatalf("error debería ser descriptivo, got: %v", err)
	}
}

// TestT_ConfigUnknownBackendError verifica P3 (C4): backend != "claude"
// → error descriptivo al cargar (owner 12 Ago: validación en tiempo de
// carga, alineado con "de carga" de P3/C4).
func TestT_ConfigUnknownBackendError(t *testing.T) {
	y := `
providers:
  - id: p
    type: subprocess
    backend: gemini
    base_url: "https://x/v1"
    api_key_env: "K"
    models: ["m"]
`
	t.Setenv("K", "k")
	_, err := LoadFile(writeTemp(t, y))
	if err == nil {
		t.Fatal("backend != claude debería fallar")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error debería nombrar backend desconocido, got: %v", err)
	}
}

// TestT_ConfigHTTPBackwardCompat verifica P4 (C5): type:http explícito +
// base_url/api_key_env carga y valida EXACTAMENTE como el tipo vacío.
func TestT_ConfigHTTPBackwardCompat(t *testing.T) {
	y := `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
providers:
  - id: p
    type: http
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_P4_KEY"
    models: ["m"]
    max_tokens: 4096
`
	t.Setenv("MOFGW_P4_KEY", "k1")
	cfg, err := LoadFile(writeTemp(t, y))
	if err != nil {
		t.Fatalf("type:http debería cargar igual que sin type: %v", err)
	}
	if cfg.Providers[0].BaseURL != "https://api.example.com/v1" {
		t.Fatalf("base_url = %q", cfg.Providers[0].BaseURL)
	}
	if cfg.Providers[0].APIKey != "k1" {
		t.Fatalf("api_key no resuelta")
	}
}

// ---- 013-004-resilience-ops: P7 — defaults de config fuera de validate() ----

// T_config_defaults_in_parse (P7, C8): al parsear un provider type:subprocess
// SIN command ni session_dir, el Config resultante lee Command=="claude" y
// SessionDir==DefaultSessionDir (defaults aplicados en Parse/factory).
// validate() no produce esa mutación como efecto secundario. RED: el skeleton
// quitó la mutación de validate() sin cablear los defaults en Parse() ⇒
// Command/SessionDir quedan vacíos ⇒ aserción falla.
func TestT_ConfigDefaultsInParse(t *testing.T) {
	raw := `
providers:
  - id: sub
    type: subprocess
    backend: claude
    models: ["m"]
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse de subprocess sin command/session_dir: %v", err)
	}
	p := cfg.Providers[0]
	if p.Command != "claude" {
		t.Fatalf("Command = %q, want %q (default aplicado en Parse)", p.Command, "claude") // RED
	}
	if p.SessionDir != DefaultSessionDir {
		t.Fatalf("SessionDir = %q, want %q", p.SessionDir, DefaultSessionDir) // RED
	}
}
