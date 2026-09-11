---
id: 019-001-fetch-modelsdev
title: Fetch + cache en disco del catálogo models.dev (paquete internal/modelsdev)
status: approved
epic: 019-provider-sync-automation
date: 2026-09-11
created: 2026-09-11
motivacion_externa: "Epic 019: mofgw mantiene config.yaml a mano; opencode resuelve lo mismo con fetch+cache+lock+TTL+digest (models.dev/api.json). Lección PR #44282: skip de writes byte-idénticos."
---

# 019-001 — Fetch + cache en disco del catálogo models.dev

## Descripción

- **D1 — Fuente upstream y shape verificado.** El catálogo vive en `GET https://models.dev/api.json`, público y sin auth. Payload real verificado por fetch (2026-09-11): **4.59 MB, 213 providers, 7.711 modelos**. Shape: `map[provider_id] → {id, name, api, npm, doc, env[], models: map[model_id] → {id, name, description, family, attachment, reasoning, reasoning_options[], tool_call, structured_output, temperature, release_date, last_updated, modalities{input[],output[]}, limit{context,output,input?}, cost{input,output,cache_read,cache_write?}, ...}}`. Schema **variable**: `cost.tiers`, `cost.context_over_200k`, `cost.input_audio/output_audio/reasoning` y `limit.input` aparecen solo en algunos modelos (evidencia en Discovery). `supported_parameters` y `variants` tienen **0 ocurrencias** en el payload real (contradice `skills/mofgw-provider-sync/SKILL.md:56`): NO se modelan en 001; su derivación es problema de 019-003.
- **D2 — Forma del entregable (LOCKEADA).** Esta feature entrega el **paquete `internal/modelsdev` + su suite de tests**. El binario `cmd/mofgw-sync` NO se construye acá: su wiring llega en **019-004** (que lo necesita para consumir el paquete). Por tanto: cero cambios en `cmd/`, `internal/config`, `internal/proxy` — el servidor mofgw queda intocado (verificado en Discovery: `/v1/models` se arma desde `config.yaml` vía `SetPricing`/`SetModelMetadata`, main.go:244-258).
- **D3 — Paquete y API a congelar (LOCKEADA).** Paquete `internal/modelsdev`. Contrato de firmas (los tests RED congelan esto):
  ```go
  const FetchTimeout = 10 * time.Second          // tope por intento HTTP
  const DefaultCacheTTL = 5 * time.Minute        // frescura del cache
  var ErrFetchDisabled, ErrLockBusy error        // errores tipados

  type Catalog struct{ Providers map[string]Provider } // tipado, ver P15
  func Fetch(ctx context.Context, opts ...Option) (*Catalog, []byte, error) // E1: opts variádico (resolución test-audit 2026-09-11, fiel a D3)
  func ParseCatalog(raw []byte) (*Catalog, error)

  type Store struct{ Path string; TTL time.Duration; Lock bool /* + campos privados */ }
  func NewStore(path string, ttl time.Duration, lock bool, opts ...Option) *Store
  func (s *Store) Refresh(ctx context.Context, force bool) (changed bool, err error)
  func (s *Store) Get() (*Catalog, error)
  ```
  Opciones de test/inyección (precedente `provider.NewClient(..., hc *http.Client, ...)`, provider.go:267): `WithBaseURL(url string)` (apunta Fetch al upstream fake) y `WithClient(hc *http.Client)` y `WithLogger(*slog.Logger)` (precedente `provider.WithLogger`, provider.go:258; nil → `slog.Default()`).
- **D4 — Fail-soft en runtime (LOCKEADA).** Fetch fallido con cache presente → se sirve el cache stale (aunque esté vencido) + warn; `Get()` NUNCA hace red y NUNCA falla por red. Error surfaced solo cuando **no hay cache Y no hay fetch posible** (fail-loud). Precedente del repo: state corrupto no bloquea el arranque (main.go:183-189, "log + seguir").
- **D5 — Knob de deshabilitación (LOCKEADA, decisión tomada).** `MOFGW_DISABLE_MODELS_FETCH=1` se chequea en `Fetch`: **devuelve el error tipado `ErrFetchDisabled` sin emitir ningún request** (no un fetch silencioso omitido). Justificación: un knob de disable NO es un fallo transitorio; devolverlo como error distinguido permite al consumidor (019-004) tratarlo como estado esperado (exit 0 + "fetch disabled" en su log) sin confundirlo con un fallo de red. `Refresh` con el knob activo: si el cache existe lo sirve (`changed=false, err=nil`, fail-soft); si no existe → `ErrFetchDisabled`. El flag `--no-fetch` queda para el binario de 019-004.
- **D6 — Digest sha256 de skip (LOCKEADA).** Digest del body crudo en **sidecar `<cache-path>.sha256`** (hex, 64 chars). El cache queda byte-fiel al upstream (nada embebido que desvíe del upstream). Body byte-idéntico al digest guardado → NO reescribir cache, NO reescribir sidecar, `changed=false`, log `skipped_identical`. Primer write exitoso (sin sidecar previo) → escribir cache + sidecar, `changed=true`.
- **D7 — Logs (LOCKEADA).** slog estructurado, eventos: `fetch_ok{bytes, duration}`, `fetch_failed{attempt, err}`, `cache_hit{stale bool}`, `skipped_identical{sha256}`, `lock_busy{path}`. Sin secretos (no hay: la URL es pública sin auth).
- **D8 — Cache, TTL, lock (LOCKEADAS).** Ubicación: `os.UserCacheDir()/mofgw/models-dev.json` (override por env `MOFGW_CACHE_DIR`: path base del directorio, el paquete crea `<dir>/models-dev.json`; el path exacto también es seteable directo vía `NewStore` para tests). TTL default 5m; staleness por **mtime** del archivo cache. Lock: `syscall.Flock` con `LOCK_EX|LOCK_NB` sobre archivo dedicado `<cache-path>.lock` (creado si falta); si está tomado por otro proceso → error rápido claro (`ErrLockBusy` envuelto), sin espera indefinida. El lock se libera al terminar Refresh (y al salir el proceso, por el SO).
- **D9 — HTTP.** Todo request del paquete envía `User-Agent: build.UserAgent` (`mofgw/<version>`, internal/build/build.go:21-25). Timeout 10s por intento (context deadline interno de Fetch, sobre el ctx del llamador). Retry transitorio: exactamente **2 intentos totales** (1 reintento, backoff ~500ms) ante errores clasificados transitorios (network / timeout / 5xx / conn refused); 4xx NO reintenta. Error resultante clasificado a la manera `ErrUpstream` de provider.go (network/timeout).
- **D10 — Escritura atómica (patrón existente).** Replicar `internal/metrics/persist.go:197-227`: `os.MkdirAll(dir, 0o750)` → `os.CreateTemp(dir, base+".tmp-*")` → write → `Sync()` → `Close()` → `os.Rename` → `defer os.Remove(tmpName)`. Nada de truncate-in-place.

## Contrato (postcondiciones)

- **P1 — Get sirve el cache sin red.** Con un cache válido previamente escrito en `<path>` (por Refresh previo o fixture), `Get()` devuelve el catálogo tipado con los datos del fixture (providers/models/cost/limit/modalities correctos por clave) y NO emite ningún request HTTP.
- **P2 — Get con cache stale también sirve.** Con mtime del cache anterior a TTL, `Get()` devuelve el catálogo igual (la frescura no bloquea la lectura); la semántica stale es decisión del llamador.
- **P3 — Fetch devuelve catálogo + raw + UA.** Contra un upstream fake (httptest) que responde 200 con un JSON válido: `Fetch(ctx)` devuelve catálogo parseado, `raw` byte-idéntico al body servido, y el request recibido lleva `User-Agent: mofgw/<build.Version>` (nunca `Go-http-client`).
- **P4 — Timeout + retry transitorio.** Upstream que tarda >10s → error de tipo timeout tras un único intento (sin retry por timeout: un intento colgado ya consumió el presupuesto). Upstream que responde 500 en el 1er request y 200 en el 2º → Fetch tiene éxito (2 intentos, backoff ~500ms). Upstream que responde 404/403 → error inmediato SIN reintento (no transitorio). En todos los casos, a los 2 intentos fallidos → error descriptivo.
- **P5 — Refresh respeta TTL.** Cache con mtime fresco (dentro de TTL) + `Refresh(ctx, force=false)` → NO fetch (contador de hits del fake = 0), `changed=false, err=nil`.
- **P6 — Refresh re-fetch stale.** Cache con mtime > TTL + body upstream distinto al cache → fetch, reescritura de cache y sidecar, `changed=true`, nuevo mtime.
- **P7 — force=true fuerza fetch.** Cache fresco + `Refresh(ctx, force=true)` → fetch igualmente (hits=1); si el body es distinto → changed=true + reescritura; si es idéntico → aplica P8.
- **P8 — Digest skip.** Fetch cuyo body es byte-idéntico al digest del sidecar → NO reescribe el cache, NO reescribe el sidecar (ambos byte-idénticos a antes), `changed=false`, log `skipped_identical`.
- **P9 — Lock exclusivo no bloqueante.** Con `Lock=true`, mientras otro proceso/llamada sostiene el flock del `<path>.lock`: un segundo `Refresh` falla rápido con error claro (`ErrLockBusy` envuelto), NO espera indefinida, NO fetch, y el cache queda byte-intacto.
- **P10 — Lock=false no lockea.** Con `Lock=false`, Refresh funciona sin crear/adquirir lock (semántica para tests y consumo single-writer).
- **P11 — Fail-soft con cache.** Fetch fallido (upstream 500 x2 / timeout / conn refused) con cache presente en disco → `Refresh` devuelve `changed=false, err=nil` + log `fetch_failed`; el cache NO se toca (byte-intacto) y `Get()` sigue sirviendo el stale.
- **P12 — Fail-loud sin cache.** Fetch fallido SIN cache en disco → `Refresh` devuelve error (no nil), `changed=false`, y no se crea ningún archivo de cache.
- **P13 — Knob disable.** Con `MOFGW_DISABLE_MODELS_FETCH=1`: `Fetch` devuelve `ErrFetchDisabled` sin ningún request; `Refresh(force)` con cache presente → `(false, nil)` (sirve el cache); sin cache → `ErrFetchDisabled`. Sin la env var (o vacía/0) → comportamiento normal.
- **P14 — Parseo tolerante.** Un fixture con modelos que omiten campos (sin `limit.input`, sin `cost.cache_write`, con `cost.tiers`, con `cost.input_audio`, `reasoning_options` vacío) parsea sin error: campos ausentes → zero-value, campos desconocidos → ignorados (sin error), providers sin `models` → válidos (mapa vacío).
- **P15 — Catálogo tipado fiel.** Para cada entry del fixture: key del mapa = id del provider/modelo; `cost.input/output/cache_read/cache_write`, `limit.context/output/input`, `modalities.input/output`, `tool_call`, `reasoning`, `attachment`, `name`, `api`, `npm` se exponen tal cual (I2: sin normalización semántica).
- **P16 — Escritura atómica y directory-creation.** Tras cualquier reescritura: el archivo final es JSON parseable completo (nunca truncado); no quedan `.tmp-*` huérfanos en el directorio; si el directorio del cache no existía, se crea (`MkdirAll`, permisos 0o750).
- **P17 — Logs estructurados.** Los eventos D7 se emiten con los campos exactos nombrados (vía handler slog capturador en tests).

## Invariantes

- **I1 — Sin impacto en el servidor.** El paquete no importa `internal/config`, `internal/proxy`, `internal/router` ni `cmd/*`; el servidor mofgw no cambia ni un byte (I-D2).
- **I2 — El upstream manda.** El paquete solo parsea y persiste: no filtra IDs, no mapea a providers de mofgw, no deriva `supported_parameters`, no normaliza precios (eso es 019-003).
- **I3 — Escrituras siempre atómicas.** Todo write al cache pasa por temp+rename; jamás truncate/sobrescritura in-place (I-D10).
- **I4 — Digest antes de escribir.** Ninguna reescritura del cache ocurre sin haber comparado el digest; body idéntico jamás reescribe ni republisha (I-D6).
- **I5 — Get es read-only y offline.** `Get()` nunca abre sockets ni intenta fetch; única fuente: el archivo de cache.
- **I6 — Fallas no corrompen.** Cualquier fallo (lock, red, parseo del body) deja el cache anterior byte-intacto y el sidecar consistente.
- **I7 — Sin auth ni secretos.** El request es GET anónimo con solo User-Agent; ningún api_key/credencial viaja (la fuente no lo exige).
- **I8 — Knob default = habilitado.** Sin `MOFGW_DISABLE_MODELS_FETCH`, comportamiento fetch-on; la env es la única deshabilitación en 001 (I-D5).
- **I9 — Tolerancia por construcción.** Cambios aditivos del schema upstream (campos nuevos) no rompen el parseo; solo campos conocidos se tipan.
- **I10 — Un lock file dedicado.** El flock es sobre `<path>.lock`, nunca sobre el propio cache (el rename del cache invalidaría locks sobre él y bloquearía lectores Get).

## Criterios de aceptación (con mapeo test)

| #   | Criterio                                                                                                       | Test                                                                  |
| --- | -------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| C1  | P1/P2: `Get()` sirve cache fresco y stale sin red (fake upstream con contador de hits en 0)                      | `TestGet_ServesCacheWithoutNetwork`, `TestGet_ServesStaleCache`           |
| C2  | P3: Fetch → catálogo + raw byte-idéntico + `User-Agent: mofgw/<version>` capturado por el fake                   | `TestFetch_CatalogRawAndUserAgent`                                      |
| C3  | P4: timeout>10s → 1 intento; 500→200 → 2 intentos, ok; 404 → 1 intento con error                               | `TestFetch_Timeout`, `TestFetch_RetryTransient`, `TestFetch_NoRetryOn4xx`   |
| C4  | P5/P6/P7: fresh no fetch; stale fetch+changed; force fetch                                                     | `TestRefresh_FreshSkips`, `TestRefresh_StaleRefetches`, `TestRefresh_Force` |
| C5  | P8: body idéntico → cache y sidecar byte-idénticos, changed=false                                              | `TestRefresh_SkipsIdenticalBody`                                        |
| C6  | P9/P10: lock busy falla rápido sin corromper; Lock=false funciona                                              | `TestRefresh_LockBusyFailsFast`, `TestRefresh_NoLockMode`                 |
| C7  | P11/P12: fetch fallido con cache → (false,nil)+stale servido; sin cache → error                                | `TestRefresh_FailSoftWithCache`, `TestRefresh_FailLoudWithoutCache`       |
| C8  | P13: knob disable → ErrFetchDisabled sin request; con/sin cache en Refresh                                     | `TestFetchDisabled_Knob`                                                |
| C9  | P14/P15: fixture tolerante (schema variable real) parsea con zero-values y sin error por desconocidos          | `TestParseCatalog_TolerantShape`, `TestParseCatalog_FaithfulFields`       |
| C10 | P16: atomic write (JSON válido final, sin .tmp-* residuales, mkdir del padre)                                  | `TestRefresh_AtomicWrite`                                               |
| C11 | P17: eventos de log con campos exactos (handler capturador)                                                    | `TestLogEvents`                                                         |
| C12 | Suite completa verde: `go test ./... -count=1 -race` (baseline 733/29 + los del paquete), `go vet` y `gofmt` limpios | suite + GREEN                                                         |

**Naturaleza del RED:** paquete `internal/modelsdev` no existe → los tests RED fallan por compilación (`undefined: modelsdev.NewStore` etc.) — RED especial de compilación, precedente 011-005/015-001. Todos los tests de comportamiento sobre la API pública del paquete con fake upstream `httptest.Server` (contador de hits para P5/P7/P8); NINGÚN test sobre estructura interna ni mocks sobre plumbing. `MOFGW_DISABLE_MODELS_FETCH` se controla por test con `t.Setenv`.

## Fuera de alcance

- **019-002:** fetch de listas Zen/Go/OpenRouter.
- **019-003:** merge/mapeo a providers[].models/pricing/model_metadata; derivación de `supported_parameters` (no viene en el payload — ver D1).
- **019-004:** escritura atómica de config.yaml, validación `config.Parse`, construcción del binario `cmd/mofgw-sync` y su flag `--no-fetch`.
- **019-005 / 019-006:** reload, systemd timer, modo `--once`.
- **019-007:** snapshot embebido en build (fallback offline). El schema del catálogo tipado de esta feature será su contrato.
- Cualquier cambio en `cmd/mofgw/main.go`, `internal/config`, `internal/proxy`, `/v1/models` del servidor (I1).
- Subcomando `mofgw -check-config` (aspiracional en SKILL.md:72, no implementado — deuda aparte).
- Actualización de clientes (opencode/openclaw/zot): fuera de toda la epic.

## Alternativas desestimadas

- **Lock sobre el propio archivo de cache:** el rename atómico invalida el inode bajo el lock (otro proceso lockearía el archivo nuevo/viejo según timing) y bloquearía a los lectores `Get()` → lock file dedicado (I10, decisión D8).
- **Librería `github.com/gofrs/flock`:** dependencia nueva contra un `go.mod` stdlib-only (solo `yaml.v3`); `syscall.Flock(LOCK_EX|LOCK_NB)` cubre el caso Linux-only del repo (sin build tags Windows en todo el árbol).
- **Subcomando `mofgw sync`:** mezcla flags del servidor (`-config/-verbose/-log-file`) con los del sync y acopla el timer (019-006) al runtime del proxy; binario separado LOCKEADO (D2).
- **Endpoint admin in-server:** contraria a la decisión de epic (sync desacoplado, corre desde cron); el servidor permanece simple.
- **Digest embebido en el JSON cacheado (envelope {digest, body}):** el cache dejaría de ser byte-fiel al upstream y cada consumidor debería desenvelopar; sidecar mantiene el cache idéntico al payload y el digest trivialmente comparable (D6).
- **Fail-loud siempre (error ante fetch fallido con cache):** rompería el propósito del cache (degradar con datos viejos > no operar); D4 LOCKEADA con precedente de state corrupto en main.go:183-189.
- **Parseo rígido con validación de schema completo:** el schema real es variable por modelo (tiers/context_over_200k/input_audio solo en algunos; 0 soporte para `variants`) → rompería en el primer cambio upstream; parseo tolerante es el contrato (P14/I9).

## Contexto técnico verificado (Discovery 2026-09-11)

- **Escritura atómica existente (patrón a replicar):** `internal/metrics/persist.go:197-227` — `SaveState`: `MkdirAll(dir, 0o750)` + `os.CreateTemp(dir, filepath.Base(path)+".tmp-*")` + write + `Sync()` + `Close()` + `os.Rename` + `defer os.Remove(tmpName)`. Test anti-.tmp-residual: persist_test.go:242.
- **User-Agent:** `internal/build/build.go:21-25` — `Version` var (ldflags overridable) + `UserAgent = "mofgw/" + Version`; ya exigido upstream por 018-001 P5 (provider.go:412).
- **Cliente HTTP upstream (precedente de clasificación de errores):** `internal/provider/provider.go:267-270` (`NewClient`, default `http.Client{Timeout: 300s}`, inyección de hc como hook de test) y `:302-306` (tipos `timeout`/`network`); `ErrUpstream` en provider.go:186-206.
- **Logging:** `internal/logging/logging.go:105` `Build(level, logFile, stdoutWriter)` (reutilizable por el binario en 019-004); en el paquete 001 se inyecta `*slog.Logger` vía `WithLogger` (precedente provider.go:258).
- **Precedentes de estilo cache/coalescing (in-memory, no reutilizables directo):** `internal/respcache/respcache.go:57-71` (defaults en constructor: maxEntries 512, TTL 5m) y `internal/singleflight/singleflight.go:64-69` (defaults en constructor). El TTL default 5m del paquete (D8) alinea con ambos y con el plan de epic.
- **Precedencia config (solo referencia, no se usa en 001):** `internal/config/config.go:486-505` `Load()`: flag > `~/.config/mofgw/config.yaml` > `/etc/mofgw/config.yaml`.
- **Convención de paths del repo:** `~/.config/mofgw/` para config y sesiones (config.go:8-9, 179); sin uso de `os.UserCacheDir` previo — esta feature lo introduce para el cache (D8, LOCKEADO).
- **flock:** no existe primitiva previa en el repo (grep 0 matches) → a construir con `syscall.Flock` stdlib.
- **Payload real verificado:** 4.59 MB / 213 providers / 7.711 modelos; provider keys `{api, doc, env, id, models, name, npm}`; model keys completas en D1; `supported_parameters`: 0; `variants`: 0.
- **Baseline:** 733 tests / 29 pkgs `-race` (plan de epic); go.mod: `module github.com/ofapsaas/mofgw`, go 1.24.4, única dependencia `gopkg.in/yaml.v3`.

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11
