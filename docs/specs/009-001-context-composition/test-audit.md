# Test Audit Report — 009-001-context-composition

**Rol:** cdad-test-writer · **Sub-fase:** 3.0 AUDIT (retrospectivo, post-hoc) · **Fecha:** 2026-08-09
**Fuentes:** `docs/specs/009-001-context-composition/spec.md` (aprobado, P1-P8/C1-C16/I1-I7), suite `internal/**/*_test.go` + `cmd/**/*_test.go`, `docs/.cdad-state.json`, contexto verificado por el orquestador (commits b81c0cd RED / 6e28730 fix de test; suite GREEN 294 tests).
**Restricción de ejecución (declarada):** bash denegado en la sesión del rol (read-only). La verificación empírica de suite verde (294 tests, `-race`) fue ejecutada por el orquestador y se toma de su contexto; la auditoría de este documento es estática sobre tests + spec, con lectura de los archivos de test involucrados.

---

## 1. Resumen del comportamiento que cambia

La feature **agrega** composición estructural de contexto: parser `composition.Analyze` (superset de `Walk`), records `ContextRecord` por sesión/cliente en metrics, endpoint `GET /v1/context`, config `context.analysis`, y persistencia `stateVersion` 1→2. Es **aditiva** salvo UN cambio que toca comportamiento validado por tests existentes:

| Cambio | Ref. spec | Efecto sobre tests |
|---|---|---|
| `stateVersion` 1→2 en `persistedState` (persist.go:32) | **P5** (líneas 200-210), **C12** (líneas 290-292) | **1 test existente**: `internal/metrics/persist_test.go` → `TestPersistVersionReject` (línea 184) reemplaza el literal de versión del snapshot guardado. El bump cambia el fixture (`"version":1` → `"version":2`) → el replace era no-op y el test fallaba por la razón equivocada. **Único test a modificar.** |
| `persistedState` gana `Contexts` (aditivo, `omitempty`, nil-safe para v1) | P5 | Sin efecto: los tests de persist existentes no aseveran ausencia de campos; `TestPersistAtomicWrite` usa la constante `stateVersion` (self-adapting, persist_test.go:254). |
| `composition.Analyze` nuevo (superset de `Walk`; `Walk` queda intacto) | P1/C3 | Aditivo. `walk_test.go` (009-000) untouched; C3 verifica explícitamente `Analyze(...).KeyPaths/Detected == Walk(...)` (refactor sin cambio de schema). |
| `ContextRecord` + mapa `contexts` propio (FIFO `max_sessions_retained`, ring buffer) | P2/P4 | Aditivo sobre metrics. NO agrega contadores Prometheus (out of scope, spec línea 378) → `TestNamesSorted` (metrics_test.go:34, exactamente 12 contadores) untouched y verde. |
| Endpoint `GET /v1/context` (mux `Handler()`) | P3 | Ruta nueva aditiva; sin conflicto con rutas existentes (suite verde lo confirma). |
| Config `context.analysis` aditivo (defaults `enabled=false`, `history_per_session=50`; valida `< 0`) | P6 | Aditivo; configs existentes sin bloque siguen parseando con defaults (verificado por `TestContextAnalysisConfig_Defaults`). |
| Setter `SetContextAnalysis` + wiring en main | P6/P8 | No cambia firma de `proxy.New` (spec línea 110: "NO cambia la firma de New") → **cero call-sites de tests a tocar**. |

**No cambia:** `Walk` (009-000), telemetría (canal separado), metrics contadores, auth/router/limiter/stream/clamp/timeouts (P8 no invasivo), headers X-Session-Id solo lectura (I4), privacidad (P7 — nunca contenido).

---

## 2. Cronología honesta del ciclo (post-hoc) y lección

El gate 3→4 exige `test-audit.md`; la feature ya está GREEN (294 tests). El ciclo real fue:

1. **RED (sesión A, commit b81c0cd):** se escribieron los 5 archivos de tests nuevos (P1-P8 vía C1-C16). El AUDIT previo al RED **no se ejecutó como sesión formal** y, por lo tanto, **no detectó que P5 (stateVersion 1→2) cambia el fixture de `TestPersistVersionReject`** — un test que manipula el literal de versión del snapshot guardado. Debía haberlo detectado: el spec declara el bump (P5, líneas 200-210) y el test existente dependía del literal `"version":1`.
2. **GREEN (implementer):** al correr la suite, `TestPersistVersionReject` falló (el snapshot se guardaba como `"version":2`, el replace de `"version":1` era no-op, LoadState ya no erraba). El implementer **correctamente no tocó el test** (AP-4) y lo reportó.
3. **Fix de test (sesión B, commit 6e28730):** el test-writer actualizó el fixture a `"version":2` → `"version":99`, preservando la intención (versión mayor rechazada + estado intacto tras el rechazo). Suite quedó verde (294).

**Lección documentada (regla "deuda documentada ≠ deuda oculta"):** el AUDIT de una feature con bump de schema debe escanear tests que dependen de **literales de fixture que el spec cambia**, no solo call-sites de firma (como hizo 009-000). El mapeo test↔postcondición fue auditado por sesiones distintas (RED: sesión A; fix: sesión B; este AUDIT: rol aislado read-only), cumpliendo el invariante anti-bias del ciclo.

---

## 3. Inventario de la suite relevante

### 3.1 Tests NUEVOS de la feature (RED commit b81c0cd)

| Archivo | Postcondiciones | Criterios |
|---|---|---|
| `internal/composition/analyze_test.go` | P1, P7 | C1-C4 |
| `internal/metrics/context_test.go` | P2, P4, P6 | C5-C11, C14 |
| `internal/metrics/persist_context_test.go` | P5 | C12 |
| `internal/config/context_analysis_test.go` | P6 | C10 |
| `internal/proxy/e2e_009001_test.go` | P2, P3, P4, P6, P7 | C5-C10, C13, C15 |

### 3.2 Harness reutilizado (SIN modificar)

- `e2e_009000_test.go`: helpers `harness`, `upstream`, `upstreamOK`, `chatWithHeaders` (línea 81), `parseJSONLines`/`waitForMsg` — **reutilizados por e2e_009001_test.go sin cambios de comportamiento** (confirmado por lectura).
- `e2e_test.go`: `build` (usado en `TestE2E009001_DisabledByDefault` para el caso disabled), `harness`, `upstream` base.
- `e2e_008001_test.go`: `bigBody` (usado en `TestE2E009001_RejectedRequest`).
- `config_test.go`: `validYAML` (usado por `parseContextConfig` en context_analysis_test.go).

Los helpers NUEVOS del feature (`buildContextAnalysis`, `buildContextMultiClient`, `getContext`, `waitContextBody`, `findRoleAgg`, `findPartAgg`, upstreams `upstreamUsagePrompt`/`upstreamStreamUsage`) viven **dentro de e2e_009001_test.go** — no se tocó ningún harness existente.

---

## 4. Mapeo postcondición → cobertura (tests nuevos + modificado)

| Postcondición | Cobertura | Tests |
|---|---|---|
| **P1** — Parser estructural (Roles, PartTypes, NumTools, TotalBytes, EstTokens=Bytes/4, maxDepth, nunca contenido) | ✅ | `TestAnalyze_StructuralComposition` (C1), `TestAnalyze_PrivacyNoContent` (C2), `TestAnalyze_BackwardCompatWithWalk` (C3), `TestAnalyze_MaxDepthTruncates` (C4) |
| **P2** — Acumulación por sesión/cliente (record `client\|session` + `client\|`, agregados, ring buffer, FIFO) | ✅ | `TestContextRecord_BasicAccumulation` (C5), `TestContextRecord_ClientIsolation` (C6), `TestContextRecord_ClientAggregatesAll` (C7), `TestContextRecord_RingBuffer` (C11), `TestContextRecord_EvictionFIFO` (C14); E2E: `TestE2E009001_SessionAccumulation` (C5), `TestE2E009001_ClientIsolation` (C6), `TestE2E009001_ClientAggregatesAll` (C7), `TestE2E009001_NoSessionClients` (C15) |
| **P3** — Endpoint `GET /v1/context` (scope session/client, summary con pct, history, latest, 404, ceros/vacío nunca nil) | ✅ | E2E: `TestE2E009001_SessionAccumulation` (C5), `TestE2E009001_ClientIsolation` (C6), `TestE2E009001_ClientAggregatesAll` (C7), `TestE2E009001_TwoPhaseUsage` (C8), `TestE2E009001_RejectedRequest` (C9), `TestE2E009001_DisabledByDefault` (C10), `TestE2E009001_PrivacyGrepNegative` (C13), `TestE2E009001_NoSessionClients` (C15); unit: `TestContextSnapshot_Disabled` (C10) |
| **P4** — prompt_tokens_actual en DOS fases (fase 1 pre-request, fase 2 por request_id, best-effort) | ✅ | Unit: `TestContextRecord_UpdateUsage` (C8), `TestContextRecord_UpdateUsageMissingRequest` (matcheo exacto/no-op); E2E: `TestE2E009001_TwoPhaseUsage` (C8, stream y no-stream), `TestE2E009001_RejectedRequest` (C9, entry sin actual) |
| **P5** — Persistencia stateVersion 1→2 (backward compat v1, round-trip, version:2, reject futuro) | ✅ | `TestPersistContextV2_BackwardCompatV1` (C12), `TestPersistContextV2_RoundTrip` (C12), `TestPersistContextV2_VersionBump` (C12), `TestPersistContextV2_RejectFutureVersion` (C12); **modificado:** `TestPersistVersionReject` (persist_test.go:184, C12/intención "versión mayor rechazada") |
| **P6** — Config `context.analysis` (defaults, validación `< 0`, `0` válido, disabled → ceros/vacío) | ✅ | `TestContextAnalysisConfig_Defaults` (C10), `TestContextAnalysisConfig_ValidateNegative` (C10), `TestContextAnalysisConfig_ValidateZero` (C10); E2E: `TestE2E009001_DisabledByDefault` (C10) |
| **P7** — Privacidad (nunca contenido en records ni state file) | ✅ | `TestAnalyze_PrivacyNoContent` (C2); E2E: `TestE2E009001_PrivacyGrepNegative` (C13, grep negativo en `/v1/context` session+client) |
| **P8** — No invasivo + bounds + suite verde | ✅ | Bounds: `TestAnalyze_MaxDepthTruncates` (C4), `TestContextRecord_RingBuffer` (C11), `TestContextRecord_EvictionFIFO` (C14). No invasivo: `TestE2E009001_DisabledByDefault` (el tráfico funciona igual con análisis off) + suite completa (I7). **C16** (suite `-race` verde, vet, gofmt) verificada empíricamente por el orquestador (294 tests) — no es un test dedicado, es la suite misma (precedente 009-000). |

**Sin tests por completitud:** todos los tests nuevos mapean a postcondición/criterio del spec (verificado test por test; los casos "extra" como `TestContextRecord_UpdateUsageMissingRequest` y `TestContextAnalysisConfig_ValidateZero` verifican cláusulas explícitas de P4/P6 — matcheo exacto, no-op, 0 = sin history).

---

## 5. Tests modificados (POST-AUDIT — ya aplicado en GREEN)

| Test / archivo | Línea | Modificación | Justificación (ref. spec) |
|---|---|---|---|
| `internal/metrics/persist_test.go` → `TestPersistVersionReject` | 184 | El replace pasó de `"version":1` → `"version":99` a `"version":2` → `"version":99` (commit 6e28730) | **P5** (spec líneas 200-210: `stateVersion = 2`, persist.go:32) y **C12** (líneas 290-292: "save → version: 2"). El bump cambia el literal del snapshot que el test manipula; sin la actualización el replace era no-op y el test fallaba por la razón equivocada (LoadState exitoso en vez de error). **Intención intacta**: versión mayor rechazada (líneas 190-191) + estado no tocado tras el rechazo (líneas 193-195). |

Estado esperado en el ciclo: tras la modificación el test debía quedar GREEN (el código ya bumpeó la versión); en el AUDIT previo al RED (que no se ejecutó formalmente, ver §2) debía marcarse como "a modificar" y quedar RED hasta el GREEN.

**No hay tests existentes que validen comportamiento viejo que la feature ELIMINE** (feature aditiva salvo el bump, que es exactamente este caso).

---

## 6. Tests nuevos (lista, RED commit b81c0cd — 5 archivos, 27 tests)

**`internal/composition/analyze_test.go` (P1/P7, C1-C4):**
- `TestAnalyze_StructuralComposition` (C1)
- `TestAnalyze_PrivacyNoContent` (C2)
- `TestAnalyze_BackwardCompatWithWalk` (C3)
- `TestAnalyze_MaxDepthTruncates` (C4)

**`internal/metrics/context_test.go` (P2/P4/P6, C5-C11/C14):**
- `TestContextRecord_BasicAccumulation` (C5)
- `TestContextRecord_ClientIsolation` (C6)
- `TestContextRecord_ClientAggregatesAll` (C7)
- `TestContextRecord_RingBuffer` (C11)
- `TestContextRecord_EvictionFIFO` (C14)
- `TestContextRecord_UpdateUsage` (C8)
- `TestContextRecord_UpdateUsageMissingRequest` (P4, matcheo exacto)
- `TestContextSnapshot_Disabled` (C10)

**`internal/metrics/persist_context_test.go` (P5, C12):**
- `TestPersistContextV2_BackwardCompatV1`
- `TestPersistContextV2_RoundTrip`
- `TestPersistContextV2_VersionBump`
- `TestPersistContextV2_RejectFutureVersion`

**`internal/config/context_analysis_test.go` (P6, C10):**
- `TestContextAnalysisConfig_Defaults`
- `TestContextAnalysisConfig_ValidateNegative`
- `TestContextAnalysisConfig_ValidateZero`

**`internal/proxy/e2e_009001_test.go` (P2/P3/P4/P6/P7, C5-C10/C13/C15):**
- `TestE2E009001_SessionAccumulation` (C5)
- `TestE2E009001_ClientIsolation` (C6)
- `TestE2E009001_ClientAggregatesAll` (C7)
- `TestE2E009001_TwoPhaseUsage` (C8; subtests stream y no-stream)
- `TestE2E009001_RejectedRequest` (C9)
- `TestE2E009001_DisabledByDefault` (C10)
- `TestE2E009001_PrivacyGrepNegative` (C13)
- `TestE2E009001_NoSessionClients` (C15)

---

## 7. Tests untouched (lista explícita)

**33 archivos completamente untouched** (el archivo `persist_test.go` se lista aparte por su test modificado):

- **`internal/composition/walk_test.go`** — todos los tests de 009-000 (Walk intacto; C3 los valida como base de backward compat).
- **`internal/metrics/metrics_test.go`** — todos, incluido `TestNamesSorted` (12 contadores — protege que la composición NO agregue contadores Prometheus, out of scope spec línea 378) y `TestRender`.
- **`internal/proxy/e2e_test.go`** — todos (harness base `build`/`harness`/`upstream` reutilizados SIN modificar).
- **`internal/proxy/e2e_009000_test.go`** — todos (harness `chatWithHeaders`/`parseJSONLines`/`waitForMsg` reutilizados sin cambios de comportamiento).
- **`internal/proxy/e2e_003001_test.go`** — todos (transparencia/stream_options, red de seguridad de I7/P8).
- **`internal/proxy/e2e_006001_test.go`**, **`e2e_006002_test.go`**, **`e2e_007002_test.go`**, **`e2e_007003_test.go`** — todos.
- **`internal/proxy/e2e_008001_test.go`**, **`e2e_008002_test.go`**, **`e2e_008003_test.go`** — todos (incl. `TestRED_SessionHeaderNoUpstream`: X-Session-Id sigue solo lectura, I4).
- **`internal/proxy/e2e_limiter_test.go`** — todos.
- **`internal/config/config_test.go`**, **`metadata_red_test.go`**, **`telemetry_red_test.go`**, **`external_review_fixes_test.go`** — todos (el bloque `context.analysis` es aditivo; defaults no alteran configs existentes).
- **`internal/logging/logging_test.go`**, **`telemetry_red_test.go`**, **`sec001_red_test.go`** — todos.
- **`cmd/mofgw/sec001_red_test.go`**, **`cmd/mofgw/telemetry_red_test.go`** — todos.
- **`internal/limiter/limiter_test.go`**, **`sec001_red_test.go`**; **`internal/router/router_test.go`**; **`internal/stream/stream_test.go`**, **`sec001_red_test.go`**; **`internal/clamp/clamp_test.go`**; **`internal/timeouts/timeouts_test.go`**; **`internal/auth/auth_test.go`**; **`internal/provider/provider_test.go`**; **`internal/health/health_test.go`**; **`internal/absorb/absorb_test.go`** — todos.

**Dentro del archivo modificado (`internal/metrics/persist_test.go`), 6 tests untouched:**
`TestPersistRoundTrip`, `TestPersistEmpty`, `TestPersistCorrupt`, `TestPersistMissing`, `TestPersistSessionEviction`, `TestPersistAtomicWrite` (solo `TestPersistVersionReject` fue modificado).

---

## 8. Evaluación de riesgo de regresión

**Riesgo global: BAJO.**

| Riesgo | Severidad | Mitigación / estado |
|---|---|---|
| `TestPersistVersionReject` roto por el bump (replace no-op) | Alta (potencial) | **Ya detectado y corregido** (6e28730). El riesgo real era que quedara oculto; queda documentado en §2. Guard futuro: `TestPersistContextV2_*` cubren el contrato P5 completo. |
| Firma `proxy.New` rota | — | No aplica: spec línea 110 "NO cambia la firma de New". Cero call-sites a tocar (a diferencia de 009-000). |
| Fuga de contenido (I1/P7) | Alta | C2 + C13 (grep negativo en Analyze y en `/v1/context` session+client) + enforcement por construcción del parser. |
| `TestNamesSorted` (12 contadores) | Media | Out of scope explícito (spec línea 378: solo endpoint dedicado). Test untouched verde como guard de regresión. |
| Config `context.analysis` rompe configs existentes | Baja | Aditivo con defaults; `TestContextAnalysisConfig_Defaults` verifica parse de configs sin bloque y con bloque sin `analysis`. |
| `persistedState` + `contexts` rompe round-trip existente | Baja | `omitempty` + nil-safe v1 (C12); `TestPersistRoundTrip`/`AtomicWrite` no aseveran ausencia de campos y quedaron verdes. |
| Ruta `/v1/context` conflictúa con mux existente | Baja | Ruta nueva; suite completa verde (I7) lo confirma. |
| Rechazo por ventana/limiter registra igual (C9) | Baja | `TestE2E009001_RejectedRequest` (400 de context_window + entry con est sin actual). |
| X-Session-Id se propaga upstream (I4) | Baja | `e2e_008003` untouched (guard existente); P3 usa X-Session-Id solo para keying. |

**Sin riesgo de regresión:** ruteo/fallback/clamp/timeouts (P8 no invasivo, cubierto por `TestE2E009001_DisabledByDefault` + suite), telemetría (canal separado, walk_test.go untouched), eventos operativos, privacidad (P7).

---

## 9. Observaciones y desviaciones documentadas (deuda documentada ≠ deuda oculta)

1. **AUDIT previo al RED no ejecutado formalmente (lección):** el gate 3→4 se cierra con este documento post-hoc. La corrección de `TestPersistVersionReject` quedó evidenciada en GREEN y fue documentada (§2), no oculta.
2. **Sub-fase PROPERTIES no invocada:** el spec declara invariantes I1-I7, pero todos están verificados por tests deterministas de ejemplo (I1→C2/C13; I2→C10/P8; I3→C6; I4→e2e_008003 untouched; I5→C4/C11/C14; I6→C12; I7→suite). Consistente con el precedente del repo (009-000 también declaró invariantes y cerró el gate sin property tests). Si el orquestador requiere verificación aleatorizada de I1/I5, es hardening post-cierre (deuda registrada, no bloquea 3→4).
3. **Acceso a campo interno en `TestPersistContextV2_BackwardCompatV1`** (persist_context_test.go:39-44: `m.contextsMu`/`m.contexts`): verifica un campo de layout **documentado en el spec** (línea 346: `contexts map[string]*ContextRecord`); no es mock sobre plumbing (no stubea comportamiento, no asevera orden de llamadas, no congela diseño más allá del spec). La alternativa observable (`ContextSnapshot(client, "")` → ok=false, vacío) cubre el mismo contrato — se recomienda para hardening futuro, pero no bloquea el gate.
4. **Evidencia empírica del gate:** suite 294 tests verdes reportada por el orquestador (bash denegado en sesión del rol, declarado en §1).

---

## 10. Gate checklist 3→4 (TDD → Review)

- [x] **Test Audit completado y aprobado** — este documento (post-hoc; beneficio de duda del AUDIT previo — `TestPersistVersionReject` — resuelto y documentado en §2).
- [x] **Cada test modificado tiene justificación explícita en spec.md** — P5 (líneas 200-210) + C12 (líneas 290-292), §5.
- [x] **Toda postcondición tiene al menos un test** — P1-P8 cubiertas (§4).
- [x] **Todo test mapea a postcondición** — sin tests por completitud (§4).
- [x] **Sin mocks sobre plumbing** — API pública (`Analyze`, `RecordContext`, `ContextSnapshot`, `Parse`, `GET /v1/context`) + upstreams fake en `httptest` (fixture, no mock interno); una observación menor documentada (§9.3).
- [x] **Mapeo test↔postcondición auditado por sesión distinta** — RED sesión A; fix sesión B; este AUDIT rol aislado read-only.
- [x] **Suite verde empíricamente** — 294 tests verdes (orquestador; §1/§9.4).
- [x] **Invariantes** — I1-I7 por tests deterministas (decisión documentada §9.2; precedente 009-000).
- [x] **Criterios E2E verdes** — `e2e_009001_test.go` (C5-C10, C13, C15) dentro de los 294.
- [x] **Commits granulares** — b81c0cd (RED, 5 archivos), 6e28730 (fix test), GREEN por implementer.

**Veredicto:** el gate 3→4 puede cerrarse. La feature es aditiva con un único cambio de fixture ya corregido y documentado; el mapeo test↔postcondición está completo (P1-P8, C1-C16 vía 27 tests nuevos + 1 modificado + suite).
