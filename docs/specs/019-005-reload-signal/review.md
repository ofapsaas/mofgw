# review.md — 019-005-reload-signal (etapa 4, Review two-layer)

## Veredicto

**REQUEST_CHANGES — 0 bloqueantes, 1 Major, 3 Minor, 4 Advisory.** El Major (semántica de `Available()`) era fail-loud y seguro, pero violaba el contrato D11/P10 en un escenario plausible (servicio stopped/failed/activating con systemd sano); fix trivial aplicado pre-merge.

**Limitación disclosure:** el sandbox negó la ejecución de `go test` al reviewer (reglas de permiso bash); la verificación 953/36 `-race` se hereda del orquestador y la capa adversarial fue estática (lectura de diff completo RED→GREEN, greps de invariantes, lectura de spec).

**Desviación estructural del ciclo (registrada en state):** el runtime de sub-agentes falló 8 veces durante la etapa 3 (outputs vacíos + 1 abort tras 3h de retry) → RED y GREEN ejecutados por el ORQUESTADOR inline (ambos roles en la misma sesión). El test-audit (§3, commiteado ANTES del GREEN, `f096ab8`) lockea el contrato de tests — la garantía del RED se sostiene en el artefacto; el reviewer verificó que el único edit de test del GREEN (`rTamperFS{FS: osFS{}, ...}`, F7) fue necesario para ejecutabilidad y no debilitó ninguna aserción (diff RED→GREEN auditado).

## Capa 1 — Postcondiciones e invariantes

| # | Veredicto | Evidencia |
|---|---|---|
| P1 | PASS | reloadsig.go:99-113 (Skipped→solo M-2; !Applied→0; Applied→fases); main.go `--no-reload` saltea hasta M-2; se invoca solo tras exit 0 de 004 |
| P2 | PASS | reloadsig.go:168-186 — match→0 (`skip verificado`), mismatch→1 con ambos digests, read-error→1; read-only |
| P3 | PASS | reloadsig.go:134-138 + execSystemdCtl.Restart (CommandContext 30s); unit constante |
| P4 | PASS | reloadsig.go:190-203 — poll 1s, ventana 30s (Clock inyectado) |
| P5 | PASS* | sin Bearer, 2xx, poll 1s/30s; *request timeout 5s vs spec 2s (F2 Minor, aceptado) |
| P6 | PASS | un request, Bearer solo acá, no-200→fail, `object=="list"`, set-compare; created/extra fuera del veredicto |
| P7 | PASS | stash verbatim (cero derivación), temp mismo dir, Chmod mode vigente default 0600, Sync, rename; write-fail→exit 3 sin re-restart |
| P8 | PASS | re-restart + solo F1+F2; todos los caminos retornan 3, jamás 0; fase exacta en el log |
| P9 | PASS | unset→Warn `verify_key_unset`/phase `degraded` + exit 0; 401 con key→rollback |
| P10 | PASS* | Available precede restart, exit 3, cero rollback; *wiring de Available desviado (F1 Major, corregido pre-merge) |
| P11 | PASS | mapeo completo 0/1/2/3; estado NUEVO/PREVIO declarado en logs (hueco F4: completado pre-merge) |
| P12 | PASS | 9 phases exactas en orden real; Info/Warn/Error según spec |
| P13/I6 | PASS | key jamás logueada; errores HTTP = status+URL, jamás headers |
| P14 | PASS | restoreStash solo Rename al config; cero escrituras al sidecar/clients.yaml |
| P15 | PASS | Stash=raw (read inicial), mismo ConfigPath, Unit constante |

**Invariantes:** I1 PASS* (superficie de diffs solo reloadsig + cmd/mofgw-sync), I2 PASS* (cero os.exec/net/http; os.* solo tipos en la interfaz FS — desvío literal de imports D1 documentado F8a), I3 PASS (única escritura = restoreStash, bytes verbatim, sidecar jamás), I4 PASS (cero diffs en cmd/mofgw), I5 PASS (jamás SIGHUP — interfaz no expone señales), I6 PASS (transversal), I7 PASS* (hermético tras fix F6 pre-merge).

## Capa 2 — Findings

**F1 — MAJOR (RESUELTO pre-merge, `1e257df`):** `Available()` conflatía "systemd usable" con "unit activo" — un unit stopped/failed con systemd sano era misdiagnosticado como "systemd no disponible" → exit 3 con causa falsa en el log, cuando el restart habría levantado el servicio. Fix: distinguir por salida (`is-system-running` con tolerancia a "degraded"), no por exit code del is-active del unit. Correcto ahora: unit caído + systemd sano → restart intentado.
**F2 — MINOR (ACEPTADO + documentado):** httpProber timeout único 5s vs 2s/5s por fase — peor caso estira el settle a ~36s; no hay path donde 5s sea insuficiente. Cierra el R2 del audit; desviación aceptada registrada en Memory Bank.
**F3 — MINOR (RESUELTO, `1e257df`):** leak del temp si Rename falla — cleanup agregado (precedente writeAtomic de 004).
**F4 — MINOR (RESUELTO, `1e257df`):** log de addrOld parse-fail sin `state` completo — agregado.
**F5 — (reservado, reclasificado Advisory de preexistencia).**
**F6 — ADVISORY (RESUELTO, `1e257df`):** warn defensivo en `!Applied && !Skipped` (I7 hermético).
**F7 — ADVISORY (registrado):** único edit de test del GREEN (necesario para ejecutabilidad, sin debilitar aserciones — verificado por diff RED→GREEN).
**F8 — ADVISORY:** (a) desvío literal de imports D1 (os/filepath/slog agregados, net/url sin uso) — anotado en Memory Bank; (b) rama muerta del default en serverAddr — eliminada con documentación del acoplamiento a `config.go:415` (`1e257df`).
**Advisory adicional (aceptado):** httpProber crea un http.Client por llamada en polling (≈30 conns por settle) — cosmético; candidato a hardening.
**Sin hallazgo (validados):** polución B7 (única fuente de env = t.Setenv, auto-cleanup); flake TestE2E010002_TTLExpiry y TestPostcondition9_MuestreoBanda: preexistentes (estadísticos/timing), fuera de alcance; B17 canary opt-in.

## Disclosure anti-bias

Reviewer: **GLM (Zhipu AI, glm-5.3-flash)**. Implementer: orquestador inline (también GLM) → esta feature corrió con **review mono-familia como única capa externa** (degradación A2 + aislamiento test-writer/implementer degradado por harness). Mitigaciones compensatorias: test-audit commiteado ANTES del GREEN (contrato de tests congelado ciego), diff RED→GREEN auditado por el reviewer (F7), y este review estático de diff completo. Registrar como deuda de proceso del epic (8º incidente de harness).

## Conclusión

F1/F3/F4/F6/F8b aplicados pre-merge (`1e257df`); suite completa **953/36 `-race` verde** (re-corrida por el orquestador post-fixes; vet limpio; 1 flake estadístico preexistente registrado). Todo lo demás PASS con evidencia.

Status: Review **REQUEST_CHANGES** by cdad-reviewer (GLM/Z.ai) on 2026-09-17
Status: **Sign-off HITL** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — F1 Major resuelto pre-merge + F3/F4/F6/F8b aplicados; F2/F8a/advisories aceptados+documentados. Gate 4→5 DESBLOQUEADO → merge + memory bank (etapa 5)
