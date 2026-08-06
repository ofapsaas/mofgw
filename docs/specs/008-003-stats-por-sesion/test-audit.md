# Test Audit — 008-003-stats-por-sesion

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada).

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_007003_test.go` usage endpoint | /v1/usage totals por cliente | Base P3 (endpoint + aislamiento) | No ?session= |
| `e2e_test.go` X-Agent-Id tests | Header de solo lectura no upstream | Patrón I1/C5 | No X-Session-Id |
| `e2e_006001_test.go harness` | chatAs con headers custom | Infraestructura | — |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P3/C1** stats por sesión | ❌ | 3 requests con X-Session-Id s1 → /v1/usage?session=s1 con requests 3 + tokens |
| **P2/C2** aislamiento por client+session | ❌ | 2 clientes con mismo session id → cada uno ve lo suyo |
| **P3** lista de sesiones en /v1/usage | ❌ | /v1/usage sin query lista sessions |
| **C4** session inexistente → 404 | ❌ | /v1/usage?session=nope → 404 |
| **C5** X-Session-Id no upstream | ❌ | gotBody sin el header |

## Convenciones

1. `buildMultiClient` + `chatAs` — chatAs debe aceptar headers extra
   (X-Session-Id) — el helper actual no los acepta; extender el test helper
   (chatAsWithHeaders) sin romper los existentes.
2. upstreamOK gotBody para verificar no-forward del header.
3. Zero red externa; suite completa -race.

## Veredicto

P1-P5 sin cobertura → RED.
