# Test Audit — 007-001-model-metadata

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada). Feature de datos (config), riesgo de sesgo mínimo.

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `config/config_test.go` | Carga de config, validación de providers | Patrón P1/P4 (load + validación) | No model_metadata |
| `e2e_006002_test.go buildPricing` | SetPricing inyección | Patrón P2 (inyección) | No SetModelMetadata |
| `e2e_006001_test.go TestRED_NoRompeCache003001` | No romper features previas | Patrón C5 | — |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P1/C1** metadata en config cargada y tipada | ❌ | Config YAML con model_metadata → load OK + acceso tipado (context_window, thinking) |
| **P2/C2** modelo sin metadata → vacía, sin error | ❌ | Config sin model_metadata → metadata vacía, ruteo normal |
| **P4/C3** validación: thinking_default fuera de thinking → error al cargar | ❌ | Config inválida → Load falla con error claro |
| **P4** valores negativos rechazados | ❌ | context_window: -1 → error |
| **C4** metadata + pricing combinados | ❌ | Ambos seteados → ambos accesibles; uno solo → otro vacío |
| **C5** request con/sin metadata idéntico | ❌ | E2E: request a modelo con metadata vs sin → misma respuesta |

## Convenciones

1. `config_test.go` patrón existente (load YAML → assert).
2. `buildPricing`/harness — SetModelMetadata via proxySrv.
3. Zero red externa; suite completa -race.

## Veredicto

P1, P2, P4, C4, C5 sin cobertura → RED. C5 se re-verifica con la suite tras
el cambio (no romper ruteo).
