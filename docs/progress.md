# Progress

Estado de features del proyecto mofgw (Memory Bank).

> Archivo **creado el 2026-09-11** — estaba pendiente como deuda desde el epic 010 /
> feature 018-001 (nunca bootstrapeado). Es el primer artefacto de progreso del repo;
> el histórico completo vive en `activeContext.md` (entries por fecha) y `TECHDEBT.md`.

## In progress

<!-- features actualmente en alguna etapa del ciclo CDAD -->

_(ninguna — 019-003 done, 019-004 no arrancada todavía)_

## Done

<!-- features cerradas (mergeadas + Memory Bank actualizado) -->

- **019-001-fetch-modelsdev** (epic 019-provider-sync-automation) — fetch + cache en disco del catálogo models.dev (`internal/modelsdev`). Merged 2026-09-11. Suite 763/30 `-race` verde. Commits: `f99c061` RED, `226728c` GREEN, `c0489bc` POST-AUDIT RED discriminante, `65503d4` fix review. Review REQUEST_CHANGES con B1/B2 resueltos (HITL delegado).
- **019-002-fetch-zen-go** (epic 019-provider-sync-automation) — motor genérico `internal/modelscache` (extraído de 019-001, generics `Fetch[T]`/`Store[T]`, retry/lock/digest/atomic/TTL idénticos) + fuentes `internal/upstream` (FetchZen/FetchGo: 70/37 items reales; FetchOpenRouter: 443 modelos + auth condicional). Merged 2026-09-11. Suite 806/32 `-race` verde. Commits: `1eddbed` RED, `0ba81ab` fix fixture (AP-4), `7baa598` GREEN, `648188f` review. Review 15/15 P PASS; bloqueante cosmético resuelto por HITL (identidad contractual = errors.Is/As).
- **019-003-merge-provider-catalog** (epic 019-provider-sync-automation) — paquete puro `internal/catalogmerge` (Plan/ProviderPlan/Merge): IR de sync determinístico con matching strip-vendor/alias-2-saltos, espejo de pricing/metadata (defaults zen→opencode, go→opencode-go), derivación de Thinking/supported_parameters (extensión aditiva modelsdev), fail-soft por fuente + knobs `sync_source`/`sync_mirror`. Merged 2026-09-16. Suite 836/33 `-race` verde (re-corrida fresca de merge; 1 flake preexistente `TestE2E010002_TTLExpiry` documentado). Commits: `b73694a` RED, `0b9d9fc` GREEN, `0f0fa33` review (APPROVE 0 bloqueantes), `047a064` sign-off HITL S1/S2. Findings S1 (enmienda I7) y S2 (aclaración P2) resueltos y documentados por HITL.

## Queued

<!-- features identificadas pero no arrancadas todavía -->

Epic **019-provider-sync-automation** (backlog; plan: `docs/epics/019-provider-sync-automation/plan.md`):
- 019-004-atomic-write-validate (write atómico config.yaml + validación `config.Parse` pre-commit + binario `cmd/mofgw-sync`)
- 019-005-reload-signal (SIGHUP si hot-reload; si no, restart — 017 pausada)
- 019-006-systemd-timer (timer 60 min + logging estructurado + modo `--once`)
- 019-007-build-snapshot (snapshot embebido del catálogo en build; fallback offline — depende de 001)

Epic **020-mofgw-consumption-report** (planificado 16 Sep 2026, arranca cuando 019-003 libere el epic 019; plan: `docs/epics/020-mofgw-consumption-report/plan.md`):
- 020-001-registry-cost-model (model + cost_usd_src + cost_usd_up nullable en TerminalEvent; captura usage.cost de OpenRouter — verificado empíricamente)
- 020-002-metrics-summary-html (GET /v1/metrics/summary?date= → HTML streaming sobre registry.jsonl + rotados)

## Blocked

<!-- features que dependen de algo externo -->

_(ninguna — 017-mofgw-client-hot-reload sigue pausada en tdd-audit, referenciada en notes/epic_history)_

---

Última actualización: 2026-09-16 (epic 020 planificado y en cola)
