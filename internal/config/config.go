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
	MaxRetries     int                 `yaml:"max_retries"`
	Cooldown       time.Duration       `yaml:"cooldown"`
	CooldownJitter time.Duration       `yaml:"cooldown_jitter"`
	Timeout        time.Duration       `yaml:"timeout"`
	Retry          RetryConfig         `yaml:"retry"`
	Health         HealthConfig        `yaml:"health"`
	Degradation    DegradationConfig   `yaml:"degradation"`
	StickyRouting  StickyRoutingConfig `yaml:"sticky_routing"` // 009-002
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

	// Overrides per-provider de 002-001/002-002 (nil = usar el global).
	Retry  *RetryConfig  `yaml:"retry"`
	Health *HealthConfig `yaml:"health"`

	// APIKey se puebla al resolver APIKeyEnv (nunca viene del YAML).
	APIKey string `yaml:"-"`
}

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

// ModelMetadata es la metadata declarativa de un modelo para el catálogo
// (007-001-model-metadata). Alimenta /v1/models (007-002) y, en el futuro,
// decisiones de ruteo. Datos verificados en research-token-efficiency.md §4.
type ModelMetadata struct {
	ContextWindow   int      `yaml:"context_window"`
	MaxOutput       int      `yaml:"max_output"`
	Thinking        []string `yaml:"thinking"`
	ThinkingDefault string   `yaml:"thinking_default"`
}

// Config tipado resultante.
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Fallback  FallbackConfig   `yaml:"fallback"`
	Providers []ProviderConfig `yaml:"providers"`
	Clients   []ClientConfig   `yaml:"clients"`

	// ModelMetadata: capabilities por modelo (007-001). Keyed por modelo.
	ModelMetadata map[string]ModelMetadata `yaml:"model_metadata"`

	// Pricing: precios por modelo (006-002). Keyed por modelo.
	Pricing map[string]PricingConfig `yaml:"pricing"`

	// Context: rechazo por ventana (008-001).
	Context ContextConfig `yaml:"context"`

	// Telemetry: telemetría de descubrimiento (009-000).
	Telemetry TelemetryConfig `yaml:"telemetry"`
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
			MaxRetries:     2,
			Cooldown:       60 * time.Second,
			CooldownJitter: 5 * time.Second,
			Timeout:        120 * time.Second,
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
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.resolveKeys(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("config: al menos un provider es obligatorio")
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
		if p.BaseURL == "" {
			return fmt.Errorf("config: provider %q: base_url es obligatorio", p.ID)
		}
		if p.APIKeyEnv == "" {
			return fmt.Errorf("config: provider %q: api_key_env es obligatorio", p.ID)
		}
		if len(p.Models) == 0 {
			return fmt.Errorf("config: provider %q: al menos un model es obligatorio", p.ID)
		}
		if p.MaxTokens < 0 {
			return fmt.Errorf("config: provider %q: max_tokens no puede ser negativo", p.ID)
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
	if c.Fallback.Health.Interval <= 0 || c.Fallback.Health.Timeout <= 0 {
		return fmt.Errorf("config: fallback.health.interval/timeout deben ser > 0")
	}
	if c.Fallback.Degradation.MaxRequests < 0 || c.Fallback.Degradation.CooldownGrace < 0 {
		return fmt.Errorf("config: fallback.degradation con valores negativos")
	}
	seenClients := map[string]bool{}
	for i := range c.Clients {
		cl := &c.Clients[i]
		if cl.ID == "" {
			return fmt.Errorf("config: clients[%d]: id es obligatorio", i)
		}
		if seenClients[cl.ID] {
			return fmt.Errorf("config: client id %q duplicado", cl.ID)
		}
		seenClients[cl.ID] = true
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
	// context.analysis (009-001 P6): history_per_session negativo no tiene
	// sentido (0 = sin history, solo agregados — consistente con la
	// validación de max_sessions_retained).
	if c.Context.Analysis.HistoryPerSession < 0 {
		return fmt.Errorf("config: context.analysis.history_per_session no puede ser negativo")
	}
	return nil
}

func (c *Config) resolveKeys() error {
	for i := range c.Providers {
		p := &c.Providers[i]
		v, ok := os.LookupEnv(p.APIKeyEnv)
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("config: provider %q: env var %s no está seteada (las keys nunca van en el YAML)", p.ID, p.APIKeyEnv)
		}
		p.APIKey = v
	}
	return nil
}
