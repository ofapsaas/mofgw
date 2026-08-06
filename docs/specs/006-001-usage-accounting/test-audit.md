# Test Audit — 006-001-usage-accounting

> Auditoría de suite existente vs postcondiciones del spec (P1-P6).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído;
> trade-off declarado: riesgo de sesgo mínimo porque el AUDIT es read-only
> y la feature extiende patrones ya establecidos en 003-001).

## Mapeo suite existente → postcondiciones

| Test existente | Qué verifica hoy | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_003001_test.go TestRED_MetricsCacheTokens` | 3 requests no-stream → `mofgw_cache_hit_tokens_total 3000`, miss 600, reasoning 90 (provider up1, model m, cliente "test") | Parcial P1 (cache fields) + patrón P3 | No prompt/completion/total; no verifica dimensión cliente |
| `e2e_003001_test.go TestE2E_StreamUsageCapturadoEnMetrics` | Stream chunk final → hit 400/miss 100 | Parcial P1 (stream) | Idem |
| `e2e_003001_test.go TestRED_MetricsCacheTokensSinUsageSumaCero` | Sin usage → cache_hit_total 0 | Parcial P2/P1 (ausencia) | No verifica prompt/completion/total en 0 |
| `provider_test.go TestCompleteParseCacheFields` | Usage parsea cached/reasoning | Base P1 (parseo ya hecho) | — |
| `proxy/e2e_test.go TestE2EDebugPrivacidadYEventos` | request_end con status_code; privacidad | Patrón P4 | No prompt/completion/total en request_end |
| `metrics/metrics_test.go TestNamesSorted` | 8 contadores base (post-003-001) | — | Se ajustará con nuevos contadores (justificar en spec) |

## Gap analysis (cobertura faltante → tests RED)

| Postcondición | Cobertura actual | Test RED necesario |
|---|---|---|
| **P1** prompt/completion/total acumulados por request (no-stream) | ❌ | 2 requests mismo cliente+provider+model con usage → contadores prompt/completion/total = suma |
| **P1** prompt/completion/total acumulados (stream) | ❌ | Stream con chunk usage → totales acumulados |
| **P2** dimensión cliente: 2 clientes distintos → agregados separados | ❌ | 2 claves distintas → contadores por client|provider|model separados; clientID vacío → label `unknown` |
| **P3** exposición en /metrics (totales + desagregado) | Parcial (cache) | Verificar líneas `mofgw_prompt_tokens_total` etc. + desagregado con client label |
| **P4** request_end con prompt/completion/total | ❌ | request_end loguea prompt_tokens/completion_tokens/total_tokens |
| **P5** no rompe 003-001 | ✓ (tests existentes) | Re-verificar cache_hit/miss/reasoning_total siguen intactos tras el cambio |
| **P6** privacidad (labels = clientID hasheado, nunca contenido) | ✓ (005-005) | Los labels de cliente usan el clientID — sin contenido |

## Convenciones a seguir en RED (del patrón 003-001)

1. Fake upstream con `usage` completo (prompt/completion/total/cached/reasoning) — patrón `upstreamUsageOK` de e2e_003001_test.go.
2. Harness E2E (`build`) con 2 clientes distintos: `auth.New([]auth.Client{{ID:"c1",...},{ID:"c2",...}})` — verificar cómo se configura el harness para multi-cliente (e2e_test.go build → authz).
3. GET /metrics + `strings.Contains` con valores exactos (patrón existente).
4. `buildWithLogger` para request_end con campos nuevos.
5. Zero red externa; suite completa -race.

## Veredicto

P1 (totales prompt/completion), P2 (dimensión cliente), P3 (exposición),
P4 (request_end) tienen **cero cobertura** → tests RED necesarios. P5 se
re-verifica con la suite existente tras el cambio (no romper 003-001).
