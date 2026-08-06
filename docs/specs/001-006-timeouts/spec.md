# Spec — 001-006-timeouts: Timeout configurable por intento

---
feature_id: 001-006-timeouts
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-002-config
paralelizable: sí
---

```yaml
context: >
  Un provider caído o degradado puede colgar un request para siempre: el
  cliente del proxy (OpenClaw, agentes clientes, crons) se queda esperando sin
  feedback. El proxy debe imponer timeouts por intento: si un provider no
  responde en el tiempo configurado, el intento se aborta y se clasifica
  como fallo del provider (para que 001-003 haga fallback al siguiente).
  El timeout clave es TTFB (time-to-first-byte): desde que se envía el
  request hasta que llega el primer byte del response (o el primer evento
  SSE). Una vez que el stream arrancó, ya no aplica TTFB: aplica el
  write_timeout del server para el stream completo.
resolution: >
  Cada intento hacia un provider corre con su propio context con timeout.
  El valor por intento sale de ProviderConfig.Timeout (override
  per-provider) o de FallbackConfig/el default global si no está seteado.
  El timeout mide TTFB: se cancela si no hay primer byte antes de vencer.
  Un timeout se clasifica como fallo del provider (error tipado
  ErrTimeout) para que el router de 001-003 lo trate igual que un 429/5xx.
  En streaming, el TTFB se mide hasta el primer evento SSE; después del
  primer byte el stream sigue gobernado por ServerConfig.WriteTimeout.
postcondition: >
  Ningún intento a un provider queda colgado más allá de su timeout
  configurado. Un provider que no responde en TTFB genera ErrTimeout
  clasificado como fallo del provider. En streaming, el timeout aplica
  solo hasta el primer byte; el resto del stream lo gobierna
  write_timeout. Semántica 3 estados del timeout (ver 001-002 §Esquema):
  campo ausente → fallback.timeout (default 120s); 0 explícito → sin
  timeout (solo corta write_timeout); N → N.
verification:
  - go test ./internal/timeouts/ → 0 failures (sin red externa)
  - httptest fake que duerme 200ms + timeout 50ms → ErrTimeout, medido
    < 200ms (la cancelación corta el intento, no espera al sleep)
  - httptest fake que responde en 10ms + timeout 50ms → éxito, sin error
  - ProviderConfig.Timeout=0 + FallbackConfig.Timeout=0 → sin timeout
    (el intento espera lo que haga falta; solo lo corta write_timeout)
  - Streaming: fake que tarda 30ms al primer evento SSE + timeout 50ms →
    éxito; fake que nunca emite primer evento + timeout 50ms → ErrTimeout
  - ErrTimeout implementa una interfaz de clasificación compartida con
    los errores de 001-003 (IsProviderFailure(err) bool → true)
```

## Contrato de API

La feature NO expone endpoints nuevos. Aporta el mecanismo de timeout y
la clasificación de errores para el router (001-003).

```go
// internal/timeouts/timeouts.go
// TimeoutFor resuelve el timeout de un intento: per-provider > global
// > default (120s). Devuelve 0 si el timeout está explícitamente
// desactivado (valor 0 en config).
func TimeoutFor(pc config.ProviderConfig, fc config.FallbackConfig) time.Duration

// AttemptContext crea el context de un intento con su timeout aplicado.
// TTFB: el context se cancela si no llega primer byte antes de vencer.
func AttemptContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc)

// ErrTimeout: error tipado del intento.
type ErrTimeout struct {
    Provider string
    Attempt  int
    Timeout  time.Duration
}
func (e *ErrTimeout) Error() string
// IsProviderFailure → true (contrato 001-003: timeout = provider caído)
```

### Integración
- `internal/provider/` (cliente OpenAI-compatible): cada `Complete`/`Stream`
  recibe un ctx ya creado con `AttemptContext`. El cliente NO crea timeouts
  por su cuenta: recibe el ctx con la política ya aplicada.
- `internal/router/` (001-003): al construir el ctx de cada intento, usa
  `TimeoutFor` + `AttemptContext`; si el error es `ErrTimeout` (o el ctx
  expiró), clasifica como fallo del provider y sigue la cadena.
- En streaming: `AttemptContext` cubre hasta el primer evento SSE (el
  cliente lo reporta al leer el primer chunk). El resto del stream lo
  gobierna `ServerConfig.WriteTimeout` (001-001).
- `internal/config` (001-002) expone `ProviderConfig.Timeout` y
  `FallbackConfig.Timeout` (ver schema en spec 001-002). Si 001-002 aún no
  define el campo global, el default es 120s y el override per-provider
  es el único campo configurable.

## Interfaz consumida (de 001-002)

```go
// internal/config — campos relevantes para esta feature
type ProviderConfig struct {
    ID      string        `yaml:"id"`
    Timeout time.Duration `yaml:"timeout"` // override per-provider, 0 = usar global
    // ...
}
type FallbackConfig struct {
    Timeout *time.Duration `yaml:"timeout"` // global por intento, default 120s, 0 = sin timeout
    // ...
}
```

## Seguridad
- Los timeouts evitan el agotamiento de goroutines/conexiones por providers
  colgados: cada intento libera su conexión al vencer.
- No loguear bodies en el path de timeout: solo `{provider, attempt,
  timeout, elapsed}`.

## No-funcionales
- Determinismo: la política de timeout es pura (config → duración).
- Coste: un `context.WithTimeout` por intento. Despreciable.
- El timeout NO debe depender del reloj de pared del cliente: se mide desde
  el envío del request al primer byte del upstream.

## Fuera de scope (features hermanas)
- Fallback en cadena + cooldown → 001-003 (consume la clasificación de
  errores; el retry/backoff entre intentos es de esa feature)
- Clamp de max_tokens → 001-004
- Streaming con retry pre-primer-byte → 001-005
- Metrics Prometheus (timeout_count, ttfb_histogram) → EPIC-005
- Retry con backoff exponencial DENTRO del mismo provider → EPIC-002
  (transparencia total). En el MVP un timeout es un fallo del provider:
  se pasa al siguiente, no se reintenta el mismo.

## Notas de implementación (para el implementer)
- Paquete puro `internal/timeouts/` + tests con `httptest.Server` fake que
  duerme lo configurable (patrón ya validado en kernel-ia, cero red real).
- Medir TTFB real: `time.Since(start)` al recibir el primer chunk; el
  test del "fake que duerme" debe verificar que la cancelación corta el
  intento ANTES de que el sleep del fake termine (assert elapsed < sleep).
- Distinguir `ctx deadline exceeded` del cliente vs del intento: si el ctx
  del cliente (OpenClaw) expira, NO es fallo del provider — es el cliente
  que se fue. La clasificación solo marca ErrTimeout cuando expira el
  timeout del intento (el interno), no el ctx entrante.
- E2E del epic (provider colgado → fallback al siguiente) es del epic, no
  de esta feature; acá basta el fake.

## Estado
- [ ] Spec aprobada (requiere aprobación del plan de epics, P1)
- [ ] Implementación MVP
- [ ] Tests verdes + E2E manual con un provider real
