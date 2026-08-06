# Spec — 007-003-usage-para-clientes: exponer consumo a los clientes

---
feature_id: 007-003-usage-para-clientes
epic: mofgw-007-catalogo
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 006-001 (IncUsage por cliente), 006-002 (IncCost), 001-007-auth (clientID)
paralelizable: no
---

## Contexto

El frente 3 de Pablo: "los runtimes no tienen datos de tokens y cache
consumidos". Hasta ahora el consumo se acumula en /metrics (006-001/006-002),
pero el cliente (el runtime que llama) no ve su propio consumo:

- En **cada respuesta**: el runtime no sabe cuántos tokens consumió el
  request (el usage viene del upstream en el body no-stream, pero en
  streaming no — y no hay header estándar que lo exponga).
- **Acumulado**: no hay forma de que el cliente pregunte "¿cuánto gasté?"
  sin scrapear /metrics (que es global, no por cliente).

Esta feature expone el consumo en dos planos:

1. **Por request** (headers de respuesta): `X-Usage-Prompt-Tokens`,
   `X-Usage-Completion-Tokens`, `X-Usage-Total-Tokens`,
   `X-Usage-Cache-Hit-Tokens`, `X-Usage-Cost-USD` en cada respuesta de
   `/v1/chat/completions` (no-stream y stream). Formato simple, sin
   dependencias, legible por cualquier runtime.
2. **Acumulado por cliente** (endpoint `GET /v1/usage`): JSON con los
   agregados del cliente autenticado — tokens por modelo y costo total.
   Autenticado con el mismo Bearer; cada cliente ve SOLO lo suyo.

## Postcondiciones

1. **P1 — Headers de usage en respuestas:** toda respuesta exitosa de
   `/v1/chat/completions` (no-stream y stream) incluye los headers
   `X-Usage-Prompt-Tokens`, `X-Usage-Completion-Tokens`, `X-Usage-Total-Tokens`,
   `X-Usage-Cache-Hit-Tokens`, `X-Usage-Cost-USD` con los valores del request
   (0 si no hay usage). Los valores son los del usage upstream (los mismos
   que se acumulan en /metrics — misma fuente, 006-001).

2. **P2 — Costo en headers:** `X-Usage-Cost-USD` usa el mismo cálculo de
   006-002 (estimateCost) — consistencia entre lo que ve el cliente y lo
   que acumula /metrics.

3. **P3 — Endpoint /v1/usage:** `GET /v1/usage` con auth Bearer devuelve
   `{"client": "<id>", "totals": {"prompt_tokens": N, "completion_tokens":
   N, "total_tokens": N, "cost_usd": N, "cache_hit_tokens": N},
   "by_model": [{"model": "...", "prompt_tokens": N, "completion_tokens": N,
   "total_tokens": N, "cost_usd": N}]}`. Cada cliente ve SOLO sus propios
   agregados (aislamiento por clientID del token).

4. **P4 — Stream también expone headers:** en streaming, los headers se
   setean ANTES del primer byte (no se pueden cambiar después del flush) —
   los valores son los del request en curso o 0 si aún no hay usage. El
   usage del chunk final (que llega después del primer byte) NO puede ir en
   headers (ya flusheados) — va en el body SSE (ya llega hoy). Los headers
   de stream usan el usage acumulado hasta el primer byte (0 si el stream
   aún no trajo el chunk de usage).

5. **P5 — Robustez:** request sin usage → headers 0, /v1/usage responde
   siempre 200 con los agregados del cliente (aunque sean 0). Sin auth →
   401 (mismo que /v1/*).

## Invariantes

- I1: La exposición es informativa — nunca altera la respuesta, el fallback
  ni el streaming (los headers son metadatos).
- I2: Aislamiento por cliente en /v1/usage: un cliente no ve los agregados
  de otro (mismo clientID del token, 001-007).
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: El endpoint /v1/usage no expone contenido de prompts/respuestas —
  solo números agregados (privacidad, consistente con 005-005).

## Criterios de aceptación

- C1: Request no-stream con usage (prompt 1200, completion 80, cached 1000,
  costo 0.00128 con pricing 1.0) → headers X-Usage-Prompt-Tokens: 1200,
  X-Usage-Completion-Tokens: 80, X-Usage-Total-Tokens: 1280,
  X-Usage-Cache-Hit-Tokens: 1000, X-Usage-Cost-USD: 0.00128.
- C2: Request sin usage → headers 0.
- C3: GET /v1/usage con auth → JSON con los agregados del cliente; dos
  clientes distintos ven sus propios acumulados (aislamiento).
- C4: GET /v1/usage sin auth → 401.
- C5: Stream: headers seteados antes del primer byte; el usage del chunk
  final sigue llegando en el body SSE (comportamiento de 003-001 intacto).
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/proxy/proxy.go handleChat`: setear headers de usage en la
  respuesta (no-stream: tras Complete con el usage; stream: antes del
  primer byte, valores del request en curso). En no-stream, el usage ya
  está en `res.Response.Usage` y el costo en estimateCost.
- `internal/proxy/proxy.go`: handler `GET /v1/usage` que lee los agregados
  del clientID autenticado (el metrics acumula por client|provider|model —
  filtrar por el client del token). Agregar la ruta al mux (auth).
- `internal/metrics/metrics.go`: acceso a los agregados por cliente
  (snapshot del map por clientID) — método nuevo tipo `UsageSnapshot(client)
  (prompt, completion, total, cost, cacheHit int64, byModel [])`.
- Tests: e2e_007003_test.go (C1-C5).

## Fuera de alcance

- Dashboard/UI — el endpoint es la API; el dashboard es futuro.
- Paginación del by_model (cardinalidad acotada — sin necesidad).
- Push/notificaciones de consumo.
- Rate limiting por consumo (budget) — EPIC-008.
