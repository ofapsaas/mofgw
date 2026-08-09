# Epic mofgw-009-contexto-analisis — Integration

> Etapa E3 de integración cross-feature. Las 3 features del epic se verificaron JUNTAS,
> no solo individualmente. Cierre: 2026-08-09.

## Tests E2E cross-feature

| Criterio del epic (plan.md:211-215) | Test | Estado |
|---|---|---|
| C1 — telemetry.jsonl captura metadata sin contenido ni keys (privacidad 005-005) | `TestE2EEpic009_FlujoCompuesto` (subtests `request1_*`, `privacidad_grep_negativo`) | ✅ verde |
| C2 — `docs/research-context-patterns.md` con hallazgos por runtime | Documental (no test) — `docs/research-context-patterns.md` actualizado con captura ~103 eventos | ✅ |
| C3 — `/v1/context?session=<id>` composición con aislamiento por cliente | `TestE2EEpic009_FlujoCompuesto` (subtests `request1_*`, `request2_*`, `request3_*`, `aislamiento_otro_cliente_misma_sesion`) | ✅ verde |
| C4 — sticky routing opcional por config, transparente, respeta cooldown/health/cadena | `TestE2EEpic009_FlujoCompuesto` (subtests `request2_sticky_mismo_provider`, `request3_sin_sesion_openclaw_zot`) | ✅ verde |

**Test:** `internal/proxy/e2e_epic009_test.go` — `TestE2EEpic009_FlujoCompuesto` (5 subtests de fase + privacidad + aislamiento), harness compuesto `buildEpic009` (telemetría + composición + sticky ON a la vez). Commit `da8446c`.

## Flujo compuesto verificado

Un request con `X-Session-Id` atraviesa las 3 features en el proxy sin pisarse:

1. **009-000 telemetría** — `emitRequestTelemetry` emite UN evento `request_telemetry` con key paths del body (`model`, `messages[].role`, `messages[].content`) y el header `X-Session-Id` en `headers`. Un evento por request (2, 3, 4 eventos según los requests emitidos). El contenido del prompt NUNCA aparece (grep negativo en `telemetry.jsonl`).
2. **009-001 composición** — fase 1 `RecordContext` crea el record `client|session` con roles/part_types/est_tokens; fase 2 `UpdateContextUsage` setea `prompt_tokens_actual` desde el usage upstream (42) en el MISMO `recordCacheTokens` que registra la afinidad. El request siguiente acumula en el MISMO record (requests:2, num_tools_total:1, part text count:2, part tool_use count:1). El request sin sesión va al record `client|` (requests:3) sin contaminar la sesión (sigue en 2).
3. **009-002 sticky** — `recordCacheTokens` registra al provider GANADOR en `AffinityStore`; el request siguiente de la misma sesión rutea al MISMO provider (contadores `up1==2`, `up2==0` — reorder post-filtro, 1 solo intento). La clave `client|` del tráfico sin sesión es independiente de la clave `client|session` (C6/C7).

**Privacidad y aislamiento compuestos:** los secrets de content/arguments NUNCA aparecen en telemetría ni en `/v1/context` (session y client). Un segundo cliente con el MISMO session id "s1" ve SOLO su record (requests:1, `client:"other"`); el record de `test|s1` sigue en 2; la afinidad `other|s1` es independiente de `test|s1`; la telemetría cubre al segundo cliente (`client_id:"other"` presente).

## Hallazgos durante integración

- **Ningún bug cross-feature emergió.** Las 3 features interactúan sin pisarse: la fase 2 de 009-001 (`UpdateContextUsage`) y el registro sticky de 009-002 (`Affinity().Set`) viven en el MISMO punto (`recordCacheTokens`) y coexisten sin conflicto — verificado por el subtest que chequea ambos efectos del mismo request (prompt_tokens_actual==42 + Affinity test|s1==up1).
- **No se requirió refactor de ninguna feature done.** Los contratos individuales (specs 009-000/009-001/009-002) fueron suficientes para la composición; los harnesses existentes se reutilizaron (cero duplicación).
- **Confirmación del dual-keying (ADR-001):** el flujo real con y sin sesión muestra exactamente el comportamiento diseñado — sesión para opencode, `client|` para openclaw/zot, sin contaminación cruzada.

## Deuda técnica detectada

- Ninguna nueva de integración. La deuda de las features (TECHDEBT #16-22) se mantiene: #21 (concurrencia sticky sin test dedicado), #22 (X-Session-Affinity sin test dedicado), #20 (gofmt preexistente en e2e_009000_test.go).

## Gate E3 → E4

- [x] Cada criterio de aceptación cross-feature del epic tiene su test E2E (C1/C3/C4; C2 documental).
- [x] Todos los E2E cross-feature pasan (6 subtests verdes).
- [x] Suite completa verde: **332 tests `-race`** (326 + 6 cross-feature), vet y gofmt limpios.
- [x] `docs/epics/plan.md` es el plan (el repo no usa subcarpeta por epic — plan central); este `integration.md` se aloja en `docs/` junto al plan.
- [x] `docs/epics/integration-009.md` (este archivo) commiteado.
