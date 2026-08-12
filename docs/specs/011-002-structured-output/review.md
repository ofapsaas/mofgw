# Review — 011-002-structured-output

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

**Ninguno.** Tras examen exhaustivo del diff, spec, invariantes, clamp, router, absorb y los 7 tests + POST-AUDIT: no se encontraron issues bloqueantes.

Verificaciones explícitas:
- **I1 (boundary):** el diff solo toca `internal/proxy/responses.go` (traducción) + archivos de test. Ni router, ni providers, ni `ParseChatRequest` modificados. ADR-004 respetado.
- **I3 (byte-identidad):** la implementación usa `json.RawMessage` para `format.Schema` (responses.go:151), lo inyecta en `jsonSchema` como RawMessage (158), y `json.Marshal` lo serializa verbatim. El clamp opera sobre `map[string]json.RawMessage` — `response_format` viaja como RawMessage opaco que el clamp no desarma. Byte-identidad sostenida en toda la cadena.
- **P4 (rama 400):** `Unmarshal` falla si format no es objeto (→ 400); `Type != "json_schema"` cubre cualquier otro type (→ 400). Edge cases: format string/null/vacío/type object/text/"" → todos 400.
- **P7 (no degradación):** `router.Complete` → error upstream → `handleChainError` → `absorb.Respond` propaga status y envelope. Sin retry, sin fallback. Confirmado en `absorb.go:177-191`.
- **I5 (zero OpenAI):** todos los upstreams son `httptest.NewServer` locales.

## Opcionales

### 1. `responseFormat` tipado como `map[string]any` — sin type safety
**Ubicación:** `internal/proxy/responses.go:146,158,162`
**Problema:** claves `"type"`, `"json_schema"`, `"name"`, `"schema"`, `"strict"` como strings sin verificación del compilador. Un typo futuro compilaría y produciría un wire incorrecto detectado solo en runtime.
**Decisión:** **APLICAR** — definir structs locales no exportados con `omitempty` en `Strict` (resuelve D2 sin `if` explícito y hace I3 más robusto con type-safety del compilador).

### 2. Sin validación local de `schema` ausente cuando `type=="json_schema"`
**Ubicación:** `internal/proxy/responses.go:154-157`
**Problema:** si el cliente manda `{"type":"json_schema","name":"x"}` sin `schema`, `format.Schema` es nil → `json.Marshal` serializa `"schema":null`. El upstream rechaza con 4xx (P7), pero el error message es del upstream (confuso).
**Decisión:** **DESCARTAR** — el upstream rechaza y se propaga (funcionalmente correcto, P7); el spec no lo exige; agregar guard local duplica validación del upstream.

### 3. POST-AUDIT subtest dentro de función nombrada "Rechazos"
**Ubicación:** `internal/proxy/e2e_011001_responses_test.go:444`
**Problema:** el subtest `text_format_json_schema` verifica traducción exitosa (200) dentro de `TestE2E011001_F_RechazosFeaturesFuturas` cuyo nombre sugiere "rechazos".
**Decisión:** **DESCARTAR** (Nit) — cosmético; no afecta corrección. Renombrar toca el archivo de 001 sin ganancia funcional.

## Auditoría test↔postcondición

| Postcondición | Test | Mapea | Dep. interna |
|---|---|---|---|
| P1 | TestE2E011002_P1_TraduccionJsonSchema | ✅ | No |
| P2 | TestE2E011002_P2_StrictPassthrough (3) | ✅ | No |
| P3 | TestE2E011002_P3_TemperatureNoForzado | ✅ | No |
| P4 | TestE2E011002_P4_TipoNoJsonSchemaRechaza (3) | ✅ | No |
| P5 | TestE2E011002_P5_SalidaIntacta | ✅ | No |
| P6 | TestE2E011002_P6_CachePorSchema | ✅ | No |
| P7 | TestE2E011002_P7_Upstream4xxSinDegradar | ✅ | No |
| P8 | Tests untouched de 001 | ✅ | No |

- **Postcondiciones sin test:** ninguna.
- **Tests sobrantes:** ninguno.
- **Tests con dependencia interna (AP-14):** ninguno.

**Notas específicas:**
- **I3:** sostenido (P1 compara schema byte-idéntico; RawMessage preservado por clamp).
- **P4:** sostenido (type object/text/malformado → 400; format null/vacío cubiertos implícitamente).
- **P7:** sostenido (400 propagado, 1 sola llamada, wire con response_format json_schema).
- **P6:** sostenido (MISS→HIT→MISS; key se computa del body Responses completo incl. schema).
- **POST-AUDIT:** coherente; strict de P1 omitido correctamente (D2), strict del POST-AUDIT cubre el caso con input que manda strict.

---

Status: Review completado. 0 bloqueantes. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #1, descartar #2 y #3 con motivo.
