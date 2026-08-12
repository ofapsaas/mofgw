# Spec — 011-004-file-attachments: traducción de file attachments de Odoo (input_file / input_image → parts chat)

---
feature_id: 011-004-file-attachments
feature_name: file-attachments
epic: 011-mofgw-odoo
status: draft
approved_by: pendiente
created_at: 2026-08-12
depends_on: 011-001-responses-endpoint
---

## Descripción funcional

La feature 011-001 dejó las parts `input_file` / `input_image` de `input[]` como
rechazo explícito (P9: HTTP 400 "file attachments not yet supported"). Esta
feature **reemplaza esa rama de rechazo por la traducción real** de las parts de
archivo del `input[]` Responses (el body que Odoo manda en
`_request_llm_openai_helper`) al formato chat-completions que hablan los
providers upstream. Todo el cambio vive en `internal/proxy/responses.go`
(ADR-004/I1).

**Decisiones estructurales (D1-D3):**

- **D1 — cambio estructural POR MENSAJE (content-string vs content-array):**
  un mensaje del `input[]` cuyo content tiene **solo** parts `input_text` se
  traduce a `content` = **string** (concatenación de los `text`, como en
  001/003 — sin regresión en los caminos de texto plano). Un mensaje cuyo
  content tiene **al menos una** part `input_image` o `input_file` se traduce a
  `content` = **array** de parts chat, con un item por part del input, en orden
  de aparición. La decisión es **por mensaje** (por item de `input[]`), no por
  request: en un mismo `input[]`, algunos mensajes pueden ser string y otros
  array.
- **D2 — mapeo de parts:** `input_text` → `{"type":"text","text":T}`;
  `input_image` → `{"type":"image_url","image_url":{"url":U,"detail":D?}}`
  (detail passthrough, omitempty si ausente); `input_file` → `{"type":"file",
  "file":{"file_data":D,"filename":F}}` (part estilo OpenAI, sin detail). Las
  data-URI (`data:...;base64,...`) viajan como string, sin re-marshal.
- **D3 — sin gating por modality / PDF honesto:** mofgw traduce y reenvía; NO
  conserva rechazo 400 local, NO filtra por modality, NO convierte PDF. El
  `modality` es declarativo (solo catálogo `/v1/models`); el router elige por
  `req.Model`. Si el modelo elegido no soporta vision/archivos, el 4xx upstream
  se propaga **no-reintentable** (patrón D5 de 002). Es la traducción honesta
  de un proxy transparente (I1); la verificación empírica del soporte PDF del
  provider real queda como **desviación no bloqueante**.

**Compatibilidad con 003:** el content-array se aplica SOLO a items `message`
(role + content). Los items `function_call` / `function_call_output` del ciclo
tool-calling NO se tocan (el switch por `Type` ya los discrimina; P3/P4 de 003
intactos). El caso mixto (mismo `input[]` con message con imagen + function_call
+ function_call_output) se resuelve por la decisión por mensaje (D1): cada item
se traduce independientemente, en orden.

**Salida:** sin cambios. El modelo responde texto; el `output[]` con item
`message` (P10 de 001 / P7 de 003) y la rama `tool_calls` → `function_call`
(P6 de 003) quedan intactos.

## Contrato

### Firma

```
POST /v1/responses            (protegido por s.auth.Wrap — no cambia vs 001)
```

**Input (body Responses de Odoo, mensaje con imagen — llm_api_service.py:283-304):**
```json
{
  "model": "<modelo>",
  "input": [
    {"role": "user", "content": [
      {"type": "input_text", "text": "¿qué dice esta imagen?"},
      {"type": "input_image", "image_url": "data:image/png;base64,<...>", "detail": "low"},
      {"type": "input_file", "filename": "doc.pdf", "file_data": "data:application/pdf;base64,<...>"}
    ]}
  ],
  "temperature": 0.7
}
```

**Body upstream chat-completions resultante (lo que ve el provider):**
```json
{
  "model": "<modelo>",
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "¿qué dice esta imagen?"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,<...>", "detail": "low"}},
      {"type": "file", "file": {"file_data": "data:application/pdf;base64,<...>", "filename": "doc.pdf"}}
    ]}
  ],
  "temperature": 0.7
}
```

### Postcondiciones

**Traducción de entrada: parts de archivo (Responses → chat)**

1. **P1 — `input_image` → part `image_url` (D2):** para una part de un item
   message con `type == "input_image"`, el content-array chat contiene un item
   `{"type":"image_url","image_url":{"url":<image_url>,"detail":<detail>}}`.
   `url` = el campo `image_url` de la part (string data-URI, sin re-marshal).
   `detail`: si la part trae `detail` se pasa tal cual; si está ausente, el
   campo `detail` **no se emite** (omitempty, misma semántica que `strict` de
   002/003).

2. **P2 — `input_file` → part `file` (D2):** para una part con
   `type == "input_file"`, el content-array chat contiene un item
   `{"type":"file","file":{"file_data":<file_data>,"filename":<filename>}}`
   (part estilo OpenAI). `file_data` y `filename` se copian tal cual (strings).
   **NO se rechaza localmente** (D3): el soporte lo decide el provider; si
   responde 4xx, se propaga no-reintentable (patrón D5 de 002). Sin `detail`.

**Decisión estructural y orden**

3. **P3 — decisión POR MENSAJE string-vs-array (D1):** para cada item de
   `input[]` con `role` + `content`:
   - si **todas** sus parts son `input_text` → `content` = **string**
     (concatenación de los `text`, en orden; exactamente como 001/003 — sin
     regresión);
   - si **al menos una** part es `input_image` o `input_file` → `content` =
     **array**, con un item por part del input, **en orden de aparición**,
     mapeando `input_text`→`{"type":"text","text":T}` (P-D1),
     `input_image`→P1, `input_file`→P2.
   El `role` del mensaje se preserva en ambos casos.

4. **P4 — items del ciclo tool-calling intactos (003):** los items de `input[]`
   con `type == "function_call"` (P3 de 003) y `type == "function_call_output"`
   (P4 de 003) se traducen EXACTAMENTE igual que 003; el content-array de P3
   solo aplica a items `message`. Un `input[]` que mezcla message (con
   image/file) + `function_call` + `function_call_output` se traduce por
   decisión por mensaje, sin tocar los items del ciclo.

**Passthrough y enrutado**

5. **P5 — resto del body sin cambios (003):** `model`, `temperature`, `tools`
   (P1 de 003), `parallel_tool_calls` (P1 de 003) y `response_format`
   (P1 de 002) viajan tal cual como en 003, coexistiendo con los content-array
   de P3.

6. **P6 — rechazo P9 de 001 ELIMINADO, sin gating por modality (D3):** la
   presencia de parts `input_image` / `input_file` YA NO produce el 400 "file
   attachments not yet supported". mofgw traduce y reenvía (transparencia I1).
   El `modality` es declarativo (solo catálogo `/v1/models`); el router elige
   por `req.Model`. Si el modelo no soporta vision/archivos, el error 4xx del
   provider se propaga **no-reintentable** (patrón D5 de 002). La verificación
   empírica del soporte PDF del provider real queda como desviación no
   bloqueante.

7. **P7 — rechazos conservados (no regresión):** los demás rechazos intactos:
   `stream:true` → 400 (P6 de 001), `tools` con `type != "function"` → 400 (P2
   de 003), `text.format` con `type != "json_schema"` → 400 (P4 de 002), body
   no-JSON o sin `model`/`input` → 400 (P2 de 001), sin Bearer → 401.

**Ventana de contexto y salida**

8. **P8 — ventana de contexto SIN cambio de código:** `estimatePromptTokens =
   len(body)/4` (misma heurística que chat). Las data-URI base64 de las parts
   inflan el `len(body)`, pero el chequeo de ventana descuenta automáticamente
   esos tokens sobreestimados; no se cambia la heurística ni el chequeo.

9. **P9 — salida sin cambios (001/003 intactos):** la traducción de salida no
   cambia. El modelo responde texto → `output[]` con item `message`
   (`content[0].text == choices[0].message.content`, P10 de 001 / P7 de 003).
   Respuesta con `tool_calls` → SOLO items `function_call` (P6 de 003, D1/D2 de
   003). El `id` estable (P11 de 001) y el forzado de modelo (P5 de 001) sin
   cambios.

## Invariantes verificables

- **I1 — Traducción solo en la capa del endpoint `/responses`:** todo el cambio
  vive en `internal/proxy/responses.go`. Router, providers y `ParseChatRequest`
  intactos (ADR-004). Sin gating por modality en el router (D3).
- **I2 — Data-URI sin re-marshal:** `image_url`/`file_data`/`filename` de las
  parts viajan como strings tal cual llegan del input (no se decodifican, no se
  re-encoden, no se altera el contenido). El `content` array preserva la
  **identidad** de las data-URI.
- **I3 — Orden de parts preservado:** el orden de los items del content-array
  (P3) == el orden de las parts del input del mensaje.
- **I4 — Zero llamadas salientes a OpenAI:** el flujo rutea solo a los
  providers configurados de mofgw.
- **I5 — Suite completa verde con `-race`, cero red externa.**

## Criterios de aceptación

- **CA-1:** message con part `input_image` → el wire upstream contiene un
  `content` array con item `{"type":"image_url","image_url":{"url":U}}` y
  `detail` presente/ausente según la part (P1, D2). Con `detail` ausente en la
  part, el campo no aparece en el wire (omitempty).
- **CA-2:** message con part `input_file` → el wire upstream contiene un
  `content` array con item `{"type":"file","file":{"file_data":D,"filename":F}}`
  (P2). **NO** responde 400 "file attachments not yet supported".
- **CA-3:** message con SOLO parts `input_text` → `content` sigue siendo string
  concatenado (sin regresión vs 001/003) (P3).
- **CA-4:** message con `input_text` + `input_image` + `input_file` → `content`
  array con 3 items en el MISMO orden de las parts del input (P3, I3).
- **CA-5:** `input[]` mixto con message-imagen + `function_call` +
  `function_call_output` → el wire upstream tiene los items del ciclo traducidos
  como 003 y el message como array, en orden de aparición; los items del ciclo
  no se ven afectados por el content-array (P4).
- **CA-6:** part `input_image`/`input_file` → ya NO 400; mock upstream que
  responde 4xx → se propaga el 4xx no-reintentable (P6, D3, D5 de 002).
- **CA-7:** rechazos conservados: `stream:true` → 400, tools no-function → 400,
  json_schema malformado → 400, sin Bearer → 401 (P7).
- **CA-8:** suite completa verde: `go test ./... -race`, `go vet`, gofmt
  limpios, cero red externa (I5).

### Mapeo de pruebas de contrato (Etapa 3)

| Postcondición | Verificación observable                                                                        |
| ------------- | ---------------------------------------------------------------------------------------------- |
| P1            | `input_image` → wire content array con `image_url.url` == data-URI; `detail` passthrough / omitempty |
| P2            | `input_file` → wire content array con `file.file_data`/`file.filename`; sin 400                      |
| P3            | solo `input_text` → content string; con image/file → content array en orden                      |
| P4            | items `function_call`/`function_call_output` intactos (003)                                        |
| P5            | tools/parallel_tool_calls/response_format passthrough como 003                                 |
| P6            | `input_image`/`input_file` → ya no 400; 4xx upstream propagado no-reintentable                     |
| P7            | rechazos P6/P2/P4 de 001/003/002 intactos                                                      |
| P8            | data-URI infla `len(body)`; ventana descuenta automáticamente (sin cambio de código)             |
| P9            | salida item message / function_call intacta (001/003)                                          |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/responses.go`: rama de rechazo P9 (líneas 268-273:
  `if p.Type == "input_file" || p.Type == "input_image" → 400`) a **reemplazar
  por la traducción**; la construcción del content por mensaje (rama default del
  switch por `Type`, líneas 267-281) donde hoy se arma el string concatenado.
- `responsesPart` (líneas 63-66, actual `{Type, Text}`) — **extender** para
  llevar `image_url`, `detail`, `filename`, `file_data` (necesarios para P1/P2).
- `responsesInputItem.Content` (`[]responsesPart`) — fuente de las parts a
  mapear por mensaje (P3).
- `responsesID`, `rewriteResponseModel`, pipeline compartida (`router.Complete`)
  — sin cambios (P9, P5, I1).

**Hooks/extensión disponibles:**
- El switch por `item.Type` (líneas 241-282) — ya discrimina `function_call` /
  `function_call_output` / message; el content-array solo toca la rama message.
- `responsesPart` — punto de extensión para los campos de las parts de archivo.
- La rama de rechazo P9 (líneas 268-273) — el punto exacto que hoy corta el
  flujo y que esta feature reemplaza por traducción.

**Convenciones aplicables:**
- Mapeo estructural: part Responses (`input_image`/`input_file`) → part
  chat-completions (`image_url`/`file`) según D2.
- `detail` passthrough con omitempty (ausente → no se emite), misma semántica
  que `strict` de 002/003.
- Envelope de error OpenAI-compatible `{"error":{type,message,code}}` (no cambia).
- El body del request Responses viaja crudo por la cadena; la traducción vive
  en la capa del endpoint (ADR-004).

**Verificaciones pendientes:**
- VERIFICAR: behavior de parts de tipo desconocido en un mensaje que activa
  content-array. Recomendación: mapear solo los tipos conocidos preservando el
  orden y dropear las desconocidas (consistente con el string-content actual que
  ignora lo que no es `input_text`). Si se requiere retener, se documenta como
  ajuste menor.
- VERIFICAR: soporte real de `file` (PDF) y `image_url` por los providers
  upstream — se propaga el 4xx no-reintentable (D5 de 002). Verificación
  empírica con provider real se documenta como **desviación no bloqueante**.

## Notas de implementación (orientación, no vinculante)

- Reemplazar la rama de rechazo P9 (líneas 268-273) y la construcción de content
  en la rama message (líneas 274-280) por la decisión por mensaje (D1/P3):
  recorrer `item.Content` una vez; si todas las parts son `input_text` →
  content string (camino actual intacto); si hay `input_image`/`input_file` →
  construir un `[]any` de parts chat con un item por part en orden (P3).
- Mapear cada part: `input_text` → `{"type":"text","text":Text}`;
  `input_image` → `{"type":"image_url","image_url":{"url":ImageURL,"detail":
  Detail?}}` (Detail como `*string` con omitempty); `input_file` →
  `{"type":"file","file":{"file_data":FileData,"filename":Filename}}` (P1/P2).
- Extender `responsesPart` con los campos `image_url`, `detail`, `filename`,
  `file_data`.
- NO tocar la traducción de salida (P9), la rama tools (P1 de 003) ni el switch
  de items del ciclo (P4).
- El chequeo de ventana de contexto NO se modifica (P8).

## Out of scope

- **Conversión / análisis de archivos:** mofgw no decodifica PDFs ni imágenes;
  las data-URI viajan tal cual (transparencia I1, D3).
- **Gating por modality:** el router no filtra por capacidad de visión/archivo
  del modelo; el 4xx del provider se propaga no-reintentable (D3, D5 de 002).
- **Verificación empírica del soporte de `file`/`image_url` por providers
  reales:** desviación no bloqueante (P6).
- **Salida multimodal:** el modelo responde texto; no se emiten images/archivos
  en `output[]` (P9).
- **`web_search_preview` / tools no-function:** rechazo 400 conservado (P7, D3
  de 003; la feature 005 lo resuelve).
- **Streaming SSE:** `stream:true` → 400 (P7, P6 de 001).
- **Audio / realtime:** deuda del epic, no se tocan.
- **Router, providers, `ParseChatRequest`:** intactos (I1, ADR-004).

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Brainstorm D1-D3 cerrado por
  Ofap (delegado HITL). Reemplaza la rama P9 de 011-001 por traducción real de
  `input_image`/`input_file` (content-array por mensaje, D1); PDF → part `file`
  estilo OpenAI con 4xx upstream propagado (D3); `detail` passthrough omitempty
  (D2). Verificado contra `llm_api_service.py:283-304` (formato de parts de
  Odoo). Sin cambios en la salida ni en el router.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
