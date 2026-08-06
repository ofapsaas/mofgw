// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
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
