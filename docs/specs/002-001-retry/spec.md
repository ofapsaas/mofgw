# Spec — 002-001-retry: Retry con backoff exponencial por intento

---
feature_id: 002-001-retry
epic: mofgw-002-resiliencia
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-003-fallback, 001-006-timeouts
paralelizable: no
---

```yaml
context: >
  La cadena de fallback de 001-003 prueba providers DISTINTOS cuando uno
  falla. Pero si el provider que falló es el único que sirve un modelo
  (o está momentáneamente degradado — un 429 transitorio, un 5xx aislado,
  un timeout de red de un segundo), saltar al siguiente provider puede ser
  peor que reintentar el mismo: el siguiente puede ser más caro, más lento,
  o no servir ese modelo. Esta feature agrega reintentos sobre el MISMO
  provider con backoff exponencial ANTES de ceder al siguiente de la
  cadena. El reintento es solo pre-primer-byte (misma regla que 001-003 y
  001-006): una vez emitido el primer byte, no se reintenta — el stream
  está comprometido. El objetivo es absorber fallos transitorios SIN
  sacrificar latencia percibida: backoff agresivo (creciente, con jitter)
  y tope duro de intentos configurable.
resolution: >
  Por cada intento hacia un provider que falle con error retryable
  (429, 5xx, ErrTimeout, error de red — el mismo clasificador de 001-003),
  el router reintenta el MISMO provider hasta `RetryConfig.MaxAttempts`
  intentos (default 2: primer intento + 1 reintento; 0 = sin reintentos),
  esperando entre intentos `backoff_base * 2^n` (n = intento fallido)
  con jitter ±20% y tope en `backoff_max` (default 5s). Si después de
  agotar MaxAttempts el provider sigue fallando, se aplica cooldown
  (001-003) y se cede al siguiente provider de la cadena. Un 429 con
  header `Retry-After` respeta ese valor (con tope en backoff_max) en
  lugar del backoff calculado. Un 4xx de cliente (400, 401, 403, 404)
  NO se reintenta jamás: es no-retryable y se devuelve al cliente
  inmediatamente. Los reintentos comparten el mismo budget de tiempo del
  intento: cada reintento corre con su propio context timeout TTFB
  (001-006); no hay un timeout global "de reintentos" en MVP — la suma
  de intentos queda acotada por MaxAttempts * TimeoutPerAttempt.
postcondition: >
  Un fallo transitorio del provider A (429 puntual, 5xx esporádico,
  timeout de red) se absorbe con reintentos sobre A sin llegar al
  provider B. Un fallo persistente de A agota MaxAttempts, aplica
  cooldown y cede a B (comportamiento 001-003 intacto). El cliente
  nunca ve reintentos internos: solo ve la respuesta final o el error
  agregado si TODA la cadena falló. Latencia adicional máxima por
  fallo transitorio ≈ (MaxAttempts-1) * (backoff + TTFB por intento),
  acotada por backoff_max y el tope de intentos.
verification:
  - go test ./internal/router/ → 0 failures (sin red externa)
  - httptest fake que falla con 429 una vez y luego 200 → el request
    llega a éxito con exactamente 2 llamadas al fake (1 fallo + 1
    reintento) y latencia ≈ backoff calculado
  - httptest fake que siempre 500 → MaxAttempts llamadas al fake,
    luego se prueba el siguiente provider (o 502 si es el último)
  - httptest fake con Retry-After: 5 → la espera es 5s (tope
    backoff_max si 5 > backoff_max)
  - test de jitter: 100 reintentos simulados → backoff siempre dentro
    de [backoff_base * 2^n * 0.8, backoff_base * 2^n * 1.2]
  - test 4xx (400): 1 sola llamada al fake, error al cliente al toque
  - test streaming: primer evento SSE recibido → reintentos
    desactivados, fallo mid-stream = evento de error (regla 001-003 §B)
contracts:
  Config: >
    RetryConfig { MaxAttempts int `yaml:"max_attempts"`, BackoffBase
    time.Duration `yaml:"backoff_base"`, BackoffMax time.Duration
    `yaml:"backoff_max"` } — anidado en FallbackConfig (001-002),
    con override per-provider opcional (ProviderConfig.Retry).
  Error clasificador: >
    Reutiliza IsRetryable(err) de 001-003: true para 429, 5xx,
    ErrTimeout, red; false para 4xx de cliente y errores de request.
  Cooldown: >
    Tras agotar MaxAttempts con error retryable → CooldownFor(provider)
    igual que en 001-003 (el conteo de fallos consecutivos incluye
    los reintentos fallidos).
out_of_scope:
  - Retry post-primer-byte (imposible por construcción, ver 001-003)
  - Retry-After parsing para cooldown largo (es de 002-004-degradacion)
  - Retry con backoff por provider con política distinta por error
  - Persistencia del contador de reintentos entre reinicios
notas_implementacion:
  - Paquete internal/router (mismo lugar que 001-003): la función de
    reintento envuelve la llamada de intento único de 001-003.
  - El jitter se calcula con math/rand (no crypto) — no es secreto.
  - Los reintentos deben respetar el context del cliente: si el cliente
    cancela (ctx.Done), abortar reintentos inmediatamente.
  - Métricas post-MVP (EPIC-005): contador de reintentos por provider.
```

## Referencias cruzadas
- 001-003-fallback: clasificador de errores retryable/no-retryable, cooldown, orden de cadena
- 001-006-timeouts: TTFB por intento; cada reintento = intento nuevo con su propio timeout
- research-architecture §3: opción A (reintentar solo hasta el primer byte) es la recomendación
- LiteLLM §5: `allowed_fails` / `retry_after` — referencia de mecánica
