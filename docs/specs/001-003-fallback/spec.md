# Spec — 001-003-fallback: Fallback en cadena con cooldown por provider

---
feature_id: 001-003-fallback
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-001-endpoint, 001-002-config
paralelizable: no
---

```yaml
context: >
  El principio rector de mofgw (decisión Pablo, 03 Ago) es la transparencia
  total: el cliente nunca se entera de que un provider falló. Cuando un
  provider upstream está caído, degradado o en cooldown, el proxy debe
  probar el siguiente de la cadena, en el orden configurado, hasta que uno
  responda. Esta feature es el cerebro de esa decisión: el router que
  recorre providers en orden, saltea los que están en cooldown, clasifica
  errores (reintentables vs no), y garantiza que el cliente reciba una
  respuesta exitosa si AL MENOS UN provider responde. Solo si TODOS fallan
  el cliente ve un error — en formato OpenAI-compatible, sin detalles
  internos.
resolution: >
  Operar la cadena de fallback se reduce a editar el orden de `providers:`
  en config.yaml (001-002): el orden del archivo ES el orden de la cadena,
  sin campo `order` separado. El estado de cooldown vive en memoria
  (map + mutex, epímero por diseño: al reiniciar todos arrancan fríos) y se
  limpia solo con un ticker. Cada intento que falla de forma retryable mete
  al provider en cooldown por `cooldown` (+ jitter), de modo que un provider
  caído no recibe tráfico durante un rato y no degrada la latencia de todos
  los requests.
postcondition: >
  internal/router resuelve cada request recorriendo los providers que sirven
  el modelo pedido, en orden de config, salteando los que están en cooldown;
  si un intento falla con error retryable (429, 5xx, ErrTimeout, red), prueba
  el siguiente; si falla con error no-retryable (4xx de cliente), devuelve el
  error al cliente inmediatamente sin probar más providers; si TODOS fallan,
  responde 502 con body OpenAI-compatible y el último error saneado; en
  streaming, el fallback solo aplica antes del primer byte (ver
  research-architecture.md §3). Todos los tests de la verification pasan sin
  red externa y sin race (go test -race).
verification:
  - go test ./internal/router/ → 0 failures, con -race (suite con providers
    fake en httptest, cero red externa)
  - 3 providers OK → responde el primero en orden de config, con 1 solo
    intento (fallback NO se dispara)
  - Providers 1 y 2 devuelven 500, provider 3 OK → el cliente recibe 200 con
    el contenido del provider 3 (transparencia total: el cliente no ve los
    fallos previos)
  - Provider falla con 500 → entra en cooldown (fake clock); el siguiente
    request lo saltea y prueba el siguiente de la cadena
  - Fake clock avanza más allá de cooldown + jitter → el provider vuelve a
    ser probado
  - Override per-provider: provider con `cooldown: 90s` queda en cooldown
    más tiempo que el global `fallback.cooldown: 60s`
  - Provider 1 devuelve 400 (error de cliente) → 400 al cliente de inmediato,
    sin probar provider 2 (attempt count == 1)
  - Modelo que ningún provider sirve → error OpenAI-compatible al cliente
    (sin intentos de red)
  - Streaming: fallo antes del primer byte → fallback al siguiente provider;
    fallo después del primer byte → SSE de error bien formado + [DONE], sin
    reconectar (verification de 001-005 integra el passthrough)
  - Todos los providers fallan → 502 con body OpenAI-compatible
    ({"error":{"message":...,"type":"upstream_error"}}), sin API keys ni
    URLs internas en el mensaje
  - Ticker: entradas expiradas del mapa de cooldown se limpian (el mapa no
    crece indefinidamente)
  - max_retries: con `max_retries: 2` y 5 providers que sirven el modelo →
    máximo 3 intentos totales (max_retries + 1)
```

## Contrato del paquete

### Ubicación en el layout

`internal/router/` — el router de fallback. Consume `internal/config`
(001-002) y `internal/provider` (cliente OpenAI-compatible, usado también
por 001-001). No depende de 001-005 (streaming) ni de 004-001
(concurrencia): define contratos mínimos (interfaces) que esas features
implementan o consumen después.

### Semántica de la cadena

1. **Resolución de modelo:** el cliente pide `model: M`. El router arma la
   lista de candidatos = providers cuyo `Models` contiene `M` (match exacto
   en el MVP). Si la lista queda vacía → error al cliente sin intentos:
   `{"error":{"message":"model <M> not served by any configured provider", "type":"invalid_request_error"}}`.
   *Alias mapping (nombre virtual → modelo real por provider) queda fuera del
   MVP: EPIC-004 lo agrega si hace falta.*
2. **Orden:** los candidatos se recorren en el orden del archivo de config
   (001-002: el orden ES el orden de fallback).
3. **Cooldown:** un provider en cooldown se saltea sin intentar. `max_retries`
   (default 2) limita los intentos TOTALES por request a `max_retries + 1`
   (default 3): se corta la cadena aunque queden candidatos.
4. **Clasificación de errores (contrato compartido con 001-006):**

   | Clase | Ejemplos | Acción |
   |-------|----------|--------|
   | Retryable | 429, 5xx, `ErrTimeout`, error de red/DNS, ctx cancelado | Marcar provider en cooldown, probar siguiente |
   | No-retryable | 400, 401, 403, 404 | Devolver el error al cliente inmediatamente, sin más intentos |

   Contrato: `func IsProviderFailure(err error) bool` y
   `func IsRetryableStatus(code int) bool` viven en `internal/router` (o un
   subpaquete `internal/router/failclass`) y son las únicas fuentes de verdad.
   001-006 (`ErrTimeout`) y 001-004 (clamp) las usan.
5. **Transparencia:** si el request se resuelve, el cliente recibe 200 sin
   ninguna señal de los fallos previos. Solo si TODOS los intentos fallan:
   `502` con `{"error":{"message":"all upstream providers failed (<N> attempts)","type":"upstream_error"}}`.
   El mensaje NUNCA contiene API keys, URLs internas ni IDs de provider.
6. **Streaming (regla de frontera):** el fallback aplica solo ANTES del
   primer byte (primer evento SSE). Después del primer byte el stream queda
   pinneado al provider que lo arrancó: si muere a mitad, el router emite un
   evento SSE de error bien formado seguido de `[DONE]` y registra la
   métrica — nunca reconecta a otro provider (research-architecture.md §3).
   El mecanismo SSE en sí es de 001-005; el router solo decide a qué
   provider delegar y cuándo parar de probar.

### Estado de cooldown (en memoria)

```go
// internal/router/cooldown.go
type cooldownEntry struct {
    Until    time.Time // no usar antes de esta hora
    Failures int       // contador de fallos consecutivos (para métricas/logs 005)
}

type CooldownStore struct {
    mu    sync.Mutex
    m     map[string]cooldownEntry // key = provider ID
    clock Clock                    // reloj inyectable para tests
    rand  *rand.Rand               // fuente de jitter inyectable
}

// Enter marca un provider en cooldown: Until = now + cooldown + jitter(0..jitter).
func (s *CooldownStore) Enter(id string, cooldown, jitter time.Duration)

// InCooldown reporta si el provider está en cooldown ahora.
func (s *CooldownStore) InCooldown(id string) bool

// Expire limpia entradas vencidas. Lo llama un time.Ticker (1 min) o
// lazy-check dentro de InCooldown (el ticker solo evita crecimiento).
func (s *CooldownStore) Expire()
```

- **Inyección:** `Clock` (interface con `Now() time.Time`) y `*rand.Rand`
  inyectables — los tests usan fake clock y rand fijo para no ser flaky.
- **Duración:** `provider.Cooldown` (override) > `fallback.cooldown`
  (default 60s) + `rand(0..fallback.cooldown_jitter)` (default 5s).
- **Jitter:** evita thundering herd cuando varios requests fallan contra el
  mismo provider a la vez (todos lo reintentarían al mismo segundo).
- **Reset:** un 200 exitoso NO limpia la entrada explícitamente (expira
  sola), pero resetea el contador de fallos para métricas. (MVP: allowed_fails
  fijo en 1 — un solo fallo retryable mete en cooldown. Sin campo nuevo en
  config; el contador queda listo para 005/ajustes futuros.)
- **Concurrencia:** un solo `sync.Mutex` alcanza (5 providers, decision de
  research-architecture.md §1). El request snapshottea el estado de cooldown
  bajo lock y luego intenta SIN lock (no bloquear la red con el mutex).

### Interfaz del router

```go
// internal/router/router.go
package router

// Resolve decide el provider para un request. Attempts = intentos ya
// consumidos (para el límite max_retries). Devuelve el provider elegido y
// su configuración, o un error si no hay candidatos disponibles.
type Router struct {
    cfg     *config.Config
    cd      *CooldownStore
    metrics MetricsSink // opcional, no-op si nil (impl real en 005)
    logger  *slog.Logger
}

func New(cfg *config.Config, opts ...Option) *Router

// PickNext devuelve el siguiente provider que sirve el modelo, no está en
// cooldown y no se intentó ya. Es la única función de decisión; el bucle
// de intentos vive en internal/proxy (001-001) usando PickNext +
// IsProviderFailure.
func (r *Router) PickNext(model string, attempted []string) (config.ProviderConfig, error)

// ReportFailure informa un fallo retryable (entra en cooldown) o
// no-retryable (solo contadores/log).
func (r *Router) ReportFailure(pc config.ProviderConfig, retryable bool)

// ReportSuccess resetea el contador de fallos del provider.
func (r *Router) ReportSuccess(id string)

// MetricsSink: contratos mínimos para 005-001 (no-op en MVP).
type MetricsSink interface {
    Attempt(provider string, ok bool)
    Fallback(provider string, reason string)
    Cooldown(provider string, until time.Time)
}
```

- **División de responsabilidades:** el router decide (PickNext + clasifica),
  el proxy (001-001) ejecuta el bucle de intentos (llamar al provider, leer
  respuesta, decidir si sigue). Alternativa válida si 001-001 prefiere un
  `router.RoundRobin(ctx, req)` monolítico: el proxy delega todo el ciclo.
  Cualquiera de las dos, pero el contrato de clasificación (IsProviderFailure
  / IsRetryableStatus) y el cooldown (CooldownStore) son únicos y viven acá.

## Interfaz consumida (de 001-002)

```go
// internal/config — campos relevantes para esta feature
type ProviderConfig struct {
    ID       string        `yaml:"id"`
    BaseURL  string        `yaml:"base_url"`
    APIKeyEnv string       `yaml:"api_key_env"`
    Models   []string      `yaml:"models"`
    Cooldown time.Duration `yaml:"cooldown"` // override, 0 = usar global
    Timeout  time.Duration `yaml:"timeout"`  // consumido por 001-006
    // ...
}
type FallbackConfig struct {
    MaxRetries     int           `yaml:"max_retries"`     // default 2
    Cooldown       time.Duration `yaml:"cooldown"`        // default 60s
    CooldownJitter time.Duration `yaml:"cooldown_jitter"` // default 5s
}
```

## Integración

- `internal/config` (001-002) expone el Config tipado; el router se
  construye una sola vez al arranque (no por request).
- `internal/provider` (cliente OpenAI-compatible): cada intento usa el ctx
  con timeout de 001-006 (`AttemptContext`); el router clasifica el error
  resultante (`ErrTimeout` → retryable).
  ⚠️ **Orden de implementación:** `ErrTimeout` está definido en 001-006
  (internal/timeouts) pero 001-003 lo consume en su clasificación. Si 003
  se implementa antes que 006, definir primero el TIPO en internal/timeouts
  (o un paquete de bajo nivel compartido) — la feature completa de 006
  puede ir después; el tipo no.
- `internal/proxy` (001-001): el endpoint delega en el router; si
  `PickNext` agota la cadena → 502 upstream_error; si el error es
  no-retryable → lo devuelve tal cual al cliente.
- `internal/metrics` (005-001): el router llama a `MetricsSink` si el
  proxy le inyectó una impl (no-op por defecto — cero dependencias en el
  MVP).
- `internal/logging` (005-002): el router loguea cada intento con
  `log/slog` (stdlib, sin dependencias): provider id, status/error,
  duración, intento N/max.

## Fuera de scope (explicitamente NO en 001-003)

- Semáforos de in-flight por provider → 004-001-concurrencia.
- Retry con backoff exponencial dentro de un mismo provider → 002-001-retry.
- Health checks periódicos → 002-002-health.
- Alias mapping de modelos (virtual → real) → EPIC-004 (solo si hace falta).
- Persistencia del cooldown (DB/Redis) → fuera del programa (epímero por
  diseño, research-architecture.md §1).
- Métricas Prometheus y logging JSON → EPIC-005.
