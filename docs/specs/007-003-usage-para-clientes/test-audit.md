# Test Audit — 007-003-usage-para-clientes

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada).

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_006001_test.go TestRED_TotalesAcumuladosPorCliente` | IncUsage acumula por cliente | Base P3 (datos por cliente) | No endpoint |
| `e2e_006002_test.go TestRED_CostoAcumulaPorCliente` | IncCost acumula | Base P2 (costo) | No headers |
| `e2e_003001_test.go TestRED_LogsCacheFields` | usage en request_end | Base P1 (usage disponible) | No headers |
| `e2e_test.go` auth tests | 401 sin auth en /v1/* | Patrón P5/C4 | No /v1/usage |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P1/C1** headers de usage en respuesta no-stream | ❌ | Request con usage → headers X-Usage-* con valores exactos |
| **P1/C2** sin usage → headers 0 | ❌ | Request sin usage → headers 0 |
| **P4/C5** stream: headers antes del primer byte | ❌ | Request stream → headers presentes (0 o usage del request) |
| **P3/C3** /v1/usage con agregados por cliente + aislamiento | ❌ | 2 clientes con consumo distinto → cada /v1/usage muestra lo suyo |
| **C4** /v1/usage sin auth → 401 | ❌ | GET /v1/usage sin Bearer → 401 |

## Convenciones

1. `buildMultiClient` (006-001) para clientes múltiples.
2. `chatAs` para requests con key distinta.
3. Assert de headers: `resp.Header.Get("X-Usage-Prompt-Tokens")`.
4. GET /v1/usage con Bearer → JSON parseado.
5. Zero red externa; suite completa -race.

## Veredicto

P1 (headers no-stream), P4 (headers stream), P3 (endpoint + aislamiento),
C4 (401) sin cobertura → RED.
