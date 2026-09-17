---
id: 019-007-build-snapshot
title: Snapshot embebido del catálogo models.dev en el binario sync + absorción EnvironmentFile al template del server (cierre F2)
status: draft
epic: 019-provider-sync-automation
date: 2026-09-17
created: 2026-09-17
motivacion_externa: "Epic 019: 001-006 DONE y mergeadas; el sync corre agendado pero un host fresco sin red/cache no tiene catálogo (fail-loud en loadSources). 007 entrega fallback offline vía snapshot byte-fiel embebido (patrón opencode models-snapshot) + cierra la divergencia F2 de 006 (template sin EnvironmentFile)."
---

# 019-007 — Build-snapshot (fallback offline) + absorción env-file al template

## Descripción

- **D1 — Componentes (LOCKEADA, decisión HITL 1/6).** Cinco piezas, dos tracks:
  1. **Paquete nuevo `cmd/mofgw-sync/snapshot`** (package `snapshot`): `snapshot.go` con `//go:embed api.json meta.json` en su propio dir + `func Embedded() (raw []byte, meta Meta, ok bool)` + `type Meta struct{ FetchedAt time.Time; SHA256 string; SourceURL string }`. Ubicación lockeada en `cmd/mofgw-sync/snapshot/` (decisión 6): SOLO crece el binario sync (+~4.6 MB); `internal/modelsdev`/`internal/modelscache` (importadas por tests y por el server) quedan livianas. NADA en `internal/`.
  2. **Datos commiteados**: `cmd/mofgw-sync/snapshot/api.json` (catálogo REAL, ~4.6 MB, decisión 9 — el orquestador lo descarga en GREEN) + `cmd/mofgw-sync/snapshot/meta.json` (`{fetched_at, sha256, source_url}`). Única fuente del fallback.
  3. **Extensión de `cmd/mofgw-sync/main.go`** (decisión 7): `runOpts` gana el campo `Snapshot runSnapshot` con `type runSnapshot struct { Raw []byte; FetchedAt time.Time; SHA256 string; Available bool }`; `main()` lo puebla desde `snapshot.Embedded()` (api.json vacío/ausente → `Available=false`, comportamiento idéntico a hoy); `loadSources()` aplica el fallback solo a models.dev. Qué NO toca: `loadRawConfig`, `configsync.Apply`, fase reload (`reloadsig.Run`), `parseArgs`/flags, `cmd/mofgw/*`, todo `internal/*`.
  4. **Script `scripts/fetch-snapshot.sh`** (decisión 2): `GET https://models.dev/api.json` → escribe `api.json` + `meta.json` con sha256. Manual, operado por el mantenedor. JAMÁS corre en build (I-hermeticidad).
  5. **Track B (decisión 5/10)**: (a) UNA línea en el heredoc de `install_unit` (`scripts/install.sh:144-160`): `EnvironmentFile=%h/.config/mofgw/env` (SIN prefix `-`); (b) header de `install.sh` documenta el patrón drop-in `mofgw.service.d/override.conf` para customs host-specific (flags/paths), creado por el OPERADOR; `install.sh` NO lo genera ni lo toca. El aviso post-divergencia de 006 (`install.sh:172-174`) queda intacto.
- **D2 — Snapshot COMPLETO byte-fiel (LOCKEADA, decisión HITL 1).** `api.json` = body crudo de `GET https://models.dev/api.json`, sin filtrar ni transformar. Fidelidad manda (I2 de 001/003 "el upstream manda"); +4.6 MB al binario sync aceptados. Alternativa filtrada descartada (acoplaría 007 al mapeo de 003).
- **D3 — Contrato de fallback por inyección (LOCKEADA, decisión HITL 7, espejo `cachePaths` de 004).** Los tests inyectan un catálogo sintético TINY vía `runOpts.Snapshot`; producción lo puebla desde `snapshot.Embedded()`. Parse del fallback vía `modelsdev.ParseCatalog` (entry point que congela `TestParseCatalog_TolerantShape` en `internal/modelsdev/modelsdev_test.go`). El digest reportado es el de `meta.json` (SHA256 del `api.json` commiteado).
- **D4 — Trigger delimitado del fallback (LOCKEADA).** El fallback corre en `loadSources()` (`cmd/mofgw-sync/main.go:237-274`) SOLO cuando `mdStore.Get()` falla por **ausencia**: cache inexistente (fetch-fallido-sin-cache, primer arranque sin red; cache ausente + `--no-fetch`; knob-disable-sin-cache). **NO cae a snapshot**: cache presente pero corrupto (parse error sobre archivo existente → error, es corruption no ausencia); config vigente inválido (fail-loud P15 de 004 intacto); Zen/Go/OpenRouter (siguen a nil con warning, decisión 3).
- **D5 — Visibilidad staleness (LOCKEADA, decisión HITL 2/8).** Al servir snapshot: log `snapshot_fallback{source=modelsdev, fetched_at, sha256, age_days}` + warning, Y warning sintético post-Merge en `plan.Warnings`: `"models.dev: catálogo snapshot embebido (fetched_at X, N días) — sin red"` (binario-added, NO derivado del IR; visible en reporte P12 de 005 + journal del timer). `age > 30d` → warning adicional de staleness. Siempre fail-soft: NUNCA cambia exit code.
- **D6 — Snapshot nunca toca disco (LOCKEADA).** El catálogo del snapshot se sirve **en memoria**; jamás se escribe como cache ni sidecar (el par cache+sidecar en disco sigue siendo propiedad exclusiva del fetch real, D6/I4 de 001). Disco byte-intacto tras fallback.
- **D7 — `fetch-snapshot.sh` manual (LOCKEADA, decisión HITL 2).** Uso: `./scripts/fetch-snapshot.sh` (o con `MOFGW_SNAPSHOT_DIR` override para tests) hace GET con `curl`, verifica JSON parseable + no-vacío, calcula `sha256sum`, escribe `api.json` + `meta.json`. Sin CI, sin hooks de build, sin gates de freshness fail-loud.
- **D8 — Track B: template + drop-in (LOCKEADA, decisión HITL 4/5/10).** (a) Heredoc gana exactamente una línea bajo `[Service]`: `EnvironmentFile=%h/.config/mofgw/env` (sin `-`; el server consume keys, sin env arranca infuncional — estricto). Resto del heredoc byte-intacto (I5 de 006 heredada). (b) Header de `install.sh` documenta: customs host-specific (`--verbose`, `--log-file`, paths) van en `~/.config/systemd/user/mofgw.service.d/override.conf` (sección `[Service]` + `ExecStart=` de reset + `ExecStart=` completo), creado por el operador. (c) `install.sh` JAMÁS escribe en `mofgw.service.d/` (ausencia de gestión = contrato). (d) El aviso post-divergencia de 006 sigue cubriendo customs.

## Contrato (postcondiciones)

- **P1 — Fallback por ausencia (inyectado).** `mdStore.Get()` falla por archivo ausente + `runOpts.Snapshot{Available:true, Raw: <tiny válido>}` → `loadSources` devuelve `catalog` parseado del snapshot (providers/models del tiny correctos por clave). Zen/Go/OpenRouter nil como hoy.
- **P2 — Parse con el parser de 001.** El tiny inyectado con schema variable (modelo sin `limit.input`, sin `cost.cache_write`, con campo desconocido) parsea sin error, zero-values, desconocidos ignorados (misma semántica que P14 de 001).
- **P3 — Meta obligatoria y parseable.** `snapshot.Embedded()` con `meta.json` válido devuelve `Meta{FetchedAt, SHA256, SourceURL}`; `SHA256` = sha256 hex del `api.json` embebido (verificado en test); `FetchedAt` RFC3339 parseable.
- **P4 — Meta corrupta/ausente → unavailable.** `meta.json` corrupto o ausente, `sha256` que no matchea el api.json embebido (recomputado en `Embedded()`, review F3), o `api.json` vacío → `Embedded()` retorna `ok=false` → `runSnapshot{Available:false}` → el binario se comporta como hoy (cero fallback, fail-loud sin cache intacto). NOTA (review F5): "meta ausente" como ARCHIVO es inalcanzable en runtime (go:embed sin el archivo = error de compilación del paquete sync — fail-loud en build) — `ok=false` cubre vacío/corrupto/mismatch.
- **P5 — Corrupt-cache NO cae a snapshot.** Cache en disco presente pero JSON inválido (parse error) + snapshot disponible → `loadSources` devuelve `catalog=nil` (error de corruption, no ausencia); el sync falla-loud según contrato de 004. El snapshot NO enmascara corrupción.
- **P6 — Snapshot jamás escribe disco.** Tras servir fallback: directorio de cache byte-intacto (sin `models-dev.json` nuevo, sin `.sha256` nuevo, sin `.tmp-*` residuales). El digest de meta solo se REPORTA, no se persiste.
- **P7 — Log + warning sintético.** Fallback servido → evento `snapshot_fallback{source=modelsdev, fetched_at, sha256, age_days}` (handler slog capturador) + `plan.Warnings` contiene el string `"models.dev: catálogo snapshot embebido (fetched_at X, N días) — sin red"` (assert por substring `catálogo snapshot embebido`, documentado como binario-added post-Merge).
- **P8 — Staleness > 30d → warning, exit intacto.** `FetchedAt` con age > 30 días → warning adicional de staleness; exit code del ciclo inalterado (0 si el resto OK). Age ≤ 30d → sin warning adicional.
- **P9 — `main()` puebla desde `Embedded()`.** Con `api.json` real commiteado: `main()` construye `runOpts.Snapshot{Available:true, SHA256: <meta>, FetchedAt: <meta>}`. Con `api.json` vacío (checkout sin snapshot): `Available=false`, comportamiento idéntico a pre-007.
- **P10 — Zen/Go/OpenRouter sin snapshot.** Las tres listas sin cache → nil con warning del IR como hoy (P12 de 003 intacto); ningún cambio en `upstream.New*Store` ni en sus paths.
- **P11 — `--no-fetch` + sin cache + sin snapshot → fail-loud intacto.** `Merge` sin fuentes → exit 1 (contrato P12d/P15 de 003/004 se mantiene); el snapshot no inventa exit 0.
- **P12 — Template con EnvironmentFile.** Tras `install.sh` (sandbox `MOFGW_HOME=$(mktemp -d)`, `MOFGW_SKIP_SYSTEMCTL=1`): el `mofgw.service` instalado contiene la línea exacta `EnvironmentFile=%h/.config/mofgw/env` (sin prefix `-`); el resto del heredoc byte-idéntico al pre-007 salvo esa línea.
- **P13 — Drop-in intocado.** Con `mofgw.service.d/override.conf` pre-sembrado (custom `ExecStart` con flags) → `install.sh` exit 0, override byte-exacto, y el unit base instalado con P12. `install.sh` jamás crea `mofgw.service.d/` si no existe.
- **P14 — `fetch-snapshot.sh` funciona.** Con upstream fake (file:// o httptest de JSON válido): genera `api.json` byte-idéntico al body + `meta.json` con `sha256` correcto y `fetched_at` RFC3339. Con body vacío/inválido → exit ≠ 0 sin escribir.

## Invariantes

- **I1 — Solo el sync crece.** El snapshot vive en `cmd/mofgw-sync/snapshot/`; `internal/*` y `cmd/mofgw` (server) cero diffs, cero bytes nuevos. El server NO embebe el catálogo.
- **I2 — Hermeticidad del build.** `go build ./...` jamás necesita red; `fetch-snapshot.sh` no corre en build, CI, ni tests (salvo su propio test P14 con fake).
- **I3 — Snapshot read-only en memoria.** Ningún path escribe `api.json`/`meta.json` embebidos a disco como cache; el par cache+sidecar sigue siendo propiedad del fetch (D6/I4 de 001).
- **I4 — Tests deterministas sin red.** Todos los tests de fallback usan `runSnapshot` inyectado (tiny sintético); el `api.json` REAL solo se toca en canary opt-in (`MOFGW_SYNC_LIVE=1`).
- **I5 — Template: solo +1 línea.** El heredoc de `install_unit` cambia exclusivamente por `EnvironmentFile` (P12); flags/paths jamás entran al template (van al drop-in del operador).
- **I6 — `install.sh` no gestiona drop-ins.** Ni crea, ni edita, ni borra `mofgw.service.d/`; los `.bak.*` de units siguen inmortales (006 intacto).
- **I7 — Fail-soft del epic.** Staleness y ausencia de snapshot nunca cambian exit codes (filosofía 001 D4 / 005 D7); solo warnings.

## Criterios de aceptación (con mapeo test)

| #   | Criterio                                                                                                                        | Test                                                                                                                                               |
| --- | ------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| C1  | P1/P2: fallback inyectado sirve tiny; schema variable tolerante                                                                 | `TestLoadSources_SnapshotFallback` + `TestLoadSources_SnapshotTolerantShape` (`cmd/mofgw-sync/main_test.go`)                                             |
| C2  | P3/P4: meta válida parsea con sha correcto; meta corrupta/ausente o api vacío → unavailable                                     | `TestSnapshot_EmbeddedMeta` + `TestSnapshot_UnavailableOnBadMeta` (`cmd/mofgw-sync/snapshot/snapshot_test.go`; RED por compilación: paquete inexistente) |
| C3  | P5: cache corrupto + snapshot disponible → nil (no enmascara)                                                                   | `TestLoadSources_CorruptCacheNoSnapshotMask` (`main_test.go`; RED por compilación: campo `runOpts.Snapshot` inexistente)                                 |
| C4  | P6: disco byte-intacto tras fallback (sin cache/sidecar/tmp nuevos)                                                             | assert en `TestLoadSources_SnapshotFallback` (readdir del dir de cache antes/después)                                                                |
| C5  | P7: log `snapshot_fallback{...}` + warning sintético en `plan.Warnings`                                                             | `TestSnapshotFallback_WarningAndLog` (slog capturador + assert substring)                                                                            |
| C6  | P8: age>30d → warning staleness, exit intacto; age≤30d → sin warning                                                            | `TestSnapshotFallback_StalenessWarn` (FetchedAt inyectado viejo/reciente)                                                                            |
| C7  | P9/P11: `Available=false` → comportamiento pre-007; `--no-fetch` sin nada → exit 1                                                  | `TestLoadSources_NoSnapshotBehavesAsBefore`                                                                                                          |
| C8  | P10: Zen/Go/OpenRouter sin snapshot (nil con warning, sin cambios)                                                              | cubierto por tests existentes de 002/003 (no-regresión) + assert nil en C1                                                                         |
| C9  | P12/P13: unit instalado con `EnvironmentFile` sin `-`; override pre-sembrado byte-exacto; `mofgw.service.d/` jamás creado por install | `test_server_unit_has_envfile` + `test_server_dropin_untouched` (`scripts/test-install.sh`, harness sandbox)                                             |
| C10 | P14: `fetch-snapshot.sh` con fake genera api+meta correctos; body inválido → exit≠0 sin escribir                                  | `test_fetch_snapshot` (`scripts/test-install.sh` o test bash dedicado con file:// fake)                                                                |
| C11 | Canary opt-in: `Embedded()` sobre el `api.json` REAL parsea vía `modelsdev.ParseCatalog` (skip sin `MOFGW_SYNC_LIVE=1`)                 | `TestSnapshot_LiveEmbeddedParses` (canary, patrón C10/C14 de 005/006)                                                                                |
| C12 | Suite completa verde `go test ./... -count=1 -race` + `go vet` + `gofmt` + harness `test-install.sh` 100%                               | gates del orquestador (bash de subagentes bloqueado — precedente process-log)                                                                      |

**Naturaleza del RED:** RED por compilación en dos frentes: (1) `cmd/mofgw-sync/snapshot/snapshot_test.go` importa `cmd/mofgw-sync/snapshot` inexistente (`undefined: snapshot.Embedded`); (2) `main_test.go` usa `runOpts{Snapshot: ...}` con campo inexistente (`unknown field Snapshot`). Ambos fallan por compilación antes de cualquier aserción — precedente 011-005/015-001/019-001. El `api.json` real (4.6 MB) NO es prerrequisito del RED (los tests usan tiny inyectado); se commitea en GREEN (decisión 9).

## Fuera de alcance

- Regeneración automática, CI, o gates fail-loud por staleness (solo warn, D5).
- Snapshots de Zen/Go/OpenRouter (decisión 3; nil+warning intacto).
- Absorción de flags (`--verbose`/`--log-file`) al template (van al drop-in del operador, D8).
- Cambios al server (`cmd/mofgw`), scheduler/timer (006 intacto), reload/verify (005 intacto), merge (003 intacto), write (004 intacto).
- Actualización de clientes (opencode/openclaw/zot): fuera de toda la epic (plan.md:17).

## Contexto técnico verificado (Discovery + HITL 2026-09-17)

- models.dev `api.json` real: 4.59 MB, 213 providers, 7.711 modelos (spec 001:15, process-log:33-36). Módulo stdlib-only + yaml.v3 (go.mod:5-9); `go:embed` es stdlib.
- Entry point de parse: el que congela `TestParseCatalog_TolerantShape` en `internal/modelsdev/modelsdev_test.go` (las keys del catálogo ya están tipadas por 001).
- Stores con `Get` disk-only + fail-soft de refresh (modelscache/store.go:123-143, 88-118, D4/P12 de 001): el fallback corre en `loadSources` de `cmd/mofgw-sync/main.go:237-274`.
- Convención FS-inyectado del repo: `cachePaths` en runOpts (004), FS en reloadsig (005) — `runOpts.Snapshot` sigue el mismo patrón (decisión 7).
- Server template: heredoc install.sh install_unit (I5 de 006 lo lockea byte-intacto — esta feature agrega EXACTAMENTE una línea, P12).
- Unit real deployado: `~/.config/systemd/user/mofgw.service:7` (`EnvironmentFile=%h/.config/mofgw/env` sin prefix) + `:8` (ExecStart con `--verbose --log-file /home/ofap/logs/mofgw.log` — host-specific, va al drop-in, NO al template).
- Baseline suite post-006: 959 tests / 36 pkgs `-race` verde + harness test-install.sh 40/40.

## Estado

Status: **Draft** — pendiente aprobación HITL (Ofap agent-delegated, goal mode)
Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17
