# Review — 014-001-registro-unificado

**Reviewer:** cdad-reviewer (familia distinta al implementer)
**Fecha:** 2026-08-18
**Etapa:** 4 (Review two-layer) — Gate 4→5
**Feature:** 014-001-registro-unificado (EPIC-014 mofgw-registro-unificado)
**Commits revisados:** GREEN 9f1ee84 (feat), RED 32e3459 (test), spec 1bc8f21

## Veredicto
**APPROVE — 0 bloqueantes.** 2 observaciones no-bloqueantes (opcional/FYI), sin cambio requerido para cerrar el gate 4→5.

## Layer 1 — Revisión de spec
La spec (1bc8f21, aprobada por Ofap HITL 18 Ago) cumple los 4 criterios del gate 2→3: 4 secciones mínimas (Descripción/Contrato/Invariantes/Criterios), postcondiciones numeradas P1-P17 verificables, criterios medibles C1-C17, marca de aprobación inequívoca (`Status: **Approved** by Ofap on 2026-08-18`). Esquema del contrato tipado en §2.1 (AttemptEvent/TerminalEvent/Tokens exactos). Mapeo test↔postcondición en §2.4. Invariantes I1-I10. Anti-scope claro. Decisiones D1-D15 lockeadas y consistentes.

## Layer 2 — Revisión de implementación (contra la spec)

### Verificación postcondición por postcondición
- **P1 (config):** ✓ `RegistryConfig{Enabled,File}` top-level, default `{false,""}`, validación `enabled && file=="" → error` (config.go:658). Tests C1 (registry_red_test.go: defaults, bloque válido, enabled+file vacío).
- **P2 (off default):** ✓ nil writer = sin emisión ni archivo; main.go solo construye writer si `cfg.Registry.Enabled`. Test C2 (P2_OffDefaultSinArchivo).
- **P3 (writer append-only fail-fast):** ✓ `NewWriter` O_APPEND|O_CREATE|O_WRONLY 0o640, error si no abre. Tests C3 (FailFast, AppendOnly, JSONLValid).
- **P4 (thread-safety):** ✓ mutex serializa escrituras. Test C12 (ThreadSafety 20 goroutines).
- **P5/P6 (esquema exacto attempt/terminal):** ✓ structs coinciden con §2.1; serialización SOLO del esquema. Tests C10 (AttemptSchemaExact, TerminalSchemaExact con `keysIguales` que ordena sets — fix de test-writer legítimo para comparar sets, no multisets).
- **P7 (attempt ok):** ✓ emitAttempt "ok" en paths de éxito de Complete/stream (router.go). Test C4.
- **P8 (attempt fallback):** ✓ emitAttempt "fallback" en TODOS los paths de fallo (retryable agotado, no-retryable, clamp/inject, timeout, stream cerrado). Tests C5/C7.
- **P9 (serialización del retry como una visita):** ✓ un solo attempt con `retries` = reintentos ejecutados (`try-1`). Test C6 (RetryMismoProvider: retries=1).
- **P10 (un terminal por request en routing):** ✓ emitTerminalSuccess en los 4 call-sites de recordCacheTokens chat (stream, sf líder, sf follower, no-stream) + responses.go:511 + embeddings.go:143; emitTerminalError en handleChainError (proxy.go:1156,1160). Tests C4/C7/C8/C13.
- **P11 (correlación request_id):** ✓ `logging.RequestID(ctx)` + `auth.ClientIDFrom(ctx)` en router y proxy. Tests C4/C11.
- **P12 (terminal success exacto):** ✓ tokens del usage real, cost_usd = estimateCost(model, miss, completion, hit) (MISMA fórmula D9), stream = flag real. Tests C4/C9.
- **P13 (terminal error exacto):** ✓ error_code = ChainError.Code, status final, final_provider = ProviderID, tokens/cost 0. Tests C7/C8/C13.
- **P14 (fallback/déficit reflejado):** ✓ p1 falla + p2 responde ⇒ 2 attempts + terminal success final_provider=p2. Test C5.
- **P15 (privacidad):** ✓ writer serializa solo el esquema (por construcción D1); emisores no pasan body/headers. Test C15 (grep negativo de prompts/args/respuesta/header/Auth).
- **P16 (resiliencia runtime):** ✓ best-effort con warn al logger, request sigue. Tests C16 (unit fallible write no panic + e2e request continúa con fd cerrado).
- **P17 (coexistencia con 009-000):** ✓ estructuralmente garantizado (canal independiente registry.NewWriter, knob propio, off default). ⚠️ Sin test dedicado (ver observación O1).

### Anti-patrones y gates
- **No auto-satisfacción:** ✓ tests escritos RED (32e3459, fallan por compilación: undefined RegistryConfig/AttemptEvent) antes que el GREEN (9f1ee84). Los 4 fixes de test en GREEN (keysIguales sort P5/P6, MaxRetries 2→1 C7, Cooldown time.Minute C13) son correcciones de TESTS del test-writer (sesión aislada), no relajación de la implementación — verificados legítimos:
  - `keysIguales`: ordenar el set `want` es correcto (comparar sets, no orden de serialización).
  - `MaxRetries: 1` ⇒ maxAttempts=MaxRetries+1=2 (semántica 002-001): correcto para el escenario C7 de 2 providers.
  - `Cooldown: time.Minute`: necesario para forzar fast-fail degradado (provider en cooldown) — correcto para C13.
- **Aditividad:** ✓ off por default, sin cambio al request path, eventos post-decisión, writer best-effort nunca altera respuesta.
- **Privacidad (I2):** ✓ enforced por construcción; verificado con grep negativo (C15).
- **Commits granulares:** ✓ RED (32e3459) y GREEN (9f1ee84) separados.

## Observaciones (no bloqueantes)
- **O1 (opcional):** P17/C14 (coexistencia con telemetry 009-000) y C17 (endpoints responses/embeddings) no tienen test dedicado. C17 está marcado **opcional** en la spec; P17 está garantizado por construcción (canal/knob independientes) pero sin test explícito. Recomendación: test de convivencia como follow-up opcional (fuera del gate 4→5).
- **O2 (FYI):** el evento terminal error para el follower de un vuelo singleflight fallido (D14) y el stream interrumpido post-primer-byte (D15) están implementados (emitTerminalError en sf líder/follower; terminal success con tokens capturados en handleStream) pero no cubiertos por test E2E dedicado; cobertura por simetría de los paths de emisión compartidos. No exige test por rama (la spec no los marca como criterios individuales).

## Evidencia
- Suite completa: `Go test: 622 passed in 23 packages` (go test ./... -count=1 -race). Nota: 1 de 4 ejecuciones presentó un fallo flaky en `TestE2E010002_TTLExpiry` (test e2e de TTL de la feature 010002, no relacionado con 014-001); re-ejecución consecutiva pasó 622/622.
- go build: OK
- go vet: limpio

## Gate 4→5
Bloqueantes: **0**. Observaciones O1/O2 no bloquean. Review COMPLETA → SIGUIENTE: merge + memory bank (etapa 5, cdad-scribe).
