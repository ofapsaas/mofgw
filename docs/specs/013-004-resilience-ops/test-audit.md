# Test Audit — 013-004-resilience-ops

**Feature:** hardening resiliencia/ops del provider subprocess. Epic 013-mofgw-cli-subprocess.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec:** `docs/specs/013-004-resilience-ops/spec.md` (aprobado, P1-P8 + C1-C8 + tabla §4.2, 12 tests).

## A) Baseline — tests afectados

- **A MODIFICAR (6 + 1 semántico):**
  - `TestT_IsRefusal` subtest "true" (claude/claude_test.go): input `"I cannot respond to that"` → P8 remueve el marker "cannot" → pasa a false. Cambiar a `"refused due to responsible use policy"`.
  - Ripple de interfaz `TranslateStreamOut(ctx, ...)`: `stubBackend.TranslateStreamOut` (harness_test.go) + 4 call-sites directos (TranslateStreamTextDeltas, SkipControl, NoUsageDone, Empty en claude_test.go).
  - `TestT_UsageFabricatedComplete` (semántico): el stub default emite usage {1,1,2} → tras P1 valida preservación (renombrar/re-doc, NO borrar). Se agrega `T_usage_fabricated_when_zero` para conservar cobertura de fabricación.
- **UNTOUCHED (17+):** todos los engine (serialización, taxonomía errores, sesión/prompt), claude que no tocan TranslateStreamOut, subtest "false" IsRefusal, config HTTP/subprocess. P3 (env allowlist) no rompe tests (solo STUB_*). P1 no toca el path de stream.

## B) Plan de tests nuevos — 12 (tabla §4.2)

Confirmados: necesarios, mapean a postcondición/criterio, comportamiento observable. Harness tricky: `T_usage_fabricated_when_zero` (stub usage cero), `T_stream_toolongline_surfaces_err` (stub línea >1MB via STUB_LONG_LINE), `T_child_env_allowlist` (STUB_ENV_FILE), ctx-guard (timing), TTL (hooks exportados: SetSessionTTL/SetNow/lockCount + sweep determinístico).

## C) Harness

- `STUB_ENV_FILE` en buildStubCLI (`env > "$STUB_ENV_FILE"`) para P3.
- `STUB_LONG_LINE` (línea 1048577 bytes) para P2 (dispara sc.Err/ErrTooLong).
- Ctx-guard: adapter (ctx cancelado + ch unbuffered sin consumidor, retorna ≤500ms) y engine (abandonar stream, luego Complete de la misma sesión completa en plazo acotado).
- TTL: hooks exportados en Provider (SetSessionTTL, SetNow, lockCount) + os.Chtimes para dir stale + sweep determinístico.

## Gate — Test Audit

**Riesgo de regresión: ALTO — ripple de interfaz.** El cambio `TranslateStreamOut(ctx,...)` deja el paquete sin compilar hasta aplicar la firma en un solo paso (engine.go interfaz+call-site, claude.go firma+guard, stubBackend, 4 call-sites). **DECISIÓN del owner (12 Ago):** aplicar el cambio de firma en el RED skeleton (precedente feature-011-005) — los tests caen por AssertionError del guard de ctx no implementado, no por compilación.

- [x] Spec aprobado (P1-P8, C1-C8, tabla §4.2).
- [x] Tests afectados identificados (6 modificar + 1 semántico + 12 nuevos).
- [x] Untouched confirmados (17+; P3/P1 no rompen existentes).
- [x] Harness definido (STUB_ENV_FILE, STUB_LONG_LINE, ctx-guard, TTL hooks).
- [x] Ripple de interfaz decidido (skeleton de RED con firma nueva).

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
