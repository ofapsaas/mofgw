# activeContext.md — Contexto activo de mofgw

> Memory Bank: estado actual, decisiones recientes, próximos pasos, deuda conocida.
> Última actualización: 2026-09-18 (feature 020-002-metrics-summary-html MERGED — epic 020 2/2 COMPLETO).

## Decisiones recientes (cronología inversa)

### 18 Sep 2026 — Feature 020-002-metrics-summary-html (epic 020-mofgw-consumption-report) MERGED — EPIC 2/2

### Decisiones relevantes

- **Feature 020-002-metrics-summary-html MERGED — ÚLTIMA del epic 020 (el reporte de consumo existe como endpoint).** `GET /v1/metrics/summary?date=YYYY-MM-DD` (auth Bearer, date UTC estricto) → **HTML self-contained** (CSS inline, sin recursos externos, determinístico sortado) con: tabla por modelo (requests/success/error/tokens por tipo/cost_usd/cost_usd_up presente-null), cobertura de proveniencia (upstream/table/none + históricas + %), totales del día, contadores de corruptas. Parse **streaming single-pass** (Scanner buffer 1MiB) de registry.jsonl + rotados logrotate `<base>.N` (tope 5, `.gz` ignorado); filtro estricto `type=="terminal"` + `ts[:10]==date` (cero doble conteo); tolerante a líneas corruptas de cualquier longitud (ErrTooLong → descarta la línea gigante y RE-ARRANCA el parse — el resto del archivo SÍ se cuenta); `model==""` → fila "desconocido". Setter `SetRegistryPath(path)` (knob vacío → 503 runtime, patrón clientconfig); main.go lo cablea SIEMPRE (independiente de registry.enabled — lectura pura).
- **Review NEEDS_FIX resuelto pre-merge (3 Majors):** F1 (línea >1MiB abortaba el resto del archivo → re-arranque del scanner), F2 (2 categorías de corruptas sin contar: objeto sin type + ts malformado — el attempt válido skip silencioso), F3 (**html.EscapeString en el model — XSS del propio registro cerrado**). F4/F5/F12: aserciones endurecidas (corruptas exactas, totales exactos, cobertura numérica exacta, determinismo 2-request byte-idéntico).
- **Suite final del merge: 997 tests / 37 paquetes `-race` verde** (vet limpio). Commits: `cd7b3e5` spec, `0df0d77` audit, `b568bb0` RED, `f803ed5` GREEN, `8d79a98` fixes review, `0b9f8b6` review+sign-off.
- **Proceso — 10º-12º incidente de harness:** subagentes test-writer/implementer en loop o vacío durante 020-002 → RED y GREEN inline del orquestador (disclosure registrada). El review auditó el mixing de fixes de test en el commit GREEN (F6): ningún cambio debilita, dos endurecen — costo documentado de la ejecución inline.

### Epic 020-mofgw-consumption-report — CERRADO (2/2)

- **El registro ahora responde "USD/tokens por modelo por día" sin joins** (020-001) y **la vista de consulta inmediata existe** (020-002). Fuera de alcance del epic (ingestión Odoo/DB, dimensiones sesión/proyecto) queda documentado en el plan para un epic posterior.
- **Pendiente de deploy (operador):** ninguna acción requerida para este endpoint (vive en el server binario — la próxima actualización de deploy lo habilita).

### 18 Sep 2026 — Feature 020-001-registry-cost-model (epic 020-mofgw-consumption-report) MERGED

### Decisiones relevantes

- **Feature 020-001-registry-cost-model MERGED — PRIMERA del epic 020 (el registro ahora responde "USD por modelo por día" sin joins).** `TerminalEvent` gana `model` + `cost_usd_src` (upstream/table/none) + `cost_usd_up` (*float64 nullable — 0.0 de modelo free ≠ null). **Captura de `usage.cost` de OpenRouter** (`provider.Usage.Cost` con `json:"-"` → jamás re-serializado al cliente): precedencia upstream (costo exacto facturado) > tabla `pricing:` (`estimateCost` reutilizado — cero duplicación, decisión vinculante) > none (dato faltante, no costo cero). `emitTerminalError` extendido con `model` (HITL-a: error también lleva la dimensión). Captura **solo-lectura**: envelope + headers `X-Usage-*` byte-idénticos (P8 congelado por B9). Compatibilidad P6: líneas históricas parsean con zero-values (encoding/json default). 6 call-sites success (chat ×4 + responses + embeddings) + 4 error via `handleChainError`.
- **Implementer abortado a mitad de reporte, trabajo COMPLETO:** el GREEN (`7cbeaa9`) dejó 5 archivos sin commitear cuando el task se cortó (crédito agotado del usuario — explicación de los 8+ incidentes de harness del día). El orquestador verificó empíricamente (build OK, B1-B9 pasan, suite 987/37, vet limpio) antes de commitear — ningún trabajo perdido.
- **Review: APPROVE 0 bloqueantes** (1 Minor + 5 Advisory no-bloqueantes). **Anti-bias SATISFECHO** (GLM reviewer vs deepseek implementer). Auditoría RED→GREEN: cero toques a tests. MINOR-1 (singleflight follower sin test propio) al backlog de consolidación; Advisories 1-5 (followers de vuelo fallido, embeddings sin terminal, stream interrumpido, divergencia header-vs-registro, costo negativo sin clamp) al backlog del epic 020.
- **Suite del merge: 987 tests / 37 paquetes `-race` verde.** Commits: `f8795b5` spec, `00edf8b` audit, `0e48bc5` RED (B1-B9 + mod P6), `7cbeaa9` GREEN, `9905d7f` review.

### Próxima feature en cola

- **020-002-metrics-summary-html** (epic 020): `GET /v1/metrics/summary?date=YYYY-MM-DD` → HTML estático (tabla por modelo + cobertura de proveniencia + totales), streaming parse, tolerante a líneas corruptas. Epic 020: 1/2 done.

### 17 Sep 2026 — EPIC 019-provider-sync-automation CERRADO

### Decisiones relevantes

- **Epic CERRADO con 7/7 features mergeadas + E3 verificado** (closure: `docs/epics/019-provider-sync-automation/closure.md`; integración: `integration.md`). Suite final **974/37 `-race`** + harness **48/48**.
- **El ciclo sync completo existe y funciona contra datos reales:** `mofgw-sync --once` = fetch (001/002) → merge determinístico (003) → write atómico (004) → restart+verify+rollback (005), agendado por timer (006), con fallback offline (007).
- **Lecciones registradas en el closure** (obligatorias para próximos epics): E3 con datos reales es obligatorio (el bug P4c solo apareció contra upstreams vivos); test-audit commiteado antes del GREEN preserva TDD sin aislamiento; corregir el origen nunca el síntoma (B-1, P4c); el instalador es código de producción (harness de 6→16 tests); enmendar el plan cuando el descubrimiento lo refuta.
- **Pendiente de deploy (operador):** `./scripts/install.sh` + `enable --now` del timer + `MOFGW_SYNC_VERIFY_KEY` en env (integration.md §4). R2 (tolerancia a corte de streams) sin definir.
- **Siguiente:** epic 020-mofgw-consumption-report (planificado, spec draft 020-001 pendiente de aprobación HITL).

### 17 Sep 2026 — Feature 019-007-build-snapshot (epic 019-provider-sync-automation) MERGED — EPIC 7/7

### Decisiones relevantes

- **Feature 019-007-build-snapshot MERGED — ÚLTIMA del epic 019 (fallback offline + cierre F2).** Paquete nuevo **`cmd/mofgw-sync/snapshot`** (go:embed api.json+meta.json en su propio dir): **api.json REAL commiteado (4.5 MB, 221 providers, 7847 modelos, fetched 2026-09-17)** + meta.json (fetched_at/sha256/source_url). Fallback en `loadSources` SOLO por ausencia (stat IsNotExist — corrupto NO enmascara, P5) con parse del parser de 001; servido en memoria, jamás persistido (P6). Visibilidad: evento `snapshot_fallback{...}` + warning sintético post-Merge + warn staleness > 30d (fail-soft siempre). `runOpts.Snapshot` inyectable (tiny en tests; `main()` puebla desde `Embedded()`). Contrato runSnapshot{Raw,FetchedAt,SHA256,Available}.
- **Review REQUEST_CHANGES resuelto:** B1 (test sin commitear) + B2 (camino corrupto sin test determinista → `TestParseMeta_RejectsBad` in-package) + **F1 Major (TOCTOU por doble stat → `loadSources` retorna flag `fromSnapshot`, call sites mecánicos)** + F3 (sha recomputado en `Embedded()`, mismatch ⇒ unavailable) + F4 (meta atómica tmp+mv) + F5 (spec: "meta ausente como archivo" = error de compilación, fail-loud en build). F2/F6/F7 aceptados+documentados.
- **Track B (cierre F2 de 006):** heredoc del server gana EXACTAMENTE `EnvironmentFile=%h/.config/mofgw/env` (sin `-`); header documenta drop-in `mofgw.service.d/override.conf` (operador-creado; install.sh jamás lo gestiona); aviso post-divergencia de 006 intacto.
- **Suite final del merge: 973 tests / 37 paquetes `-race` verde** + harness **48/48** (re-corridos; vet limpio). Commits: `8f54d8f` spec, `4c40d39` audit, `393a4c0` RED, `ed1833f` GREEN, `190bd70` fixes review, `51e4da2` review+sign-off.
- **Ciclo del epic 019 COMPLETO:** 001 fetch-modelsdev → 002 fetch-zen-go → 003 merge-provider-catalog → 004 atomic-write-validate → 005 reload-signal → 006 systemd-timer → 007 build-snapshot. Siguiente: integración cross-feature E3 (criterios del plan: `mofgw-sync --once` contra upstreams reales + paridad `/v1/models` + timer activo) y closure E4.

### Deuda técnica detectada

- **Regeneración del snapshot sin dueño/cadencia:** api.json envejece silenciosamente (solo warn > 30d). Si la cadencia supera ~1/mes, reconsiderar frecuencia o documentación (F6). Mantenedor: regenerar con `scripts/fetch-snapshot.sh` cuando el sync loguee staleness.
- **Canary B12 (`MOFGW_SYNC_LIVE=1`) sin correr** — Embedded() sobre el real nunca verificado en runtime (el parse está congelado por tests contra el parser de 001; riesgo residual mínimo).
- **Absorción de flags (`--verbose`/`--log-file`) al template:** NO hecha por diseño (van al drop-in). El unit real deployado sigue divergiendo en flags — el aviso post-install lo cubre.
- **Flakes preexistentes:** `TestE2E010002_TTLExpiry` + `TestPostcondition9_MuestreoBanda` (estadísticos, fuera del epic).

### Próximo paso

- **E3 Integración del epic 019** (cdad-epic): E2E cross-feature (`mofgw-sync --once` real + paridad + timer) y cierre del loop; luego **E4 Closure** (closure.md + Memory Bank consolidado). Epic 020 en cola.

### 17 Sep 2026 — Feature 019-006-systemd-timer (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-006-systemd-timer MERGED — SEXTA del epic 019 (agenda el ciclo: cierra el criterio "Timer systemd activo con logs del sync").** Dos unidades commiteadas como fuente única de verdad — `scripts/systemd/mofgw-sync.service` (Type=oneshot, ExecStart=%h/.local/bin/mofgw-sync, EnvironmentFile compartido tolerante `-%h/.config/mofgw/env`, journal; SIN Restart/Install/timeouts) + `scripts/systemd/mofgw-sync.timer` (OnBootSec=10min + OnUnitActiveSec=60min; sin OnCalendar/Persistent — decisión estructural, no calendario). install.sh extendido: `install_sync_binary` (precedencia SYNC_BIN_SRC→prebuilt→go build, espejo de install_binary), `install_sync_units` por COPIA (no heredocs), `start_sync_timer` (daemon-reload + enable --now + verificación de agenda, DESPUÉS de server sano), uninstall sync (disable tolerante, .bak inmortales), resumen P12. Decisión explícita: **sin knob de intervalo, sin kill-switch, sin Persistent, `--once` queda no-op** (superficie mínima, precedente 005).
- **Corrección estructural D6 (el hallazgo de valor del ciclo):** el patrón `cmp→mv` de install.sh pisaba SIN rescate el unit real divergido del operador (env-file + flags verificados en host). Ahora: **backup-on-overwrite universal** `<unit>.bak.<ts>` ANTES de instalar — para los 3 units (incluido `mofgw.service`). **Pero (F2 del review): el backup salva el ARCHIVO, no el COMPORTAMIENTO** — el template del server sigue sin env-file/flags, así que re-instalar deja un server que arranca distinto. Mitigación inmediata: aviso LOUD post-install cuando hubo divergencia ("conciliar env-file/flags/customs antes del próximo restart"). La absorción de env+flags al template es scope de 019-007 (spec I5/P4 de 006 lockean el template byte-intacto).
- **Review REQUEST_CHANGES resuelto:** F1 (P10: header documenta `MOFGW_SYNC_VERIFY_KEY` + assert en harness — sin doc, F3 jamás se activa), F3 (pairing test tautológico → deriva del FS), F4 (timestamps `%N`), F5 (guard python3). F6 aceptado (tolerancia documentada del binario %h ausente en sandbox — territorio del canary B4). F7: no hay catch-up post-downtime con OnUnitActiveSec (semántica aceptada: sync idempotente).
- **Evidencia del gate:** golden Go 4/4, suite **959/36 `-race` verde**, harness bash **40/40** (re-corridos; chmod +x de ambos scripts commiteado 100755). Commits: `70b7e84` spec, `0f3ee06` audit, `e338154` RED golden, `c776a3e` GREEN, `e630c93` fixes review, `496d284` review+sign-off.

### Deuda técnica detectada

- **Absorción de env-file+flags al template del server → scope 019-007** (decisión F2; el warning post-install es solo mitigación).
- **Canary B4 opt-in (`MOFGW_SYNC_LIVE=1`) sin correr:** el deploy real del timer (daemon-reload + enable + list-timers) queda para el cierre del epic / deploy. El criterio "Timer activo" se cierra con this merge a nivel artefacto; la activación real es E3/integración o deploy del operador.
- **Paralelismo en el repo:** el fix `0edec80` (router 401/402 failover, incidente acct-4) landed mid-ciclo sin interferencia (suite verde con ambos). Coordinar con ese track si toca router en 019-007/E3.

### Próxima feature en cola

- **019-007-build-snapshot** (epic 019): snapshot embebido del catálogo en build (fallback offline, patrón opencode models-snapshot; depende de 001). Scope adicional acordado: absorción env-file+flags al template (F2). Epic 020 en cola tras el cierre de 019.

### 17 Sep 2026 — Feature 019-005-reload-signal (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-005-reload-signal MERGED — QUINTA del epic 019 (aplica la config escrita: el ciclo sync queda completo de punta a punta).** Paquete puro **`internal/reloadsig`** (interfaces inyectadas SystemdCtl/Prober/FS/Clock, precedente FS de 004): **restart `systemctl --user restart mofgw.service`** (D4: unit constante — decisión estructural, no temporal: el discovery verificó que el epic 017 es clients-polling, jamás hot-reload de providers[]; "SIGHUP cuando 017 retome" del plan epic se descarta por INCORRECTO) + **verificación en 3 fases** (F1 is-active poll 1s/ventana 30s → F2 `/healthz` sin Bearer → F3 paridad de IDs por SET contra `/v1/models` con Bearer `MOFGW_SYNC_VERIFY_KEY` — key de cliente; unset → degradación fail-soft a F1/F2 + warning) + **rollback restore-only** ante cualquier fallo de verificación (restaura los bytes previos leídos al inicio del run — jamás derivados — con patrón atómico temp+chmod+fsync+rename, re-restart y re-verificación liveness; el veredicto del rollback JAMÁS es exit 0) + **defensa M-2** (skip + digest disco≠candidato → fail-loud sin restart — cierra el skip-trap heredado de 004). Exit codes extendidos: 0/1/2 (heredados) + **3** = applied pero reload/verificación falló (rollback intentado). Flag `--no-reload` (D12: aplica sin restartear — streams en curso/testing/006).
- **Descubrimiento estructural clave:** el servidor NO tiene ningún mecanismo de reload (config.Load boot-only, wiring pre-tráfico, SIGHUP 0 hits — y sin handler SIGHUP la señal MATA el proceso). `/v1/models` SÍ requiere Bearer (auth.go:94-110) — el curl sin auth del skill mofgw-client-sync es STALE (R3 cerrado; corregir doc del skill quedó como tarea de documentación del merge).
- **Review REQUEST_CHANGES resuelto (7af4d22): F1 Major corregido pre-merge** — `Available()` conflatía "systemd sano" con "unit activo": un unit stopped/failed era misdiagnosticado como "sin systemd" → fix `is-system-running` con tolerancia a `degraded` (unit caído + systemd sano → restart intentado). F3 (cleanup temp), F4 (state completo), F6 (warn defensivo), F8b (rama muerta del default) aplicados; F2 (timeout 5s único vs 2s/5s) aceptado+documentado.
- **Suite final del merge: 953 tests / 36 paquetes `-race` verde** (re-corrida fresca post-fixes; vet limpio). Commits: `c6720e2` spec, `f096ab8` test-audit, `391d883` RED (21 tests B1-B17 + fakes + fixture addr 4444), `58d5d47` GREEN (reloadsig + fase post-write), `1e257df` fixes review, `7af4d22` review+sign-off.
- **Proceso — 8º incidente de harness del epic (el más severo):** el runtime de sub-agentes falló 8 veces durante la etapa 3 (outputs vacíos ×5 en 3 tipos de agente + 1 abort tras 3h de retry) → RED y GREEN ejecutados por el ORQUESTADOR inline (contract-primero: el test-audit commiteado ANTES del GREEN preserva la garantía del RED; el reviewer auditó el diff RED→GREEN y confirmó que el único edit de test fue de ejecutabilidad sin debilitar aserciones). Anti-bias DEGRADADO: review mono-familia como única capa externa (GLM vs GLM). Flakes preexistentes registrados: `TestE2E010002_TTLExpiry` + `TestPostcondition9_MuestreoBanda` (estadístico).

### Deuda técnica detectada

- **M-2 de 004 (DEFENSA ACTIVA AHORA):** el skip-trap sidecar-primero produce fail-loud en cada corrida (exit 1) hasta intervención — comportamiento deseado, pero el operador debe saber interpretarlo (log m2_check con ambos digests).
- **`MOFGW_SYNC_VERIFY_KEY` requiere registrarse como key de cliente** en clients.yaml — la paridad automatizada post-reload sin key queda en degradación (warning). Documentar en config.example/env.
- **Curl sin auth en skill mofgw-client-sync (stale):** corregir en el merge de documentación.
- **Advisories F8a/F-http-client + flakes preexistentes** al backlog de hardening (detalle: docs/specs/019-005-reload-signal/review.md).

### Próxima feature en cola

- **019-006-systemd-timer** (epic 019): unidad timer 60 min + OnBootSec + logging estructurado + modo `--once` (ya existe como default). Después: 019-007-build-snapshot. Epic 020 en cola tras el cierre de 019.

### 17 Sep 2026 — Feature 019-004-atomic-write-validate (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-004-atomic-write-validate MERGED — CUARTA del epic 019 (entrega el escritor de config.yaml que consume el IR `catalogmerge.Plan` de 019-003).** Paquete nuevo **`internal/configsync` puro** (sin red; disco solo vía parámetro, D9/P13): edición estructural de `config.yaml` vía **`yaml.Node`** (comentarios preservados, orden in-place = cadena fallback, merge-back — `thinking_default` jamás tocado), **validación pre-commit** con API aditiva **`config.ParseForValidation`** (vía `parseCommon`, sin resolución de env keys — el YAML en disco ya está materializado) y **write atómico temp+rename** solo si la validación pasa. Binario nuevo **`cmd/mofgw-sync`**: `--no-fetch` (cache-only), `--once` (no-op — el ciclo es once por diseño; el scheduling es 006), exit 0 (éxito) / 1 (fallo de sync o validación) / 2 (error de uso). **Skip byte-idéntico** con sidecar `config.yaml.sha256` (lección 019-001/002 replicada; auto-cura: digest ≠ sidecar → reescribe).
- **Decisión HITL D2 lockeada en el spec:** edición estructural yaml.Node **sobre** regeneración completa (yaml.v3 round-trip pierde comentarios) — el config.yaml del operador es territorio sagrado: comentarios, orden y claves ajenas al sync sobreviven intactos. Contrato clave: el único territorio mutable de `providers[]` es lo que el IR dicta (models/pricing/metadata); el resto del documento es read-only por construcción.
- **Review: REQUEST_CHANGES → resuelto** (2026-09-17). **B-1 bloqueante real: hueco de mapeo del audit — la postcondición de `--once` no tenía test.** Resuelto vía corrección del ORIGEN (contract-primero): mini-RED `6fed1d9` (test que castiga la ausencia de la postcondición) → mini-GREEN `c3ea424` (implementación), sin toquetear tests post-RED. **Anti-bias SATISFECHO por primera vez en el epic** (reviewer GLM/Z.ai vs implementer deepseek — deuda A2 recurrente mitigada).
- **Menores aceptados con fundamento:** M-1 oracles B8/B9 tautológicos — el serializer no-op devuelve raw verbatim (comportamiento más fiel que un oracle artificial); M-2 skip-trap sidecar-primero documentado como input del spec de **019-005/006** (D6 lockeada). Advisories A-4..A-9 al backlog de hardening.
- **Suite final del merge: 903 tests / 35 paquetes `-race` verde** (re-corrida fresca del orquestador 2026-09-17; build OK, vet limpio). Commits: `e6ba734` spec, `1ac13ea` spec aprobado, `3764b29` test-audit, `211f2d7` audit aprobado, `6d63adf` RED (21 tests B1-B21 + 2 fixtures testdata), `98d1826` GREEN (configsync + ParseForValidation + cmd/mofgw-sync), `eb29036` fixes AP-4, `7401710` review, `6fed1d9` mini-RED --once, `c3ea424` mini-GREEN --once, `efae476` sign-off.
- **Cero impacto servidor (I1 se mantiene):** `configsync` no toca el proxy/router; el servidor sigue cargando su config al arranque — `mofgw-sync` es una herramienta offline que prepara el archivo que 019-005/006 harán recargar.

### Deuda técnica detectada

- **Advisories A-4..A-9 de la review** al backlog de hardening (019-hardening): detalles en `docs/specs/019-004-atomic-write-validate/review.md`.
- **M-1 (aceptado, documentado):** oracles B8/B9 tautológicos — el raw verbatim del no-op es la semántica, no un bug; si el serializer gana lógica futura, reevaluar los oracles.
- **Deuda de tooling vigente (5º incidente de harness en el epic):** bash de los subagentes quedó bloqueado durante el ciclo (roles no pudieron correr `go`/`git`) → el orquestador ejecutó todas las verificaciones y materializó los fixtures `testdata` (permisos). Los roles razonaron y editaron; la ejecución fue del orquestador. Registrar en process-log del epic.
- **AP-4 (fix-back de tests):** 2 defectos del test-writer corregidos por fix-back (compile bug `declaredOf`, warning text B16, marker B7) — patrón AP-4 recurrente del epic.

### Próxima feature en cola

- **019-005-reload-signal** (epic 019): aplicar la config escrita — SIGHUP si hot-reload está disponible; si no, **restart del service systemd** (epic 017 sigue pausada → default restart). Verificación post-reload: `GET /v1/models` refleja el catálogo. **Inputs lockeados:** M-2 de esta feature (skip-trap sidecar-primero, D6) como input de su spec. Después: 019-006 (systemd-timer + observabilidad) y 019-007 (build-snapshot). Epic 020 en cola tras el cierre de 019.

### 16 Sep 2026 — Feature 019-003-merge-provider-catalog (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-003-merge-provider-catalog MERGED — TERCERA del epic 019 (entrega el IR de sync que 019-004 escribirá en config.yaml).** Paquete nuevo **`internal/catalogmerge` puro** (sin red, sin disco, sin `config.Load`; todo inyectado por parámetro, D9/P13): `Merge(...) → Plan{Providers, SourcesUsed, Warnings}` con `ProviderPlan{ProviderID, Source, Models, Pricing, Metadata, Warnings}` — pricing/metadata keyed por ID de acceso (I2). Asociación provider→fuente con **knobs declarativos `sync_source`/`sync_mirror`** (convención repo: declarativo, jamás inferido; default `""` = auto determinística por `base_url` contra consts de `upstream`; verificado contra el config vivo: 8×go, 8×zen, 1×openrouter, resto→modelsdev). Matching: strip de vendor OpenRouter + **alias de 2 saltos** vía `alias_target.slug` (extensión aditiva única en `internal/upstream`: `AliasTargetSlug`). Espejo de verdad pricing/metadata: defaults `zen→opencode`, `go→opencode-go`, `openrouter→modelsdev directo`; fallback alfabético-con-cost determinístico. **Extensión aditiva de `modelsdev`** (`StructuredOutput`/`Temperature`/`ReasoningEffort`, zero-value) para derivar `Thinking` y `supported_parameters` (P9/P10). **Determinismo byte a byte** (P11, deep-equal 2 corridas) y **fail-soft por fuente** (P12; `SourcesUsed`/`Warnings` exponen la degradación para que 004 decida).
- **Findings S1/S2 de la review resueltos por HITL (Pablo/Ofap, sign-off 2026-09-16 — gate 4→5 desbloqueado).** **S1 (L2-I7):** enmienda I7 en spec.md — `modelsdev` permite extensión aditiva zero-value requerida por P9/P10 (la implementación GREEN ya la materializó; suite 001 verde sin cambios de comportamiento). **S2 (L2-P2):** aclaración en P2 — `Models`(zen/go) = lista declarada en config (`prov.Models`) **enriquecida** con pricing/metadata; la paridad del set de IDs upstream compete a **019-004**. **L2-P7** queda como limitación documentada (`tiers`/`context_over_200k` no tipados en modelsdev → warning de descarte solo alcanzable vía `cache_write≠0`; candidato a extensión en 019-hardening).
- **Review: APPROVE, 0 bloqueantes** (2026-09-12). Verificación P1-P14 por postcondición; auditoría test↔postcondición C1-C14 completa (17 tests nuevos B1-B13, RED `b73694a` → GREEN `0b9d9fc` sin tocar tests). Anti-bias degradado (A2) nuevamente disclosado: reviewer corrió en la misma familia (deepseek-v4-flash) que el implementer — mitigaciones estructurales presentes, validación externa recomendada antes de deploy.
- **Suite final del merge: 836 tests / 33 paquetes `-race` verde** (re-corrida fresca del orquestador 2026-09-16, build OK + vet limpio; 1er run registró 1 flake de `TestE2E010002_TTLExpiry` del epic 010 — preexistente, pasa 5/5 aislado y el suite completo pasa de corrida). Commits: `b73694a` RED (17 tests B1-B13), `0b9d9fc` GREEN, `0f0fa33` review, `047a064` sign-off HITL S1/S2. Evidencia de no-regresión: `git diff --stat 0b9d9fc..HEAD` sin archivos `.go` antes del merge.
- **Cero impacto servidor (I1):** `catalogmerge` importa solo `internal/config` (tipos + 2 knobs), `internal/modelsdev`, `internal/upstream` y stdlib; jamás `proxy`/`router`/`cmd`/`modelscache`; el servidor no importa el paquete. Cambio a `internal/config` exclusivamente aditivo (P14: knobs nuevos, configs sin knobs cargan idéntico).

### Deuda técnica detectada

- **L2-P7 (limitación documentada):** warning de descarte de `tiers`/`context_over_200k` solo parcialmente satisfacible (los campos se pierden en el parse de modelsdev) — extensión aditiva candidata en 019-hardening.
- **Paridad del set de IDs a upstream NO cubierta:** el merge enriquece el subset declarado sin ampliarlo (S2 aclarado) — decisión pendiente en el spec de **019-004**.
- **OpenRouter anónimo vs autenticado sigue SIN contrastar** (deuda heredada de 002; pendiente de evidencia con key configurada).
- **Anti-bias degradado (A2, 3ª recurrencia en el epic):** reviewer e implementer en la misma familia de modelo — revisar instalador/routing de perfiles.
- **Flake `TestE2E010002_TTLExpiry`:** test TTL del epic 010 sensible a carga bajo `-race` en suite completa (pasa aislado y en re-corrida). Candidato a hardening.

### Próxima feature en cola

- **019-004-atomic-write-validate** (epic 019): serialización del IR `catalogmerge.Plan` → config.yaml con write atómico + validación `config.Parse` pre-commit + binario `cmd/mofgw-sync`. Decide: regeneración acotada vs. edición estructural (yaml.v3 no preserva comentarios). Backlog 019-005..007 queued. Epic 020 planificado y en cola tras el cierre de 019.

### 11 Sep 2026 — Feature 019-002-fetch-zen-go (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-002-fetch-zen-go MERGED — SEGUNDA del epic 019 (regla: catálogo upstream es copia verificada por fuente, no declaración).** Dos fases: **Fase A (refactor, P1-P3)** extrae el mecanismo de 019-001 a paquete genérico `internal/modelscache` (`Spec{BaseURL, Parse, AuthEnv, KnobEnv, Source}`, `Fetch[T]`/`Store[T]` con generics, retry/lock/digest/atomic/TTL **idénticos** incl. decisiones HITL B1/B2 de 019-001: timeout NO reintenta, sidecar-first con rename atómico); `internal/modelsdev` quedó como facade con API bit a bit (aliases de tipo, Spec compuesto, gate: suite byte-intacto verificado). **Fase B (fuentes, P4-P15):** paquete `internal/upstream` con `FetchZen`/`FetchGo` (ModelList fiel por índice; **70 y 37 items reales verificados**), `FetchOpenRouter` (`OpenRouterCatalog`: **443 modelos**, pricing strings→float64, `supported_parameters` presente en todos — complementa la deuda de models.dev para 019-003, `architecture.modality`, `top_provider`, **16 aliases `~` verbatim**), auth condicional OpenRouter (env `OPENROUTER_API_KEY` por llamada, valor jamás en logs), caches separados por fuente (`zen-models.json`/`go-models.json`/`openrouter-models.json`), knob `MOFGW_DISABLE_UPSTREAM_FETCH` aislado.
- **Decisión review (HITL aceptada): prefijo de errores `modelscache:` aceptado y documentado.** El bloqueante cosmético de la review quedó resuelto no cambiando el string sino reencuadrando el contrato de identidad: la identidad contractual de errores tipados es `errors.Is`/`errors.As`, no el prefijo textual. N.1 (conteo stale del audit) aplicado. **15/15 P PASS.**
- **Suite final: 806 tests / 32 paquetes `-race` verde.** Commits: `1eddbed` RED, `0ba81ab` fix fixture (AP-4, sesión aislada test-writer), `7baa598` GREEN (+fix top_provider), `648188f` review. `modelsdev_test.go` intacto (gate byte-idéntico).
- **Cero impacto servidor (I1 de 019-001 se mantiene):** `modelscache` + `upstream` no importan `config/proxy/router/cmd`; `/v1/models` sigue armándose desde `config.yaml`. El contrato cross-feature se GENERALIZA: consumidores 019-003/007 dependen del motor genérico, no de models.dev específico.

### Deuda técnica detectada

- **OpenRouter anónimo vs autenticado SIN contrastar** (sin key en entorno): la rama con key (`OPENROUTER_API_KEY`) está implementada y no-loguea el valor, pero no hay evidencia empírica de la diferencia de shape/catálogo. Verificar en 019-003 (mapeo real con key configurada).
- **Vocabulario `supported_parameters` de OpenRouter ≠ models.dev:** OpenRouter lo trae en los 443 modelos; models.dev lo omite (0 ocurrencias). El mapeo/normalización es trabajo concreto de **019-003 (R5)**.
- **Rate limits OpenRouter por monitorear** en **019-006** (systemd-timer + observabilidad).
- **Sin import-linter en el repo** (deuda de tooling vigente): I1 sigue verificándose manualmente.
- **Anti-bias degradado (A2 vigente):** el arnés mantiene reviewer e implementer en la misma familia (`mofgw/deepseek-v4-flash`). Revisar instalador/routing (process-log 019).

### Próxima feature en cola

- **019-003-merge-provider-catalog** (epic 019): merge de models.dev por IDs autorizados → providers[].models/pricing/model_metadata; deriva `supported_parameters`. **Dependencias resueltas por 019-002:** fuente de IDs (70/37/443 reales verificados) + catálogo rico OpenRouter que complementa el vacío de models.dev. Incluye el mapeo R5 de vocabulario. Backlog 019-004..007 queued.

### 11 Sep 2026 — Feature 019-001-fetch-modelsdev (epic 019-provider-sync-automation) MERGED

### Decisiones relevantes

- **Feature 019-001-fetch-modelsdev MERGED — PRIMERA del epic 019 (regla de la epic: catálogo upstream es copia verificada, no declaración).** Paquete nuevo `internal/modelsdev` (modelsdev.go/fetch.go/store.go): fetch + cache en disco del catálogo de models.dev (`GET https://models.dev/api.json`; payload real verificado 4.59 MB / 213 providers / 7.711 modelos). Cache en `os.UserCacheDir()/mofgw/models-dev.json` (env `MOFGW_CACHE_DIR` overridea el path base → `<dir>/models-dev.json`; fallback `os.TempDir()/mofgw` si `UserCacheDir` falla — review #4, sin test del override por simular HOME roto frágil). TTL default 5m por **mtime**. Lock `syscall.Flock` `LOCK_EX|LOCK_NB` sobre **`<path>.lock` dedicado** (nunca el propio cache: el rename atómico invalidaría locks sobre él, I10), lock-first antes del TTL-check. Sidecar sha256 `<path>.sha256` con skip de writes byte-idénticos (lección externa opencode PR #44282). Escritura atómica temp+rename replicando `internal/metrics/persist.go` — **sidecar primero, cache último (punto de commit)**, decisión B2. Retry **2 intentos** backoff ~500ms SOLO transporte network/5xx; 4xx SIN reintento; **timeout NUNCA reintenta** (B1). Fail-soft con cache / fail-loud sin cache. Knob `MOFGW_DISABLE_MODELS_FETCH=1` → error tipado `ErrFetchDisabled` por llamada (no fetch omitido silencioso). UA `build.UserAgent`. Logs slog (`fetch_ok/fetch_failed/cache_hit/skipped_identical/lock_busy`). Errores tipados `errors.Is`: `ErrFetchDisabled` / `ErrLockBusy` / `FetchError{Type: timeout|network|canceled|status|parse}`.
- **Decisión review B1 (HITL delegado — Ofap): P4 vs D9 resuelto — `timeout` NUNCA es retryable.** Un intento colgado ya consumió su presupuesto; reintentarlo duplicaría la latencia (2×10s). D9 se reinterpreta: solo errores de transporte `network` (conn refused/reset/5xx) son transitorios. Fijado con RED discriminante (`TestFetch_TimeoutBudget_NoRetry`, c0489bc, falla hits==2) + fix del implementer (65503d4: `retryable timeout→false`, `classifyTransport` distingue `DeadlineExceeded` de `Canceled`).
- **Decisión review B2 (I6): par cache+sidecar NO atómico cross-file, aceptado.** Se adopta sidecar-first: sidecar temp+rename PRIMERO, cache ÚLTIMO (commit). Fallo del sidecar → cache intacto + `(false, err)` (I6 consistente). Fallo del cache tras sidecar OK → el próximo refresh detecta digest ≠ sidecar y reescribe ambos (auto-cura); interim `Get()` sirve el cache viejo (aceptable). La atomicidad real cross-file queda fuera de alcance (el spec impone 2 archivos).
- **Cero impacto en el servidor (I1):** el paquete no importa `internal/config`, `internal/proxy`, `internal/router` ni `cmd/*`; `/v1/models` se sigue armando desde `config.yaml` (`SetPricing`/`SetModelMetadata`, main.go:244-258). El binario `cmd/mofgw-sync` NO se construyó acá (D2 lo posterga a 019-004).
- **Contrato cross-feature congelado:** API pública del paquete (`Fetch(ctx, opts...)` variádico con `WithBaseURL/WithClient/WithLogger`, `ParseCatalog`, `Catalog{Providers map[string]Provider}` tipado, `Store{Path,TTL,Lock}` + `Refresh/Get`, `FetchTimeout=10s`, `DefaultCacheTTL=5m`) queda como interfaz para los specs hermanos 019-002/003/007. Primer uso en el repo de `os.UserCacheDir` y `syscall.Flock`.
- **Suite final: 763 tests / 30 paquetes `-race` verde**, go vet + gofmt limpios. Commits: `f99c061` RED (19 tests B1-B11, RED por compilación), `226728c` GREEN, `c0489bc` POST-AUDIT RED discriminante (B1), `65503d4` fix review.

### Deuda técnica detectada

- **Test del override `MOFGW_CACHE_DIR` NO escrito** (review #4 parcial): se desestimó por fragilidad (simular HOME roto); el fallback de `DefaultCachePath` a `os.TempDir()` queda cubierto. Documendar en hardening si el path se vuelve crítico.
- **Sin import-linter en el repo** → I1 verificada manualmente (deuda de tooling, abstención A3 de la review).
- **Anti-bias degradado (A2):** el arnés corrió el reviewer en `mofgw/deepseek-v4-flash` = **misma familia que el implementer**. La Capa 2 (HITL) validó los bloqueantes B1/B2 por inspección directa del código antes de decidir. *Mejora candidata:* el perfil de `cdad-reviewer` no fija modelo distinto — revisar instalador/routing (process-log 019).
- **Deuda del epic vigente:** `yaml.v3` no preserva comentarios en round-trip → decisión de regeneración vs. edición estructural adelantada a 019-004; hot-reload del epic 017 sigue pausada → `019-005` default = restart del service systemd.
- **`docs/progress.md` y `docs/systemPatterns.md` NO existían** (deuda desde epic 010 / 018-001) → **creados en este ciclo** (primer bootstrap del Memory Bank completo).

### Próxima feature en cola

- **019-002-fetch-zen-go** (epic 019-provider-sync-automation): fetch de las listas autorizadas Zen/Go/OpenRouter (condicional a API keys configuradas). Sin dependencias. Backlog 019-003..007 queued. Coordinar vía `cdad-epic`.

### 03 Sep 2026 — Feature 018-001 (x-opencode-session + UA upstream) MERGED — ciclo CDAD completo en un día

- **Motivación externa con deadline:** Anomaly (OpenCode Go) exige header `x-opencode-session` (valor opaco estable por conversación) — sin él puede error desde 2026-09-06; deepseek-v4-flash vía ruta Go ya daba HTTP 400 (precedente hermes-agent #81584). mofgw era el "Go HTTP client" sin UA propio (`Go-http-client/1.1` = "broad user agent" prohibido por docs de Anomaly).
- **Solución:** knob declarativo `opencode_session: true` por provider (precedente `thinking_path`, I1 nunca inferir) → inyección de `x-opencode-session` con precedencia `X-Session-Id` entrante → fallback `client_id`, sanitizado `[A-Za-z0-9._-]`→`_`, clamp 128, vacío→omitir+warn (sin valor en log). `User-Agent: mofgw/<version>` global (constante `internal/build.Version`, var para ldflags). Plumbing `RequestMeta` por context (stateless, I6).
- **Invariantes clave verificadas en review:** I2 no-leak (header solo a providers con knob; responses/health/embeddings jamás inyectan), I3 body intacto, I4 sin impersonar CLI (rechazo comunitario explícito).
- **Ciclo:** spec aprobado por Pablo → RED (12 AssertionError + B9 build-fail con precedente) → GREEN 10/12 con 2 tests RED defectuosos corregidos por test-writer aislado (AP-4) → POST-AUDIT verde 733/29 -race → review APPROVE_WITH_NITS (0 bloqueantes; nits #1/#2 aplicados: comentario stale + warn silencioso en endpoints sin meta).
- **Commits:** 8547b3f (spec) → 77b79ae (aprobación) → 16619b9 (RED) → 3d19fc9+3dedb40 (GREEN) → e5ccf1d (POST-AUDIT) → 920472a (review) → 187a318 (nits). Suite final: **733 tests / 29 paquetes -race verde**.
- **⚠️ Pendiente operativo: DEPLOY antes del 2026-09-06** — la feature no protege hasta que las instancias mofgw en producción carguen config con `opencode_session: true` en los providers de la familia opencode (go-*) y se redeployen. **Deuda registrada:** `docs/progress.md` no existe (bootstrap pendiente desde epic 010).
- **Nota de proceso:** runtime `delegate` de opencode roto durante el ciclo (3 timeouts, 0 tokens — verificado: ninguna request llegó a mofgw; `task` funciona). Architect/scribe inline con disclosure; reviewer vía agente general read-only conductual. Desviaciones documentadas en spec/review.

### 22 Ago 2026 — EPIC-016 (mofgw-client-config) CERRADO — 4/4 features done + integración E2E verificada + closure

### Decisiones relevantes

- **EPIC-016 (mofgw-client-config) CERRADO — 4/4 features done + integración cross-feature E2E verificada.** Endpoint `GET /v1/client-config?client=<id>` que devuelve el **fragmento de config del provider mofgw** listo para insertar por cliente (opencode/openclaw/zot), generado desde el catálogo real, no hardcodeado. **Suite completa 697 tests `-race` en 27 paquetes** verde, cero regresión. Epics: 016-001 (core) → 016-002/003/004 (adapters) → integración wiring `a8af57e` + E2E `7424b83` → closure `docs/epics/016-mofgw-client-config/closure.md`.
- **Los clientes usan el MISMO IR pero cada uno tiene su shape/sintaxis de env-ref — verificado por research, no asumido:** opencode = `{env:VAR}` template string + `npm:"@ai-sdk/openai-compatible"`; openclaw = `${VAR}` + JSON5 `~/.openclaw/openclaw.json` (`models.providers.mofgw`, `models[]` array, `api:"openai-completions"`); zot = **sin campo apiKey** (env derivada `MOFGW_API_KEY`) + `$ZOT_HOME/models.json` (`providers.mofgw`, `api:"openai"`). El IR (`ConfigIR{ClientID,BaseURL,KeyEnvRef,Models}`) es el contrato cross-feature; reusa `modelCatalogEntry` (misma fuente que /v1/models).
- **Invariantes del epic sostenidos en los 4 features:** I1 key solo env-ref (o ausente en zot), nunca literal; I2 catálogo fuente de verdad viva; I3 aditivo cero regresión; I4 base_url siempre del knob. Patrón adapter consolidado: renderer puro determinista (encoding/json sort), guard >0 + fallback-rule (ausente≠0) — lección del hallazgo Importante de 016-002 aplicada desde RED en 003/004 (P12).
- **Retrospectiva:** research > suposición (corrigió shapes en opencode y openclaw); el canal `delegate` del arnés estaba roto → resuelto con `task`+carrier que ejecuta el contrato del rol (usado en todo el ciclo); review atrapó 1 Importante real en 002 (limit:0) no repetido.
- **Deuda llevada (closure-016):** registro de adapters declarativo en main.go (patrón claro para futuros clientes); campos opcionales por-cliente (reasoning/input/cost/pricing) no emitidos por minimalismo → enriquecer requiere verificar contra catálogo real (epic hardening futuro); `models.mode=="merge"` de openclaw sin postcondición propia; E2E real manual contra un cliente real pendiente (opcional, sin fixture-drift).

### Próxima en cola

- **Epic 016 CERRADO.** Sin next feature dentro del epic. Posibles siguientes: epic de enriquecimiento (registro de adapters by-config + campos opcionales por-cliente desde catálogo), o hardening E2E real con clientes. Coordinar vía `cdad-epic`.

### 22 Ago 2026 — Feature: 016-004-adapter-zot (epic 016-mofgw-client-config) MERGED

### Decisiones relevantes

- **Feature 016-004-adapter-zot MERGED — el TERCER y ÚLTIMO adapter del epic 016 (feature 4/4, epic completo).** Un `clientconfig.Renderer` en paquete nuevo `internal/clientconfig/zot/` que serializa un `ConfigIR` en el **fragmento config del provider `mofgw`** para zot. Registrado vía `clientconfig.Register("zot", zot.Renderer{})`. Suite completa **691 tests `-race` en 27 paquetes** verde, `go vet` limpio. Commits: spec `8513903`, RED `99e608b`, GREEN `8ea10ff`, review `ba459f4`.
- **Research (qué es "zot"):** `patriceckhart/zot` (zot.sh), "Yet another coding agent harness", CLI coding-agent Go. Config custom providers en `$ZOT_HOME/models.json` (shape `UserModelsFile`: `{"providers":{"<id>":{"baseUrl","api","models":[array de {id,name?,contextWindow?,maxTokens?}]}}}`). Agrupado con opencode/openclaw en el epic; research-context-patterns lo perfila como cliente real de mofgw.
- **Desviación I1 deliberada (clave de esta feature):** el formato de zot **NO tiene campo `apiKey`** — `models.json` jamás almacena secrets; zot resuelve la key en runtime via `--api-key` → env **derivada** `UPPER_SNAKE(provider)_API_KEY` → auth.json. Para el provider "mofgw" es **`MOFGW_API_KEY`**. El adapter emite CERO campo de key (ni template `{env:}` ni `${}`), más fuerte en I1. `IR.KeyEnvRef` no se emite y **vacío NO es error** (vs 002/003 que emiten template). Para alinear el knob `client_config.key_env`, el operador setea `MOFGW_API_KEY`.
- **Shape del adapter:** top-level `"providers"` (no "models"), `api: "openai"` (no "openai-completions"), `models[]` ARRAY `{id,name?,contextWindow?,maxTokens?}`. Para `BaseURL` vacío → error; guard SOLO en BaseURL (no KeyEnvRef, desviación I1). Replicó modeloFragment/metaString/metaInt verbatim del patrón review-cleaned de 003, con guard >0 y fallback-rule (P12).
- **Postcondiciones P1-P12 (16 tests)** cubren: JSON válido top-level providers, baseUrl fiel, sin apiKey/template/secret, api "openai" + key "mofgw", modelos+limits de Meta, sin ids extraños, models vacío→[], solo-id sin-metadata, guard solo BaseURL (KeyEnvRef vacío no-error), registro API pública + cleanup scoped, determinismo, guard>0 + fallback. **Review APPROVE, 0 bloqueantes.** 1 FYI opcional (display_name vacío sin caso directo) no aplicado (copia verbatim del patrón validado).
- **★ EPIC 016 COMPLETO — 4/4 features MERGED:** 001 core (/v1/client-config + IR + registry) + 002 opencode + 003 openclaw + 004 zot. Suite completa **691 tests `-race` en 27 paquetes** verde. Pendiente a nivel epic (E3 integración + E4 closure): registrar los 3 adapters en producción (wiring en main vs init()), el E2E del endpoint con client=opencode/openclaw/zot (fragmentos reales desde /v1/client-config), y el closure. Coordinar vía `cdad-epic`.

### Próximo paso (epic)

- **Integración cross-feature del epic 016 (E3):** registrar los adapters en producción + E2E del endpoint `GET /v1/client-config` con cada client (opencode/openclaw/zot) devolviendo el fragmento fiel al catálogo real. Luego closure del epic (E4). Coordinar vía `cdad-epic` (chat del epic).

### 22 Ago 2026 — Feature: 016-003-adapter-openclaw (epic 016-mofgw-client-config) MERGED

### Decisiones relevantes

- **Feature 016-003-adapter-openclaw MERGED — el SEGUNDO adapter del epic 016 (feature 3/4).** Un `clientconfig.Renderer` en paquete nuevo `internal/clientconfig/openclaw/` que serializa un `ConfigIR` en el **fragmento config del provider `mofgw`** para OpenClaw. Registrado vía `clientconfig.Register("openclaw", openclaw.Renderer{})`. Suite completa **675 tests `-race` en 26 paquetes** verde, `go vet` limpio. Commits: spec `124594a`, RED `d4eb6cd`, GREEN `8eb16e6`, review `4dd5459`.
- **Corrección de research (crítico): OpenClaw NO usa `openclaw.conf` ni TOML/YAML.** Es **`~/.openclaw/openclaw.json`** (JSON5) y la env-ref en config es template STRING **`"${VAR}"`** — DISTINTA al `{env:VAR}` de opencode, y NO objeto SecretRef. Shape provider: `models.providers.mofgw.{baseUrl, apiKey, api:"openai-completions", models[]}` donde `models[]` es **ARRAY de objetos** `{id, name?, contextWindow?, maxTokens?}` (no mapa keyed como opencode). Fuentes: https://docs.openclaw.ai/gateway/configuration, model-providers.md, config-tools.md.
- **Replicó fielmente el patrón review-cleaned de 016-002 (opencode), con la lección P12 incorporada desde RED:** guard `>0` (presente-pero-≤0 se omite), fallback `max_context_length`/`max_completion_tokens`, `display_name` desde Meta, registry cleanup **scoped** (borra solo "openclaw", no todo el registry global — lección del hallazgo de 002). Divergencias intencionales correctas: `${VAR}` (vs `{env:VAR}`), `models[]` array (vs mapa), `baseUrl`/`contextWindow`/`maxTokens`/`api`/`mode`/`providers`.
- **Postcondiciones P1-P12 (16 tests RED → 16 green)** cubren: JSON válido top-level "models", baseUrl fiel, apiKey `${VAR}` string sin secret, api/providerKey, modelos+contextWindow/maxTokens desde Meta, sin ids extraños, models vacío→[], solo-id sin-metadata, error si BaseURL/KeyEnvRef vacíos, registro API pública, determinismo, y P12 fallback-rule + guard>0 (4 casos). **Review APPROVE, 0 bloqueantes, 0 importantes.** Único FYI: `models.mode=="merge"` sin postcondición propia (el impl lo emite; default de OpenClaw) — documentado, no aplicado (churn marginal).
- **Notas para el epic (3/4 done):** patrón consolidado para 004 (zot). Siguiente: **016-004-adapter-zot** (último). Sin deuda bloqueante. Pendiente a nivel epic (integración): registrar los adapters en producción (wiring en main vs init()) y el E2E del endpoint con client=opencode/openclaw.

### Próxima feature en cola

- **016-004-adapter-zot** (epic 016-mofgw-client-config): renderer (`clientconfig.Register`) que serializa el IR al fragmento de config para zot (tercer adapter; replicar patrón de 002/003). Verificar el shape real del config de zot (docs/format) + tests contra ese shape. Queda SOLO 004 pendiente (001-003 done). Coordinar vía `cdad-epic`.

### 22 Ago 2026 — Feature: 016-002-adapter-opencode (epic 016-mofgw-client-config) MERGED

### Decisiones relevantes

- **Feature 016-002-adapter-opencode MERGED — el PRIMER adapter concreto del epic 016 (feature 2/4).** Un `clientconfig.Renderer` en paquete nuevo `internal/clientconfig/opencode/` que serializa un `ConfigIR` en el **fragmento JSON del provider `mofgw`** listo para insertar bajo la clave `provider` de `opencode.json`. Registrado vía `clientconfig.Register("opencode", opencode.Renderer{})`. Suite completa **659 tests `-race` en 25 paquetes** verde, `go vet` limpio. Commits: spec `af4d756`, RED `842f183`, GREEN `d5dcaa1`, post-review fixes + review `15bd538`.
- **Hallazgo de research (crítico para el shape): opencode usa template STRING `"{env:VARNAME}"` para apiKey, NO un objeto `{"env":…}`.** El renderer emite `options.apiKey = "{env:" + ir.KeyEnvRef + "}"` — nunca el literal ni un objeto. Fuente: https://opencode.ai/docs/config/ (Env vars). También requiere `npm: "@ai-sdk/openai-compatible"` para custom providers OpenAI-compatible, `name: "mofgw"`, `options.baseURL = IR.BaseURL`, y `models` derivados del catálogo con `limit:{context,output}` opcional.
- **Serialización pura (adapter), sin I/O ni dependencias nuevas:** entrada ConfigIR, salida `[]byte` JSON determinista (encoding/json ordena map keys alfabéticamente ⇒ byte-idéntico entre llamadas, P11). Modelos derivados SOLO de `IR.Models` (I2, cero ids hardcodeados); `limit` emitido solo si la metadata está presente Y > 0 (guarda `>0`, fallback-rule ausente≠0 — fix post-review del hallazgo Importante #1); `name` desde `Meta["display_name"]` (opcional), alineado con el fixture (rama viva).
- **Postcondiciones P1-P11 (13 tests RED → 19 tras review)** cubren: JSON válido con top-level "mofgw", baseURL fiel al IR, apiKey env-ref string sin secret, npm/name, modelos+limits desde Meta, sin ids extraños, models vacío→{}, sin-limit sin-metadata, error si BaseURL/KeyEnvRef vacíos, registro via API pública, determinismo. Review APPROVE con **1 Importante resuelto** (limit emitía 0 en claves presentes-con-0, alcanzable desde `modelCatalogEntry`) + fallback-trigger test + rama name + registry scoped (solo "opencode", para no pisar futuros 003/004).
- **Notas para el epic (2/4 done):** este adapter define el patrón que seguirán 003 (openclaw) y 004 (zot): renderer puro + `clientconfig.Register("<client>", renderer)`. El registro explícito en wiring (`main`) vs `init()` del subpkg queda a decidir cuando haya más de un adapter; hoy `Register` se llama en tests y el fragmento queda disponible via endpoint cuando se registre en prod. Siguiente: **016-003-adapter-openclaw** (then zot `004`). Sin deuda bloqueante.

### Próxima feature en cola

- **016-003-adapter-openclaw** (epic 016-mofgw-client-config): renderer (`clientconfig.Register`) que serializa el IR al fragmento de config para `openclaw.conf` (formato de config del cliente OpenClaw). Consume el IR definido en 001 y replica el patrón del adapter 002 (opencode). Verificar el shape real del config de OpenClaw (docs/format) + tests contra ese shape. Quedan 003-004 pendientes (001-002 done; 003/004 paralelizables entre sí, ambas dependen de 001). Coordinar vía `cdad-epic`.

### 22 Ago 2026 — Feature: 016-001-config-renderer-core (epic 016-mofgw-client-config) MERGED

### Decisiones relevantes

- **Feature 016-001-config-renderer-core MERGED — el CORE del epic 016 (mofgw-client-config), feature 1/4.** Nuevo endpoint `GET /v1/client-config?client=<id>` que devuelve el **fragmento de config del provider mofgw** listo para insertar en un cliente soportado (opencode/openclaw/zot), generado desde el catálogo real, no hardcodeado. Entrega el tronco: paquete nuevo `internal/clientconfig` (IR tipado + interfaz Renderer + registry público), knobs `client_config:{base_url,key_env}`, handler + wiring en proxy, auth detrás de `/v1`. Los adapters (registry) están **VACÍOS en producción** — opencode/openclaw/zot son 016-002/003/004. Suite completa **640 tests `-race` verde en 24 paquetes** (baseline pre-feature 330 pass/2 paquetes → 640/24, cero regresión), `go vet` limpio. Commits: spec `a7928e0`, test-audit `8216777`, RED `a04cdf8`, GREEN `3f28fd3`, review `75a7814`.
- **IR = contrato compartido de todos los renderers:** `ConfigIR{ClientID, BaseURL, KeyEnvRef, Models []ModelEntry}` donde `ClientID` sale de `auth.ClientIDFrom(ctx)`, `BaseURL` es el knob vivo, `KeyEnvRef` es la ref a env var (NUNCA el valor), y `Models` reusa `modelCatalogEntry` (misma fuente de verdad en memoria que `/v1/models`). Registry = **API pública del paquete** (mapa exportado `Renderers` + `Register`/`SupportedList`/`Lookup`) — registrar un renderer de prueba para ejercitar el path 200 no es mock sobre plumbing, es el contrato (P10).
- **Endpoint y orden de precedencia (runnable 400-antes-503):** (1) `client` ausente/vacío → **400** `invalid_request_error` (se evalúa PRIMERO — independiente de config); (2) `base_url` vacío → **503** `server_error` `"client_config.base_url not set"` sin fallback; (3) cliente no registrado → **404** `not_found_error` con `supported: <lista viva del registry>`; (4) `Renderer.Render` error → **500**; (5) ok → **200** con los bytes del renderer. Errores en envelope `openAIError` `{"error":{message,type,code}}`; **401** `invalid_api_key` lo emite `auth.Wrap` (ruta bajo `/v1/`, fuera de `publicPrefixes`), no el handler. Fix M1 de la review: la prosa del spec §2.2 (que listaba 503-primero) se alineó a 400-antes-503, coherente con la impl y los tests RED (P2 golpea client vacío + base_url sin setear esperando 400).
- **Knob config aditivo off-safe (I3/I4):** `ClientConfigConfig{base_url, key_env}`, default `{BaseURL:"", KeyEnv:"MOFGW_KEY"}`. **`BaseURL` vacío NO falla el arranque** (a diferencia de telemetry/registry que exigen `file`) — es el **503 de runtime**, coherente con I4 (no fail-fast, off-safe). `key_env` viaja como **referencia**, no se resuelve ninguna env var en `resolveKeys()` (I1/P8). Existing struct `ClientConfig` (cliente autenticado 001-007) intacto; el knob nuevo usa el nombre exacto `ClientConfigConfig` para no colisionar (bandera de test-audit). Setter pre-tráfico `SetClientConfig(baseURL,keyEnv)` (patrón `SetContextAnalysis`, inmutables post-setter), ruta `GET /v1/client-config` en `Handler()`, wiring `srv.SetClientConfig(cfg.ClientConfig...)` en `cmd/mofgw/main.go`, documentado en `config.example.yaml` (líneas ~223-236).
- **Invariantes sostenidos (I1-I4):** **I1** key nunca literal — el fragmento/respuesta solo llevan `KeyEnvRef`, grep de literales `sk-` en implementación sin resultado, nunca se resuelve ni emite el valor; **I2** catálogo fuente de verdad viva — `configCatalogModels()` reusa `modelCatalogEntry` (misma función) con idéntica iteración/`seen`-map que `handleModels`, sobre `s.providers` (`interface{ Models() []string }`, patrón 13-003 D3/P6); **I3** aditivo cero regresión — única ruta nueva, `/v1/models` y resto de `/v1/*` intactos; **I4** `base_url` vacío → 503 explícito, no default hardcodeado ni host alternativo, runtime no fail-fast. Cero llamadas HTTP salientes nuevas → ADR-005 no aplica.
- **Review two-layer APPROVE (0 bloqueantes, 0 importantes):** familia distinta al implementer; P1-P10 todos con test (P6/P7 combinado `IRMatchesModelsAndKnobs`, P8/P10 en `clientconfig` + proxy + config), invariantes I1-I4 e I2/P10 auditaron PASS, relevance audit sin sobrantes, sin mocks de plumbing (renderers de prueba vía API pública `Register`, harness real). **M1 (prose spec §2.2)** fix aplicado por el orquestador; nits **N1** (Content-Type seteado 2 veces) y **N2** (bucle `seen` duplicado ~10 líneas en `configCatalogModels`, no viola I2) **no aplicados** — decisión de scope (inofensivos, evita churn post-aprobación). FYI: tests `TestRED_*` permanentes de GREEN (convención del repo); `Content-Type: application/json` quedará para 200 aunque un futuro adapter quiera otro (lo podrán ajustar 016-002/003/004).
- **Notas para el epic (1/4 done):** este core define el IR que consumen los 3 adapters (002/003/004). El registry queda vacío en prod hasta 016-002. Siguiente: **016-002-adapter-opencode** (then openclaw `003`, zot `004`). Sin deuda bloqueante; nits N1/N2 dejados como FYI de refactor futuro.

### Próxima feature en cola

- **016-002-adapter-opencode** (epic 016-mofgw-client-config): renderer (`clientconfig.Register`) que serializa el IR al fragmento JSON para `opencode.json` (responsabilidad de provider HTTP compatible con la estructura que opencode ya entiende). Consume el IR definido en 001; verificar el shape real del config de opencode (context7/docs) + tests contra ese shape. Quedan 002-004 pendientes (001 done; orden: opencode, openclaw `003`, zot `004`; 003/004 paralelizables entre sí, todas dependen de 001). Coordinar vía `cdad-epic`.

## Estado del programa

**✅ Programa mofgw 10/10 epics DONE (001-010) + EPIC-011 (mofgw-odoo) CERRADO 12 Ago 2026 — 9/9 features done + integración E2E verificada + closure completado + deudas resueltas. mofgw es el 100% del proveedor de IA de Odoo (reemplazo total de OpenAI, verificado: chat + embeddings + server actions).**

| Epic | Features | Status | Commit evidence |
|------|----------|--------|-----------------|
| EPIC-001 (core) | 7 features | ✅ DONE | e426f15d |
| EPIC-002 (resiliencia) | 4 features | ✅ DONE | 5966c5f4 |
| EPIC-003 (cache) | 1 feature | ✅ DONE | bb6367b→e5a9a16 |
| EPIC-004 (escala) | 2 features | ✅ DONE | (05 Ago) |
| EPIC-005 (ops) | 5 features | ✅ DONE | (05 Ago) |
| EPIC-006 (observabilidad) | 2 features | ✅ DONE | 119fe9d→2846c12 |
| EPIC-007 (catálogo) | 3 features | ✅ DONE | (06 Ago) |
| EPIC-008 (contexto) | 3 features | ✅ DONE | (06 Ago) |
| SEC-001 (hardening) | 4 fixes | ✅ DONE | b368656 |
| EPIC-009 (contexto-análisis) | 3 features | ✅ DONE | da8446c (E2E cross-feature) + closure-009.md |
| EPIC-010 (catálogo-fiel) | 2 features | ✅ DONE | (11 Ago, deploy prod) |
| EPIC-011 (odoo) | 001-009 DONE (9/9) | ✅ DONE + INTEGRACIÓN + CLOSURE | closure-011.md |
| EPIC-012 (odoo-component) | versionar mofgw_ai en odoo/ | ✅ DONE + CLOSURE | 8884036 |

**Suite:** 19 paquetes, **499 tests verde con `-race`** (mofgw Go; el módulo Odoo mofgw_ai tiene 14 tests de módulo que pasan en VPS). vet limpio.
**EPIC-012 (mofgw-odoo-component) CERRADO — versionar `mofgw_ai` como componente Odoo distribuible:** el fuente del módulo Odoo `mofgw_ai` (epic 011) quedó versionado en el repo bajo `odoo/mofgw_ai/` (componente opcional para quien use Odoo 19 con mofgw como proxy de IA). Antes era solo una copia instalada en staging sin versionar. Commit `8884036`, closure `docs/epics/closure-012.md`.
**EPIC-011 (mofgw-odoo) CERRADO — 9/9 features + integración E2E verificada:** Odoo 19 enterprise usa mofgw como único proveedor de IA (reemplazo total de OpenAI). Lado Go: responses (001), structured output (002), tool-calling (003), file-attachments (004), web-search grounded (005), embeddings (006). Lado Odoo (módulo local mofgw_ai): embedding-vector (007), odoo-provider (008, monkeypatch in-place). staging-enterprise (009). **E2E real verificado:** Odoo (staging-instance) → provider mofgw → túnel → mofgw Go local → deepseek-v4-flash, /v1/responses 200 "PONG", 0 llamadas a api.openai.com.
**Deploy:** systemd user service activo en puerto 3369, providers reales (acct1, acct2, qwen/bailian, zen free).

## Decisiones recientes (cronología inversa)

### 18 Ago 2026 — Feature 014-001-registro-unificado MERGED + EPIC-014 CERRADO

### Decisiones relevantes

- **Feature 014-001-registro-unificado (única feature del EPIC-014 mofgw-registro-unificado) MERGED + CERRADA.** Registro **append-only en disco (JSONL)** del ciclo completo de cada request que llega a la cadena de routing: **un evento por intento** (`attempt`: request_id, ts RFC3339 ms UTC, client, provider, model, outcome ok|fallback, cause, status, attempt, retries) + **un evento terminal** (`terminal`: outcome success|error, error_code, status, final_provider, tokens{prompt,completion,cache,reasoning}, cost_usd, stream). Es la **fuente de verdad con resolución temporal** que no existía: telemetry.jsonl (009-000) se loguea al ENTRAR (sin provider/tokens/costo) y /metrics+state.json acumulan desde arranque (sin ventana). El esquema del evento es el **contrato** que consume un epic EXTERNO de consolidación/visualización (fuera de mofgw; lee el JSONL, no toca el proxy).
- **Arquitectura (aditiva, off por default):** nuevo paquete `internal/registry` (Writer síncrono best-effort, mutex thread-safe, `NewWriter` O_APPEND|O_CREATE|O_WRONLY 0o640 con fail-fast de arranque, `.Attempt()`/`.Terminal()`) + knob top-level `registry: {enabled, file}` (default `{false,""}`, `enabled=true`+`file=""` → error de validación). Emisores: `emitAttempt` en `internal/router/router.go` (Complete y stream, al concluir cada visita con outcome/cause/status/attempt/retries — reusa `classify` para `cause` y `ChainError.Code` para `error_code`), y `emitTerminalSuccess`/`emitTerminalError` en `internal/proxy/proxy.go` junto a `recordCacheTokens` (4 call-sites chat + responses.go + embeddings.go) y `handleChainError`. `cost_usd` = MISMA fórmula `estimateCost(model, miss, completion, hit)` (D9) que /metrics. Privacidad metadata-only por construcción (D1/I2): el writer serializa SOLO el esquema, nunca contenido de prompts/respuestas/tools/headers/keys (verificado con grep negativo, C15). Identidad por ctx: `logging.RequestID(ctx)` + `auth.ClientIDFrom(ctx)`.
- **Aditividad total:** off por default (P2), no toca ruteo/fallback/cache/sticky/singleflight/metrics/telemetry 009-000 (conviven, P17); eventos post-decisión; fallo de escritura jamás altera la respuesta (best-effort runtime, fail-fast solo arranque P16/P3).
- **Suite completa `go test ./... -count=1 -race` 622 passed in 23 packages** (nota: 1 flake estadístico aislado en `TestE2E010002_TTLExpiry` de la feature 010002, no relacionado, re-ejecución 622/622). go build OK, go vet limpio.
- **Review two-layer APPROVE (0 bloqueantes):** familia de modelo distinta al implementer; P1-P17/I1-I10/C1-C17 verificados contra spec; no auto-satisfacción (RED 32e3459 → GREEN 9f1ee84 sin relajar implementación; 4 fixes de TEST legítimos del test-writer: keysIguales sort, MaxRetries 2→1, Cooldown time.Minute). 2 observaciones no-bloqueantes: O1 (C17 endpoints responses/embeddings y P17 telemetry sin test dedicado — opcional/follow-up fuera del gate) y O2 (FYI, terminal error del follower singleflight y stream interrumpido post-byte cubiertos por simetría). Artifacto: `docs/specs/014-001-registro-unificado/review.md`.
- **Commits:** spec `1bc8f21` (aprobada Ofap HITL 18 Ago), RED `32e3459`, GREEN `9f1ee84`, review (review.md commiteada), merge+memory bank (este commit). Config `registry:` documentado en `config.example.yaml` (ya presente desde GREEN, línea ~219).
- **Decisiones D1-D15 lockeadas en el spec** (writer dedicado tipado sin slog; discriminador type attempt|terminal; ts RFC3339 ms; intento = visita concluida con attempt/retries; cause = typ de classify; alcance only-routing D7; error_code = ChainError.Code; cost_usd = estimateCost; writer síncrono best-effort; identidad por ctx; esquema v1 extensible; singleflight por request físico D14; stream interrumpido = success con tokens capturados D15). Sin ADR nuevo (feature sin nueva llamada saliente ni frontera arquitectónica; decisiones en el spec). **EPIC-014 CERRADO** (única feature; closure: `docs/epics/closure-014.md`). Siguiente en cola: el epic EXTERNO de consolidación/visualización (fuera de mofgw).

### 18 Ago 2026 — Feature: 015-001-inter-attempt-delay (knob SPOF) MERGED

### Decisiones relevantes

- **Feature 015-001-inter-attempt-delay MERGED — retardo configurable entre intentos de la cadena (`fallback.inter_attempt_delay`) ante blips transitorios del endpoint compartido (SPOF).** Nuevo knob (duration, default `0` = off, cero regresión) que, ante un fallo **transitorio** (timeout/network/connect/I-O/EOF pre-primer-byte — `transientStatus`), duerme `N` antes del siguiente intento SOLO cuando el siguiente provider **comparte `base_url`** con el que falló (cuentas del mismo proveedor caen juntas; el recorrido instantáneo no daba protección). NO aplica en `429` (cuota → salto inmediato). ctx-aware (aborta si el ctx del cliente cancela). Nuevo campo `ProviderSpec.BaseURL` (detección SPOF) + `Options.InterAttemptDelay` / `FallbackConfig.InterAttemptDelay`. Suite completa **601 tests `-race` en 22 paquetes** verde; go build + vet limpios, gofmt limpio. Commits: `602020f` spec, `804f00d` RED, `78934f7` GREEN. Feature standalone PRIORITARIA posterior a EPIC-013 (toma del worker antes que EPIC-014).
- **Arquitectura (aditiva, off por default P8):** solo agregó un knob de config + un helper condicional `interAttemptDelaySleep` (`internal/router/router.go:752-758`) en los loops `complete`/`stream`; NO toca cooldown/sticky/cache/max_retries/transparencia. `transientStatus` captura exactamente 5xx retryable + timeout/network→502 excluyendo 429. Guard `prevBaseURL != ""` → providers legacy sin `base_url` nunca retrasan. Config negativa rechazada en validación (`config.go:486`). Review two-layer APPROVE (0 bloqueantes, familia de modelo distinta al implementer; P1-P8 verificadas contra spec, sin auto-satisfacción RED->GREEN). Documentado en `config.example.yaml` (fallback.inter_attempt_delay).

### 18 Ago 2026 — Feature 015-001-inter-attempt-delay CERRADA

### Decisiones relevantes

- **Feature 015-001-inter-attempt-delay MERGED (standalone, priorizada por Pablo el 17 Ago).** Nuevo knob `fallback.inter_attempt_delay` (duration, default `0` = off, cero regresión): ante un fallo **transitorio** (timeout/network/connect/I-O/EOF pre-primer-byte) donde el siguiente candidato de la cadena **comparte `base_url`** (SPOF entre cuentas del mismo proveedor), duerme `N` antes del intento siguiente — da tiempo al endpoint a recuperarse en blips de segundos que hoy tumban todas las cuentas a la vez. Suite completa **601 tests `-race` en 22 paquetes** verde, go build + vet + gofmt limpios. Commits: `804f00d` RED (P1-P6+P8 por cdad-test-writer), `78934f7` GREEN, `bbf2f27` review.md, `5e95406` state review-done.
- **Semántica del delay — condicionado, nunca a ciegas:** aplica SOLO en fallos transitorios retryable de red del mismo `base_url`; **nunca** en `429` (cuota agotada → salto inmediato, la cuenta está exhausta no trabada) ni cuando el próximo candidato tiene **distinta** `base_url` (GO→qwen→claude-pro no se retrasa). Guard `prevBaseURL != ""` da backward-compat (providers legacy sin `base_url` nunca retrasan). Delay ctx-aware: si el ctx del cliente cancela durante el sleep, retorno inmediato. Implementación: helper `interAttemptDelaySleep` en `internal/router/router.go` compartido por ramas `complete` y `stream`.
- **No interfiere con el pipeline existente:** no toca cooldown (300s) ni sticky ni cache ni registro (EPIC-014) ni `max_retries`. Es un knob aditivo + un helper condicional, off por default, reversible (anti-scope respetado).
- **Review APPROVE (familia distinta al implementer): 0 bloqueantes.** Auditoría test↔postcondición P1-P8 todas con test, ninguno sobrante, sin mocks sobre plumbing; contrato declarado en RED (inter_attempt_delay, `Options.InterAttemptDelay`, `ProviderSpec.BaseURL`) y materializado en GREEN sin tocar tests (no auto-satisfacción). El optional (test directo en rama `stream`, hoy solo vía `complete` por simetría del helper) y el FYI (P6 asevera `err != nil` sin tipar el ChainError de cancelación) se **descartaron**: cobertura aceptable y suficiente para el contrato. Nota de proceso: el orquestador persistió el código de GREEN porque el cdad-implementer no pudo ejecutar go/git en su sandbox (edits en worktree).

### Próxima en cola

- **014-001-registro-unificado (epic 014)** CERRADA (18 Ago 2026) + **EPIC-014 (mofgw-registro-unificado) CERRADO** — feature 014-001 MERGED, review two-layer APPROVE (0 bloqueantes), memory bank actualizada, closure en `docs/epics/closure-014.md`. Siguiente paso: el epic EXTERNO de consolidación/visualización (fuera de mofgw; consume el JSONL, no toca el proxy).

### 12 Ago 2026 — Epic 013-mofgw-cli-subprocess CERRADO (provider subprocess para suscripciones de CLIs)

### Decisiones relevantes

- **EPIC-013 (mofgw-cli-subprocess) CERRADO — 4/4 features done + integración E3 verificada + closure E4.** Nuevo provider `subprocess` que ejecuta el CLI de un backend de IA como subproceso (usa la suscripción Claude Pro sin reverse-engineering de OAuth ni bridges HTTP). Suite **581 tests `-race` en 22 paquetes** verde, go vet + gofmt limpios, 0 bloqueantes de review en las 4 features. ADR-010 (provider subprocess + frontera Backend). Commits por feature: 001 (7d6737d→b9cbd33), 002 (d20c293→e5ec87d), 003 (5e14760→e21fa77), 004 (7ce359f→c2c3fd8), integración+closure (c4452cb→closure.md). `docs/epics/013-mofgw-claude-subprocess/{plan,integration,closure}.md`.
- **Arquitectura (ADR-010):** motor genérico `internal/subprocess` (sesión por cliente, serialización, exec, usage, errores ErrUpstream) + interfaz `Backend` (argv/traducción/parseo/refusal en el adapter, motor sin strings backend-específicos) + adapter `claude` (013-002) + wiring config/factory/catálogo/restricción (013-003) + resiliencia TTL/ctx-guard/env allowlist (013-004).
- **Decisiones clave del epic:** sesión por cliente (CLI sostiene historial, prompt único limpio, sin tools — D3/D4); restricción "solo agentes del usuario" a nivel router (allowlist, fallback preservado); tools OMIT (el CLI nunca las ve); `--output-format stream-json` como wire-format único (sin `--include-partial-messages`); env allowlist PATH+HOME+STUB_* (mitiga sombreado de ANTHROPIC_API_KEY); ctx-guard en `TranslateStreamOut(ctx,...)` (cambio de interfaz, fix del IMPORTANT de 013-002).
- **Deuda llevada:** smoke test real con el CLI claude (pendiente manual: --session-id, prompt stdin, markers refusal B-Q7); health check subprocess, mapeo rate-limit/auth, allowlist env configurable (diferidos); passthrough de tools y structured output (deuda del epic); knob `session_key: client+model` (requiere tocar el motor); adapters gemini/codex futuros.

### 12 Ago 2026 — Feature: 013-002-adapter-claude (epic 013-mofgw-cli-subprocess)

### Decisiones relevantes

- **Feature 013-002-adapter-claude MERGED (2/4 del epic 013).** Nuevo paquete `internal/subprocess/claude/`: implementa la interfaz `subprocess.Backend` (de 013-001) para el CLI `claude`. Suite completa **552 tests `-race` en 21 paquetes**, go vet + gofmt limpios. Commits: `d20c293` RED, `b8a211a` GREEN, `e5ec87d` fixes robustez + review.md. El motor (013-001) NO se modificó.
- **Adapter delgado (solo implementa `Backend`, sin tocar el motor):** `Name()="claude"`; `Args` = `[bin, -p, --session-id s.ID, --model model, --output-format stream-json, ...flags]` (prompt SIEMPRE por stdin, nunca en argv — P3); `TranslateReq` = último mensaje `user` limpio (string o array text, sin tools — D3/D4); `TranslateOut` parsea el stream-json agregado → `choices[0].content` (text deltas) + `finish_reason` (`end_turn`/`stop_sequence`→`stop`, `max_tokens`→`length`); `TranslateStreamOut` = un chunk OpenAI por `text_delta`, sin usage/[DONE] (los agrega el motor); `IsRefusal` sobre stderr (markers refus/cannot/not able to/policy/responsible use/safety).
- **Wire-format único stream-json (C1):** `Args` no distingue stream ⇒ `--output-format stream-json` SIEMPRE; `TranslateOut` parsea el stream-json agregado. Un formato para Complete y Stream, motor intacto. **No se incluye `--include-partial-messages`** (decisión owner 12 Ago).
- **Text-only (I4):** solo blocks `text` se surfacean; `thinking`/`tool_use` descartados. `usage` real de claude se descarta (el motor fabrica/sobreescribe — P6/P7 de 001).
- **Review APPROVE (qwen3.7-plus):** **0 bloqueantes**, relevance audit 19/19 PASS. 2 importantes documentados para **013-004** (limitaciones de diseño, no errores del implementer): (1) `TranslateStreamOut` send sin guard de ctx — raíz en interfaz `Backend` sin context → cambio de interfaz en hardening; (2) marker `IsRefusal` "cannot" con riesgo de falsos positivos → ajustar con datos del smoke real (spec B-Q7). 3 menores → 2 resueltos en `e5ec87d` (scanner buffer, prompt vacío fail-fast), 1 aceptado (stream ID). 2 nits, 2 FYI.

### Deuda técnica transferida a 013-004 (hardening)

- Cambio de interfaz `Backend.TranslateStreamOut` para recibir context (evita goroutine leak bajo abandono del consumidor).
- Ajustar markers de `IsRefusal` de claude con datos del smoke real (spec B-Q7).
- De la review de 013-001: preservar usage real del backend si no-cero; manejar `sc.Err()` del scanner; allowlist de env; limpieza TTL de sesiones y lock map.

### Próxima feature en cola

- **013-003-config-wiring** (epic 013-mofgw-cli-subprocess): config `type: subprocess` + `backend` + `command` + `session_dir` + `backend_flags` en `ProviderConfig`, factory en `cmd/mofgw/main.go` (rama subprocess), catálogo `/v1/models` sin-tools para modelos subprocess, request path omite/rechaza `tools`, restricción "solo agentes del usuario". Quedan 003-004 (001-002 done). Coordinar vía `cdad-epic`. Los adapters gemini/codex quedan a futuro.

### 12 Ago 2026 — Feature: 013-001-subprocess-core (epic 013-mofgw-cli-subprocess)

### Decisiones relevantes

- **Feature 013-001-subprocess-core MERGED — motor `subprocess` genérico para providers que ejecutan el CLI de un backend de IA como subproceso (primera feature del epic 013).** Nuevo paquete `internal/subprocess`: `subprocess.Provider` implementa `provider.Provider` intacto (I1) — en vez de HTTP, spawnea el binario del backend con el prompt traducido por el `Backend`. Suite completa **531 tests `-race` en 20 paquetes** (nuevo paquete + 19 tests feature), go vet + gofmt limpios. Commits: `7d6737d` RED, `d07de8c` GREEN, `b9cbd33` fixes bloqueantes review, `043bec1` review.md. **1er feature del epic 013** (sigue: adapter claude 013-002).
- **Arquitectura D1 — motor genérico + interfaz `Backend` (→ ADR-010):** el motor posee exec, resolución de sesión por cliente, serialización, captura stdout/exit, fabricación de usage y normalización de errores; el `Backend` posee argv, traducción de formato, parseo y detección de negativa. **El motor NO conoce strings backend-específicos** (I2): no hay `"claude"`, no interpreta flags; `backend_flags` pasan opacos a `Args`.
- **D2 — sesión por CLIENTE (default):** `Session.ID` = sha256(clientID) determinístico; el modelo se pasa por request (`Backend.Args` → `--model`), NO participa de la key en modo default (P3). Override `client+model` (ID = hash(clientID|model)) implementado y testeable en 001, expuesto por 003 (P2).
- **D3/D4 — historial en la sesión, prompt único limpio:** mofgw envía SOLO el contenido del último mensaje `user` (extraído por `TranslateReq`) como un único prompt vía stdin; sin tools, sin tool_calls/resultados/marcadores internos; el CLI acumula la conversación en su sesión estable. El modelo se trata como sin-tools desde mofgw.
- **D5 — sin fallback a nivel backend:** un único backend subprocess; negativa/falla del CLI NO se auto-rutea a otro backend subprocess. Todo fallo se normaliza a `provider.ErrUpstream`; el fallback cross-provider genérico de mofgw aplica si otro provider sirve el mismo modelo.
- **D7/D8/D9 — usage fabricado + serialización + errores normalizados:** usage fabricado por el motor en Complete y Stream (chunk final compatible con `CaptureUsage`); a lo sumo un proceso CLI en vuelo por sesión (lock cap 1 sostenido durante todo el request incl. stream, espera ctx-cancelable); errores siempre vía `NewErrUpstream` sanitizado — spawn→`network`, exit no-cero→`upstream_error`, timeout/deadline→`timeout`, **refusal→`invalid_request_error`/400 NO retryable** (P11), clientID vacío→fail-closed (P13), nunca se filtra `ctx.Err()` crudo.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **2 bloqueantes RESUELTOS en `b9cbd33`** — (1) goroutine leak + inanición del lock por sesión en Stream (consumidor que abandona el canal → sends bloqueados, `release()` no corre): fix con sends con `select ctx.Done()` + kill/reap (`cmd.Wait()`) + release por defer en todos los outcomes + guard del scanner; (2) `isRefusal` en el engine violaba la frontera Backend (interpretaba stderr con keywords hardcodeadas): fix `IsRefusal(stderr string) bool` agregado a la interfaz `Backend`. 5 no-bloqueantes (3 deuda para 013-004, 2 FYI). Relevance audit **19/19 PASS**, cero dependencia interna (AP-14).

### Deuda técnica detectada

- **Llevadas a 013-004-resilience-ops (de la review):** preservar usage real del backend si no-cero; manejar `sc.Err()` del scanner stdout (línea >1MB); mensaje "invalid backend output" con status 0/network; allowlist de env (`cmd.Env = os.Environ()` hoy expone todo al hijo); limpieza del lock map y TTL de sesiones en disco.
- **Riesgos del epic vigentes (plan-013, sin cambio):** cuota de suscripción (Claude Pro) se quema rápido — el CLI reporta rate-limit y mofgw lo absorbe/fallbackea (transparencia); spawn overhead ~1-2s (mitigado con `--bare` + sesión); `ANTHROPIC_API_KEY` puede sombrear OAuth → validar auth al arrancar. Passthrough de tools y structured output vía CLI quedan fuera (deuda del epic).

### Próxima feature en cola

- **013-002-adapter-claude** (epic 013-mofgw-cli-subprocess): implementa la interfaz `Backend` para el binario `claude` — argv (`-p --session-id`), traducción OpenAI↔claude (Complete + Stream). El motor 001 queda listo; 002 no toca el motor. Quedan 002-004 pendientes (001 done). Coordinar vía `cdad-epic`. Los adapters gemini/codex quedan a futuro (fuera del epic).

### 12 Ago 2026 — Epic 012-mofgw-odoo-component cerrado (módulo Odoo distribuible)

- **EPIC-012 (mofgw-odoo-component) CERRADO — 3/3 features done.** El fuente del módulo
  Odoo `mofgw_ai` (epic 011) quedó versionado en el repo bajo `odoo/mofgw_ai/` como
  **componente opcional** para quien use Odoo 19 con mofgw como proxy de IA. Hallazgo:
  el fuente existía localmente (esta máquina es el host de dominios; `mofgw-staging.example.com`
  y el workstation comparten filesystem), no era solo "una copia instalada en el server".
  Copia fiel verificado con `diff -r`, sin `__pycache__`. Distribución opción A (versionar
  en repo; cada instancia copia a su addons_path). Commit `8884036`, closure-012.md.

### 12 Ago 2026 — Epic 011-mofgw-odoo cerrado (mofgw como proveedor de IA de Odoo)

### Decisiones relevantes

- **EPIC-011 (mofgw-odoo) CERRADO — 9/9 features done + integración cross-feature E2E verificada + closure completado.** El epic hizo que mofgw sea el **único proveedor de IA** de una instancia Odoo 19 enterprise (reemplazo total de OpenAI), sin que Odoo se entere del cambio. Cadena entregada: lado Go (`/v1/responses` [001], structured output [002], tool-calling [003], file attachments [004], web-search grounded [005], `/v1/embeddings` [006]) + lado Odoo (módulo `mofgw_ai`: vector 384 [007], provider [008]) + infra staging enterprise [009]. Programa mofgw queda **10/10 epics done (001-010) + EPIC-011 completo**. Closure: `docs/epics/closure-011.md`, ADRs 004-009.
- **Integración E2E real verificada (integration-011.md):** `Odoo enterprise (staging-instance) → provider mofgw → túnel odoo-host:3369 → mofgw Go local → deepseek-v4-flash`, `curl /v1/responses` con key `<test-key>` → **HTTP 200 "PONG"**; log mofgw confirmó traducción Responses→chat interna. 14/14 tests del módulo Odoo. 0 llamadas a `api.openai.com`. Suite Go **499 tests `-race`** en 19 paquetes.
- **Decisiones transversales del epic → ADR:** (1) **"el cliente nunca elige"** (ADR-008) — mofgw decide el modelo de embeddings por cliente (006), gating opt-in de web_search (005), dimensión única de Odoo (008); (2) **contrato alineado al parser real de Odoo** (ADR-009) — forma del `output[]`, solo-texto de web search, tests contra `llm_api_service.py` real, no la spec OpenAI.
- **8 criterios de aceptación del epic verificados** (integration-011.md): 9/9 done, E2E chat/structured/RAG/file/web, modelo forzado por cliente, 0 llamadas a OpenAI, suite `-race` verde.

### Deuda técnica detectada (se llevó al closure-011)

- **Resuelto tras closure:** embeddings reales verificados (Odoo→mofgw→Ollama all-minilm 384-dim) y `ir_actions_server.AI_PROVIDER` overrideado a mofgw/deepseek-v4-flash (server actions ya no usan openai). **100% reemplazo de OpenAI en Odoo (ai.agent + server actions) verificado.**
- **`ir_actions_server.AI_PROVIDER` (D6) — RESUELTO 12 Ago:** override declarativo de los atributos de clase en `mofgw_ai/models/ir_actions_server.py` → server actions (`state='ai'`) usan `mofgw`/`deepseek-v4-flash`. Test `test_server_actions_use_mofgw`.
- **Audio out (whisper/realtime):** deuda del epic — Odoo con mofgw no tendrá voz hasta epic posterior.
- **Reindexación de sources:** si una instancia tenía chunks 1536-dim, reindexar tras el redimensionado (paso operativo en README de mofgw_ai).

### Próximo paso en cola

- **NINGUNO en mofgw-odoo — epic 011 cerrado + 100% deuda resuelta.** Programa mofgw completo (10/10 epics + 011). Restos operativos del programa general: E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Decidir: próximo epic, feature standalone, o cerrar.

### 12 Ago 2026 — Feature: 011-008-odoo-provider (epic 011-mofgw-odoo) — ÚLTIMA, epic 9/9

### Decisiones relevantes

- **Feature 011-008-odoo-provider DONE — registro de mofgw como provider de IA de Odoo 19 (módulo local `mofgw_ai`, ÚLTIMA feature del epic 011 → epic 9/9 done).** Del lado Odoo (0 cambios Go). El módulo registra mofgw reutilizando el flujo openai de `LLMApiService` (formato `/v1/responses`, tool call openai) apuntando `base_url` a mofgw. Verificado en VPS (staging-vps/staging-instance): **13/13 tests pasan** (4 de 007 + 9 del provider).
- **Mecanismo D1 — monkeypatch in-place (no subclase):** el core instancia `LLMApiService` por nombre y comparte `PROVIDERS` por referencia. `mofgw_ai/__init__.py` hace `PROVIDERS.append(Provider('mofgw', 'mofgw', 'all-minilm', [5 llms]))` + parchea in-place `__init__` (base_url), `_get_api_token` (config→env→UserError), `_request_llm` (delega a `_request_llm_openai`), `_build_tool_call_response` (formato openai). Cada parche = guard+delegate al original para openai/google (sin regresión I2). **Doble guard de idempotencia** (`if not any(p.name=='mofgw')` + flag `_mofgw_patched`). **→ ADR-007.**
- **D3 — override obligatorio de `ai.embedding.embedding_model`:** `PROVIDERS.append` NO actualiza `EMBEDDING_MODELS_SELECTION` (snapshot separado) → re-declara `embedding_model = fields.Selection(selection_add=[('all-minilm','All-minilm')])` con `ondelete={'all-minilm':'cascade'}`. El `embedding_model` del Provider mofgw es exactamente `'all-minilm'` → embeddings 384-dim alineados con la columna `vector(384)` de 007 (I4).
- **D4/D5 — config por instancia:** `ir.config_parameter` `ai.mofgw_url` + `ai.mofgw_key`, fallback env `ODOO_AI_MOFGW_TOKEN`; `res.config.settings` hereda + view. **URL default `http://127.0.0.1:3369/v1`.** Masking key vía `widget="password"` (patrón core).
- **Review APPROVE (qwen3.7-plus):** **0 bloqueantes.** Fix #2 APLICADO (spec password via widget); #1/#3 DESCARTADOS; FYI #4 (E2E staging). Auditoría: 10/11 postcondiciones con test (P11 E2E deuda). Monkeypatch correcto, doble guard idempotencia, ondelete cascade, seguridad key adecuada.
- **3 bugs de test corregidos en GREEN:** openai también usa `function_call_output`; `get_values()` devuelve strings; `requests` normaliza `method` a `'post'`.
- **EPIC 011 COMPLETO — 9/9 done (001-009).** Entra a integración cross-feature y closure — coordinar vía `cdad-epic`.

### Deuda técnica detectada

- **`ir_actions_server.AI_PROVIDER` (D6, out of scope):** el path de server actions queda fuera; el path principal cubierto es `ai.agent`. Deuda del epic para integración/closure.
- **P11/C7 E2E no verificada (FYI):** `request_llm` real contra mofgw en staging — ejecutar manualmente antes de cerrar 011.
- **`_to_open_ai_tool_schema`** no transforma para provider != 'openai'; mofgw traduce en su capa. Verificar si algún upstream exige la forma openai estricta.
- **Monkeypatch global (ADR-007, Media):** patrón no soportado de Odoo — re-evaluar si el core cambia `LLMApiService`/`PROVIDERS` en futuras versiones.

### Próxima feature en cola

- **NINGUNA — epic 011 completo (9/9 done).** Siguiente: **integración cross-feature + closure del epic 011-mofgw-odoo** vía `cdad-epic` (E2E integral de los 8 endpoints Go + módulo Odoo en staging, ejecutar P11/C7, resolver deuda D6).

### 12 Ago 2026 — Feature: 011-007-embedding-vector (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-007-embedding-vector DONE — alineación de la dimensión del vector de embeddings en Odoo (módulo local `mofgw_ai`).** Overridea `ai.embedding.embedding_vector = Vector(size=384)` (reemplaza el `Vector(size=1536)` hardcodeado en ai_embedding.py:32) y redimensiona el schema en PostgreSQL. `_get_dimensions()` ya lee `size` en runtime (ai_embedding.py:37) → 384 fluye automático a los 2 usos (cron ai_embedding.py:112, RAG ai_agent.py:597) sin tocar el core. **Verificado end-to-end en VPS (staging-vps, instancia enterprise `staging-instance`):** columna `vector(384)` + índice ivfflat recreado automáticamente en install, 4 tests del módulo pasan. Sin cambios Go. Es la 7ma feature del epic (**8/9 done: 001-007 + 009**).
- **Corrección de diseño mayor (verificada contra source Odoo 19) — redimensionado del schema vía `pre_init_hook` (install) + `migrations/1.0/pre-migrate.py` (upgrade), NO `post_init_hook` ni solo `migrations/`.** Razones verificadas: (a) `post_init_hook` corre DESPUÉS del schema sync (`init_models`, loading.py:186→235) → dropea el índice y nada lo recrea (I4 roto); (b) `post_init_hook` NO corre en upgrade (`update_operation != 'install'`, loading.py:231-238) → la columna PG queda 1536 (P6 roto); (c) `migrations/` solo corren en `'to upgrade'` (migration.py:151) → no corren en install fresco. `pre_init_hook` + `pre-migrate` corren ANTES de `init_models` → tras el DROP+ALTER, `apply_to_database`/`check_indexes` recrea el índice automáticamente. → **ADR-006**.
- **Firma del hook:** `def redimension_embedding_vector(env)` — Odoo 19 invoca pre_init_hook con UN argumento (loading.py:177/235); el hook vive en `mofgw_ai/__init__.py`.
- **Idempotencia explícita (P9, bloqueante #3 review):** guarda que consulta `format_type` y skip si ya es `vector(384)`. Bloqueantes #1/#2/#3 de la review **resueltos** (pre_init_hook, pre-migrate.py, guarda); opcionales #5 README / #6 migrations / #7 typo **aplicados**; #4 parcial (B6 idempotencia), #8 descartado.
- **README documenta el prerequisito operativo (P10/D3/C5):** una `ai_embedding` con chunks 1536-dim hace fallar el `ALTER`; purgar antes de migrar. Paso operativo documentado.
- **Infraestructura resuelta en VPS:** pgvector instalado en staging-vps (root); instancia Odoo enterprise `staging-instance` creada (db `staging_mofgw` UTF8 + vector). `ai` + `ai_app` + `mofgw_ai` instalados.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante, P10/D3):** comportamiento real del `ALTER ... TYPE vector(384)` en una instancia con chunks 1536-dim. Paso operativo de purga documentado; staging vacío sin impacto.
- **Comparte módulo con 011-008 (D4):** `mofgw_ai` es el módulo compartido; 011-008 agrega el registro del provider mofgw/Ollama.
- **Patrón reusable documentado (ADR-006):** redimensionar una columna `vector` en Odoo 19 requiere pre_init_hook (install) + pre-migrate (upgrade) ANTES de `init_models`, con guarda de idempotencia y DROP del índice ivfflat antes del ALTER.

### Próxima feature en cola

- **011-008-odoo-provider** (epic 011-mofgw-odoo): registrar mofgw en `PROVIDERS` + `base_url`/key en `LLMApiService`, config por instancia. Mismo módulo local `mofgw_ai` (D4 de 007). Queda SOLO 008 pendiente (001-007 done; 009 staging-enterprise ya DONE — **8/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-006-embeddings (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-006-embeddings DONE — `POST /v1/embeddings` en mofgw, forward a Ollama (endpoint que Odoo 19 usa en `_request_llm_embedding`).** mofgw actúa como gateway: recibe el request de Odoo, **fuerza el modelo de embeddings del cliente** (principio "el cliente nunca elige", P3 — el `model` que manda Odoo se IGNORA por completo, no llega a Ollama ni al envelope), lo forwardea al Ollama configurado en `embeddings.base_url` y devuelve el vector OpenAI-compatible. Segunda llamada saliente del proxy (tras DDG en 005), con un **cliente HTTP dedicado**. Suite completa **499 tests `-race`** verde en **19 paquetes** (paquete nuevo `internal/embeddings` + 18 tests feature), go vet limpio. Commits: `ca0931f` RED, `7c3b5d7` GREEN. Es la 6ta feature del epic 011 (**7/9 done con 009**).
- **Nuevo paquete `internal/embeddings` — cliente HTTP dedicado (patrón `websearch.Client` de 005, aplica ADR-005):** `provider.Client` NO es reusable para embeddings (`Complete`/`Stream` hardcodean `/chat/completions`, I7). `embeddings.Client` tipado (interfaz `Embed(ctx, body) → raw`, sin reflexión) y el concreto `Ollama` hace `POST base_url+"/embeddings"`. Nunca habla con `api.openai.com` (I2). Setter `SetEmbeddings(baseURL, apiKey)` (global) + `SetClientEmbeddingsModel(clientID, model)` (mapa inmutable post-set, patrón SetBudget). Router, providers y `provider.Client` intactos (I1, I7).
- **D1 — dimensions passthrough (hallazgo descubrimiento verificado):** mofgw fuerza el modelo por cliente pero **NO valida el `dimensions` de Odoo** (hardcodeado en 1536 en ai_embedding.py:32); lo deja pasar a Ollama, que **SÍ acepta `dimensions` y trunca** si el modelo lo soporta. El vector real es la dimensión **nativa** del modelo (all-minilm = 384). La alineación de dimensión es **config manual en Odoo** (feature 011-007), no validación en mofgw (I4). Cambiar de modelo de embeddings ⇒ reindexar sources en Odoo (riesgo operativo, plan-011:66). `input` viaja byte-idéntico (P5).
- **D2 — cliente sin `embeddings.model` → 400 fail-fast:** sin default global silencioso; `error.message == "no embeddings model configured for client"`. D3 — `base_url` **global** (un Ollama por instancia mofgw), modelo **por cliente**. D4 — auth de Ollama sin key por default, `embeddings.api_key_env` **opcional**. D5 — pricing por **tokens del input** (`prompt_tokens`, mismo flujo `recordCacheTokens`); modelo ausente de `pricing:` → costo 0; si se agrega a `pricing:` pasa a costar automáticamente. Headers `X-Usage-*` con `setUsageHeaders`. Budget por cliente aplicado (P8).
- **Review APPROVE (qwen3.7-plus):** **0 bloqueantes.** Opcional **#2 APLICADO** — `parseEmbeddingsUsage` reutiliza `provider.Usage` directo; opcional **#6 APLICADO** — frontmatter del spec. #1/#3/#4 **DESCARTADOS con motivo**; FYI #5 (límite 4MB documentado). Auditoría: **10/10 postcondiciones con test**, ninguno sobrante, cero dependencia interna.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** compatibilidad real del shape de embeddings (`input` string-vs-array, `data[].embedding`, `dimensions`/truncado) contra el Ollama real y `_request_llm_embedding` de Odoo 19 (tests usan mock upstream).
- **Riesgo operativo documentado (plan-011:66):** cambiar el modelo de embeddings a una dimensión distinta a la nativa (all-minilm=384) ⇒ reindexar sources en Odoo. Alineación manual (011-007).
- **Dependencia saliente a Ollama (nueva, aplica ADR-005):** segunda llamada saliente del proxy (tras DDG). Timeout 30s por intento, body upstream limitado a 4MB, 429 de Ollama no-reintentable → 502 (P9).

### Próxima feature en cola

- **011-007-embedding-vector** (epic 011-mofgw-odoo): alineación de la dimensión del vector de embeddings en Odoo (config manual, arranca desde el hallazgo D1 de 006). Quedan 007-008 pendientes (001-006 done; 009 staging-enterprise ya DONE — **7/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-005-web-search (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-005-web-search DONE — grounded search server-side en `/v1/responses`.** Reemplaza el rechazo incondicional D3 de 003 (cualquier tool `type != "function"` → HTTP 400) por la **resolución real** del caso `web_search_preview`: detectar → buscar en DDG → inyectar los resultados como system message prependido → **remover** la tool del upstream → item message. mofgw **emula** el grounded search server-side porque Odoo NO maneja el ciclo `web_search_call` (verificado: Odoo solo lee `text` del item `message`). Suite completa **481 tests `-race`** verde en **18 paquetes** (paquete nuevo `internal/websearch` + 16 tests feature), go vet limpio. Commits: `dfea7b1` RED, `ae10d86` GREEN.
- **D1 — mofgw emula grounded search (primera llamada saliente del proxy → ADR-005):** detectar `web_search_preview` → ejecutar DDG → inyectar resultados como system message **prependido** → **QUITAR** `web_search_preview` de los tools upstream → item message normal. Sin ciclo `web_search_call`. Decisión arquitectónica nueva → **ADR-005**.
- **D2 — query = último mensaje user (determinista):** concatenación de los text de las parts input_text del último item message con role=="user". Descartada la alternativa de 2 round-trips.
- **D3 — cliente DDG propio en Go, sin dependencias:** `internal/websearch` scrapea `html.duckduckgo.com/html/?q=` → top-N `{Title,URL,Snippet}`, con fallback a Instant Answer API. Solo stdlib (regex + html.UnescapeString + net/http). Contrato `websearch.Client` desacoplado del parser.
- **D4 — inyección como system message:** `[Web search results for "<query>"]` + N items numerados `title|url|snippet`, N = max_results (default 3). Sin url_citation (Odoo solo lee text).
- **D5 — gating `web_search.enabled:false` default (opt-in):** false → 400 conservado de 003 (sin regresión); true → flujo web search. Fallo de DDG = **best-effort** sin grounding (nunca 4xx/5xx al cliente). Tool type!=function no-web-search sigue 400 en ambos modos.
- **Despacho 3 vías** en la rama que rechazaba: (a) web_search_preview + enabled:true → flujo; (b) web_search_preview + enabled:false → 400; (c) type!=function no-web-search → 400. Infra: WebSearchConfig (enabled/max_results/timeout), setter **tipado** `SetWebSearch(enabled bool, client websearch.Client)`, wiring en main.go. **I1/ADR-004 respetado.**
- **P8 — cache excluido para web search:** un request web search activo **nunca** se sirve ni almacena en el response cache aunque temperature==0 (resultados DDG cambian; P8_CacheExcluidoParaWebSearch verifica 2 llamadas al upstream con mock A→B).
- **Review APPROVE (qwen3.7-plus):** **1 bloqueante RESUELTO + 1 opcional APLICADO.** Bloqueante #1 — setter/campo `any` + 47 líneas de reflect (fallo silencioso, viola 2 laws) → **reemplazado por la interfaz tipada `websearch.Client`** (mock devuelve `[]websearch.Result`; se eliminó el reflect, el import y reflectFieldString). Opcional #2 APLICADO — `html.UnescapeString` stdlib. #3/#4/#5 DESCARTADOS con motivo. FYI #6/#7 sin acción. Auditoría: 8/8 postcondiciones con test.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** soporte real de DDG (scrape + fallback Instant Answer) con provider real — tests usan mock (I6).
- **Dependencia saliente a DDG (nueva, ADR-005):** primera llamada saliente del proxy a un servicio no-provider. Best-effort sin errores nuevos (I2), query solo como query parameter (sin SSRF), body limitado a 4MB. Vigilar disponibilidad/rate limits de DDG en prod.
- **Riesgo del epic vigente (sin cambio):** Odoo 19 usa Responses API en estado "preview". Web search queda enabled:false por default hasta opt-in en config de prod.

### Próxima feature en cola

- **011-006-embeddings** (epic 011-mofgw-odoo): forward de embeddings a Ollama (modelo por cliente, pricing). Quedan 006-008 pendientes (001-005 done como troncos; 009 staging-enterprise ya DONE — **6/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-004-file-attachments (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-004-file-attachments DONE — traducción de las parts de archivo de Odoo (`input_file`/`input_image`) en `/v1/responses`.** Reemplaza la rama de rechazo P9 de 001 (400 "file attachments not yet supported") por la traducción real de las parts del `input[]` Responses al formato chat-completions que hablan los providers upstream. Todo el cambio vive en `internal/proxy/responses.go` (ADR-004/I1: router, providers y `ParseChatRequest` intactos). Suite completa **465 tests `-race`** verde en 17 paquetes (452 → 465, 12 tests feature nuevos en 6 bloques + POST-AUDIT en 001 y 003 + 1 fix review), go vet limpio. Commits: `7fce35b` RED, `69ddae1` GREEN.
- **D1 — decisión POR MENSAJE string-vs-array (P3):** un mensaje del `input[]` con SOLO parts `input_text` → `content` = **string** (concatenación de los `text`, exactamente como 001/003 — sin regresión en los caminos de texto plano); un mensaje con AL MENOS una part `input_image`/`input_file` → `content` = **array** con un item por part del input, en orden de aparición (I3). La decisión es por item, no por request: en un mismo `input[]` conviven mensajes string y array (verificado por `P3_MensajesMixtosPorMensaje`). El content-array se aplica SOLO a items `message`; los items `function_call`/`function_call_output` del ciclo tool-calling de 003 quedan intactos (P4).
- **D2 — mapeo de parts:** `input_text` → `{"type":"text","text":T}`; `input_image` → `{"type":"image_url","image_url":{"url":U,"detail":D?}}` (detail passthrough con omitempty: ausente → el campo no se emite, misma semántica que `strict` de 002/003); `input_file` → `{"type":"file","file":{"file_data":D,"filename":F}}` (part estilo OpenAI, sin detail). Data-URIs viajan como strings tal cual, sin re-marshal (I2, byte-identidad). Parts de tipo desconocido se dropean conservando el orden de las conocidas (consistente con el string-content que ignora lo que no es `input_text`).
- **D3 — sin gating por modality / PDF honesto (P6):** mofgw traduce y reenvía; NO conserva el 400 local, NO filtra por modality, NO convierte PDF. El `modality` es declarativo (solo catálogo `/v1/models`); el router elige por `req.Model`. Si el modelo elegido no soporta vision/archivos, el 4xx upstream se propaga **no-reintentable** (patrón D5 de 002). Verificación empírica del soporte PDF del provider real queda como **desviación no bloqueante**.
- **Sin cambio en salida ni en contexto (P8/P9):** la traducción de salida queda intacta (item `message`/`function_call` de 001/003); `estimatePromptTokens = len(body)/4` no se toca — las data-URI base64 inflan `len(body)` pero la ventana descuenta automáticamente los tokens sobreestimados (P8, sin cambio de código).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcionales **#1 APLICADO** (eliminar dead code `inputPart` de 001 sin callers tras POST-AUDIT) y **#3 APLICADO** (subtest `P3_PartTipoDesconocidoDropeada`: part `input_unknown` en mensaje content-array → 2 items en orden, la desconocida dropeada). #4/#5 **DESCARTADOS con motivo**, FYI #6 sin acción. Auditoría test↔postcondición: **9/9 postcondiciones con test** (P7/P9 por tests untouched de 001/003), ninguno sobrante, cero dependencia interna (AP-14).

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** soporte real de las parts `file` (PDF) y `image_url` por los providers upstream — mofgw traduce y propaga el 4xx no-reintentable (D5 de 002) si el provider no las soporta. Verificar con provider real.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py:283-304`, formato de parts verificado en el spec).

### Próxima feature en cola

- **011-005-web-search** (epic 011-mofgw-odoo): habilita `web_search_preview` / tools no-function (hoy 400 conservado como D3 de 003 y P7 de 004). Quedan 005-008 pendientes (001-004 done como troncos del epic; 009 staging-enterprise ya DONE — **5/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-003-tool-calling (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-003-tool-calling DONE — traducción del ciclo tool-calling de Odoo (tools ↔ function_call ↔ function_call_output) en `/v1/responses`.** Reemplaza la rama de rechazo P7 de 001 (400 "tool calling not yet supported") por la traducción real bidireccional en las tres direcciones: (1) entrada `body["tools"]` (formato Responses plano) → `tools` chat-completions anidado bajo `function`; (2) salida `choices[0].message.tool_calls` → items `function_call` del `output[]`; (3) entrada item `function_call_output` → mensaje `role:"tool"`. **Quién maneja el ciclo: Odoo** ejecuta las tools por su cuenta y arma el siguiente request; mofgw solo traduce (stateless, sin estado de sesión — cada request es una traducción puntual del `input[]` completo). Suite completa **452 tests `-race`** verde en 17 paquetes (426 → 452), go vet limpio. Commits: `cea354e` RED, `fefb55c` GREEN.
- **D1 — `call_id` = eco directo del id upstream (base del round-trip):** el `call_id` del item `function_call` de salida (P6) == `tool_calls[i].id` upstream; el `id` estable del item (P11 de 001, `responsesID`) va APARTE (no == call_id, P8). Solo así Odoo devuelve `function_call_output.call_id` == ese mismo id y mofgw lo mapea al `role:"tool"` con `tool_call_id` correcto (P4/I4). En entrada, el `tool_call.id` del assistant == `call_id` del item `function_call` (fallback al `id` si no trae `call_id`), cerrando el round-trip en ambas direcciones.
- **D2 — solo items `function_call` cuando hay tool_calls (cero items message):** con `tool_calls` no vacío, `output[]` contiene EXACTAMENTE un item `function_call` por `tool_calls[i]` y NINGÚN item `message` (Odoo ignora el texto del message en esa rama, `elif not has_tool_calls`). Con `tool_calls` vacío/ausente → item `message` intacto (P7, P10 de 001). Shape verificado parseable por el snippet real `_request_llm_openai_helper` (lee `name`/`arguments`/`call_id` → `to_call == [(name, call_id, arguments)]`).
- **D3 — `type != "function"` → 400 conservado (lo resuelve 005):** si `tools` contiene CUALQUIER tool de `type != "function"` (incluido `web_search_preview` y el caso mixto function+no-function), HTTP 400 "tool calling not yet supported" — el chequeo ocurre ANTES de traducir. 003 traduce solo los `type == "function"`. Rechazo de tool JSON malformado separado: **"invalid tool definition"** (opcional #3 de la review, APLICADO).
- **Byte-identidad I3 e invariantes sostenidos:** `parameters` de cada tool viaja como `json.RawMessage` y se serializa verbatim (byte-idéntico al input, sin re-marshal); `arguments` del item de salida es el string JSON crudo parseable por `json.loads` (I2); `strict` con omitempty (ausente → no se emite, misma semántica que D2 de 002). `tools`/`parallel_tool_calls` viajan como passthrough en `chatBodyMap` (inyectados antes del `Marshal`) y sobreviven clamp e inyección de thinking (P9), con cache exact-match separada por endpoint que cambia de key con los tools.
- **provider.go: solo type definitions (I1/ADR-004 sin romper):** `ChatToolCall` (id/type/function{name,arguments}) y `ToolCalls []ChatToolCall` en `ChatMessage` (`omitempty`). NO modifica `ParseChatRequest`, router ni providers; `responsesInputItem` discriminado por `type` (message / function_call / function_call_output) cubre los items mixtos del `input[]`. Todo el cambio de la feature vive en `internal/proxy/responses.go`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #3 **APLICADO** — unmarshal fallido de tool → "invalid tool definition" (rama separada del 400 "tool calling not yet supported" que queda solo para type!=function). Opcionales #1 (agrupar function_call adyacentes en un único assistant multi-tool_call) y #2 (subtest tool_calls_vacio no ejercita `[]`) **DESCARTADOS con motivo**. FYI #4/#5 sin acción. Auditoría test↔postcondición: P1-P10 todos con test (10/10), ninguno sobrante, cero dependencia interna (AP-14). POST-AUDIT en 001: subtest `tools` flip 400→200 (traducción verificada), `web_search_preview` conserva 400 (D3).

### Deuda técnica detectada

- **Agrupación de `function_call` adyacentes en un único mensaje assistant multi-tool_call** (hardening futuro): el contrato especifica 1:1 (un item function_call → un mensaje assistant con un tool_call), cubriendo el caso de un solo tool (el común en Odoo); con `parallel_tool_calls:true` y varios adyacentes, hoy se generan N mensajes assistant separados. Funcional para Odoo (parsea tool_calls uno a uno); documentado como out-of-scope en el spec, optimización de implementación a futuro.
- **Verificación empírica pendiente (desviación no bloqueante):** aceptación de `strict` en tools por los providers reales upstream — el diseño pasa `strict` tal cual; si un provider lo rechaza, mofgw propaga el 4xx no-reintentable (patrón D5 de 002, sin degradación). Verificar con provider real.
- **Deuda del epic vigente (sin cambio):** streaming SSE del Responses API (deuda del epic), y `web_search_preview`/tools no-function que hoy son 400 y los resuelve la feature 005.

### Próxima feature en cola

- **011-004-file-attachments** (epic 011-mofgw-odoo): habilita las parts `input_file`/`input_image` del `input[]` (hoy 400 "file attachments not yet supported", P9 de 001). Quedan 004-008 pendientes (001/002/003 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-002-structured-output (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-002-structured-output DONE — `text.format.json_schema` → `response_format` en la ENTRADA de `/v1/responses` (structured output, AI Field Fill de Odoo).** Reemplaza el rechazo P8 de 001 (400 "structured output not yet supported") por la traducción real: cuando Odoo manda `text.format = {"type":"json_schema",...}`, mofgw inyecta `response_format = {"type":"json_schema","json_schema":{...}}` en el body chat-completions que delega al provider. **Alcance estricto: solo la entrada; la salida NO cambia** — sigue el shape P10 de 001 (`output[0]` item `message`, `content[0].text == choices[0].message.content`), el JSON estructurado viaja como texto normal del `content`, verificado contra `_request_llm_openai_helper`. Suite completa **426 tests `-race`** verde en 17 paquetes (413 → 426, 7 tests nuevos + POST-AUDIT), go vet limpio. Commits: `6223207` RED, `de162e1` GREEN.
- **Mapeo D1 — `response_format` estructural (no plano):** `text.format` (4 campos planos de Odoo) → `response_format = {"type":"json_schema","json_schema":{"name","schema","strict"}}`. El `schema` viaja como `json.RawMessage` (responses.go:151,158) y se serializa verbatim → **byte-identidad I3** sostenida en toda la cadena (el clamp opera sobre `map[string]json.RawMessage`, `response_format` es passthrough opaco que no desarma). `strict` con `omitempty` (D2): presente → valor exacto; ausente → campo no se emite.
- **D3/D4/D5 (decisiones del brainstorm mantenidas):** solo `type == "json_schema"` se traduce; el resto de `type` o un format malformado conserva la rama P8 (400 "structured output not yet supported") (D3). `temperature` tal cual, NO forzado a 0 por tener structured output (D4). Sin degradación a `json_object`: provider que no soporta `response_format` → mofgw propaga el 4xx upstream no-reintentable, sin fallback de menor capacidad (D5, `router.Complete` → `handleChainError` → `absorb.Respond`).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #1 **APLICADO** — `wireResponseFormat`/`wireJSONSchema` tipados (structs locales no exportados con `omitempty` en `Strict`), dando type-safety de compilador (un typo de clave ya no compila) y resolviendo D2 sin `if` explícito. Opcionales #2 (guard local de `schema` ausente — el upstream propaga 4xx, funcionalmente correcto, P7) y #3 (POST-AUDIT dentro de función "Rechazos" — cosmético) **DESCARTADOS con motivo**. Auditoría test↔postcondición: P1-P8 todos con test, ninguno sobrante, cero dependencia interna (AP-14).
- **Pipeline compartida sin regresión (P6):** `response_format` viaja como campo passthrough en `chatBodyMap` (inyectado antes del `Marshal`), preservado por clamp e inyección de thinking; atraviesa limiter/cache/clamp/ventana/budget/ruteo idéntico a 001. Cache exact-match con key separada por endpoint (P12 de 001): misma `schema` + mismo body → HIT; `schema` distinta → MISS (verificado por `TestE2E011002_P6_CachePorSchema`).

### Deuda técnica detectada

- **Ninguna deuda bloqueante.** Decisiones del brainstorm D1-D5 y fix #1 aplicados en su totalidad; #2/#3 de la review descartados con motivo documentado en `review.md`. La verificación empírica del soporte real de `response_format` por un provider (P7, desviación no bloqueante del spec) sigue pendiente — el diseño falla explícito con 4xx no-reintentable, sin degradación silenciosa.
- **Nota de higiene de state file:** `.cdad-state.json` tenía la clave `postconditions_status` **duplicada** (JSON ambiguo donde el segundo `map` sobreescribe al primero). Deduplicada en el cierre de esta feature.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa el Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-003-tool-calling** (epic 011-mofgw-odoo): habilita el ciclo tool-calling de Odoo. Reemplaza el rechazo P7 de 001 (400 "tool calling not yet supported"). El `call_id`/item id estable de 001 (P11) ya está pensado para este ciclo (Odoo reutiliza el id de item). Quedan 003-008 pendientes (001 y 002 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-001-responses-endpoint (tronco del epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-001-responses-endpoint DONE — `/v1/responses` en mofgw, tronco del epic 011-mofgw-odoo (reemplazo total de OpenAI en Odoo 19).** Implementa el Responses API (`POST /v1/responses`) que Odoo 19 enterprise usa en `_request_llm_openai_helper`, traduciendo Responses↔ChatCompletions no-stream. Se registra junto a `/v1/chat/completions` (proxy.go:283) bajo `s.auth.Wrap`. Suite completa **413 tests `-race`** verde en 17 paquetes (388 → 413, +25 tests feature), go vet limpio. Commits: `d2142f6` RED (24 fallan por 404 + 1 pasa 401 vía auth.Wrap), `3bec0b5` GREEN, `595e742` fixes review. Es el 1er feature del epic 011.
- **Traducción vive SOLO en la capa del endpoint (I1) — decisión arquitectónica del epic → ADR-004:** `handleResponses` (responses.go) traduce `input`→`messages`, delega en la MISMA pipeline de chat (`router.Complete`) y traduce la salida a `output[]`. `ParseChatRequest`, router y providers intactos. El body Responses viaja crudo por la cadena (pattern mofgw de transparencia). Esta es la frontera que las features 002-005 van a extender.
- **Modo solo NO-stream (D1) + rechazos explícitos de features futuras (P6-P9):** `stream:true` → 400 "streaming not supported yet"; `tools`/`web_search_preview` → 400 "tool calling not yet supported"; `text.format.json_schema` → 400 "structured output not yet supported"; part `input_file`/`input_image` → 400 "file attachments not yet supported". Features 002-005 reemplazan los rechazos; streaming SSE queda como deuda del epic.
- **`output[]` alineado al parser real de Odoo (D2, P10):** item `{"type":"message","id":"<stable>","role":"assistant","status":"completed","content":[{"type":"output_text","text":...}]}` — parseable por `_request_llm_openai_helper` (lee `content[i]["text"]`). Verificado contra `llm_api_service.py:364-397`.
- **IDs estables/determinísticas (P11):** sha256(clientID+body) con prefijo `resp_` → mismo input = mismo id de response y de item. Habilita que Odoo reutilice el id de item como `call_id` en el ciclo tool-calling de 003.
- **Cache separada por endpoint (P12):** `responsesCacheKey` usa namespace propio (prefix `"responses"` + clientID + body canónico) → una responses-response jamás se sirve como chat-response ni a la inversa. Cache solo para requests determinísticos (`temperature == 0`). La separación cross-endpoint es invariante estructural garantizada por el body wire (input vs messages).
- **Pipeline compartida con chat (P4):** limiter global + keyed + **por agente**, telemetría de descubrimiento (009-000), contextAnalysis (009-001), clamp por modelo, context-window check, budget, usage/cost accounting, `recordCacheTokens`, `setUsageHeaders`, `emitRequestTelemetry` + `emitRequestEnd`. `store` ignorado/stateless (P13); `max_output_tokens` no-op (Odoo no lo manda). Modelo forzado a nivel raíz con `rewriteResponseModel` (P5).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 1 bloqueante (test G P12 no ejercitaba la cache: bodies sin `temperature` → `isDeterministic=false` → MISS obligatorios y test vacío) **RESUELTO** agregando `temperature: 0` para ejercitar el camino HIT/MISS real. Opcionales #2 (telemetría/context/emitRequestEnd) y #3 (rate limit por agente) **APLICADOS**; #4 (singleflight) → deuda documentada, #5 (doble marshaling) y #7 (validación de role) descartados nits, #6 (model del envelope) no aplicado (FYI, patrón consistente con chat). Cero bloqueantes pendientes.

### Deuda técnica detectada

- **Llevadas a TECHDEBT consolidado:** streaming SSE del Responses API (fuera de alcance de 001, deuda del epic), singleflight/coalescing para `/v1/responses` (opcional #4 desestimado → feature futura), y el resto del scope cross-endpoint de 002-005 (structured output, tool calling, file attachments, web search) que hoy son 400.
- **Nota P12 (de la review):** la separación de namespace por endpoint es un invariante estructural garantizado por el hash del body wire responses (nunca coincide con el body wire chat), no end-to-end demostrable por el test.
- **Riesgo del epic vigente (plan-011):** Odoo 19 usa Responses API en estado "preview" para varias features; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-002-structured-output** (epic 011-mofgw-odoo): `text.format.json_schema` → `response_format` (AI Field Fill). Reemplaza el rechazo P8 (400 "structured output not yet supported"). Depende de 011-001. Coordinar vía `cdad-epic`. Quedan 002-008 pendientes (001 es el tronco; 009 staging-enterprise ya DONE).

### 11 Ago 2026 — fix: follower de single-flight factura su clientID + X-Usage-*

- **Bug corregido (RED→GREEN granular, `6ace500` + `d4727bd`):** en el coalescing single-flight (010-001 P0), `recordCacheTokens` y `setUsageHeaders` corrían solo dentro de `fn()` (el líder). El follower reutilizaba el blob compartido pero no facturaba a SU `clientID` (006-001 P2) ni exponía `X-Usage-*` en SU response (007-003 P1) — sus headers quedaban en cero. Encontrado por auditoría de Claude en `proxy.go`, sin test que lo cubriera.
- **Fix:** flag `leader` capturado en `fn()`; en la rama `shared==true`, para `!leader` se reconstruye `*provider.Usage` desde `sfRes.Usage` y se llama `recordCacheTokens(clientID)` + `setUsageHeaders(w.Header())` + `lastUsage`. El flag evita doble-facturar al líder. Nota menor: `singleflight.Usage` no lleva `ReasoningTokens` → el follower reporta reasoning 0 en `recordCacheTokens` (no afecta headers ni facturación por cliente; mejora separada si se quiere tracking exacto).
- **Test nuevo `TestSF_FollowerFacturaYExponeUsage` (e2e_singleflight_test.go):** bloquea el upstream del líder para garantizar la superposición y aserta que el follower (a) fue deduplicado (`mofgw_single_flight_deduped_total{client="client-k2"} 1` — condición de validez), (b) factura a su clientID en /metrics, y (c) recibe `X-Usage-Total-Tokens: 1280`.
- **Evidencia:** suite completa **388 tests `-race` verde en 17 paquetes** (387 → 388), build + vet + gofmt limpios. Bug era latente: `singleFlightEnabled` off por default — corregido de todos modos.

### 11 Ago 2026 — EPIC-010 mofgw-catalogo-fiel CERRADO + DEPLOYADO

### Decisiones relevantes

- **EPIC-010 (mofgw-catalogo-fiel) CERRADO — 2/2 features done, ambas mergeadas + desplegadas en prod + verificadas 11 Ago.** El epic cerró los huecos de fidelidad del catálogo detectados en la auditoría del 10 Ago: /v1/models ahora informa completo, veraz y prescriptivo (010-001), y el request path hace valer lo que el catálogo declara (010-002). Cadena entregada: catálogo fiel → clamp/inyección en el request path. Los 4 criterios de aceptación del epic (plan.md) verificados. Suite completa **387 tests `-race`** verde (332 → 387), vet + gofmt limpios, 17 paquetes.
- **010-001-catalogo-fiel (MERGED, deployado):** `/v1/models` emite por modelo — `supported_parameters` [tools,reasoning], `modality` + `architecture` (input/output_modalities; `text+image+video->text` para minimax/kimi/qwen, `text->text` para deepseek/glm/flash-0731), `top_provider` {context_length, max_completion_tokens}, `max_output_tokens`, y `thinking_default` **prescriptivo** (ADR-003). Metadata de `deepseek-v4-flash-0731` agregada. `qwen3.7-plus max_output` corregido 65536 → **131072** (D8). Fallback rule: capability no declarada/verificada se OMITE, nunca se adivina (ausente ≠ 0/[]/false).
- **C3 enmendado (P6 wins, 11 Ago):** la review del 010-001 detectó contradicción interna C3-vs-P6 — `top_provider`/`max_output_tokens` NO son capabilities nuevas sujetas a fallback rule, son **alias derivados** de `context_window`/`max_output` (007-002) y se emiten cuando sus fuentes son > 0. Enmendado en spec por decisión del dueño (GVR), tests respetan la enmienda.
- **010-002-request-path-fiel (MERGED, deployado):** (1) **clamp por modelo pre-routing** en el proxy (`min(raw, modelMaxOutput)`, convive con el clamp por provider del router → efectivo `min(modelo, provider)`) — kimi-k2.7-code (max_output 32768) con max_tokens 384000 ya no produce el 400→502; (2) **window-check fiel**: usa el max_tokens EFECTIVO post-clamp (`promptTokens + min(raw, MaxOutput)` vs `window×(1+margin)`), preservando el fix TECHDEBT #23; (3) **inyección per-attempt provider-aware** del `thinking_default` via knob declarativo `providers[].thinking_path` (`""`|`zen`|`bailian`, default `""` = sin inyección): zen → `reasoning_effort`; bailian → `reasoning_effort` + `enable_thinking` (deepseek/glm) o `enable_thinking` (qwen); kimi/minimax **NUNCA** (always-on / adaptive nativo == prescriptivo). Nunca pisa effort explícito del cliente (P5). Ruteo/fallback/clasificación intactos (4xx sigue no-reintentable, P9).
- **POST-AUDIT mandatorio aplicado:** `TestRED_MaxTokensCuentaEnVentana` (e2e_008001) cambió de contrato con la regla fiel — **flip 400 → 200** (750 + min(400,100) = 850 ≤ 1000). El test existente se actualizó ANTES del GREEN (premisa reemplazada: el max_tokens crudo ya no cuenta contra la ventana). Colisión de nombre de archivo resuelta: los tests de esta feature viven en `e2e_010002_requestpath_test.go` (el `e2e_010002_test.go` ya estaba ocupado por la feature response-cache, numeración previa del EPIC-010 de eficiencia).
- **Decisiones de arquitectura:** ADR-003 (thinking_default **prescriptivo** — el configurado altera el nativo) creado en 010-001. Knob `providers[].thinking_path` adoptado por decisión del dueño 11 Ago (declarativo, consistente con I2 — valores de config, nunca inferidos por patrón de ID: los IDs reales acct1/acct2/qwen son frágiles para inferir path).
- **Reviews (qwen3.7-plus, familia distinta al implementer):** 010-001 APPROVE WITH COMMENTS — bloqueante C7 (config.example.yaml) RESUELTO 11 Ago, 2 nits/FYI sin acción; 010-002 APPROVE WITH COMMENTS — 0 bloqueantes, Major #1 (qwen thinking) resuelto, Minor #2 → TECHDEBT #25, Minor #3 (thinking_path) resuelto, nits #4/#5 aceptados, FYI #6.
- **Verificado en prod (11 Ago):** `/v1/models` responde los 7 modelos con metadata completa; **bailian ACEPTA `max_tokens=131072` para qwen3.7-plus** (200, sin 400 — verificación empírica pendiente del spec resuelta); kimi `max_tokens=100000` → **200** (clamp funciona, sin 400/502); deepseek smoke → 200 con thinking activo (inyección). healthz 6 providers OK. Suite 387 tests `-race` verde, vet + gofmt limpios.

### Deuda técnica detectada

- **Llevadas al índice consolidado (TECHDEBT, Cambios 11 Ago):** #25 (`thinkingProfile` frágil ante cambios de config — derivación por niveles de Thinking orden-dependiente, ACEPTADA sin cambio estructural, LOW; si el catálogo evoluciona → campo declarativo `ThinkingFamily` o unit test que fije la semántica), #26 (`qwen3.7-plus max_output` 64K→128K corregido y verificado empíricamente en deploy: bailian acepta 131072), #27 (`bodyHasExplicitEffort` re-parsea el body — optimización opcional, LOW). #4 cerrada completa (flash-0731 pricing + metadata en prod + example).
- **Verificaciones empíricas pendientes (Phase 4, fuera del contrato):** glm-5.2 `reasoning_effort=medium` vía bailian (la tabla §5.4 dice high/max only — si bailian lo ignora silenciosamente, el wire value enviado es el configurado; discrepancia → TECHDEBT); context window real minimax/glm/qwen (1,000,000 vs 1,048,576 — verificación empírica del techo pendiente, NO cambiar valores). Nits de la review 010-001 sin registro (nit preexistente: `created: time.Now()` por respuesta; FYI: `top_provider` es eco del formato OpenRouter, sin concepto en mofgw).
- **Dos decisiones del 010-001 a mantener en memoria:** fallback rule (capabilities no verificadas se omiten) y modality honesta negativa (`text->text` = "no documentada como soportada", no conjetura).

### Próxima feature en cola

- **Ninguna — el epic 010 está cerrado.** El programa mofgw queda con los **10 epics done (001-010)**. Restan los criterios de aceptación del programa completo (plan.md §Criterios del programa): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo residual: vigilar el anuncio de suba de precios de deepseek (TECHDEBT #4), monitorear cuota GO_2/GO_3 (#5), y la aceptación empírica de glm medium vía bailian cuando aplique.

### 09 Ago 2026 — Decisión: sticky_routing NO se habilita (póliza default-off)

- **Decisión formal del dueño del proceso: `fallback.sticky_routing` queda DESHABILITADO en prod (default off), documentado como decisión, no como deuda.** La feature 009-002 está implementada, testeada (332 tests) y mergeada — el costo de habilitarla es solo un toggle de config + reinicio.
- **Por qué no:** la data real muestra que no haría diferencia hoy — `failovers=0` y `cooldown_hits=0` en ~19.7K requests (la cadena nunca rotó), y el cache hit ya está en ~97.7% sin sticky (el orden de config ya es de facto sticky). El sticky solo paga cuando la cadena rota (cooldown/429/5xx), escenario no observado. Para openclaw/zot (sin sesión) es redundante con el orden de config.
- **Cuándo re-evaluar:** si aparecen cooldowns recurrentes (señal existente: TECHDEBT #5 — GO_2/GO_3 en 429 por cuota mensual). Si se habilita, validar empíricamente con la frecuencia del log `sticky_applied` en mofgw.log (esperado ~0 hoy).
- **Estado:** póliza activa — código listo, default off, ADR-002 documenta el diseño (reordenamiento post-filtro, nunca fuerza cooldown/health/cadena).

### 09 Ago 2026 — Verificación de criterios del programa en prod

- **Métricas vivas confirman el proxy absorbiendo todo el tráfico real:** 18,221 requests / 18,121 streams, **0 failovers, 0 cooldown hits, 87 errores totales** (todos absorbidos — el cliente nunca los ve). RAM 18.6M. Cache hit ~97.7% (2.31B hit vs 55M miss). Costos contabilizados: $77.09 USD total, cliente `zot`, por provider/modelo (`/metrics`).
- **Criterios del programa verificados en prod (plan.md §Criterios):** /metrics contabiliza tokens y costo por cliente/modelo/provider ✅; /v1/models responde (con capabilities, 007-002) ✅; rechazo temprano por ventana (008-001) activo en el binario ✅; omniroute no aparece como servicio ✅ (criterio E2E integral 48h + 10+ agentes = validación de observación pendiente).
- **Telemetría viva (1,943 eventos):** opencode capturado con `X-Session-Id`/`X-Session-Affinity` reales, key paths correctos, sin contenido (privacidad). 
- **Composición habilitada:** `/v1/context` responde 401 sin auth (ruta registrada + protegida — esperado); la verificación autenticada de acumulación de records requiere la key plana del cliente `zot` (operativo del operador, no expuesto en el registro).

### 09 Ago 2026 — Deploy del epic 009 en producción

- **Binario con las 3 features del epic desplegado** (09 Ago, ~02:12 ART): el binario anterior en `~/.local/bin/mofgw` NO tenía 009-001/009-002 (verificado por strings: 0 matches vs 16 del build nuevo). Reemplazado por build fresco (`go build ./cmd/mofgw`, 10.3M), backup en `mofgw.bak-20260809`. Servicio reiniciado: healthz OK (6 providers healthy), state.json restaurado (v1→v2 sin error — C12 funcionando en prod), config cargada, tráfico real fluyendo (WARN "telemetry header negado" de agentes con X-Parent-Session-Id — telemetría 009-000 viva).
- **`context.analysis` HABILITADO en prod** (read-only, no invasivo — P8 de 009-001): `~/.config/mofgw/config.yaml` → `context: {margin: 0.1, analysis: {enabled: true, history_per_session: 50}}`. El endpoint `GET /v1/context` queda activo para el cliente `zot` (auth Bearer).
- **`fallback.sticky_routing` queda DEFAULT OFF** (decisión de criterio): es la feature más delicada para activar en tráfico real (cambia ruteo de requests vivos). Se habilita tras observar la composición funcionando — el diseño post-filtro (ADR-002) garantiza que no rompe la cadena si se activa, pero se prefiere activación escalonada.
- **plan.md actualizado** (commit `3a484f8`): status del cierre del epic 009 + criterios del programa 9/9 done.

## 2026-08-09 — Epic 009 cerrado

### Decisiones relevantes

- **EPIC-009 (mofgw-contexto-analisis) CERRADO — 3/3 features done, integración cross-feature verde (E3) y closure (E4) completados.** El epic cumplió su objetivo: descubrir y optimizar la composición del contexto del tráfico real. Cadena entregada: telemetría de descubrimiento (009-000, desplegada en prod 09 Ago) → composición de contexto `/v1/context` (009-001) → sticky routing por sesión (009-002). Los 4 criterios de aceptación del epic (plan.md:211-215) verificados: E2E cross-feature `TestE2EEpic009_FlujoCompuesto` (6 subtests) verde; suite completa **332 tests `-race`** (326 + 6 cross-feature); **cero bugs cross-feature** (integration-009.md, commit `da8446c`).
- **Dual-keying confirmado por captura real (~103 eventos) — el criterio de cierre del epic (ADR-001, creado en 009-001):** solo opencode manda `X-Session-Id` (header); openclaw/zot no (ni header ni metadata, `detected_ids` = 0 en todos los eventos) → composición y sticky keyed `client|session` para opencode y `client|` para openclaw/zot, conviviendo simultáneamente en runtime.
- **Sticky como reordenamiento post-filtro (ADR-002, creado en 009-002):** `applyStickyReorder` solo mueve al preferido al frente del slice `ready` post-`resolveReady` (cooldown/health/Serves ya aplicaron); nunca fuerza. El criterio del epic "respeta cooldown/health/cadena" (plan.md:215) queda cubierto por diseño, no por accidente.
- **Integración E3 sin fricción:** las 3 features conviven en el proxy sin pisarse — la fase 2 de 009-001 (`UpdateContextUsage`) y el registro sticky de 009-002 (`Affinity().Set`) viven en el MISMO punto (`recordCacheTokens`) y coexisten sin conflicto; el dual-keying se confirmó en el flujo real con y sin sesión. Cero refactor de features done.
- **Operativo pendiente (default off — no altera comportamiento sin habilitarlo):** activar `context.analysis.enabled: true` (009-001) + `fallback.sticky_routing.enabled: true` (009-002) en `~/.config/mofgw/config.yaml` de prod. `telemetry` ya está activa (009-000, desplegada).

### Deuda técnica detectada

- **El epic se lleva al índice consolidado (TECHDEBT):** #20 (gofmt preexistente en `e2e_009000_test.go`, LOW), #21 (concurrencia sticky sin test dedicado — hardening post-cierre, LOW), #22 (`X-Session-Affinity` sin test dedicado — hardening post-cierre, FYI) + nits sin registro de la review 009-002 (idiom `evictLRU`; `AffinityStore` incondicional — aceptados, sin acción). Ninguna bloqueante; el resto de la deuda del epic (16-19) ya está registrada.
- **Dos lecciones de proceso del epic (AUDIT post-hoc, documentadas en TECHDEBT §Deuda de proceso):** (1) 009-001 — el AUDIT previo al RED debe escanear literales de fixture que el spec cambia (bump `stateVersion` 1→2); (2) 009-002 — los contadores/harness deben medir lo que el test afirma (5 de 32 tests nuevos nacieron con bugs de test, detectados en GREEN).

### Próxima feature en cola

- **Ninguna — el epic 009 está cerrado.** El programa mofgw queda con los 9 epics done (001-009). Siguiente paso: criterios de aceptación del programa completo (plan.md §Criterios del programa) — E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo opcional: habilitar `context.analysis` + `sticky_routing` en prod.

### 09 Ago 2026 — Feature: 009-002-sticky-session (cierre de ciclo — última del epic)

## 2026-08-09 — Feature: 009-002-sticky-session

### Decisiones relevantes

- **Afinidad como reordenamiento post-filtro (I2/P9) — el corazón de la feature:** `applyStickyReorder` actúa SOLO sobre el slice `ready` post-`resolveReady` (ya post-cooldown/health/Serves): si el preferido está en `ready`, se mueve al frente preservando el orden relativo del resto; si NO está (cooldown, unhealthy, ya no sirve el modelo, clave ausente/evictada, stickyKey vacío) → slice intacto, comportamiento IDÉNTICO a legacy. NUNCA fuerza un provider que el filtro descartó, nunca cambia `maxAttempts` ni la clasificación de errores. Garantiza el criterio del epic (plan.md:215, "respeta cooldown/health/cadena") y la transparencia total (P7/I1/D6): el cliente nunca percibe el ruteo; única diferencia observable es el log debug `sticky_applied`. → **ADR-002**.
- **Keying `client|session` + `client|` (P2, consume ADR-001):** misma fuente de sesión que 009-001 — `X-Session-Id` solo lectura, nunca upstream (I4). opencode → `clientID|sessionID` (afinidad por sesión real); openclaw/zot sin sesión → `clientID|` (afinidad por cliente, comportamiento deseado según captura 009-000). `X-Session-Affinity` deliberadamente NO se usa (D5/I3: en opencode es idéntico a `X-Session-Id`). `clientID` nunca vacío (token autenticado) → clave nunca `""`.
- **`AffinityStore` efímero LRU (P3/D3/D4):** store en memoria (patrón `CooldownStore`; restart → frío; sin bump de `stateVersion`), evicción LRU por last-used (Set y Get refrescan — un cliente activo no se evicta, C12), cap = `Options.StickyMaxEntries` REUTILIZANDO el MISMO knob `server.max_sessions_retained` (default 100): D1 limita la config a `enabled`, el operador gobierna retención de sesiones y afinidad con un solo knob. Claves `client|` cuentan contra el mismo tope; pérdida benigna (re-registra en frío).
- **`CompleteFor`/`StreamFor` con backward compat total (P8/D2):** refactor del loop de `Complete`/`Stream` a helpers compartidos (`complete`/`stream` + `applyStickyReorder`); `Complete`/`Stream`/`New` legacy intactos (stickyKey `""` → retorno inmediato); `StickyMaxEntries` nuevo en Options (0 = default). Con sticky off (default) el proxy llama legacy → cero diferencias observables (C11), e2e existentes pasan sin cambios.
- **Registro post-éxito del GANADOR (P6):** en `recordCacheTokens` (punto único post-éxito con providerID para stream y no-stream), gated por `s.stickyRouting` (off → store vacío, cero memoria). Registra el provider que RESPONDIÓ/arrancó el stream (no necesariamente el preferido). Fallo total (502) o rechazos pre-router (400/413/limiter/budget/ventana) NO tocan la afinidad (C9); stream arrancado que muere a mitad SÍ registra (cache calentado). Best-effort: error de registro jamás falla el request.
- **No invasivo + bounds (P9):** sin cambios en limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta)/telemetría; sin métricas nuevas (solo log debug); cardinalidad acotada (LRU) + mutex, seguro concurrente. Limitación aceptada (D4): afinidad por clave, no por `(clave, modelo)` — el óptimo de cache del segundo modelo puede no alcanzarse; corrección siempre garantizada vía `Serves`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes, 5 opcionales (4 aceptados como deuda #21-#22 + nits sin registro; 1 aplicado — Nit #5: `max_sessions_retained` documentado en `config.example.yaml`). Suite completa 326 tests `-race` verde (294 + 32 nuevos), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Feature aditiva (test-audit):** 0 tests existentes modificados, 38 archivos untouched, 32 tests nuevos (3 config + 18 router + 11 e2e) que cubren P1-P9 / C1-C13; 5 fixes de tests NUEVOS documentados (§5.2) con lección: **los contadores deben medir lo que el test afirma** (`fakeProvider.Stream` no cuenta en `callCount`; el health check de `buildSticky` contamina contadores; `upstreamOK` no produce costo → budget nunca dispara).
- **Operativo:** la feature cierra el ciclo (merge) y el EPIC-009 (3/3 done). Habilitar `fallback.sticky_routing.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo) — junto con `context.analysis.enabled` de 009-001.

### Deuda técnica detectada

- **TECHDEBT #21** — concurrencia sticky sin test dedicado (C13): garantía estructural (mutex de `AffinityStore`) + `-race` en suite; sin TOCTOU. Test concurrente dedicado → hardening post-cierre.
- **TECHDEBT #22** — `X-Session-Affinity` irrelevante sin test dedicado (I3/D5): garantía por construcción (proxy solo lee `X-Session-Id`) + guard `e2e_008003` untouched. Test dedicado → hardening post-cierre.
- **Nits de la review sin registro** (aceptados, sin acción de mejora): `evictLRU` con flag `first` (idiom Go correcto, refactor cosmético opcional); `AffinityStore` creado incondicionalmente (costo ~120 bytes, consistente con `CooldownStore`); `Get` refresca siempre `LastUsed` (semántica EXACTA del spec P3, revisado "no cambiar").
- **Lección de proceso (test-audit §2/§5.2):** AUDIT previo al RED no ejecutado como sesión formal → 5 de los 32 tests nuevos nacieron con bugs de test (detectados en GREEN: 3 por el implementer respetando AP-4, 2 por el orquestador). Lección: además de escanear fixtures (009-001), escanear que los contadores/harness midan lo que el test afirma medir. Ver entrada de proceso en TECHDEBT.

### Próxima feature en cola

- **Ninguna del epic 009 — las 3 features están done.** Siguiente paso: **integración cross-feature (E3) y closure del epic (E4)** vía `cdad-epic` (chat nuevo): verificar los criterios de aceptación del epic (telemetry.jsonl capturando; `docs/research-context-patterns.md`; `/v1/context?session=<id>`; sticky routing opcional por config transparente que respeta cooldown/health/cadena — este último cubierto por 009-002), habilitar las features en prod y decidir el cierre formal.

### 09 Ago 2026 — Feature: 009-001-context-composition (cierre de ciclo)

## 2026-08-09 — Feature: 009-001-context-composition

### Decisiones relevantes

- **Dual-keying confirmado por captura (~103 eventos) — decisión de cierre del epic resuelta:** opencode manda `X-Session-Id` en header → composición keyed `client|session`; openclaw/zot NO mandan sesión (ni header ni metadata del body, `detected_ids` = 0 en todos los eventos) → composición keyed `client|` (agregado por client_id, fallback diseñado). Ambos keyings conviven simultáneamente en runtime (P2/C15). Fuente: `docs/research-context-patterns.md`. → **ADR-001**.
- **Endpoint `GET /v1/context` con aislamiento por clientID** (auth Bearer sobre `/v1/*`): `?session=<id>` → `scope:"session"` con 404 si la sesión no existe para el cliente (consistente 008-003 P5); sin session → `scope:"client"` con el agregado `client|` (incluye TODO el tráfico del cliente, con y sin sesión). Shape `{client, scope, session?, requests, summary, latest, history}`, ceros/vacío nunca nil (P3).
- **Parser estructural en UN solo recorrido (`composition.Analyze`):** superset de `Walk` (absorbe el refactor del audit 009-000 R1); `Walk` intacto y verificado semánticamente idéntico (C3, schema lock R10 de telemetría verde); privacidad por construcción — solo metadata estructural, nunca contenido (P7/I1).
- **`prompt_tokens_actual` en DOS fases:** fase 1 pre-request (mismo gate que telemetría: body válido, stream y no-stream, incluidos los rechazos por limiter/budget/ventana/upstream — P4/C9); fase 2 post-response con matcheo exacto por `request_id` (evita races entre handlers concurrentes de la misma sesión). Best-effort: error de análisis jamás falla el request (I7).
- **stateVersion 1→2 con backward compat (P5/I6):** `persistedState` gana `Contexts` (`omitempty`, nil-safe para v1); binario nuevo lee snapshot v1 sin error; binario viejo rechaza v2 con el check existente (persist.go:243); round-trip preserva contexts con history.
- **Config `context.analysis` aditiva (P6):** `enabled=false` default (sin records ni memoria; endpoint 200 con ceros/vacío), `history_per_session=50` (0 = sin history, solo agregados), valida `< 0`; INDEPENDIENTE de `telemetry`. Wiring pre-tráfico `srv.SetContextAnalysis(...)` + `m.SetContextHistoryPerSession(...)` (patrón SetContextMargin/SetMaxSessionsRetained, main.go:217-218).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes (1 reportado desestimado con motivo escrito — nil-slices falso positivo: `make(..., 0, len(...))` nunca emite `null`, `[]` garantizado), 5 opcionales aceptados como deuda documentada (#16-#20). Suite completa 294 tests `-race` verde, vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Operativo:** la feature cierra el ciclo (merge). Habilitar `context.analysis.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo).

### Deuda técnica detectada

- **TECHDEBT #16** — redundancia `SetContextHistoryPerSession` en main.go:218 (defensa en profundidad; limpiar en refactor futuro).
- **TECHDEBT #17** — casing PascalCase de `PartType` a nivel entry del endpoint (vs snake_case del summary; consumidores propios no dependen).
- **TECHDEBT #18** — `latest: null` vs "ausente" con `history_per_session=0` (cosmética; representación JSON válida, e2e test la espera).
- **TECHDEBT #19** — ring buffer con prepend O(N) en context.go:171 (FYI de hardening; default 50 → despreciable; deque circular O(1) futuro).
- **TECHDEBT #20** — gofmt preexistente en `e2e_009000_test.go` (preexistente en HEAD, fuera del diff de esta feature; correr gofmt cuando se toque el archivo).
- **Lección de proceso (test-audit §2/§9.1):** AUDIT previo al RED no ejecutado como sesión formal → `TestPersistVersionReject` falló en GREEN porque el bump P5 cambia un literal de fixture (`"version":1` → `"version":2`) y el replace era no-op. Corregido en sesión B (6e28730) preservando la intención. Lección: el AUDIT de una feature con bump de schema debe escanear literales de fixture que el spec cambia, no solo call-sites de firma.
- (menor, test-audit §9.3) acceso a campo interno en `TestPersistContextV2_BackwardCompatV1` (persist_context_test.go:39-44) — alternativa observable (`ContextSnapshot(client, "")`) recomendada para hardening futuro.

### Próxima feature en cola

- **009-002-sticky-session** (pendiente del epic mofgw-009): afinidad de provider por sesión para maximizar cache hit. Consume la composición de 009-001 y la fuente de sesión confirmada (`X-Session-Id` de opencode; client_id para openclaw/zot). Coordinar vía `cdad-epic` (chat nuevo) — el orquestador sugiere volver al epic al cerrar esta feature.

### 09 Ago 2026
- **Feature 009-000-request-telemetry DESPLEGADA en producción:** telemetría de requests activa (`telemetry.enabled: true, sample_rate: 1`), archivo `/home/<user>/logs/mofgw-telemetry.jsonl` (0640). Ciclo CDAD completo: spec → audit → RED → GREEN → review (APPROVE qwen3.7-plus, 0 bloqueantes). Suite 265 tests -race verde.
- **Hallazgo de infraestructura (causa raíz artifacts vacíos):** `fallback.timeout: 120s` cortaba streams largos de sub-agentes (el timeout aplica a todo el intento, no solo TTFB — discrepancia contrato-vs-impl). Fix en prod: 120s→300s. Documentado en `docs/hallazgos/2026-08-09-timeout-streams-largos.md`. Fix de diseño (TTFB-only vs stream_timeout) pendiente en backlog.
- **Descubrimiento en curso (24-48h):** telemetría capturando tráfico real. Primeros datos confirman la investigación — opencode manda X-Session-Id en header; el runtime OpenAI/JS (zot/openclaw) NO manda sesión ni en headers ni en metadata del body.

### 08 Ago 2026
- **Cierre formal del replanteo de eficiencia:** P4 CERRADO — Memory Bank aprobada por Pablo, `.cdad-state.json` marcado epic-closed (006/007/008 done). Validación externa CERRADA (7 APPROVE + 2 REQUEST CHANGES corregidos).
- **EPIC-009 planificado y aprobado:** telemetría de tráfico (009-000) → composición de contexto (009-001) + sticky routing por sesión (009-002). Fase 1 = telemetría: loguear headers/metadata del body del tráfico real para descubrir dónde viven los ids de sesión (investigación confirmó: solo opencode manda X-Session-Id; openclaw y zot no). Destino: telemetry.jsonl dedicado.

### 07 Ago 2026
- **Memory Bank creada** (cdad-scribe): `docs/projectbrief.md` + `docs/activeContext.md` — Etapa 5 del epic cycle completada.
- **GAP 1 resuelto:** `docs/specs/external-reviews/` ya existe con 9 `.resp.json` (evidencia de validación externa). El reporte del orchestrator era incorrecto.

### 06 Ago 2026
- **Replanteo de eficiencia completo:** EPIC-003/006/007/008 — 9 features, todas spec→audit→RED→GREEN→review.
- **SEC-001 security hardening:** 4 fixes de auditoría externa (SSE saneado, X-Agent-Id truncado+TTL, ReadHeaderTimeout, log 0640). Review con qwen3.7-plus (familia distinta).
- **Persistencia de accounting:** SaveState/LoadState JSON atómico (commit f90c0b7).
- **Validación externa:** 7 APPROVE + 2 REQUEST CHANGES. Ambos Majors corregidos (commit 082d10c).
- **Pricing/metadata reales** cableados en config de producción.

### 05 Ago 2026
- **EPIC-001 MVP completo** (commit e426f15d, 27 files, 3808 ins).
- **EPIC-002 completo** (commit 5966c5f4, 133 tests PASS).
- **EPIC-004/005 implementados y desplegados.**
- **Hotfix 001-001-endpoint-fix-content-array:** mofgw devolvía 400 a OpenClaw (content array vs string). Fix: `Messages` → `[]json.RawMessage`.
- **Feature 005-005-verbose** implementada (flags --verbose/--log-file, privacidad testeada).
- **Feature 001-003-fallback-v2** implementada (max_retries como tope de intentos totales, cooldown entre requests).

### 04 Ago 2026
- **Plan de epics aprobado** por Pablo (sesión Telegram) — con auth in-scope del MVP.
- **Todas las specs escritas** (EPIC-001 7/7, EPIC-002 4/4, EPIC-005 4/4).

### 03 Ago 2026
- **Decisiones fundacionales** (Pablo): nombre mofgw, transparencia total, puerto 3369, deploy systemd user, config paths.

## Próximos pasos

1. **EPIC-009 CERRADO** (3/3 features done + integración E3 + closure E4). Operativo opcional pendiente: habilitar `context.analysis.enabled` + `fallback.sticky_routing.enabled` en config de prod (default off).
2. **Criterios de aceptación del programa** (ver plan.md §criterios): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes, /metrics con tokens/costo, /v1/models capabilities, rechazo temprano por ventana.
3. **Deuda técnica** (ver `docs/TECHDEBT.md`): 24 entradas registradas (#16-24 = epic 009 + decisiones), varias cerradas, resto BAJA/FUTURO.
4. **Budget para cliente zot/OpenClaw** (decisión de Pablo pendiente — evaluado y diferido 08 Ago: el gasto es intencional, no se limita).

## Conocimiento operativo clave

- **Config de producción:** `~/.config/mofgw/config.yaml` — providers reales, pricing Zen, context.margin 0.1.
- **State file:** `~/.config/mofgw/state.json` (0600) — contadores de accounting, persistidos cada 10min.
- **Logs:** `/home/<user>/clawd/projects/mofgw/logs/` (permisos 0640).
- **Service:** `systemctl --user status mofgw` — active + enabled.
- **RAM:** ~12MB en operación normal.
- **Cache hit rate:** 94-99% medido en /metrics.

## Archivos de referencia

| Archivo | Qué contiene |
|---------|-------------|
| `docs/projectbrief.md` | Identidad, decisiones irreversibles, epics |
| `docs/activeContext.md` | Este archivo — estado actual, decisiones recientes |
| `docs/epics/plan.md` | Plan completo de epics con decomposición |
| `docs/research-architecture.md` | Arquitectura recomendada (ReverseProxy + cooldown RAM) |
| `docs/research-token-efficiency.md` | Precios verificados, cache providers, thinking capabilities |
| `docs/research.md` | Competidores y decisiones iniciales |
| `docs/USER-GUIDE.md` | Guía de usuario (instalación, config, endpoints) |
| `docs/TECHDEBT.md` | Deuda técnica consolidada (24 entradas) |
| `docs/specs/` | Specs de cada feature (27 directorios) |
| `docs/specs/external-reviews/` | Evidencia de validación externa (9 .resp.json) |
| `docs/specs/CROSS-SPEC-REVIEW.md` | Revisión de consistencia entre specs EPIC-001 |
| `task_plan.md` | Plan de tareas con estado de cada feature |
| `.cdad-state.json` | Estado del ciclo CDAD |
