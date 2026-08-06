# Test Audit — 008-001-rechazo-por-ventana

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada).

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_007002_test.go` | metadata disponible en Server (SetModelMetadata) | Base P1 (context_window accesible) | No rechazo |
| `e2e_test.go` 400 tests | errores OpenAI-compatible | Patrón C5 | No contexto |
| `e2e_006001_test.go harness` | buildMultiClient con proxySrv | Infraestructura | — |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P1/C1** rechazo temprano 400 sin intentos | ❌ | Modelo con context_window chico + body grande → 400, upstream NO recibió request (gotBody vacío) |
| **P2/C2** request que cabe → normal | ❌ | Mismo modelo + body chico → 200, upstream recibió |
| **P4/C3** sin metadata → sin rechazo | ❌ | Modelo sin metadata + body grande → 200 |
| **P3/C4** margen respetado | ❌ | margin 0.5 → request estimado dentro del margen → 200 |
| **C5** error OpenAI-compatible | ❌ | 400 con error.message/error.type=invalid_request_error |

## Convenciones

1. `buildMultiClient` + `SetModelMetadata` (007-001) para context_window.
2. `upstreamOK` con gotBody para verificar "cero intentos".
3. Body grande: mensaje con string repetido (len controlable).
4. Zero red externa; suite completa -race.

## Veredicto

P1-P5 sin cobertura → RED. Se re-verifica la suite completa tras el cambio
(no romper flujo normal).
