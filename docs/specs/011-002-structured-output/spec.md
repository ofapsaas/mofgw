# Spec — 011-002-structured-output: traducción de `text.format.json_schema` a `response_format` (structured output)

---
feature_id: 011-002-structured-output
feature_name: structured-output
epic: 011-mofgw-odoo
status: draft
approved_by: pendiente
created_at: 2026-08-12
depends_on: 011-001-responses-endpoint
---

## Descripción funcional

La feature 011-001 dejó `text.format.json_schema` como rechazo explícito (P8:
HTTP 400 "structured output not yet supported"). Esta feature **reemplaza esa
rama de rechazo por la traducción real**: cuando Odoo manda
`text.format = {"type":"json_schema", ...}`, mofgw lo traduce a
`response_format` en el body chat-completions que envía al provider upstream.

**Alcance estricto:** solo toca la **entrada** de `/v1/responses`. La **salida
no cambia**: sigue siendo el shape P10 de 001 (`output[0]` con item `message`,
`content[0].text == choices[0].message.content`), porque el provider upstream
responde el JSON estructurado como `choices[0].message.content` normal, y el
contrato de Odoo lo lee igual. No hay traducción nueva de salida.

Sigue las decisiones del brainstorm D1-D5: mapeo estructural a
`{"type":"json_schema","json_schema":{...}}` (D1), `strict` pasado tal cual
(D2), **solo** `type == "json_schema"` se traduce (el resto de `type` o
`text.format` malformado mantiene el 400 de P8 como rama de rechazo) (D3),
`temperature` sin forzar a 0 (D4), y sin degradación a `json_object` si el
provider no soporta `response_format` — falla explícito con el 4xx upstream,
no-reintentable (D5).

## Contrato

### Firma

```
POST /v1/responses            (protegido por s.auth.Wrap — no cambia vs 001)
```

**Input (nuevo, con structured output — Odoo `llm_api_service.py:325-333`):**
```json
{
  "model": "<modelo>",
  "input": [{"role": "user", "content": [{"type": "input_text", "text": "<texto>"}]}],
  "temperature": 0.7,
  "text": {
    "format": {
      "type": "json_schema",
      "name": "json_schema",
      "schema": { "<json-schema>" },
      "strict": true
    }
  }
}
```

**Body upstream chat-completions resultante (lo que ve el provider):**
```json
{
  "model": "<modelo>",
  "messages": [{"role": "user", "content": "<texto>"}],
  "temperature": 0.7,
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "json_schema",
      "schema": { "<json-schema>" },
      "strict": true
    }
  }
}
```
Nota: el `name` y el `schema` se toman del `text.format` de Odoo tal cual
(D1); `strict` se pasa con el valor que mande Odoo y **no se emite el campo**
si está ausente (D2).

**Output:** idéntico al de 001 (P10): `output[]` con un solo item `message`,
`content[0].text == choices[0].message.content`. Sin cambios.

### Postcondiciones

**Traducción de entrada (`text.format` → `response_format`)**

1. **P1 — `text.format.type == "json_schema"` se traduce a `response_format`:** si
   `text.format.type` es `"json_schema"`, el body chat-completions que recibe el
   provider incluye `response_format == {"type":"json_schema","json_schema":{
   "name":<name>,"schema":<schema>,"strict":<strict>}}`. El `name` y el `schema`
   se toman byte-a-byte de los campos `name`/`schema` de `text.format` del input
   (D1). El `schema` en el wire resultante es **idéntico byte-a-byte** al `schema`
   del input.

2. **P2 — `strict` passthrough tal cual:** si `text.format.strict` está presente,
   el `json_schema.strict` del wire == el valor exacto del input (no se fuerza, no
   se reordena). Si `strict` está ausente en el input, el campo `strict` **no se
   emite** en el `json_schema` del wire (D2).

3. **P3 — `temperature` sin forzar a 0:** el `temperature` del body upstream es el
   que mandó Odoo (passthrough, igual que en 001 P3). No se fuerza a 0 por tener
   structured output (D4).

4. **P4 — Solo `json_schema` se traduce (resto → 400):** si `text.format` está
   presente pero `text.format.type != "json_schema"` (cualquier otro type), o
   `text.format` está malformado (no es un objeto con `type` string legible),
   responde **HTTP 400** con `{"error":{"type":...,"message":"structured output
   not yet supported","code":...}}`. Esta es la rama que conserva P8 de 001 como
   rechazo (D3).

**Salida y pipeline**

5. **P5 — Salida intacta (P10 de 001):** cuando hay structured output y el
   provider responde, el `output[]` del envelope es EXACTAMENTE un item `message`
   (`type:"message","role":"assistant","status":"completed","content":[
   {"type":"output_text","text":...}]`) con `content[0].text ==
   choices[0].message.content`. El JSON estructurado que devuelve el provider
   viaja como texto normal del `content` — sin parseo, sin re-empaquetado (la
   salida no cambia).

6. **P6 — Pipeline compartida sin regresión:** el request con `response_format`
   pasa por la MISMA pipeline que 001: limiter, cache exact-match (con key
   separada por endpoint, P12 de 001), clamp, context-window, budget y ruteo. El
   `response_format` viaja como campo passthrough en `chatBodyMap` y es preservado
   por el clamp y por la inyección de thinking.

**Manejo de error ante provider sin soporte**

7. **P7 — Fallo explícito sin degradación (D5):** si el provider upstream responde
   **4xx** ante `response_format` (no soporta structured output), mofgw **NO
   degrada** a `json_object` ni retiene el `response_format`; propaga el error 4xx
   del upstream tal cual, como error no-reintentable (misma semántica de
   `router.go:600-601`). El request no se reenruta con un fallback de menor
   capacidad.

**Rechazos que se conservan**

8. **P8 — Rechazos no-structured-output intactos:** los demás rechazos de 001 se
   conservan sin cambios: `stream:true` → 400 (P6 de 001), `tools` /
   `web_search_preview` → 400 (P7 de 001), parts `input_file`/`input_image` → 400
   (P9 de 001), body no-JSON / sin `input` → 400 (P2 de 001), sin Bearer → 401.

## Invariantes verificables

- **I1 — Traducción solo en la capa del endpoint `/responses`:** todo el cambio
  vive en `internal/proxy/responses.go`. El router, los providers y
  `ParseChatRequest` quedan intactos (ADR-004, 0 tests existentes modificados).
- **I2 — `response_format` es passthrough en `chatBodyMap`:** se inyecta en el map
  antes del `Marshal`; el clamp y la inyección de thinking lo preservan sin
  alterarlo.
- **I3 — Schema byte-identidad:** el `schema` del wire `response_format` es
  byte-a-byte igual al `schema` del input (sin re-marshal que reordene/reescale).
- **I4 — Salida sin cambios vs 001:** el shape del envelope de salida (P10) no
  cambia por la presencia de structured output.
- **I5 — Zero llamadas salientes a OpenAI:** el flujo rutea solo a los providers
  configurados de mofgw.
- **I6 — Suite completa verde con `-race`, cero red externa.**

## Criterios de aceptación

- **CA-1:** body Responses con `text.format.type == "json_schema"` y un `schema`
  dado → el wire upstream contiene `response_format == {"type":"json_schema",
  "json_schema":{"name":<name>,"schema":<schema>,"strict":<strict>}}` con el
  `schema` **idéntico byte-a-byte** al del input (P1, P2, I3).
- **CA-2:** con structured output y provider que responde `choices[0].message.
  content == "<json>"`, el `output[0].content[0].text` del envelope == ese
  `<json>` exacto (P5, I4).
- **CA-3:** `text.format.type != "json_schema"` (o `text.format` malformado) →
  **HTTP 400**, `error.message == "structured output not yet supported"` (P4,
  rama P8 conservada).
- **CA-4:** cache — misma `schema` + mismo body → HIT (upstream no re-llamado);
  `schema` distinta → MISS (upstream re-llamado) (P6, P12 de 001).
- **CA-5:** mock upstream que responde 400 ante `response_format` → mofgw propaga
  el 4xx (no degrada a `json_object`, no reintenta) (P7, D5).
- **CA-6:** suite completa verde: `go test ./... -race`, `go vet`, gofmt limpios,
  cero red externa (I6).

### Mapeo de pruebas de contrato (Etapa 3)

| Postcondición | Verificación observable                                                      |
| ------------- | ---------------------------------------------------------------------------- |
| P1            | wire upstream tiene `response_format` estructural correcto (D1)                |
| P2            | `strict` passthrough / ausente → no se emite                                   |
| P3            | `temperature` tal cual (no forzado a 0)                                        |
| P4            | `type != json_schema` o malformado → 400 "structured output not yet supported" |
| P5            | `output[0].content[0].text == choices[0].message.content`                      |
| P6            | `response_format` atraviesa limiter/clamp/cache sin perderse                   |
| P7            | mock upstream 400 ante `response_format` → mofgw propaga 4xx                   |
| P8            | rechazos P6/P7/P9/P2 de 001 intactos (no regresión)                          |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/responses.go`: rama de rechazo P8 a **reemplazar por la
  traducción** (líneas ~143-146: `if rb.Text != nil && rb.Text.Format != nil →
  400`), e inyección de `response_format` en `chatBodyMap` (líneas ~173-177,
  antes del `json.Marshal`). `rb.Text.Format` es `*json.RawMessage` (líneas
  41-43): el `schema`/`name`/`strict` se extraen por unmarshal de ese RawMessage
  o se mantienen crudos según el mapeo D1.
- `internal/proxy/proxy.go`: `router.go:600-601` — semántica de error
  no-reintentable ante 4xx upstream (referenciada por P7, no se toca).
- `internal/provider/provider.go`: `ParseChatRequest` — **intacto** (I1).
- Clamp (`internal/clamp`), inyección de thinking: preservan `response_format`
  como passthrough (I2).

**Hooks/extensión disponibles:**
- `rb.Text.Format` como `*json.RawMessage` — punto de extracción del mapeo D1.
- `chatBodyMap` (`map[string]any`) — inyección de `response_format` sin tocar la
  estructura de traducción `input`→`messages` de 001.
- La rama de rechazo P8 (líneas 143-146) — punto exacto donde hoy se corta el
  flujo y donde se decide traducir vs rechazar (D3).

**Convenciones aplicables:**
- Mapeo estructural D1: `text.format` (4 campos planos de Odoo) →
  `response_format = {"type":"json_schema","json_schema":{...}}`. NO se pasa
  plano.
- Envelope de error OpenAI-compatible `{"error":{type,message,code}}` — igual que
  chat y 001 (rama P4).
- El body del request Responses viaja crudo por la cadena; la traducción vive en
  la capa del endpoint (ADR-004).

**Verificaciones pendientes:**
- VERIFICAR: soporte real de `response_format` por provider — la propuesta no
  degrada a `json_object` (D5) y se espera el 4xx no-reintentable (P7); la
  verificación empírica con un provider real se documenta como **desviación no
  bloqueante** (no impide cerrar la feature).

## Notas de implementación (orientación, no vinculante)

- En la rama que hoy rechaza (líneas 143-146), parsear `rb.Text.Format`
  (RawMessage) como un struct con `type`/`name`/`schema`/`strict` (RawMessage).
  - Si `type == "json_schema"`: construir `response_format` y NO retornar 400.
  - Si `type` es otro, o el parse falla (malformado): mantener el 400 P8 (P4).
- Inyectar `response_format` en `chatBodyMap` antes del `Marshal` (línea ~177),
  usando el RawMessage del `schema` para preservar la identidad byte-a-byte (I3) —
  no re-marshalar el `schema` por un map genérico.
- `strict`: pasar el valor exacto; si ausente, omitir la clave (D2).
- No tocar la salida (P5): el JSON del provider llega como `content` normal.

## Out of scope

- **Cambios en la salida:** el shape P10 de 001 no se toca; el JSON estructurado
  viaja como texto plano del `content`.
- **Degradación a `json_object`** cuando el provider no soporta `response_format`
  (D5 — prohibido; se falla explícito).
- **Traducción de otros `text.format.type`** que no sean `json_schema` (D3 —
  siguen con 400).
- **Tool calling / file attachments / web search / streaming:** rechazos P7/P9/P6
  de 001 intactos (P8).
- **Router, providers, `ParseChatRequest`:** intactos (I1, ADR-004).
- **Forzar `temperature` a 0** con structured output (D4 — no se hace).

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Brainstorm D1-D6 cerrado por
  Ofap (delegado HITL). Reemplaza la rama P8 de 011-001 por traducción real; la
  salida sigue P10 de 001.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
