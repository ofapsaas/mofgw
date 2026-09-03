// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package config carga y valida config.yaml para mofgw (feature 001-002).
//
// Precedencia de ubicación: el path explícito (flag -config) gana; si no,
// ~/.config/mofgw/config.yaml (nivel home); si no existe, /etc/mofgw/config.yaml
// (nivel sistema). Las API keys NUNCA viven en el YAML: solo referencias
// api_key_env a variables de entorno, resueltas al cargar. Un config
// inválido falla al arranque con error descriptivo.
package config

import (
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerConfig: parámetros del listener HTTP.
type ServerConfig struct {
	Addr         string        `yaml:"addr"`
	MaxBodyBytes int64         `yaml:"max_body_bytes"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`

	// Concurrencia global (004-001-concurrencia):
	// MaxConcurrentRequests 0 = ilimitado. Cuando el semáforo global está
	// saturado, el request espera hasta BackpressureTimeout por un slot;
	// si expira → 429 rate_limit_exceeded.
	MaxConcurrentRequests int           `yaml:"max_concurrent_requests"`
	BackpressureTimeout   time.Duration `yaml:"backpressure_timeout"`

	// Tope de retención de sesiones en /v1/usage (008-003 P4).
	// 0 = default (100). Setter cableado en main.go desde config.
	MaxSessionsRetained int `yaml:"max_sessions_retained"`

	// Persistencia del registro de accounting (TECHDEBT #13).
	// StateFile: ruta del snapshot JSON (usage/cost/sesiones). Vacío =
	// deshabilitado (todo en memoria, backward compatible). Al arrancar,
	// si el archivo existe y es válido se restaura; un archivo corrupto
	// NO impide el arranque (se loguea y se sigue con estado limpio).
	StateFile string `yaml:"state_file"`
	// StateSaveInterval: cada cuánto se persiste el estado en caliente.
	// 0 = solo al shutdown limpio. Con StateFile vacío se ignora.
	StateSaveInterval time.Duration `yaml:"state_save_interval"`
}

// RetryConfig: reintentos sobre el MISMO provider (002-001-retry).
// MaxAttempts cuenta intentos totales (default 2 = 1 intento + 1 reintento;
// 0 = sin reintentos). Backoff: base * 2^n con jitter ±20%, tope BackoffMax.
type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BackoffBase time.Duration `yaml:"backoff_base"`
	BackoffMax  time.Duration `yaml:"backoff_max"`
}

// HealthConfig: sondeo proactivo de disponibilidad (002-002-health).
type HealthConfig struct {
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

// DegradationConfig: política de degradación controlada (002-004-degradacion).
// MaxRequests 0 = ilimitado (no corta requests en degradación).
type DegradationConfig struct {
	MaxRequests   int           `yaml:"max_requests"`
	CooldownGrace time.Duration `yaml:"cooldown_grace"`
}

// StickyRoutingConfig: afinidad de provider por sesión/cliente (009-002 P1).
// Enabled=false (default) → el proxy usa la cadena en orden de config
// (cero cambio de comportamiento). El scope (sesión vs cliente) NO es
// configurable: lo decide la fuente (X-Session-Id, ADR-001 — P2).
type StickyRoutingConfig struct {
	Enabled bool `yaml:"enabled"` // default false
}

// FallbackConfig: política global de la cadena de fallback.
type FallbackConfig struct {
	MaxRetries     int           `yaml:"max_retries"`
	Cooldown       time.Duration `yaml:"cooldown"`
	CooldownJitter time.Duration `yaml:"cooldown_jitter"`
	Timeout        time.Duration `yaml:"timeout"`
	// InterAttemptDelay: 015-001 — retardo entre intentos de la cadena ante
	// fallos TRANSITORIOS del endpoint compartido (SPOF de cuentas del
	// mismo proveedor). Aplica solo cuando el siguiente candidato comparte
	// base_url con el intento que falló; 429 (cuota) NUNCA añade delay.
	// 0 (default) = off (cero regresión). Se valida >= 0 en Parse.
	InterAttemptDelay time.Duration `yaml:"inter_attempt_delay"`
	// FirstTokenTimeout: tope de TTFB (time-to-first-token) por intento de
	// STREAM (TECHDEBT #29, incidente Obs-Radar 12 Ago 2026: un upstream
	// colgado quemó 11+ min sin emitir primer token). Semántica de 3
	// estados: ausente (0 en YAML) = sin tope de primer token (el intento
	// puede esperar el primer byte indefinidamente, gobierna Timeout);
	// N positivo = aborta el intento si no hay primer token en N y rota al
	// siguiente provider de la cadena. NO aplica a requests no-stream
	// (Complete): ahí el tope sigue siendo Timeout (la respuesta completa
	// es el "primer byte"). Default 90s.
	FirstTokenTimeout time.Duration       `yaml:"first_token_timeout"`
	Retry             RetryConfig         `yaml:"retry"`
	Health            HealthConfig        `yaml:"health"`
	Degradation       DegradationConfig   `yaml:"degradation"`
	StickyRouting     StickyRoutingConfig `yaml:"sticky_routing"` // 009-002
}

// ProviderConfig: un upstream OpenAI-compatible.
type ProviderConfig struct {
	ID        string   `yaml:"id"`
	BaseURL   string   `yaml:"base_url"`
	APIKeyEnv string   `yaml:"api_key_env"`
	Models    []string `yaml:"models"`
	MaxTokens int64    `yaml:"max_tokens"`

	// Cooldown/Timeout con semántica de 3 estados:
	// nil = ausente (usa el global), 0 explícito = sin límite, N = N.
	Cooldown *time.Duration `yaml:"cooldown"`
	Timeout  *time.Duration `yaml:"timeout"`

	// FirstTokenTimeout: override per-provider del tope TTFB de streaming
	// (TECHDEBT #29). Misma semántica de 3 estados: nil = usa el global
	// fallback.first_token_timeout, 0 explícito = sin tope de primer
	// token para este provider, N = N.
	FirstTokenTimeout *time.Duration `yaml:"first_token_timeout"`

	// Overrides per-provider de 002-001/002-002 (nil = usar el global).
	Retry  *RetryConfig  `yaml:"retry"`
	Health *HealthConfig `yaml:"health"`

	// ThinkingPath: path declarativo del provider para la inyección del
	// thinking_default prescriptivo (010-002-request-path-fiel). Valores:
	// "" (default, sin inyección) | "zen" | "bailian". Un path no
	// reconocido degrada a sin inyección (fallback rule §5.4: no se
	// adivina). Consistente con I2: valores declarativos, nunca inferidos
	// por patrón de ID (los IDs reales son acct1, go-1/2, qwen...).
	ThinkingPath string `yaml:"thinking_path"`

	// OpenCodeSession: knob declarativo (018-001 D3). true → mofgw inyecta
	// el header x-opencode-session en los requests upstream de ESTE
	// provider (precedencia X-Session-Id entrante → fallback client_id,
	// P1-P4; valor sanitizado [A-Za-z0-9._-] y clampeado a 128, P3).
	// Default false (zero value, I7): configs existentes cargan idéntico.
	// Declarativo, JAMÁS inferido del ID/base_url/modelo (I1); el header
	// solo sale hacia providers con knob activo (I2: no leak).
	OpenCodeSession bool `yaml:"opencode_session"`

	// APIKey se puebla al resolver APIKeyEnv (nunca viene del YAML).
	APIKey string `yaml:"-"`

	// 013-003-config-wiring (D1): discriminador de tipo + campos del
	// provider subprocess. Type zero-value "" (o "http") = comportamiento
	// HTTP actual (backward compatible, P4); "subprocess" requiere Backend
	// y NO exige base_url/api_key_env (P2).
	Type string `yaml:"type"`
	// Backend: backend del motor subprocess ("claude" hoy; gemini/codex a
	// futuro). Obligatorio cuando Type=="subprocess"; != "claude" → error
	// descriptivo al cargar (owner 12 Ago, C4/P3).
	Backend string `yaml:"backend"`
	// Command: binario del CLI (default "claude").
	Command string `yaml:"command"`
	// SessionDir: base de sesiones del motor (default DefaultSessionDir).
	SessionDir string `yaml:"session_dir"`
	// BackendFlags: flags opacos que pasan tal cual al argv del CLI (I2).
	BackendFlags []string `yaml:"backend_flags"`
	// Clients: allowlist de clientID con acceso al provider subprocess
	// (D5, mapeada a ProviderSpec.AllowedClients). Vacío = sin restricción
	// (P9, warning de arranque).
	Clients []string `yaml:"clients"`
}

// DefaultSessionDir es el directorio base de sesiones del provider
// subprocess cuando session_dir no está configurado (013-003 P1).
const DefaultSessionDir = "~/.config/mofgw/sessions"

// BudgetConfig es el límite de consumo de un cliente (008-002-budget):
// cost_usd_max y/o tokens_max opcionales (0 = sin límite para esa
// dimensión). Sin budget configurado → cliente sin límite.
type BudgetConfig struct {
	CostUSDMax float64       `yaml:"cost_usd_max"`
	TokensMax  int64         `yaml:"tokens_max"`
	Window     time.Duration `yaml:"window"` // documentado como aproximación (ventana simple desde start)
}

// ClientConfig: un cliente autorizado (001-007-auth).
type ClientConfig struct {
	ID        string `yaml:"id"`
	KeySHA256 string `yaml:"key_sha256"`

	// Aislamiento por cliente (004-002-aislamiento):
	// MaxConcurrentRequests 0 = ilimitado. Los requests que exceden el
	// budget del cliente reciben 429 inmediato (fast-fail, sin espera).
	MaxConcurrentRequests int `yaml:"max_concurrent_requests"`
	// MaxConcurrentPerAgent 0 = ilimitado. Límite por (cliente, X-Agent-Id).
	MaxConcurrentPerAgent int `yaml:"max_concurrent_per_agent"`

	// Budget: límite de consumo (008-002). nil = sin límite.
	Budget *BudgetConfig `yaml:"budget"`

	// Embeddings: modelo de embeddings forzado por cliente (011-006 P3).
	// nil = cliente sin modelo → 400 "no embeddings model configured" (P4).
	Embeddings *ClientEmbeddingsConfig `yaml:"embeddings"`
}

// PricingConfig son los precios por millón de tokens de un modelo
// (006-002-costos). Cero = sin precio para esa componente.
type PricingConfig struct {
	InputUSDPerM    float64 `yaml:"input_usd_per_m"`
	OutputUSDPerM   float64 `yaml:"output_usd_per_m"`
	CacheHitUSDPerM float64 `yaml:"cache_hit_usd_per_m"`
}

// ContextAnalysisConfig es la configuración del análisis estructural del
// contexto (009-001 P6): composición por sesión/cliente en /v1/context.
// Enabled=false (default) → sin records ni memoria; HistoryPerSession =
// ring buffer de history por record (0 = sin history, solo agregados).
type ContextAnalysisConfig struct {
	Enabled           bool `yaml:"enabled"`
	HistoryPerSession int  `yaml:"history_per_session"`
}

// ContextConfig es la configuración del rechazo por contexto
// (008-001). Margin: fracción 0-1 sobre context_window (default 0.1;
// -1 = usar default). Analysis: análisis de composición (009-001).
type ContextConfig struct {
	Margin   float64               `yaml:"margin"`
	Analysis ContextAnalysisConfig `yaml:"analysis"`
}

// TelemetryConfig es la configuración de la telemetría de descubrimiento
// (009-000-request-telemetry): canal dedicado telemetry.jsonl con metadata
// de cada request (headers allowlist + key paths + ids detectados en safe
// fields). Independiente de --verbose (I6); enabled=false = apagado (C1).
type TelemetryConfig struct {
	Enabled    bool    `yaml:"enabled"`
	SampleRate float64 `yaml:"sample_rate"`
	File       string  `yaml:"file"`
}

// RegistryConfig es la configuración del registro unificado de accounting +
// outcome (014-001): canal dedicado registry.jsonl con UN evento por intento
// + UN evento terminal por request en routing. Off por default (I3);
// enabled=true + file="" → error de validación en Parse (D2, fail-fast
// patrón telemetry 009-000 P8).
type RegistryConfig struct {
	Enabled bool   `yaml:"enabled"`
	File    string `yaml:"file"`
}

// WebSearchConfig es la configuración del grounded search server-side
// (011-005 D5): Enabled=false (default) → `web_search_preview` conserva el
// 400 de D3 de 003 (sin regresión); Enabled=true → mofgw emula el grounded
// search con DDG. MaxResults: tope de resultados inyectados (default 3).
// Timeout: tope por llamada a DDG (0 = default 10s).
type WebSearchConfig struct {
	Enabled    bool          `yaml:"enabled"`
	MaxResults int           `yaml:"max_results"`
	Timeout    time.Duration `yaml:"timeout"`
}

// EmbeddingsConfig es la configuración GLOBAL del endpoint /v1/embeddings
// (011-006 D3/D4): un Ollama por instancia mofgw. BaseURL: el Ollama al que
// mofgw forwardea (`POST base_url+"/embeddings"`, I2: nunca api.openai.com).
// APIKeyEnv: variable de entorno de la key (opcional — Ollama local sin key
// por default). El modelo forzado es POR CLIENTE (ClientConfig.Embeddings).
type EmbeddingsConfig struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	// APIKey se puebla al resolver APIKeyEnv (nunca viene del YAML).
	APIKey string `yaml:"-"`
}

// ClientEmbeddingsConfig es el modelo de embeddings forzado por cliente
// (011-006 P3/D2): `model` es el que mofgw manda a Ollama (ignorando el
// `model` de Odoo, "el cliente nunca elige") y el que aparece en el envelope
// de respuesta. Ausente (nil) → cliente sin modelo de embeddings → 400 (P4).
type ClientEmbeddingsConfig struct {
	Model string `yaml:"model"`
}

// ClientConfigConfig es la configuración del endpoint /v1/client-config
// (016-001-config-renderer-core). Aditivo y off-safe (I3/I4):
//   - BaseURL vacío → el endpoint responde 503 en runtime ("client_config.base_url
//     not set"). NO es fail-fast de arranque: BaseURL vacío es un estado válido
//     y documentado (el 503 es runtime, no error de carga).
//   - KeyEnv es la REFERENCIA (nombre de variable de entorno) de la key del
//     cliente; mofgw NUNCA la resuelve ni emite su valor (I1/P8). Default
//     "MOFGW_KEY".
type ClientConfigConfig struct {
	BaseURL string `yaml:"base_url"` // vacío → endpoint 503 en runtime (NO fail-fast)
	KeyEnv  string `yaml:"key_env"`  // env-ref de la key; default "MOFGW_KEY"
}

// ModelMetadata es la metadata declarativa de un modelo para el catálogo
// (007-001-model-metadata). Alimenta /v1/models (007-002) y, en el futuro,
// decisiones de ruteo. Datos verificados en research-token-efficiency.md §4.
type ModelMetadata struct {
	ContextWindow   int      `yaml:"context_window"`
	MaxOutput       int      `yaml:"max_output"`
	Thinking        []string `yaml:"thinking"`
	ThinkingDefault string   `yaml:"thinking_default"`
	// SupportedParameters: parámetros de request que el modelo soporta
	// (010-001-catalogo-fiel P1/P3). Zero-value (nil) → campo omitido del
	// catálogo (P2). Sin validación nueva (backward compatible 007-001).
	SupportedParameters []string `yaml:"supported_parameters"`
	// Modality: cadena declarativa "inputs->outputs" (010-001 P1/P4).
	// Zero-value ("") → campo omitido; architecture derivada mecánicamente.
	Modality string `yaml:"modality"`
}

// Config tipado resultante.
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Fallback  FallbackConfig   `yaml:"fallback"`
	Providers []ProviderConfig `yaml:"providers"`

	// Clients: registro de clientes autorizados (001-007-auth). Se puebla
	// SOLO desde `clients_file` (017-001, SINGLE source): el bloque inline
	// `clients:` del config.yaml ya no se soporta. 0 clientes = válido.
	Clients []ClientConfig

	// ClientsFile: path al archivo de registro de clientes (única fuente).
	// Se usa tal cual (CWD), igual que server.state_file/registry.file/
	// telemetry.file. Ausente + sin clients inline → sin clientes (válido).
	// Ausente + clients inline → error de migración fail-fast (P1). Archivo
	// ausente o inválido → error fail-fast (P2).
	ClientsFile string `yaml:"clients_file"`

	// ModelMetadata: capabilities por modelo (007-001). Keyed por modelo.
	ModelMetadata map[string]ModelMetadata `yaml:"model_metadata"`

	// Pricing: precios por modelo (006-002). Keyed por modelo.
	Pricing map[string]PricingConfig `yaml:"pricing"`

	// Context: rechazo por ventana (008-001).
	Context ContextConfig `yaml:"context"`

	// Telemetry: telemetría de descubrimiento (009-000).
	Telemetry TelemetryConfig `yaml:"telemetry"`

	// Registry: registro unificado de accounting + outcome (014-001).
	Registry RegistryConfig `yaml:"registry"`

	// WebSearch: grounded search server-side (011-005).
	WebSearch WebSearchConfig `yaml:"web_search"`

	// Embeddings: forward de embeddings a Ollama (011-006). Global: un
	// Ollama por instancia mofgw; el modelo forzado es por cliente.
	Embeddings EmbeddingsConfig `yaml:"embeddings"`

	// ClientConfig: endpoint /v1/client-config (016-001). Aditivo, off-safe:
	// bloque ausente/vacío no cambia ningún comportamiento existente (I3);
	// base_url vacío → 503 en runtime, no fail-fast (I4).
	ClientConfig ClientConfigConfig `yaml:"client_config"`

	// Efficiency: eficiencia de gateway (010-001).
	Efficiency EfficiencyConfig `yaml:"efficiency"`
}

// EfficiencyConfig agrupa features de eficiencia a nivel gateway.
type EfficiencyConfig struct {
	// SingleFlight: coalescing de requests idénticos CONCURRENTES
	// (010-001 P0). Off por default (cambio de semántica opt-in).
	SingleFlight SingleFlightConfig `yaml:"single_flight"`

	// ResponseCache: cache exact-match de respuestas (010-002 P1).
	// Off por default (cambio de semántica opt-in).
	ResponseCache ResponseCacheConfig `yaml:"response_cache"`
}

// ResponseCacheConfig configura el cache exact-match de respuestas
// (010-002 P1): sirve solo requests byte-idénticos (canonical) no-stream
// determinísticos, con LRU + TTL. Opt-in — OFF por default.
type ResponseCacheConfig struct {
	// Enabled: activa el cache para requests elegibles (no-stream,
	// temperature == 0 o seed presente).
	Enabled bool `yaml:"enabled"`
	// MaxEntries: tope de entradas LRU; 0 = default 512 (respeta la
	// huella de RAM ~12MB del gateway).
	MaxEntries int `yaml:"max_entries"`
	// TTL: vida de cada entrada; 0 = default 5m.
	TTL time.Duration `yaml:"ttl"`
}

// SingleFlightConfig configura el dedupe en vuelo de requests idénticos.
type SingleFlightConfig struct {
	// Enabled: activa el coalescing para requests determinísticos
	// (temperature == 0 o seed presente).
	Enabled bool `yaml:"enabled"`
	// MaxFlights: tope de vuelos simultáneos; 0 = default 512.
	// Anti-DoS: un grupo lleno ejecuta sin coalescing (bypass).
	MaxFlights int `yaml:"max_flights"`
}

// Defaults aplicados sobre YAML parcial.
func defaults() Config {
	return Config{
		Server: ServerConfig{
			Addr:                  "127.0.0.1:3369",
			MaxBodyBytes:          10 << 20, // 10MB
			ReadTimeout:           120 * time.Second,
			WriteTimeout:          300 * time.Second,
			MaxConcurrentRequests: 0, // 0 = ilimitado (backward compatible)
			BackpressureTimeout:   10 * time.Second,
			MaxSessionsRetained:   100,
			StateFile:             "", // persistencia deshabilitada por default
			StateSaveInterval:     0,
		},
		Fallback: FallbackConfig{
			MaxRetries:        2,
			Cooldown:          60 * time.Second,
			CooldownJitter:    5 * time.Second,
			Timeout:           120 * time.Second,
			FirstTokenTimeout: 90 * time.Second,
			Retry: RetryConfig{
				MaxAttempts: 2,
				BackoffBase: 500 * time.Millisecond,
				BackoffMax:  5 * time.Second,
			},
			Health: HealthConfig{
				Interval: 60 * time.Second,
				Timeout:  5 * time.Second,
			},
			Degradation: DegradationConfig{
				MaxRequests:   0,
				CooldownGrace: 5 * time.Second,
			},
			StickyRouting: StickyRoutingConfig{
				Enabled: false, // default: afinidad apagada (P1/P8)
			},
		},
		Context: ContextConfig{
			Margin: -1, // -1 = usar default del proxy (0.1)
			Analysis: ContextAnalysisConfig{
				Enabled:           false,
				HistoryPerSession: 50,
			},
		},
		Telemetry: TelemetryConfig{
			Enabled:    false,
			SampleRate: 1,
			File:       "",
		},
		Registry: RegistryConfig{
			Enabled: false,
			File:    "",
		},
		WebSearch: WebSearchConfig{
			Enabled:    false,
			MaxResults: 3,
			Timeout:    0, // 0 = default 10s (lo resuelve el cliente DDG)
		},
		ClientConfig: ClientConfigConfig{
			BaseURL: "", // vacío → 503 en runtime (I4), no fail-fast
			KeyEnv:  "MOFGW_KEY",
		},
		Efficiency: EfficiencyConfig{
			SingleFlight: SingleFlightConfig{
				Enabled:    false,
				MaxFlights: 512,
			},
			ResponseCache: ResponseCacheConfig{
				Enabled:    false,
				MaxEntries: 512,
				TTL:        5 * time.Minute,
			},
		},
	}
}

// LoadFile carga y valida el config desde un path explícito.
func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: leer %s: %w", path, err)
	}
	return Parse(raw)
}

// Load busca el config por precedencia: flag path > home > sistema.
func Load(explicit string) (*Config, string, error) {
	if explicit != "" {
		cfg, err := LoadFile(explicit)
		return cfg, explicit, err
	}
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "mofgw", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			cfg, err := LoadFile(p)
			return cfg, p, err
		}
	}
	p := "/etc/mofgw/config.yaml"
	if _, err := os.Stat(p); err == nil {
		cfg, err := LoadFile(p)
		return cfg, p, err
	}
	return nil, "", fmt.Errorf("config: no se encontró config.yaml (busqué ~/.config/mofgw/config.yaml y %s)", p)
}

// Parse valida y resuelve un YAML crudo.
func Parse(raw []byte) (*Config, error) {
	cfg := defaults()
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: YAML inválido: %w", err)
	}
	// 017-001 (SINGLE source, P1/P3): el bloque inline `clients:` se eliminó;
	// la única fuente del registro es `clients_file`. Detectamos la presencia
	// del key top-level `clients:` para distinguir ausencia (válido) de
	// presencia (migración pendiente o dual-source → fail-fast).
	hasInlineClients, err := hasYAMLKey(raw, "clients")
	if err != nil {
		return nil, err
	}
	if hasInlineClients && cfg.ClientsFile != "" {
		// P3: anti dual-source.
		return nil, fmt.Errorf("config: `clients:` inline y `clients_file` a la vez: SINGLE source violado — usá solo clients_file (P3)")
	}
	if hasInlineClients {
		// P1: migración pendiente.
		return nil, fmt.Errorf("config: `clients:` inline ya no se soporta; mové el bloque a `clients_file` (P1)")
	}
	if cfg.ClientsFile != "" {
		// P2: el registro de clientes se carga y valida del archivo dedicado.
		clients, err := LoadClientsFile(cfg.ClientsFile)
		if err != nil {
			return nil, err
		}
		cfg.Clients = clients
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	// P7 (013-004): los defaults del provider subprocess se aplican en Parse,
	// no como efecto secundario de validate(). validate() ya no muta estos
	// campos; aquí se resuelven Command/SessionDir ausentes.
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Type != "subprocess" {
			continue
		}
		if p.Command == "" {
			p.Command = "claude"
		}
		if p.SessionDir == "" {
			p.SessionDir = DefaultSessionDir
		}
	}
	if err := cfg.resolveKeys(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ValidateClients valida un registro de clientes (017-001, extraído de
// la validación inline de config.go:600-619). Reglas: id obligatorio,
// ids únicos, key_sha256 de 64 chars hex, concurrencia no negativa.
// Compartido por el Load() de boot (fail-fast) y por el reloader de
// 017-002 (fail-soft).
func ValidateClients(clients []ClientConfig) error {
	seen := map[string]bool{}
	for i := range clients {
		cl := &clients[i]
		if cl.ID == "" {
			return fmt.Errorf("config: clients[%d]: id es obligatorio", i)
		}
		if seen[cl.ID] {
			return fmt.Errorf("config: client id %q duplicado", cl.ID)
		}
		seen[cl.ID] = true
		if len(cl.KeySHA256) != 64 {
			return fmt.Errorf("config: client %q: key_sha256 debe ser sha256 hex (64 chars), got %d", cl.ID, len(cl.KeySHA256))
		}
		if _, err := hex.DecodeString(cl.KeySHA256); err != nil {
			return fmt.Errorf("config: client %q: key_sha256 no es hex válido: %w", cl.ID, err)
		}
		if cl.MaxConcurrentRequests < 0 || cl.MaxConcurrentPerAgent < 0 {
			return fmt.Errorf("config: client %q: max_concurrent_requests/max_concurrent_per_agent no pueden ser negativos", cl.ID)
		}
	}
	return nil
}

// LoadClientsFile lee y valida un archivo de registro de clientes (017-001).
// El archivo es una top-level list de mapas con las MISMAS keys del bloque
// inline `clients:` histórico (id, key_sha256, max_concurrent_requests,
// max_concurrent_per_agent, budget, embeddings) pero SIN la clave `clients:`.
// Archivo ausente o inválido → error fail-fast (P2).
func LoadClientsFile(path string) ([]ClientConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: leer clients_file %s: %w", path, err)
	}
	var clients []ClientConfig
	if err := yaml.Unmarshal(raw, &clients); err != nil {
		return nil, fmt.Errorf("config: clients_file %s: YAML inválido: %w", path, err)
	}
	if err := ValidateClients(clients); err != nil {
		return nil, fmt.Errorf("config: clients_file %s: %w", path, err)
	}
	return clients, nil
}

// hasYAMLKey reporta si el YAML crudo es un mapping top-level con el key
// dado presente. Se usa para detectar el bloque inline `clients:` obsoleto
// (017-001, P1/P3) de forma robusta — distingue ausencia (válido) de
// presencia (migración pendiente o dual-source → error).
func hasYAMLKey(raw []byte, key string) (bool, error) {
	var m map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return false, fmt.Errorf("config: YAML inválido: %w", err)
	}
	_, ok := m[key]
	return ok, nil
}

func (c *Config) validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("config: al menos un provider es obligatorio")
	}
	if c.Fallback.FirstTokenTimeout < 0 {
		return fmt.Errorf("config: fallback.first_token_timeout no puede ser negativo")
	}
	if c.Fallback.InterAttemptDelay < 0 {
		return fmt.Errorf("config: fallback.inter_attempt_delay no puede ser negativo")
	}
	if c.Server.MaxBodyBytes <= 0 {
		return fmt.Errorf("config: server.max_body_bytes debe ser > 0")
	}
	if c.Server.MaxConcurrentRequests < 0 {
		return fmt.Errorf("config: server.max_concurrent_requests no puede ser negativo")
	}
	if c.Server.BackpressureTimeout < 0 {
		return fmt.Errorf("config: server.backpressure_timeout no puede ser negativo")
	}
	seen := map[string]bool{}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.ID == "" {
			return fmt.Errorf("config: providers[%d]: id es obligatorio", i)
		}
		if seen[p.ID] {
			return fmt.Errorf("config: provider id %q duplicado", p.ID)
		}
		seen[p.ID] = true
		// 013-003-config-wiring (P1-P4): el discriminador type condiciona la
		// validación. "" o "http" = comportamiento HTTP actual (P4, cero
		// cambio); "subprocess" = no exige base_url/api_key_env (P2),
		// requiere backend (P1) y rechaza backend != "claude" al cargar
		// (P3, owner 12 Ago); cualquier otro type → error descriptivo (P3).
		switch p.Type {
		case "", "http":
			if p.BaseURL == "" {
				return fmt.Errorf("config: provider %q: base_url es obligatorio", p.ID)
			}
			if p.APIKeyEnv == "" {
				return fmt.Errorf("config: provider %q: api_key_env es obligatorio", p.ID)
			}
		case "subprocess":
			if p.Backend == "" {
				return fmt.Errorf("config: provider %q: backend es obligatorio (type: subprocess)", p.ID)
			}
			if p.Backend != "claude" {
				return fmt.Errorf("config: provider %q: unknown backend %q", p.ID, p.Backend)
			}
		default:
			return fmt.Errorf("config: provider %q: unknown provider type %q", p.ID, p.Type)
		}
		if len(p.Models) == 0 {
			return fmt.Errorf("config: provider %q: al menos un model es obligatorio", p.ID)
		}
		if p.MaxTokens < 0 {
			return fmt.Errorf("config: provider %q: max_tokens no puede ser negativo", p.ID)
		}
		if p.FirstTokenTimeout != nil && *p.FirstTokenTimeout < 0 {
			return fmt.Errorf("config: provider %q: first_token_timeout no puede ser negativo", p.ID)
		}
		if p.Retry != nil && (p.Retry.MaxAttempts < 0 || p.Retry.BackoffBase < 0 || p.Retry.BackoffMax < 0) {
			return fmt.Errorf("config: provider %q: retry con valores negativos", p.ID)
		}
		if p.Health != nil && (p.Health.Interval <= 0 || p.Health.Timeout <= 0) {
			return fmt.Errorf("config: provider %q: health.interval/timeout deben ser > 0", p.ID)
		}
	}
	if c.Fallback.Retry.MaxAttempts < 0 || c.Fallback.Retry.BackoffBase < 0 || c.Fallback.Retry.BackoffMax < 0 {
		return fmt.Errorf("config: fallback.retry con valores negativos")
	}
	if c.Efficiency.ResponseCache.MaxEntries < 0 {
		return fmt.Errorf("config: efficiency.response_cache.max_entries no puede ser negativo")
	}
	if c.Efficiency.ResponseCache.TTL < 0 {
		return fmt.Errorf("config: efficiency.response_cache.ttl no puede ser negativo")
	}
	if c.Fallback.Health.Interval <= 0 || c.Fallback.Health.Timeout <= 0 {
		return fmt.Errorf("config: fallback.health.interval/timeout deben ser > 0")
	}
	if c.Fallback.Degradation.MaxRequests < 0 || c.Fallback.Degradation.CooldownGrace < 0 {
		return fmt.Errorf("config: fallback.degradation con valores negativos")
	}
	if err := ValidateClients(c.Clients); err != nil {
		return err
	}
	if c.Server.MaxSessionsRetained < 0 {
		// 008-003 external review (Major): tope negativo no tiene sentido.
		return fmt.Errorf("config: server.max_sessions_retained no puede ser negativo")
	}
	if c.Server.StateSaveInterval < 0 {
		return fmt.Errorf("config: server.state_save_interval no puede ser negativo")
	}
	// model_metadata (007-001): valores negativos y thinking_default fuera
	// de la lista se rechazan temprano (P4).
	for model, md := range c.ModelMetadata {
		if md.ContextWindow < 0 {
			return fmt.Errorf("config: model_metadata %q: context_window no puede ser negativo", model)
		}
		if md.MaxOutput < 0 {
			return fmt.Errorf("config: model_metadata %q: max_output no puede ser negativo", model)
		}
		if md.ThinkingDefault != "" && len(md.Thinking) == 0 {
			// 007-001 external review (Major): thinking_default para una
			// capability inexistente es inconsistencia → rechazar.
			return fmt.Errorf("config: model_metadata %q: thinking_default %q pero thinking está vacío", model, md.ThinkingDefault)
		}
		if md.ThinkingDefault != "" && len(md.Thinking) > 0 {
			found := false
			for _, l := range md.Thinking {
				if l == md.ThinkingDefault {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("config: model_metadata %q: thinking_default %q no está en thinking %v", model, md.ThinkingDefault, md.Thinking)
			}
		}
	}
	// pricing (006-002): precios negativos, NaN o Inf se rechazan.
	// (Hallazgo de review externo 006-002: `.inf`/`.nan` en YAML pasaban
	// la validación `< 0` → costos NaN/Inf en metrics. math.IsNaN/IsInf
	// cierran el gap.)
	for model, p := range c.Pricing {
		if p.InputUSDPerM < 0 || p.OutputUSDPerM < 0 || p.CacheHitUSDPerM < 0 {
			return fmt.Errorf("config: pricing %q: precios no pueden ser negativos", model)
		}
		if math.IsNaN(p.InputUSDPerM) || math.IsInf(p.InputUSDPerM, 0) ||
			math.IsNaN(p.OutputUSDPerM) || math.IsInf(p.OutputUSDPerM, 0) ||
			math.IsNaN(p.CacheHitUSDPerM) || math.IsInf(p.CacheHitUSDPerM, 0) {
			return fmt.Errorf("config: pricing %q: precios deben ser finitos (no NaN/Inf)", model)
		}
	}
	// telemetry (009-000 P8): sample_rate debe estar en (0, 1]; NaN/Inf se
	// rechazan (mismo patrón que pricing); enabled=true con file vacío es
	// error de arranque (C10/I5, fail-fast).
	if c.Telemetry.SampleRate <= 0 || c.Telemetry.SampleRate > 1 ||
		math.IsNaN(c.Telemetry.SampleRate) || math.IsInf(c.Telemetry.SampleRate, 0) {
		return fmt.Errorf("config: telemetry.sample_rate debe estar entre 0 y 1")
	}
	if c.Telemetry.Enabled && c.Telemetry.File == "" {
		return fmt.Errorf("config: telemetry.enabled=true requiere telemetry.file")
	}
	// registry (014-001 P1/D2): enabled=true con file vacío es error de
	// arranque (fail-fast, mismo patrón que telemetry).
	if c.Registry.Enabled && c.Registry.File == "" {
		return fmt.Errorf("config: registry.enabled=true requiere registry.file")
	}
	// context.analysis (009-001 P6): history_per_session negativo no tiene
	// sentido (0 = sin history, solo agregados — consistente con la
	// validación de max_sessions_retained).
	if c.Context.Analysis.HistoryPerSession < 0 {
		return fmt.Errorf("config: context.analysis.history_per_session no puede ser negativo")
	}
	// web_search (011-005): max_results y timeout negativos no tienen sentido.
	if c.WebSearch.MaxResults < 0 {
		return fmt.Errorf("config: web_search.max_results no puede ser negativo")
	}
	if c.WebSearch.Timeout < 0 {
		return fmt.Errorf("config: web_search.timeout no puede ser negativo")
	}
	return nil
}

func (c *Config) resolveKeys() error {
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.Type == "subprocess" {
			// 013-003 P2: el provider subprocess no usa API key (claude usa
			// OAuth/suscripción); nunca se resuelve api_key_env para él.
			continue
		}
		v, ok := os.LookupEnv(p.APIKeyEnv)
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("config: provider %q: env var %s no está seteada (las keys nunca van en el YAML)", p.ID, p.APIKeyEnv)
		}
		p.APIKey = v
	}
	// embeddings.api_key_env (011-006): opcional — solo se resuelve si está
	// configurado (Ollama local sin key por default, D3). Seteada y ausente
	// → fail-fast (mismo criterio que providers).
	if c.Embeddings.APIKeyEnv != "" {
		v, ok := os.LookupEnv(c.Embeddings.APIKeyEnv)
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("config: embeddings: env var %s no está seteada (las keys nunca van en el YAML)", c.Embeddings.APIKeyEnv)
		}
		c.Embeddings.APIKey = v
	}
	return nil
}
