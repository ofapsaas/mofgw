---
id: 020-002-metrics-summary-html
epic: 020-mofgw-consumption-report
status: draft
---

# Feature 020-002 — metrics-summary-html

> **DRAFT — Pendiente de aprobación HITL (Pablo/Ofap — indelegable).**
> Redactado por cdad-architect desde el Discovery con evidencia de lectura
> (gate 1→2 cerrado; decisiones HITL 1-4 ratificadas 17-18 Sep 2026; decisiones
> de architect 5-12 lockeadas por el discovery).

## Descripción

Endpoint **read-only** `GET /v1/metrics/summary?date=YYYY-MM-DD` que sirve un
documento **HTML estático self-contained** con el consumo del día leído en
streaming desde `registry.jsonl` + sus rotados logrotate (activo + `.1..N`).
Tabla por modelo + cobertura de proveniencia del costo + totales. Sin
dependencias nuevas; sin tocar el write path del registro (014-001/020-001).

Decisiones lockeadas:

- **D1.** Handler nuevo en `internal/proxy/metricssummary.go` (mismo package;
  precedentes `embeddings.go`/`responses.go`/`clientconfig.go`). Ruta
  registrada como `mux.HandleFunc("GET /v1/metrics/summary",
  s.handleMetricsSummary)` en `Handler()` (`internal/proxy/proxy.go:374-379`).
  Los helpers de parse/agregación/render pueden vivir en archivos adicionales
  del MISMO package `proxy` (nunca paquete nuevo).
- **D2.** **Auth Bearer**: la ruta NO se agrega a `publicPrefixes`
  (`auth.Wrap`, `internal/auth/auth.go:94-110`) → protegida por default,
  consistente con `/v1/usage` (expone costos cross-client).
- **D3.** Path del registro llega al Server vía nuevo setter
  `SetRegistryPath(path string)` (patrón setters pre-tráfico,
  `SetRegistry` proxy.go:429-431). El valor viene de `cfg.Registry.File` en
  `main.go` wiring (`cmd/mofgw/main.go:226-235`): `main.go` llama
  `SetRegistryPath(cfg.Registry.File)` SIEMPRE (independiente de
  `registry.enabled`). Los rotados se derivan mecánicamente del mismo base:
  `<dir>/<base>.N` (convención logrotate verificada en `/home/ofap/logs/`).
  Sin knob de glob nuevo; la sección `registry:` del config NO cambia.
- **D4.** Query `date=YYYY-MM-DD` **requerido**, interpretado en **UTC** (el
  `ts` del registro es UTC, `internal/registry/registry.go:68`). El HTML
  declara explícitamente que la fecha es UTC.
- **D5.** Path no configurado (knob vacío: `registry.enabled=false` con file
  vacío, o `SetRegistryPath` nunca llamado) → **503 en runtime**, patrón
  clientconfig 016-001 (`clientconfig.go:56-59`): el endpoint es consulta
  opt-in, el arranque NO falla. El endpoint opera aunque
  `registry.enabled=false` si el path está seteado (lectura pura, no depende
  del writer).
- **D6.** Parse **streaming single-pass**: `bufio.Scanner` con `Buffer()`
  ampliado (o `bufio.Reader` equivalente), filtrado por **prefijo de `ts`**
  (primeros 10 chars == date) y filtro **estricto `type=="terminal"`** (los
  `attempt` traen `model` desde 014-001, `registry.go:51` — incluirlos
  duplicaría el conteo). Memoria O(nº de modelos), no O(bytes del archivo).
- **D7.** Lectura de rotados: activo + `.1` + `.2` … hasta que `.N` no exista
  (tope 5 archivos por request). `.gz` u otras extensiones no matchean → no
  se leen (fail-soft, sin detección).
- **D8.** Líneas históricas sin `model` (esquema pre-020-001, presente en
  producción hasta hoy) → **fila `"desconocido"` + contador visible** en la
  tabla; NO se excluyen (los costos son agregables, I2 de 020-001).
  `cost_usd_src==""` (histórico) cuenta dentro del bucket **`none`** de la
  cobertura, con contador visible de líneas históricas.
- **D9.** HTML self-contained: CSS inline, sin `<script>` ni recursos
  externos, `Content-Type: text/html; charset=utf-8`, sin dependencias nuevas
  (stdlib only; go.mod queda intacto).
- **D10.** Errores de request (400/401/503) responden en formato
  OpenAI-compatible JSON (`openAIError`, proxy.go:497-503) — solo el 200 es
  HTML.
- **D11.** Read-only absoluto: jamás escribe el registry, rotados, ni ningún
  archivo; no emite eventos al registro; sin estado entre requests (cada
  request relee los archivos del momento).

## Contrato (postcondiciones)

- **P1.** `Handler()` registra exactamente UNA ruta nueva:
  `GET /v1/metrics/summary` con pattern de método (`Go ≥1.22` stdlib) →
  cualquier otro método HTTP sobre esa ruta responde **405**. Las rutas
  existentes quedan intactas.
- **P2.** Sin Bearer válido → **401 `invalid_api_key`** en formato
  OpenAI-compatible (emitido por `auth.Wrap`; la ruta no está en
  `publicPrefixes`).
- **P3.** `date` ausente, vacío o inválido → **400 `invalid_request_error`**.
  Formato estricto `time.Parse("2006-01-02", ...)`: rechaza `"2026-9-1"`,
  `"20260901"`, `"garbage"`, timestamps con hora/TZ, y valores con espacios.
  Si el query trae múltiples `date`, se usa el primero
  (`r.URL.Query().Get`).
- **P4.** Knob del path vacío (`registry.file` sin setear) → **503
  `server_error` "registry file not configured"** en runtime, sin fail-fast
  de arranque y sin fallback silencioso (I4 clientconfig).
- **P5.** Path configurado pero archivo activo inexistente → **200 con
  reporte vacío + aviso visible en el HTML** ("archivo no encontrado"), no
  error: la ausencia de rotados es estado normal y un día sin datos es un
  resultado válido, no una falla de config.
- **P6.** Solo eventos con `type=="terminal"` agregan. Un fixture con líneas
  `attempt` del mismo día produce cero contribución de esas líneas a
  cualquier contador.
- **P7.** Solo eventos cuya fecha UTC (prefijo `ts[:10]`) == `date`
  participan. Leyendo activo + hasta 4 rotados, cada evento del día se
  cuenta exactamente una vez (los archivos son disjuntos por rotación; el
  filtro ts excluye los días vecinos que comparten archivo).
- **P8.** Rotados: se leen `<dir>/<base>` y `<dir>/<base>.N` para N=1,2,…
  hasta que el `.N` siguiente no exista (máx 4 rotados = 5 archivos). Un
  `.gz` adyacente NO se lee y NO produce error ni línea corrupta contada.
- **P9.** Agregado **por modelo** (solo terminales del día): `requests`
  (total), `success`, `error` (por `outcome`), `tokens.prompt`,
  `tokens.completion`, `tokens.cache`, `tokens.reasoning` (sumas),
  `cost_usd` (suma — siempre numérico agregable, I2 de 020-001; NUNCA se suma
  `cost_usd_up`), `cost_usd_up_presente` (conteo no-null; `0.0` legítimo
  cuenta como presente, P3 de 020-001), `cost_usd_up_null` (conteo null).
- **P10.** **Cobertura de proveniencia** del día: conteo por
  `cost_usd_src` ∈ {`upstream`, `table`, `none`} con `""` contado como
  `none` + contador visible de líneas históricas (`""`), + % de cobertura
  (`(upstream+table)/total_terminales_día`) y **totales del día** (sumas de
  todas las filas: requests, tokens por tipo, cost_usd).
- **P11.** `model==""` produce una fila etiquetada **`desconocido`** en la
  tabla (no excluida) con sus agregados completos y contador visible.
- **P12.** Líneas corruptas (JSON inválido, objeto sin `type`, terminal con
  `ts` no comparable a fecha) → **skip + contador `lineas_corruptas`** visible
  en el HTML; el response NUNCA se aborta por contenido corrupto; una línea
  corrupta de cualquier longitud (incl. > buffer default de Scanner, 64 KiB)
  es descartada y el parse continúa con las líneas siguientes.
- **P13.** El parse es streaming: ningún archivo se carga completo en
  memoria (lectura línea a línea con buffer acotado); la agregación vive en
  estructuras O(nº de modelos distintos).
- **P14.** Éxito → **200** con `Content-Type: text/html; charset=utf-8`,
  documento HTML self-contained (CSS inline, cero `<script src>`, cero
  `<link>`, cero referencias externas) que contiene: (a) encabezado con la
  fecha consultada **declarada en UTC**, (b) tabla por modelo con los
  agregados de P9, (c) sección de cobertura de proveniencia y totales de P10,
  (d) contadores de líneas corruptas y filas desconocidas de P11/P12.
- **P15.** Read-only: tras un request, los bytes del archivo activo y de
  cada rotado leído son idénticos byte a byte; el endpoint no crea archivos.
- **P16.** Fidelidad exacta: dado cualquier fixture JSONL, cada valor
  mostrado en el HTML es función determinística del contenido del día
  (los tests pueden escribir fixture → request → asertar agregados). Dos
  requests consecutivos sobre datos inmutables producen el mismo HTML
  byte a byte.
- **P17.** Sin dependencias nuevas: `go.mod`/`go.sum` idénticos; la feature
  compila y corre con stdlib únicamente. El esquema del registro (writer y
  eventos, `internal/registry/registry.go:45-80`) NO cambia.

## Invariantes

- **I1.** Write path del registro intacto: `internal/registry/` no se
  modifica; los call-sites de emisión (`emitTerminalSuccess`/
  `emitTerminalError`/attempt del router) quedan byte a byte iguales.
- **I2.** Read-only: el endpoint jamás abre archivos en modo escritura, ni
  emite al registry, ni muta contadores de `/metrics`.
- **I3.** Sin dependencias nuevas (stdlib only; `go.mod` sin diffs).
- **I4.** Cero impacto en el server: `publicPrefixes` sin cambios; exactamente
  una ruta agregada al mux; la sección `registry:` del config conserva su
  schema (`enabled`, `file` — `config.go:261-264`); `SetRegistryPath` es
  pre-tráfico e inmutable después (patrón `SetRegistry`).
- **I5.** Sin cache ni estado entre requests: el reporte refleja el estado de
  disco al momento de cada request.
- **I6.** Tolerancia total a datos: ningún contenido del archivo (corrupto,
  vacío, gigante, de otros días) puede colgar, abortar o envenenar la
  respuesta.

## Criterios de aceptación

RED: **por compilación** — los tests RED referencian `SetRegistryPath` y la
ruta nueva (API inexistente) y no compilan hasta GREEN (precedente:
`internal/config/registry_red_test.go` de 014-001). Las aserciones de
comportamiento también fallan hoy (404 en la ruta).

| #   | Criterio                                                                                                                                                                                                                                                                                                                                                                                                                  | Test (internal/proxy)                                                                 |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| C1  | Sin regresión: suite completa verde                                                                                                                                                                                                                                                                                                                                                                                       | `go test ./... -race` (baseline vigente en HEAD — verificar número al AUDIT) |
| C2  | Fidelidad fixture (P16, maestro): fixture en `t.TempDir()` con success upstream (cost_up presente, 0.0 incluido), success table, success none, error (tokens 0), attempt del día (ignorado), líneas de OTRO día en activo y en `.1` (filtro ts), línea histórica sin model, línea corrupta y corrupta gigante (>64 KiB) seguida de línea válida → HTML con TODOS los agregados exactos, contador corruptas y fila desconocido | `e2e_020002_test.go Test020002_FixtureFidelity`                                         |
| C3  | `date` ausente/inválido → 400 formato OpenAI                                                                                                                                                                                                                                                                                                                                                                                | `Test020002_DateValidation`                                                             |
| C4  | Sin auth → 401 `invalid_api_key`                                                                                                                                                                                                                                                                                                                                                                                            | `Test020002_AuthRequired`                                                               |
| C5  | Knob vacío → 503 runtime (arranque no falla)                                                                                                                                                                                                                                                                                                                                                                              | `Test020002_NotConfigured503`                                                           |
| C6  | Rotados: activo + `.1`/`.2` con líneas del día y de otros días → conteo exacto, cero doble conteo; `.gz` ignorado                                                                                                                                                                                                                                                                                                               | `Test020002_RotatedFiles`                                                               |
| C7  | Corruptas no abortan: response 200, contador > 0, líneas válidas posteriores contadas                                                                                                                                                                                                                                                                                                                                     | `Test020002_CorruptLines`                                                               |
| C8  | Read-only: bytes del fixture inalterados post-request                                                                                                                                                                                                                                                                                                                                                                     | `Test020002_ReadOnly`                                                                   |
| C9  | HTML shape: Content-Type `text/html; charset=utf-8`, sin `src=`/`href=` externos, fecha UTC declarada en el documento                                                                                                                                                                                                                                                                                                           | `Test020002_HTMLShape`                                                                  |
| C10 | Archivo activo inexistente (path seteado) → 200 vacío + aviso visible                                                                                                                                                                                                                                                                                                                                                     | `Test020002_MissingFileSoft`                                                            |

El fixture de C2 se escribe con la MISMA serialización del writer
(`json.Marshal` del struct + `\n`, `registry.go:119-132`), incluyendo líneas
con y sin los campos de 020-001 (mezcla de esquemas, P6 de 020-001).

## Fuera de alcance

- Rotación de logs (la hace logrotate) y compresión `.gz` (fail-soft: no se
  leen, sin detección ni warning).
- Ingestión a Odoo/DB (fase 2 del epic, epic aparte).
- Dimensiones sesión/proyecto/cliente en el reporte (la tabla es por modelo;
  `client` no se agrega como dimensión de fila).
- Cambio de schema del registro (cerrado por 020-001: `model`,
  `cost_usd_src`, `cost_usd_up` ya existen).
- Autenticación distinta a Bearer; any formato no-HTML (JSON/Prometheus API
  paralela) del mismo agregado.
- Cualquier escritura: el endpoint no persiste agregados ni cachea.

---

Status: **Draft** — pendiente aprobación HITL (Pablo/Ofap)
Status: **Approved** by Pablo/Ofap (HITL — owner técnico mofgw, chat orquestador) on 2026-09-17
