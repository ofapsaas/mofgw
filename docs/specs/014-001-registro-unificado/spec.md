---
feature_id: 014-001-registro-unificado
feature_name: registro-unificado
epic: 014-mofgw-registro-unificado
status: draft
created_at: 2026-08-17
updated_at: 2026-08-17
depends_on: 001-003-fallback (outcome por intento), 002-001-retry (retries por intento), 003-001-provider-cache (tokens cache/reasoning), 006-001-usage-accounting (tokens prompt/completion), 006-002-costos (cost_usd), 005-005-verbose (precedente privacidad), 009-000-request-telemetry (precedente writer JSONL + convivencia anti-scope), 011-001-responses-endpoint, 011-006-embeddings (recordCacheTokens en esos endpoints)
paralelizable: no
---

# Spec — 014-001-registro-unificado: registro unificado de accounting + outcome

Proyecto: `github.com/ofapsaas/mofgw` (Go)

---

## 1. Descripción

Registro **append-only en disco (JSONL)** del ciclo completo de cada request de mofgw
que llega a la cadena de routing: **un evento por intento** (cada visita a un provider
de la cadena, con outcome `ok|fallback`, causa y retries) + **un evento terminal**
por request (outcome `success|error`, error_code, tokens, costo, streaming). Es la
**fuente de verdad con resolución temporal** que hoy no existe: telemetry.jsonl
(009-000) se loguea al ENTRAR el request (sin provider ni tokens ni costo) y
/metrics+state.json acumulan desde arranque (sin ventana temporal). Este registro
permite responder "cuánto gastó el cliente X en provider Y esta semana" y la
estadística de fallos por provider con números EXACTOS.

El **esquema del evento es el contrato** que consume el epic EXTERNO de
consolidación/visualización (fuera de mofgw): el epic externo lee el JSONL por fuera,
no toca el proxy. Por eso el formato es estricto: cada línea es JSON exacto con los
campos definidos en esta spec (nada más, nada menos), sin envoltorio de slog.

**Aditividad estricta:** off por default (`registry.enabled=false`). On NO toca
ruteo/fallback/cache/sticky/metrics: los eventos se emiten post-decisión y un fallo de
escritura jamás altera la respuesta al cliente.

**Decisiones lockeadas (ver §Cambios):**
- **D1 — Writer dedicado `internal/registry/` (nuevo), sin slog**: el esquema es un
  contrato externo → se serializa SOLO los campos del esquema con `json.Marshal`
  (structs tipadas o map con orden estable documentado), una línea JSON por evento
  terminada en `\n`. NO reutilizar el handler de slog (metería `time/level/msg`).
- **D2 — Knob de config top-level `registry: {enabled, file}`** (default
  `enabled=false`, `file=""`). `enabled=true` + `file=""` → error de validación
  (fail-fast en Parse, patrón `telemetry` 009-000 P8). Ignorado → registry off.
- **D3 — Discriminador `type` por línea**: `"attempt"` | `"terminal"` (necesario en
  JSONL con dos formas de evento).
- **D4 — `ts` = RFC3339 UTC con milisegundos** (p.ej. `2026-08-17T18:00:00.123Z`),
  consistente con telemetry P4.
- **D5 — Intento = una visita a un provider de la cadena filmada al CONCLUSIÓN** de la
  visita (outcome ya conocido), incluyendo los reintentos 002-001 como parte de la
  misma visita: `attempt` = ordinal global de la visita (1-based, secuencial en el
  request), `retries` = nº de reintentos ejecutados sobre ese provider en esa visita.
- **D6 — `cause` = `typ` de la clasificación del router** (classify/timeouts.Classify):
  `timeout`, `network`, `rate_limit`, `invalid_request_error`, `upstream_error`,
  etc.; vacío (`""`) en outcome `ok`. `status` = HTTP status del fallo tal como lo
  vio la cadena (timeout/network → 502 normalizado); `200` en outcome `ok`.
- **D7 — Alcance: SOLO requests que llegan al routing (cadena de providers).** Los
  rechazos pre-routing (auth 401, parseo 400, limiter 429, budget 429, /v1/models,
  /healthz) NO emiten eventos: no consumen providers y ya están contados en
  `mofgw_rejected_requests_total`/metrics. El registro es de accounting por provider.
- **D8 — Termal error: `error_code` = `ChainError.Code` semántico** (`chain_exhausted`,
  `degraded_limit`, `upstream_unavailable`, `model_not_found`...) y `status` = status
  HTTP FINAL enviado al cliente (502/503/404...), no el raw del upstream.
- **D9 — `cost_usd` = `estimateCost(model, miss, completion, hit)`** — MISMA fórmula
  que recordCacheTokens/metrics (006-002 P2) para que registro y /metrics concuerden
  exactamente. `miss = prompt - cache`, `hit = cache`.
- **D10 — Writer síncrono best-effort en runtime**: mutex + write + (error → warn al
  logger operativo, el request sigue). Fail-fast SOLO al arranque (archivo no
  abrible → error al construir el writer).
- **D11 — Identidad: `request_id` = `logging.RequestID(ctx)` y `client` =
  `auth.ClientIDFrom(ctx)`**, ambos disponibles en router y proxy (el router ya usa
  ClientIDFrom en resolveReady). Un request sin request_id en ctx no se registra.
- **D12 — Extensibilidad del contrato**: el esquema v1 es exacto; versiones futuras
  pueden AÑADIR campos (p.ej. `session_id`) sin romper consumidores tolerantes. El
  escribiente del epic externo debe tolerar campos nuevos (documentado en USER-GUIDE).
- **D13 — Rechazos y errores pre-router**: no emiten (D7). Errores de chain SÍ
  (terminal error con 0+ intentos según corresponda; los intentos fallidos ya se
  filmaron con outcome `fallback`).
- **D14 — Singleflight (010-001)**: cada request FÍSICO emite su propio terminal
  (el follower también llama recordCacheTokens). Los followers NO emiten intentos (no
  hicieron llamadas upstream) y su terminal puede referenciar `final_provider` y
  tokens compartidos del vuelo. Follower de vuelo fallido → terminal error con
  `error_code` del error absorbido.
- **D15 — Stream interrumpido post-primer-byte** (Copy error en handleStream): se
  registra terminal `success` con los tokens capturados (0 si no se pudo leer usage);
  el request SÍ obtuvo respuesta del provider (el evento ya arrancó); el detalle del
  error queda en logs operativos.

---

## 2. Contrato

### 2.1 Firmas esperadas (orientativas, no vinculantes — el contrato es el esquema)

```go
// internal/registry (NUEVO paquete)
type Writer struct { mu sync.Mutex; f *os.File }
func NewWriter(file string) (*Writer, error) // O_APPEND|O_CREATE|O_WRONLY 0o640; error si no abre
func (w *Writer) Close() error
func (w *Writer) Attempt(ev AttemptEvent) error  // best-effort: error → warn, request sigue
func (w *Writer) Terminal(ev TerminalEvent) error

type AttemptEvent struct {
    Type      string         `json:"type"`               // "attempt"
    RequestID string         `json:"request_id"`
    Ts        string         `json:"ts"`                 // RFC3339 ms UTC
    Client    string         `json:"client"`
    Provider  string         `json:"provider"`
    Model     string         `json:"model"`
    Outcome   string         `json:"outcome"`            // "ok" | "fallback"
    Cause     string         `json:"cause"`              // "" si ok
    Status    int            `json:"status"`             // 200 si ok; HTTP del fallo
    Attempt   int            `json:"attempt"`            // ordinal global 1-based
    Retries   int            `json:"retries"`            // reintentos en esta visita
}

type TerminalEvent struct {
    Type          string         `json:"type"`           // "terminal"
    RequestID     string         `json:"request_id"`
    Ts            string         `json:"ts"`
    Client        string         `json:"client"`
    Outcome       string         `json:"outcome"`        // "success" | "error"
    ErrorCode     string         `json:"error_code"`     // "" si success
    Status        int            `json:"status"`         // 200 / 502 / 503 / 404...
    FinalProvider string         `json:"final_provider"` // ganador o último intentado; "" si ninguno
    Tokens        Tokens         `json:"tokens"`         // {prompt,completion,cache,reasoning}
    CostUSD       float64        `json:"cost_usd"`
    Stream        bool           `json:"stream"`
}
type Tokens struct {
    Prompt     int `json:"prompt"`
    Completion int `json:"completion"`
    Cache      int `json:"cache"`      // cached/hit tokens (003-001)
    Reasoning  int `json:"reasoning"`  // reasoning tokens (003-001)
}
```

### 2.2 Config

```go
// internal/config — bloque top-level
type RegistryConfig struct {
    Enabled bool   `yaml:"enabled"`
    File    string `yaml:"file"`
}
```

### 2.3 Postcondiciones (P1..P17)

Cada postcondición es de **comportamiento observable** (contenido del archivo y/o
request path) e individualmente testeable con un test de comportamiento.

- **P1 — Config y defaults:** bloque top-level `registry: {enabled, file}`; con el
  bloque ausente o `enabled` false/ausente → `Registry == {false, ""}`. `enabled=true`
  + `file=""` → error de validación en `config.Parse` (el proceso no levanta).

- **P2 — Off por default (aditivo):** con registry off/ausente: NO se crea ningún
  archivo, NO se emite ningún evento y el request path es IDÉNTICO al previo —
  la suite existente completa pasa verde con `-race` sin cambios de comportamiento.

- **P3 — Writer JSONL append-only con fail-fast de arranque:** `NewWriter(file)`
  abre con `O_APPEND|O_CREATE|O_WRONLY`, permisos `0o640`; archivo no abrible →
  error (fail-fast, patrón `logging.BuildTelemetry` logging.go:147-157). Cada evento
  se escribe como UNA línea JSON válida terminada en `\n`. El archivo NUNCA se trunca
  ni se reescribe: siempre append (sobrevive restarts del proceso).

- **P4 — Thread-safety y orden:** dos requests concurrentes producen líneas completas
  y no intercaladas (serialización por mutex); el orden en el archivo es el orden de
  emisión; ninguna línea queda a medias ante escrituras concurrentes.

- **P5 — Esquema del evento attempt (exacto):** cada línea `attempt` contiene SOLO:
  `type="attempt"`, `request_id`, `ts`, `client`, `provider`, `model`,
  `outcome("ok"|"fallback")`, `cause`, `status`, `attempt`, `retries` — con los tipos
  de 2.1. Sin campos extra (el test -> un unmarshal a map y verifica el set exacto
  de keys).

- **P6 — Esquema del evento terminal (exacto):** cada línea `terminal` contiene SOLO:
  `type="terminal"`, `request_id`, `ts`, `client`, `outcome("success"|"error")`,
  `error_code`, `status`, `final_provider`, `tokens{prompt,completion,cache,reasoning}`,
  `cost_usd`, `stream`. Sin campos extra.

- **P7 — Emisión del intento (éxito):** cuando la cadena concluye con éxito en el
  provider P (Complete OK o stream que arrancó), el request filmó exactamente UN
  evento `attempt` para P con `outcome="ok"`, `cause=""`, `status=200`, `attempt=k`
  (ordinal global de la visita a P), `retries=` nº de reintentos 002-001 ejecutados
  sobre P en esa visita.

- **P8 — Emisión del intento (fallback):** cuando un provider P falla (retryable
  agotado, no-retryable, TTFB timeout, stream cerrado pre-primer-byte, error de
  clamp/inject), el request filma UN evento `attempt` para P con `outcome="fallback"`,
  `cause=` typ de la clasificación (D6), `status=` HTTP del fallo visto por la cadena
  (o 502 para timeout/network), `attempt=k`, `retries=` reintentos hechos en la
  visita. Se filma aunque sea el ÚLTIMO intento del chain (después vendrá el terminal
  error).

- **P9 — Serialización del retry:** un provider que falla y se reintenta con 002-001
  (retry_same_provider) filmado como UNA sola visita: p.ej. try1 falla → retry → try2
  OK ⇒ 1 intento `outcome="ok"` con `retries=1`. try1 falla → retry → try2 falla ⇒ 1
  intento `outcome="fallback"` con `retries=1`.

- **P10 — Un terminal por request en routing:** todo request que llega a la cadena
  filma EXACTAMENTE un evento `terminal`: `success` (post-respuesta, en los
  call-sites de `recordCacheTokens`) o `error` (cuando la cadena falla, en el path de
  `handleChainError`/absorb de los endpoints chat/responses). No hay request en
  routing sin terminal.

- **P11 — Correlación request_id:** TODOS los eventos de un mismo request comparten
  el MISMO `request_id` (del ctx, `logging.RequestID`), y `client` es el clientID
  autenticado (`auth.ClientIDFrom`). El terminal llega SIEMPRE después (en orden de
  archivo) de los intentos de su request.

- **P12 — Terminal success (campos exactos):** `outcome="success"`, `error_code=""`,
  `status=200`, `final_provider=` provider ganador (el que respondió o arrancó el
  stream), `tokens` = usage real del provider (`prompt`=PromptTokens,
  `completion`=CompletionTokens, `cache`=CachedTokens, `reasoning`=ReasoningTokens;
  ausente → 0), `cost_usd` = `estimateCost(model, miss, completion, hit)` (D9), y
  `stream` = flag real del request (`req.Stream`).

- **P13 — Terminal error (campos exactos):** `outcome="error"`, `error_code` =
  `ChainError.Code` semántico (D8: `chain_exhausted`, `degraded_limit`,
  `upstream_unavailable`, `model_not_found`, ...), `status` = status HTTP FINAL
  enviado al cliente (502/503/404...), `final_provider` = `ChainError.ProviderID`
  (último provider intentado; `""` si ninguno — p.ej. `model_not_found` o degraded
  fast-fail), `tokens` todos 0, `cost_usd` = 0, `stream` = flag real del request.

- **P14 — Fallback/déficit reflejado (CA del epic #3):** request donde p1 falla y p2
  responde ⇒ 2 intentos (p1 `fallback` con cause/status del fallo; p2 `ok`) + terminal
  `success` con `final_provider=p2`. La consolidación de fallos NO depende del outcome
  final del request.

- **P15 — Privacidad (metadata-only):** en el archivo NUNCA aparece contenido de
  prompts, respuestas, tools, argumentos, headers, Authorization, cookies, api keys,
  ni ningún valor del body. El writer solo serializa los campos del esquema (D1 por
  construcción). Verificable con grep negativo de strings distintivos.

- **P16 — Resiliencia del writer en runtime:** una falla de escritura (disco lleno,
  EIO) NO falla el request ni crashea el proceso: se loguea un warn al logger
  operativo y el request sigue su curso. La falla de arranque SÍ es fatal (P3).

- **P17 — Coexistencia con 009-000:** `telemetry.jsonl` (descubrimiento, muestreo) y
  sus knobs quedan exactamente iguales; `registry` es un canal nuevo e independiente
  con su propio knob. El registro NO reemplaza ni modifica 009-000, ni /metrics, ni
  state.json (TECHDEBT #13).

### 2.4 Mapeo test ↔ postcondición (sugerido)

| Test ID                          | Verifica postcondición(es) | Criterio(s) |
|----------------------------------|----------------------------|-------------|
| `T_config_defaults_registry`     | P1                         | C1          |
| `T_config_enabled_con_file_vacio`| P1                         | C1          |
| `T_off_default_sin_archivo`      | P2                         | C2          |
| `T_off_default_suite_verde`      | P2                         | C2          |
| `T_writer_fail_fast`             | P3                         | C3          |
| `T_writer_append_only`           | P3                         | C3/C11      |
| `T_writer_jsonl_valid`           | P3, P5, P6                 | C10         |
| `T_writer_thread_safety`         | P4                         | C12         |
| `T_attempt_schema`               | P5                         | C10         |
| `T_terminal_schema`              | P6                         | C10         |
| `T_happy_path_no_stream`         | P7, P11, P12               | C4          |
| `T_fallback_consolidado`         | P8, P14                    | C5          |
| `T_retry_mismo_provider`         | P9                         | C6          |
| `T_chain_agotado`                | P8, P10, P13               | C7          |
| `T_model_not_found`              | P10, P13                   | C8          |
| `T_streaming_ok`                 | P7, P12                    | C9          |
| `T_degraded_fastfail`            | P10, P13                   | C13         |
| `T_correlacion_request_id`       | P11                        | C11         |
| `T_privacy_grep_negativo`        | P15                        | C15         |
| `T_writer_fallible_no_rompe`     | P16                        | C16         |
| `T_telemetry_convive`            | P17                        | C14         |
| `T_responses_endpoint`           | P12                        | C17 (opcional) |
| `T_embeddings_endpoint`          | P12                        | C17 (opcional) |

---

## 3. Invariantes

- **I1 — Append-only:** el archivo solo se abre `O_APPEND`; nunca se trunca/reescribe;
  una línea ya escrita no se modifica; el registro sobrevive restarts.
- **I2 — Privacidad:** nunca contenido de prompts/respuestas/tools/headers/keys;
  enforced por construcción (el writer serializa solo el esquema; los emisores no
  pasan body/headers al writer).
- **I3 — Off default:** sin `registry.enabled=true`, no existe ni el archivo ni el
  costo de emisión; el request path es funcionalmente idéntico.
- **I4 — Best-effort runtime / fail-fast arranque:** un error de escritura no altera
  la respuesta ni mata el proceso; un error de apertura sí impide el arranque.
- **I5 — Thread-safety:** escrituras serializadas; líneas completas; sin intercalado.
- **I6 — Aditividad total:** no se modifica ruteo/fallback/cache/sticky/metrics/
  singleflight/telemetry; los eventos se emiten post-decisión sin retroalimentación.
- **I7 — Esquema estable:** los campos y tipos de 2.1 son el contrato del epic
  externo; no se serializa nada fuera del esquema (D1/D12).
- **I8 — Correlación:** `request_id` nunca vacío en eventos de requests en routing y
  compartido por todos los eventos del request.
- **I9 — Calidad Go:** suite completa `go test ./... -race` verde, `go vet` y
  `gofmt` limpios.
- **I10 — Alcance D7:** los rechazos pre-routing y endpoints no-routing nunca emiten
  eventos al registro.

---

## 4. Criterios de aceptación (medibles)

- **C1:** config sin `registry:` → `Registry{Enabled:false, File:""}`; con
  `registry.enabled=true` y `registry.file=""` → `config.Parse` devuelve error.
- **C2:** con registry off, tras N requests (incluidos fallback/stream/error) no
  existe el archivo configurado y la suite existente pasa verde `-race`.
- **C3:** `NewWriter("/ruta/no/abrible/x.jsonl")` → error no-nil.
- **C4 (happy path no-stream):** 2 providers, p1 responde 200 ⇒ el archivo tiene 1
  `attempt` (`outcome:"ok"`, `cause:""`, `status:200`, `attempt:1`, `retries:0`,
  provider p1, model y client correctos) + 1 `terminal` (`outcome:"success"`,
  `error_code:""`, `status:200`, `final_provider:p1`, tokens == usage real,
  `cost_usd` == estimateCost de esos tokens, `stream:false`); ambos con el mismo
  `request_id`.
- **C5 (fallback):** p1 falla 500 retryable, p2 responde ⇒ 2 `attempt` (p1:
  `outcome:"fallback"`, `status:500`/502, `attempt:1`, `cause` != ""; p2:
  `outcome:"ok"`, `attempt:2`) + `terminal success` con `final_provider:p2`.
- **C6 (retry):** p1 falla 500, retry 002-001 OK ⇒ 1 `attempt` con `outcome:"ok"`,
  `retries:1`, `attempt:1`; terminal success p1.
- **C7 (chain agotado):** ambos providers fallan retryable ⇒ 2 `attempt` con
  `outcome:"fallback"` + `terminal` con `outcome:"error"`, `error_code:
  "chain_exhausted"`, `status:502`, `final_provider` = último provider intentado,
  tokens 0, `cost_usd:0`.
- **C8 (model_not_found):** ningún provider sirve el modelo ⇒ 0 `attempt` + 1
  `terminal` con `outcome:"error"`, `error_code:"model_not_found"`, `status:404`,
  `final_provider:""`, tokens 0.
- **C9 (streaming):** request `stream:true` con primer byte OK ⇒ intentos + terminal
  `success` con `stream:true` y tokens no degenerados/iguales al usage del chunk
  final.
- **C10:** cada línea del archivo (todos los escenarios) parsea como JSON con el set
  exacto de keys de su `type` (sin extra) y tipos correctos.
- **C11:** correr 2 requests seguidos ⇒ el terminal del 1º aparece antes que el 1er
  intento del 2º (orden); los `request_id` aparecen correlacionados; reabrir el
  proceso con el mismo archivo ⇒ continúa append (no trunca).
- **C12:** 20 requests concurrentes (mixtos ok/fallback) ⇒ 20 terminales + suma de
  intentos coherente; ninguna línea corrupta ni intercalada; todos los request_id
  correlacionan.
- **C13 (degraded 002-004):** todos los providers down con degraded activo ⇒ 0
  intentos + `terminal error` con `error_code` del absorb (`degraded_limit`/
  `all_providers_down` según corresponda) y `status:503`.
- **C14:** con registry ON y telemetry (009-000) activa, `telemetry.jsonl` sigue
  escribiendo su esquema normal y el archivo del registry es independiente.
- **C15 (privacidad):** enviar requests con strings distintivos LARGOS en
  `content`/`arguments`/headers/Authorization ⇒ grep de esos strings en el archivo
  del registry devuelve 0 matches (los intentos/terminales contienen solo el esquema).
- **C16:** con un writer cuyo archivo falla al escribir (p.ej. se cierra el fd o se
  inyecta un writer fallible): el request responde con su resultado normal
  (status/body/usage headers idénticos a writer sano) y se registra warn en logs.
- **C17 (opcional, endpoints):** `/v1/responses` happy path ⇒ 1 terminal success con
  `stream:false` y tokens/cost correctos (via recordCacheTokens responses.go:510);
  `/v1/embeddings` happy path ⇒ 1 terminal success con `stream:false` y tokens de
  input (`prompt` = total, `completion:0`) (via embeddings.go:142).

---

## Contexto técnico (autoritativo — hooks verificados en código)

- `internal/proxy/proxy.go`:
  - `recordCacheTokens(requestID, clientID, sessionID, providerID, model string, u *provider.Usage)` (proxy.go:857-891) — **emisor del terminal success**; acumula usage/cost/context/session/sticky. Call-sites: proxy.go:704 (stream, con `streamUsage`), 741 (singleflight líder), 791 (singleflight follower), 810 (non-stream); responses.go:510; embeddings.go:142. Para D15/función adjunta el flag `stream` se pasa desde el call-site (`req.Stream`).
  - `handleChainError(w, err, logger)` (proxy.go:1043-1059) — **emisor del terminal error**: tiene `ChainError{Status, Type, Message, Code, ProviderID}`; los call-sites (proxy.go:736, 805, 1011; responses.go:502) tienen `requestID/clientID/model/stream` en scope → pasar contexto de emisión o emitir en los call-sites.
  - `withRequestID` (proxy.go:460-463) — `logging.WithRequestID(r.Context(), newRequestID())` para cada request; `logging.RequestID(ctx)` disponible.
  - `estimateCost(model, miss, completion, hit)` (proxy.go:896-908) — fórmula del costo (D9).
  - `openAIError`/absorb — status finales que ve el cliente (C7/C8/C13).
- `internal/proxy/responses.go:510` — recordCacheTokens del endpoint responses (011-001), siempre `stream:false`.
- `internal/proxy/embeddings.go:142` — recordCacheTokens del endpoint embeddings (011-006), `stream:false`.
- `internal/router/router.go`:
  - `provider_attempt` (826 Complete / 973 stream) — inicio de visita; `retry_same_provider` (873, 1017, 1065, 1087, 1130, 1150); `provider_fallback` (883, 1172 `logFallback`); `cooldown_start` (393 en `recordFailure`).
  - Los eventos attempt se filman al CONCLUSIÓN de cada visita: puntos de éxito (Complete: err==nil → return, router.go:858-861; stream: commitStream/primer byte, router.go:1074-1092+) y puntos de fallo (no-retryable router.go:866-868; retry agotado/fallback router.go:878-891; TTFB timeout 1049-1073; stream cerrado 1082+; clamp/inject errors 833-854).
  - `classify(err)` (router.go:653-668) → `(retryable, status, typ, msg)` — fuente de `cause` (D6).
  - `auth.ClientIDFrom(ctx)` ya usado en `resolveReady` (router.go:724) — identidad disponible en el router.
  - `ChainError` (router.go:59-67): `Status, Type, Message, Code, ProviderID, Err, RetryAfter` — fuente de `error_code` (D8).
- `internal/config/config.go` — precedente `TelemetryConfig` (220-224) top-level + defaults (387) + validación (620-628); `RegistryConfig` sigue el mismo patrón.
- `internal/logging/logging.go:140-157` — precedente `BuildTelemetry(file)` (O_APPEND|O_CREATE|O_WRONLY 0o640, fail-fast) para el constructor del writer.
- `cmd/mofgw/main.go:382` — precedente de wiring del logger telemetry con closer; el writer del registry se construye SOLO si `cfg.Registry.Enabled` (fail-fast) y se cierra con defer; pasar `nil` al proxy/router si off.
- Config example: `config.example.yaml` gana bloque `registry:` documentado.

## Cambios de código esperados (orientación, no vinculante)

- `internal/registry/` (NUEVO): `registry.go` con `Writer`, `AttemptEvent`, `TerminalEvent`, `Tokens`; `NewWriter` (fail-fast), `Close`, `Attempt`, `Terminal` (mutex + json.Marshal + `\n`, best-effort con warn).
- `internal/config/config.go`: `RegistryConfig` + campo `Registry` en `Config` + defaults `{false,""}` + validación `enabled && file=="" → error`.
- `internal/router/router.go`: campo/setter `Registry *registry.Writer` (nil = off, patrón Options/SetStickyRouting); emisión de eventos attempt al concluir cada visita en Complete y stream (reusar `logging.RequestID(ctx)` y `auth.ClientIDFrom(ctx)`); método helper `emitAttempt(...)` con nil-check.
- `internal/proxy/proxy.go`: campo `registry` en Server (+ setter o param en New, nil = off); emisión del terminal success en los 4 call-sites de recordCacheTokens en chat (pasando `stream`), o helper `emitTerminalSuccess` invocado junto a recordCacheTokens; emisión del terminal error en los call-sites de handleChainError (pasando requestID/clientID/model/stream + `ce.Code/Status/ProviderID`), o param adicional a handleChainError.
- `internal/proxy/responses.go:510` y `embeddings.go:142`: emisión del terminal success (`stream:false`) junto a recordCacheTokens; responses error path (502) también emite terminal error.
- `cmd/mofgw/main.go`: construir writer si `cfg.Registry.Enabled` (fail-fast), wiring a proxy/router, closer con defer; `config.example.yaml` documenta el bloque.
- Tests: `internal/proxy/e2e_014001_test.go` (C4-C17), `internal/config/registry_red_test.go` o unit de config (C1), `internal/registry/registry_test.go` (C3, C10, C12, C16) — siguiendo el patrón `e2e_009000_test.go` (harness con `logging.BuildTelemetry` → aquí `registry.NewWriter`).

## Fuera de alcance (anti-scope)

- **Consolidación diaria/semanal/mensual y visualizaciones** — epic EXTERNO (lee el JSONL por fuera, no toca mofgw).
- NO tocar ruteo/fallback/cache/sticky/singleflight (solo emisión post-hoc).
- NO reemplazar ni modificar la telemetría 009-000 (conviven; P17/I6).
- NO reemplazar /metrics ni state.json (TECHDEBT #13) — el registro es adicional.
- NO requests pre-routing (401/400/429 limiter/budget) ni /v1/models ni /healthz (D7/I10).
- NO rotación/compresión/retention del JSONL (deuda documentada; append simple, igual que 009-000).
- `session_id` NO se incluye en v1 (D12: extensión futura si el usuario la pide).
- NO análisis de contenido (nunca — privacidad, I2).

---

## Cambios / decisiones (consolidación)

- D1..D15 detalladas en §1 (writer dedicado tipado; knob `registry`; discriminador `type`; ts RFC3339 ms; intento = visita concluida con attempt/retries; cause = typ de classify; alcance only-routing; error_code = ChainError.Code; cost_usd = estimateCost; writer síncrono best-effort; identidad por ctx; esquema v1 extensible; rechazos pre-router sin eventos; singleflight por request físico; stream interrumpido = success con tokens capturados).

---

Status: **Draft** (cdad-architect, round 2/5, 2026-08-17). Pendiente aprobación del usuario (Pablo/Ofap HITL — indelegable) antes de RED.