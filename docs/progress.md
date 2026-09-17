# Progress

Estado de features del proyecto mofgw (Memory Bank).

> Archivo **creado el 2026-09-11** — estaba pendiente como deuda desde el epic 010 /
> feature 018-001 (nunca bootstrapeado). Es el primer artefacto de progreso del repo;
> el histórico completo vive en `activeContext.md` (entries por fecha) y `TECHDEBT.md`.

## In progress

<!-- features actualmente en alguna etapa del ciclo CDAD -->

_(ninguna — epic 019 loop completo 7/7; sigue integración E3)_

## Done

<!-- features cerradas (mergeadas + Memory Bank actualizado) -->

- **019-001-fetch-modelsdev** (epic 019-provider-sync-automation) — fetch + cache en disco del catálogo models.dev (`internal/modelsdev`). Merged 2026-09-11. Suite 763/30 `-race` verde. Commits: `f99c061` RED, `226728c` GREEN, `c0489bc` POST-AUDIT RED discriminante, `65503d4` fix review. Review REQUEST_CHANGES con B1/B2 resueltos (HITL delegado).
- **019-002-fetch-zen-go** (epic 019-provider-sync-automation) — motor genérico `internal/modelscache` (extraído de 019-001, generics `Fetch[T]`/`Store[T]`, retry/lock/digest/atomic/TTL idénticos) + fuentes `internal/upstream` (FetchZen/FetchGo: 70/37 items reales; FetchOpenRouter: 443 modelos + auth condicional). Merged 2026-09-11. Suite 806/32 `-race` verde. Commits: `1eddbed` RED, `0ba81ab` fix fixture (AP-4), `7baa598` GREEN, `648188f` review. Review 15/15 P PASS; bloqueante cosmético resuelto por HITL (identidad contractual = errors.Is/As).
- **019-003-merge-provider-catalog** (epic 019-provider-sync-automation) — paquete puro `internal/catalogmerge` (Plan/ProviderPlan/Merge): IR de sync determinístico con matching strip-vendor/alias-2-saltos, espejo de pricing/metadata (defaults zen→opencode, go→opencode-go), derivación de Thinking/supported_parameters (extensión aditiva modelsdev), fail-soft por fuente + knobs `sync_source`/`sync_mirror`. Merged 2026-09-16. Suite 836/33 `-race` verde (re-corrida fresca de merge; 1 flake preexistente `TestE2E010002_TTLExpiry` documentado). Commits: `b73694a` RED, `0b9d9fc` GREEN, `0f0fa33` review (APPROVE 0 bloqueantes), `047a064` sign-off HITL S1/S2. Findings S1 (enmienda I7) y S2 (aclaración P2) resueltos y documentados por HITL.
- **019-004-atomic-write-validate** (epic 019-provider-sync-automation) — write atómico de config.yaml vía edición estructural yaml.Node (`internal/configsync`: comentarios preservados, orden in-place = cadena fallback, merge-back, thinking_default jamás tocado) + validación pre-commit `config.ParseForValidation` (API aditiva, sin env keys) + binario `cmd/mofgw-sync` (--no-fetch cache-only, --once no-op, exit 0/1/2) + skip byte-idéntico con sidecar `config.yaml.sha256` (auto-cura). Merged 2026-09-17. Suite 903/35 `-race` verde (re-corrida fresca de merge). Commits: `e6ba734` spec, `1ac13ea` spec aprobado, `3764b29` test-audit, `211f2d7` audit aprobado, `6d63adf` RED (21 tests B1-B21), `98d1826` GREEN, `eb29036` fixes AP-4, `7401710` review, `6fed1d9` mini-RED --once, `c3ea424` mini-GREEN --once, `efae476` sign-off. Review REQUEST_CHANGES → resuelto (B-1 corregido en el origen, contract-primero); anti-bias SATISFECHO 1ª vez en el epic (GLM/Z.ai vs deepseek).
- **019-005-reload-signal** (epic 019-provider-sync-automation) — fase post-write de `mofgw-sync`: restart systemd (`internal/reloadsig` puro con interfaces inyectadas) + verificación 3 fases (is-active → /healthz → paridad de IDs por SET en /v1/models con `MOFGW_SYNC_VERIFY_KEY`) + rollback restore-only de los bytes previos (exit 3 jamás 0) + defensa M-2 del skip-trap + `--no-reload` + exit code 3. Restart estructural (017 es clients-polling — "SIGHUP" del plan epic descartado por incorrecto). Merged 2026-09-17. Suite 953/36 `-race` verde (re-corrida post-fixes). Commits: `c6720e2` spec, `f096ab8` audit, `391d883` RED, `58d5d47` GREEN, `1e257df` fixes review (F1 Major: Available() distinguía mal manager sano de unit caído), `7af4d22` review+sign-off. 8º incidente de harness (RED/GREEN inline por orquestador, contract-primero).
- **019-006-systemd-timer** (epic 019-provider-sync-automation) — unidades commiteadas `scripts/systemd/mofgw-sync.{service,timer}` (oneshot + timer 60min/OnBootSec, fuente única) + install.sh extendido (binario sync, copia idempotente, start_sync_timer con verificación de agenda, uninstall sync, resumen P12) + corrección estructural D6: backup-on-overwrite universal para los 3 units (incluido `mofgw.service`) + aviso LOUD de divergencia del server. Merged 2026-09-17. Suite 959/36 `-race` verde + harness bash 40/40. Commits: `70b7e84` spec, `0f3ee06` audit, `e338154` RED golden, `c776a3e` GREEN, `e630c93` fixes review (F1 doc, F2 warning+scope 007, F3/F4/F5), `496d284` review+sign-off. Deuda: absorción env+flags al template → 019-007; canary B4 opt-in sin correr (deploy real).
- **019-007-build-snapshot** (epic 019-provider-sync-automation) — snapshot byte-fiel models.dev embebido en el binario sync (`cmd/mofgw-sync/snapshot/`: api.json REAL 4.5 MB 221prov/7847mod + meta.json + `Embedded()` con sha recomputado) + fallback solo-por-ausencia en `loadSources` (parse de 001, en memoria, jamás persistido) + visibilidad (evento + warning sintético + staleness) + `scripts/fetch-snapshot.sh` (manual, fake testeable) + absorción `EnvironmentFile` al template del server (cierre F2; drop-in documentado, jamás gestionado). Merged 2026-09-17. Suite 973/37 `-race` verde + harness 48/48. Commits: `8f54d8f` spec, `4c40d39` audit, `393a4c0` RED, `ed1833f` GREEN, `190bd70` fixes review (F1 TOCTOU→flag, B2 parseMeta, F3/F4/F5), `51e4da2` review+sign-off. **Epic 019 loop completo: 7/7.**

## Queued

<!-- features identificadas pero no arrancadas todavía -->

Epic **019-provider-sync-automation** (backlog; plan: `docs/epics/019-provider-sync-automation/plan.md`): 7/7 features done — sigue integración E3.

Epic **020-mofgw-consumption-report** (planificado 16 Sep 2026, arranca cuando 019-003 libere el epic 019; plan: `docs/epics/020-mofgw-consumption-report/plan.md`):
- 020-001-registry-cost-model (model + cost_usd_src + cost_usd_up nullable en TerminalEvent; captura usage.cost de OpenRouter — verificado empíricamente)
- 020-002-metrics-summary-html (GET /v1/metrics/summary?date= → HTML streaming sobre registry.jsonl + rotados)

## Blocked

<!-- features que dependen de algo externo -->

_(ninguna — 017-mofgw-client-hot-reload sigue pausada en tdd-audit, referenciada en notes/epic_history)_

---

Última actualización: 2026-09-16 (epic 020 planificado y en cola)
