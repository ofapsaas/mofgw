# Spec — 011-006-embeddings: endpoint /v1/embeddings (forward a Ollama)

---
feature_id: 011-006-embeddings
feature_name: embeddings
epic: 011-mofgw-odoo
status: draft
approved_by: "Ofap (agent-delegated)"
created_at: 2026-08-12
updated_at: 2026-08-12
depends_on: 011-001-responses-endpoint
paralelizable: sí (con 011-001)
---

## Descripción funcional

Implementar `POST /v1/embeddings` en mofgw: el endpoint que Odoo 19 enterprise
usa para generar embeddings de un texto (`_request_llm_embedding` → `POST
/v1/embeddings`). mofgw actúa como gateway hacia Ollama: recibe el request de
Odoo, **fuerza el modelo de embeddings del cliente** (principio "el cliente
nunca elige" — mofgw manda el modelo configurado, ignora el `model` que manda
Odoo), lo forwardea a un Ollama local y devuelve el vector OpenAI-compatible.

Esta feature es la primera **llamada saliente** del proxy con un cliente HTTP
dedicado (patrón ya introducido por 011-005-web-search con `websearch.Client`,
ADR-005). El `provider.Client` NO es reusable para embeddings: `Complete`/`Stream`
hardcodean el path `/chat/completions` (provider.go:260). Se necesita un cliente
simple que haga `POST <baseURL>/embeddings`.

**Configuración (D3/D4):**
- `embeddings.base_url` **global** (un Ollama por instancia mofgw).
- `clients[].embeddings.model` **por cliente** (el modelo forzado).
- `embeddings.api_key_env` opcional (Ollama local sin key por default).

**Dimensiones (D1, hallazgo descubrimiento):** Ollama SÍ acepta `dimensions` y
trunca la salida si el modelo lo soporta. mofgw NO valida el `dimensions` de
Odoo (hardcodeado en 1536 en ai_embedding.py:32); lo deja pasar a Ollama. El
vector real es la dimensión **nativa** del modelo configurado (all-minilm = 384).
La alineación de dimensión entre mofgw y Odoo se hace por **config manual en
Odoo** (feature 011-007), no por validación en mofgw. Cambiar de modelo de
embeddings a una dimensión distinta ⇒ reindexar sources en Odoo (riesgo
operativo documentado en plan-011:66).

**Pricing (D5):** los embeddings pagan pricing. Se cuentan los tokens del input
(`prompt_tokens` del usage de Ollama) con el MISMO flujo de `recordCacheTokens`
(IncUsage + IncCost). Arranca en costo 0: el modelo de embeddings no tiene
entrada en `pricing:` → `estimateCost` devuelve 0. Si un operador agrega el
modelo a `pricing:`, los embeddings pasan a costar automáticamente (sin código
nuevo).

## Contrato

### Firma

```
POST /v1/embeddings            (protegido por s.auth.Wrap, clientID vía auth.ClientIDFrom)
```

**Input (body crudo, formato OpenAI-compatible que manda Odoo):**
```json
{
  "input": "<texto> | [\"<texto1>\", ...] | [[1,2,3], ...]",
  "model": "<modelo que Odoo cree usar — IGNORADO por mofgw>",
  "dimensions": 1536,
  "encoding_format": "float"
}
```

**Output (formato OpenAI-compatible que espera Odoo):**
```json
{
  "object": "list",
  "data": [
    {"object": "embedding", "index": 0, "embedding": [0.1, 0.2, ...]}
  ],
  "model": "<modelo forzado por cliente>",
  "usage": {"prompt_tokens": N, "total_tokens": N}
}
```

**Request upstream hacia Ollama (mofgw reescribe `model`):**
```json
{
  "model": "<modelo del cliente>",
  "input": "<texto>",
  "dimensions": 1536,
  "encoding_format": "float"
}
```

### Postcondiciones

**Request / autenticación**

1. **P1 — Ruta protegida y clientID sin scoping nuevo:** la ruta
   `POST /v1/embeddings` está registrada en el mux bajo `s.auth.Wrap`
   (`mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)` tras proxy.go:316).
   Un request sin Bearer válido responde **HTTP 401** con envelope de error
   OpenAI-compatible. El `clientID` se obtiene con `auth.ClientIDFrom`; no se
   introduce ningún scoping/cabecera de autorización nuevo.

2. **P2 — Body inválido → HTTP 400:** un body que no es JSON válido, o que no
   tiene `input` usable, responde **HTTP 400** con envelope
   `{"error":{"type":...,"message":...,"code":null}}` (mismo contrato que
   `/v1/chat/completions`, función `openAIError`).

**Modelo forzado por cliente (principio "el cliente nunca elige")**

3. **P3 — Modelo forzado por cliente, no el de Odoo:** el body que mofgw envía a
   Ollama tiene `model` == el modelo de embeddings configurado para el
   `clientID` (`clients[].embeddings.model`), y el `model` del envelope de
   respuesta == ese mismo modelo. El `model` que Odoo manda en el body se
   IGNORA por completo (no llega a Ollama ni aparece en la respuesta).

4. **P4 — Cliente sin `embeddings.model` configurado → HTTP 400 (D2):** si el
   `clientID` no tiene un `clients[].embeddings.model` configurado, el request
   responde **HTTP 400** (fail-fast) con `error.message == "no embeddings model
   configured for client"` (o equivalente descriptivo). No hay default global
   silencioso.

**Forward a Ollama**

5. **P5 — `input` pasado intacto, `dimensions` passthrough (D1):** el `input`
   del request upstream es byte-idéntico al `input` del request de Odoo
   (string, array de strings, o array de token-arrays). El `dimensions` de Odoo
   (si viene) se pasa tal cual a Ollama (que trunca si el modelo lo soporta);
   mofgw NO lo valida contra la dimensión nativa del modelo. `encoding_format`
   se pasa tal cual si viene.

6. **P6 — Respuesta OpenAI-compatible y passthrough:** mofgw devuelve el body de
   respuesta en el shape OpenAI de embeddings: `{"object":"list","data":[
   {"object":"embedding","index":i,"embedding":[...]}], "model":"<modelo
   cliente>", "usage":{"prompt_tokens":N,"total_tokens":N}}`. Los vectores
   (`data[].embedding`), el orden (`index`) y el conteo de items se preservan de
   la respuesta de Ollama. El `model` del envelope es el forzado por cliente (P3).

**Pricing y telemetría**

7. **P7 — Pricing por tokens del input (D5):** el flujo `/v1/embeddings` cuenta
   los tokens de entrada (`prompt_tokens` del usage de Ollama) con el MISMO
   mecanismo de `recordCacheTokens` (IncUsage + IncCost): los contadores de
   usage y costos del `clientID` se incrementan. Con el modelo de embeddings
   ausente de `pricing:` el costo contribuido es **0** (P4 de 006-002). Los
   headers `X-Usage-*` se emiten con `setUsageHeaders`.

8. **P8 — Budget por cliente aplicado (D5):** el request pasa por el chequeo de
   `budgetExceeded(clientID)` (008-002). Un request que excede el budget del
   cliente responde **HTTP 429** `rate_limit_exceeded`, igual que `/v1/chat/
   completions`.

**Errores upstream**

9. **P9 — Error upstream → HTTP 502:** un fallo de red, timeout o respuesta no-
   2xx de Ollama responde **HTTP 502** con envelope `{"error":{...}}` OpenAI-
    compatible, mismo contrato que `/v1/chat/completions`. Un 429 de Ollama no
    reentrable responde como fallo upstream (no se reenvía el request).

**Envelope de error (D5 de 001)**

10. **P10 — Envelope de error OpenAI-compatible:** todos los errores responden
    `{"error":{"type","message","code"}}` con el status HTTP correcto: **400**
    (body inválido / cliente sin modelo / timeout de concurrency), **401** (sin
    auth), **429** (rate limit / budget / concurrency), **502** (upstream).
    Mismo contrato que `/v1/chat/completions`.

## Invariantes verificables

- **I1 — Router y providers intactos:** el forward de embeddings vive en la capa
  del endpoint `/embeddings` (y su cliente HTTP dedicado); el router, los
  providers y `provider.ParseChatRequest` NO cambian (contrato cross-feature
  plan-011:42).
- **I2 — Cero llamadas salientes a OpenAI:** el flujo `/v1/embeddings` habla con
  el Ollama configurado en `embeddings.base_url`; nunca con `api.openai.com`.
- **I3 — Envelope de error consistente con chat:** el shape `{"error":{...}}` y
  los códigos de status son los mismos que ya expone `/v1/chat/completions`.
- **I4 — El vector real es la dimensión nativa del modelo (D1):** mofgw no
  trunca, no paddea y no re-validar dimensiones; entrega lo que Ollama devuelve.
  La alineación de dimensión es responsabilidad de la config manual en Odoo
  (011-007), no de mofgw.
- **I5 — Suite completa verde con `-race`, cero red externa.**
- **I6 — Autenticación sin regresión:** `auth.Wrap` sigue protegiendo `/v1/*`;
  agregar la ruta no debilita la cobertura de auth existente.
- **I7 — `provider.Client` no se toca:** su hardcodeo de `/chat/completions`
  permanece (provider.go:260); el cliente de embeddings es una pieza aparte.

## Criterios de aceptación

- **C1:** `POST /v1/embeddings` sin Bearer → **HTTP 401** con `error.type` no
  vacío (P1).
- **C2:** body `not-json` → **HTTP 400** con envelope `{"error":{...}}` (P2, P10).
- **C3:** request válido con un `model` "x" en el body de Odoo y el cliente con
  `embeddings.model == "m"` → el body upstream hacia Ollama tiene `model ==
  "m"`, y el envelope de respuesta tiene `model == "m"` (P3). Verificable
  observando el request que recibe el upstream mock.
- **C4:** cliente sin `embeddings.model` configurado → **HTTP 400** con
  `error.message` descriptivo "no embeddings model configured for client"
  (P4).
- **C5:** el upstream mock devuelve un vector de dimensión nativa (ej. 384) →
  el `data[0].embedding` de la respuesta mofgw == ese vector, sin truncar ni
  padear (P5, P6, I4). El `dimensions` que Odoo mande (ej. 1536) se reenvía a
  Ollama (P5).
- **C6:** el upstream mock reporta `usage.prompt_tokens == 10` → los contadores
  de usage del clientID se incrementan en 10; `X-Usage-Prompt-Tokens == 10`
  (P7). Sin entrada del modelo en `pricing:` → `X-Usage-Cost-USD == 0.000000`
  (P7, D5).
- **C7:** request de un cliente con budget excedido → **HTTP 429**
  `rate_limit_exceeded` (P8).
- **C8:** upstream mock devuelve 500 → mofgw responde **HTTP 502** con envelope
  `{"error":{...}}` (P9, P10).
- **C9:** `provider.Client.Complete` sigue hardcodeando `/chat/completions` (I7);
  el flujo de embeddings usa su propio cliente `/embeddings` (I1).
- **C10:** Suite completa verde: `go test ./... -race`, `go vet`, gofmt limpios,
  cero red externa (I5).

### Mapeo de pruebas de contrato (Etapa 3)

Cada postcondición se verifica contra el formato real que Odoo manda/espera en
`ai/embedding` (`~/src/odoo/19/enterprise/ai`): el request usa `input` +
`model` + `dimensions`; el response espera `data[].embedding`.

| Postcondición | Verificación observable                                                |
| ------------- | ---------------------------------------------------------------------- |
| P1            | request sin Bearer → 401                                               |
| P2            | body no-JSON → 400 error envelope                                      |
| P3            | upstream recibe `model` del cliente; envelope responde `model` del cliente |
| P4            | cliente sin `embeddings.model` → 400 "no embeddings model configured"    |
| P5            | `input` byte-idéntico; `dimensions` passthrough                            |
| P6            | shape OpenAI list/data/embedding/index preservado; `model` forzado       |
| P7            | `prompt_tokens` incrementan usage del clientID; costo 0 sin pricing      |
| P8            | budget excedido → 429 `rate_limit_exceeded`                              |
| P9            | upstream 500 → 502 error envelope                                      |
| P10           | todos los errores con envelope `{"error":{...}}` y status correcto       |

## Contexto técnico

**Modelos/entidades tocadas:**
- `internal/proxy/proxy.go`: `Handler()` (proxy.go:313-324, agregar
  `mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)` tras la línea 316,
  bajo `s.auth.Wrap`); `recordCacheTokens` (proxy.go:812), `estimateCost`
  (proxy.go:851), `budgetExceeded` (proxy.go:379), `setUsageHeaders`
  (proxy.go:782), `openAIError` (proxy.go:432) — todos reutilizables (P7/P8).
- `internal/config/config.go`: `Config` (agregar `Embeddings EmbeddingsConfig`
  a nivel raíz, config.go:215-238), `ClientConfig` (config.go:136, agregar
  campo `Embeddings *EmbeddingsConfig` anidado por cliente). `defaults()`
  (config.go:276) y la validación de clients (config.go:445-464) para validar el
  modelo por cliente.
- `internal/provider/provider.go`: `Client` (provider.go:225) — **NO reusable**
  (hardcodea `/chat/completions` en `Complete`, provider.go:260). Necesita
  cliente simple para `/embeddings` (POST `baseURL`+"/embeddings").
- `internal/auth/auth.go`: `ClientIDFrom` (auth.go:35), `auth.Wrap`.
- `cmd/mofgw/main.go`: wiring de la config (patrón `SetWebSearch` main.go:245-250,
  `SetBudget` main.go:207-213, `SetPricing` main.go:192-201).

**Hooks/extensión disponibles:**
- `auth.Wrap(mux, "/healthz", "/metrics")` — la ruta `/v1/*` queda protegida.
- Patrón de setter pre-tráfico (`SetWebSearch`/`SetBudget`/`SetPricing`): se
  agrega `SetEmbeddings(baseURL string, apiKey string)` global + setter/mapa
  `clientID→model` (patrón SetBudget, mapa inmutable post-set). `SetPricing`
  ya cubre el costo de embeddings si el modelo aparece en `pricing:` (D5).
- `recordCacheTokens` + `estimateCost` — pricing de embeddings reutilizado.

**Convenciones aplicables:**
- Envelope de error OpenAI-compatible `{"error":{"type","message","code"}}` —
  igual que `/v1/chat/completions` (P10).
- `provider.Client` no se toca (I7); cliente HTTP de embeddings como pieza
  aparte (patrón `websearch.Client` de 011-005, ADR-005: interfaz tipada, sin
  reflexión).
- Config con API keys solo por referencia a env (`api_key_env`, config.go:9).

**Verificaciones pendientes:**
- VERIFICAR: valores exactos de `type`/`code` en el envelope de error 400 del
  cliente sin modelo (P4) — se fijan en tests de contrato.
- VERIFICAR: el snippet exacto de `_request_llm_embedding` de Odoo 19 (path
  `ai/embedding`) para confirmar el shape de request/response que espera
  (campos `input` string-vs-array, lectura de `data[].embedding`) — test P6
  contra código real.
- VERIFICAR: semántica de timeout/backpressure para el cliente de embeddings
  (¿reutiliza el `http.Client` global o uno dedicado?) — decisión de
  implementación, no de contrato.

## Notas de implementación (orientación, no vinculante)

- `Handler()`: `mux.HandleFunc("POST /v1/embeddings", s.handleEmbeddings)` tras
  proxy.go:316, bajo `s.auth.Wrap`.
- `handleEmbeddings`: leer body crudo con límite → parsear struct propio
  (`input`, `model`, `dimensions`, `encoding_format`) → resolver modelo del
  cliente (P3/P4) → reescribir `model` → delegar al cliente HTTP `/embeddings`
  → parsear respuesta → forzar `model` en el envelope → `recordCacheTokens`
  (P7) → `setUsageHeaders` → emitir.
- Config: `EmbeddingsConfig{BaseURL string, APIKeyEnv string}` global +
  `clients[].embeddings.model` por cliente (D3). Setter pre-tráfico
  `SetEmbeddings` + mapa `clientID→model` (patrón SetBudget).
- Pricing: el modelo de embeddings ausente de `pricing:` → cost 0 por
  `estimateCost` (P7, D5). Agregarlo a `pricing:` hace que pague sin código
  nuevo.
- No reutilizar `provider.Client.Complete`/`Stream` (hardcodean
  `/chat/completions`); cliente dedicado `POST baseURL+"/embeddings"` (I7).

## Out of scope

- **Alineación/validación de dimensión** — la alineación del `dimensions` de
  Odoo con la dimensión real del vector es config **manual en Odoo** (feature
  011-007); mofgw NO valida ni transforma dimensiones (D1, I4).
- **Cambio de modelo por request** — una instancia Odoo queda fijada a un modelo
  de embeddings (el del cliente); la selección dinámica por request es deuda del
  epic (plan-011:21).
- **Auto-sync de dimensión entre mofgw y Odoo** — configuración a mano
  (plan-011:22).
- **Batch/retry del lado mofgw hacia Ollama** — solo forward directo (P9: fallo
  upstream → 502).
- Cambios en router/providers/`provider.Client` (I1, I7).
- **Audio** (`/v1/audio/*`) y **realtime** (`/v1/realtime/*`) — deuda del epic,
  no se tocan.

## Cambios
- 2026-08-12: draft inicial (architect, Etapa 2). Decisiones D1-D5 resueltas por
  el dueño delegado. Hallazgo descubrimiento verificado: Ollama acepta
  `dimensions` y trunca; all-minilm nativo = 384; alineación por config manual
  en Odoo (011-007).

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
