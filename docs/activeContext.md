# activeContext.md — Contexto activo de mofgw

> Memory Bank: estado actual, decisiones recientes, próximos pasos, deuda conocida.
> Última actualización: 2026-08-12 (cdad-scribe, feature 011-004-file-attachments MERGED).

## Estado del programa

**✅ Programa mofgw 10/10 epics DONE (001-010). EN CURSO: EPIC-011 (mofgw-odoo) — mofgw como proveedor de IA de Odoo (reemplazo total de OpenAI). Features 011-001/002/003/004 MERGED 12 Ago (5/9 con 009).**

| Epic | Features | Status | Commit evidence |
|------|----------|--------|-----------------|
| EPIC-001 (core) | 7 features | ✅ DONE | e426f15d |
| EPIC-002 (resiliencia) | 4 features | ✅ DONE | 5966c5f4 |
| EPIC-003 (cache) | 1 feature | ✅ DONE | bb6367b→e5a9a16 |
| EPIC-004 (escala) | 2 features | ✅ DONE | (05 Ago) |
| EPIC-005 (ops) | 5 features | ✅ DONE | (05 Ago) |
| EPIC-006 (observabilidad) | 2 features | ✅ DONE | 119fe9d→2846c12 |
| EPIC-007 (catálogo) | 3 features | ✅ DONE | (06 Ago) |
| EPIC-008 (contexto) | 3 features | ✅ DONE | (06 Ago) |
| SEC-001 (hardening) | 4 fixes | ✅ DONE | b368656 |
| EPIC-009 (contexto-análisis) | 3 features | ✅ DONE | da8446c (E2E cross-feature) + closure-009.md |
| EPIC-010 (catálogo-fiel) | 2 features | ✅ DONE | (11 Ago, deploy prod) |
| EPIC-011 (odoo) | 001/002/003/004 DONE; 005-008 queued; 009 DONE | 🔄 EN CURSO | 69ddae1 (011-004) |

**Suite:** 17 paquetes, **465 tests verde con `-race`** (464 + test part desconocida fix review). vet limpio; gofmt limpio en archivos de la feature.
**EPIC-011 (mofgw-odoo):** Odoo 19 enterprise como cliente de mofgw — reemplazo total de OpenAI. Chat vía Responses API (001), structured output (002), tool-calling (003), file-attachments (004), embeddings via Ollama por cliente, staging enterprise en mofgw-staging.example.com (009 DONE).
**Deploy:** systemd user service activo en puerto 3369, providers reales (acct1, acct2, qwen/bailian, zen free).

## Decisiones recientes (cronología inversa)

### 12 Ago 2026 — Feature: 011-004-file-attachments (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-004-file-attachments DONE — traducción de las parts de archivo de Odoo (`input_file`/`input_image`) en `/v1/responses`.** Reemplaza la rama de rechazo P9 de 001 (400 "file attachments not yet supported") por la traducción real de las parts del `input[]` Responses al formato chat-completions que hablan los providers upstream. Todo el cambio vive en `internal/proxy/responses.go` (ADR-004/I1: router, providers y `ParseChatRequest` intactos). Suite completa **465 tests `-race`** verde en 17 paquetes (452 → 465, 12 tests feature nuevos en 6 bloques + POST-AUDIT en 001 y 003 + 1 fix review), go vet limpio. Commits: `7fce35b` RED, `69ddae1` GREEN.
- **D1 — decisión POR MENSAJE string-vs-array (P3):** un mensaje del `input[]` con SOLO parts `input_text` → `content` = **string** (concatenación de los `text`, exactamente como 001/003 — sin regresión en los caminos de texto plano); un mensaje con AL MENOS una part `input_image`/`input_file` → `content` = **array** con un item por part del input, en orden de aparición (I3). La decisión es por item, no por request: en un mismo `input[]` conviven mensajes string y array (verificado por `P3_MensajesMixtosPorMensaje`). El content-array se aplica SOLO a items `message`; los items `function_call`/`function_call_output` del ciclo tool-calling de 003 quedan intactos (P4).
- **D2 — mapeo de parts:** `input_text` → `{"type":"text","text":T}`; `input_image` → `{"type":"image_url","image_url":{"url":U,"detail":D?}}` (detail passthrough con omitempty: ausente → el campo no se emite, misma semántica que `strict` de 002/003); `input_file` → `{"type":"file","file":{"file_data":D,"filename":F}}` (part estilo OpenAI, sin detail). Data-URIs viajan como strings tal cual, sin re-marshal (I2, byte-identidad). Parts de tipo desconocido se dropean conservando el orden de las conocidas (consistente con el string-content que ignora lo que no es `input_text`).
- **D3 — sin gating por modality / PDF honesto (P6):** mofgw traduce y reenvía; NO conserva el 400 local, NO filtra por modality, NO convierte PDF. El `modality` es declarativo (solo catálogo `/v1/models`); el router elige por `req.Model`. Si el modelo elegido no soporta vision/archivos, el 4xx upstream se propaga **no-reintentable** (patrón D5 de 002). Verificación empírica del soporte PDF del provider real queda como **desviación no bloqueante**.
- **Sin cambio en salida ni en contexto (P8/P9):** la traducción de salida queda intacta (item `message`/`function_call` de 001/003); `estimatePromptTokens = len(body)/4` no se toca — las data-URI base64 inflan `len(body)` pero la ventana descuenta automáticamente los tokens sobreestimados (P8, sin cambio de código).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcionales **#1 APLICADO** (eliminar dead code `inputPart` de 001 sin callers tras POST-AUDIT) y **#3 APLICADO** (subtest `P3_PartTipoDesconocidoDropeada`: part `input_unknown` en mensaje content-array → 2 items en orden, la desconocida dropeada). #4/#5 **DESCARTADOS con motivo**, FYI #6 sin acción. Auditoría test↔postcondición: **9/9 postcondiciones con test** (P7/P9 por tests untouched de 001/003), ninguno sobrante, cero dependencia interna (AP-14).

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** soporte real de las parts `file` (PDF) y `image_url` por los providers upstream — mofgw traduce y propaga el 4xx no-reintentable (D5 de 002) si el provider no las soporta. Verificar con provider real.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py:283-304`, formato de parts verificado en el spec).

### Próxima feature en cola

- **011-005-web-search** (epic 011-mofgw-odoo): habilita `web_search_preview` / tools no-function (hoy 400 conservado como D3 de 003 y P7 de 004). Quedan 005-008 pendientes (001-004 done como troncos del epic; 009 staging-enterprise ya DONE — **5/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-003-tool-calling (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-003-tool-calling DONE — traducción del ciclo tool-calling de Odoo (tools ↔ function_call ↔ function_call_output) en `/v1/responses`.** Reemplaza la rama de rechazo P7 de 001 (400 "tool calling not yet supported") por la traducción real bidireccional en las tres direcciones: (1) entrada `body["tools"]` (formato Responses plano) → `tools` chat-completions anidado bajo `function`; (2) salida `choices[0].message.tool_calls` → items `function_call` del `output[]`; (3) entrada item `function_call_output` → mensaje `role:"tool"`. **Quién maneja el ciclo: Odoo** ejecuta las tools por su cuenta y arma el siguiente request; mofgw solo traduce (stateless, sin estado de sesión — cada request es una traducción puntual del `input[]` completo). Suite completa **452 tests `-race`** verde en 17 paquetes (426 → 452), go vet limpio. Commits: `cea354e` RED, `fefb55c` GREEN.
- **D1 — `call_id` = eco directo del id upstream (base del round-trip):** el `call_id` del item `function_call` de salida (P6) == `tool_calls[i].id` upstream; el `id` estable del item (P11 de 001, `responsesID`) va APARTE (no == call_id, P8). Solo así Odoo devuelve `function_call_output.call_id` == ese mismo id y mofgw lo mapea al `role:"tool"` con `tool_call_id` correcto (P4/I4). En entrada, el `tool_call.id` del assistant == `call_id` del item `function_call` (fallback al `id` si no trae `call_id`), cerrando el round-trip en ambas direcciones.
- **D2 — solo items `function_call` cuando hay tool_calls (cero items message):** con `tool_calls` no vacío, `output[]` contiene EXACTAMENTE un item `function_call` por `tool_calls[i]` y NINGÚN item `message` (Odoo ignora el texto del message en esa rama, `elif not has_tool_calls`). Con `tool_calls` vacío/ausente → item `message` intacto (P7, P10 de 001). Shape verificado parseable por el snippet real `_request_llm_openai_helper` (lee `name`/`arguments`/`call_id` → `to_call == [(name, call_id, arguments)]`).
- **D3 — `type != "function"` → 400 conservado (lo resuelve 005):** si `tools` contiene CUALQUIER tool de `type != "function"` (incluido `web_search_preview` y el caso mixto function+no-function), HTTP 400 "tool calling not yet supported" — el chequeo ocurre ANTES de traducir. 003 traduce solo los `type == "function"`. Rechazo de tool JSON malformado separado: **"invalid tool definition"** (opcional #3 de la review, APLICADO).
- **Byte-identidad I3 e invariantes sostenidos:** `parameters` de cada tool viaja como `json.RawMessage` y se serializa verbatim (byte-idéntico al input, sin re-marshal); `arguments` del item de salida es el string JSON crudo parseable por `json.loads` (I2); `strict` con omitempty (ausente → no se emite, misma semántica que D2 de 002). `tools`/`parallel_tool_calls` viajan como passthrough en `chatBodyMap` (inyectados antes del `Marshal`) y sobreviven clamp e inyección de thinking (P9), con cache exact-match separada por endpoint que cambia de key con los tools.
- **provider.go: solo type definitions (I1/ADR-004 sin romper):** `ChatToolCall` (id/type/function{name,arguments}) y `ToolCalls []ChatToolCall` en `ChatMessage` (`omitempty`). NO modifica `ParseChatRequest`, router ni providers; `responsesInputItem` discriminado por `type` (message / function_call / function_call_output) cubre los items mixtos del `input[]`. Todo el cambio de la feature vive en `internal/proxy/responses.go`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #3 **APLICADO** — unmarshal fallido de tool → "invalid tool definition" (rama separada del 400 "tool calling not yet supported" que queda solo para type!=function). Opcionales #1 (agrupar function_call adyacentes en un único assistant multi-tool_call) y #2 (subtest tool_calls_vacio no ejercita `[]`) **DESCARTADOS con motivo**. FYI #4/#5 sin acción. Auditoría test↔postcondición: P1-P10 todos con test (10/10), ninguno sobrante, cero dependencia interna (AP-14). POST-AUDIT en 001: subtest `tools` flip 400→200 (traducción verificada), `web_search_preview` conserva 400 (D3).

### Deuda técnica detectada

- **Agrupación de `function_call` adyacentes en un único mensaje assistant multi-tool_call** (hardening futuro): el contrato especifica 1:1 (un item function_call → un mensaje assistant con un tool_call), cubriendo el caso de un solo tool (el común en Odoo); con `parallel_tool_calls:true` y varios adyacentes, hoy se generan N mensajes assistant separados. Funcional para Odoo (parsea tool_calls uno a uno); documentado como out-of-scope en el spec, optimización de implementación a futuro.
- **Verificación empírica pendiente (desviación no bloqueante):** aceptación de `strict` en tools por los providers reales upstream — el diseño pasa `strict` tal cual; si un provider lo rechaza, mofgw propaga el 4xx no-reintentable (patrón D5 de 002, sin degradación). Verificar con provider real.
- **Deuda del epic vigente (sin cambio):** streaming SSE del Responses API (deuda del epic), y `web_search_preview`/tools no-function que hoy son 400 y los resuelve la feature 005.

### Próxima feature en cola

- **011-004-file-attachments** (epic 011-mofgw-odoo): habilita las parts `input_file`/`input_image` del `input[]` (hoy 400 "file attachments not yet supported", P9 de 001). Quedan 004-008 pendientes (001/002/003 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-002-structured-output (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-002-structured-output DONE — `text.format.json_schema` → `response_format` en la ENTRADA de `/v1/responses` (structured output, AI Field Fill de Odoo).** Reemplaza el rechazo P8 de 001 (400 "structured output not yet supported") por la traducción real: cuando Odoo manda `text.format = {"type":"json_schema",...}`, mofgw inyecta `response_format = {"type":"json_schema","json_schema":{...}}` en el body chat-completions que delega al provider. **Alcance estricto: solo la entrada; la salida NO cambia** — sigue el shape P10 de 001 (`output[0]` item `message`, `content[0].text == choices[0].message.content`), el JSON estructurado viaja como texto normal del `content`, verificado contra `_request_llm_openai_helper`. Suite completa **426 tests `-race`** verde en 17 paquetes (413 → 426, 7 tests nuevos + POST-AUDIT), go vet limpio. Commits: `6223207` RED, `de162e1` GREEN.
- **Mapeo D1 — `response_format` estructural (no plano):** `text.format` (4 campos planos de Odoo) → `response_format = {"type":"json_schema","json_schema":{"name","schema","strict"}}`. El `schema` viaja como `json.RawMessage` (responses.go:151,158) y se serializa verbatim → **byte-identidad I3** sostenida en toda la cadena (el clamp opera sobre `map[string]json.RawMessage`, `response_format` es passthrough opaco que no desarma). `strict` con `omitempty` (D2): presente → valor exacto; ausente → campo no se emite.
- **D3/D4/D5 (decisiones del brainstorm mantenidas):** solo `type == "json_schema"` se traduce; el resto de `type` o un format malformado conserva la rama P8 (400 "structured output not yet supported") (D3). `temperature` tal cual, NO forzado a 0 por tener structured output (D4). Sin degradación a `json_object`: provider que no soporta `response_format` → mofgw propaga el 4xx upstream no-reintentable, sin fallback de menor capacidad (D5, `router.Complete` → `handleChainError` → `absorb.Respond`).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #1 **APLICADO** — `wireResponseFormat`/`wireJSONSchema` tipados (structs locales no exportados con `omitempty` en `Strict`), dando type-safety de compilador (un typo de clave ya no compila) y resolviendo D2 sin `if` explícito. Opcionales #2 (guard local de `schema` ausente — el upstream propaga 4xx, funcionalmente correcto, P7) y #3 (POST-AUDIT dentro de función "Rechazos" — cosmético) **DESCARTADOS con motivo**. Auditoría test↔postcondición: P1-P8 todos con test, ninguno sobrante, cero dependencia interna (AP-14).
- **Pipeline compartida sin regresión (P6):** `response_format` viaja como campo passthrough en `chatBodyMap` (inyectado antes del `Marshal`), preservado por clamp e inyección de thinking; atraviesa limiter/cache/clamp/ventana/budget/ruteo idéntico a 001. Cache exact-match con key separada por endpoint (P12 de 001): misma `schema` + mismo body → HIT; `schema` distinta → MISS (verificado por `TestE2E011002_P6_CachePorSchema`).

### Deuda técnica detectada

- **Ninguna deuda bloqueante.** Decisiones del brainstorm D1-D5 y fix #1 aplicados en su totalidad; #2/#3 de la review descartados con motivo documentado en `review.md`. La verificación empírica del soporte real de `response_format` por un provider (P7, desviación no bloqueante del spec) sigue pendiente — el diseño falla explícito con 4xx no-reintentable, sin degradación silenciosa.
- **Nota de higiene de state file:** `.cdad-state.json` tenía la clave `postconditions_status` **duplicada** (JSON ambiguo donde el segundo `map` sobreescribe al primero). Deduplicada en el cierre de esta feature.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa el Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-003-tool-calling** (epic 011-mofgw-odoo): habilita el ciclo tool-calling de Odoo. Reemplaza el rechazo P7 de 001 (400 "tool calling not yet supported"). El `call_id`/item id estable de 001 (P11) ya está pensado para este ciclo (Odoo reutiliza el id de item). Quedan 003-008 pendientes (001 y 002 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-001-responses-endpoint (tronco del epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-001-responses-endpoint DONE — `/v1/responses` en mofgw, tronco del epic 011-mofgw-odoo (reemplazo total de OpenAI en Odoo 19).** Implementa el Responses API (`POST /v1/responses`) que Odoo 19 enterprise usa en `_request_llm_openai_helper`, traduciendo Responses↔ChatCompletions no-stream. Se registra junto a `/v1/chat/completions` (proxy.go:283) bajo `s.auth.Wrap`. Suite completa **413 tests `-race`** verde en 17 paquetes (388 → 413, +25 tests feature), go vet limpio. Commits: `d2142f6` RED (24 fallan por 404 + 1 pasa 401 vía auth.Wrap), `3bec0b5` GREEN, `595e742` fixes review. Es el 1er feature del epic 011.
- **Traducción vive SOLO en la capa del endpoint (I1) — decisión arquitectónica del epic → ADR-004:** `handleResponses` (responses.go) traduce `input`→`messages`, delega en la MISMA pipeline de chat (`router.Complete`) y traduce la salida a `output[]`. `ParseChatRequest`, router y providers intactos. El body Responses viaja crudo por la cadena (pattern mofgw de transparencia). Esta es la frontera que las features 002-005 van a extender.
- **Modo solo NO-stream (D1) + rechazos explícitos de features futuras (P6-P9):** `stream:true` → 400 "streaming not supported yet"; `tools`/`web_search_preview` → 400 "tool calling not yet supported"; `text.format.json_schema` → 400 "structured output not yet supported"; part `input_file`/`input_image` → 400 "file attachments not yet supported". Features 002-005 reemplazan los rechazos; streaming SSE queda como deuda del epic.
- **`output[]` alineado al parser real de Odoo (D2, P10):** item `{"type":"message","id":"<stable>","role":"assistant","status":"completed","content":[{"type":"output_text","text":...}]}` — parseable por `_request_llm_openai_helper` (lee `content[i]["text"]`). Verificado contra `llm_api_service.py:364-397`.
- **IDs estables/determinísticas (P11):** sha256(clientID+body) con prefijo `resp_` → mismo input = mismo id de response y de item. Habilita que Odoo reutilice el id de item como `call_id` en el ciclo tool-calling de 003.
- **Cache separada por endpoint (P12):** `responsesCacheKey` usa namespace propio (prefix `"responses"` + clientID + body canónico) → una responses-response jamás se sirve como chat-response ni a la inversa. Cache solo para requests determinísticos (`temperature == 0`). La separación cross-endpoint es invariante estructural garantizada por el body wire (input vs messages).
- **Pipeline compartida con chat (P4):** limiter global + keyed + **por agente**, telemetría de descubrimiento (009-000), contextAnalysis (009-001), clamp por modelo, context-window check, budget, usage/cost accounting, `recordCacheTokens`, `setUsageHeaders`, `emitRequestTelemetry` + `emitRequestEnd`. `store` ignorado/stateless (P13); `max_output_tokens` no-op (Odoo no lo manda). Modelo forzado a nivel raíz con `rewriteResponseModel` (P5).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 1 bloqueante (test G P12 no ejercitaba la cache: bodies sin `temperature` → `isDeterministic=false` → MISS obligatorios y test vacío) **RESUELTO** agregando `temperature: 0` para ejercitar el camino HIT/MISS real. Opcionales #2 (telemetría/context/emitRequestEnd) y #3 (rate limit por agente) **APLICADOS**; #4 (singleflight) → deuda documentada, #5 (doble marshaling) y #7 (validación de role) descartados nits, #6 (model del envelope) no aplicado (FYI, patrón consistente con chat). Cero bloqueantes pendientes.

### Deuda técnica detectada

- **Llevadas a TECHDEBT consolidado:** streaming SSE del Responses API (fuera de alcance de 001, deuda del epic), singleflight/coalescing para `/v1/responses` (opcional #4 desestimado → feature futura), y el resto del scope cross-endpoint de 002-005 (structured output, tool calling, file attachments, web search) que hoy son 400.
- **Nota P12 (de la review):** la separación de namespace por endpoint es un invariante estructural garantizado por el hash del body wire responses (nunca coincide con el body wire chat), no end-to-end demostrable por el test.
- **Riesgo del epic vigente (plan-011):** Odoo 19 usa Responses API en estado "preview" para varias features; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-002-structured-output** (epic 011-mofgw-odoo): `text.format.json_schema` → `response_format` (AI Field Fill). Reemplaza el rechazo P8 (400 "structured output not yet supported"). Depende de 011-001. Coordinar vía `cdad-epic`. Quedan 002-008 pendientes (001 es el tronco; 009 staging-enterprise ya DONE).

### 11 Ago 2026 — fix: follower de single-flight factura su clientID + X-Usage-*

- **Bug corregido (RED→GREEN granular, `6ace500` + `d4727bd`):** en el coalescing single-flight (010-001 P0), `recordCacheTokens` y `setUsageHeaders` corrían solo dentro de `fn()` (el líder). El follower reutilizaba el blob compartido pero no facturaba a SU `clientID` (006-001 P2) ni exponía `X-Usage-*` en SU response (007-003 P1) — sus headers quedaban en cero. Encontrado por auditoría de Claude en `proxy.go`, sin test que lo cubriera.
- **Fix:** flag `leader` capturado en `fn()`; en la rama `shared==true`, para `!leader` se reconstruye `*provider.Usage` desde `sfRes.Usage` y se llama `recordCacheTokens(clientID)` + `setUsageHeaders(w.Header())` + `lastUsage`. El flag evita doble-facturar al líder. Nota menor: `singleflight.Usage` no lleva `ReasoningTokens` → el follower reporta reasoning 0 en `recordCacheTokens` (no afecta headers ni facturación por cliente; mejora separada si se quiere tracking exacto).
- **Test nuevo `TestSF_FollowerFacturaYExponeUsage` (e2e_singleflight_test.go):** bloquea el upstream del líder para garantizar la superposición y aserta que el follower (a) fue deduplicado (`mofgw_single_flight_deduped_total{client="client-k2"} 1` — condición de validez), (b) factura a su clientID en /metrics, y (c) recibe `X-Usage-Total-Tokens: 1280`.
- **Evidencia:** suite completa **388 tests `-race` verde en 17 paquetes** (387 → 388), build + vet + gofmt limpios. Bug era latente: `singleFlightEnabled` off por default — corregido de todos modos.

### 11 Ago 2026 — EPIC-010 mofgw-catalogo-fiel CERRADO + DEPLOYADO

### Decisiones relevantes

- **EPIC-010 (mofgw-catalogo-fiel) CERRADO — 2/2 features done, ambas mergeadas + desplegadas en prod + verificadas 11 Ago.** El epic cerró los huecos de fidelidad del catálogo detectados en la auditoría del 10 Ago: /v1/models ahora informa completo, veraz y prescriptivo (010-001), y el request path hace valer lo que el catálogo declara (010-002). Cadena entregada: catálogo fiel → clamp/inyección en el request path. Los 4 criterios de aceptación del epic (plan.md) verificados. Suite completa **387 tests `-race`** verde (332 → 387), vet + gofmt limpios, 17 paquetes.
- **010-001-catalogo-fiel (MERGED, deployado):** `/v1/models` emite por modelo — `supported_parameters` [tools,reasoning], `modality` + `architecture` (input/output_modalities; `text+image+video->text` para minimax/kimi/qwen, `text->text` para deepseek/glm/flash-0731), `top_provider` {context_length, max_completion_tokens}, `max_output_tokens`, y `thinking_default` **prescriptivo** (ADR-003). Metadata de `deepseek-v4-flash-0731` agregada. `qwen3.7-plus max_output` corregido 65536 → **131072** (D8). Fallback rule: capability no declarada/verificada se OMITE, nunca se adivina (ausente ≠ 0/[]/false).
- **C3 enmendado (P6 wins, 11 Ago):** la review del 010-001 detectó contradicción interna C3-vs-P6 — `top_provider`/`max_output_tokens` NO son capabilities nuevas sujetas a fallback rule, son **alias derivados** de `context_window`/`max_output` (007-002) y se emiten cuando sus fuentes son > 0. Enmendado en spec por decisión del dueño (GVR), tests respetan la enmienda.
- **010-002-request-path-fiel (MERGED, deployado):** (1) **clamp por modelo pre-routing** en el proxy (`min(raw, modelMaxOutput)`, convive con el clamp por provider del router → efectivo `min(modelo, provider)`) — kimi-k2.7-code (max_output 32768) con max_tokens 384000 ya no produce el 400→502; (2) **window-check fiel**: usa el max_tokens EFECTIVO post-clamp (`promptTokens + min(raw, MaxOutput)` vs `window×(1+margin)`), preservando el fix TECHDEBT #23; (3) **inyección per-attempt provider-aware** del `thinking_default` via knob declarativo `providers[].thinking_path` (`""`|`zen`|`bailian`, default `""` = sin inyección): zen → `reasoning_effort`; bailian → `reasoning_effort` + `enable_thinking` (deepseek/glm) o `enable_thinking` (qwen); kimi/minimax **NUNCA** (always-on / adaptive nativo == prescriptivo). Nunca pisa effort explícito del cliente (P5). Ruteo/fallback/clasificación intactos (4xx sigue no-reintentable, P9).
- **POST-AUDIT mandatorio aplicado:** `TestRED_MaxTokensCuentaEnVentana` (e2e_008001) cambió de contrato con la regla fiel — **flip 400 → 200** (750 + min(400,100) = 850 ≤ 1000). El test existente se actualizó ANTES del GREEN (premisa reemplazada: el max_tokens crudo ya no cuenta contra la ventana). Colisión de nombre de archivo resuelta: los tests de esta feature viven en `e2e_010002_requestpath_test.go` (el `e2e_010002_test.go` ya estaba ocupado por la feature response-cache, numeración previa del EPIC-010 de eficiencia).
- **Decisiones de arquitectura:** ADR-003 (thinking_default **prescriptivo** — el configurado altera el nativo) creado en 010-001. Knob `providers[].thinking_path` adoptado por decisión del dueño 11 Ago (declarativo, consistente con I2 — valores de config, nunca inferidos por patrón de ID: los IDs reales acct1/acct2/qwen son frágiles para inferir path).
- **Reviews (qwen3.7-plus, familia distinta al implementer):** 010-001 APPROVE WITH COMMENTS — bloqueante C7 (config.example.yaml) RESUELTO 11 Ago, 2 nits/FYI sin acción; 010-002 APPROVE WITH COMMENTS — 0 bloqueantes, Major #1 (qwen thinking) resuelto, Minor #2 → TECHDEBT #25, Minor #3 (thinking_path) resuelto, nits #4/#5 aceptados, FYI #6.
- **Verificado en prod (11 Ago):** `/v1/models` responde los 7 modelos con metadata completa; **bailian ACEPTA `max_tokens=131072` para qwen3.7-plus** (200, sin 400 — verificación empírica pendiente del spec resuelta); kimi `max_tokens=100000` → **200** (clamp funciona, sin 400/502); deepseek smoke → 200 con thinking activo (inyección). healthz 6 providers OK. Suite 387 tests `-race` verde, vet + gofmt limpios.

### Deuda técnica detectada

- **Llevadas al índice consolidado (TECHDEBT, Cambios 11 Ago):** #25 (`thinkingProfile` frágil ante cambios de config — derivación por niveles de Thinking orden-dependiente, ACEPTADA sin cambio estructural, LOW; si el catálogo evoluciona → campo declarativo `ThinkingFamily` o unit test que fije la semántica), #26 (`qwen3.7-plus max_output` 64K→128K corregido y verificado empíricamente en deploy: bailian acepta 131072), #27 (`bodyHasExplicitEffort` re-parsea el body — optimización opcional, LOW). #4 cerrada completa (flash-0731 pricing + metadata en prod + example).
- **Verificaciones empíricas pendientes (Phase 4, fuera del contrato):** glm-5.2 `reasoning_effort=medium` vía bailian (la tabla §5.4 dice high/max only — si bailian lo ignora silenciosamente, el wire value enviado es el configurado; discrepancia → TECHDEBT); context window real minimax/glm/qwen (1,000,000 vs 1,048,576 — verificación empírica del techo pendiente, NO cambiar valores). Nits de la review 010-001 sin registro (nit preexistente: `created: time.Now()` por respuesta; FYI: `top_provider` es eco del formato OpenRouter, sin concepto en mofgw).
- **Dos decisiones del 010-001 a mantener en memoria:** fallback rule (capabilities no verificadas se omiten) y modality honesta negativa (`text->text` = "no documentada como soportada", no conjetura).

### Próxima feature en cola

- **Ninguna — el epic 010 está cerrado.** El programa mofgw queda con los **10 epics done (001-010)**. Restan los criterios de aceptación del programa completo (plan.md §Criterios del programa): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo residual: vigilar el anuncio de suba de precios de deepseek (TECHDEBT #4), monitorear cuota GO_2/GO_3 (#5), y la aceptación empírica de glm medium vía bailian cuando aplique.

### 09 Ago 2026 — Decisión: sticky_routing NO se habilita (póliza default-off)

- **Decisión formal del dueño del proceso: `fallback.sticky_routing` queda DESHABILITADO en prod (default off), documentado como decisión, no como deuda.** La feature 009-002 está implementada, testeada (332 tests) y mergeada — el costo de habilitarla es solo un toggle de config + reinicio.
- **Por qué no:** la data real muestra que no haría diferencia hoy — `failovers=0` y `cooldown_hits=0` en ~19.7K requests (la cadena nunca rotó), y el cache hit ya está en ~97.7% sin sticky (el orden de config ya es de facto sticky). El sticky solo paga cuando la cadena rota (cooldown/429/5xx), escenario no observado. Para openclaw/zot (sin sesión) es redundante con el orden de config.
- **Cuándo re-evaluar:** si aparecen cooldowns recurrentes (señal existente: TECHDEBT #5 — GO_2/GO_3 en 429 por cuota mensual). Si se habilita, validar empíricamente con la frecuencia del log `sticky_applied` en mofgw.log (esperado ~0 hoy).
- **Estado:** póliza activa — código listo, default off, ADR-002 documenta el diseño (reordenamiento post-filtro, nunca fuerza cooldown/health/cadena).

### 09 Ago 2026 — Verificación de criterios del programa en prod

- **Métricas vivas confirman el proxy absorbiendo todo el tráfico real:** 18,221 requests / 18,121 streams, **0 failovers, 0 cooldown hits, 87 errores totales** (todos absorbidos — el cliente nunca los ve). RAM 18.6M. Cache hit ~97.7% (2.31B hit vs 55M miss). Costos contabilizados: $77.09 USD total, cliente `zot`, por provider/modelo (`/metrics`).
- **Criterios del programa verificados en prod (plan.md §Criterios):** /metrics contabiliza tokens y costo por cliente/modelo/provider ✅; /v1/models responde (con capabilities, 007-002) ✅; rechazo temprano por ventana (008-001) activo en el binario ✅; omniroute no aparece como servicio ✅ (criterio E2E integral 48h + 10+ agentes = validación de observación pendiente).
- **Telemetría viva (1,943 eventos):** opencode capturado con `X-Session-Id`/`X-Session-Affinity` reales, key paths correctos, sin contenido (privacidad). 
- **Composición habilitada:** `/v1/context` responde 401 sin auth (ruta registrada + protegida — esperado); la verificación autenticada de acumulación de records requiere la key plana del cliente `zot` (operativo del operador, no expuesto en el registro).

### 09 Ago 2026 — Deploy del epic 009 en producción

- **Binario con las 3 features del epic desplegado** (09 Ago, ~02:12 ART): el binario anterior en `~/.local/bin/mofgw` NO tenía 009-001/009-002 (verificado por strings: 0 matches vs 16 del build nuevo). Reemplazado por build fresco (`go build ./cmd/mofgw`, 10.3M), backup en `mofgw.bak-20260809`. Servicio reiniciado: healthz OK (6 providers healthy), state.json restaurado (v1→v2 sin error — C12 funcionando en prod), config cargada, tráfico real fluyendo (WARN "telemetry header negado" de agentes con X-Parent-Session-Id — telemetría 009-000 viva).
- **`context.analysis` HABILITADO en prod** (read-only, no invasivo — P8 de 009-001): `~/.config/mofgw/config.yaml` → `context: {margin: 0.1, analysis: {enabled: true, history_per_session: 50}}`. El endpoint `GET /v1/context` queda activo para el cliente `zot` (auth Bearer).
- **`fallback.sticky_routing` queda DEFAULT OFF** (decisión de criterio): es la feature más delicada para activar en tráfico real (cambia ruteo de requests vivos). Se habilita tras observar la composición funcionando — el diseño post-filtro (ADR-002) garantiza que no rompe la cadena si se activa, pero se prefiere activación escalonada.
- **plan.md actualizado** (commit `3a484f8`): status del cierre del epic 009 + criterios del programa 9/9 done.

## 2026-08-09 — Epic 009 cerrado

### Decisiones relevantes

- **EPIC-009 (mofgw-contexto-analisis) CERRADO — 3/3 features done, integración cross-feature verde (E3) y closure (E4) completados.** El epic cumplió su objetivo: descubrir y optimizar la composición del contexto del tráfico real. Cadena entregada: telemetría de descubrimiento (009-000, desplegada en prod 09 Ago) → composición de contexto `/v1/context` (009-001) → sticky routing por sesión (009-002). Los 4 criterios de aceptación del epic (plan.md:211-215) verificados: E2E cross-feature `TestE2EEpic009_FlujoCompuesto` (6 subtests) verde; suite completa **332 tests `-race`** (326 + 6 cross-feature); **cero bugs cross-feature** (integration-009.md, commit `da8446c`).
- **Dual-keying confirmado por captura real (~103 eventos) — el criterio de cierre del epic (ADR-001, creado en 009-001):** solo opencode manda `X-Session-Id` (header); openclaw/zot no (ni header ni metadata, `detected_ids` = 0 en todos los eventos) → composición y sticky keyed `client|session` para opencode y `client|` para openclaw/zot, conviviendo simultáneamente en runtime.
- **Sticky como reordenamiento post-filtro (ADR-002, creado en 009-002):** `applyStickyReorder` solo mueve al preferido al frente del slice `ready` post-`resolveReady` (cooldown/health/Serves ya aplicaron); nunca fuerza. El criterio del epic "respeta cooldown/health/cadena" (plan.md:215) queda cubierto por diseño, no por accidente.
- **Integración E3 sin fricción:** las 3 features conviven en el proxy sin pisarse — la fase 2 de 009-001 (`UpdateContextUsage`) y el registro sticky de 009-002 (`Affinity().Set`) viven en el MISMO punto (`recordCacheTokens`) y coexisten sin conflicto; el dual-keying se confirmó en el flujo real con y sin sesión. Cero refactor de features done.
- **Operativo pendiente (default off — no altera comportamiento sin habilitarlo):** activar `context.analysis.enabled: true` (009-001) + `fallback.sticky_routing.enabled: true` (009-002) en `~/.config/mofgw/config.yaml` de prod. `telemetry` ya está activa (009-000, desplegada).

### Deuda técnica detectada

- **El epic se lleva al índice consolidado (TECHDEBT):** #20 (gofmt preexistente en `e2e_009000_test.go`, LOW), #21 (concurrencia sticky sin test dedicado — hardening post-cierre, LOW), #22 (`X-Session-Affinity` sin test dedicado — hardening post-cierre, FYI) + nits sin registro de la review 009-002 (idiom `evictLRU`; `AffinityStore` incondicional — aceptados, sin acción). Ninguna bloqueante; el resto de la deuda del epic (16-19) ya está registrada.
- **Dos lecciones de proceso del epic (AUDIT post-hoc, documentadas en TECHDEBT §Deuda de proceso):** (1) 009-001 — el AUDIT previo al RED debe escanear literales de fixture que el spec cambia (bump `stateVersion` 1→2); (2) 009-002 — los contadores/harness deben medir lo que el test afirma (5 de 32 tests nuevos nacieron con bugs de test, detectados en GREEN).

### Próxima feature en cola

- **Ninguna — el epic 009 está cerrado.** El programa mofgw queda con los 9 epics done (001-009). Siguiente paso: criterios de aceptación del programa completo (plan.md §Criterios del programa) — E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo opcional: habilitar `context.analysis` + `sticky_routing` en prod.

### 09 Ago 2026 — Feature: 009-002-sticky-session (cierre de ciclo — última del epic)

## 2026-08-09 — Feature: 009-002-sticky-session

### Decisiones relevantes

- **Afinidad como reordenamiento post-filtro (I2/P9) — el corazón de la feature:** `applyStickyReorder` actúa SOLO sobre el slice `ready` post-`resolveReady` (ya post-cooldown/health/Serves): si el preferido está en `ready`, se mueve al frente preservando el orden relativo del resto; si NO está (cooldown, unhealthy, ya no sirve el modelo, clave ausente/evictada, stickyKey vacío) → slice intacto, comportamiento IDÉNTICO a legacy. NUNCA fuerza un provider que el filtro descartó, nunca cambia `maxAttempts` ni la clasificación de errores. Garantiza el criterio del epic (plan.md:215, "respeta cooldown/health/cadena") y la transparencia total (P7/I1/D6): el cliente nunca percibe el ruteo; única diferencia observable es el log debug `sticky_applied`. → **ADR-002**.
- **Keying `client|session` + `client|` (P2, consume ADR-001):** misma fuente de sesión que 009-001 — `X-Session-Id` solo lectura, nunca upstream (I4). opencode → `clientID|sessionID` (afinidad por sesión real); openclaw/zot sin sesión → `clientID|` (afinidad por cliente, comportamiento deseado según captura 009-000). `X-Session-Affinity` deliberadamente NO se usa (D5/I3: en opencode es idéntico a `X-Session-Id`). `clientID` nunca vacío (token autenticado) → clave nunca `""`.
- **`AffinityStore` efímero LRU (P3/D3/D4):** store en memoria (patrón `CooldownStore`; restart → frío; sin bump de `stateVersion`), evicción LRU por last-used (Set y Get refrescan — un cliente activo no se evicta, C12), cap = `Options.StickyMaxEntries` REUTILIZANDO el MISMO knob `server.max_sessions_retained` (default 100): D1 limita la config a `enabled`, el operador gobierna retención de sesiones y afinidad con un solo knob. Claves `client|` cuentan contra el mismo tope; pérdida benigna (re-registra en frío).
- **`CompleteFor`/`StreamFor` con backward compat total (P8/D2):** refactor del loop de `Complete`/`Stream` a helpers compartidos (`complete`/`stream` + `applyStickyReorder`); `Complete`/`Stream`/`New` legacy intactos (stickyKey `""` → retorno inmediato); `StickyMaxEntries` nuevo en Options (0 = default). Con sticky off (default) el proxy llama legacy → cero diferencias observables (C11), e2e existentes pasan sin cambios.
- **Registro post-éxito del GANADOR (P6):** en `recordCacheTokens` (punto único post-éxito con providerID para stream y no-stream), gated por `s.stickyRouting` (off → store vacío, cero memoria). Registra el provider que RESPONDIÓ/arrancó el stream (no necesariamente el preferido). Fallo total (502) o rechazos pre-router (400/413/limiter/budget/ventana) NO tocan la afinidad (C9); stream arrancado que muere a mitad SÍ registra (cache calentado). Best-effort: error de registro jamás falla el request.
- **No invasivo + bounds (P9):** sin cambios en limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta)/telemetría; sin métricas nuevas (solo log debug); cardinalidad acotada (LRU) + mutex, seguro concurrente. Limitación aceptada (D4): afinidad por clave, no por `(clave, modelo)` — el óptimo de cache del segundo modelo puede no alcanzarse; corrección siempre garantizada vía `Serves`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes, 5 opcionales (4 aceptados como deuda #21-#22 + nits sin registro; 1 aplicado — Nit #5: `max_sessions_retained` documentado en `config.example.yaml`). Suite completa 326 tests `-race` verde (294 + 32 nuevos), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Feature aditiva (test-audit):** 0 tests existentes modificados, 38 archivos untouched, 32 tests nuevos (3 config + 18 router + 11 e2e) que cubren P1-P9 / C1-C13; 5 fixes de tests NUEVOS documentados (§5.2) con lección: **los contadores deben medir lo que el test afirma** (`fakeProvider.Stream` no cuenta en `callCount`; el health check de `buildSticky` contamina contadores; `upstreamOK` no produce costo → budget nunca dispara).
- **Operativo:** la feature cierra el ciclo (merge) y el EPIC-009 (3/3 done). Habilitar `fallback.sticky_routing.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo) — junto con `context.analysis.enabled` de 009-001.

### Deuda técnica detectada

- **TECHDEBT #21** — concurrencia sticky sin test dedicado (C13): garantía estructural (mutex de `AffinityStore`) + `-race` en suite; sin TOCTOU. Test concurrente dedicado → hardening post-cierre.
- **TECHDEBT #22** — `X-Session-Affinity` irrelevante sin test dedicado (I3/D5): garantía por construcción (proxy solo lee `X-Session-Id`) + guard `e2e_008003` untouched. Test dedicado → hardening post-cierre.
- **Nits de la review sin registro** (aceptados, sin acción de mejora): `evictLRU` con flag `first` (idiom Go correcto, refactor cosmético opcional); `AffinityStore` creado incondicionalmente (costo ~120 bytes, consistente con `CooldownStore`); `Get` refresca siempre `LastUsed` (semántica EXACTA del spec P3, revisado "no cambiar").
- **Lección de proceso (test-audit §2/§5.2):** AUDIT previo al RED no ejecutado como sesión formal → 5 de los 32 tests nuevos nacieron con bugs de test (detectados en GREEN: 3 por el implementer respetando AP-4, 2 por el orquestador). Lección: además de escanear fixtures (009-001), escanear que los contadores/harness midan lo que el test afirma medir. Ver entrada de proceso en TECHDEBT.

### Próxima feature en cola

- **Ninguna del epic 009 — las 3 features están done.** Siguiente paso: **integración cross-feature (E3) y closure del epic (E4)** vía `cdad-epic` (chat nuevo): verificar los criterios de aceptación del epic (telemetry.jsonl capturando; `docs/research-context-patterns.md`; `/v1/context?session=<id>`; sticky routing opcional por config transparente que respeta cooldown/health/cadena — este último cubierto por 009-002), habilitar las features en prod y decidir el cierre formal.

### 09 Ago 2026 — Feature: 009-001-context-composition (cierre de ciclo)

## 2026-08-09 — Feature: 009-001-context-composition

### Decisiones relevantes

- **Dual-keying confirmado por captura (~103 eventos) — decisión de cierre del epic resuelta:** opencode manda `X-Session-Id` en header → composición keyed `client|session`; openclaw/zot NO mandan sesión (ni header ni metadata del body, `detected_ids` = 0 en todos los eventos) → composición keyed `client|` (agregado por client_id, fallback diseñado). Ambos keyings conviven simultáneamente en runtime (P2/C15). Fuente: `docs/research-context-patterns.md`. → **ADR-001**.
- **Endpoint `GET /v1/context` con aislamiento por clientID** (auth Bearer sobre `/v1/*`): `?session=<id>` → `scope:"session"` con 404 si la sesión no existe para el cliente (consistente 008-003 P5); sin session → `scope:"client"` con el agregado `client|` (incluye TODO el tráfico del cliente, con y sin sesión). Shape `{client, scope, session?, requests, summary, latest, history}`, ceros/vacío nunca nil (P3).
- **Parser estructural en UN solo recorrido (`composition.Analyze`):** superset de `Walk` (absorbe el refactor del audit 009-000 R1); `Walk` intacto y verificado semánticamente idéntico (C3, schema lock R10 de telemetría verde); privacidad por construcción — solo metadata estructural, nunca contenido (P7/I1).
- **`prompt_tokens_actual` en DOS fases:** fase 1 pre-request (mismo gate que telemetría: body válido, stream y no-stream, incluidos los rechazos por limiter/budget/ventana/upstream — P4/C9); fase 2 post-response con matcheo exacto por `request_id` (evita races entre handlers concurrentes de la misma sesión). Best-effort: error de análisis jamás falla el request (I7).
- **stateVersion 1→2 con backward compat (P5/I6):** `persistedState` gana `Contexts` (`omitempty`, nil-safe para v1); binario nuevo lee snapshot v1 sin error; binario viejo rechaza v2 con el check existente (persist.go:243); round-trip preserva contexts con history.
- **Config `context.analysis` aditiva (P6):** `enabled=false` default (sin records ni memoria; endpoint 200 con ceros/vacío), `history_per_session=50` (0 = sin history, solo agregados), valida `< 0`; INDEPENDIENTE de `telemetry`. Wiring pre-tráfico `srv.SetContextAnalysis(...)` + `m.SetContextHistoryPerSession(...)` (patrón SetContextMargin/SetMaxSessionsRetained, main.go:217-218).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes (1 reportado desestimado con motivo escrito — nil-slices falso positivo: `make(..., 0, len(...))` nunca emite `null`, `[]` garantizado), 5 opcionales aceptados como deuda documentada (#16-#20). Suite completa 294 tests `-race` verde, vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Operativo:** la feature cierra el ciclo (merge). Habilitar `context.analysis.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo).

### Deuda técnica detectada

- **TECHDEBT #16** — redundancia `SetContextHistoryPerSession` en main.go:218 (defensa en profundidad; limpiar en refactor futuro).
- **TECHDEBT #17** — casing PascalCase de `PartType` a nivel entry del endpoint (vs snake_case del summary; consumidores propios no dependen).
- **TECHDEBT #18** — `latest: null` vs "ausente" con `history_per_session=0` (cosmética; representación JSON válida, e2e test la espera).
- **TECHDEBT #19** — ring buffer con prepend O(N) en context.go:171 (FYI de hardening; default 50 → despreciable; deque circular O(1) futuro).
- **TECHDEBT #20** — gofmt preexistente en `e2e_009000_test.go` (preexistente en HEAD, fuera del diff de esta feature; correr gofmt cuando se toque el archivo).
- **Lección de proceso (test-audit §2/§9.1):** AUDIT previo al RED no ejecutado como sesión formal → `TestPersistVersionReject` falló en GREEN porque el bump P5 cambia un literal de fixture (`"version":1` → `"version":2`) y el replace era no-op. Corregido en sesión B (6e28730) preservando la intención. Lección: el AUDIT de una feature con bump de schema debe escanear literales de fixture que el spec cambia, no solo call-sites de firma.
- (menor, test-audit §9.3) acceso a campo interno en `TestPersistContextV2_BackwardCompatV1` (persist_context_test.go:39-44) — alternativa observable (`ContextSnapshot(client, "")`) recomendada para hardening futuro.

### Próxima feature en cola

- **009-002-sticky-session** (pendiente del epic mofgw-009): afinidad de provider por sesión para maximizar cache hit. Consume la composición de 009-001 y la fuente de sesión confirmada (`X-Session-Id` de opencode; client_id para openclaw/zot). Coordinar vía `cdad-epic` (chat nuevo) — el orquestador sugiere volver al epic al cerrar esta feature.

### 09 Ago 2026
- **Feature 009-000-request-telemetry DESPLEGADA en producción:** telemetría de requests activa (`telemetry.enabled: true, sample_rate: 1`), archivo `/home/<user>/logs/mofgw-telemetry.jsonl` (0640). Ciclo CDAD completo: spec → audit → RED → GREEN → review (APPROVE qwen3.7-plus, 0 bloqueantes). Suite 265 tests -race verde.
- **Hallazgo de infraestructura (causa raíz artifacts vacíos):** `fallback.timeout: 120s` cortaba streams largos de sub-agentes (el timeout aplica a todo el intento, no solo TTFB — discrepancia contrato-vs-impl). Fix en prod: 120s→300s. Documentado en `docs/hallazgos/2026-08-09-timeout-streams-largos.md`. Fix de diseño (TTFB-only vs stream_timeout) pendiente en backlog.
- **Descubrimiento en curso (24-48h):** telemetría capturando tráfico real. Primeros datos confirman la investigación — opencode manda X-Session-Id en header; el runtime OpenAI/JS (zot/openclaw) NO manda sesión ni en headers ni en metadata del body.

### 08 Ago 2026
- **Cierre formal del replanteo de eficiencia:** P4 CERRADO — Memory Bank aprobada por Pablo, `.cdad-state.json` marcado epic-closed (006/007/008 done). Validación externa CERRADA (7 APPROVE + 2 REQUEST CHANGES corregidos).
- **EPIC-009 planificado y aprobado:** telemetría de tráfico (009-000) → composición de contexto (009-001) + sticky routing por sesión (009-002). Fase 1 = telemetría: loguear headers/metadata del body del tráfico real para descubrir dónde viven los ids de sesión (investigación confirmó: solo opencode manda X-Session-Id; openclaw y zot no). Destino: telemetry.jsonl dedicado.

### 07 Ago 2026
- **Memory Bank creada** (cdad-scribe): `docs/projectbrief.md` + `docs/activeContext.md` — Etapa 5 del epic cycle completada.
- **GAP 1 resuelto:** `docs/specs/external-reviews/` ya existe con 9 `.resp.json` (evidencia de validación externa). El reporte del orchestrator era incorrecto.

### 06 Ago 2026
- **Replanteo de eficiencia completo:** EPIC-003/006/007/008 — 9 features, todas spec→audit→RED→GREEN→review.
- **SEC-001 security hardening:** 4 fixes de auditoría externa (SSE saneado, X-Agent-Id truncado+TTL, ReadHeaderTimeout, log 0640). Review con qwen3.7-plus (familia distinta).
- **Persistencia de accounting:** SaveState/LoadState JSON atómico (commit f90c0b7).
- **Validación externa:** 7 APPROVE + 2 REQUEST CHANGES. Ambos Majors corregidos (commit 082d10c).
- **Pricing/metadata reales** cableados en config de producción.

### 05 Ago 2026
- **EPIC-001 MVP completo** (commit e426f15d, 27 files, 3808 ins).
- **EPIC-002 completo** (commit 5966c5f4, 133 tests PASS).
- **EPIC-004/005 implementados y desplegados.**
- **Hotfix 001-001-endpoint-fix-content-array:** mofgw devolvía 400 a OpenClaw (content array vs string). Fix: `Messages` → `[]json.RawMessage`.
- **Feature 005-005-verbose** implementada (flags --verbose/--log-file, privacidad testeada).
- **Feature 001-003-fallback-v2** implementada (max_retries como tope de intentos totales, cooldown entre requests).

### 04 Ago 2026
- **Plan de epics aprobado** por Pablo (sesión Telegram) — con auth in-scope del MVP.
- **Todas las specs escritas** (EPIC-001 7/7, EPIC-002 4/4, EPIC-005 4/4).

### 03 Ago 2026
- **Decisiones fundacionales** (Pablo): nombre mofgw, transparencia total, puerto 3369, deploy systemd user, config paths.

## Próximos pasos

1. **EPIC-009 CERRADO** (3/3 features done + integración E3 + closure E4). Operativo opcional pendiente: habilitar `context.analysis.enabled` + `fallback.sticky_routing.enabled` en config de prod (default off).
2. **Criterios de aceptación del programa** (ver plan.md §criterios): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes, /metrics con tokens/costo, /v1/models capabilities, rechazo temprano por ventana.
3. **Deuda técnica** (ver `docs/TECHDEBT.md`): 24 entradas registradas (#16-24 = epic 009 + decisiones), varias cerradas, resto BAJA/FUTURO.
4. **Budget para cliente zot/OpenClaw** (decisión de Pablo pendiente — evaluado y diferido 08 Ago: el gasto es intencional, no se limita).

## Conocimiento operativo clave

- **Config de producción:** `~/.config/mofgw/config.yaml` — providers reales, pricing Zen, context.margin 0.1.
- **State file:** `~/.config/mofgw/state.json` (0600) — contadores de accounting, persistidos cada 10min.
- **Logs:** `/home/<user>/clawd/projects/mofgw/logs/` (permisos 0640).
- **Service:** `systemctl --user status mofgw` — active + enabled.
- **RAM:** ~12MB en operación normal.
- **Cache hit rate:** 94-99% medido en /metrics.

## Archivos de referencia

| Archivo | Qué contiene |
|---------|-------------|
| `docs/projectbrief.md` | Identidad, decisiones irreversibles, epics |
| `docs/activeContext.md` | Este archivo — estado actual, decisiones recientes |
| `docs/epics/plan.md` | Plan completo de epics con decomposición |
| `docs/research-architecture.md` | Arquitectura recomendada (ReverseProxy + cooldown RAM) |
| `docs/research-token-efficiency.md` | Precios verificados, cache providers, thinking capabilities |
| `docs/research.md` | Competidores y decisiones iniciales |
| `docs/USER-GUIDE.md` | Guía de usuario (instalación, config, endpoints) |
| `docs/TECHDEBT.md` | Deuda técnica consolidada (24 entradas) |
| `docs/specs/` | Specs de cada feature (27 directorios) |
| `docs/specs/external-reviews/` | Evidencia de validación externa (9 .resp.json) |
| `docs/specs/CROSS-SPEC-REVIEW.md` | Revisión de consistencia entre specs EPIC-001 |
| `task_plan.md` | Plan de tareas con estado de cada feature |
| `.cdad-state.json` | Estado del ciclo CDAD |
