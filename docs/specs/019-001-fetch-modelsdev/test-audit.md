# Test Audit — 019-001-fetch-modelsdev

**Fecha:** 2026-09-11 · **Auditor:** cdad-test-writer (sesión aislada striped-black-bedbug) · **Materializado por:** orquestador
**Spec:** docs/specs/019-001-fetch-modelsdev/spec.md (aprobado 837639a)

## Baseline verificado (evidencia, corrido por el orquestador)

- `go build ./...` → OK · `go vet ./...` → OK
- `go test ./... -count=1` → **733 passed / 29 packages** (sin -race; -race exigido en GREEN)

## Comportamiento que cambia

Cero en código existente: la feature entrega el paquete nuevo `internal/modelsdev` (D2). Grep verificado: `modelsdev|models.dev|MOFGW_DISABLE_MODELS_FETCH|MOFGW_CACHE_DIR|UserCacheDir|ErrFetchDisabled|ErrLockBusy` → 0 matches en todo el árbol. `internal/modelsdev/` no existe.

## Tests a modificar: **0**

Ninguno de los 77 archivos `*_test.go` de la suite (733 tests) referencia símbolos o comportamientos que la feature cambie. Untouched por construcción; `go.mod` no cambia dependencias.

## Tests nuevos — bloques B1..B11 (19 funciones en `internal/modelsdev/modelsdev_test.go`)

Fixture JSON tolerante inline (2 providers: uno completo, uno con schema variable). Fake upstream `httptest.Server` con contador atómico de hits; `t.Setenv` para el knob; `t.TempDir()` SIEMPRE (jamás `~/.cache` real); captura slog para P17.

| Bloque | Postcond. | Tests | Mecanismo |
|---|---|---|---|
| B1 | P1, P2 | TestGet_ServesCacheWithoutNetwork, TestGet_ServesStaleCache | Cache fixture en t.TempDir + Get() → hits==0; stale via os.Chtimes `now-(TTL+2min)` |
| B2 | P3 | TestFetch_CatalogRawAndUserAgent | Fetch a fake → raw byte-idéntico + UA `mofgw/<build.Version>` |
| B3 | P4 | TestFetch_Timeout, TestFetch_RetryTransient, TestFetch_NoRetryOn4xx | Timeout via ctx 200ms + server que bloquea (hits==1, sin retry); 500→200 (hits==2, backoff ~500ms); 404 (hits==1) |
| B4 | P5-P7 | TestRefresh_FreshSkips, TestRefresh_StaleRefetches, TestRefresh_Force | Store con fake; TTL parametrizado; Chtimes para stale |
| B5 | P8 | TestRefresh_SkipsIdenticalBody | 2º Refresh con body idéntico → cache+sidecar byte-idénticos, changed=false |
| B6 | P9, P10 | TestRefresh_LockBusyFailsFast, TestRefresh_NoLockMode | Flock sostenido en el mismo proceso (fd abierto) → ErrLockBusy rápido; Lock=false no crea .lock |
| B7 | P11, P12 | TestRefresh_FailSoftWithCache, TestRefresh_FailLoudWithoutCache | 500x2 con/sin cache → (false,nil)+stale / error sin crear archivos |
| B8 | P13 | TestFetchDisabled_Knob | t.Setenv → ErrFetchDisabled sin request; Refresh con/sin cache |
| B9 | P14, P15 | TestParseCatalog_TolerantShape, TestParseCatalog_FaithfulFields | ParseCatalog directo sobre fixture tolerante (campos ausentes/desconocidos) |
| B10 | P16 | TestRefresh_AtomicWrite | Subdir anidado → mkdir 0o750, JSON parseable, sin `.tmp-*` residuales |
| B11 | P17 | TestLogEvents | WithLogger(slog.NewJSONHandler(&buf)) → eventos con campos exactos |

Total: **19 tests nuevos**, P1-P17 completas (0 huérfanas, 0 sobre-espec). C12 no es test: es el gate de suite completa (`-race` + vet + gofmt) validado por el orquestador.

## Decisión P4/timeout (sin extensión de contrato)

`FetchTimeout=10s` es tope del intento sobre el ctx del llamador (D9). `TestFetch_Timeout`: `context.WithTimeout(200ms)` + server que bloquea → error timeout con hits==1, sin retry. Determinístico, sin sleep. Descartado inyectar client Timeout corto (ambigüedad retryable/per-budget).

## Extensión de contrato E1 (RESUELTA por HITL delegado)

**E1 ACEPTADA:** `Fetch(ctx context.Context, opts ...Option) (*Catalog, []byte, error)` — variádico aditivo, fiel a D3 (WithBaseURL "apunta Fetch al upstream fake") y necesario para P3/P4 testables. Las Options se leen por llamada (incl. re-leer `MOFGW_DISABLE_MODELS_FETCH` cada llamada — R4). No rompe la firma congelada (options ya declaradas en D3).

## Riesgos RED (mitigaciones acordadas)

- R1 backoff ~500ms determinístico en RetryTransient: aceptado (+0.5s en suite).
- R2 mtime race: t.TempDir + Chtimes con margen amplio; TTL parametrizado; sin sleeps para envejecer.
- R3 LockBusy cross-proceso: flock en el mismo proceso, determinístico.
- R4 env knob cacheada a nivel package: tests con t.Setenv por llamada + aviso al implementer (no `t.Parallel()` en esos tests).
- R5/R6/R7: oráculo hits==0 para "Get sin red"; atomicidad validada por invariante observable post-éxito (patrón persist_test:242); WithClient(ts.Client()) en tests de red.

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11 — desbloquea RED (3.1) con E1 incorporada.
