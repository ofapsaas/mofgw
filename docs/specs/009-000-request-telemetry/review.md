# Review — 009-000-request-telemetry

**Reviewer model:** mofgw/qwen3.7-plus (familia DISTINTA al implementer deepseek-v4-flash — invariante anti-bias cumplida)
**Fecha:** 2026-08-09
**Estado:** APPROVE — 0 bloqueantes

## Metodología

Lectura completa del spec, los 5 archivos de implementación, los 5 archivos de test, y verificación cruzada de cada postcondición (P1-P10), invariante (I1-I8) y criterio (C1-C14). Análisis estático línea por línea del diff. La suite (265 tests -race) fue certificada por el orquestador; no se re-corrió.

## Bloqueantes

**Ninguno.**

## Hallazgos (opcionales/nits/FYI)

### 1. Parsing redundante del body en `emitRequestTelemetry` (Optional, confianza 95%)
`internal/proxy/proxy.go:120-137` — el body se parsea 3 veces: `composition.Walk` (key paths + detected), `json.Unmarshal` por cada message (roles), `json.Unmarshal` para tools. El walk ya visita todo; extenderlo para devolver numMessages/roles/numTools en un solo recorrido elimina ~2x parsing JSON por request. Para bodies LLM típicos (100KB-1MB) agrega latencia evitable en el hot path.

**Decisión del orquestador (dueño del proceso):** no bloquea el merge. Registrado como follow-up de hardening del epic 009 — 009-001-context-composition ya va a extender `composition` para la composición estructural, y puede absorber este refactor (WalkResult único) sin costo extra.

### 2. Sin test para el header `X-Session-Affinity` (Nit, confianza 100%)
La implementación captura el header correctamente (proxy.go:162) pero ningún test lo verificaba.

**Decisión del orquestador:** RESUELTO en el merge — se agregó `X-Session-Affinity: "sticky-1"` a `TestPostcondition5_HeadersAllowlist` con su aserción. Test verificado verde.

### 3. `detected_ids` sin deduplicar entre paths (FYI, confianza 85%)
Si el mismo `ses_abc` aparece en `metadata.session_id` y `messages[].session_id`, se reportan 2 entradas. Los `key_paths` sí se deduplican; `detected_ids` no.

**Decisión del orquestador:** BY DESIGN para telemetría de descubrimiento — cada ocurrencia en un path distinto es un hallazgo independiente que informa la estructura del tráfico. Documentado como intencional.

### 4. Timestamp duplicado `time` (slog) + `ts` (explícito) (FYI, confianza 100%)
Cada evento tiene 2 timestamps: el envelope de slog (`time`) y el campo del schema (`ts`, exigido por P4). ~30 bytes extra por evento.

**Decisión del orquestador:** decisión explícita del spec (P4 exige `ts` como campo del evento); el schema lock (R10) acepta el envelope de slog. Sin cambio.

## Cumplimiento de postcondiciones (P1-P10)

| P | Estado | Evidencia |
|---|---|---|
| P1 BuildTelemetry separado | ✅ | logging.go:147-157: constructor independiente, JSONHandler Info, fail-fast, 0o640, closer. 5 tests. |
| P2 Wiring proxy | ✅ | proxy.go:58,90,97: campo + param (10 params). main.go:170-178: buildTelemetryFromConfig 3 ramas. |
| P3 Evento request_telemetry | ✅ | proxy.go:329: emit ANTES de limiter/budget/ventana/upstream. JSON malformado → 400 antes del emit. 7 tests. |
| P4 Schema exacto | ✅ | proxy.go:139-150: exactamente los 11 campos. Schema lock test (R10). |
| P5 Headers allowlist+denylist | ✅ | proxy.go:102-110,160-176: allowlist 7 headers, Content-Length de r.ContentLength, warn con nombre. 3 tests. |
| P6 Walk key paths | ✅ | composition/walk.go:42-49: recursivo, dots, [], orden estable, dedup, maxDepth. 3 tests. |
| P7 Detección ids safe fields | ✅ | walk.go:191-199: isSafeField lista dura, isIDLike prefijos, nunca content. 3 tests. |
| P8 Config | ✅ | config.go:153-157,225-229,398-407: defaults, validación sample_rate∈(0,1], enabled+file="". 5 tests. |
| P9 Muestreo | ✅ | main.go:326-348: samplingHandler, rand.Float64() >= keep. Test C9 banda 40-60. |
| P10 Privacidad | ✅ | Solo metadata; greps negativos de secrets. |

## Cumplimiento de invariantes (I1-I8)

I1 privacidad ✅ · I2 allowlist+denylist ✅ · I3 safe fields ✅ · I4 maxDepth ✅ · I5 fail-fast ✅ · I6 independencia --verbose ✅ · I7 no invasivo ✅ (solo LEE body, best-effort) · I8 suite verde ✅ (265 -race).

## Cumplimiento de criterios (C1-C14)

C1 ✅ `TestPostcondition2_MainWiringTelemetry` · C2 ✅ `TelemetryOnEscribeEventos` + `SinEventoBodyInvalido` · C3 ✅ `HeadersAllowlist` (+X-Session-Affinity tras el fix del merge) · C4 ✅ `HeaderXCustomNegadoConWarn` · C5 ✅ `HeadersProhibidosNunca` · C6 ✅ `DetectedIdsE2E` · C7 ✅ `SoloPathsNoValores` · C8 ✅ `MaxDepthE2E` · C9 ✅ `MuestreoBanda` (cmd) · C10 ✅ `EnabledConFileVacio` + `MainWiring` · C11 ✅ `MainWiring` (Stat IsNotExist) · C12 ✅ `TelemetryOffSinEventos` + suite intacta · C13 ✅ 265 -race · C14 ✅ `PrivacidadGrepNegativo`.

## Veredicto final

**APPROVE.** La implementación cumple fielmente las 10 postcondiciones, 8 invariantes y 14 criterios del spec. Arquitectura limpia (paquete composition con API acotada, BuildTelemetry separado, samplingHandler composicional). Privacidad enforced por construcción (allowlist + safe fields + denylist), no por convención. Tests verifican comportamiento observable contra el contrato, sin mocks de plumbing.

Follow-up registrado: parsing redundante del body (Optional #1) → hardening del epic 009 / 009-001.
