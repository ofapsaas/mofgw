# Epic 019 — Closure: provider-sync-automation

Cerrado: 2026-09-17

## Resumen

El epic 019 automatizó la sincronización del catálogo de providers de mofgw: un ciclo completo `fetch → merge → write → restart → verify → rollback`, agendado cada 60 minutos por systemd, con fallback offline embebido. Antes del epic, el único disparador era el workflow manual (skill `mofgw-provider-sync`); ahora `mofgw-sync --once` ejecuta el ciclo completo contra upstreams reales (models.dev 4.6 MB + listas Zen/Go/OpenRouter), escribe `config.yaml` con edición estructural que preserva comentarios y orden de fallback, valida pre-commit, restartea el server con verificación en 3 fases y rollback restore-only ante fallos, y skipea byte-idéntico cuando nada cambió. Las 7 features están done e integradas; la integración E2E contra upstreams reales se verificó en sandbox (docs/epics/019-provider-sync-automation/integration.md).

## Features entregadas (7/7)

| Feature | Descripción | Cierre |
|---|---|---|
| 019-001-fetch-modelsdev | Fetch `models.dev/api.json` con cache en disco (TTL), flock, retry, digest skip | 11 Sep 2026 |
| 019-002-fetch-zen-go | Listas Zen/Go/OpenRouter + motor genérico `modelscache`, auth condicional | 11 Sep 2026 |
| 019-003-merge-provider-catalog | Merge determinístico → IR `Plan` (strip vendor, alias 2 saltos, espejo, Thinking/derived) | 16 Sep 2026 |
| 019-004-atomic-write-validate | Write atómico yaml.Node (comentarios/orden preservados, merge-back) + `ParseForValidation` + `cmd/mofgw-sync` | 16 Sep 2026 |
| 019-005-reload-signal | Restart systemd + verificación 3 fases + rollback restore-only + defensa M-2 + exit 3 | 17 Sep 2026 |
| 019-006-systemd-timer | Units commiteados + install con backup-on-overwrite + timer 60min/OnBootSec | 17 Sep 2026 |
| 019-007-build-snapshot | Snapshot byte-fiel embebido (4.5 MB) + fallback offline + absorción env-file | 17 Sep 2026 |

## Criterios de aceptación del epic

- ✅ Las 7 features done individualmente (spec→audit→RED→GREEN→review→merge, gates con evidencia).
- ✅ E2E: `mofgw-sync --once` contra upstreams reales en sandbox (fetch 4 fuentes + merge + validación + write atómico; segunda corrida → skip).
- ✅ E2E: hallazgo real de integración (qwen3.8-flash thinking_default stale) resuelto contract-primero (enmienda P4c) con el artefacto verificado.
- ✅ Suite Go completa verde: **974/37 `-race`** + harness `test-install.sh` **48/48** + vet/gofmt limpios.
- ⏳ `GET /v1/models` refleja paridad post-reload + timer activo: PENDIENTE DE DEPLOY (acción del operador: `./scripts/install.sh` + `list-timers`; ver integration.md §4).

## Decisiones arquitectónicas del epic

- **ADR-011** (motor de catálogo: fetch + cache + digest + IR puro) + enmienda 019-002 (motor genérico con generics).
- **Edición estructural sobre regeneración** (config del operador es territorio sagrado: comentarios, orden=fallback, merge-back).
- **Restart estructural, no temporal** (el server no tiene hot-reload de providers; "SIGHUP cuando 017 retome" descartado por incorrecto — 017 es clients-polling).
- **Fail-loud sobre silencio en todo el ciclo** (validación pre-commit, M-2, rollback con exit 3 jamás 0, skip-trap fail-loud).
- **Sin ADR nuevo en 004-007** (decisiones lockeadas en specs con HITL; D2 de 004 ya cubre la frontera durable).

## Deuda técnica que se llevó (para 019-hardening / backlog)

1. **Deploy del timer + paridad live** (operador): `install.sh` + `enable --now` + `MOFGW_SYNC_VERIFY_KEY` en env (integration.md §4).
2. **Regeneración del snapshot sin cadencia** (`scripts/fetch-snapshot.sh` manual; warn > 30d; ~4.5 MB por regeneración en historia git).
3. **Canaries opt-in sin correr** (B17 reload, B4 units, C10/C14, B12 snapshot-live).
4. **Oracles tautológicos aceptados** (B8/B9 verbatim por shortcut no-op — comportamiento superior documentado).
5. **Advisories de reviews** (A-4..A-9 de 004, F8a/F-http-client de 005, secciones vacías, interface FS ancha, http.Client por llamada).
6. **Flakes preexistentes** (`TestE2E010002_TTLExpiry`, `TestPostcondition9_MuestreoBanda` — estadísticos).
7. **OpenRouter anónimo vs autenticado sin contrastar** (heredado de 002).
8. **Doc stale del skill mofgw-client-sync** (curl /v1/models sin auth — R3 de 005).
9. **Anti-bias degradado en 005/006/007** (review mono-familia por harness caído; 004 lo satisfizo — GLM vs deepseek).
10. **L2-P7 de 003** (warning de tiers/context_over_200k solo vía cache_write — limitación documentada).

## Retrospectiva breve

**Lo que funcionó:** la disciplina de gates con evidencia sostuvo la calidad incluso con el harness degradado; el E3 pagó su costo con un bug real (consistencia P4c) que ningún gate de feature habría atrapado; el mini-ciclo RED→GREEN para fixes de review (patrón B-1 de 004) demostró ser el mecanismo correcto para cerrar huecos de contrato sin parches; el backup-on-overwrite (D6) y el rollback restore-only (D9) convirtieron dos operaciones peligrosas (re-install, restart) en reversibles.

**Lo que se complicó:** el runtime de sub-agentes falló 8+ veces durante 005-007 (outputs vacíos, un abort tras 3h de retry, bash matcher bloqueando go/git) → RED/GREEN inline del orquestador con aislamiento degradado (disclosure en state en cada etapa); el test-audit commiteado ANTES del GREEN + reviews adversariales + auditoría de diffs RED→GREEN compensaron estructuralmente. Un commit paralelo de ops (`0edec80` router 401/402) landed mid-ciclo sin interferencia.

**Aprendizajes para futuros epics:**
1. **E3 con datos reales es obligatorio, no ceremonial:** el único bug arquitectónico del epic (P4c) apareció solo contra upstreams vivos — los fixtures sintéticos no lo habrían revelado jamás.
2. **Registrar el test-audit ANTES del GREEN preserva la garantía del TDD** incluso cuando el aislamiento de sesiones falla — el artefacto commiteado es la barrera, no la sesión.
3. **Corregir el origen, no el síntoma:** los dos loops de fixes del epic (B-1 --once, P4c consistencia) se resolvieron a nivel contrato (test primero), nunca como parches — y ambos quedaron más baratos que el atajo.
4. **El harness de deploy merece tests de contrato** (test-install.sh pasó de 6 a 16 tests): el instalador es código de producción con side effects reales; sin harness, el trap del unit divergido habría destruido config del operador.
5. **Documentar el plan cuando el descubrimiento lo invalida** (SIGHUP/017, "lista de acceso completa"): los supuestos del plan que el código refuta deben enmendarse en el acto, no arrastrarse.

## Referencias

- Plan: `docs/epics/019-provider-sync-automation/plan.md`
- Integración E3: `docs/epics/019-provider-sync-automation/integration.md`
- Specs: `docs/specs/019-00{1..7}-*/spec.md` (+ review.md, test-audit.md por feature)
- ADRs: `docs/adr/ADR-011.md`
