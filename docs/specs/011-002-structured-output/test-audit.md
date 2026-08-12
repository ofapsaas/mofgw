# Test Audit Report — 011-002-structured-output

**Feature:** `text.format.json_schema` → `response_format` en la entrada de `/v1/responses` (structured output, epic 011-mofgw-odoo).
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-002-structured-output/spec.md` (aprobado por Ofap el 2026-08-12).

## 1. Resumen del comportamiento que cambia

La feature reemplaza la rama de rechazo P8 de 001 (`text.format.json_schema` → **HTTP 400** "structured output not yet supported") por la **traducción real** a `response_format` en la entrada de `/v1/responses`:

| Aspecto                                                        | Antes (001)                                                              | Después (002)                                                                          |
| -------------------------------------------------------------- | ------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| `text.format.type == "json_schema"`                              | 400 "structured output not yet supported" (P8 de 001) | 200 + `response_format` traducido en el wire upstream (P1 de 002)                        |
| `text.format.type != "json_schema"` / malformado                 | 400 (P8 de 001)                                                          | **se conserva 400** "structured output not yet supported" (P4 de 002 — rama P8 conservada) |
| Salida `output[]`                                                | shape P10 de 001                                                         | **sin cambios** (P5 de 002, I4)                                                            |
| `stream`/`tools`/`web_search_preview`/`input_file`/`input_image`/no-JSON | 400 (P6/P7/P9/P2 de 001)                                                 | **sin cambios** (P8 de 002)                                                                |

**Impacto concreto en la suite:** solo el subtest `text_format_json_schema` del bloque F de `e2e_011001_responses_test.go` (líneas 440-445) cambia de contrato. Todo el resto de la suite de 001 queda untouched.

## 2. Tests modificados (1)

| Test | Ubicación | Justificación | Spec ref |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Subtest `text_format_json_schema` dentro de `TestE2E011001_F_RechazosFeaturesFuturas` (líneas 440-445) | `internal/proxy/e2e_011001_responses_test.go` | El comportamiento que valida (rechazo 400 de `json_schema`) **ya no existe**: 002 reemplaza la rama P8 por traducción. Debe **actualizarse en su archivo original** (POST-AUDIT) para validar el comportamiento NUEVO: `text.format.type == "json_schema"` → **200** y el wire upstream contiene `response_format`. Recomendación: convertir el assert de rechazo (400) en assert de traducción (200 + `response_format` en `u.gotBody`). | spec 002 §P1 (traducción), §P4 (rama 400 conservada para otros types); spec 001 §P8/CA-8 (contrato viejo que se invalida) |

## 3. Tests nuevos a escribir (7, en 4 bloques lógicos — archivo nuevo `internal/proxy/e2e_011002_structuredoutput_test.go`)

**Bloque A — happy path structured output (P1, P2, P3, P5 → CA-1, CA-2, I3, I4):**
- `TestE2E011002_P1_TraduccionJsonSchema`: `text.format.type=="json_schema"` con `name`/`schema`/`strict` → 200, y el wire upstream (`u.gotBody`) contiene `response_format == {"type":"json_schema","json_schema":{"name":<name>,"schema":<schema>,"strict":<strict>}}`; el `schema` del wire es **byte-idéntico** al del input (I3, CA-1).
- `TestE2E011002_P2_StrictPassthrough`: subtests `strict=true`, `strict=false`, y `strict` **ausente** → el campo `strict` no se emite en el `json_schema` del wire (P2, D2).
- `TestE2E011002_P3_TemperatureNoForzado`: con `temperature:0.7` + structured output → el wire upstream mantiene `temperature == 0.7` (no forzado a 0) (P3, D4).
- `TestE2E011002_P5_SalidaIntacta`: provider responde `choices[0].message.content == "<json>"` → `output[0].content[0].text == "<json>"` exacto, shape message/assistant/completed (P5, I4, CA-2).

**Bloque B — rechazo de otros types (P4 → CA-3):**
- `TestE2E011002_P4_TipoNoJsonSchemaRechaza`: subtests `type:"object"`, `type:"text"`, y `text.format` malformado → **HTTP 400** con `error.message == "structured output not yet supported"` (P4, rama P8 conservada, D3).

**Bloque C — pipeline compartida / cache (P6 → CA-4, I2):**
- `TestE2E011002_P6_CachePorSchema`: con cache habilitada, misma `schema` + mismo body → HIT (upstream no re-llamado); `schema` **distinta** → MISS (upstream re-llamado). Verifica que `response_format` participa de la key de cache exact-match (CA-4, P12 de 001).

**Bloque D — error upstream sin degradación (P7 → CA-5):**
- `TestE2E011002_P7_Upstream4xxSinDegradar`: mock upstream que responde **400** ante `response_format` → mofgw propaga el 4xx (mismo envelope `{"error":{...}}`); **no** degrada a `json_object`, **no** reintenta con fallback (D5). Assert de que el upstream no recibe un segundo request con `response_format` eliminado.

**P8 (rechazos no-structured-output intactos):** NO requiere test nuevo dedicado — queda verificado por los tests untouched de 001 (P6 stream, P7 tools/web_search, P9 files, P2 no-JSON, P1 auth).

## 4. Tests untouched (lista explícita)

De `internal/proxy/e2e_011001_responses_test.go` — **se mantienen sin cambios**:

| Bloque | Test | Postcondición 001 |
| ------ | ---------------------------------------------------------------------------------- | ----------------------------- |
| A      | `TestE2E011001_P1_Auth`                                                              | P1 (auth/401)                 |
| B      | `TestE2E011001_P2_BodyInvalido`                                                      | P2 (400 body/no-input)        |
| C      | `TestE2E011001_C_HappyPath`                                                          | P3/P5/P10                     |
| D      | `TestE2E011001_D_IDsEstables`                                                        | P11                           |
| E      | `TestE2E011001_E_PipelineCompartida`                                                 | P4 (context-window/429/usage) |
| F      | subtests `stream_true`, `tools`, `web_search_preview`, `part_input_file`, `part_input_image` | P6, P7, P9                    |
| G      | `TestE2E011001_G_CacheSeparadaPorEndpoint`                                           | P12                           |
| H      | `TestE2E011001_H_StoreIgnorado`                                                      | P13                           |
| I      | `TestE2E011001_I_EnvelopeError`                                                      | P14                           |

Todo el resto de la suite del proyecto (413 tests, 17 paquetes) queda untouched — no hay otro test que toque `text.format`/`json_schema`/`response_format`/`structured output`.

## 5. Regression risk assessment

1. **Cache colapsando schemas distintos (ALTO):** si `response_format`/`schema` no participa de la key de cache, requests con schemas distintos → HIT incorrecto. Mitigación: `TestE2E011002_P6_CachePorSchema` (CA-4). Riesgo alto de falso positivo si el implementer inyecta `response_format` después de computar la key.
2. **`response_format` perdido en clamp/thinking (MEDIO):** I2 exige que clamp y thinking preserven `response_format` como passthrough. Cubierto por Bloque A/C.
3. **Rama 400 no intencionalmente relajada (MEDIO):** riesgo de que otros rechazos (P6/P7/P9 de 001) se aflojen. Cubierto por P8 de 002 (untouched de 001 + Bloque B).
4. **Byte-identidad del schema (MEDIO):** si se re-marshala `schema` por `map[string]any` (en vez del RawMessage), pierde el orden byte-a-byte (I3). Cubierto por Bloque A.
5. **I1 — no tocar router/providers/ParseChatRequest (BAJO):** el `response_format` debe vivir solo en `responses.go`. Sin test directo; mitigado por revisión I1.
6. **P7 — no degradar a `json_object` (MEDIO):** riesgo de fallback "cómodo". Cubierto por Bloque D (P7).

## 6. Gate de Test Audit checklist

- [x] Spec 002 leído completo (8 postcondiciones P1-P8, 6 invariantes I1-I6, CA-1..CA-6).
- [x] Cada test existente potencialmente afectado clasificado (modificado/untouched), con justificación y ref al spec.
- [x] Verificación empírica por grep de `text.format`/`json_schema`/`structured output`/`response_format` en toda la suite → **1 solo subtest** afectado (F de 001).
- [x] Lista de untouched EXPLÍCITA (no implícita): bloques A, B, C, D, E, G, H, I + 5 subtests de F.
- [x] Tests nuevos mapeados a P1-P7 (P8 cubierto por untouched de 001), agrupados en 4 bloques lógicos en archivo nuevo.
- [x] Regression risks documentados (cache por schema = alto).
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de POST-AUDIT/RED.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
