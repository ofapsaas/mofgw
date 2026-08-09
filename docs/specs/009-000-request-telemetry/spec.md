# Spec — 009-000-request-telemetry: telemetría de metadata de requests

---
feature_id: 009-000-request-telemetry
epic: mofgw-009-contexto-analisis
status: draft (aprobado por Ofap — dueño del proceso, delegación Pablo 08 Ago 2026)
created_at: 2026-08-09
updated_at: 2026-08-09
approved_by: Ofap (dueño del proceso — delegación explícita de Pablo, 08 Ago 2026)
approved_at: 2026-08-09
depends_on: 005-002-logging (Build/fanout), 005-005-verbose (Build con archivo), 001-007-auth (clientID), 008-003 (X-Session-Id solo lectura)
paralelizable: no
---

## Contexto

El epic 009 busca optimizar el contexto (85% del costo es cache hit = releer
contexto). Para diseñar composición de contexto por sesión y sticky routing
PRIMERO hay que descubrir dónde viven los ids de sesión en el tráfico real.
Investigación verificada: solo opencode manda `X-Session-Id`; openclaw y zot NO
(verificado en su source). Decisión de Pablo: "loguear con lo que hay" —
capturar la metadata real del tráfico para descubrir los patrones, sospechando
que el session id puede vivir en metadata del body (anidado), no en headers.

Esta feature es TELEMETRÍA DE DESCUBRIMIENTO: un archivo dedicado
`telemetry.jsonl` con metadata de cada request (headers allowlist + paths de
claves del body + detección de ids en campos seguros). NO es log operativo, NO
es medición de consumo (eso ya vive en metrics/006/008), NO toca el flujo del
request. Habilita la decisión de la feature siguiente del epic: composición
keyed por session vs keyed por client_id.

## Contexto técnico — 009-000-request-telemetry

### Modelos/entidades tocadas
- `proxy.Server` (internal/proxy/proxy.go:48-79) — campo `logger *slog.Logger` (línea 53); gana campo `telemetryLogger`.
- `proxy.New(...)` (internal/proxy/proxy.go:84) — firma actual de 9 parámetros; gana `telemetryLogger *slog.Logger` (nil = deshabilitado). Cambio de firma rompe 6 call-sites: main.go:165, e2e_test.go:113 y 145, e2e_007002_test.go:72, e2e_006001_test.go:56, e2e_limiter_test.go:106 (todos pasan nil → backward compatible).
- `handleChat` (internal/proxy/proxy.go:197-339) — punto de emisión del evento tras `ParseChatRequest` (línea 224) y lectura de `clientID`/`sessionID` (líneas 236-239); body `[]byte` ya en memoria (línea 213); precedente de lectura de metadata del body crudo en rawTools (líneas 273-277); precedente del evento `request_start` con num_messages/num_tools (líneas 279-287).
- `logging.Build` (internal/logging/logging.go:104-137) — fanout por nivel; NO se reutiliza. Patrón fail-fast del open de archivo en líneas 115-118 (`os.OpenFile(..., os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)`). `nopCloser` (líneas 139-142) reutilizable.
- `logging.WithRequest` (internal/logging/logging.go:41-46) — adjunta request_id del context; se usa con el telemetryLogger.
- `config.Config` (internal/config/config.go:160-174) — gana bloque `Telemetry TelemetryConfig yaml:"telemetry"`; defaults en `defaults()` (líneas 177-213); validación en `validate()` (líneas 261-381).
- `provider.ChatRequest` (internal/provider/provider.go:46-51) — Messages como `[]json.RawMessage`; NO tiene campo metadata tipado: el walk opera sobre el body crudo, no sobre la vista.
- `auth.ClientIDFrom(ctx)` (internal/auth/auth.go:35-38) — client_id disponible post-auth.Wrap.
- `metrics.RecordSession` (internal/metrics/metrics.go:286-307) — precedente de captura por sesión keyed `client|session`; la telemetría es CANAL SEPARADO (no toca metrics).

### Hooks/extensión disponibles
- `logging.WithRequestID`/`RequestID` (logging.go:23-31) + `withRequestID` (proxy.go:174-179): request_id ya propagado en ctx para cada request.
- `X-Session-Id` solo lectura en proxy.go:239 (008-003 I1) — precedente de no-forward.
- `X-Agent-Id` lectura en proxy.go:259 (004-002) — precedente de allowlist de headers.
- `estimatePromptTokens` (proxy.go:157-159) — precedente de procesar body sin re-parsear.
- `config.example.yaml` (líneas 12-84): secciones top-level server/fallback/providers/clients/pricing/model_metadata/context → telemetry es una sección top-level nueva.

### Convenciones aplicables a esta feature
- Fail-fast de arranque para recursos no abribles (logging.go:115-118; config inválida → error en Parse).
- Los bodies viajan CRUDOS []byte (transparencia, provider.go doc header) → el walk no debe re-marshal ni perder campos.
- Eventos de observabilidad como `request_start`/`request_end` son Debug en log operativo (proxy.go:280, 414) — la telemetría es un logger DEDICADO, no se mezcla.
- `sanitize()` de provider.go:195-207 es solo para mensajes de error upstream → NO sirve de base; logging/sanitize.go es nuevo.

## Contrato — firmas

```go
// internal/logging — NUEVO constructor, separado del fanout por nivel de Build()
func BuildTelemetry(file string) (*slog.Logger, io.Closer, error)

// internal/proxy — New() gana parámetro telemetryLogger (nil = telemetría off)
func New(r *router.Router, providers []provider.Provider, a *auth.Authenticator,
    m *metrics.Metrics, logger *slog.Logger, telemetryLogger *slog.Logger,
    maxBodyBytes int64, globalLimiter *limiter.Limiter, keyedLimiter *limiter.Keyed,
    backpressureTimeout time.Duration) *Server

// internal/config — bloque top-level telemetry
type TelemetryConfig struct {
    Enabled    bool    `yaml:"enabled"`
    SampleRate float64 `yaml:"sample_rate"`
    File       string  `yaml:"file"`
}
```

## Postcondiciones

1. **P1 — Destino dedicado y constructor separado:** `logging.BuildTelemetry(file
   string)` construye un logger JSON a nivel Info que escribe UNA línea JSON por
   evento en `file` (formato jsonl, handler JSONHandler sobre el archivo). Es un
   constructor SEPARADO de `Build()` (la telemetría se enruta por contenido, no
   por nivel; no reutiliza `levelFanoutHandler`). Con `file == ""` → error.
   Si el archivo no se puede abrir → devuelve error (fail-fast, mismo patrón que
   `Build()` en logging.go:115-118: `os.O_APPEND|os.O_CREATE|os.O_WRONLY`,
   permisos 0o640). Devuelve un closer que cierra el archivo.

2. **P2 — Wiring en proxy:** `proxy.New()` acepta `telemetryLogger *slog.Logger`;
   nil = telemetría deshabilitada (sin eventos, sin penalidad de rendimiento
   apreciable, comportamiento previo intacto). `Server` guarda el logger en un
   campo inmutable post-New (mismo patrón que `logger`, proxy.go:53). `main.go`
   llama `logging.BuildTelemetry(cfg.Telemetry.File)` cuando la telemetría está
   habilitada y gestiona AMBOS closers (operativo + telemetría) con defer.

3. **P3 — Evento request_telemetry:** en `handleChat`, inmediatamente después de
   `ParseChatRequest` exitoso y de leer clientID (proxy.go:224-239), si
   `telemetryLogger != nil` y el muestreo pasa, se emite UN evento por request.
   El evento cubre todo request con body válido — stream y no-stream, y también
   los que luego se rechacen por limiter/budget/ventana/upstream (el
   descubrimiento quiere ver TODO el tráfico). Un request con **JSON malformado**
   (error de parseo → 400) NO emite evento; un body JSON válido pero rechazado
   por validación de contrato (p.ej. sin messages → 400) SÍ emite evento
   (decisión del orquestador, R4 del audit). El evento se loguea con
   `logging.WithRequest(telemetryLogger, ctx)` para incluir request_id.

4. **P4 — Schema del evento:** cada evento es un objeto JSON con estos campos:
   - `ts` (RFC3339), `request_id` (del context), `client_id` (auth), `model`,
     `stream` (bool).
   - `headers`: mapa de headers de la allowlist presentes (solo valores de
     headers permitidos).
   - `key_paths`: array de paths de claves del body con dots (único, orden
     estable), p.ej. `["model", "messages[].role", "messages[].content",
     "metadata.session_id"]`. Sin valores.
   - `num_messages`, `num_tools`, `roles` (array de roles de messages).
   - `detected_ids`: array de `{path, value}` con los ids detectados en campos
     seguros (ver P7).
   - No hay otros campos. Formato de ejemplo (placeholders):
     `{"ts":"2026-08-09T12:00:00Z","request_id":"1a2b3c4d","client_id":"agent-main","model":"deepseek-v4-flash","stream":false,"headers":{"X-Session-Id":"ses_example","User-Agent":"opencode/1.18.4"},"key_paths":["model","messages[].role","messages[].content","metadata.session_id"],"num_messages":4,"num_tools":0,"roles":["system","user","assistant","user"],"detected_ids":[{"path":"metadata.session_id","value":"ses_example"}]}`

5. **P5 — Headers: allowlist + denylist:** se capturan SOLO los headers de la
   allowlist: `X-Session-Id`, `X-Agent-Id`, `X-Session-Affinity`, `User-Agent`,
   `Content-Length`, `Content-Type`, `Accept`. Cualquier header `X-*` que NO esté
   en la allowlist → negado (no aparece en el evento) + warn al logger operativo
   con el NOMBRE del header (nunca su valor). Los headers prohibidos
   (`Authorization`, `Cookie`, `X-Forwarded-For`, `X-Api-Key`, `X-Real-Ip`,
   `X-Host`, `Forwarded`, `Via`) jamás se capturan, aunque estén presentes.

6. **P6 — Walk de key paths:** un walk recursivo del body JSON (el `[]byte` ya en
   memoria, sin re-parsear el request) emite los NOMBRES de paths de claves con
   notación de dots (`model`, `messages[].role`, `messages[].content`,
   `metadata.session_id`) — SIN values profundos, SIN strings de contenido.
   El walk acepta `maxDepth` como parámetro; el proxy usa el default 10 (el
   formato OpenAI es ≤4 niveles). Profundidad excedida → el walk trunca esa rama
   sin error ni crash. Vive en el paquete NUEVO `internal/composition/`
   (`walk.go`). Reusa el body ya leído en proxy.go:213.

7. **P7 — Detección de ids en safe fields:** se inspeccionan VALORES solo en la
   lista dura de campos seguros: `metadata.*`, `*.session_id`, `*.thread_id`,
   `role`, `name`, `tool_call_id`. Dentro de esos campos, un valor se reporta en
   `detected_ids` si es id-like (prefijos `ses_`, `msg_`, `agent:`). NUNCA se
   inspecciona el valor de `content`, `text`, `description`, `input`,
   `arguments` — esos campos solo aportan su path (P6), jamás valor.

8. **P8 — Config:** bloque top-level `telemetry: {enabled, sample_rate, file}`.
   Defaults: `enabled=false`, `sample_rate=1`, `file=""`. `validate()` rechaza:
   `sample_rate <= 0`, `sample_rate > 1`, y `enabled=true` con `file` vacío
   (fail-fast en config, consistente con el patrón de validación existente).
   La telemetría es INDEPENDIENTE de `--verbose`: tiene kill switch propio; no
   exige ni se activa con debug operativo.

9. **P9 — Muestreo:** `sample_rate` aplica a la EMISIÓN del evento (no a la
   captura): `1` = 100% de los requests, `0.5` = ~50%. Con `enabled=false` no se
   emite nada. Mecanismo de muestreo (determinístico sobre request_id o
   aleatorio) es decisión del implementer; el criterio de aceptación C9 valida la
   banda resultante.

10. **P10 — Privacidad:** en `telemetry.jsonl` NUNCA se escribe contenido de
    prompts, respuestas, tools, keys, Authorization, cookies, ni el valor de
    ningún campo no-safe. Solo metadata: headers allowlist, paths de claves,
    conteos, roles e ids detectados en safe fields. (Regla de privacidad
    testeada en 005-005; aplica aquí como postcondición.)

## Invariantes

- I1 — **Privacidad:** nunca contenido de prompts/respuestas/tools/keys en
  telemetry.jsonl. Solo metadata (enforcement por evento, no por convención).
- I2 — **Allowlist + denylist:** enforcement por evento; un header `X-*` fuera de
  la allowlist se niega y dispara warn al log operativo; los prohibidos nunca se
  emiten.
- I3 — **Safe fields:** la inspección de valores ocurre SOLO en
  `metadata.*`, `*.session_id`, `*.thread_id`, `role`, `name`, `tool_call_id`;
  nunca en `content`, `text`, `description`, `input`, `arguments`.
- I4 — **maxDepth:** el walk está acotado a maxDepth (default 10); exceder la
  profundidad trunca sin error, sin panic y sin pico de CPU/memoria.
- I5 — **Fail-fast:** archivo de telemetría no abrible → error de arranque;
  config de telemetría inválida → error de arranque. Nunca silencioso.
- I6 — **Independencia de --verbose:** telemetría no exige debug operativo;
  --verbose no activa telemetría.
- I7 — **No invasivo:** la telemetría no modifica el request (no reescribe body,
  no forwardea headers, no afecta ruteo, fallback, clamp ni timeouts). Un error
  de emisión no falla el request (best-effort en runtime; fail-fast solo al
  arranque, I5).
- I8 — **Suite completa** `go test ./... -race` verde (cero red externa); vet y
  gofmt limpios.

## Criterios de aceptación

- C1: Con `telemetry` ausente o `enabled=false` (default) → no se crea
  `telemetry.jsonl` y no se emiten eventos (tráfico normal funciona igual).
- C2: Con `enabled=true` + `file` → cada request con body válido (stream y
  no-stream) emite EXACTAMENTE una línea JSON válida en `telemetry.jsonl` con
  `request_id`; un request con body inválido (400 de parseo) NO emite.
- C3: Un request con headers de la allowlist (`X-Session-Id`, `X-Agent-Id`,
  `User-Agent`, `Content-Type`, `Content-Length`, `Accept`) → el evento contiene
  esos headers con sus valores.
- C4: Un request con `X-Custom-Something` (fuera de allowlist) → el header NO
  aparece en el evento y el log operativo registra un warn con el nombre del
  header.
- C5: Un request con `Authorization`, `Cookie`, `X-Api-Key`, `X-Forwarded-For`
  seteados → ninguno de esos valores aparece en `telemetry.jsonl` (grep
  negativo del archivo).
- C6: Un body con `metadata.session_id` anidado (p.ej.
  `{"metadata":{"session_id":"ses_abc"}}`) → `key_paths` contiene
  `metadata.session_id` y `detected_ids` contiene `{path:"metadata.session_id",
  value:"ses_abc"}`.
- C7: Un body con contenido real en `messages[].content`, `text`,
  `description`, `input` o `arguments` → el evento contiene solo los paths
  (`messages[].content`, ...), jamás los valores.
- C8: Un body con anidamiento > maxDepth (p.ej. 15 niveles) → el walk trunca sin
  error, sin crash y el evento se emite igual.
- C9: Con `sample_rate=0.5` y **100 requests** → entre **40 y 60** emiten evento
  (banda amplia del muestreo, P≈96.4% binomial — enmendado por orquestador, R1
  del audit; la muestra de 20 con banda 8-12 era flaky ~26%).
- C10: `enabled=true` + `file=""` → error de validación de config al arrancar
  (el proceso no levanta).
- C11: Con `--verbose` y `enabled=false` → no se escribe `telemetry.jsonl`
  (independencia total del debug operativo).
- C12: `proxy.New(..., telemetryLogger=nil, ...)` → comportamiento previo
  intacto: la suite existente pasa sin cambios de comportamiento (los call-sites
  existentes agregan nil).
- C13: Suite completa verde (`go test ./... -race`), vet y gofmt limpios.
- C14: Test de privacidad: enviar requests con strings distintivos largos en
  content/arguments → grep de esos strings en `telemetry.jsonl` da cero matches.

## Cambios de código esperados (orientación, no vinculante)

- `internal/logging/sanitize.go` (NUEVO): allowlist de headers, denylist
  secundaria (`X-*` catch-all), lista dura de safe fields y reglas de detección
  id-like (`ses_`, `msg_`, `agent:`). No reutilizar el `sanitize()` de
  provider.go (es solo para mensajes de error upstream, provider.go:195-207).
- `internal/logging/logging.go`: + `BuildTelemetry(file string) (*slog.Logger,
  io.Closer, error)` — JSONHandler nivel Info sobre el archivo, append, sin
  fanout; fail-fast igual que `Build()` (líneas 115-118); reutiliza `nopCloser`.
- `internal/composition/walk.go` (NUEVO paquete): walk recursivo de key paths con
  `maxDepth`. (`composition.go` de la Phase 2 queda para la feature de
  composición de contexto — fuera de alcance acá.)
- `internal/proxy/proxy.go`: campo `telemetryLogger` en Server (junto a
  `logger`, línea 53); parámetro nuevo en `New` (línea 84); en `handleChat` tras
  ParseChatRequest (línea 224) la emisión del evento P4 con allowlist/safe
  fields/walk (reusando `body` de la línea 213 y `clientID` de la 236).
- `internal/config/config.go`: `TelemetryConfig` + campo en `Config` (líneas
  160-174) + defaults (líneas 177-213) + reglas en `validate()` (líneas
  261-381).
- `cmd/mofgw/main.go`: **helper nuevo `buildTelemetryFromConfig(cfg)`** (patrón
  `newHTTPServer` de SEC-001 — test seam): `enabled=false → (nil, nil, nil)` sin
  archivo; `enabled=true+file → (logger, closer, nil)`; `enabled=true+file="" →
  error`. Llamada a `BuildTelemetry` cuando enabled, pasaje a `proxy.New` (línea
  165), gestión del segundo closer (decisión del orquestador, R8 del audit).
- `config.example.yaml`: bloque `telemetry` documentado (sección top-level
  nueva, después de `context`).
- Tests: unit tests de `logging.BuildTelemetry` (fail-fast, jsonl), de
  `composition/walk` (maxDepth, paths), de sanitize (allowlist/denylist/safe
  fields) y e2e `e2e_009000_test.go` (C1-C14).

## Fuera de alcance

- Composición de contexto por sesión y sticky routing (Phase 2 del epic — usan
  los datos que esta telemetría descubre; `composition.go` se crea en esa
  feature).
- Decisión de diseño de composición keyed por session vs keyed por `client_id`:
  es el CRITERIO DE CIERRE del epic, documentado como fallback (si tras la
  captura openclaw/zot no muestran sesión ni anidada → composición por
  client_id). No se decide acá.
- Rotación/retensión/compresión de `telemetry.jsonl`: diferida y documentada
  (aceptable para 24-48h de descubrimiento); append simple en esta feature.
- Análisis de contenido de sesión o prompts (nunca — privacidad).
- Cambios en openclaw/zot para mandar `X-Session-Id` (decisiones de runtime,
  fuera del proxy).
- Endpoint HTTP para consultar la telemetría (el archivo es el artefacto).
