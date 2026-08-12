# Test Audit Report — 011-003-tool-calling

**Feature:** ciclo tool-calling de Odoo (tools ↔ function_call ↔ function_call_output) en `/v1/responses`, epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-003-tool-calling/spec.md` (aprobado por Ofap el 2026-08-12).

## 1. Resumen del comportamiento que cambia

La feature reemplaza **la rama de rechazo P7 de 011-001** (`tools` → HTTP 400 "tool calling not yet supported") por la traducción real del ciclo tool-calling de Odoo, en las tres direcciones:

- **Entrada — definiciones de tools** (spec 003 §P1): `tools` con **todos** los items `type=="function"` → deja de ser 400 y pasa a traducirse a `tools` chat-completions anidado (`{"type":"function","function":{...}}`) con `parameters` byte-idéntico (I3) y `strict` passthrough/omitempty. `parallel_tool_calls` passthrough.
- **Entrada — items del ciclo** (spec 003 §P3, §P4): items `function_call` → mensaje assistant con `tool_calls`; items `function_call_output` → mensaje `role:"tool"` con `tool_call_id` (round-trip D1/I4).
- **Salida** (spec 003 §P6, §P7, §P8): `choices[0].message.tool_calls` no vacío → **solo** items `function_call` (D2, cero items `message`), `call_id` = eco del `id` upstream (D1), `arguments` string JSON (I2), `id` estable determinístico distinto del `call_id` (P8/P11 de 001). Sin `tool_calls` → item `message` (P7, P10 de 001 intacto).
- **Rechazos conservados** (spec 003 §P2/D3, §P10): `tools` con **cualquier** item `type!="function"` (incluido `web_search_preview`) → **sigue 400** "tool calling not yet supported"; `stream:true`, `text.format.type!="json_schema"`, `input_file`/`input_image`, body no-JSON/sin `input`, sin Bearer → intactos.

**Impacto en la suite existente (verificado por grep):** solo el **subtest `tools`** del bloque F de `e2e_011001_responses_test.go` cambia de contrato. El subtest `web_search_preview` (type `web_search_preview` = no-function) **mantiene** su 400 (D3). Ningún test de `e2e_011002` se ve afectado. Cero impacto en los tests de `/v1/chat/completions`.

## 2. Tests modificados (1)

| Test | Ubicación | Cambio | Justificación (spec ref) |
| ----- | ----- | ----- | ----- |
| subtest `tools` del bloque `TestE2E011001_F_RechazosFeaturesFuturas` | `internal/proxy/e2e_011001_responses_test.go` (líneas 429-434) | Hoy aserta `assertReject(400, "tool calling not yet supported")` para `tools:[{"type":"function","name":"f"}]`. Con 003 **ya no aplica el rechazo**: se actualiza en su archivo original para asertar la **traducción a 200** (wire upstream con `tools:[{"type":"function","function":{...}}]` y `parameters` byte-idéntico), reutilizando el patrón de assert de traducción del subtest `text_format_json_schema` (que 002 convirtió igual). | Spec 003 §P1 (líneas 137-145), §P2 (147-152), D3 (53-56), y Descripción funcional (líneas 15-16: "reemplaza esa rama de rechazo"). El rechazo P7 de 001 queda cubierto por P2/D3 solo para `type!="function"`. |

**Nota de conteo:** el bloque F completa no se elimina; se toca únicamente su subtest `tools`. Sus otros 5 subtests (`stream_true`, `web_search_preview`, `text_format_json_schema`, `part_input_file`, `part_input_image`) quedan intactos.

## 3. Tests nuevos (10 tests, agrupados en 4 bloques)

Archivo nuevo: `internal/proxy/e2e_011003_toolcalling_test.go`. Mapeo P1-P10 → bloques:

**BLOQUE A — Traducción de entrada: definiciones de tools (P1, P2 → CA-1, CA-2)**
- `test_postcondition_1_tools_function_a_chat_anidado` (P1, I3): body con `tools:[{"type":"function","name","description","parameters","strict"}]` → wire upstream con `tools:[{"type":"function","function":{name,description,parameters,strict}}]`; `parameters` byte-idéntico al input; `strict` presente passthrough / ausente → no se emite; `parallel_tool_calls` passthrough.
- `test_postcondition_2_web_search_preview_400` (P2, D3): `tools` con item `type!="function"` (incluido `web_search_preview`, y un caso mixto function+web_search para asegurar que el chequeo 400 gana antes de traducir) → **HTTP 400** "tool calling not yet supported".

**BLOQUE B — Traducción de entrada: items del ciclo (P3, P4, P5 → CA-3, CA-4)**
- `test_postcondition_3_function_call_item_a_assistant_tool_calls` (P3): item `{type:"function_call",call_id,name,arguments}` → wire con mensaje `{"role":"assistant","content":null,"tool_calls":[{id:==call_id,type:"function",function:{name,arguments}}]}`; fallback a `id` si no trae `call_id`; `arguments` string JSON sin re-marshal.
- `test_postcondition_4_function_call_output_item_a_tool_role` (P4, I4): item `{type:"function_call_output",call_id,output}` → wire con `{"role":"tool","tool_call_id":<call_id>,"content":<output>}`; en un request que combina function_call + function_call_output se verifica el round-trip (el `tool_call_id` matchea el `id` del tool_call del assistant).
- `test_postcondition_5_message_items_sin_regresion` (P5): `input[]` con items mixtos (message + function_call + function_call_output) → mensajes emitidos en orden de aparición; los items `message` se traducen igual que 001 (role preservado, content = concatenación de parts `input_text`).

**BLOQUE C — Traducción de salida (P6, P7, P8 → CA-5, CA-6, CA-7)**
- `test_postcondition_6_tool_calls_a_solo_function_call_items` (P6, D1, D2, I2): mock upstream con `choices[0].message.tool_calls` de N elementos → `output[]` con **EXACTAMENTE** N items `function_call` y **CERO** items `message`; cada item con `call_id` == eco del `id` upstream, `name`/`arguments` del `function`, `arguments` parseable por `json.loads`, `status:"completed"`, shape parseable por `_request_llm_openai_helper`.
- `test_postcondition_7_sin_tool_calls_item_message` (P7): mock upstream con `tool_calls` vacío/ausente → `output[]` con un solo item `message`, `content[0].text == choices[0].message.content` (P10 de 001 intacto).
- `test_postcondition_8_id_estable_deterministico` (P8, D1): mismo clientID + body con tool_calls → el `id` de los items `function_call` es el mismo (determinístico) y **distinto** del `call_id` (que es eco del upstream).

**BLOQUE D — Pipeline / invariantes de integración (P9, P10 → CA-8)**
- `test_postcondition_9_pipeline_compartida_y_passthrough` (P9): request con `tools`/`parallel_tool_calls` pasa por la misma pipeline (limiter/cache/clamp/context/budget/ruteo); `tools`/`parallel_tool_calls` sobreviven al clamp y a la inyección de thinking; cache con key separada por endpoint (P12 de 001) y `tools` participando del body canónico.
- `test_postcondition_10_rechazos_conservados` (P10): `stream:true` → 400; `text.format.type!="json_schema"` → 400; parts `input_file`/`input_image` → 400; body no-JSON/sin `input` → 400; sin Bearer → 401; `text.format.json_schema` **coexiste** con `tools` (ambas traducciones en el mismo request).

## 4. Tests untouched (explícito)

**`internal/proxy/e2e_011001_responses_test.go` — 8 top-level completos + 5 subtests del bloque F:**

- `TestE2E011001_P1_Auth` (P1) — untouched: auth sin scoping nuevo (003 no toca auth).
- `TestE2E011001_P2_BodyInvalido` (P2) — untouched: body no-JSON/sin input → 400 (P10 de 003 lo conserva).
- `TestE2E011001_C_HappyPath` (P3/P5/P10) — untouched: camino message-only, P7 de 003 preserva P10.
- `TestE2E011001_D_IDsEstables` (P11) — untouched: id estable de response/item message.
- `TestE2E011001_E_PipelineCompartida` (P4) — untouched: pipeline compartida, 003 la conserva (P9).
- Bloque `TestE2E011001_F_RechazosFeaturesFuturas` — 5 subtests untouched: `stream_true` (P6), `web_search_preview` (P7→P2/D3 de 003, sigue 400), `text_format_json_schema` (P8→traducción 002, 003 la conserva en P10), `part_input_file` (P9), `part_input_image` (P9).
- `TestE2E011001_G_CacheSeparadaPorEndpoint` (P12) — untouched: cache separada por endpoint (003 P9 la conserva).
- `TestE2E011001_H_StoreIgnorado` (P13) — untouched.
- `TestE2E011001_I_EnvelopeError` (P14) — untouched: envelope uniforme (P2/P10 de 003 lo conservan).

**`internal/proxy/e2e_011002_structuredoutput_test.go` — 7 top-level completos:**
- `TestE2E011002_P1_TraduccionJsonSchema` (P1), `P2_StrictPassthrough` (P2), `P3_TemperatureNoForzado` (P3), `P4_TipoNoJsonSchemaRechaza` (P4), `P5_SalidaIntacta` (P5), `P6_CachePorSchema` (P6), `P7_Upstream4xxSinDegradar` (P7) — todos untouched: 003 no toca `text.format`→`response_format` ni la salida message-only.

**Total untouched:** 15 top-level test functions completas + 5 subtests del bloque F (que permanece en su mayoría intacto).

## 5. Regression risk assessment

**Riesgo: sí — acotado a 2 puntos.**

1. **Alto — Traducción de entrada `tools` (P1/I3).** La rama de rechazo se reemplaza por una construcción estructural nueva (`tools` plano → anidado). Riesgo de shape incorrecto o de pérdida de byte-identidad de `parameters`. Mitigado por `test_postcondition_1` (I3 asertado) y por la reutilización del patrón de traducción ya probado de 002.
2. **Medio — `call_id`/round-trip (P6/P8/D1).** Si el implementer usa el `id` estable en lugar del eco del `id` upstream, el round-trip con Odoo se rompe silenciosamente. Mitigado por `test_postcondition_6` (eco explícito) y `test_postcondition_8` (id estable ≠ call_id).
3. **Medio — D3 mixto.** Riesgo de traducir un `tools` que mezcla function + no-function en lugar de rechazar 400. Cubierto en `test_postcondition_2` (caso mixto).
4. **Bajo — Passthrough por pipeline (P9).** Riesgo de que clamp/inyección de thinking dropeen `tools`/`parallel_tool_calls`. Cubierto por `test_postcondition_9`.
5. **Nulo —** Cero tests de `/v1/chat/completions` y cero de 011002 afectados (verificado por grep); la traducción vive solo en `responses.go` (I1/ADR-004).

**Beneficio de duda resuelto:** el subtest `tools` no se elimina ni se debilita — se convierte en assert de traducción (200) cubriendo P1; el 400 para `web_search_preview` sigue siendo cubierto por el subtest untouched homónimo (P2/D3).

## 6. Gate de Test Audit checklist

- [x] Comportamiento que cambia resumido (reemplazo P7 de 001 por traducción bidireccional, D1-D3).
- [x] Cada test modificado (1: subtest `tools` de bloque F) tiene justificación explícita en spec 003 §P1/§P2/D3.
- [x] Tests nuevos mapeados P1-P10, agrupados en 4 bloques (archivo nuevo `e2e_011003_toolcalling_test.go`).
- [x] Tests untouched listados explícitamente (15 top-level + 5 subtests de F).
- [x] Regression risks evaluados (sí — 2 altos/medios acotados a la nueva traducción; mitigados por los tests nuevos).
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de POST-AUDIT/RED.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
