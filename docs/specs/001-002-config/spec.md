# Spec — 001-002-config: Config file (providers, orden fallback, cooldowns, timeouts)

---
feature_id: 001-002-config
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: —
paralelizable: sí
---

```yaml
context: >
  mofgw necesita saber a qué providers hablar, en qué orden, con qué
  cooldowns y timeouts. Esta feature es el cerebro de configuración del
  proxy: carga un archivo YAML desde ~/.config/mofgw/config.yaml (nivel
  home) o /etc/mofgw/config.yaml (nivel sistema), lo valida con reglas
  estrictas, y expone un struct Config tipado a las demás features
  (001-001 endpoint, 001-003 fallback, 001-004 clamp, 001-006 timeouts).
  Las API keys NUNCA viven en el YAML: solo referencias a env vars.
resolution: >
  Operar mofgw se reduce a editar un solo archivo YAML versionable:
  agregar/remover/quitar providers, reordenar la cadena de fallback, ajustar
  cooldowns y timeouts. Un config inválido falla al arranque con un error
  claro (nunca arranca medio-configurado), y un config válido se valida
  con el mismo código que usan los tests.
postcondition: >
  El paquete internal/config carga y valida config.yaml (home o sistema,
  con precedencia documentada), resuelve variables de entorno para API keys,
  y produce el struct Config tipado; config inválido → error descriptivo al
  arranque; todos los tests de la verification pasan sin red externa.
verification:
  - go test ./internal/config/ → 0 failures (suite con archivos YAML de
    fixture en testdata/, cero red externa)
  - YAML válido mínimo (server + 1 provider con api_key_env) → Config
    cargado, campos mapeados 1:1 contra el fixture
  - YAML con 3 providers → orden preservado en Config.Providers (el orden
    del archivo ES el orden de fallback)
  - api_key_env resuelta: env var seteada → api_key poblada; env var no
    seteada → error de validación (con el nombre de la var, sin el valor)
  - Precedencia home/sistema: solo ~/.config/mofgw/config.yaml → usa ese;
    solo /etc/mofgw/config.yaml → usa ese; ambos → gana home (documentado);
    ninguno → error "no config file found" con las dos rutas intentadas
  - YAML con campo desconocido (typo en `provdiers`) → error con path del
    campo (p.ej. "field provdiers not found") — strict decoding
  - YAML con provider duplicado (mismo id) → error
  - YAML sin providers → error
  - YAML con provider sin api_key_env o sin base_url → error por provider
  - Defaults aplicados: cooldown, timeouts y límites ausentes → valores
    default documentados (cooldown 60s, timeout 120s, max_retries 2, max_body 10MB, etc.)
  - Semántica 3 estados del timeout: campo ausente → fallback.timeout (o 120s);
    0 explícito → timeout desactivado; N → N. Los tests cubren los 3 casos.
  - `--config /ruta/explicita` (flag) sobreescribe la resolución
    home/sistema — test con ruta inexistente → error claro
```

## Contrato de archivo

### Ubicación y precedencia (resolución)

| Fuente | Ruta | Prioridad |
|--------|------|-----------|
| Flag CLI | `--config <path>` | 1 (más alta) |
| Home | `$XDG_CONFIG_HOME/mofgw/config.yaml` o `~/.config/mofgw/config.yaml` | 2 |
| Sistema | `/etc/mofgw/config.yaml` | 3 |

- Si la fuente de mayor prioridad existe → se usa, se ignora el resto.
- Si ninguna existe → error al arranque: `no config file found (tried:
  ~/.config/mofgw/config.yaml, /etc/mofgw/config.yaml)`.
- `$XDG_CONFIG_HOME` respeta la spec XDG (default `~/.config` si no está
  seteada). El flag `--config` acepta `-` para stdin? → NO en el MVP
  (fuera de scope).

### Esquema YAML (v1)

```yaml
# ~/.config/mofgw/config.yaml — ejemplo completo
server:
  addr: "127.0.0.1:3369"        # default
  max_body_bytes: 10485760      # 10MB default
  read_timeout: 120s            # default 120s
  write_timeout: 300s           # default 300s (streaming largo)

fallback:
  max_retries: 2                # intentos totales por request (default 2 → hasta 3 providers)
  cooldown: 60s                 # default por provider si no se especifica
  cooldown_jitter: 5s           # jitter para evitar thundering herd (default 5s)
  timeout: 120s                 # global por intento (TTFB, 001-006); override per-provider en
                                # providers[].timeout. Semántica 3 estados (ver nota abajo):
                                # ausente → este valor; 0 explícito → sin timeout; N → N

providers:
  - id: provider-a                # obligatorio, único
    base_url: "https://api.example.com/v1"   # obligatorio
    api_key_env: "MOFGW_PROVIDER_A_KEY"       # obligatorio (referencia, NUNCA el valor)
    models: ["deepseek-v4-flash", "glm-5.2"]   # obligatorio, ≥1
    max_tokens: 8192            # opcional → clamp 001-004; default 0 = sin clamp
    cooldown: 90s               # opcional → override del fallback.cooldown
    timeout: 60s                # opcional → override TTFB (001-006). Semántica 3 estados:
                                # ausente → fallback.timeout; 0 explícito → sin timeout; N → N.
                                # En Go: *time.Duration (nil = ausente), NO time.Duration plano.
    semaphore: 8                # ⚠️ RESERVADO para 004-001-concurrencia (fuera del MVP EPIC-001)                # opcional → in-flight max (default 8)
  - id: provider-c
    base_url: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"
    api_key_env: "MOFGW_PROVIDER_C_KEY"
    models: ["qwen3.7-plus"]
    max_tokens: 16384
```

### Struct resultante (contrato cross-feature)

```go
// internal/config/config.go — expuesto a 001-001/003/004/006
type Config struct {
    Server    ServerConfig    `yaml:"server"`
    Fallback  FallbackConfig  `yaml:"fallback"`
    Providers []ProviderConfig `yaml:"providers"`
}

type ServerConfig struct {
    Addr          string `yaml:"addr"`
    MaxBodyBytes  int64  `yaml:"max_body_bytes"`
    ReadTimeout   time.Duration `yaml:"read_timeout"`
    WriteTimeout  time.Duration `yaml:"write_timeout"`
}

type FallbackConfig struct {
    MaxRetries     int           `yaml:"max_retries"`
    Cooldown       time.Duration `yaml:"cooldown"`
    CooldownJitter time.Duration `yaml:"cooldown_jitter"`
}

type ProviderConfig struct {
    ID         string        `yaml:"id"`
    BaseURL    string        `yaml:"base_url"`
    APIKeyEnv  string        `yaml:"api_key_env"`
    APIKey     string        // poblada en runtime desde APIKeyEnv; nunca serializada
    Models     []string      `yaml:"models"`
    MaxTokens  int           `yaml:"max_tokens"`
    Cooldown   time.Duration `yaml:"cooldown"`
    Timeout    time.Duration `yaml:"timeout"`
    Semaphore  int           `yaml:"semaphore"`
}
```

- **El orden de `providers:` ES el orden de fallback** (001-003 lo recorre
  en orden; no hay campo `order` separado — menos estados, una sola fuente
  de verdad).
- `ProviderConfig.APIKey` se marca `yaml:"-"` (nunca se escribe ni se
  loguea).
- Duraciones: formato Go (`60s`, `90s`, `1m30s`); parse con
  `time.ParseDuration`.

## Reglas de validación (estrictas, fail-fast)

| # | Regla | Error |
|---|-------|-------|
| V1 | YAML malformado (syntax) | `parse config: <detalle yaml>` |
| V2 | Campo desconocido en cualquier nivel | `field <path> not found` (strict decoding) |
| V3 | `providers` vacío o ausente | `config: at least one provider required` |
| V4 | `id` ausente, vacío o duplicado | `provider[<i>]: id required/duplicate: <id>` |
| V5 | `base_url` ausente o URL inválida | `provider[<i>] <id>: invalid base_url` |
| V6 | `api_key_env` ausente | `provider[<i>] <id>: api_key_env required` |
| V7 | `api_key_env` seteada pero env var no definida | `provider[<i>] <id>: env var <NAME> not set` |
| V8 | `models` vacío | `provider[<i>] <id>: at least one model required` |
| V9 | duración inválida (p.ej. `abc`) | `provider[<i>] <id>: invalid duration: abc` |
| V10 | valores negativos (cooldown, timeout, semaphore ≤ 0) | `provider[<i>] <id>: must be > 0` |
| V11 | `server.addr` vacío | `server: addr required` |
| V12 | `max_body_bytes` ≤ 0 | `server: max_body_bytes must be > 0` |

- Los errores acumulan TODOS los problemas encontrados (no el primero):
  `validation failed: 3 errors: ...` — el operador arregla de una pasada.
- `slog` loguea la ruta de config cargada y los ids de providers al
  arranque (nivel info), NUNCA keys ni env var names con valores.

## Defaults

| Campo | Default | Nota |
|-------|---------|------|
| `server.addr` | `127.0.0.1:3369` | mismo default que 001-001 |
| `server.max_body_bytes` | 10485760 (10MB) | ídem |
| `server.read_timeout` | 120s | |
| `server.write_timeout` | 300s | margen para SSE largo |
| `fallback.max_retries` | 2 | = hasta 3 intentos (provider inicial + 2 fallbacks) |
| `fallback.cooldown` | 60s | |
| `fallback.cooldown_jitter` | 5s | |
| `provider.max_tokens` | 0 | 0 = sin clamp (001-004 aplica si >0) |
| `provider.cooldown` | hereda `fallback.cooldown` | |
| `provider.timeout` | 60s | TTFB, ver 001-006 |
| `provider.semaphore` | 8 | |

## Implementación (notas)

- `internal/config/` (layout research-architecture §6): `config.go`
  (structs + Load), `validate.go` (V1-V12), `defaults.go`, tests en
  `config_test.go` con fixtures en `testdata/`.
- Carga: `os.ReadFile` → `yaml.UnmarshalStrict` (V2) → defaults → merge →
  validación (V3-V12) → resolución de env vars (V7). El orden importa:
  defaults ANTES de validar para que los tests de defaults sean
  deterministas.
- Cero dependencias externas: `gopkg.in/yaml.v3` (o `sigs.k8s.io/yaml`
  — decisión: yaml.v3, ya es la standard de facto en el ecosistema Go).
- `Load(path string) (*Config, error)` + `LoadAuto() (*Config, error)`
  (resolución de precedencia). `LoadAuto` usa `os.Args` para el flag
  `--config` (parseado en `cmd/mofgw/main.go`, no en el paquete config —
  el paquete recibe la ruta resuelta o vacía).
- Hot-reload: NO en el MVP (requiere recarga de estado cooldown en
  001-003; se documenta como mejora post-MVP). Cambios de config =
  restart del servicio.

## Seguridad

- API keys SOLO vía env vars; el YAML solo referencia nombres. Si un
  valor de key se detecta en el YAML (heurística: campo `api_key` con
  valor literal) → error de validación: `api_key must be an env var
  reference (api_key_env), never a literal value`.
- Los errores de validación NUNCA incluyen valores de env vars, solo
  nombres.
- Permisos sugeridos en install: config 0644 (no tiene secretos por
  diseño); el archivo de env (si se usa `EnvironmentFile=` en systemd)
  va 0600.

## No-funcionales

- Carga de config < 50ms (un archivo pequeño, sin red).
- La suite de tests de config corre sin red externa (fixtures locales).
- Error messages: inglés, con path exacto del campo, accionables.

## Fuera de scope (features hermanas)

- Endpoint HTTP + validación de requests → 001-001 (consume el subset
  `server` + `providers[0]` de config en su MVP)
- Cadena de fallback + cooldown en runtime → 001-003 (consume
  `fallback.*` y `provider.cooldown`)
- Clamp de max_tokens → 001-004 (consume `provider.max_tokens`)
- Timeouts por intento → 001-006 (consume `provider.timeout`)
- Hot-reload / SIGHUP → post-MVP (documentado)
- `--config -` (stdin) → post-MVP
- Múltiples archivos / includes / templating → post-MVP

## Nota

Esta spec es hermana de 001-001 (ambas sin dependencias, paralelizables).
La config mínima que 001-001 consume en su MVP (server + primer provider)
debe ser un subset válido de este esquema completo — si 001-001 arranca
primero, su `Load` mínimo será reemplazado por este `internal/config` sin
cambiar el formato del archivo.
