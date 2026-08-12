# Test Audit — 013-002-adapter-claude

**Feature:** adapter `claude.Backend` (implementación de la interfaz `subprocess.Backend` de 013-001). Epic 013-mofgw-cli-subprocess.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec:** `docs/specs/013-002-adapter-claude/spec.md` (aprobado por Ofap, P1–P11 + C1–C11 + tabla §4.2).
**Paquete nuevo:** `internal/subprocess/claude/` — 0 archivos; comportamiento net-new.

## A) Baseline — se usan, NO se re-testean

El adapter consume el motor de 013-001 (mergeado, 19 tests verdes) como dependencia de composición. El motor no cambia (I1).

| Contrato de 013-001 | Decisión |
|---------------------|----------|
| `NewProvider` + `Provider.Complete/Stream` | Rely (los tests de integración cablean el adapter real por el motor; no re-verifican spawn/lock/usage/[DONE]) |
| `subprocess.Backend` (interfaz) | Rely (`claude.Backend` la implementa, P10) |
| `provider.ChatResponse`/`StreamEvent`/... | Rely (el adapter produce estas shapes) |
| `auth.ClientIDFrom` vía `auth.Wrap` | Rely (harness `clientCtx` para identidad) |

**Confirmación:** `grep claude` en `internal/**/*_test.go` solo matchea un comentario de harness — cero cobertura de traducción claude existente. Las funciones puras del adapter (`TranslateReq`/`TranslateOut`/`TranslateStreamOut`/`IsRefusal`/`Args`) son 100% net-new.

## B) Plan de tests nuevos — 19 (tabla §4.2)

Confirmados uno a uno: necesarios, mapean a postcondición/criterio correctos, verifican comportamiento observable. Unit tests = funciones puras del adapter real (`claude.New(bin)` + JSON canned, sin spawn); integration tests = a través de `subprocess.NewProvider` + stub claude-shaped.

Los candidatos a "parecer duplicados" (`T_translate_req_last_user`/`_clean_no_tools` vs. homónimos de 001) NO son duplicados: 001 los ejercita vía test-double `stubBackend` (verifica el motor); 013-002 ejercita el **adapter real**. Distinta capa.

## C) Harness

- **`buildClaudeStubCLI(t)`** — script POSIX (0o755 en `t.TempDir()`) que drena stdin (`cat > /dev/null`) y emite líneas de **stream-json de claude** (una por línea, `\n` preservados): `message_start` → `content_block_start(text)` → `content_block_delta(text_delta)` → `message_delta(stop_reason)` → `message_stop`. `STUB_STDERR` configurable (refusal), `STUB_EXIT_CODE` configurable. Debe emitir al menos un `text_delta` + `message_delta` con `stop_reason` en el caso Complete (evita pass vacuo).
- **Wiring:** `claude.New(stubBin)` → `subprocess.NewProvider(..., claude.New(stubBin), logger)`. `argv[0]` = stub → cero binario claude real, cero red (P11/I3).
- **Paquete:** tests en `claude_test` en `internal/subprocess/claude/` (respeta I2: strings claude solo en ese paquete). `clientCtx`/`chatBody`/`hasStr` se replican como mini-helper local (~24 líneas); obligatorio en los 2 tests de integración (el motor fail-closed sin identidad).
- Unit tests sin spawn, sin red.

## Riesgos de regresión (bajo)

1. `T_argv_build` acoplado a B-Q1 — **RESUELTO por el owner (12 Ago): NO incluir `--include-partial-messages`**. Args = `[bin, -p, --session-id, ID, --model, model, --output-format, stream-json, ...flags]`.
2. Gap de composición refusal adapter→motor: no se agrega test (no está en §4.2 → violaría relevancia). Deuda observacional, no bloqueante.
3. Stub claude-shaped debe emitir stream-json válido (si no, `TranslateOut` da error — test honesto).
4. `T_is_refusal_true/_false` = un test con subtests (coherencia de conteo).

## Gate — Test Audit

- [x] Spec aprobado (P1–P11, C1–C11, tabla §4.2).
- [x] Comportamiento net-new (paquete `claude` nuevo).
- [x] Contratos compartidos rely (motor, provider, auth).
- [x] 0 tests a modificar; motor y paquetes untouched.
- [x] 19 tests nuevos mapeados, de comportamiento observable (1 guard de compilación legítimo).
- [x] Sin mocks sobre plumbing; sin duplicación.
- [x] Harness hermético (stub claude-shaped + `claude.New(stubBin)`).

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
