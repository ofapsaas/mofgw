# Test Audit Report — 011-004-file-attachments

**Feature:** file attachments de Odoo (input_file / input_image → content-array chat) en `/v1/responses`, epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-004-file-attachments/spec.md` (aprobado por Ofap el 2026-08-12).

## 1. Resumen del comportamiento que cambia

La feature 011-004 **elimina la rama de rechazo P9 de 001** (spec 004 §P6, D3): la presencia de parts `input_file`/`input_image` en el `input[]` **ya no produce HTTP 400 "file attachments not yet supported"**. En su lugar se traduce por mensaje (D1) a un `content` **array** de parts chat-completions: `input_image` → `{"type":"image_url","image_url":{"url","detail?"}}` (P1), `input_file` → `{"type":"file","file":{"file_data","filename"}}` (P2). El cambio vive exclusivamente en `internal/proxy/responses.go` (rama de rechazo líneas 268-273 + construcción de content por mensaje; spec 004 I1, ADR-004). Los caminos de texto plano (content-string, 001/003), el ciclo tool-calling (003 P3/P4), el passthrough del body (003 P1, 002 P1) y toda la traducción de salida (001 P10/P11, 003 P6/P7) quedan **intactos**.

**Impacto directo en la suite:** solo los tests que **afirman el 400 de P9 de 001** cambian de contrato. Verificación empírica con grep: exactamente **4 subtests** afirman el rechazo `"file attachments not yet supported"`. Ningún otro test referencia `input_file`/`input_image`/`file attachments` con expectativa de 400.

## 2. Tests modificados (cambian de contrato)

**Espec ref:** spec 004 §P1, §P2, §P3, §P6 (reemplaza spec 001 §P9 y spec 003 §P10/ramas file-image).

| # | Archivo / test | Subtest | Antes | Después | Justificación |
| --- | ----- | ----- | ----- | ----- | ----- |
| M1 | `e2e_011001_responses_test.go` — `TestE2E011001_F_RechazosFeaturesFuturas` | `part_input_file` (L532-536) | 400 (P9 001) | 200 con content array part `{"type":"file","file":{"file_data","filename"}}` (P2, CA-2) | El rechazo P9 se elimina (spec 004 §P6). Misma conversión rechazo→traducción que 002/003. |
| M2 | mismo | `part_input_image` (L537-541) | 400 | 200 con content array part `{"type":"image_url","image_url":{"url","detail?"}}` (P1, CA-1) | Ídem M1. `detail` passthrough/omitempty. |
| M3 | `e2e_011003_toolcalling_test.go` — `TestE2E011003_P10_RechazosConservados` | `part_input_file_400` (L648-651) | 400 | 200 con traducción a part `file` (P2) | spec 004 §P6 reemplaza P9; el subtest ya no vive en "rechazos conservados". |
| M4 | mismo | `part_input_image_400` (L652-655) | 400 | 200 con traducción a part `image_url` (P1) | Ídem M3. |

**Nota de autoría para POST-AUDIT:** el helper compartido `inputPart` (e2e_011001_responses_test.go:558-565) construye la part como `{"type": partType, "file_id": "file-123"}` — no trae `image_url`/`file_data`/`filename`. Bajo el contrato nuevo (D2, P1/P2), la part debe llevar los campos reales (data-URI, file_data, filename). Los subtests M1-M4 requieren construir parts completas (o extender el helper), no reutilizar `inputPart` tal cual.

## 3. Tests nuevos — mapeo P1-P9 a bloques (archivo nuevo `internal/proxy/e2e_011004_fileattachments_test.go`)

| Postcond. | Verificación observable | Bloque | Subtests |
| ----- | ----- | ----- | ----- |
| P1 | input_image → image_url con url == data-URI; detail passthrough/omitempty | Bloque A | `image_url_detail_passthrough`, `image_url_detail_ausente_omitempty` |
| P2 | input_file → file.file_data/filename tal cual; sin 400 | Bloque A | `file_part_traduccion`, `file_sin_400` |
| P3 | solo input_text → string; con image/file → content array en orden | Bloque B | `solo_input_text_string`, `mixto_array_orden`, `mensajes_mixtos_por_mensaje` |
| P4 | items ciclo tool-calling intactos (003) con message-array | Bloque C | `mixto_ciclo_intacto` |
| P5 | tools/parallel_tool_calls/response_format coexisten con content-array | Bloque D | `body_coexiste_con_content_array` |
| P6 | image/file ya no 400; 4xx upstream propagado no-reintentable | Bloque E | `file_no_400_upstream_ok`, `upstream_4xx_propagado` |
| P7 | rechazos conservados | sin test nuevo — tests untouched | — |
| P8 | data-URI infla len(body); ventana descuenta automáticamente | Bloque F | `data_uri_dentro_ventana_pasa` |
| P9 | salida sin cambios | sin test nuevo — tests untouched | — |

**Resumen:** 6 bloques (A-F), P1-P6 y P8 cubiertas por tests nuevos; P7 y P9 por tests existentes untouched.

## 4. Tests untouched (lista explícita)

**Del bloque F de 001 (`TestE2E011001_F_RechazosFeaturesFuturas`):**
- `stream_true` → 400 (spec 004 §P7).
- `tools` → ya convertido por 003; sigue validando tools function → 200 (spec 004 §P5).
- `text_format_json_schema` → ya convertido por 002; sigue validando traducción (spec 004 §P5).

**Del bloque P10 de 003 (`TestE2E011003_P10_RechazosConservados`):**
- `stream_true_400`, `text_format_no_json_schema_400`, `body_no_json_400`, `body_sin_input_400`, `sin_bearer_401`, `json_schema_coexiste_con_tools` → intactos (spec 004 §P7/P5).

**De 003 (ciclo tool-calling intacto, spec 004 §P4/P9):**
- `P3_FunctionCallItemAAssistantToolCalls`, `P4_FunctionCallOutputItemAToolRole`, `P5_MessageItemsSinRegresion`, `P6_ToolCallsASoloFunctionCallItems`, `P7_SinToolCallsItemMessage`, `P8_IdEstableDeterministico`, `P9_PipelineCompartidaYPassthrough` → intactos.

**De 001 (salida y entrada string intactas, spec 004 §P3/P9):**
- `C_HappyPath`, `D_IDsEstables`, `E_PipelineCompartida`, `G_CacheSeparadaPorEndpoint`, `H_StoreIgnorado`, `I_EnvelopeError` → intactos.

**De 002 (passthrough response_format, spec 004 §P5):** P1-P8 → intactos.

**Todo el resto de la suite** (~448 tests) → sin relación con el contrato de 004, untouched salvo los 4 de la sección 2.

## 5. Regression risk assessment

1. **Contenido de la rama eliminada:** los 4 subtests M1-M4 son los únicos que afirman el 400 de P9. Si quedaran sin convertir, fallarían al implementar P6. **Control:** conversión en POST-AUDIT + correr suite completa tras RED.
2. **Helper `inputPart` incompleto:** produce parts sin `image_url`/`file_data`/`filename`. Si M1-M4 lo reutilizan tal cual, el wire resultante sería vacío → test mal formado. **Control:** construir parts completas; assert positivo sobre url/file_data/filename no vacíos.
3. **Part type desconocido en mensaje-array (VERIFICAR spec 004):** content-array mapea solo tipos conocidos y dropea las desconocidas. Riesgo medio-bajo; silencio intencional. **Control recomendado (opcional):** subtest en Bloque B. Mejora, no bloqueante.
4. **4xx upstream no-reintentable (D5 de 002):** cubierto por Bloque E; riesgo bajo si el patrón ya está testeado en 002.

## 6. Gate de Test Audit checklist

- [x] Tests modificados con justificación explícita en spec.md (M1-M4, spec 004 §P1/§P2/§P3/§P6).
- [x] Lista de tests untouched EXPLÍCITA (sección 4).
- [x] Riesgo de regresión evaluado (sección 5).
- [x] Mapeo P1-P9 a tests nuevos (P7/P9 → existentes; resto → 6 bloques A-F).
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de POST-AUDIT/RED.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
