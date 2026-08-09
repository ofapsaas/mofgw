# Test Audit Report — 009-000-request-telemetry

**Rol:** cdad-test-writer · **Sub-fase:** 3.0 AUDIT · **Fecha:** 2026-08-09
**Fuentes:** `docs/specs/009-000-request-telemetry/spec.md` (aprobado 08 Ago 2026), suite de tests `internal/**/*_test.go` + `cmd/**/*_test.go`, `docs/.cdad-state.json`.
**Restricción de ejecución (declarada):** no se pudo correr `go test ./... -race` ni comandos bash (permisos del entorno). El estado "suite verde en HEAD" se infiere de `docs/.cdad-state.json` (features previas done con suite verificada `-race` verde) y lectura estática de los tests. Verificación empírica pendiente en POST-AUDIT Parte 2.

## 1. Resumen del comportamiento que cambia

La feature **agrega** telemetría de descubrimiento (archivo `telemetry.jsonl` con metadata de request). Es un canal NUEVO, separado del log operativo y de metrics. Cambios de contrato relevantes para la suite existente:

| Cambio | Ref. spec | Efecto sobre tests |
|---|---|---|
| `proxy.New()` gana parámetro `telemetryLogger *slog.Logger` (9→10 params) | P2 | **5 call-sites en tests deben agregar `nil`** (compilación; sin cambio de comportamiento). C12 exige backward-compat. |
| `logging.BuildTelemetry(file)` nuevo constructor | P1 | No afecta `Build()` existente. Test nuevo. |
| Paquete nuevo `internal/composition/walk.go` | P6 | Greenfield; no existe el paquete. |
| Bloque config `telemetry:` top-level + defaults + validación | P8 | Aditivo; configs existentes sin bloque siguen válidas (defaults). |
| Emisión de evento `request_telemetry` en `handleChat` | P3 | No modifica eventos `request_start`/`request_end` del log operativo. |
| `main.go`: `buildTelemetryFromConfig` helper + segundo closer | P2, R8 | Nuevo test seam (patrón `newHTTPServer` de SEC-001). |

**No cambia:** `Build()` (fanout por nivel), metrics (canal separado — `TestNamesSorted` de metrics_test.go:42 asevera exactamente 12 contadores y lo protege), auth, router, limiter, stream, clamp, timeouts, absorb, health, provider.

## 2. Inventario de la suite relevante

### 2.1 Harness e2e del proxy (`internal/proxy/`)
- `e2e_test.go`: harness base (`upstream` fake con `gotBody`/`gotHeaders`, `harness`, `build` :91, `buildWithLogger` :98 — **call-site New :113**, `buildWithOptions` :121 — **call-site New :145**, `h.chat()` :163, helpers `jsonLogLine`/`parseJSONLines`/`lineWithMsg`/`waitForMsg` :408-447, `TestE2EDebugPrivacidadYEventos` :449). **Máxima reutilización** — `parseJSONLines`/`waitForMsg` sirven para el evento; el grep negativo de C14 replica el de ese test.
- `e2e_006001_test.go`: `buildMultiClient` :37 — **call-site New :56**; `chatAs` :65; `metricsBody` :78.
- `e2e_006002_test.go`: `buildPricing` :39 envuelve buildMultiClient.
- `e2e_007002_test.go`: `buildWithModels` :53 — **call-site New :72**.
- `e2e_007003_test.go`: `usageBody` :185, `assertUsageHeaders`/`itoa`/`jsonNumber`.
- `e2e_008001_test.go`: `bigBody(n)` :26 (rechazo por ventana con `SetModelMetadata`).
- `e2e_008002_test.go`: `SetBudget`/`SetPricing` (rechazo 429 por budget).
- `e2e_008003_test.go`: `chatAsSession` :24 setea `X-Session-Id`; `TestRED_SessionHeaderNoUpstream` :169 — precedente de P5.
- `e2e_limiter_test.go`: `slowUpstream`, `buildLimited` :77 — **call-site New :106**; `chatWith` :113 setea `X-Agent-Id`; `TestE2EXAgentIDNotForwarded` :297 — precedente de P5.
- `e2e_003001_test.go`: `upstreamUsageOK`/`upstreamNoUsage`/`upstreamStreamUsageOK`; **`var _ = proxy.New` :355** — referencia de compilación, NO se rompe con el cambio de firma, NO editar.

### 2.2 Logging (`internal/logging/`)
- `logging_test.go`: `TestRequestIDRoundTrip`/`TestWithRequestAddsID`; tests de `Build()` destinos (66-153); `TestArchivoNoAbrible` :155 (fail-fast); helper `readLogFile` :166. **Plantilla de P1** — `TestDestinoInfoConArchivo` (close-then-read, 100-106), `TestArchivoNoAbrible`.
- `sec001_red_test.go`: `TestSEC001_LogFilePermisos0640` — P1 exige 0o640, mismo patrón.

### 2.3 Config (`internal/config/`)
- `config_test.go`: `validYAML`, `writeTemp` :42, `TestLoadValid`, `TestLoadMissingProviderFields` :112 (tabla), helpers `contains`/`indexOf`. **Plantilla de P8.**
- `metadata_red_test.go`: `TestRED_LoadMetadataValida`, `TestRED_LoadSinMetadataVacia`, `TestRED_ThinkingDefaultFueraDeLista` — **patrón RED exacto de P8** (bloque nuevo con defaults + reglas).
- `external_review_fixes_test.go`: asserts sobre defaults (:54) y mensajes de error (:26, :87).

### 2.4 cmd (`cmd/mofgw/`)
- `sec001_red_test.go`: `TestSEC001_HTTPServerTieneReadHeaderTimeout`/`TestSEC001_HTTPServerSirve` — testea `newHTTPServer(cfg, handler)` extraído de main. **Precedente del helper `buildTelemetryFromConfig` (R8).**

### 2.5 Otros paquetes (untouched)
`provider_test.go`, `auth_test.go`, `router_test.go`, `limiter_test.go` + sec001, `stream_test.go`, `clamp_test.go`, `timeouts_test.go`, `absorb_test.go`, `health_test.go`, `metrics_test.go` + `persist_test.go`.

**Verificación del inventario:** `grep proxy.New(` en tests → exactamente **5 call-sites** (coincide con spec): `e2e_test.go:113`, `e2e_test.go:145`, `e2e_007002_test.go:72`, `e2e_006001_test.go:56`, `e2e_limiter_test.go:106`. Ningún test referencia `telemetry`/`key_paths`/`detected_ids`/`composition`/`BuildTelemetry`/`sample_rate`. `internal/composition/` no existe.

## 3. Mapeo postcondición → cobertura existente

| Postcondición | ¿Cubierta? | Análisis |
|---|---|---|
| **P1** — BuildTelemetry (jsonl, Info, fail-fast, closer, 0o640) | Terreno nuevo (precedente fuerte) | No existe BuildTelemetry. Precedentes: `TestArchivoNoAbrible`, `TestDestinoInfoConArchivo`, `TestSEC001_LogFilePermisos0640`, `readLogFile`. Build() untouched. |
| **P2** — Wiring proxy.New(telemetryLogger); nil=off; main 2 closers | Parcial | 5 call-sites de test (compile-only). "nil=off/non-nil=on" terreno nuevo. Wiring de main sin test seam → **R8 (helper buildTelemetryFromConfig)**. |
| **P3** — Evento request_telemetry (1/request válido, stream/no-stream/rechazados; NO body inválido; request_id) | Terreno nuevo | Precedentes: e2e_008001 (ventana), e2e_008002 (budget), e2e_limiter, upstreamFail. Patrón: buildWithLogger + waitForMsg. |
| **P4** — Schema del evento (campos exactos, sin otros) | Terreno nuevo | Reutiliza parseJSONLines. Schema lock (R10): conjunto exacto de keys. |
| **P5** — Headers allowlist + denylist + warn | Terreno nuevo | Precedentes de "header solo lectura": `TestRED_SessionHeaderNoUpstream`, `TestE2EXAgentIDNotForwarded`. |
| **P6** — Walk key paths (dots, [], maxDepth=10, truncación, sin valores) | Terreno nuevo (greenfield) | internal/composition no existe; cero tests. |
| **P7** — Detección ids safe fields (lista dura, prefijos, nunca content) | Terreno nuevo (greenfield) | Sin precedentes. |
| **P8** — Config telemetry (defaults, validación) | Terreno nuevo (precedente fuerte) | Patrón metadata_red_test.go; asserts de defaults en external_review_fixes_test.go. |
| **P9** — Muestreo a la emisión | Terreno nuevo | C9 con 20 req banda 8-12 → **flaky ~26% → R1 enmendado (100 req banda 40-60)**. |
| **P10** — Privacidad (nunca contenido en telemetry.jsonl) | Parcial (precedente fuerte) | TestE2EDebugPrivacidadYEventos cubre log operativo; telemetry.jsonl es sink nuevo sin cobertura. C14 explícito. |

## 4. Tests a modificar (POST-AUDIT, Parte 1 — compile-only)

| Test / archivo | Línea | Modificación |
|---|---|---|
| `internal/proxy/e2e_test.go` — `buildWithLogger` | 113 | Agregar `nil` como 10º arg |
| `internal/proxy/e2e_test.go` — `buildWithOptions` | 145 | Idem con `o.Logger` |
| `internal/proxy/e2e_006001_test.go` — `buildMultiClient` | 56 | Idem |
| `internal/proxy/e2e_007002_test.go` — `buildWithModels` | 72 | Idem |
| `internal/proxy/e2e_limiter_test.go` — `buildLimited` | 106 | Idem (manteniendo limiter args después) |

**Nota:** `var _ = proxy.New` en e2e_003001_test.go:355 referencia el valor de la función → NO requiere edición. Tras editar, DEBEN fallar (el implementer aún no agregó el parámetro) — estado RED esperado de POST-AUDIT Parte 1.

**No hay tests existentes que validen comportamiento viejo que la feature cambie** (aditiva). Ningún test cae en "eliminar".

## 5. Tests untouched (lista explícita)

- **`internal/proxy/e2e_test.go`**: todos los tests (TestE2EHealthzNoAuth, TestE2EMetricsNoAuth, TestE2EAuthRequired, TestE2EChatSuccess, TestE2EFallbackTransparent, TestE2EAllProvidersFail502, TestE2EBadRequest, TestE2EModelNotFound, TestE2EStreamingFallback, TestE2EClampMaxTokens, TestE2EPreservesExtraFields, TestE2EBodyTooLarge, TestE2EModelsEndpoint, TestE2EDebugPrivacidadYEventos, TestE2EHealthzProviderDetail, TestE2EHealthzWithStoreHealthy, TestE2EHealthz503AllDown, TestE2EHealthzRecovery, TestE2EHealthSkipsDownProvider, TestE2EAbsorbedErrorSanitized) — solo helpers buildWithLogger/buildWithOptions se tocan.
- **`internal/proxy/e2e_003001_test.go`**: todos (incluye guard de stream_options/transparencia — red de seguridad de I7).
- **`internal/proxy/e2e_006001_test.go`**: todos (usage accounting; `TestRED_NoRompeCache003001`).
- **`internal/proxy/e2e_006002_test.go`**: todos.
- **`internal/proxy/e2e_007002_test.go`**: todos los tests (solo buildWithModels se toca).
- **`internal/proxy/e2e_007003_test.go`**: todos.
- **`internal/proxy/e2e_008001/008002/008003_test.go`**: todos (incl. `TestRED_SessionHeaderNoUpstream` — X-Session-Id sigue solo lectura).
- **`internal/proxy/e2e_limiter_test.go`**: todos los tests (solo buildLimited se toca).
- **`internal/logging/logging_test.go`**, **`sec001_red_test.go`**: todos.
- **`internal/config/config_test.go`**, **`metadata_red_test.go`**, **`external_review_fixes_test.go`**: todos.
- **`cmd/mofgw/sec001_red_test.go`**: ambos.
- **`internal/provider`**, **`auth`**, **`router`**, **`limiter`**, **`stream`**, **`clamp`**, **`timeouts`**, **`absorb`**, **`health`**, **`metrics`** (incl. `TestNamesSorted` — protege que la telemetría NO toque metrics).

## 6. Riesgos para el test-writer (y regresión)

1. **R1 — C9 muestreo: flake estadístico alto.** Con sample_rate=0.5 y 20 requests, conteo binomial(20, 0.5): P(8≤X≤12) ≈ 73.7% → ~26% de falla por chance. **RESUELTO POR ORQUESTADOR:** C9 enmendado → 100 requests, banda 40-60 (P≈96.4%).
2. **R2 — Content-Length en allowlist (P5/C3).** Go no expone Content-Length en `r.Header` (vive en `r.ContentLength`). El test de C3 asevera `Content-Length == len(body)` (contrato sobre longitud); diagnóstico apunta al implementer (leer `r.ContentLength`).
3. **R3 — Headers que agrega http.DefaultClient.** Tests con `h.chat()` llevan `User-Agent: Go-http-client/1.1` de default. C3 setea explícitamente los headers a aseverar; no aseverar ausencia de allowlist no controlados. `Accept-Encoding: gzip` (no allowlist, no X-*) no genera warn.
4. **R4 — "Body inválido" vs "400 de validación" (P3/C2). RESUELTO POR ORQUESTADOR:** el test de no-emisión usa JSON malformado real (`{`); body JSON válido pero rechazado por validación (p.ej. sin messages) SÍ emite evento.
5. **R5 — Orden estable de key_paths (P4/P6).** Test asevera orden exacto; si el implementer itera un map, RED lo caza.
6. **R6 — Flush del archivo.** Harness retiene el closer de BuildTelemetry y cierra antes de leer (patrón close-then-read) o usa `waitForLines`. El cierre en test no interfiere con el de main (instancias distintas).
7. **R7 — Warn de P5 va al logger OPERATIVO.** Harness combina logger operativo capturado + telemetryLogger a archivo; assert `"level":"WARN"` + nombre del header + grep negativo del valor en opLogger.
8. **R8 — Wiring de main sin test seam. RESUELTO POR ORQUESTADOR:** extraer `buildTelemetryFromConfig(cfg)` en main.go (patrón newHTTPServer): enabled=false → (nil,nil,nil) sin archivo; enabled=true+file → logger+closer; enabled=true+file="" → error.
9. **R9 — I7 no-invasividad con telemetría ON.** Test: mismo status/body/gotBody upstream con y sin telemetría (replicar TestE2EPreservesExtraFields con telemetry on).
10. **R10 — Schema lock de P4 ("No hay otros campos").** Test asevera conjunto EXACTO de keys del evento (sin extras) — protege contra filtración accidental.
11. **R11 — Concurrencia de archivo.** slog es seguro para concurrencia; cada test su propio t.TempDir().
12. **R12 — client_id del evento.** `build` → "test"; `buildMultiClient` → "client-k1". El test asevera el id que el harness realmente autentica.

## 7. Recomendación de tests RED (por postcondición)

**Unidad — internal/logging (P1):**
- `test_postcondition_1_BuildTelemetryEscribeJSONL` — cada Info es UNA línea JSON; Debug no sale; close-then-read.
- `test_postcondition_1_BuildTelemetryFileVacioError` — file=="" → error.
- `test_postcondition_1_BuildTelemetryFailFast` — path no abrible → error.
- `test_postcondition_1_BuildTelemetryPermisos0640` — os.Stat → 0o640.
- `test_postcondition_1_BuildTelemetryCloserCierra` — closer llamable sin error.

**Unidad — internal/composition (P6, P7; greenfield):**
- `test_postcondition_6_WalkPathsConDots` — orden EXACTO (R5).
- `test_postcondition_6_WalkMaxDepthTrunca` — 15 niveles → trunca sin panic (C8).
- `test_postcondition_6_WalkSinValores` — ninguna string de valor profundo en output.
- `test_postcondition_7_DetectedIdsSafeFields` — metadata.session_id → {path,value} (C6); thread_id, role, name, tool_call_id.
- `test_postcondition_7_SafeFieldNoIdLike` — valor no id-like → path en key_paths, ausente de detected_ids.
- `test_postcondition_7_NuncaInspeccionaContent` — content/text/description/input/arguments jamás como valor (C7, I3).

**Unidad — internal/config (P8):**
- `test_postcondition_8_DefaultsTelemetry` — {Enabled:false, SampleRate:1, File:""}.
- `test_postcondition_8_BlockValidoSeCarga` — bloque presente → carga tipada.
- `test_postcondition_8_SinBloqueDefaults` — validYAML sin bloque → defaults.
- `test_postcondition_8_SampleRateInvalido` — tabla 0, -0.1, 1.5 → error.
- `test_postcondition_8_EnabledConFileVacio` — enabled+file="" → error (C10).

**E2E — internal/proxy/e2e_009000_test.go (P2-P5, P9-P10; C1-C14):**
- Harness `buildTelemetry(t, ups, key, opLogger)` — llama BuildTelemetry con t.TempDir()/telemetry.jsonl, pasa a proxy.New, retiene closer.
- `test_postcondition_2_TelemetryOffSinEventos` — nil → cero líneas (C1).
- `test_postcondition_2_TelemetryOnEscribeEventos` — logger → una línea por request (C2).
- `test_postcondition_3_UnEventoPorRequestNoStream` / `Stream` — exactamente 1 línea con request_id (C2).
- `test_postcondition_3_EventoEnRechazoBudget` / `EventoEnRechazoVentana` / `EventoEnFalloUpstream` — 429/400/502 Y evento emitido (P3).
- `test_postcondition_3_SinEventoBodyInvalido` — JSON malformado `{` → 400, cero líneas (P3, R4).
- `test_postcondition_4_SchemaCompleto` — keys exactas sin extras (R10); ts RFC3339.
- `test_postcondition_4_CamposBase` — client_id, model, stream, num_messages, num_tools, roles (orden).
- `test_postcondition_5_HeadersAllowlist` — C3 con headers explícitos; Content-Length == len(body) (R2).
- `test_postcondition_5_HeaderXCustomNegadoConWarn` — X-Custom-Something ausente + WARN con nombre + grep negativo del valor (C4, R7).
- `test_postcondition_5_HeadersProhibidosNunca` — Authorization/Cookie/X-Api-Key/X-Forwarded-For → grep negativo (C5).
- `test_postcondition_7_DetectedIdsE2E` — metadata.session_id anidado (C6).
- `test_postcondition_7_SoloPathsNoValores` — C7 (C7).
- `test_postcondition_6_MaxDepthE2E` — 15 niveles sin crash (C8).
- `test_postcondition_9_MuestreoBanda` — **100 requests, banda 40-60 (R1 enmendado)** (C9).
- `test_postcondition_10_PrivacidadGrepNegativo` — strings distintivos → cero matches (C14).
- `test_postcondition_10_VerboseNoActiva` — opLogger Debug + telemetry nil → cero líneas (C11, I6).
- `test_postcondition_2_NoInvasivoConTelemetry` — I7 (R9).

**cmd (R8 aprobado):**
- `test_postcondition_2_MainWiringTelemetry` sobre `buildTelemetryFromConfig`: enabled=false → (nil,nil,nil) sin archivo; enabled=true+file → logger; enabled=true+file="" → error.

## 8. Evaluación de riesgo de regresión

**Riesgo global: MEDIO-ALTO**, concentrado en coordinación de firma y C9.

| Riesgo | Severidad | Mitigación |
|---|---|---|
| Firma proxy.New rompe 5 call-sites si POST-AUDIT no los actualiza | Alta | §4 lista exacta; var _ = proxy.New NO se toca; C12 + suite completa como guard. |
| C9 banda 8-12 flaky (~26%) | Alta | **R1 resuelto: 100 req banda 40-60 (P≈96.4%).** |
| Fuga de contenido a telemetry.jsonl (I1/P10) | Alta | C14 + C5 + C7 grep negativo; schema lock R10. |
| Content-Length no capturado (implementer lee r.Header) | Media | R2 → test por contrato (len(body)); flaggear spec. |
| Ambigüedad "400 parseo" vs "400 validación" | Media | **R4 resuelto: JSON malformado = no emite; body válido rechazado = emite.** |
| Wiring de main sin test seam | Media | **R8 resuelto: helper buildTelemetryFromConfig.** |
| Orden inestable key_paths | Media | R5 → assert orden exacto. |
| Telemetría ON altera flujo (I7) | Media | Test no-invasividad; transparencia guardada por e2e_003001. |
| Warn P5 con valor del header | Media | R7 → grep negativo del valor en opLogger. |
| Harness sin flush | Baja | R6 → closer + close-then-read / waitForLines. |

**Sin riesgo de regresión:** metrics (TestNamesSorted), eventos operativos (canal separado), ruteo/fallback/clamp/timeouts (I7), headers solo lectura (e2e_008003/e2e_limiter).

## 9. Gate checklist (AUDIT)

- [x] Tests modificados identificados con justificación explícita (5 call-sites, spec líneas 35-36/89-94).
- [x] Tests untouched listados explícitamente (§5).
- [x] Riesgos de regresión evaluados (§8).
- [ ] Suite verde en HEAD **no verificada empíricamente** (bash bloqueado en sesión del rol; pendiente POST-AUDIT Parte 2 — el orquestador la corre).
- [x] C1/C10/C11: **resuelto** — helper `buildTelemetryFromConfig` en main (R8).
- [x] C9: **resuelto** — enmendado a 100 req banda 40-60 (R1).
- [x] R4: **resuelto** — definición explícita de "body inválido" (JSON malformado; body válido rechazado SÍ emite).

## 10. Decisiones del orquestador (dueño del proceso) — 08 Ago 2026

1. **R1 (C9):** enmendado — `sample_rate=0.5` con **100 requests**, banda **40-60** (P≈96.4% binomial). Actualizar spec C9.
2. **R8 (wiring main):** aprobado — extraer `buildTelemetryFromConfig(cfg)` en `cmd/mofgw/main.go` (patrón `newHTTPServer`): `enabled=false → (nil, nil, nil)` sin archivo; `enabled=true+file → (logger, closer, nil)`; `enabled=true+file="" → error`. Actualizar spec cambios de código.
3. **R4 (body inválido):** definido — JSON malformado (`{`) NO emite; body JSON válido pero rechazado por validación (sin messages, etc.) SÍ emite evento. Actualizar spec P3.
