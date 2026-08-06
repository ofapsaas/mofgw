# Test Audit — 008-002-budget-por-cliente

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada).

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_007003_test.go TestRED_UsageEndpointAislamiento` | SnapshotClient por cliente | Base P2-P4 (acumulado por cliente) | No budget |
| `e2e_006002_test.go` | IncCost acumula | Base P3 (costo) | No límite |
| `e2e_test.go` 429 tests (limiter) | 429 rate_limit_exceeded | Patrón C5 | No budget |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P3/C1** 429 por budget de costo | ❌ | Cliente con cost_usd_max 0.01 → tras acumular ≥ 0.01 → 429; antes → 200 |
| **P4/C2** 429 por budget de tokens | ❌ | Cliente con tokens_max 3000 → tras acumular → 429 |
| **P1/C3** sin budget → nunca 429 | ❌ | Cliente sin budget + consumo alto → 200 |
| **P5/C4** aislamiento entre clientes | ❌ | Cliente con budget agotado → 429; otro sin → 200 |
| **C5** 429 OpenAI-compatible sin upstream | ❌ | error.type=rate_limit_exceeded + gotBody vacío |

## Convenciones

1. `buildMultiClient` + SetBudget (nuevo setter en proxy, contrato del RED).
2. `chatAs` con key distinta; upstreamUsageOK con gotBody.
3. Pricing 1.0 para acumular costo rápido (upstreamUsageOK: prompt 1200,
   completion 80, cached 1000 → costo 0.00128 por request).
4. Zero red externa; suite completa -race.

## Veredicto

P3, P4, P1, P5 sin cobertura → RED.
