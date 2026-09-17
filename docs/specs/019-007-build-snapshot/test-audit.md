# test-audit.md — 019-007-build-snapshot

**Feature:** 019-007-build-snapshot · **Etapa:** 3.0 (AUDIT) · **Fecha:** 2026-09-17
**Fuente única:** `docs/specs/019-007-build-snapshot/spec.md` (P1-P14, I1-I7, C1-C12) + `scripts/test-install.sh` (harness).
**Desviación de proceso:** AUDIT inline por el orquestador (spawning de sub-agentes caído — 8º incidente del epic, disclosure en state).
**Evidencia de baseline:** suite 959/36 `-race` (post-006, verificada por el orquestador al cerrar 006).

## 1. Resumen ejecutivo

- **Veredicto global: 0 tests existentes requieren modificación.** `runOpts` gana el campo `Snapshot` (aditivo; zero-value = `Available:false` → comportamiento pre-007 idéntico — mismo patrón nil-disabled de 005). `loadSources` solo agrega una rama de fallback tras `Get()` fallido; `main()` puebla `Snapshot` desde `Embedded()`; P11 de 004/005 intactos. I1: cero diffs en `internal/*` y `cmd/mofgw`.
- **Tests nuevos:** 7 Go (B1-B7: 2 en `cmd/mofgw-sync/snapshot/snapshot_test.go` + 5 en `cmd/mofgw-sync/main_test.go`) + 3 bash en `scripts/test-install.sh` (C9×2, C10) + 1 canary (C11). 12/12 criterios cubiertos; 14/14 postcondiciones con test.
- **RED por compilación en dos frentes** (lockeado por el spec): (1) `snapshot_test.go` importa `cmd/mofgw-sync/snapshot` inexistente; (2) `main_test.go` usa `runOpts{Snapshot: runSnapshot{...}}` (campo + tipo inexistentes).
- El `api.json` real (4.6 MB) NO es prerrequisito del RED (los tests usan tiny inyectado); se commitea en GREEN.

## 2. Inventario + veredicto por test existente

| Paquete/área | Tests | Veredicto |
| --- | --- | --- |
| `cmd/mofgw-sync` (main_test.go, reload_test.go, reload_live_test.go, unitfiles_test.go) | 26 top-level | UNTOUCHED — `runOpts.Snapshot` zero-value (`Available:false`) ⇒ `loadSources` idéntica a pre-007 (misma mecánica que los hooks nil-disabled de 005, test-audit §2.4 de 019-005) |
| `internal/*` (todo) | todo | UNTOUCHED (I1; el fallback parsea con el parser existente, cero cambios) |
| `scripts/test-install.sh` (13 tests) | bash | Extensión ADITIVA en GREEN (los 13 existentes deben seguir pasando — gate) |

Commits RED agregan SOLO: `cmd/mofgw-sync/snapshot/snapshot_test.go` (NUEVO), adiciones al final de `cmd/mofgw-sync/main_test.go`... **DECISIÓN (desviación F7-like documentada):** los tests de fallback van en `main_test.go` (append, mismo package) en vez de archivo nuevo — comparten los fixtures de 004 (`syncConfigTemplate`, seeders) y evitan duplicar helpers; `main_test.go` queda modificado solo por ADICIÓN al final (funciones nuevas, cero ediciones a tests existentes).

## 3. Plan de tests nuevos

Contratos de firma (el implementer DEBE respetarlos — espejo `cachePaths` de 004):

```go
// cmd/mofgw-sync/main.go (extensión aditiva):
type runSnapshot struct {
	Raw       []byte    // catálogo sintético tiny (tests) / api.json embebido (prod)
	FetchedAt time.Time // meta: para age_days + warning staleness (D5/P8)
	SHA256    string    // meta: digest reportado (D3)
	Available bool      // false ⇒ comportamiento pre-007 (P9/P11)
}
// runOpts gana: Snapshot runSnapshot
// snapshot.Embedded() (paquete NUEVO cmd/mofgw-sync/snapshot):
func Embedded() (raw []byte, meta Meta, ok bool)
type Meta struct {
	FetchedAt time.Time
	SHA256    string
	SourceURL string
}
```

- **B1 `TestSnapshot_EmbeddedMeta`** (C2, P3; `snapshot/snapshot_test.go`): `Embedded()` sobre meta válida → Meta parseada; `SHA256` == sha256 hex del api.json embebido; FetchedAt RFC3339. RED: paquete inexistente.
- **B2 `TestSnapshot_UnavailableOnBadMeta`** (C2, P4): tabla — meta corrupta / meta ausente / api.json vacío → `ok=false`. (En RED: paquete inexistente. Implementación necesaria para distinguir "ausente" de "corrupto": ambas → unavailable; el test congela ambos caminos.)
- **B3 `TestLoadSources_SnapshotFallback`** (C1, P1+P6; `main_test.go`): cache modelsdev AUSENTE + `runOpts.Snapshot{Available:true, Raw: tiny válido}` → catalog parseado del tiny (providers/models por clave correctos); Zen/Go/OpenRouter nil (P10); **disco byte-intacto** (readdir del dir de cache antes/después: sin models-dev.json/sidecar/tmp nuevos — P6). RED: `runOpts{Snapshot:...}` campo inexistente.
- **B4 `TestLoadSources_SnapshotTolerantShape`** (C1, P2): tiny con schema variable (sin `limit`, sin `cache_write`, campo desconocido) → parse sin error, zero-values, desconocidos ignorados (misma semántica P14 de 001). RED: compilación.
- **B5 `TestLoadSources_CorruptCacheNoSnapshotMask`** (C3, P5): cache PRESENTE con JSON inválido + snapshot disponible → `catalog == nil` (error de corruption; NO fallback) via `run()` completo o `loadSources` directo — `loadSources` es función privada de main.go (in-package, testeable directo). RED: compilación.
- **B6 `TestSnapshotFallback_WarningAndLog`** (C5, P7): fallback servido → log con evento `snapshot_fallback` (slog capturador con los 4 atributos) + `plan.Warnings` del run contiene substring `catálogo snapshot embebido`. RED: compilación (campo + paquete).
- **B7 `TestSnapshotFallback_StalenessWarn`** (C6, P8): `FetchedAt` 31 días atrás → warning de staleness en log, exit intacto; `FetchedAt` 5 días → sin warning. RED: compilación.
- **B8 `TestLoadSources_NoSnapshotBehavesAsBefore`** (C7, P9/P11): `Available:false` + sin cache + NoFetch → comportamiento pre-007 (catalog nil → Merge sin fuentes → exit 1 en run completo). RED: compilación.
- **B9 `test_server_unit_has_envfile`** (C9, P12; bash): post-install sandbox, el `mofgw.service` instalado contiene la línea exacta `EnvironmentFile=%h/.config/mofgw/env` (sin prefix `-`).
- **B10 `test_server_dropin_untouched`** (C9, P13; bash): `mofgw.service.d/override.conf` pre-sembrado con custom → byte-exacto tras install; si el dir no existe pre-install → install jamás lo crea.
- **B11 `test_fetch_snapshot`** (C10, P14; bash): `scripts/fetch-snapshot.sh` con `MOFGW_SNAPSHOT_URL=file://…` (body válido) → `api.json` byte-idéntico + `meta.json` con sha/fetched_at RFC3339; body inválido → exit≠0 sin escribir.
- **B12 `TestSnapshot_LiveEmbeddedParses`** (C11, canary): `MOFGW_SYNC_LIVE=1` + `Embedded()` sobre el api.json REAL → `modelsdev.ParseCatalog` OK; skip sin la var.

Cobertura: P1 B3 · P2 B4 · P3 B1 · P4 B2 · P5 B5 · P6 B3 · P7 B6 · P8 B7 · P9 B8 · P10 B3/B8 · P11 B8 · P12 B9 · P13 B10 · P14 B11. 14/14 con test. I1-I7 arquitecturales (etapa 4 + gate suite).

## 4. Benefit-of-doubt

- **Resuelto:** `runOpts.Snapshot` zero-value = `Available:false` ⇒ loadSources idéntica (patrón 005). Los tests existentes de 004/005 corren sin el campo → fase 004/005 intactas.
- **Resuelto:** `modelsdev.ParseCatalog` es el entry point (modelsdev.go:127, congela TestParseCatalog_TolerantShape) — el fallback NO reimplementa parseo.
- **Resuelto:** el warning sintético se agrega en `run()` post-Merge (no en el IR) — documentado como binario-added; los tests de 003 no lo ven.
- **Resuelto:** `fetch-snapshot.sh` fakeable vía `MOFGW_SNAPSHOT_URL` (soporta file://) + `MOFGW_SNAPSHOT_DIR` override para tests — el test B11 no necesita red.
- **Pendientes → ninguna:** el spec cerró todo en D1-D8.

## 5. Estrategia RED + discriminantes

RED por compilación (frentes del spec). **Discriminantes:** corrupt-cache (B5: corruption ≠ ausencia); disco byte-intacto (B3: readdir antes/después — un impl que persistiera el snapshot falla); warning sintético por substring + evento slog con atributos (B6: un impl que solo loguee sin tocar plan.Warnings falla y viceversa); staleness con FetchedAt inyectado viejo/reciente (B7); Template: la línea EnvironmentFile exacta sin `-` (B9: con `-` es tolerante, el server exige estricto); drop-in: byte-exacto + jamás creado (B10).

**Commits RED:** B1-B2 (snapshot package) + B3-B8 (main_test.go append) → `test(mofgw): 019-007 — RED (B1-B8) por compilación`. Harness bash (B9-B11) va con GREEN (desviación 006: el harness se mantiene ejecutable).

## 6. Fixtures

- Tiny sintético (providers espejo opencode con 2-3 modelos, schema variable para B4) — inline en main_test.go.
- `syncConfigTemplate` + seeders de 004 reutilizados para el ciclo completo (B6/B7/B8 a nivel run()).
- fetch-snapshot fake: archivo JSON local + `MOFGW_SNAPSHOT_URL=file://…`.

## 7. Gate checklist

- [x] Baseline 959/36 `-race` (post-006).
- [x] Tests a modificar: **0** — justificado (aditividad nil-disabled + I1).
- [x] Toda postcondición P1-P14 tiene test (tabla §3).
- [x] Todo test nuevo mapea a criterio C1-C12.
- [x] Sin dependencia de estructura interna (tiny inyectado = contrato; asserts por contrato observable).
- [x] Benefit-of-doubt resuelto; riesgos: (R1) fetch-snapshot.sh con curl — fake file:// en tests; (R2) api.json real 4.6 MB en GREEN (descarga real, commiteado); (R3) harness corre en orquestador.

---

**Resumen:** Tests a modificar: **0** · Tests nuevos: **8 Go (B1-B8) + 3 bash (B9-B11) + 1 canary (B12)** · Regression risks: 3 (detalle §7).

Status: **Approved** by Ofap (agent-delegated HITL, goal mode) on 2026-09-17 — gate 3.0 cerrado, arranca RED (3.1).
