# Spec — 011-003-tool-calling: traducción del ciclo tool-calling de Odoo (tools ↔ function_call ↔ function_call_output)

---
feature_id: 011-003-tool-calling
feature_name: tool-calling
epic: 011-mofgw-odoo
status: draft
approved_by: pendiente
created_at: 2026-08-12
depends_on: 011-001-responses-endpoint
---

## Descripción funcional

La feature 011-001 dejó `tools` como rechazo explícito (P7: HTTP 400 "tool
calling not yet supported"). Esta feature **reemplaza esa rama de rechazo por
la traducción real del ciclo tool-calling de Odoo**, en sus tres direcciones:

1. **Entrada — definiciones de tools** (Odoo `_to_open_ai_tool_schema`,
   llm_api_service.py:652-670): `body["tools"]` (formato Responses function,
   plano: `{"type":"function","name","description","parameters","strict"}`)
   se traduce al formato chat-completions (anidado bajo `function`) en el body
   que ve el provider upstream.

2. **Salida — `function_call`** (Odoo `_request_llm_openai_helper`,
   llm_api_service.py:379-391): cuando el provider responde con
   `choices[0].message.tool_calls`, mofgw traduce cada uno a un item
   `function_call` del `output[]`. Odoo lee `name`, `call_id` y `arguments`
   (json.loads), NO ejecuta las tools por medio de mofgw: **Odoo las ejecuta
   por su cuenta** y arma el siguiente request.

3. **Entrada — `function_call_output`** (Odoo `_build_tool_call_response`,
   llm_api_service.py:672-683): Odoo devuelve el resultado de la tool como item
   `{"type":"function_call_output","call_id":...,"output":...}` en el `input`
   del siguiente request. mofgw lo traduce a un mensaje `role:"tool"` de
   chat-completions, cerrando el round-trip.

**Quién maneja el ciclo:** el loop de ejecución de tools lo maneja Odoo (manda
el input, recibe los `function_call`, los ejecuta, y re-manda con
`function_call_output`). mofgw **solo traduce**; no mantiene estado de sesión
entre requests (stateless, como 001). Cada request es una traducción puntual.

**Decisiones cerradas (D1-D3):**
- **D1 — mapeo `call_id`:** el `call_id` del item `function_call` de salida =
  **eco directo** del `tool_calls[i].id` upstream. El `id` estable del item
  (P11 de 001) va **aparte** (no == call_id). Solo así el round-trip funciona:
  Odoo devuelve `function_call_output.call_id` = ese mismo id, y mofgw lo
  mapea al `role:"tool"` con `tool_call_id` correcto.
- **D2 — item message cuando hay tool_calls:** con `tool_calls` no vacío se
  emiten SOLO items `function_call` (NO item `message`). Odoo ignora el texto
  del message en esa rama (`elif not has_tool_calls`,
  llm_api_service.py:392); un message vacío sería ruido sin consumidor.
- **D3 — `web_search_preview`:** si `tools` contiene CUALQUIER tool de
  `type != "function"` (incluido `web_search_preview`), responde HTTP 400
  "tool calling not yet supported" (conserva el rechazo; la feature 005 lo
  resuelve). 003 traduce solo los tools de `type == "function"`.

## Contrato

### Firma

```
POST /v1/responses            (protegido por s.auth.Wrap — no cambia vs 001)
```

**Input (body Responses de Odoo, ciclo con tools — llm_api_service.py:335-343):**
```json
{
  "model": "<modelo>",
  "input": [
    {"role": "user", "content": [{"type": "input_text", "text": "¿clima en París?"}]},
    {"type": "function_call",
     "id": "resp_<stable>",
     "call_id": "call_abc",
     "name": "get_weather",
     "arguments": "{\"location\":\"París\"}"},
    {"type": "function_call_output",
     "call_id": "call_abc",
     "output": "22°C soleado"}
  ],
  "temperature": 0.2,
  "tools": [
    {"type": "function", "name": "get_weather",
     "description": "Obtiene el clima", "strict": true,
     "parameters": {"type": "object", "properties": {"location": {"type": "string"}},
                    "required": ["location"], "additionalProperties": false}}
  ],
  "parallel_tool_calls": true
}
```
Nota: `tools` viene ya en formato OpenAI /responses (Odoo aplicó
`_to_open_ai_tool_schema` porque mofgw es su provider `openai`). Los items
`function_call` y `function_call_output` conviven con items `message` en el
mismo `input[]`.

**Body upstream chat-completions resultante (lo que ve el provider):**
```json
{
  "model": "<modelo>",
  "messages": [
    {"role": "user", "content": "¿clima en París?"},
    {"role": "assistant", "content": null,
     "tool_calls": [{"id": "call_abc", "type": "function",
                     "function": {"name": "get_weather", "arguments": "{\"location\":\"París\"}"}}]},
    {"role": "tool", "tool_call_id": "call_abc", "content": "22°C soleado"}
  ],
  "temperature": 0.2,
  "tools": [{"type": "function",
             "function": {"name": "get_weather", "description": "Obtiene el clima",
                          "parameters": {"type": "object", "properties": {"location": {"type": "string"}},
                                         "required": ["location"], "additionalProperties": false},
                          "strict": true}}],
  "parallel_tool_calls": true
}
```

**Output con tool calls (envelope `output[]`):**
```json
{
  "id": "resp_<stable>",
  "object": "response",
  "output": [
    {"type": "function_call",
     "id": "resp_<stable>",
     "call_id": "call_abc",
     "name": "get_weather",
     "arguments": "{\"location\":\"París\"}",
     "status": "completed"}
  ]
}
```

### Postcondiciones

**Traducción de entrada: definiciones de tools (Responses → chat)**

1. **P1 — `tools` function → `tools` chat anidado:** para cada tool del body
   Responses con `type == "function"`, el body upstream chat incluye `tools`
   con un elemento `{"type":"function","function":{"name":<name>,
   "description":<description>,"parameters":<parameters>,["strict":<strict>]}}`.
   `name`/`description` se copian tal cual; `parameters` es **idéntico
   byte-a-byte** al del input (invariante de byte-identidad, no re-marshal).
   `strict`: si viene en el tool del input se pasa tal cual; si está ausente no
   se emite (misma semántica omitempty que D2 de 002). El `parallel_tool_calls`
   del input, si viene, se pasa tal cual al body upstream.

2. **P2 — `web_search_preview` / `type != "function"` → 400 (D3):** si `tools`
   contiene algún tool de `type != "function"` (incluido `web_search_preview`),
   responde **HTTP 400** con `{"error":{"type":...,"message":"tool calling not
   yet supported","code":...}}`. Esta es la rama que conserva P7 de 001 como
   rechazo para los tools no-function (la feature 005 la reemplaza). El chequeo
   ocurre ANTES de cualquier traducción.

**Traducción de entrada: items del ciclo (Responses → chat)**

3. **P3 — item `function_call` en input → mensaje assistant con tool_calls:**
   para cada item de `input[]` con `type == "function_call"`, el mensaje chat
   resultante es `{"role":"assistant","content":null,"tool_calls":[{"id":<call_id>,
   "type":"function","function":{"name":<name>,"arguments":<arguments>}}]}`
   donde `<call_id>` = el campo `call_id` del item (o su `id` si no trae
   `call_id`), `<name>` = el campo `name`, `<arguments>` = el campo `arguments`
   (string JSON, sin re-marshal). El `id` del tool_call == el `call_id` del
   item input (asegura que el `role:"tool"` subsiguiente matchee).

4. **P4 — item `function_call_output` en input → mensaje tool:** para cada item
   de `input[]` con `type == "function_call_output"`, el mensaje chat resultante
   es `{"role":"tool","tool_call_id":<call_id>,"content":<output>}` donde
   `<call_id>` = el campo `call_id` del item y `<content>` = el campo `output`.
   Round-trip D1: el `call_id` que Odoo devuelve == el que mofgw emitió en el
   `function_call` de salida == el `id` upstream, de modo que el `tool_call_id`
   del mensaje `role:"tool"` matchea el `id` del tool_call del assistant (P3).

5. **P5 — items `message` sin regresión:** los items de `input[]` con
   `role`+`content` de parts `input_text` se traducen igual que 001 (P3):
   role preservado, content = concatenación de los text de las parts
   `input_text`, en orden. Conviven sin conflicto con los items P3/P4 del mismo
   `input[]`.

**Traducción de salida (chat → Responses)**

6. **P6 — respuesta con tool_calls → SOLO items `function_call` (D2):** si
   `choices[0].message.tool_calls` no está vacío, `output[]` contiene EXACTAMENTE
   un item `function_call` por `tool_calls[i]`, y NO contiene ningún item
   `message` (D2). Cada item:
   `{"type":"function_call","id":<stable>,"call_id":<echo tool_calls[i].id>,
   "name":<tool_calls[i].function.name>,"arguments":<tool_calls[i].function.arguments>,
   "status":"completed"}`. `<call_id>` = **eco directo** del `tool_calls[i].id`
   upstream (D1). `<arguments>` es string JSON parseable por `json.loads`
   (invariante I2 de 001). El shape es parseable por el snippet real de Odoo
   (`_request_llm_openai_helper` lee `name`/`arguments`/`call_id`,
   llm_api_service.py:380-389). El `model` del envelope es el modelo del cliente
   (P5 de 001).

7. **P7 — respuesta SIN tool_calls → item message (P10 de 001 intacto):** si
   `tool_calls` vacío o ausente, el `output[]` contiene EXACTAMENTE un item
   `message` (`type:"message","role":"assistant","status":"completed","content":
   [{"type":"output_text","text":...}]`) con `content[0].text ==
   choices[0].message.content`. Sin cambios vs 001 (el camino tool-calling no
   degrada el camino de texto plano).

8. **P8 — `id` estable del item `function_call` (P11 de 001):** el `id` de cada
   item `function_call` se genera con `responsesID(clientID, body)`
   (determinístico: mismo clientID + body → mismo id), y es **distinto** del
   `call_id` (que es eco no-determinístico del id upstream). Separar ambos
   campos es la base del round-trip D1: Odoo reutiliza `call_id` (no el `id`
   estable) para el `function_call_output`.

**Pipeline / invariantes de integración**

9. **P9 — Pipeline compartida sin regresión:** los requests con `tools`,
   items `function_call`/`function_call_output` pasan por la MISMA pipeline que
   001/002: limiter (global/client/agent), cache exact-match (key separada por
   endpoint, P12 de 001, elegible solo si temperature == 0), clamp, ventana de
   contexto, budget y ruteo. Los campos `tools` y `parallel_tool_calls` viajan
   como passthrough en `chatBodyMap` y sobreviven al clamp y a la inyección de
   thinking (I2 de 002) sin perderse.

10. **P10 — Rechazos conservados (no regresión):** los demás rechazos de
    001/002 intactos: `stream:true` → 400 (P6 de 001), `text.format` con
    `type != "json_schema"` → 400 (P4 de 002), parts `input_file`/`input_image`
    → 400 (P9 de 001), body no-JSON o sin `model`/`input` → 400 (P2 de 001),
    sin Bearer → 401. `text.format.json_schema` sigue traduciéndose a
    `response_format` (P1 de 002), compatible y coexistente con `tools`.

## Invariantes verificables

- **I1 — Traducción solo en la capa del endpoint `/responses`:** todo el cambio
  vive en `internal/proxy/responses.go`. Router, providers y `ParseChatRequest`
  intactos (ADR-004; 0 tests existentes modificados).
- **I2 — `arguments` como string JSON válido:** todo `function_call` emitido en
  salida (P6) tiene `arguments` parseable por `json.loads` — no un objeto.
- **I3 — Byte-identidad de `parameters` de tools:** el `parameters` del wire
  `tools` es byte-a-byte igual al del input (sin re-marshal que reordene).
- **I4 — `call_id` como eco del id upstream:** el `call_id` del item
  `function_call` de salida (P6) == `tool_calls[i].id`; y el `tool_call_id` del
  mensaje `role:"tool"` (P4) == el `call_id` del `function_call_output` de
  Odoo == ese mismo id. El round-trip es cerrado.
- **I5 — Zero llamadas salientes a OpenAI:** el flujo rutea solo a los
  providers configurados de mofgw.
- **I6 — Suite completa verde con `-race`, cero red externa.**

## Criterios de aceptación

- **CA-1:** body Responses con `tools:[{"type":"function",...}]` → el wire
  upstream contiene `tools:[{"type":"function","function":{...}}]` con
  `parameters` **idéntico byte-a-byte** al del input y `strict` passthrough /
  ausente → no se emite (P1, I3).
- **CA-2:** `tools` con un tool `{"type":"web_search_preview"}` (o cualquier
  `type != "function"`) → **HTTP 400**, `error.message == "tool calling not yet
  supported"` (P2, D3).
- **CA-3:** `input[]` con item `{"type":"function_call",...}` → el wire upstream
  tiene un mensaje `{"role":"assistant","content":null,"tool_calls":[{...}]}`
  con `id` == el `call_id` del item (P3).
- **CA-4:** `input[]` con item `{"type":"function_call_output",...}` → el wire
  upstream tiene un mensaje `{"role":"tool","tool_call_id":<call_id>,"content":
  <output>}` (P4, I4).
- **CA-5:** mock upstream que responde `choices[0].message.tool_calls` con N
  elementos → `output[]` con EXACTAMENTE N items `function_call` y CERO items
  `message`; cada item con `call_id` == eco del `id` upstream, `arguments`
  parseable (P6, D1, D2, I2). El shape pasa por el snippet real de Odoo y
  devuelve `to_call == [(name, call_id, arguments)]`.
- **CA-6:** mock upstream con `tool_calls` vacío → `output[]` con un solo item
  `message`, `content[0].text == choices[0].message.content` (P7).
- **CA-7:** mismo clientID + body → el `id` de los items `function_call` es el
  mismo (determinístico) y distinto del `call_id` (P8, D1).
- **CA-8:** los campos `tools`/`parallel_tool_calls` sobreviven clamp e
  inyección de thinking (P9); rechazos P6/P9 de 001 y P4 de 002 intactos (P10).
- **CA-9:** suite completa verde: `go test ./... -race`, `go vet`, gofmt
  limpios, cero red externa (I6).

### Mapeo de pruebas de contrato (Etapa 3)

Cada postcondición de traducción se verifica contra el snippet real de Odoo
(`~/src/odoo/19/enterprise/ai/utils/llm_api_service.py`): el `output[]` de P6
debe poder pasarse por `_request_llm_openai_helper` (líneas 379-391) y devolver
`to_call == [(name, call_id, arguments)]`; y el `function_call_output` que
produce `_build_tool_call_response` (líneas 672-683) debe mapearse al mensaje
`role:"tool"` de P4.

| Postcondición | Verificación observable                                                     |
| ------------- | --------------------------------------------------------------------------- |
| P1            | wire upstream tiene `tools` anidados, `parameters` byte-idénticos               |
| P2            | `type != function` → 400 "tool calling not yet supported"                     |
| P3            | item `function_call` → mensaje assistant con tool_calls, `id` == call_id        |
| P4            | item `function_call_output` → mensaje `role:"tool"` con `tool_call_id` correcto   |
| P5            | items message traducidos como 001 (no regresión)                            |
| P6            | N tool_calls → N items function_call, 0 items message, call_id eco upstream |
| P7            | sin tool_calls → un solo item message (P10 de 001)                          |
| P8            | id estable determinístico, distinto del call_id                             |
| P9            | tools/parallel_tool_calls sobreviven pipeline; cache separada por endpoint  |
| P10           | rechazos P6/P9 de 001 y P4 de 002 intactos                                  |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/responses.go`: rama de rechazo P7 (líneas 154-157:
  `if len(rb.Tools) > 0 → 400`) a **reemplazar por la traducción**; inyección de
  `tools` y `parallel_tool_calls` en `chatBodyMap` (líneas ~202-209, antes del
  `json.Marshal`); traducción de salida (líneas 344-364, rama `tool_calls`).
- `responsesRequestBody.Tools` (`[]json.RawMessage`, línea 40) — raw para
  preservar byte-identidad de `parameters` (I3). `responsesInputItem` (líneas
  46-49, actual `{role, content}`) — **extender a items de tipos mixtos**
  (`message`, `function_call`, `function_call_output`) con un struct
  discriminado por `type`.
- `responsesID` (línea 104) — id estable del item `function_call` (P8, D1).
- `responsesChatMessage` (líneas 59-62, actual `{role, content}`) — extender
  para emitir `tool_calls` (P3) y `role:"tool"` con `tool_call_id` (P4).
- `internal/proxy/proxy.go`: `router.Complete` (pipeline compartida, P9);
  `rewriteResponseModel` (forzado de modelo, P6).
- `internal/clamp`, inyección de thinking: preservan `tools`/
  `parallel_tool_calls` como passthrough (I2 de 002, P9).

**Hooks/extensión disponibles:**
- `rb.Tools` como `[]json.RawMessage` — extracción de cada tool sin re-marshal
  (byte-identidad I3).
- `chatBodyMap` (`map[string]any`) — inyección de `tools`/`parallel_tool_calls`
  sin tocar la traducción `input`→`messages` de 001.
- La rama de rechazo P7 (líneas 154-157) — punto exacto donde hoy se corta el
  flujo y donde se decide traducir vs rechazar (P1 vs P2).
- `responsesInputItem` — punto de extensión para items de tipos mixtos.

**Convenciones aplicables:**
- Mapeo estructural: tool Responses plano (`{"type":"function","name",...}`) →
  tool chat anidado (`{"type":"function","function":{...}}`). NO se pasa plano.
- `strict` passthrough con omitempty (ausente → no se emite), misma semántica
  que D2 de 002.
- Envelope de error OpenAI-compatible `{"error":{type,message,code}}` — igual
  que chat, 001 y 002 (rama P2).
- El body del request Responses viaja crudo por la cadena; la traducción vive
  en la capa del endpoint (ADR-004).

**Verificaciones pendientes:**
- VERIFICAR: agrupación de tool_calls paralelos (Odoo manda
  `parallel_tool_calls:true`). El contrato de esta feature especifica 1:1
  (un item `function_call` → un mensaje assistant con un tool_call), que cubre
  el caso de un solo tool (el común en Odoo). El agrupamiento de varios
  `function_call` adyacentes en un único mensaje assistant multi-tool_call es
  optimización de implementación — se documenta, no se exige en el contrato.
- VERIFICAR: aceptación de `strict` en tools por los providers upstream — la
  propuesta pasa `strict` tal cual; si un provider lo rechaza, se propaga el 4xx
  no-reintentable (patrón D5 de 002, sin degradación). Verificación empírica con
  provider real se documenta como **desviación no bloqueante**.

## Notas de implementación (orientación, no vinculante)

- En la rama que hoy rechaza (líneas 154-157), reemplazar el 400 por:
  (a) chequeo D3: si algún tool tiene `type != "function"` → conservar el 400
  "tool calling not yet supported" (P2);
  (b) si todos son `function`: construir `tools` chat anidado (P1) usando el
  RawMessage de `parameters` para preservar byte-identidad (I3), y NO retornar
  400.
- Extender `responsesInputItem` a un struct discriminado por `type`: items con
  `role` → P5 (como 001); items `function_call` → P3; items
  `function_call_output` → P4. Recorrer `input[]` una sola vez, emitiendo
  mensajes en orden de aparición.
- Traducción de salida: si `choices[0].message.tool_calls` no vacío, construir
  un item `function_call` por tool_call con `id` = `responsesID` (P8), `call_id`
  = `tool_calls[i].id` (D1), `name`/`arguments` del `function` — y NO emitir
  item `message` (D2). Si `tool_calls` vacío → camino P7 intacto.
- `parallel_tool_calls` e inyectar `tools` en `chatBodyMap` antes del
  `Marshal`; el clamp y la inyección de thinking los preservan (P9).
- El `arguments` del item de salida es el string JSON crudo del upstream
  (I2); no re-marshalar.

## Out of scope

- **`web_search_preview` y tools no-function:** rechazo 400 conservado (D3);
  la feature 005 los traduce.
- **Ejecución de tools por mofgw:** Odoo ejecuta las tools por su cuenta; mofgw
  solo traduce (stateless, sin estado de sesión entre requests).
- **Mantener estado de conversación entre requests del ciclo:** cada request es
  una traducción puntual; el `input[]` completo (message + function_call +
  function_call_output) llega en cada request.
- **Streaming SSE** (`stream:true` → 400, P6 de 001 intacto).
- **File attachments** (P9 de 001 intacto).
- **Agrupación de tool_calls paralelos como contrato** (VERIFICAR, optimización
  de implementación, ver Contexto técnico).
- **Cambios en la salida cuando no hay tool_calls** (P10 de 001 intacto, P7).
- **Router, providers, `ParseChatRequest`:** intactos (I1, ADR-004).

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Brainstorm D1-D3 cerrado por
  Ofap (delegado HITL). Reemplaza la rama P7 de 011-001 por traducción real
  bidireccional del ciclo tool-calling; verificado contra el snippet real de
  Odoo (`llm_api_service.py:335-343`, `364-397`, `652-670`, `672-683`).

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
