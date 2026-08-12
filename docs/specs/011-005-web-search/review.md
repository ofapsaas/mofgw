# Review — 011-005-web-search

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

### 1. `any` + reflect en `SetWebSearch` / `runWebSearch` — complejidad innecesaria y frágil
**Ubicación:** proxy.go:119,169-172 (`webSearchClient any` + setter); responses.go:612-658 (fallback reflectivo, 47 líneas + reflectFieldString).

**Problema:** El setter `SetWebSearch(enabled bool, client any)` acepta `any`. El mock del test (`proxy_test`) devuelve `[]webSearchResult` (tipo local), y Go casa firmas por identidad de tipos → el mock no satisface `websearch.Client`. Esto fuerza 47 líneas de reflect que:
- **Falla silenciosamente** (si el mock cambia un campo, `reflectFieldString` devuelve "" sin error).
- **No tiene cobertura propia**.
- **Viola Make Illegal States Unrepresentable** (Law 2) y **Fail Loud** (Law 4).

**Decisión:** **APLICAR** — la alternativa es trivial: el mock del test importa `internal/websearch` y devuelve `[]websearch.Result`; el setter/campo se tipa a `websearch.Client`; se elimina todo el bloque reflectivo (47 líneas, el import `reflect`, `reflectFieldString`). El test-writer actualiza el mock; el implementer tipa el setter y elimina el reflect.

## Opcionales

### 2. `htmlUnescape` incompleta — entidades HTML comunes no se decodifican
**Ubicación:** websearch.go:212-218.
**Decisión:** **APLICAR** — usar `html.UnescapeString` de stdlib (cubre todas las entidades HTML4/5), eliminar la función casera. Sin dependencias externas.

### 3. Sin tests unitarios del parser `websearch`
**Ubicación:** internal/websearch/ (sin websearch_test.go).
**Decisión:** **DESCARTAR** — spec lo marca como desviación no bloqueante; fallback Instant Answer + best-effort mitigan. Hardening de parser fuera del ciclo de feature.

### 4. Alineación título/snippet por índice es frágil ante HTML asimétrico
**Ubicación:** websearch.go:164-188.
**Decisión:** **DESCARTAR** (Nit) — impacto en calidad de grounding, no data corruption.

### 5. `deriveWebSearchQuery` re-parsea los items (doble unmarshal)
**Ubicación:** responses.go:579-602.
**Decisión:** **DESCARTAR** (Nit) — performance negligible.

## FYI

### 6. URLs hardcodeadas a DDG — sin riesgo de SSRF
Decisión: sin acción — la query va solo como query parameter, cliente solo habla con los 2 dominios DDG.

### 7. Body de respuesta DDG limitado a 4MB
Decisión: sin acción — bien implementado (io.LimitReader).

## Auditoría test↔postcondición

| Postcond. | Test(s) | Cubierta |
| ----- | ----- | ----- |
| P1 | TestE2E011005_P1_GatingEnabledTrue (2 subtests) | ✅ |
| P2 | TestE2E011005_P2_QueryUltimoMensajeUser, P2_QueryDeterminista | ✅ |
| P3 | TestE2E011005_P3_TopNConsumido (2 subtests) | ✅ |
| P4 | TestE2E011005_P4_InyeccionSystemMessage | ✅ |
| P5 | TestE2E011005_P5_RemocionWebSearchPreview (2 subtests) | ✅ |
| P6 | TestE2E011005_P6_SalidaItemMessage | ✅ |
| P7 | TestE2E011005_P7_DDGFallaBestEffort, P7_SinMensajeUserSinGrounding | ✅ |
| P8 | TestE2E011005_P8_CacheExcluidoParaWebSearch | ✅ |

- **Postcondiciones sin test directo:** P1 enabled:false → 400 (cubierto por tests untouched de 001/003).
- **Tests sobrantes:** ninguno.
- **Dependencia interna:** el mock del cliente DDG es boundary externo inyectado (prescrito por spec §251-259), no plumbing interno. Aceptable.

**Nota reflect:** veredicto Required (hallazgo #1). El mock puede devolver `[]websearch.Result` importando el paquete de producción, eliminando todo el reflect.
**Nota P1/P2/P4/P5/P7/P8:** bien cubiertos. El test P8 (cache excluido) es particularmente bueno — 2 requests mismo body llegan al upstream 2 veces y devuelven resultados distintos.

---

Status: Review completado. 1 bloqueante (reflect → interfaz tipada) + opcional #2 a aplicar. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #1 y #2, descartar #3/#4/#5, FYI #6/#7.
