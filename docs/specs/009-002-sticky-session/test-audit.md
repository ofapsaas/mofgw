# Test Audit Report — 009-002-sticky-session

**Rol:** cdad-test-writer · **Sub-fase:** 3.0 AUDIT (retrospectivo, post-hoc) · **Fecha:** 2026-08-09
**Fuentes:** `docs/specs/009-002-sticky-session/spec.md` (aprobado, P1-P9/C1-C13/I1-I7), tests nuevos `internal/config/sticky_red_test.go`, `internal/router/router_sticky_test.go`, `internal/proxy/e2e_009002_test.go`, suite existente `internal/**/*_test.go` + `cmd/**/*_test.go`, `docs/.cdad-state.json`, contexto verificado por el orquestador (commits d19177f RED / 1a94eb9 fixes de tests / febaea0 implementación; suite 326 tests `-race` verdes, verificado empíricamente).
**Restricción de ejecución (declarada):** sesión del rol read-only (bash denegado). La verificación empírica de suite verde (326 tests, `-race`, vet, gofmt) fue ejecutada por el orquestador y se toma de su contexto; la auditoría de este documento es estática sobre tests + spec, con lectura completa de los archivos de test de la feature y de los harness reutilizados.

---

## 1. Resumen del comportamiento que cambia

La feature es **aditiva**: agrega config `fallback.sticky_routing.enabled` (default `false`), el store `AffinityStore` en router, los métodos `CompleteFor`/`StreamFor` + accessor `Affinity()`, y el wiring `SetStickyRouting` en proxy. **Cero comportamiento existente cambia** — P8 (spec líneas 267-274) lo garantiza explícitamente y C11 lo verifica.

| Cambio | Ref. spec | Efecto sobre tests existentes |
|---|---|---|
| `FallbackConfig` gana `StickyRouting{Enabled bool}`, default `false`, sin reglas nuevas de `validate()` | P1 (158-167), C1 (305-306) | **Ninguno**: bloque ausente → false (cero cambio observable); configs existentes parsean igual (defaults). |
| `Router` gana `Options.StickyMaxEntries` (0 = default 100), campo `affinity`, accessor `Affinity()` | P3 (184-206) | **Ninguno**: campo nuevo opcional; `New` legacy intacto (P8). |
| `CompleteFor`/`StreamFor` nuevos (reorder post-`resolveReady` + loop existente) | P4 (208-225), P5 (227-238) | **Ninguno**: `Complete`/`Stream` legacy intactos; sin cambios de firma ni lógica de loop. |
| `proxy.SetStickyRouting(enabled)` + keying `clientID\|sessionID` / `clientID\|` | P2 (169-182), P6 (240-257) | **Ninguno**: setter nuevo pre-tráfico; `handleChat`/`recordCacheTokens` solo ramifican si el flag está on (default off → legacy exacto). |
| Wiring en `main.go` + `config.example.yaml` | P1, P8 | **Ninguno**: no cambia firmas de `New`/`NewWithOptions`; cero call-sites de tests a tocar. |

**No cambia:** `Complete`/`Stream`/`New` legacy, `candidates`/`resolveReady`, cooldown/health/Serves (I2 — la preferencia es solo reordenamiento post-filtro), limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta, P9 líneas 276-284), telemetría, keying X-Session-Id solo lectura (I4), contadores Prometheus (P7: sin métricas nuevas — solo log debug). **Cero tests existentes validan comportamiento viejo que la feature elimine o altere.**

---

## 2. Cronología honesta del ciclo (post-hoc) y lección

El gate 3→4 exige `test-audit.md`; la feature ya está GREEN (326 tests). El ciclo real fue:

1. **RED (sesión A, commit d19177f):** 3 archivos de tests nuevos (32 tests: P1 vía C1; P3/P4/P5 vía C2-C8/C12; C2-C12 e2e). El AUDIT previo al RED **no se ejecutó como sesión formal** — la feature es aditiva (cero tests existentes a modificar), pero 5 de los 32 tests nuevos nacieron con bugs de test.
2. **GREEN (implementer):** la suite no compilaba/pasaba; el implementer **detectó 3 de los 5 bugs y correctamente NO tocó los tests (AP-4)** — los reportó. El orquestador detectó los 2 restantes (sesión con diagnóstico).
3. **Fix de tests (sesión B, commit 1a94eb9):** el test-writer corrigió los 5 tests. Los 5 fixes están corroborados en el estado actual de los archivos (ver §5). Suite quedó GREEN (326, `-race`).

**Lección documentada (regla "deuda documentada ≠ deuda oculta"):** el aprendizaje de 009-001 fue "escanear fixtures que el spec cambia" (literales de `stateVersion`); acá se extendió a **"escanear que los contadores midan lo que el test afirma medir"**: (a) `fakeProvider.Stream` no incrementa el contador de Complete → `callCount()` no sirve para el path streaming (fix C8); (b) el health check de `buildSticky` (`o.Health.CheckNow()`) sondea a los upstreams y contamina `stickyUpstream.calls()` → `gotBody` mide solo requests de chat (fix C4); (c) `upstreamOK` solo trae `total_tokens` → costo 0 → el budget nunca dispara (fix C9). Los 2 bugs restantes son de timing/scope del assert (C6: asserts al final no prueban independencia entre requests; C10: prohibir `X-*` genéricamente es incorrecto porque `setUsageHeaders` de 007-003 escribe `X-Usage-*` siempre en el path no-stream exitoso). El mapeo test↔postcondición fue auditado por sesiones distintas (RED: sesión A; fixes: sesión B; este AUDIT: rol aislado read-only), cumpliendo el invariante anti-bias del ciclo.

---

## 3. Inventario de la suite relevante

### 3.1 Tests NUEVOS de la feature (RED d19177f, fixes 1a94eb9 — 3 archivos, 32 tests)

| Archivo | Postcondiciones | Criterios | Tests |
|---|---|---|---|
| `internal/config/sticky_red_test.go` | P1 | C1 | 3 |
| `internal/router/router_sticky_test.go` | P3, P4, P5 | C2-C8, C10, C12 | 18 |
| `internal/proxy/e2e_009002_test.go` | P2, P4, P5, P6, P7, P8, P9 | C2-C4, C6-C12 | 11 |

### 3.2 Harness reutilizado (SIN modificar)

- `internal/router/router_test.go`: `fakeProvider`/`newFake` (L27/40), `streamFake` (L161), `reqFor` (L185), `bodyFor` (L189), `parseLogLines` (L494), `specsOf` (L596), `newHealthStore` (L748) — **reutilizados por `router_sticky_test.go` sin cambios** (confirmado por lectura; el `streamCountingFake` nuevo es local a `router_sticky_test.go`).
- `internal/proxy/e2e_test.go`: `upstreamOK`/`upstreamStreamOK`/`upstreamFail` (L67-75), `build` (L91), `buildWithOptions` (L121, usado por `TestE2E009002_StickyOffLegacy`) — reutilizados sin cambios.
- `internal/proxy/e2e_009000_test.go`: `harness`, `chatWithHeaders` (L81), `waitForSubstring` (L126) — reutilizados sin cambios.
- `internal/proxy/e2e_009001_test.go`: `upstreamUsagePrompt` (L165, usado por el fix C9) — reutilizado sin cambios.
- `internal/config/config_test.go`: `validYAML`/`writeTemp` (usados por `sticky_red_test.go`) — reutilizados sin cambios.

Los helpers NUEVOS de la feature (`stickyUpstream`, `buildSticky`, `headerNameSetEqual` en e2e_009002_test.go; `streamCountingFake` en router_sticky_test.go; `stickyYAML` en sticky_red_test.go) viven **dentro de los archivos nuevos** — no se tocó ningún harness existente.

---

## 4. Mapeo postcondición → cobertura

| Postcondición | Cobertura | Tests |
|---|---|---|
| **P1** — Config `fallback.sticky_routing.enabled` (default false, no-bool → error) | ✅ | `TestPostcondition1_StickyEnabledTrue` (C1), `TestPostcondition1_StickyAusenteDefaults` (C1), `TestPostcondition1_StickyNoBoolError` (C1); off-path e2e: `TestE2E009002_StickyOffLegacy` (C11) |
| **P2** — Fuente y keying (`client\|session` / `client\|`, nunca vacía, X-Session-Id solo lectura) | ✅ | E2E: `TestE2E009002_AfinidadPorSesion` (C2, clave `test\|s1`), `TestE2E009002_ClienteSinSesion` (C7, clave `test\|` + sesión no usa afinidad `client\|`), `TestE2E009002_AislamientoDeSesiones` (C6, claves independientes); unit: `TestPostcondition4_SesionesIndependientes` (C6) |
| **P3** — `AffinityStore` (upsert, touch en Set/Get, LRU, cap default 100, Len ≤ cap, efímero, mutex) | ✅ | `TestPostcondition3_AffinityStore_SetGetUpsert`, `TestPostcondition3_AffinityStore_LRUEviction` (C12), `TestPostcondition3_AffinityStore_GetRefrescaLastUsed`, `TestPostcondition3_AffinityStore_ClaveActivaNoSeEvicta` (C12), `TestPostcondition3_AffinityStore_CapDefaultCien` (I6), `TestPostcondition3_AffinityAccessorEnRouter` (I5, arranque frío); E2E: `TestE2E009002_EviccionLRU` (C12) |
| **P4** — `CompleteFor` reorder post-filtro (preferido primero preservando el resto; no-aplica cooldown/health/Serves/clave ausente/vacía; 404 idéntico; loop legacy; log `sticky_applied`) | ✅ | `TestPostcondition4_PreferidoPrimero` (C2), `TestPostcondition4_SinAfinidadOrdenConfig` (P4/P8), `TestPostcondition4_PreferidoEnCooldownNoAplica` (C3/I2), `TestPostcondition4_PreferidoUnhealthyNoAplica` (C4/I2), `TestPostcondition4_PreferidoNoSirveModeloNoAplica` (C5/I2), `TestPostcondition4_ModelNotFoundConAfinidad`, `TestPostcondition4_SesionesIndependientes` (C6), `TestPostcondition4_StickyAppliedLog` (C10/D6); E2E: C2/C3/C4/C6/C7/C10 |
| **P5** — `StreamFor` reorder + frontera primer-byte (fallback pre-primer-byte con preferencia; sin reconexión post-primer-byte; stream de un solo provider) | ✅ | `TestPostcondition5_PreferidoPrimeroStream`, `TestPostcondition5_FallbackPrePrimerByteConPreferencia` (C8), `TestPostcondition5_SinMezclaPostPrimerByte` (C8), `TestPostcondition5_SinAfinidadFallbackLegacy` (P4/P8); E2E: `TestE2E009002_StreamingFallbackPrePrimerByte` (C8), `TestE2E009002_SinReconexionPostPrimerByte` (C8) |
| **P6** — Registro post-éxito del ganador en `recordCacheTokens` (gated por flag; no actualiza en fallo total / rechazos pre-router; stream arrancado registra; best-effort) | ✅ | E2E: `TestE2E009002_AfinidadPorSesion`, `TestE2E009002_FallbackCooldownActualizaAfinidad` (C3), `TestE2E009002_PreferidoUnhealthyNoAplica` (C4), `TestE2E009002_StreamingFallbackPrePrimerByte` (C8), `TestE2E009002_SinReconexionPostPrimerByte` (C8), `TestE2E009002_FalloTotalNoActualizaAfinidad` (C9), `TestE2E009002_StickyOffLegacy` (C11) |
| **P7** — Transparencia estricta (body/SSE byte-identical, mismos headers, sin headers nuevos, log debug) | ✅ | `TestE2E009002_TransparenciaByteIdentical` (C10/I1); log: `TestPostcondition4_StickyAppliedLog` (C10/D6) |
| **P8** — Backward compat total (legacy intactos; off → cero diferencias; e2e existentes pasan) | ✅ | `TestE2E009002_StickyOffLegacy` (C11); unit: `TestPostcondition4_SinAfinidadOrdenConfig`, `TestPostcondition5_SinAfinidadFallbackLegacy`; **suite completa** (I7) dentro de los 326 verdes |
| **P9** — No invasivo + bounds (preferencia solo post-filtro; nunca fuerza; cardinalidad acotada; suite `-race` verde; vet/gofmt) | ✅ | Bounds: `TestPostcondition3_AffinityStore_LRUEviction`, `TestPostcondition3_AffinityStore_CapDefaultCien`, `TestPostcondition3_AffinityStore_ClaveActivaNoSeEvicta`, `TestE2E009002_EviccionLRU` (C12). No invasivo: C3/C4/C5 (I2). Suite: C13 empírico (326 `-race`, vet, gofmt). Cláusula de concurrencia: observación §9.2. |

**Sin tests por completitud:** los 32 tests nuevos mapean a postcondición/criterio del spec, verificado test por test.

**Cobertura de criterios:** C1→3 unit; C2→unit+e2e; C3→unit+e2e; C4→unit+e2e; C5→unit; C6→unit+e2e; C7→e2e+unit; C8→2 unit+2 e2e; C9→e2e; C10→unit+e2e; C11→e2e+suite; C12→3 unit+1 e2e; C13→suite (empírico).

---

## 5. Tests modificados

### 5.1 Tests existentes modificados: **0 (cero)**

Feature aditiva; ningún test existente valida comportamiento que cambie (ver §1). No hay `TestPersistVersionReject`-like en esta feature: no hay bump de schema, cambio de firma, ni literal de fixture que el spec altere.

### 5.2 Fixes de tests NUEVOS (commit 1a94eb9 — 5 de los 32 tests, documentados como deuda)

| Test / archivo | Fix | Justificación (ref. spec) |
|---|---|---|
| `router_sticky_test.go` → `TestPostcondition4_SesionesIndependientes` | **C6:** los asserts de independencia se verifican ANTES del request de s2 (`p2.callCount() == 0` tras el request de s1), no al final | C6 (spec 318-320: "sin contaminación" entre claves). |
| `router_sticky_test.go` → `streamCountingFake` + `TestPostcondition5_FallbackPrePrimerByteConPreferencia` | **C8:** wrapper que cuenta intentos de `Stream` — `fakeProvider.Stream` no incrementa `calls["complete"]` | P5/C8 (spec 227-238, 324-327: fallback pre-primer-byte con preferencia). |
| `e2e_009002_test.go` → `TestE2E009002_PreferidoUnhealthyNoAplica` | **C4:** se usa `up1.gotBody`/`up2.gotBody` en vez de `stickyUpstream.calls()` — el `o.Health.CheckNow()` de `buildSticky` sondea a ambos upstreams y contamina el contador | C4 (spec 314-316: preferido unhealthy no aplica). |
| `e2e_009002_test.go` → `TestE2E009002_FalloTotalNoActualizaAfinidad` | **C9:** upstream `upstreamUsagePrompt("m", 1000)` para que el request exitoso acumule costo — `upstreamOK` solo trae `total_tokens` → costo 0 → el budget nunca excede | C9 (spec 328-331: rechazo por budget no toca afinidad). |
| `e2e_009002_test.go` → `headerNameSetEqual` + `TestE2E009002_TransparenciaByteIdentical` | **C10:** comparar el SET de nombres de headers sticky vs legacy en vez de prohibir `X-*` — `setUsageHeaders` de 007-003 escribe `X-Usage-*` SIEMPRE en el path no-stream exitoso | C10/I1/P7 (spec 332-334, 288-290: "mismos headers (sin headers nuevos)"). |

**Ningún assert relajado:** cada fix verifica exactamente el comportamiento que el spec pide. Los 5 fixes fueron detectados en GREEN (3 por el implementer, que respetó AP-4; 2 por el orquestador) y corregidos en sesión de test-writer aislada (sesión B).

---

## 6. Tests nuevos (lista — RED d19177f, 3 archivos, 32 tests)

**`internal/config/sticky_red_test.go` (P1, C1 — 3 tests):** `TestPostcondition1_StickyEnabledTrue`, `TestPostcondition1_StickyAusenteDefaults`, `TestPostcondition1_StickyNoBoolError`.

**`internal/router/router_sticky_test.go` (P3/P4/P5, C2-C8/C10/C12 — 18 tests):** P3: `TestPostcondition3_AffinityStore_SetGetUpsert`, `_LRUEviction`, `_GetRefrescaLastUsed`, `_ClaveActivaNoSeEvicta`, `_CapDefaultCien`, `TestPostcondition3_AffinityAccessorEnRouter`; P4: `TestPostcondition4_PreferidoPrimero`, `_SinAfinidadOrdenConfig`, `_PreferidoEnCooldownNoAplica`, `_PreferidoUnhealthyNoAplica`, `_PreferidoNoSirveModeloNoAplica`, `_ModelNotFoundConAfinidad`, `_SesionesIndependientes`, `_StickyAppliedLog`; P5: `TestPostcondition5_PreferidoPrimeroStream`, `_FallbackPrePrimerByteConPreferencia`, `_SinMezclaPostPrimerByte`, `_SinAfinidadFallbackLegacy`.

**`internal/proxy/e2e_009002_test.go` (C2-C4, C6-C12 — 11 tests):** `TestE2E009002_AfinidadPorSesion` (C2), `_FallbackCooldownActualizaAfinidad` (C3), `_PreferidoUnhealthyNoAplica` (C4), `_AislamientoDeSesiones` (C6), `_ClienteSinSesion` (C7), `_StreamingFallbackPrePrimerByte` (C8), `_SinReconexionPostPrimerByte` (C8), `_FalloTotalNoActualizaAfinidad` (C9), `_TransparenciaByteIdentical` (C10), `_StickyOffLegacy` (C11), `_EviccionLRU` (C12).

---

## 7. Tests untouched (lista explícita — TODO lo existente; 38 archivos)

- **`internal/router/router_test.go`** — todos, incluidos los helpers reutilizados SIN modificar.
- **`internal/proxy/e2e_test.go`** — todos (`build`, `buildWithOptions`, `upstreamOK`/`upstreamFail`/`upstreamStreamOK`).
- **`internal/proxy/e2e_009000_test.go`** — todos (harness `chatWithHeaders`/`waitForSubstring`).
- **`internal/proxy/e2e_009001_test.go`** — todos (incl. `upstreamUsagePrompt`).
- **`internal/proxy/e2e_003001_test.go`**, **`e2e_006001_test.go`**, **`e2e_006002_test.go`**, **`e2e_007002_test.go`**, **`e2e_007003_test.go`**, **`e2e_008001_test.go`**, **`e2e_008002_test.go`**, **`e2e_008003_test.go`**, **`e2e_limiter_test.go`** — todos.
- **`internal/config/config_test.go`**, **`context_analysis_test.go`**, **`metadata_red_test.go`**, **`telemetry_red_test.go`**, **`external_review_fixes_test.go`** — todos.
- **`internal/metrics/metrics_test.go`**, **`context_test.go`**, **`persist_test.go`**, **`persist_context_test.go`** — todos (P9: `stateVersion` intacta; P7: `TestNamesSorted` untouched).
- **`internal/composition/walk_test.go`**, **`analyze_test.go`** — todos.
- **`internal/limiter/limiter_test.go`**, **`sec001_red_test.go`**; **`internal/stream/stream_test.go`**, **`sec001_red_test.go`**; **`internal/clamp/clamp_test.go`**; **`internal/timeouts/timeouts_test.go`**; **`internal/auth/auth_test.go`**; **`internal/provider/provider_test.go`**; **`internal/health/health_test.go`**; **`internal/absorb/absorb_test.go`**; **`internal/logging/logging_test.go`**, **`sec001_red_test.go`**, **`telemetry_red_test.go`** — todos.
- **`cmd/mofgw/sec001_red_test.go`**, **`cmd/mofgw/telemetry_red_test.go`** — todos.

---

## 8. Evaluación de riesgo de regresión

**Riesgo global: BAJO.**

| Riesgo | Severidad | Mitigación / estado |
|---|---|---|
| Backward compat rota (legacy `Complete`/`Stream`/`New`) | Alta (potencial) | P8 + `TestE2E009002_StickyOffLegacy` (C11) + unit (`SinAfinidadOrdenConfig`, `SinAfinidadFallbackLegacy`) + **toda la suite existente untouched verde** (I7). |
| Preferido fuerza un provider en cooldown/unhealthy/que no sirve | Alta (potencial) | I2 verificado por 3 unit (C3/C4/C5) + 2 e2e (C3/C4). |
| Transparencia rota (headers/body distintos) | Alta (potencial) | C10 byte-identical + comparación de sets de headers + `sticky_applied` en logs. |
| Registro post-éxito pisa afinidad en fallos | Media | C9 + P6 gating por flag. |
| Cardinalidad / evicción | Media | C12 (3 unit + 1 e2e). |
| Concurrencia / race en el store | Media | P3 mutex + `-race` en suite (C13, empírico). Sin test dedicado de requests concurrentes — observación §9.2. |
| `X-Session-Affinity` leída por el sticky | Baja | I3/D5 por construcción (P2) + guard existente `e2e_008003` untouched. Sin test dedicado — observación §9.3. |
| Config nueva rompe configs existentes | Baja | Aditivo con default false; `TestPostcondition1_StickyAusenteDefaults`. |
| Keying incorrecto (mezcla `client\|session` vs `client\|`) | Baja | C6 + C7 + unit de touch. |

**Sin riesgo de regresión:** limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta), telemetría, contadores Prometheus (P7), firma de `proxy.New`/`NewWithOptions`.

---

## 9. Observaciones y desviaciones documentadas (deuda documentada ≠ deuda oculta)

1. **AUDIT previo al RED no ejecutado formalmente (lección):** el gate 3→4 se cierra con este documento post-hoc. Feature aditiva → beneficio de duda del AUDIT = cero por construcción. Los 5 bugs de tests NUEVOS detectados en GREEN quedan documentados (§2, §5.2), no ocultos.
2. **C13 — cláusula de concurrencia sin test dedicado:** no hay test que dispare requests concurrentes de la misma sesión con sticky on; la garantía es estructural (mutex de `AffinityStore`, P3) + `-race` sobre la suite. Test concurrente dedicado → hardening post-cierre (deuda registrada, no bloquea 3→4).
3. **I3 — cláusula "X-Session-Affinity irrelevante" sin test dedicado:** ningún test manda `X-Session-Affinity` y verifica que no influye en el keying; la garantía es por construcción (P2: el proxy solo lee `X-Session-Id`, D5) + guard existente `e2e_008003` untouched. Test dedicado → hardening post-cierre (deuda registrada, no bloquea 3→4).
4. **Sub-fase PROPERTIES no invocada:** invariantes I1-I7 verificados por tests deterministas. Consistente con el precedente del repo (009-000, 009-001). Property tests → hardening post-cierre si el orquestador lo requiere.
5. **Acceso a puntos de inyección documentados en el spec:** fake clock declarado en el spec (§Contexto técnico: "now inyectable"); `Affinity()`/`Affinity().Get` inspeccionados vía API pública (D2). No es mock sobre plumbing. Upstreams e2e son `httptest` reales (fixture, no mock interno).
6. **Evidencia empírica del gate:** suite 326 tests verdes `-race` + vet + gofmt limpios reportada por el orquestador (sesión del rol read-only, bash denegado, declarado en §1). Consistencia aritmética: 294 (cierre 009-001) + 32 nuevos = 326.

---

## 10. Gate checklist 3→4 (TDD → Review)

- [x] **Test Audit completado y aprobado** — este documento (post-hoc; beneficio de duda = cero tests existentes a modificar por feature aditiva; los 5 fixes de tests nuevos documentados en §2/§5.2).
- [x] **Cada test modificado tiene justificación explícita en spec.md** — 0 tests existentes modificados (P8); los 5 fixes de tests nuevos justificados contra C6/C8/C4/C9/C10 (§5.2).
- [x] **Toda postcondición tiene al menos un test** — P1-P9 cubiertas (§4).
- [x] **Todo test mapea a postcondición** — sin tests por completitud (§4).
- [x] **Sin mocks sobre plumbing** — API pública + upstreams `httptest` fake + fake clock documentado en spec (§9.5).
- [x] **Mapeo test↔postcondición auditado por sesión distinta** — RED sesión A; fixes sesión B; este AUDIT rol aislado read-only.
- [x] **Suite verde empíricamente** — 326 tests `-race` verdes (orquestador; §1/§9.6).
- [x] **Invariantes** — I1-I7 por tests deterministas (decisión documentada §9.4; precedente 009-000/009-001).
- [x] **Criterios E2E verdes** — `e2e_009002_test.go` (C2-C4, C6-C12) dentro de los 326.
- [x] **Commits granulares** — d19177f (RED, 3 archivos, 32 tests), 1a94eb9 (5 fixes de tests), febaea0 (GREEN implementer).

**Veredicto:** el gate 3→4 puede cerrarse. Feature aditiva con cero tests existentes modificados, 32 tests nuevos que cubren P1-P9 / C1-C13 (C13 vía suite empírica), 38 archivos untouched explícitamente listados, y los 5 bugs de tests detectados en GREEN documentados como deuda resuelta — no oculta.
