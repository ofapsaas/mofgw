# Spec — 001-003-fallback v2: max_retries como tope de intentos circulares

---
feature_id: 001-003-fallback-v2
epic: mofgw-001-core (ajuste)
status: draft (aprobado por Pablo en sesión, 05 Ago 2026, Opción 2)
created_at: 2026-08-05
updated_at: 2026-08-05
depends_on: 001-003-fallback (semántica previa)
paralelizable: no
---

## Contexto

Problema verificado en producción: la semántica actual de `max_retries` limita
a `max_retries + 1` intentos sobre UNA pasada de la cadena (en
`internal/router/router.go`, `Complete()` y `Stream()`:
`for i, idx := range ready { if attempts >= r.maxAttempts { break } }`). Con 5
providers que sirven el modelo y `max_retries: 2`, solo se prueban 3 → si los
primeros 3 fallan, el cliente ve 502 en el primer request aunque haya
providers vivos más abajo en la cadena. El fallback depende del retry del
cliente, NO del proxy: contradice el principio rector de mofgw — "el cliente
nunca se entera".

Decisión de Pablo (05 Ago 2026, Opción 2): **`max_retries` debe ser un tope de
intentos TOTALES por request con recorrido CIRCULAR de la cadena**. Con
`max_retries: 9` y 5 providers → hasta 10 intentos = 2 vueltas completas a cada
provider. **El cooldown solo aplica ENTRE requests**: dentro del mismo request
NO se saltea un provider que falló (se reintenta circularmente); el cooldown
registrado (`recordFailure`) afecta solo a requests posteriores.

Comportamiento esperado:

- `max_retries >= len(providers) - 1` → el proxy agota TODA la cadena en un
  solo request (una vuelta completa garantizada), y puede dar más vueltas si
  `max_retries` es mayor.
- Si TODOS los intentos (`maxAttempts = max_retries + 1`) fallan → 502
  `upstream_error` (como hoy).
- El cooldown intra-request NO filtra: un provider que falló en la vuelta 1 se
  reintenta en la vuelta 2 (si hay presupuesto de intentos).
- El cooldown entre requests SÍ filtra: un provider en cooldown de un request
  anterior no se intenta en el nuevo request (`candidates()` al inicio del
  request ya lo excluye — no cambiar).
- Clasificación retryable/no-retryable SIN CAMBIOS: 429/5xx/timeout/red =
  retryable (sigue el loop); 4xx de cliente = no-retryable (responde ya, sin
  reintentar).
- `provider_fallback` en el loop circular: el "próximo provider" es el
  siguiente índice circular (con wraparound).

## Postcondición

`max_retries` es un tope de intentos TOTALES por request con recorrido
CIRCULAR de la cadena de candidatos. El cooldown solo opera entre requests
(intra-request no filtra). Con `max_retries >= N - 1` providers, la cadena se
agota completa en un solo request. La clasificación retryable/no-retryable no
cambia. Todos los tests pasan con `go test -race` (cero red externa).

## Cambios de código esperados (orientación, no vinculante)

- `internal/router/router.go`, `Complete()` y `Stream()`: reemplazar el loop
  `for i, idx := range ready { if attempts >= r.maxAttempts { break } ... }`
  por un loop que itere circularmente sobre `ready`
  (`idx := ready[attempts % len(ready)]`) mientras `attempts < r.maxAttempts`.
  El `provider_fallback` debe calcular el próximo índice con wraparound.
- El cooldown se sigue registrando (`recordFailure`) pero NO se verifica
  intra-request: un provider que falló en la vuelta 1 se vuelve a intentar en
  la vuelta 2 si queda presupuesto de intentos.
- Atención: `candidates()` al inicio del request ya excluye cooldowns previos
  (entre requests) — no cambiar eso.
- El resto del contrato queda intacto: clasificación de errores (contrato
  compartido con 001-006), 502 con body OpenAI-compatible sin detalles
  internos, y streaming con fallback solo antes del primer byte.

## Verification

- `go test ./... -race` → 0 failures (suite con providers fake en httptest,
  cero red externa).
- Test multi-pasada: 3 providers, `max_retries: 8` (`maxAttempts = 9`) → cada
  provider se intenta 3 veces (intentos 1,4,7 / 2,5,8 / 3,6,9); total 9
  intentos.
- Test una-vuelta-completa: 5 providers, `max_retries: 4` (`maxAttempts = 5`)
  → cada provider se intenta 1 vez; el request llega al último provider.
- Test cooldown entre requests (no romper): provider que falló queda en
  cooldown; el request siguiente lo saltea (TestCooldownSkipsFailedProvider
  existente debe seguir pasando).
- Test retryable/no-retryable sin cambios: 4xx de cliente responde ya sin
  reintentar (TestClientErrorNoFallback existente).
- Test todos fallan → 502 (TestAllProvidersFail existente).
- Test `max_retries: 0` → 1 solo intento (primer provider).

## Fuera de alcance

- Retry con backoff exponencial por provider (002-001) — otro epic.
- Health checks (002-002) — otro epic.
