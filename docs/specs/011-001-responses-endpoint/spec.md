# Spec — 011-001-responses-endpoint: endpoint /v1/responses (tronco del epic Odoo)

---
feature_id: 011-001-responses-endpoint
feature_name: responses-endpoint
epic: 011-mofgw-odoo
status: draft
approved_by: pendiente
created_at: 2026-08-12
updated_at: 2026-08-12
depends_on: 010-001 (catalogo fiel), 006-001 (usage accounting), 008-001 (rechazo por ventana), 001-004 (clamp), 001-002 (config) — todos done
paralelizable: no
---

## Descripción funcional

Implementar `POST /v1/responses` en mofgw: el endpoint del **Responses API de
OpenAI** que Odoo 19 enterprise usa para hablar con el proveedor de IA
(`_request_llm_openai_helper`). mofgw traduce el body Responses que manda Odoo
al formato chat-completions que hablan los providers upstream, y traduce la
respuesta de vuelta a `output[]`. Es el tronco del epic 011: las features 002
(structured output), 003 (tool calling), 004 (file attachments) y 005 (web
search) reemplazan, en etapas posteriores, los rechazos explícitos de esta
feature por traducción real.

Modo **solo no-stream** (D1): Odoo 19 usa request NO-stream en
`_request_llm_openai_helper` (el body no incluye `stream`). El streaming SSE
del Responses API queda fuera de esta feature (gap/deuda del epic).

La ruta se registra junto a `/v1/chat/completions` en `Handler()` (proxy.go:282)
y queda protegida por `s.auth.Wrap` (D7). El handler `s.handleResponses` es
paralelo a `handleChat` (proxy.go:407): lee el body crudo con límite, parsea un
struct propio de Responses (el body usa `input`, no `messages`), traduce
`input`→`messages`, delega en la MISMA pipeline de ruteo que habla
chat-completions, y traduce la salida a `output[]`.

## Contrato

### Firma

```
POST /v1/responses            (protegido por s.auth.Wrap, clientID vía auth.ClientIDFrom)
```

**Input (body crudo, tipo Responses API real de Odoo):**
```json
{
  "model": "<modelo>",
  "input": [
    {"role": "user", "content": [{"type": "input_text", "text": "<texto>"}]}
  ],
  "store": false,
  "temperature": 0.7
}
```
Nota: Odoo NO manda `max_output_tokens` (no-op). `store` siempre viene como
`false`; en 001 se ignora (D8, stateless).

**Output (no-stream):**
```json
{
  "id": "<stable-id>",
  "object": "response",
  "model": "<modelo del cliente>",
  "output": [
    {
      "type": "message",
      "id": "<stable-id>",
      "role": "assistant",
      "status": "completed",
      "content": [{"type": "output_text", "text": "<texto>"}]
    }
  ]
}
```

### Postcondiciones

**Request / autenticación**

1. **P1 — Ruta protegida y clientID sin scoping nuevo:** la ruta
   `POST /v1/responses` está registrada en el mux bajo `s.auth.Wrap`. Un request
   sin Bearer válido responde **HTTP 401** con envelope de error OpenAI-compatible.
   El `clientID` se obtiene con `auth.ClientIDFrom`; no se introduce ningún
   scoping/cabecera de autorización nuevo.

2. **P2 — Body inválido → HTTP 400:** un body que no es JSON válido, o que no
   tiene `input` usable para traducir a `messages`, responde **HTTP 400** con
   envelope `{"error":{"type":...,"message":...,"code":...}}`. Mismo contrato de
   error que `/v1/chat/completions`.

**Traducción de entrada (Responses → chat-completions)**

3. **P3 — `input` traducido a `messages`:** para cada item de `input[]` con
   `role` y `content` de parts `input_text`, el mensaje chat-completions resultante
   tiene el mismo `role` y `content` = string concatenado de los `text` de las
   parts `input_text` (en orden). El `model` y el `temperature` (si viene) se
   pasan tal cual. Items `input` de role `user`/`assistant`/`system` se preservan.

4. **P4 — Pipeline compartida y protecciones aplicadas:** el request traducido
   pasa por la MISMA pipeline que `/v1/chat/completions`: ruteo de provider,
   cost accounting, usage accounting, rate limiter, clamp y context-window check.
   Observable: (a) un request que excede la ventana de contexto responde igual que
   en chat; (b) un request sobre el límite de rate responde **HTTP 429**; (c) los
   contadores de usage y costos se incrementan para el `clientID`.

5. **P5 — Modelo forzado por cliente:** el `model` que Odoo ve en la respuesta
   es el modelo del cliente (principio "el cliente nunca elige"), forzado con
   `rewriteResponseModel` (proxy.go:1335, reescribe `model` a nivel raíz — formato
   Responses compatible). El modelo que ve Odoo == modelo que resolvió el router
   para el cliente.

**Rechazo explícito de features futuras (D1, D3)**

6. **P6 — `stream:true` → HTTP 400 streaming:** si el body Responses tiene
   `stream:true`, responde **HTTP 400** con
   `{"error":{"type":"...","message":"streaming not supported yet","code":...}}`.
   No se traduce ni se procesa el request. Full Responses streaming = deuda del
   epic (fuera de alcance).

7. **P7 — `tools` / `web_search_preview` → HTTP 400 tool calling:** si el body
   contiene `tools` (incluido un tool `{"type":"web_search_preview"}`), responde
   **HTTP 400** con `{"error":{"type":"...","message":"tool calling not yet
   supported","code":...}}`. Feature 003/005 reemplaza este rechazo.

8. **P8 — `text.format.json_schema` → HTTP 400 structured output:** si el body
   contiene `text.format.json_schema`, responde **HTTP 400** con
   `{"error":{"type":"...","message":"structured output not yet
   supported","code":...}}`. Feature 002 reemplaza este rechazo.

9. **P9 — parts `input_file`/`input_image` → HTTP 400 file attachments:** si
   alguna part de `input[]` tiene `type` `input_file` o `input_image`, responde
   **HTTP 400** con `{"error":{"type":"...","message":"file attachments not yet
   supported","code":...}}`. Feature 004 reemplaza este rechazo.

**Traducción de salida (chat-completions → Responses)**

10. **P10 — chat sin tool calls → un solo item `message`:** si la respuesta
    upstream no tiene `tool_calls`, el `output[]` contiene EXACTAMENTE un item
    `{"type":"message","id":"<stable>","role":"assistant","status":"completed",
    "content":[{"type":"output_text","text":"<texto>"}]}` donde `<texto>` ==
    `choices[0].message.content`. Este shape es parseable por el snippet real de
    Odoo (`_request_llm_openai_helper`: lee `line["content"][i]["text"]`).
    El `model` del envelope es el modelo del cliente (P5).

11. **P11 — IDs estables:** el `id` del response y el `id` del item `message`
    se generan de forma estable/determinística (mismo input → mismo id, no
    aleatorio por request). Esto habilita que Odoo reutilice el id de item como
    `call_id` en el ciclo tool-calling de 003.

**Cache y store**

12. **P12 — Cache separada por endpoint:** las respuestas cacheadas para
    `/v1/responses` usan claves de cache distintas de `/v1/chat/completions`.
    Observable: tras cachear un request en `/responses`, un request equivalente a
    `/chat/completions` NO se sirve desde esa cache (re-ejecuta), y viceversa.
    Nunca se sirve una chat-response como responses-response ni a la inversa.

13. **P13 — `store` ignorado (stateless):** el request Responses se procesa igual
    con `store:false`, `store:true`, o sin campo `store` (ausente). El valor de
    `store` no cambia el comportamiento de respuesta. 001 es stateless; la
    persistencia de `store:true` queda documentada como fuera de alcance.

**Envelope de error (D5)**

14. **P14 — Envelope de error OpenAI-compatible:** todos los errores responden
    `{"error":{"type","message","code"}}` con el status HTTP correcto: **400**
    (body inválido / feature no soportada / streaming), **401** (sin auth),
    **429** (rate limit), **502** (upstream). Mismo contrato que
    `/v1/chat/completions`.

## Invariantes verificables

- **I1 — Router y providers intactos:** la traducción vive en la capa del
  endpoint `/responses`, no en el router ni en los providers (contrato
  cross-feature plan-011:42). `provider.ParseChatRequest` y el ruteo de providers
  no cambian.
- **I2 — `arguments` como string JSON válido:** cualquier item `function_call`
  emitido en el futuro (base 003) debe tener `arguments` como string JSON
  parseable por `json.loads` — no un objeto. (En 001 no se emiten `function_call`
  porque `tools` se rechaza en P7; el invariante queda para el contrato de 003.)
- **I3 — Zero llamadas salientes a OpenAI:** el flujo `/v1/responses` rutea a los
  providers configurados de mofgw; nunca a `api.openai.com`.
- **I4 — Envelope de error consistente con chat:** el shape `{"error":{...}}` y
  los códigos de status son los mismos que ya expone `/v1/chat/completions`.
- **I5 — Suite completa verde con `-race`, cero red externa.**
- **I6 — Autenticación sin regresión:** `auth.Wrap` sigue protegiendo `/v1/*`;
  agregar la ruta no debilita la cobertura de auth existente.

## Criterios de aceptación

- **C1:** `POST /v1/responses` sin Bearer → **HTTP 401** con `error.type` no vacío
  (P1).
- **C2:** body `not-json` → **HTTP 400** con envelope `{"error":{type,message,code}}`
  (P2, P14).
- **C3:** body Responses válido con `input:[{role:"user",content:[
  {type:"input_text",text:"hola"}]}]` → el request upstream hacia el provider se
  construye con `messages:[{role:"user",content:"hola"}]` (P3). La respuesta
  upstream `choices[0].message.content=="hola mundo"` → body responses con
  `output:[{"type":"message","id":"<stable>","role":"assistant","status":
  "completed","content":[{"type":"output_text","text":"hola mundo"}]}]`, parseable
  por el snippet real de Odoo (P10, P11).
- **C4:** request que excede la ventana de contexto → misma respuesta de rechazo
  que `/v1/chat/completions`; request sobre el límite de rate → **HTTP 429**;
  contadores de usage/costo del clientID se incrementan (P4).
- **C5:** el `model` en el envelope de la respuesta == modelo del cliente, aunque
  el body pida otro (P5).
- **C6:** body con `stream:true` → **HTTP 400**, `error.message == "streaming not
  supported yet"` (P6).
- **C7:** body con `tools` o `web_search_preview` → **HTTP 400**, `error.message ==
  "tool calling not yet supported"` (P7).
- **C8:** body con `text.format.json_schema` → **HTTP 400**, `error.message ==
  "structured output not yet supported"` (P8).
- **C9:** body con part `input_file` o `input_image` → **HTTP 400**, `error.message
  == "file attachments not yet supported"` (P9).
- **C10:** tras cachear un request en `/responses`, un request equivalente a
  `/chat/completions` no se sirve desde esa cache (P12).
- **C11:** mismos requests con `store` ausente/`false`/`true` → mismo output (P13).
- **C12:** Suite completa verde: `go test ./... -race`, `go vet`, gofmt limpios,
  cero red externa (I5).

### Mapeo de pruebas de contrato (Etapa 3)

Cada postcondición se verifica contra el snippet real de Odoo
(`~/src/odoo/19/enterprise/ai/utils/llm_api_service.py:364-397`): el test que
produce un `output[]` para P10 debe poder pasarse por `_request_llm_openai_helper`
y devolver `response == ["hola mundo"]` con `to_call == []`.

| Postcondición | Verificación observable                                           |
| ------------- | ----------------------------------------------------------------- |
| P1            | request sin Bearer → 401                                          |
| P2            | body no-JSON → 400 error envelope                                 |
| P3            | upstream recibe `messages` traducidos de `input`                      |
| P4            | context-window/429/usage idénticos a chat                         |
| P5            | model del envelope == modelo del cliente                          |
| P6            | `stream:true` → 400 "streaming not supported yet"                   |
| P7            | `tools`/`web_search_preview` → 400 "tool calling not yet supported"   |
| P8            | `json_schema` → 400 "structured output not yet supported"           |
| P9            | `input_file`/`input_image` → 400 "file attachments not yet supported" |
| P10           | `output[]` con item message parseable por Odoo                      |
| P11           | mismo input → mismo id de response/item                           |
| P12           | cache de /responses no sirve a /chat/completions y viceversa      |
| P13           | store ausente/false/true → mismo output                           |
| P14           | todos los errores con envelope `{"error":{...}}` y status correcto  |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/proxy.go`: `Handler()` (proxy.go:280-290, registrar la ruta junto a
  chat/completions), `handleChat` (proxy.go:407, patrón de handler a replicar),
  `rewriteResponseModel` (proxy.go:1335, función libre que reescribe `model` a nivel
  raíz — compatible con Responses).
- `internal/provider/provider.go`: `ParseChatRequest` (provider.go:55) — exige
  `messages`, NO reusable para Responses (usa `input`); se traduce antes de llamarlo.
- `internal/auth/auth.go`: `ClientIDFrom` (auth.go:35), `auth.Wrap` (proxy.go:289).
- `internal/stream/stream.go`: `RewriteModel` (stream.go:92) — **no aplica** en 001
  (solo no-stream; streaming es 400).

**Hooks/extensión disponibles:**
- `auth.Wrap(mux, "/healthz", "/metrics")` — la ruta `/v1/*` queda protegida.
- `rewriteResponseModel` — forzado de modelo a nivel raíz (P5).
- Pipeline compartida de chat (routing, usage, costos, limiter, clamp, context
  window) — reutilizada (P4).

**Convenciones aplicables:**
- Envelope de error OpenAI-compatible `{"error":{"type","message","code"}}` — igual
  que `/v1/chat/completions` (D5).
- El body del request Responses viaja crudo por la cadena (pattern mofgw de
  transparencia); la traducción vive en la capa del endpoint (plan-011:42).

**Verificaciones pendientes:**
- VERIFICAR: valores exactos de `type`/`code` en los envelopes de error de las
  features rechazadas (P6-P9) — se fijan en tests de contrato.
- VERIFICAR: mecanismo de cache existente (keys por endpoint) — el test P12 confirma
  la separación empíricamente.

## Notas de implementación (orientación, no vinculante)

- `Handler()`: `mux.HandleFunc("POST /v1/responses", s.handleResponses)` junto a
  proxy.go:282, bajo `s.auth.Wrap`.
- `handleResponses`: leer body crudo con límite → parsear struct Responses propio
  (`model`, `input[]`, `store`, `temperature`) → rechazar según P6-P9 → traducir
  `input`→`messages` → delegar en la pipeline de chat → traducir
  `choices[0].message.content` a item `message` → forzar `model` con
  `rewriteResponseModel` → emitir `output[]`.
- `store` se ignora (P13); `max_output_tokens` no-op (Odoo no lo manda).
- No emitir items `message` cuando la respuesta tenga `tool_calls` (el output
  `function_call` es base de 003; en 001 no se produce porque `tools` se rechaza
  en P7).

## Out of scope

- **Streaming SSE** del Responses API (`stream:true` → 400; deuda del epic).
- **Tool calling real** (`function_call` output / traducción de `tools`) — feature
  003; en 001 `tools` → 400.
- **Structured output** (`json_schema`) — feature 002; en 001 → 400.
- **File attachments** (`input_file`/`input_image`) — feature 004; en 001 → 400.
- **Web search** (`web_search_preview`) — feature 005; en 001 → 400.
- **`/v1/audio/*` y `/v1/realtime/*`** — deuda del epic, no se tocan (D9).
- **Persistencia de `store:true`** — 001 es stateless; `store` se ignora y documenta (D8).
- Cambios en router/providers/`ParseChatRequest` (I1).

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Brainstorm D1-D9 cerrado por Ofap
  (delegado HITL). Verificación: snippet Odoo `_request_llm_openai_helper` lee
  `content[].text` del item message (línea 396) — el shape P10 usa `output_text`
  con campo `text`.

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
