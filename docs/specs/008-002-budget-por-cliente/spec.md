# Spec — 008-002-budget-por-cliente: límite de tokens/costo por cliente

---
feature_id: 008-002-budget-por-cliente
epic: mofgw-008-contexto
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 006-001 (IncUsage por cliente), 006-002 (IncCost), 001-007-auth (clientID)
paralelizable: no
---

## Contexto

La medición (006-001/006-002) ya contabiliza tokens y costo por cliente.
EPIC-008 lleva la medición a la acción: límites. Esta feature define
**budgets por cliente** — un tope de consumo (tokens o costo) en una ventana
de tiempo. Al exceder el budget, los requests del cliente se rechazan con
429 (rate_limit_exceeded) hasta que la ventana rote.

Es el "diente" del sistema de medición: sin budget, la medición es
informativa; con budget, el operador puede acotar el gasto de un cliente
(útil para crons descontrolados, agentes en loop, o clientes que pagan).

Diseño (decisión de alcance): budget por costo USD o por tokens totales,
con ventana de tiempo configurable. Simplicidad: un budget por cliente con
dos límites opcionales (cost_usd_max, tokens_max) y una ventana (por
ejemplo diaria). La contabilidad ya existe en metrics (IncCost/IncUsage por
cliente) — el budget consulta el snapshot del cliente (SnapshotClient,
007-003) contra el límite.

## Postcondiciones

1. **P1 — Config de budget por cliente:** el config YAML acepta
   `clients[].budget: {cost_usd_max: N, tokens_max: N, window: "24h"}`.
   Ambos límites opcionales (solo uno, o ambos). Sin budget → cliente sin
   límite (backward compatible). `window` default 24h.

2. **P2 — Evaluación en el request:** ANTES de procesar un request, el
   gateway evalúa si el consumo acumulado del cliente en la ventana excede
   algún límite. Si excede → 429 `rate_limit_exceeded` con mensaje claro
   ("budget exceeded: cost $X > max $Y" o "budget exceeded: tokens N >
   max M"), SIN tocar el router ni upstream. La ventana es deslizante
   (acumulado desde now-window) — la contabilidad actual (en memoria, sin
   timestamps por request) NO permite ventana deslizante exacta → se usa
   ventana simple (desde el arranque del proceso, o desde el primer request
   del cliente) documentada como aproximación. Decisión: ventana simple
   (acumulado total desde start), con la ventana de config documentada como
   rotación manual (reinicio del proceso o comando de reset).

3. **P3 — Límite por costo:** si `cost_usd_max > 0` y el costo acumulado
   del cliente ≥ límite → 429. El costo es el estimado de 006-002 (misma
   fuente que /v1/usage).

4. **P4 — Límite por tokens:** si `tokens_max > 0` y los tokens totales
   acumulados ≥ límite → 429. Los tokens son el total de 006-001 (misma
   fuente que /v1/usage).

5. **P5 — Aislamiento y robustez:** el budget aplica por clientID del token
   (001-007). Cliente sin budget configurado → nunca rechazado por budget.
   El chequeo es informativo y no bloquea el procesamiento normal cuando el
   budget no se excede.

## Invariantes

- I1: El budget NO altera el contrato de respuesta cuando no se excede —
  solo agrega un 429 en el caso de exceso.
- I2: Los límites vienen de config (data), no hardcodeados.
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: La ventana es simple (acumulado desde start) — documentado como
  aproximación; la ventana deslizante exacta requiere timestamps por request
  (futuro, 008-003 stats por sesión lo habilita).

## Criterios de aceptación

- C1: Cliente con budget cost_usd_max 0.01 → tras requests que acumulen
  ≥ 0.01 → el siguiente request da 429 rate_limit_exceeded; antes del
  límite → 200.
- C2: Cliente con budget tokens_max 3000 → tras acumular ≥ 3000 → 429.
- C3: Cliente sin budget → nunca 429 por budget (aunque consuma mucho).
- C4: Dos clientes: uno con budget agotado → 429; el otro sin budget →
  200 (aislamiento).
- C5: El 429 es OpenAI-compatible (error.type = rate_limit_exceeded) y no
  toca upstream (gotBody vacío).
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`: `BudgetConfig` en ClientConfig
  (cost_usd_max, tokens_max, window duration).
- `internal/proxy/proxy.go`: después del chequeo de ventana (008-001) y
  antes del router, evaluar el budget del clientID con SnapshotClient.
  Config de budget inyectada al Server (map clientID → budget, setter o
  vía config en New). Si excede → 429 OpenAI-compatible + IncRejected
  (reason "budget_cost"/"budget_tokens").
- `internal/metrics/metrics.go`: sin cambios (SnapshotClient ya existe) —
  salvo que el 429 por budget también sea observable.
- Tests: e2e_008002_test.go (C1-C5).

## Fuera de alcance

- Ventana deslizante exacta (requiere timestamps por request — 008-003).
- Reset automático de ventana (rotación) — documentado como reinicio.
- Alertas/notificaciones al exceder budget (solo rechazo).
- Budget global (suma de clientes) — por cliente por decisión.
