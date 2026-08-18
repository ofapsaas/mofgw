# Review — 015-001-inter-attempt-delay

**Status: APPROVE — 0 bloqueantes.** Reviewer: familia de modelo distinta al implementer (revisión independiente).

## Veredicto

La implementación satisface las 8 postcondiciones (P1-P8) con tests de comportamiento
que verifican el contrato observable (timing + provider elegido + config parseada),
no detalles internos. Off por default (P8), aditivo, reversible. Suite completa verde
con -race (Gate 3→4 cerrado).

## Capa 1 — Revisión de spec

- Spec CDAD formal (Problema/Objetivo/Diseño/Contrato P1-P8/Criterios/Anti-scope/Config) aprobada por Ofap (GVR, 17 Ago).
- Postcondiciones numeradas, medibles, testeables. Anti-scope claro (transparencia, cooldown, sticky, cache, max_retries intactos).
- La feature es un knob aditivo con default 0: cumple el gate 2→3 (marca de aprobación presente).

## Capa 2 — Revisión de implementación contra spec

| Postcondición | Satisfecha | Evidencia en código | Test |
|---|---|---|---|
| P1 — Config parsea/valida duración, default 0 | ✅ | `config.go:96` (`yaml:"inter_attempt_delay"`), `validate()` `:485-487` (negativo rechazado) | `Test015001_P1_Default...`, `_Explicit...`, `_Negative...` |
| P2 — inter_attempt_delay=0 → traversal idéntico sin delay | ✅ | `interAttemptDelaySleep` retorna true inmediato cuando `interDelay<=0` (`router.go:753`) | `Test015001_P2_DefaultIdenticalTraversalNoDelay` (vía `New`, default 0) |
| P3 — N>0, fallo transitorio + mismo base_url → sleep N | ✅ | `interAttemptDelaySleep` (`router.go:752-758`) + tracking `prevBaseURL/prevTransient` (`interDelayPrev` `:739-744`) en loops complete `:866-868,947` y stream `:1021-1023,1228` | `Test015001_P3_DelayTransientSameBaseURL` |
| P4 — 429 → nunca delay | ✅ | `transientStatus` (`:731-733`): `>=500 && !=429`; 429 → no transient → sin delay | `Test015001_P4_NoDelayOn429` |
| P5 — distinta base_url → sin delay | ✅ | `:753` `prevBaseURL != s.BaseURL` → retorna true inmediato | `Test015001_P5_NoDelayDifferentBaseURL` |
| P6 — delay respeta cancelación del ctx | ✅ | `sleepCtx` (`:713-725`) select ctx.Done → false → retorno ChainError inmediato | `Test015001_P6_CancelDuringSleepReturnsImmediately` |
| P7 — Suite completa verde, build/vet limpios | ✅ | Verificado empíricamente: `go test ./... -race -count=1` 0 FAIL (20 ok + 2 no test files), build=0, vet=0, gofmt limpio | suite completa |
| P8 — Aditivo, off por default, reversible | ✅ | Default 0 en config y Options; delay condicional (knob>0 AND transient AND misma base_url); solo cambios aditivos | P2 (transitivo) + config P1 default 0 |

## Verificación test ↔ postcondición (auditoría)

- Cada postcondición P1-P8 tiene al menos un test; ningún test sobrante sin mapeo.
- Tests verifican contrato observable (timing, provider ganador, config parseada), no plumbing.
- No hay mocks sobre estructura interna. El contrato nuevo (`Options.InterAttemptDelay`, `ProviderSpec.BaseURL`) fue declarado en el RED y materializado por GREEN — sin modificación de tests a posteriori para hacerlos pasar (no auto-satisfacción).
- La rama `stream` comparte el mismo helper `interAttemptDelaySleep` (wiring `:1021-1023`), aunque las tests P3-P6 ejercitan solo `complete` — cobertura streaming es por simetría del helper, no por test directo.

## Hallazgos

- **Bloqueantes:** ninguno.
- **Optional:** no hay test directo que ejerza el delay en la rama `stream` (el RE se testea vía `complete`; el helper es compartido). Cobertura aceptable; el spec no exige test por rama.
- **Nit/FYI:** en `P6` el test asevera `err != nil` pero no verifica que sea el ChainError de cancelación; suficiente para el contrato (retorno inmediato, no dormir pasada la cancelación).

## Observaciones positivas

1. Aditividad limpia: un knob + un helper condicional, sin tocar cooldown/sticky/cache/retry.
2. `transientStatus` captura exactamente la clase transitiva del spec (5xx retryable y timeout/network→502) excluyendo 429.
3. Guard `prevBaseURL != ""` da backward-compat: providers legacy sin `base_url` nunca retrasan.
4. Config negativa rechazada en validación (defensa temprana).

**Resumen: 0 bloqueantes. APPROVE.**
