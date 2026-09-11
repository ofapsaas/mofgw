# Test Audit — 019-002-fetch-zen-go

**Fecha:** 2026-09-11 · **Auditor:** cdad-test-writer (sesión aislada full-amethyst-crane) · **Materializado por:** orquestador
**Spec:** docs/specs/019-002-fetch-zen-go/spec.md (aprobado 4443a83)

## Baseline verificado (orquestador)

Suite completa 763/30 `-race` verde, build/vet OK (post-merge 019-001).

## Fase A — Gate de refactor: 0 tests nuevos, 0 modificaciones

- Todos los símbolos que la suite 001 usa (Fetch/NewStore/Store.Path-TTL-Lock/consts/errores/Options/ParseCatalog/build.UserAgent) quedan preservados por P2/aliases. Sin struct literals de Store en la suite → el alias genérico compila sin tocar tests.
- **Ningún test aserta el texto exacto de ErrFetchDisabled/ErrLockBusy** — solo `errors.Is` (identidad por alias de la misma var del motor). Refactor mecánico viable.
- `TestLogEvents` de 001 aserta presencia de campos, no ausencia → el gate de byte-igualdad de logs lo cubre B9 con assert negativo (ausencia de `source` en eventos modelsdev).
- `modelscache` NO recibe test propio: P3 (motor semánticamente equivalente) queda cubierto por la suite 001 que ejercita el motor vía facade (D7/P15).
- Gate C1: `go test ./... -count=1 -race` verde + `git diff --exit-code internal/modelsdev/modelsdev_test.go`.

## Tests nuevos (Fase B) — bloques B1..B10 en `internal/upstream/upstream_test.go` (package upstream, RED por compilación)

| Bloque | P | Tests | Mecanismo |
|---|---|---|---|
| B1 | P4, P5 | TestFetchZen_FaithfulList, TestFetchGo_FaithfulList | Fake httptest + contador + UA; fixture 2 items reales; fiel por índice |
| B2 | P6 | TestParseModelList_Tolerant | data ausente/null → nil sin error; extras ignorados; solo JSON inválido → error |
| B3 | P7 | TestParseOpenRouterCatalog_PricingStrings | pricing strings → float64; no numérico/ausente → 0 sin error |
| B4 | P8, P9 | TestParseOpenRouterCatalog_FaithfulFields, _TolerantShape | fields fieles + `~` verbatim + schema variable ignorada |
| B5 | P10 | TestFetchOpenRouter_AuthConditional | t.Setenv: con env → Bearer; sin/vacía → anónimo; lectura por llamada (sin t.Parallel) |
| B6 | P11 | TestOpenRouter_KeyNeverLogged | handler capturador + fetch_failed 401; serialized ∌ key; Error() ∌ key |
| B7 | P12 | TestUpstreamKnob_DisablesSources, TestUpstreamKnob_Isolation | knob aislado por Spec; **oráculo = contador de hits** (NO identidad de errores entre paquetes) |
| B8 | P13 | TestDefaultCachePaths_PerSource, TestStores_Isolation | MOFGW_CACHE_DIR→t.TempDir; paths explícitos por fuente en subdirs; corrupción cruzada sin error |
| B9 | P14 | TestEngineLog_SourceAttribute | 4 specs: upstream → source presente; modelsdev (Source="") → ausencia (complemento del P17 de 001) |
| B10 | P15 | TestZenStore_Wiring, TestGoStore_Wiring, TestOpenRouterStore_Wiring | por fuente: fetch+changed, Get tipado, digest skip (force), fail-soft |

Total: **16 tests nuevos** en un archivo. Fixtures definidos (OpenRouter mínimo representativo + zen/go 2 items) inline en el audit.

## Riesgos RED y mitigaciones

- (a) Identidad de errores compartida entre paquetes (alias mismo var del motor): tests de aislamiento de knobs usan hits como oráculo, jamás errors.Is cruzado entre paquetes.
- (b) P14: B9 agrega assert negativo de ausencia de `source` en eventos modelsdev (la suite 001 no lo cubre).
- (c) t.Setenv → nunca t.Parallel() en B5/B7/B8.
- (d) Aislamiento P13: paths explícitos por fuente en subdirs de t.TempDir (nunca Default*CachePath en el test).
- Sin extensión de contrato (D8 congeló toda la API).

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11 — desbloquea RED (3.1).
