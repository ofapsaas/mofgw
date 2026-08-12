# Spec — 011-005-web-search: grounded search server-side (web_search_preview → DDG → inyección → item message)

---
feature_id: 011-005-web-search
feature_name: web-search
epic: 011-mofgw-odoo
status: draft
approved_by: pendiente
created_at: 2026-08-12
depends_on: 011-003-tool-calling
---

## Descripción funcional

La feature 011-003 dejó la tool `web_search_preview` como rechazo explícito (D3 de
003: cualquier tool de `type != "function"` → HTTP 400 "tool calling not yet
supported", `responses.go:227-230`). Esta feature **reemplaza ese rechazo por la
RESOLUCIÓN real**: cuando Odoo manda `{"type":"web_search_preview"}` en `tools`,
mofgw **emula un grounded search server-side** — detectar → buscar en DDG →
inyectar los resultados como contexto → quitar la tool del upstream → devolver
la respuesta.

**Conclusión verificada (por qué mofgw emula en vez de exponer el ciclo):** Odoo
NO maneja el ciclo `web_search_call` (0 matches de `web_search_call` /
`web_search_call_output` / `search_query` en `llm_api_service.py`). Odoo solo lee
el campo `text` del item `message`. Por eso mofgw hace el grounded search por su
cuenta (server-side) y el contrato observable para Odoo es un item `message` con
`content[].text` — el mismo contrato de P10 de 001 / P7 de 003.

**Decisiones cerradas (D1-D5):**

- **D1 — mofgw emula grounded search:** detectar `web_search_preview` → ejecutar
  DDG → inyectar resultados como system message prependido → **QUITAR**
  `web_search_preview` de los `tools` del upstream (los providers
  chat-completions no la entienden). Devolver item `message` normal (P10 de 001 /
  P7 de 003). Sin ciclo `web_search_call`.
- **D2 — query = último mensaje user:** la query es la concatenación de los
  `text` de las parts `input_text` del **último** item `message` con
  `role == "user"` del `input[]` (misma regla de concatenación que P5 de 001).
  Determinista y cacheable. Descartada la alternativa de 2 round-trips (query
  derivada por el modelo).
- **D3 — DDG propio en Go, sin dependencias:** scrape
  `html.duckduckgo.com/html/?q=<query>` → top-N `{title,url,snippet}`, con
  fallback a la Instant Answer API (`api.duckduckgo.com/?q=<query>&format=json`).
  Sin librerías de terceros.
- **D4 — inyección como system message:** mensaje `role:"system"` prependido con
  `[Web search results for "<query>"]` + items numerados
  `title`/`url`/`snippet`, N = `max_results` (configurable, default 3). **NO** se
  emiten anotaciones `url_citation` (Odoo solo lee text; decorativo).
- **D5 — config `web_search.enabled: false` default:** `false` → `web_search_preview`
  conserva el 400 de D3 de 003 (sin regresión). `true` → flujo web search.
  **Fallo de DDG = best-effort sin grounding** (no 400, proseguir sin contexto
  inyectado).

**Efecto neto:** con `enabled:false` (default) el comportamiento de 003/004 queda
intacto (400 conservado). Con `enabled:true`, un request con `web_search_preview`
produce un request upstream con un system message grounded + los tools function
restantes traducidos (003 P1) y `web_search_preview` removido; la respuesta es un
item `message` normal.

## Contrato

### Firma

```
POST /v1/responses            (protegido por s.auth.Wrap — no cambia vs 001)
```

**Input (body Responses de Odoo con web search — llm_api_service.py:335-343):**
```json
{
  "model": "<modelo>",
  "input": [
    {"role": "user", "content": [{"type": "input_text", "text": "¿cuál es el clima en París?"}]}
  ],
  "temperature": 0.2,
  "tools": [
    {"type": "web_search_preview"},
    {"type": "function", "name": "get_weather", "description": "Obtiene el clima",
     "parameters": {...}}
  ]
}
```

**Body upstream chat-completions resultante (con `web_search.enabled:true`, D1/D4):**
```json
{
  "model": "<modelo>",
  "messages": [
    {"role": "system", "content": "[Web search results for \"¿cuál es el clima en París?\"]\n\n1. <title> | <url> | <snippet>\n2. <title> | <url> | <snippet>\n3. <title> | <url> | <snippet>"},
    {"role": "user", "content": "¿cuál es el clima en París?"}
  ],
  "temperature": 0.2,
  "tools": [{"type": "function", "function": {...}}]
}
```
Nota: `web_search_preview` fue **removido** de `tools` (D1). El system message se
prepende ANTES del primer mensaje del `input[]`.

**Output (envelope `output[]`, idéntico a P10 de 001 / P7 de 003):**
```json
{
  "id": "resp_<stable>",
  "object": "response",
  "output": [
    {"type": "message", "id": "resp_<stable>", "role": "assistant",
     "status": "completed",
     "content": [{"type": "output_text", "text": "<texto grounded>"}]}
  ]
}
```

### Postcondiciones

**Gate de activación**

1. **P1 — gating por `web_search.enabled` (D5):** si `tools` contiene algún tool
   con `type == "web_search_preview"`:
   - con `web_search.enabled == false` (default) → responde **HTTP 400**
     `{"error":{"type":...,"message":"tool calling not yet supported","code":...}}`
     (conserva D3 de 003 — sin regresión);
   - con `web_search.enabled == true` → **NO responde 400**: activa el flujo web
     search (P2-P6). Un tool `type != "function"` que NO sea `web_search_preview`
     (p.ej. `code_interpreter`) sigue cayendo en el 400 de D3 de 003 en ambos
     modos.

**Derivación de la query**

2. **P2 — query = último mensaje user (D2):** con `web_search.enabled == true` y
   presencia de `web_search_preview`, la query es la concatenación de los `text`
   de las parts `input_text` del **último** item de `input[]` con `role == "user"`
   (en orden de aparición). Determinista: mismo `input[]` → misma query. Si no
   existe ningún item message con `role == "user"`, no hay query → el flujo web
   search se **saltea** (best-effort, P7) y se prosigue sin grounding.

**Ejecución DDG**

3. **P3 — DDG sin dependencias (D3):** la query se ejecuta contra
   `html.duckduckgo.com/html/?q=<query>`; el resultado se parsea a top-N
   `{title,url,snippet}` con N = `web_search.max_results` (default 3). Si el
   scrape HTML falla, se usa el fallback a la Instant Answer API
   (`api.duckduckgo.com/?q=<query>&format=json`). Ambas llamadas best-effort con
   timeout `web_search.timeout` (si se configura).

**Inyección y remoción**

4. **P4 — inyección como system message prependido (D4):** si se obtuvieron
   resultados (P3), el primer mensaje del wire upstream es un
   `{"role":"system","content":<text>}` donde `<text>` empieza con
   `[Web search results for "<query>"]` y contiene exactamente **N** items
   numerados (1..N), cada uno con `title`, `url` y `snippet` (N = `max_results`,
   ≤ los top-N de P3). **NO** se emiten anotaciones `url_citation`.

5. **P5 — `web_search_preview` removido del upstream (D1):** el wire upstream
   **NO** contiene ningún tool de `type == "web_search_preview"`. Los tools
   `type == "function"` restantes se traducen exactamente como P1 de 003 (anidados
   bajo `function`, `parameters` byte-idénticos). Si no queda ningún tool
   function, el campo `tools` no se emite en el wire upstream.

**Salida**

6. **P6 — respuesta como item `message` (P10 de 001 / P7 de 003 intactos):** la
   respuesta al cliente es el envelope `output[]` con EXACTAMENTE un item
   `message` (`content[0].text == choices[0].message.content`). Con `enabled:true`
   y `web_search_preview`, el provider responde al prompt grounded y el output NO
   contiene items `function_call` por la tool de web search (fue removida, P5).
   El `id` estable (P11 de 001), el forzado de modelo (P5 de 001) y el resto del
   envelope no cambian.

**Fallo / edge cases**

7. **P7 — fallo de DDG = best-effort (D5):** si DDG falla (error de red, timeout,
   5xx, parse vacío) o no hay query (P2), mofgw **prosigue sin grounding**: no
   inyecta system message, remueve `web_search_preview` de todos modos (P5), y el
   provider responde con el contexto que tenga. **Nunca** se traduce un fallo de
   DDG en un 4xx/5xx al cliente. Con `web_search_preview` presente + fallo de DDG,
   la respuesta es igualmente un item `message` (P6).

8. **P8 — pipeline intacta, cache excluida para web search:** el request pasa por
   la MISMA pipeline de 001/002/003: limiter (global/client/agent), clamp, ventana
   de contexto, budget y ruteo (P9 de 003). **Excepción de corrección:** un request
   que activa el flujo web search (enabled:true + `web_search_preview`) **nunca se
   sirve desde el response cache** ni se almacena en él, aunque `temperature == 0`
   (los resultados de DDG cambian en el tiempo; servir del cache daría resultados
   obsoletos). El resto de los rechazos de 001/002/003 intactos (P10 de 003).

## Invariantes verificables

- **I1 — Cambio solo en la capa del endpoint `/responses`:** toda la lógica
  (detección, derivación de query, DDG, inyección, remoción) vive en
  `internal/proxy/responses.go` (y un nuevo cliente DDG inyectado por el Server).
  Router, providers y `ParseChatRequest` intactos (ADR-004).
- **I2 — Best-effort, sin errores nuevos:** el flujo web search jamás introduce
  un error al cliente que no existiera antes; un fallo de DDG degrada a ungrounded
  (P7), nunca a 4xx/5xx.
- **I3 — Query determinista:** mismo `input[]` → misma query (P2); permite
  reproducibilidad y consistencia entre requests idénticos.
- **I4 — Zero llamadas salientes a OpenAI:** el flujo rutea solo a los providers
  configurados de mofgw + DDG (I5 de 003 intacto).
- **I5 — Sin `url_citation`:** el output/item nunca lleva anotaciones
  `url_citation` (D4).
- **I6 — Suite completa verde con `-race`, cero red externa:** DDG se mockea en
  tests; ninguna prueba toca red real.

## Criterios de aceptación

- **CA-1 (P1):** `enabled:false` (default) + tools con `{"type":"web_search_preview"}`
  → **HTTP 400** "tool calling not yet supported" (sin regresión vs 003/004).
- **CA-2 (P1/P5):** `enabled:true` + tools con `{"type":"web_search_preview"}` +
  último mensaje user "clima en París" → el wire upstream **no** contiene
  `web_search_preview` en `tools`; no responde 400.
- **CA-3 (P2/P4):** el wire upstream tiene un system message prependido que
  empieza con `[Web search results for "clima en París"]` y contiene **exactamente
  `max_results`** items numerados con `title`/`url`/`snippet`.
- **CA-4 (P5):** tools function conviviendo con `web_search_preview` se traducen
  como 003 P1 (anidados, `parameters` byte-idénticos) mientras `web_search_preview`
  se remueve; si no quedan function tools, `tools` no se emite.
- **CA-5 (P6):** con mock upstream respondiendo texto → `output[]` con un solo
  item `message`, `content[0].text == choices[0].message.content`; sin items
  `function_call` por la web search.
- **CA-6 (P7):** DDG mock falla (5xx/timeout) → no 400, no system message
  inyectado, `web_search_preview` igualmente removido, respuesta item `message`
  ungrounded (best-effort).
- **CA-7 (P7):** `enabled:true` + `web_search_preview` pero sin item message user
  en `input[]` → sin grounding (no se inyecta system message), sin 400.
- **CA-8 (P8):** mismo body (temperature==0) con DDG mock result A → resultado A;
  se cambia el mock a result B y se repite el mismo body → devuelve B (nunca el
  cacheado A). El request web search nunca se sirve ni almacena en cache.
- **CA-9 (P8):** `enabled:false` conserva los rechazos de 001/002/003 (stream,
  tools no-function no-web-search, json_schema malformado, body inválido, 401).
- **CA-10 (I6):** suite completa verde: `go test ./... -race`, `go vet`, gofmt
  limpios, cero red externa.

### Mapeo de pruebas de contrato (Etapa 3)

| Postcondición | Verificación observable                                                                               |
| ------------- | ----------------------------------------------------------------------------------------------------- |
| P1            | enabled:false + web_search_preview → 400; enabled:true → no 400; tool no-function-no-web-search → 400 |
| P2            | query == concatenación del último mensaje user; sin user message → skip                               |
| P3            | DDG mock devuelve top-N {title,url,snippet}; fallback Instant Answer                                  |
| P4            | system message prependido con header + N items; sin url_citation                                      |
| P5            | web_search_preview removido del wire tools; function tools traducidos (003)                           |
| P6            | output[] con un solo item message; sin function_call por web search                                   |
| P7            | fallo DDG / sin query → ungrounded, sin 400                                                           |
| P8            | pipeline intacta; web search bypassa el response cache (no stale)                                     |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/responses.go`: la rama de rechazo D3 de 003 (líneas 227-230,
  `if t.Type != "function" { openAIError(400, "tool calling not yet supported"); return }`)
  → punto donde hoy se corta el flujo y donde se decide: si `web_search.enabled`
  es false → conservar el 400 (P1); si true y el tool es `web_search_preview` →
  marcar que activa el flujo web search (no 400). La inyección del system message
  (P4) ocurre al construir `messages` (rama default del switch por item.Type,
  líneas ~302-325) y la remoción (P5) al armar `chatTools` (líneas 213-241).
- Nuevo cliente DDG (p.ej. `internal/websearch`): scrape HTML + fallback Instant
  Answer API (D3), configurable con `max_results` y `timeout`. Se inyecta en
  `Server` (via `New` o setter) — sin tocar router/providers (I1).
- `internal/config/config.go`: agregar `WebSearch WebSearchConfig yaml:"web_search"`
  al struct `Config` (patrón `ContextConfig`/`TelemetryConfig`).
- `config.example.yaml`: agregar el bloque `web_search:`.
- `internal/proxy/responses.go` pipeline: limiter/cache/clamp/ventana/budget/ruteo
  intactos (P8). Única excepción: exclusión del response cache para requests web
  search (P8, I6).

**Hooks/extensión disponibles:**
- `responsesRequestBody.Tools` (`[]json.RawMessage`, línea 40) — inspección de
  cada tool para detectar `web_search_preview` sin re-marshal.
- El loop de tools (líneas 214-241) — punto donde hoy se rechaza y donde se decide
  traducir function vs activar web search vs rechazar no-function-no-web-search.
- La construcción de `messages` (líneas 269-327) — punto donde se prepende el
  system message grounded (P4).
- El switch por `item.Type` (líneas 276-326) — permite derivar la query del último
  item message user (P2).
- `config` + `Server` — punto de wiring del cliente DDG y del flag
  `web_search.enabled`.

**Convenciones aplicables:**
- Envelope de error OpenAI-compatible `{"error":{type,message,code}}` (P1, igual
  que chat, 001, 002, 003).
- Mapeo estructural de tools function → anidados (003 P1) intacto; solo se agrega
  la remoción de `web_search_preview` (P5).
- Patrón `enabled:false` default opt-in, mismo que `context.analysis`,
  `telemetry`, `response_cache`, `sticky_routing` (config.example.yaml).
- Best-effort sin errores nuevos (I2): mismo espíritu que D5 de 002.
- Traducción solo en la capa del endpoint (ADR-004, I1).

**Verificaciones pendientes:**
- VERIFICAR: formato exacto del parse del HTML de `html.duckduckgo.com/html/?q=`
  (selector de resultados) y del shape de la Instant Answer API — se resuelve con
  el provider real; el contrato observable (P3/P4: top-N {title,url,snippet}) es
  estable e independiente del parser. Verificación empírica con DDG real queda
  como **desviación no bloqueante** (tests usan mock, I6).
- VERIFICAR: comportamiento con `web_search_preview` + `parallel_tool_calls:true`
  y múltiples tools function junto a la web search — el contrato de esta feature
  especifica la remoción de `web_search_preview` y la traducción de function tools
  (P5); no se exige un orden particular de los tools function resultantes.

## Notas de implementación (orientación, no vinculante)

- En la rama que hoy rechaza (líneas 227-230), reemplazar el 400 incondicional
  por: (a) si el tool es `web_search_preview` y `s.webSearch.Enabled` → marcar la
  activación del flujo (no 400, no agregar a `chatTools`); (b) si el tool es
  `type != "function"` y no es `web_search_preview` → conservar el 400 "tool
  calling not yet supported" (P1); (c) si es `function` → traducir como 003 P1
  (P5).
- Derivar la query (P2) recorriendo `input[]` en orden y tomando el último item
  message con `role == "user"`; concatenar los `text` de sus parts `input_text`.
  Si no existe → saltar web search (P7).
- Ejecutar DDG (P3) con el cliente nuevo; ante cualquier fallo → `nil` resultados
  → sin inyección (P7). Con resultados → construir el system message (P4) con
  `max_results` items y prependerlo al slice `messages` antes del `Marshal`.
- Remover `web_search_preview` de `chatTools` (P5); si `chatTools` queda vacío no
  emitir el campo `tools`.
- Excluir del response cache: al detectar web search activo, no consultar ni
  almacenar en `responseCache` (P8, I6) aunque `temperature == 0`.
- No tocar la traducción de salida (P6), la rama tools function (003), ni el
  router/providers (I1).

## Out of scope

- **Ciclo `web_search_call`:** no se implementa; Odoo no lo maneja (conclusión
  verificada). mofgw emula el grounded search server-side (D1).
- **Anotaciones `url_citation`:** no se emiten (D4, I5); Odoo solo lee text.
- **Verificación empírica del soporte de DDG real:** desviación no bloqueante
  (tests usan mock, I6).
- **Cliente de búsqueda alternativo / APIs de terceros (Brave, SerpAPI, etc.):**
  solo DDG propio sin dependencias (D3).
- **Persistencia de resultados / cache de búsqueda:** out of scope (P8 solo
  excluye el cache de responses).
- **Streaming SSE:** `stream:true` → 400 (P8, P6 de 001 intacto).
- **Audio / realtime:** deuda del epic, no se tocan.
- **Router, providers, `ParseChatRequest`:** intactos (I1, ADR-004).

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Brainstorm D1-D5 cerrado por el
  dueño delegado. Reemplaza el rechazo D3 de 003 (`responses.go:227-230`) por
  grounded search server-side (DDG → system message → remoción de la tool → item
  message). Conclusión clave verificada: Odoo no maneja el ciclo `web_search_call`,
  solo lee `text` del item message. Default `enabled:false` sin regresión.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
