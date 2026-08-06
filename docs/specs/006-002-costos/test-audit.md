# Test Audit — 006-002-costos

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada — runtime de delegación caído). La feature extiende patrones ya
> establecidos en 006-001, riesgo de sesgo mínimo.

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_006001_test.go TestRED_TotalesAcumuladosPorCliente` | Acumulación prompt/completion/total por cliente | Base P3 (key client\|provider\|model) | No costo |
| `e2e_006001_test.go TestRED_NoRompeCache003001` | Contadores cache intactos | Patrón C5 | No costo |
| `e2e_006001_test.go TestRED_ClientesSeparados` | Dimensión cliente separada | Base P3 | No costo |
| `metrics_test.go TestNamesSorted` | 11 contadores | — | Se ajustará a 12 con justificación |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P2/C1** cálculo exacto: prompt 1M miss + 1M cached + 500K completion con precios 0.14/0.28/0.028 → $0.308 | ❌ | Request con usage exacto + config pricing → `mofgw_cost_usd_total 0.308` |
| **P2/C2** modelo sin pricing → costo 0 | ❌ | Harness sin pricing → costo 0, sin error |
| **P3/C3** acumulación por cliente en /metrics | ❌ | 2 requests mismo cliente → costo = 2×; desagregado `mofgw_cost_usd{client,...}` |
| **C4** request sin usage → costo 0 | ❌ | upstreamNoUsage → costo 0 |
| **C5** no rompe 006-001 | ✓ (suite) | Re-verificar tras cambio |

## Convenciones

1. Harness E2E (`buildMultiClient`/`buildWithLogger`) — el proxy.New debe
   recibir la tabla de precios (nueva firma o config); ajustar harness.
2. upstreamUsageOK (prompt 1200, completion 80, cached 1000) — valores fijos
   para cálculos exactos; para C1 usar un upstream con usage específico.
3. `strings.Contains` sobre /metrics con valores %f.
4. Zero red externa; suite completa -race.

## Veredicto

P2 (cálculo), P3 (acumulación), C4 (sin usage) sin cobertura → RED. C5 se
re-verifica con la suite tras el cambio.
