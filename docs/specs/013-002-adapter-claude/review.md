# Review — 013-002-adapter-claude

**Reviewer model:** mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash).
**Fecha:** 2026-08-12
**Commits revisados:** RED `d20c293`, GREEN `b8a211a`, fixes robustez (post-review).

## A) Contract correctness (P1–P11, I1–I6)

| Postcondición | Veredicto |
|---------------|-----------|
| P1 Name="claude" | PASS |
| P2 argv (bin,-p,--session-id,ID,--model,model,--output-format stream-json,...flags) | PASS |
| P3 sin leak de prompt en argv | PASS |
| P4 último user limpio (string/array text) | PASS |
| P5 traducción sin tools | PASS |
| P6 mapeo Complete (stream-json→ChatResponse, stop_reason→finish_reason) | PASS |
| P7 mapeo Stream (un chunk por text_delta, sin usage/[DONE]) | PASS |
| P8 filtrado de eventos de control | PASS |
| P9 IsRefusal (markers) | PASS |
| P10 composición sin tocar el motor | PASS (engine.go sin strings claude, verificado grep) |
| P11 hermeticidad | PASS (stub CLI, cero red) |
| I1–I6 | PASS (I5 verificado empíricamente: suite verde) |

## B) Test↔postcondition relevance audit

**PASS.** 19/19 test IDs presentes y correctamente mapeados (tabla §4.2). 0 huérfanos, 0 gaps. Todos verifican comportamiento observable, sin mocks sobre plumbing. Gaps menores de cobertura (no bloqueantes): mapStopReason default y markers IsRefusal individuales sin test dedicado — misma lógica cubierta.

## C) Hallazgos por severidad

**0 bloqueantes.**

**Importantes (2 — limitaciones de diseño, documentadas para 013-004, no errores del implementer):**
1. `TranslateStreamOut` send bloqueante sin guard de ctx — raíz en la interfaz `Backend` (013-001) que no pasa context. No manifiesta en happy path ni tests. **Fix en 013-004** (cambio de interfaz `TranslateStreamOut(ctx, ...)` o goroutine intermediaria en el engine).
2. `IsRefusal` marker `"cannot"` — riesgo de falsos positivos con errores genéricos de sistema. Spec B-Q7 (ajustar con datos del smoke real). Ajuste sugerido: markers más específicos.

**Menores (3):**
3. Scanner de `TranslateOut` sin buffer explícito — **RESUELTO** (buffer 1MB consistente con el motor).
4. Prompt vacío no detectado — **RESUELTO** (fail-fast `empty user message content`).
5. Stream chunk ID `chatcmpl-<model>` no único — menor; el engine no sobreescribe ID en Stream. Aceptado por ahora.

**Nits (2):** `hasStr` sin usar (dead code en harness); verificación empírica pendiente (ya confirmada por el orquestador).

**FYI (2):** spec B-Q7 sigue abierto (markers refusal con smoke real); scanner no verifica `sc.Err()` (improbable con buffer 1MB).

## D) Conclusión

- **0 bloqueantes** → merge aprobado.
- 2 importantes → deuda documentada para **013-004** (cambio de interfaz del stream + ajuste de markers IsRefusal con smoke real).
- 3 menores → 2 resueltos, 1 aceptado.
- **Relevance audit: PASS** (19/19).
- **Evidencia empírica:** `go build` OK, `go vet` limpio, `go test ./internal/subprocess/... -race` 40/2, `go test ./... -race` 552 tests en 21 paquetes.

Status: Review passed — 0 bloqueantes. Aprobado para merge por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
