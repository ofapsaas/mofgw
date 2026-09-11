---
id: 019-002-fetch-zen-go
title: Fetch de listas de acceso (opencode Zen/Go) + catálogo OpenRouter sobre motor genérico modelscache
status: approved
epic: 019-provider-sync-automation
date: 2026-09-11
created: 2026-09-11
motivacion_externa: "Epic 019: 019-003 necesita los IDs autorizados de Zen/Go (qué modelos puede usar cada provider) y el catálogo OpenRouter (pricing/limits soportados); hoy se obtienen a mano con el workflow del skill mofgw-provider-sync."
---

# 019-002 — Fetch de listas de acceso Zen/Go + catálogo OpenRouter (motor genérico `modelscache`)

## Descripción

- **D1 — Extracción del motor (LOCKEADA).** El mecanismo de fetch+cache+lock+digest+TTL de `internal/modelsdev` (019-001, ADR-011) se **generaliza** en un paquete nuevo `internal/modelscache`, extraído por **refactor mecánico del código mergeado de 001**: loop de intentos/backoff/cancelación (`fetchWith`), clasificación `FetchError{Attempt,Type,Status}` + `classifyTransport`, política retry (HITL B1: timeout NUNCA reintenta; solo network/5xx), Store (flock `LOCK_EX|LOCK_NB` file dedicado, digest sidecar con commit sidecar-primero/cache-último HITL B2, TTL por mtime, fail-soft/loud, escritura atómica temp+rename), knobs y hooks `WithBaseURL/WithClient/WithLogger`. El motor es parametrizable por `modelscache.Spec{BaseURL string; Parse func([]byte) (any, error); AuthEnv string; KnobEnv string; Source string}`. `modelsdev` queda como **facade**: delega en `modelscache` vía **aliases de tipo** (`type Option = modelscache.Option`, `type FetchError = modelscache.FetchError`, `type Store = modelscache.Store[*Catalog]`) conservando su API pública **bit a bit** y TODAS sus postcondiciones. **GATE INAMOVIBLE:** la suite completa actual (baseline 763/30 `-race`) verde con `internal/modelsdev/modelsdev_test.go` **byte-intacto** (git diff vacío).
- **D2 — Tipos de las fuentes (LOCKEADA).** Shape zen/go verificado (70 y 37 modelos): `ModelList{Object string; Models []ModelRef}` con `ModelRef{ID string; OwnedBy string; Created int64}` (OpenAI-list `{object, data[]}`, items `{id, object:"model", created, owned_by}`). Shape OpenRouter verificado (443 modelos, 733 KB): `OpenRouterCatalog{Models []OpenRouterModel}` con modelado tolerante value-semantics (precedente 019-001): `ID, CanonicalSlug, Name string`, `ContextLength int`, `Pricing{Prompt, Completion, CacheRead, CacheWrite float64}` (upstream sirve **strings**; string→float64 tolerante, inválido/ausente→0), `SupportedParameters []string` (presente en 443/443 verificados), `Architecture{Modality string; InputModalities, OutputModalities []string}` (la `modality` NO es top-level: vive en `architecture`, verificado), `TopProvider{ContextLength, MaxCompletionTokens int}`. **NO se tipan:** `benchmarks`, `per_request_limits`, `pricing.overrides`, `default_parameters`, `hugging_face_id`, `knowledge_cutoff`, `expiration_date`, `links` (schema variable; 019-003 puede re-parsear del raw). IDs **tal cual upstream** — prefijos de provider (`deepseek/deepseek-v4-flash`) y aliases `~` (`~z-ai/glm-latest`, 16 verificados) incluidos; el filtrado/alias-resolution es de 019-003 (I2).
- **D3 — URLs como consts exportadas.** `ZenURL = "https://opencode.ai/zen/v1/models"`, `GoURL = "https://opencode.ai/zen/go/v1/models"`, `OpenRouterURL = "https://openrouter.ai/api/v1/models"` (precedente `defaultBaseURL`, fetch.go:41). El hook de test `WithBaseURL` del motor apunta al upstream fake (precedente 019-001 tests:159).
- **D4 — Auth opcional OpenRouter (LOCKEADA).** Catálogo anónimo verificado completo (HTTP 200, 733 KB — Bearer del skill es desactivable): `OPENROUTER_API_KEY` seteada y no vacía → `Authorization: Bearer <valor>`; env ausente/vacía → GET anónimo (solo UA `mofgw/<version>`, I7). La env se lee **POR LLAMADA** (precedente `fetchDisabled()`, fetch.go:231-234). El valor jamás se loguea ni persiste. Zen/Go: sin auth (verificado 200 anónimo).
- **D5 — Cache separado por fuente (LOCKEADA).** Un archivo cache por fuente con lock+sidecar propios (patrón ADR-011 ×3): `<base>/zen-models.json`, `<base>/go-models.json`, `<base>/openrouter-models.json`. Regla de base idéntica a 001 (D8 de 001): `MOFGW_CACHE_DIR` > `os.UserCacheDir()/mofgw` > `os.TempDir()/mofgw`. El motor generaliza `DefaultCachePath(filename string)`; `modelsdev.DefaultCachePath()` conserva su firma `string()` (wrapper sobre `modelscache.DefaultCachePath("models-dev.json")`).
- **D6 — Knob de las fuentes (LOCKEADA).** `MOFGW_DISABLE_UPSTREAM_FETCH=1` (const `KnobDisableUpstreamFetch`), semántica P13 de 001: `Fetch*` → error envolviendo `ErrFetchDisabled` sin emitir request; `Refresh` con cache presente → `(false, nil)`; sin cache → error envolviendo `ErrFetchDisabled`. El knob de modelsdev (`MOFGW_DISABLE_MODELS_FETCH`) queda intacto y NO afecta a las fuentes nuevas (ni viceversa): cada Spec lleva su `KnobEnv`.
- **D7 — TTL y clases de mecanismo (LOCKEADA).** `DefaultCacheTTL = 5m` por fuente (mtime). Las tres fuentes comparten TODAS las clases de comportamiento del motor (frescura/stale/force, lock busy, digest skip, fail-soft/loud, atomic, knobs) — las postcondiciones-mecanismo de 001 ya están suiteadas; 002 las ejerce por wiring, NO las re-testea exhaustivamente por fuente.
- **D8 — API a congelar (LOCKEADA, decisiones del architect).** Empaquetado: **motor en `internal/modelscache`** + **fuentes en un paquete único `internal/upstream`** con 3 archivos (`zen.go`, `go.go`, `openrouter.go`). Forma stdlib-simple: **generics de Go 1.24.4** (`Store[T any]`, `Fetch[T any]`) — typed end-to-end sin wrappers por fuente. Firmas a congelar (los tests RED fijan esto):
  ```go
  // internal/modelscache (motor genérico, extraído de 001)
  type Spec struct {
      BaseURL string                      // upstream (default vacío → obligatorio por fuente)
      Parse   func([]byte) (any, error)   // parser de la fuente (tolerante)
      AuthEnv string                      // env de API key; "" = sin auth
      KnobEnv string                      // env de deshabilitación
      Source  string                      // atributo "source" de logs; "" = sin atributo (modelsdev)
  }
  type Option func(*Options)   // WithBaseURL/WithClient/WithLogger (ya genéricos)
  type FetchError struct { /* Attempt, Type, Status, Message — idéntico 001 */ }
  var ErrFetchDisabled, ErrLockBusy error
  func Fetch[T any](ctx context.Context, spec Spec, opts ...Option) (T, []byte, error)
  func DefaultCachePath(filename string) string
  type Store[T any] struct { Path string; TTL time.Duration; Lock bool /* + spec/opts privados */ }
  func NewStore[T any](spec Spec, path string, ttl time.Duration, lock bool, opts ...Option) *Store[T]
  func (s *Store[T]) Refresh(ctx context.Context, force bool) (bool, error)
  func (s *Store[T]) Get() (T, error)

  // internal/upstream (fuentes 002; paquete único, 3 archivos)
  const ZenURL, GoURL, OpenRouterURL string
  const KnobDisableUpstreamFetch = "MOFGW_DISABLE_UPSTREAM_FETCH"
  var ErrFetchDisabled, ErrLockBusy error // = modelscache.* (misma identidad, errors.Is)
  func DefaultZenCachePath() string          // <base>/zen-models.json
  func DefaultGoCachePath() string           // <base>/go-models.json
  func DefaultOpenRouterCachePath() string   // <base>/openrouter-models.json
  func FetchZen(ctx context.Context, opts ...modelscache.Option) (ModelList, []byte, error)
  func FetchGo(ctx context.Context, opts ...modelscache.Option) (ModelList, []byte, error)
  func FetchOpenRouter(ctx context.Context, opts ...modelscache.Option) (OpenRouterCatalog, []byte, error)
  func NewZenStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[ModelList]
  func NewGoStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[ModelList]
  func NewOpenRouterStore(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[OpenRouterCatalog]
  func ParseModelList(raw []byte) (ModelList, error)
  func ParseOpenRouterCatalog(raw []byte) (OpenRouterCatalog, error)
  ```
  Path vacío en `New*Store` → `Default*CachePath()`; ttl <= 0 → `modelscache.DefaultCacheTTL`.
- **D9 — Logs con fuente (LOCKEADA).** El motor agrega atributo `source` a sus eventos (`fetch_ok{bytes,duration}`, `fetch_failed{attempt,err}`, `cache_hit{stale}`, `skipped_identical{sha256}`, `lock_busy{path}`) **solo si `Spec.Source != ""`**. `modelsdev` compone su Spec con `Source: ""` → sus logs quedan byte-idénticos a los de 001 (P17 de 001 no se rompe, gate). Upstream: `source = "zen" | "go" | "openrouter"`.

## Contrato (postcondiciones)

**Fase A — Extracción del motor (gate, ya verificado por suite existente):**

- **P1 — Suite 019-001 intacta y verde.** Tras el refactor: `go test ./... -count=1 -race` completa verde con `internal/modelsdev/modelsdev_test.go` **sin ninguna modificación** (medible: diff vacío del archivo). Criterio de aceptación principal de la Fase A.
- **P2 — API pública de modelsdev preservada bit a bit.** `Fetch`, `ParseCatalog`, `Store`, `NewStore`, `Get`, `Refresh`, `DefaultCachePath`, `DefaultCacheTTL`, `FetchTimeout`, `KnobDisableModelsFetch`, `Catalog/Provider/Model/Limit/Cost/Modalities` y los errores `ErrFetchDisabled`/`ErrLockBusy`/`FetchError` compilan y se comportan idéntico; los aliases de tipo preservan identidad (`errors.As` con `*FetchError` sigue funcionando en tests existentes).
- **P3 — Motor genérico semánticamente equivalente.** `modelscache.Spec` con el spec de modelsdev compuesto (BaseURL models.dev, Parse=ParseCatalog, KnobEnv=MOFGW_DISABLE_MODELS_FETCH, AuthEnv="", Source="") reproduce el comportamiento exacto de 001: clases P4-P16 de 001 (timeout-no-retry HITL B1, lock-first, sidecar-primero HITL B2, digest skip, fail-soft/loud, atomic, knob por llamada) siguen pasando **sin cambios en los tests**.

**Fase B — Fuentes nuevas (postcondiciones específicas de 002):**

- **P4 — FetchZen fiel.** `FetchZen(ctx)` contra fake upstream 200 con `{object:"list", data:[{id, object, created, owned_by}...]}` devuelve `ModelList{Object:"list", Models[i]}` **fiel por índice**: `Models[i].ID/OwnedBy/Created` = item `i` de `data[]`; `raw` byte-idéntico al servido; `User-Agent: mofgw/<version>` presente (nunca `Go-http-client`).
- **P5 — FetchGo fiel.** Misma clase sobre `GoURL`: 37 items reales → `ModelList` fiel por índice (shape idéntico a Zen, verificado).
- **P6 — Parseo ModelList tolerante.** `data` ausente o `null` → `ModelList{Models: nil}` sin error; items con campos extra desconocidos → ignorados sin error; `object` top-level ausente → zero-value. Solo JSON inválido → error.
- **P7 — Pricing OpenRouter string→float64.** `"0.00001"` → `0.00001`; valor no numérico, vacío o campo ausente → `0` (zero-value) **sin error**; `pricing.prompt/completion/input_cache_read/input_cache_write` → `Pricing{Prompt, Completion, CacheRead, CacheWrite}` (mapeo 1:1).
- **P8 — Campos OpenRouter fieles + aliases incluidos.** `ID/CanonicalSlug/Name` tal cual; `context_length` → `ContextLength`; `supported_parameters` → `SupportedParameters` tal cual; `architecture.modality` → `Architecture.Modality`, `architecture.input_modalities/output_modalities` → `InputModalities/OutputModalities`; `top_provider.context_length/max_completion_tokens` → `TopProvider.*`; IDs con `~` incluidos **tal cual** (I2: 019-003 decide).
- **P9 — Tolerancia OpenRouter.** Items sin `pricing`/`architecture`/`top_provider`/`alias_target`/`supported_parameters` (o con `null`) → zero-values sin error; campos variables (`benchmarks`, `per_request_limits`, `overrides`, `default_parameters`, `expiration_date`, `links`) ignorados sin error. Solo JSON inválido → error.
- **P10 — Auth condicional OpenRouter.** Con `OPENROUTER_API_KEY` seteada y no vacía: el request lleva `Authorization: Bearer <valor>`. Sin env o vacía: sin header `Authorization` (anónimo, solo UA). La env se lee en CADA llamada (cambiarla entre llamadas cambia el comportamiento).
- **P11 — Secreto jamás en logs.** El valor de `OPENROUTER_API_KEY` no aparece en ningún evento de log ni en el texto de ningún error del paquete (con auth fallida 401 el error menciona status, nunca la key).
- **P12 — Knob de fuentes aislado.** `MOFGW_DISABLE_UPSTREAM_FETCH=1` → `FetchZen/FetchGo/FetchOpenRouter` devuelven error con `errors.Is(err, ErrFetchDisabled)` sin ningún request; los tres Stores: `Refresh` con cache en disco → `(false, nil)`; sin cache → error con `ErrFetchDisabled`. Cruzado: setear el knob de 002 NO cambia `modelsdev.Fetch` y setear `MOFGW_DISABLE_MODELS_FETCH` NO afecta `FetchZen/Go/OpenRouter` (cada Spec con su KnobEnv).
- **P13 — Caches separados por fuente.** `DefaultZenCachePath/DefaultGoCachePath/DefaultOpenRouterCachePath` devuelven `zen-models.json`/`go-models.json`/`openrouter-models.json` bajo la misma regla de base de 001 (`MOFGW_CACHE_DIR` > `UserCacheDir()/mofgw` > `TempDir()/mofgw`); cada Store crea lock/sidecar propios (`<path>.lock`/`<path>.sha256`); un `Refresh` de una fuente no lee, escribe ni lockea los caches de las otras (aislamiento verificable: refresh de zen con openrouter-cache corrupto en disco → sin error).
- **P14 — Logs con source.** Los eventos del motor emitidos desde los Stores/Fetch de 002 llevan atributo `source` con valor `zen`/`go`/`openrouter`; los eventos de modelsdev (Source="") NO llevan el atributo (byte-idénticos a 001, gate).
- **P15 — Wiring del Store por fuente (clases del motor, una pasada).** Para CADA fuente: `New*Store(path→fake-upstream)` + `Refresh(ctx, false)` con cache ausente → fetch+persist+`changed=true`; `Get()` devuelve el tipo tipado de la fuente; segundo `Refresh` con body idéntico → `changed=false` sin reescritura (digest skip); fetch fallido con cache → `(false, nil)` fail-soft. (Clases P5/P6/P8/P11/P16 de 001 ejercidas a través de las fuentes — sin re-test del motor en sí.)

## Invariantes

- **I1 — Sin impacto en el servidor.** `modelscache` y `upstream` no importan `internal/config`, `internal/proxy`, `internal/router` ni `cmd/*`; `modelsdev` sigue sin importarlos (su nueva dependencia es `modelscache`, paquete hermano del sync — permitida por el ADR-011: el servidor no importa nada del sync).
- **I2 — El upstream manda.** Parseo fiel por fuente: sin filtrado de IDs, sin resolución de aliases `~`, sin normalización de precios ni de vocabulario de `supported_parameters` (eso es 019-003).
- **I3 — Refactor mecánico (Fase A).** Los cambios a `internal/modelsdev` son SOLO delegación mecánica a `modelscache` (aliases + composición del Spec): cero cambios de comportamiento, cero cambios de tests.
- **I4-I11 — Clases del motor heredadas por construcción:** escrituras siempre temp+rename (I3 de 001); digest antes de escribir, body idéntico nunca reescribe (I4/I6 de 001); `Get()` read-only y sin red (I5 de 001); fallas no corrompen caches (I6 de 001); request sin secretos salvo la auth de OpenRouter (I7 de 001); knobs default-on con semántica "1" (I8 de 001); tolerancia de schema por construcción (I9 de 001); lock file dedicado, nunca el propio cache (I10 de 001). Aplican a las tres fuentes vía el motor compartido.
- **I12 — Secreto no persistido.** El valor de `OPENROUTER_API_KEY` solo vive en el header del request en vuelo: jamás en logs, caches ni sidecars.
- **I13 — Aislamiento de knobs.** Cada fuente deshabilita/autoriza por su propio Spec (`KnobEnv`/`AuthEnv`); ningún cross-effect entre modelsdev y las fuentes nuevas.

## Criterios de aceptación (con mapeo test)

| #   | Criterio                                                                                                                                                  | Test                                                                                                                                                                 |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| C1  | P1/P2/P3: gate de refactor — suite completa 019-001 (y total) verde, `modelsdev_test.go` byte-intacto, `go vet`/`gofmt` limpios                                 | `go test ./... -count=1 -race` + verificación de diff vacío en `internal/modelsdev/modelsdev_test.go` (la suite EXISTENTE es el test de P1-P3; ningún test nuevo de 001) |
| C2  | P4/P5: FetchZen/FetchGo → ModelList fiel por índice + raw + UA                                                                                            | `TestFetchZen_FaithfulList`, `TestFetchGo_FaithfulList` (fake httptest con fixture real recortado)                                                                       |
| C3  | P6: tolerancia de ModelList (data ausente/null, extras, object ausente)                                                                                   | `TestParseModelList_Tolerant`                                                                                                                                          |
| C4  | P7/P8/P9: OpenRouter — pricing string→float64 (válido/inválido/ausente), fields fieles, `~` incluidos, schema variable sin error                            | `TestParseOpenRouterCatalog_PricingStrings`, `TestParseOpenRouterCatalog_FaithfulFields`, `TestParseOpenRouterCatalog_TolerantShape`                                       |
| C5  | P10: auth condicional — con env → `Authorization: Bearer <valor>` capturado; sin env → ausente (solo UA); lectura por llamada (env cambiada entre llamadas) | `TestFetchOpenRouter_AuthConditional`                                                                                                                                  |
| C6  | P11: valor de la key ausente en todos los eventos capturados (incl. fetch_failed 401)                                                                     | `TestOpenRouter_KeyNeverLogged` (handler slog capturador)                                                                                                              |
| C7  | P12: knob de fuentes → ErrFetchDisabled sin request; Refresh con/sin cache; cross-isolation con el knob de modelsdev                                      | `TestUpstreamKnob_DisablesSources`, `TestUpstreamKnob_Isolation` (`t.Setenv`)                                                                                              |
| C8  | P13: paths default por fuente + aislamiento (refresh de una fuente no toca caches de las otras)                                                           | `TestDefaultCachePaths_PerSource`, `TestStores_Isolation`                                                                                                                |
| C9  | P14: atributo `source` en eventos upstream; ausente en eventos modelsdev                                                                                    | `TestEngineLog_SourceAttribute` (handler capturador, 4 specs)                                                                                                          |
| C10 | P15: wiring del Store por fuente (fetch+changed, digest skip, fail-soft) a través del motor                                                               | `TestZenStore_Wiring`, `TestGoStore_Wiring`, `TestOpenRouterStore_Wiring` (fake upstream + contador de hits)                                                               |
| C11 | Suite completa: `go test ./... -count=1 -race` (baseline 763/30 + paquetes nuevos), vet/gofmt limpios                                                       | suite + GREEN                                                                                                                                                        |

**Naturaleza del RED:** los paquetes `internal/modelscache` e `internal/upstream` no existen → los tests RED fallan por **compilación** (`undefined: modelscache.Fetch`, `undefined: upstream.ZenURL`, etc.) — RED especial de compilación, precedente 011-005 / 015-001 / 019-001. Todos los tests de comportamiento sobre la API pública con fake upstreams `httptest.Server` + fixtures self-contained (recortes del payload real capturado 2026-09-11: zen 70 items, go 37, openrouter subset). El fixture OpenRouter debe incluir: un item con pricing completo en strings, un item sin `architecture` y sin `pricing`, un alias `~`, un item con `overrides`/`benchmarks` (que deben ignorarse), y un `supported_parameters` no vacío. `MOFGW_DISABLE_UPSTREAM_FETCH` y `OPENROUTER_API_KEY` se controlan por test con `t.Setenv`.

## Fuera de alcance

- **019-003:** merge/mapeo de catálogos a providers[].models/pricing/model_metadata; resolución de aliases `~` de OpenRouter; filtrado por IDs autorizados; mapeo de vocabulario `supported_parameters` (OpenRouter usa `include_reasoning`/`reasoning_effort`, models.dev/opencode usa otro — decisión pendiente 003, R5); tipado de `overrides`/`benchmarks` si 003 los demanda (re-parseo del raw).
- **019-004:** binario `cmd/mofgw-sync` (construcción, flags, `--no-fetch`), escritura de config.yaml.
- **019-005 / 019-006 / 019-007:** reload, systemd timer, snapshot embebido en build.
- Cualquier cambio en `cmd/`, `internal/config`, `internal/proxy`, `/v1/models` del servidor (I1).
- Cambios de comportamiento en `internal/modelsdev` (solo refactor mecánico de delegación, I3/P1).
- Auth en Zen/Go (verificado innecesaria: 200 anónimo).

## Alternativas desestimadas

- **Duplicación controlada (paquete hermano con código copiado de modelsdev):** replicaría a mano ~250 L de lock/digest/atomic/retry que ya pasaron review externa con correcciones sutiles (B1 timeout-no-retry, B2 sidecar-primero, #5 canceled-vs-deadline); tres copias derivarán silenciosamente (R1). DESCARTADA por HITL (D1 aprobada).
- **Generalizar DENTRO de modelsdev (motor + 3 fuentes en un solo paquete):** mezclaría el contrato tipado congelado de 001 (Catalog/Provider/Model) con los tipos nuevos, engordando el paquete y acoplando los suites; además la API pública de modelsdev tendría que absorber nombres de fuentes ajenas. Facade + motor separado mantiene cada contrato limpio.
- **Cache combinado (un archivo para las 3 fuentes):** acopla dominios de fallo (OpenRouter caído bloquearía el cache de Zen), digests compartidos forzarían refetch combinado, tamaños divergen 5900×; archivos separados LOCKEADOS (D5).
- **Auth OpenRouter obligatoria (siempre Bearer):** rompe el sync en hosts sin key pese a que el catálogo anónimo está verificado completo (733 KB); auth opcional por env es estrictamente más capaz.
- **Knob único renombrado (unificar `MOFGW_DISABLE_MODELS_FETCH` y el nuevo):** rompería P13/I8 de 001 ya aprobados y suiteados; knobs por Spec aislados (D6/P12).
- **Atributo `source` incondicional en el motor:** rompería la byte-igualdad de logs de modelsdev exigida por el gate (P17 de 001 aserta campos exactos); `Source != ""` condicional (D9/P14).
- **Struct literals del Store genérico como API (sin constructor):** el suite 019-001 usa constructores (`NewStore(path, ttl, lock, opts...)`, verificado L159-161), no literales → el facade con constructor delegado es compilable sin tocar tests; un Store genérico con spec obligatoria en el literal rompería el gate.
- **Tipar `overrides`/`benchmarks`/`default_parameters` de OpenRouter:** schema variable sin demanda confirmada de 003 (R4/R5); con value-semantics + raw disponible, se agrega cuando exista demanda (tolerancia I9).

## Contexto técnico verificado (Discovery 2026-09-11)

### Empírico (fetch real, UA `mofgw/0.1.0`)
| Fuente       | Endpoint                             | Auth probada | Resultado        | Shape                                                                                                                                                                                                         |
| ------------ | ------------------------------------ | ------------ | ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| opencode Zen | `https://opencode.ai/zen/v1/models`    | anónimo      | HTTP 200, 5.9 KB | `{object, data[70]}`, items `{id, object:"model", created(unix), owned_by:"opencode"}`                                                                                                                            |
| opencode Go  | `https://opencode.ai/zen/go/v1/models` | anónimo      | HTTP 200, 3.1 KB | idéntico, `data[37]` (ej: `minimax-m3`)                                                                                                                                                                           |
| OpenRouter   | `https://openrouter.ai/api/v1/models`  | anónimo      | HTTP 200, 733 KB | `{data[443], total_count, links}` rico; `supported_parameters` en 443/443; pricing **strings**; `modality` en `architecture` (NO top-level); 16 aliases `~`; todos los modelos de la epic presentes con prefijo de provider |

### Componentes del motor de 001 (genéricos salvo 3 acoples — evidencia de D1)
- **Ya genéricos:** loop de intentos/backoff/cancelación `fetch.go:fetchWith` (L138-163); clasificación `FetchError`/`classifyTransport` (fetch.go:58-72, 194-207); política retry HITL B1 `retryable` (fetch.go:209-229: timeout NUNCA, solo network/5xx); lock flock `LOCK_EX|LOCK_NB` file dedicado `store.go:acquireLock` (L170-188); digest sidecar commit sidecar-primero HITL B2 `store.go:persist` (L147-164); escritura atómica `writeFileAtomic` (L193-219, patrón metrics/persist.go:197-227); TTL por mtime + fail-soft/loud `Refresh` (L85-115, lock-first); hooks `WithBaseURL/WithClient/WithLogger` (fetch.go:85-105); knob leído por llamada `fetchDisabled` (fetch.go:231-234).
- **3 puntos acoplados a generalizar:** `defaultBaseURL` const (fetch.go:41), `ParseCatalog` hardcodeado en `attemptFetch` (fetch.go:187), knob/knob-const + `DefaultCachePath()` filename fijo (fetch.go:45, store.go:63-73).
- **API pública de 001 a preservar (P2):** `Fetch/ParseCatalog/Store/NewStore/DefaultCachePath/DefaultCacheTTL/FetchTimeout/KnobDisableModelsFetch/ErrFetchDisabled/ErrLockBusy/FetchError/Catalog/Provider/Model/Limit/Cost/Modalities`.
- **Compatibilidad del facade verificada:** `modelsdev_test.go` usa solo constructores y options (`NewStore(path, ttl, lock, opts...)` L159-161, `Fetch(ctx, WithBaseURL, WithClient, WithLogger...)`) — ningún struct literal de `Store` → alias `type Store = modelscache.Store[*Catalog]` compila sin tocar tests.
- **Logs P17 de 001:** eventos con campos exactos (handler capturador, modelsdev_test.go:961-1018) → el atributo `source` del motor es condicional a `Spec.Source != ""` (modelsdev: "") para no romper el gate.
- **Convención de keys:** NUNCA en YAML, refs env resueltas al usar (config.go:9-10, systemPatterns.md:19; provider.go:408 patrón Bearer); skill SKILL.md:23 usa `OPENROUTER_API_KEY` como nombre — se respeta el mismo nombre de env (D4). Corrección al plan de epic: "OpenRouter ya en config" no existe en `config.example.yaml` (solo placeholders provider-a/b) — irrelevante para 002 (sin config, R3).
- **go.mod:** `go 1.24.4` — generics disponibles (decisión D8: `Store[T]`/`Fetch[T]` stdlib-simple); única dep externa `yaml.v3` (sin cambios).
- **Baseline:** 763 tests / 30 pkgs `-race` (ADR-011, post-merge 001); RED especial de compilación precedente 011-005/015-001/019-001.

### Riesgos
- **R1 — Duplicación si el motor no se extrae:** derivas silenciosas de las correcciones HITL de 001 en cada copia (mitigado: D1 aprobada).
- **R2 — Rate limits / volumen:** catálogo OpenRouter de 733 KB re-fetcheado por corrida del timer (60 min en 019-006; el TTL 5m hace stale a casi cada corrida — es lo que la epic quiere). Sin límite público documentado verificado; digest-skip mitiga writes, no fetches. Monitorear en 006.
- **R3 — Aliases `~` de OpenRouter:** si 003 los tratara como IDs reales duplicaría modelos (`~z-ai/glm-latest` vs `z-ai/glm-5.2`); 002 los persiste tal cual (I2), decisión documentada para 003.
- **R4 — Pricing strings + `overrides[]`:** string→float64 puede perder precisión/exponentes raros; 002 expone el parseado + raw (003 puede re-parsear); redondeo/formato = decisión de 003.
- **R5 — Vocabulario `supported_parameters`:** OpenRouter usa `include_reasoning`/`reasoning_effort`; models.dev/opencode usa otro (`tools`, `structured_outputs`, `temperature`) — compararlos requiere mapeo en 003, NO en 002.
- **VERIFICAR — OpenRouter anónimo vs autenticado:** sin key en el entorno no contrastable; el anónimo sirve el catálogo completo (riesgo bajo); re-verificar en 004/005 cuando exista key en deploy.
- **VERIFICAR — estabilidad de shapes Zen/Go:** 2 fetches verificados hoy; si `data[]` cambia de shape (no OpenAI-list), el parseo tolerante degrada a zero-values (I9) sin romper; el digest + `owned_by:"opencode"` es la firma observable.

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11
