# Spec — 009-001-context-composition: composición estructural del contexto por sesión/cliente

---
feature_id: 009-001-context-composition
epic: mofgw-009-contexto-analisis
status: draft (sin aprobar — pendiente decisión de keying al cerrar la captura; ver §Decisiones pendientes)
created_at: 2026-08-09
updated_at: 2026-08-09
depends_on: 009-000-request-telemetry (paquete composition, gate de emisión, audit R1), 008-003-stats-por-sesion (X-Session-Id solo lectura, keying client|session, FIFO), 007-003-usage-para-clientes (patrón endpoint ?session= + aislamiento), 001-007-auth (clientID), 006-001-usage-accounting (Usage.PromptTokens post-response), 005-005-verbose (privacidad nunca contenido)
paralelizable: no
---

## Contexto

El 85% del costo de los requests es cache hit = releer contexto. Para optimizar
hay que VER la composición del contexto que los runtimes envían: qué roles
dominan (system vs user vs tool), qué part types (text/image/tool_result/
tool_use/thinking), qué tamaños por porción, y cómo evoluciona request a
request dentro de una sesión. Esta feature lo expone en un endpoint dedicado
`GET /v1/context`, acumulando por sesión (opencode manda `X-Session-Id`) y por
client_id (openclaw/zot no mandan sesión — research-context-patterns.md:13-17).

Es la SEGUNDA feature del epic 009. Reusa el paquete `internal/composition`
creado por 009-000 (walk de key paths + detección de ids) y absorbe el refactor
del audit (review.md Optional #1): un recorrido único que devuelve key paths +
detected ids + composición estructural, eliminando el parsing redundante del
body en `emitRequestTelemetry` (proxy.go:120-137).

La decisión de keying (session vs client_id) es el CRITERIO DE CIERRE del epic
y depende de la captura de telemetría 24-48h en curso. Este spec diseña el
parser y el endpoint para soportar AMBAS keyings simultáneamente; las
postcondiciones que dependen de esa decisión están marcadas **[KEYING]** y el
orquestador las ajusta al aprobar con el análisis de patrones. Fallback
documentado: si openclaw/zot no muestran sesión (ni anidada en metadata) →
composición por client_id para ellos.

## Contexto técnico

### Modelos/entidades tocadas
- `internal/composition/walk.go:42` — `Walk` existente; NUEVO `Analyze` que
  devuelve un `WalkResult` superset en UN solo recorrido (absorbe review.md
  Optional #1).
- `internal/proxy/proxy.go` — `handleChat` (281): fase 1 del registro tras
  clientID/sessionID (320-323) y telemetría (329), mismo gate (body válido,
  stream y no-stream, incluidos rechazos). `recordCacheTokens` (462-480):
  fase 2 con `Usage.PromptTokens`. `Handler()` (180-189): mux `GET
  /v1/context`. `handleUsage` (711-771): plantilla del endpoint con
  `?session=` + aislamiento.
- `internal/metrics/metrics.go` — nuevo `ContextRecord` + mapa propio con la
  misma política FIFO de `maxSessionsRetained` (008-003 P4).
- `internal/metrics/persist.go:32` — `stateVersion` 1→2, `persistedState` gana
  `contexts`.
- `internal/config/config.go:145-147` — `ContextConfig` gana `analysis`.
- `cmd/mofgw/main.go` — setter pre-tráfico `SetContextAnalysis` (patrón
  `SetContextMargin`, línea 208).

### Hooks/extensión disponibles
- `logging.WithRequestID`/`RequestID` (logging.go:23-31) — request_id en ctx
  para matchear fase 1 ↔ fase 2.
- `composition.Walk` → superset `Analyze` (review.md:17-20).
- `estimatePromptTokens` (proxy.go:241-243) — patrón `len/4` por porción.
- `handleUsage` (711-771) — plantilla endpoint `?session=` + 404 + aislamiento.
- `SessionsList`/`SnapshotClient` (metrics.go:185, 350) — prefix scan para el
  agregado por client_id.

### Convenciones aplicables
- Bodies crudos `[]byte` (transparencia) → análisis solo LEE.
- Best-effort en runtime (I7 009-000); fail-fast solo en config.
- Privacidad por construcción (009-000 I1/I3, 005-005): nunca contenido.
- Keying `client|session` + FIFO (008-003 P4); 404 sesión desconocida
  (008-003 P5).

## Contrato — firmas

```go
// internal/composition — NUEVO: análisis estructural en un solo recorrido.
// Absorbe el refactor del audit 009-000 R1 (keyPaths/detected con semántica
// IDÉNTICA a Walk → emitRequestTelemetry puede migrar sin cambiar schema).
type WalkResult struct {
    KeyPaths  []KeyPath    // igual que Walk (dedup, orden estable)
    Detected  []DetectedID // igual que Walk (safe fields, I3)
    Roles     []string     // rol de cada mensaje, en orden
    PartTypes []PartType   // composición por porción (ver PartType)
    NumTools  int          // cantidad total de tool calls (entries tool_calls)
    TotalBytes int         // len(body)
}

type PartType struct {
    Role      string // system|user|assistant|tool|developer|other
    Type      string // text|image|tool_result|tool_use|thinking|audio|other
    Bytes     int    // longitud cruda de la porción (valor JSON, sin wrapper)
    EstTokens int    // Bytes/4 (división entera, patrón estimatePromptTokens)
}

// Walk queda intacto (backward compatible con 009-000); Analyze es el superset.
func Analyze(body []byte, maxDepth int) (*WalkResult, error)

// internal/metrics — NUEVO registro de composición (mapa propio, FIFO).
func (m *Metrics) RecordContext(clientID, sessionID, requestID, model string, res *composition.WalkResult, nowUnix int64)
func (m *Metrics) UpdateContextUsage(clientID, sessionID, requestID string, promptTokens int64)
func (m *Metrics) ContextSnapshot(clientID, sessionID string) (ContextSnapshot, bool) // session "" = record de cliente
func (m *Metrics) SetContextHistoryPerSession(n int)

// internal/proxy — NUEVO endpoint + setter (NO cambia la firma de New).
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request)
func (s *Server) SetContextAnalysis(enabled bool, historyPerSession int)

// internal/config — bloque context existente gana analysis.
type ContextAnalysisConfig struct {
    Enabled           bool `yaml:"enabled"`
    HistoryPerSession int  `yaml:"history_per_session"`
}
```

## Postcondiciones

1. **P1 — Parser estructural (independiente de keying).** `composition.Analyze
   (body, maxDepth)` recorre el body UNA vez y devuelve, además de
   keyPaths/detected (semántica idéntica a `Walk`, absórbese el refactor del
   audit 009-000 R1), la composición estructural: `Roles` (rol de cada mensaje
   en orden), `PartTypes` (una entrada por porción de contenido con
   `Role`, `Type`, `Bytes` crudos y `EstTokens = Bytes/4`), `NumTools`
   (cantidad de entradas de `tool_calls`) y `TotalBytes`. Reglas de mapeo de
   `Type` (lista dura, con `other` como fallback, nunca crash):
   - content string → `text` (el string como porción).
   - content array → una porción por elemento: `text`, `image_url` → `image`,
     `input_audio` → `audio`, `tool_use` → `tool_use`, `tool_result` →
     `tool_result`, `thinking`/`redacted_thinking` → `thinking`, otro → `other`.
   - message con `tool_calls` no vacío → una porción `tool_use` por entry
     (porción = el JSON crudo de `arguments`); `content` null no aporta porción.
   - message con `role: tool` → porción `tool_result` (content del mensaje).
   - campo `reasoning_content` no vacío → porción `thinking`.
   - `maxDepth` acota la recursión (default 10; exceder trunca sin error ni
     panic, I4 de 009-000). El resultado NUNCA incluye contenido: valores de
     content/text/description/input/arguments jamás se reportan (solo sus
     tamaños y tipos).

2. **P2 — Acumulación por sesión/cliente [KEYING parcial].** NUEVO
   `ContextRecord` en `metrics`, en un mapa propio keyed como 008-003
   (`client|session` cuando hay sesión; `client|` para el record de cliente).
   TODO request registrado (P4 fase 1) actualiza SIEMPRE el record de cliente
   y, si hay `X-Session-Id`, además el record de sesión. El record acumula:
   `requests`, por-role (count de mensajes, bytes, est_tokens), por-part-type
   (count, bytes, est_tokens), `num_tools_total`, `prompt_tokens_estimated`
   (Σ est_tokens de porciones), `prompt_tokens_actual` (Σ Usage.PromptTokens
   post-response), `first_request_at`/`last_request_at`. Cada record tiene un
   ring buffer `history` de entries por request (acotado a
   `history_per_session`, default 50). Retención: los records por sesión se
   evictan FIFO por `last_request_at` con el MISMO tope `max_sessions_retained`
   (008-003 P4); el record de cliente (`client|`) NUNCA se evicta (acotado por
   la cantidad de clientes del config). **[KEYING]** El comportamiento para
   tráfico SIN sesión (openclaw/zot → solo record de cliente) es el fallback
   m4: si el análisis de la captura confirmara sesión anidada (detected_ids),
   esa fuente podría sumarse — el orquestador ajusta esta postcondición al
   aprobar.

3. **P3 — Endpoint `GET /v1/context` [KEYING parcial].** Con auth Bearer
   (`/v1/*` ya protegido por `auth.Wrap`, proxy.go:188) y aislamiento por
   clientID (cada cliente ve SOLO sus records):
   - `GET /v1/context?session=<id>` → 200 con `{client, scope:"session",
     session, requests, summary, latest, history}` del record `client|<id>`.
     404 si la sesión no existe para el cliente (consistente 008-003 P5).
   - `GET /v1/context` (sin session) → 200 con el MISMO shape pero
     `scope:"client"`, sin campo `session`, del record `client|` (agregado
     por client_id, incluye TODO el tráfico del cliente, con y sin sesión).
   - `summary` = `{roles:[{role, messages, est_tokens, pct}], part_types:
     [{type, count, bytes, est_tokens}], num_tools_total,
     prompt_tokens_estimated, prompt_tokens_actual, first_request_at,
     last_request_at}` — `pct` = est_tokens del role / Σ est_tokens de roles
     (0 si el total es 0). `latest` = entry más nuevo del history. `history` =
     entries request a request, más nuevo primero, acotado a
     `history_per_session`. Sin data → 200 con ceros/vacío (nunca nil,
     consistente con `UsageSnapshot`); con `history_per_session=0` → `history`
     vacío y `latest` ausente. **[KEYING]** La semántica de 404 para clientes
     sin sesiones (openclaw/zot) y el contenido del agregado por client_id son
     el fallback m4 — se ajustan al aprobar si la captura cambia la fuente de
     sesión.

4. **P4 — prompt_tokens_actual en DOS fases (independiente de keying).**
   Fase 1 (pre-request, en `handleChat` tras leer clientID/sessionID, mismo
   gate que telemetría 009-000 P3: body válido, stream y no-stream, incluidos
   los rechazos por limiter/budget/ventana/upstream): `Analyze(body, 10)` +
   `RecordContext(clientID, sessionID, requestID, model, res, now)` — crea la
   entry del history (con `prompt_tokens_actual` ausente) y acumula agregados
   estimados. Fase 2 (post-response, donde `Usage` existe — patrón
   `recordCacheTokens` 462-480, stream y no-stream):
   `UpdateContextUsage(clientID, sessionID, requestID, u.PromptTokens)` — setea
   el `prompt_tokens_actual` de la entry por `request_id` (matcheo exacto,
   evita races entre handlers concurrentes de la misma sesión) y acumula el
   agregado del record. Best-effort: request sin respuesta upstream (rechazado)
   queda con la entry sin `prompt_tokens_actual`; entry evictada antes de la
   fase 2 → solo acumula el agregado. Un error de análisis jamás falla el
   request (I7 009-000).

5. **P5 — Persistencia stateVersion 1→2 (independiente de keying).**
   `persistedState` gana `Contexts map[string]*ContextRecord json:"contexts,
   omitempty"` y `stateVersion = 2` (persist.go:32). Backward compat:
   - Nuevo binario lee snapshot v1 (Version 1 ≤ 2) → restaura sin error,
     `contexts` ausente = vacío (cero data de composición, arranque limpio).
   - Binario viejo (stateVersion 1) lee snapshot v2 (Version 2 > 1) → rechaza
     con el check EXISTENTE (persist.go:183) y `main.go` arranca limpio
     (comportamiento ya documentado de "state file no restaurado").
   - Round-trip save/load preserva `contexts` con su history completa. Los
     records evictados por retención no se persisten (consistente con
     sessions).

6. **P6 — Config `context.analysis` (independiente de keying).** El bloque
   `context:` existente (config.go:145-147) gana
   `analysis: {enabled, history_per_session}` (config.example.yaml:83-84).
   Defaults: `enabled=false`, `history_per_session=50`. `validate()`
   (config.go:279-409) rechaza `history_per_session < 0` (0 = sin history,
   solo agregados; consistente con la validación de
   `max_sessions_retained`). `enabled=false` → el proxy NO llama a
   `RecordContext` (sin records, sin memoria) y `/v1/context` responde 200 con
   ceros/vacío. INDEPENDIENTE de `telemetry`: puede estar on sin telemetría y
   viceversa. Wiring: `main.go` llama `srv.SetContextAnalysis(...)` antes del
   tráfico (patrón `SetContextMargin`, main.go:206-208) y
   `m.SetContextHistoryPerSession(...)` (patrón `SetMaxSessionsRetained`,
   main.go:209-213).

7. **P7 — Privacidad (independiente de keying).** Los `ContextRecord` guardan
   SOLO metadata estructural: roles, part types, bytes, est/actual tokens,
   conteos, `model`, `request_id`, timestamps. NUNCA contenido de prompts,
   respuestas, tools, keys, ni paths de claves (los key paths viven en
   telemetry.jsonl, 009-000). (Regla 005-005/009-000 I1, enforced por
   construcción — el parser jamás reporta valores.)

8. **P8 — No invasivo + bounds (independiente de keying).** El análisis no
   modifica el request ni la respuesta (solo LEE el body ya en memoria,
   proxy.go:297); no afecta ruteo, fallback, clamp, timeouts ni cache. Un
   error de análisis/registro no falla el request (best-effort en runtime;
   fail-fast solo en config, I5). Costo acotado: un recorrido por request
   (sin re-parsear), `maxDepth` 10, ring buffer `history_per_session`, records
   FIFO por `max_sessions_retained`, cardinalidad por clientes/sesiones.
   Suite completa `go test ./... -race` verde (cero red externa); vet y gofmt
   limpios.

## Invariantes

- I1 — **Privacidad:** nunca contenido en los records de composición ni en el
  state file; solo metadata estructural (P7). Enforcement por construcción
  (el parser no expone valores).
- I2 — **No invasivo:** el análisis no modifica request/respuesta; best-effort
  en runtime (I7 009-000); fail-fast solo en config.
- I3 — **Aislamiento por cliente:** los records de composición de un cliente
  no son visibles para otro (mismo clientID del token, 008-003 I2).
- I4 — **X-Session-Id solo lectura:** se usa para keying, nunca upstream
  (008-003 I1).
- I5 — **Bounds:** ring buffer acotado (history_per_session), records FIFO
  (max_sessions_retained), walk acotado (maxDepth 10); cardinalidad finita.
- I6 — **Backward compat de state:** nuevo lee v1 sin error; viejo rechaza v2
  (check existente persist.go:183).
- I7 — **Suite completa** `go test ./... -race` verde (cero red externa); vet
  y gofmt limpios.

## Criterios de aceptación

- C1 (P1): un body con content string + content array (text/image) +
  tool_calls + message role=tool + reasoning_content → `Analyze` devuelve
  roles en orden exacto, part types correctos (text/image/tool_use/
  tool_result/thinking), `EstTokens = Bytes/4` exacto por porción y
  `NumTools` correcto.
- C2 (P1/P7): strings distintivos largos en content/arguments/description →
  NUNCA aparecen como valor en el output de `Analyze` (solo tamaños).
- C3 (P1): para el mismo body, `Analyze(...).KeyPaths`/`.Detected` ==
  `Walk(...)` (refactor sin cambio de schema) y la suite de 009-000
  (telemetría, incluido el schema lock R10) sigue verde sin cambios de
  comportamiento.
- C4 (P1): body con anidamiento > maxDepth → trunca sin error ni panic (igual
  C8 009-000).
- C5 (P2/P3): 2 requests con `X-Session-Id` "s1" y cuerpos distintos →
  `GET /v1/context?session=s1` devuelve `requests: 2`, summary con roles
  agregados (est_tokens y pct correctos contra el esperado computable),
  history con 2 entries (más nuevo primero) y `latest` = el segundo request.
- C6 (P3): 2 clientes con el MISMO session id "s1" → cada cliente ve SOLO su
  propio record (aislamiento).
- C7 (P2/P3): 3 requests (2 con sesión, 1 sin sesión) del mismo cliente →
  `GET /v1/context` (sin session) muestra `requests: 3` (el record de cliente
  acumula TODO); `GET /v1/context?session=<la que existe>` muestra `requests: 2`.
- C8 (P4): upstream fake devuelve `usage.prompt_tokens: N` → la entry del
  request (matcheada por request_id) y `summary.prompt_tokens_actual` reflejan
  N (dos fases). Stream y no-stream.
- C9 (P4): request rechazado (p.ej. context_window o limiter) → la entry
  existe en history con `est_tokens` y SIN `prompt_tokens_actual`.
- C10 (P6): `analysis` ausente o `enabled=false` (default) → `GET /v1/context`
  responde 200 con ceros/vacío; sin records en memoria; `?session=unknown` →
  404.
- C11 (P2/P6): `history_per_session: 2` → tras 3 requests la history tiene 2
  entries (ring buffer).
- C12 (P5): snapshot v1 (sin `contexts`) → LoadState OK, contexts vacíos;
  save → `version: 2` y round-trip preserva contexts con history; snapshot con
  `version: 3` → LoadState error (check existente).
- C13 (P7): strings distintivos en content/arguments → grep negativo en
  `/v1/context` y en el state file serializado.
- C14 (P2/P3): [KEYING] con `max_sessions_retained: 1` y 3 sesiones distintas
  → `GET /v1/context` (cliente) muestra `requests: 3` (record de cliente
  sobrevive); `GET /v1/context?session=<la más vieja>` → 404 (evictada).
- C15 (P2/P3): [KEYING — fallback m4] requests SIN sesión (openclaw/zot) →
  `GET /v1/context` muestra su composición agregada por client_id;
  `GET /v1/context?session=<cualquier id>` → 404. El orquestador ajusta este
  criterio al aprobar con el análisis de patrones.
- C16 (P8): suite completa verde `go test ./... -race`, vet y gofmt limpios.

## Decisiones pendientes — criterio de cierre del epic (keying)

La decisión "composición keyed por session (opencode) vs keyed por client_id
(openclaw/zot)" se resuelve al cerrar la captura 24-48h
(research-context-patterns.md:59-65). El diseño soporta AMBAS a la vez en
runtime: requests con `X-Session-Id` → records `client|session`; sin sesión →
record `client|`. Postcondiciones que el orquestador debe revisar/ajustar al
aprobar con el análisis:

- **P2 (acumulación)** y **C15**: el fallback m4 (openclaw/zot → solo
  client_id) depende de que la captura confirme la ausencia de sesión. Si
  aparecieran session ids anidados (`detected_ids` en metadata.session_id/
  thread_id), la fuente de sesión podría extenderse a esos valores.
- **P3 (endpoint)** y **C14**: la semántica de 404 para clientes sin sesiones
  y la relación record-de-cliente ↔ records-de-sesión se ajustan si la keying
  primaria cambia.
- **P5 (persistencia)**: el layout de `Contexts` se mantiene para ambas
  keyings; si la decisión cambiara la estructura del record, el bump a v2 ya
  contempla el cambio de schema (misma estrategia de backward-compat).

Independientes de la decisión (no requieren ajuste): P1 (parser), P4 (dos
fases), P6 (config), P7 (privacidad), P8 (no invasivo).

## Cambios de código esperados (orientación, no vinculante)

- `internal/composition/walk.go`: + `WalkResult`, `PartType`, `Analyze(body,
  maxDepth)` — un solo recorrido que reutiliza la lógica de walkValue/skipValue
  y agrega la extracción de roles/part types/bytes/est tokens/numTools.
  `Walk` queda intacto (backward compatible). `emitRequestTelemetry`
  (proxy.go:116-151) PUEDE migrar a `Analyze` usando KeyPaths/Detected (schema
  lock R10 debe seguir verde).
- `internal/metrics/metrics.go`: + `ContextRecord` (agregados + history ring
  buffer + cap), `RoleAgg`, `PartAgg`, `ContextEntry` (con `PromptTokensActual
  *int64` nil = no conocido), `ContextSnapshot`; mapa `contextsMu` +
  `contexts map[string]*ContextRecord`; `RecordContext`,
  `UpdateContextUsage` (matcheo por request_id), `ContextSnapshot(client,
  session)` (session "" → record `client|`), `SetContextHistoryPerSession`,
  evicción FIFO reutilizando `evictOldestLocked`-style (o la misma, sobre el
  mapa nuevo).
- `internal/metrics/persist.go`: `stateVersion = 2`, `persistedState` +
  `Contexts map[string]*ContextRecord json:"contexts,omitempty"`, snapshot/
  restore del mapa (nil-safe para v1).
- `internal/proxy/proxy.go`: + `SetContextAnalysis(enabled, historyPerSession)`
  (setter pre-tráfico, patrón SetContextMargin); en `handleChat` tras
  clientID/sessionID (320-323) y telemetría (329): fase 1
  `Analyze(body, 10)` + `RecordContext` (gate: análisis habilitado, body
  válido); en los dos puntos post-response (407, 420) o dentro de
  `recordCacheTokens`: fase 2 `UpdateContextUsage` con
  `logging.RequestID(r.Context())`; + `handleContext` (plantilla
  `handleUsage`), registro en `Handler()` línea 184-186.
- `internal/config/config.go`: `ContextAnalysisConfig` + `Analysis` en
  `ContextConfig` (145-147), defaults (222-224), validación en `validate()`
  (`history_per_session < 0` → error).
- `cmd/mofgw/main.go`: `srv.SetContextAnalysis(...)` +
  `m.SetContextHistoryPerSession(...)` junto a los setters (184-213).
- `config.example.yaml`: bloque `context.analysis` documentado (83-84).
- Tests: unit de `composition.Analyze` (P1/C1-C4), de metrics
  (RecordContext/UpdateContextUsage/ContextSnapshot/ring buffer/evicción,
  P2/P4/C5/C11/C14), de persist v2 (P5/C12), de config (P6/C10) y e2e
  `e2e_009001_test.go` (C5-C10, C13, C15 — harness de e2e_009000_test.go).

## Fuera de alcance

- Sticky routing por sesión (009-002 del epic — consume esta composición).
- Alertas proactivas (futuro, plan.md epic 009 out of scope).
- Análisis de contenido de sesión o prompts (nunca — privacidad).
- Compresión/reescritura de prompts (vive en los runtimes, 008 out of scope).
- Cambios en openclaw/zot para mandar `X-Session-Id` (decisiones de runtime).
- Exposición de composición en Prometheus/metrics (solo endpoint dedicado;
  los agregados por client_id en `/v1/context` cubren el reporting).
