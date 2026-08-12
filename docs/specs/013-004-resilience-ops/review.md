# Review — 013-004-resilience-ops

**Reviewer model:** mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash).
**Fecha:** 2026-08-12
**Commits revisados:** RED `7ce359f`, GREEN `c2c3fd8`.

## A) Contract correctness (P1–P8, I1–I5)

| Postcondición | Veredicto |
|---------------|-----------|
| P1 usage real preservado (fabrica solo si TotalTokens<=0) | PASS |
| P2 sc.Err surfaced (upstream_error, sin [DONE]) | PASS |
| P3 env allowlist (PATH+HOME+STUB_*) | PASS |
| P4 ctx-guard TranslateStreamOut (interfaz + adapter) | PASS |
| P5 TTL sweep disco (stale removidos, in-flight protegido) | PASS |
| P6 lock map eviction (idle+free, re-creable) | PASS |
| P7 defaults de config en Parse | PASS |
| P8 IsRefusal sin "cannot" | PASS |
| I1-I5 | PASS (frontera Backend con ctx, hermeticidad, serialización preservada, config/HTTP sin regresión) |

## B) Test↔postcondition relevance audit

**PASS.** 12/12 tests (tabla §4.2) presentes y correctamente mapeados. Tests modificados correctos (IsRefusal true → marker policy, call-sites ctx, usage rename). 0 huérfanos, 0 gaps. Todos verifican comportamiento observable.

## C) Hallazgos por severidad

**0 bloqueantes.**

**10 opcionales (nits/FYI — polish, documentados, no bloquean):**
- Nit: `lastSweep` inicia en zero (sweep corre en primer request implícitamente).
- Optional: `dirMtime` ignora errores de WalkDir (fail-safe; considerar logging).
- Nit: canal scanErr no drenado en success path (GC; ok).
- Nit: si PATH/HOME ausentes no se agregan al hijo.
- Optional: usage negativo del backend → fabrica (condición <=0; log warning sugerido).
- Nit: IsRefusal lowercase (ok).
- Nit: múltiples providers subprocess comparten defaults (validate chequea IDs duplicados).
- FYI: eviction + clock skew (general TTL).
- Nit: sweepStale concurrente (throttle bajo mutex, ok).
- Nit: TranslateStreamOut skip líneas vacías (ok).

## D) Conclusión

- **0 bloqueantes** → merge aprobado.
- **Relevance audit: PASS** (12/12).
- **Evidencia empírica:** `go build` OK, `go vet` limpio, `go test ./... -race` **581 tests en 22 paquetes**. Un bug de GREEN corregido en el camino: `resolveSession` hacía MkdirAll antes del sweep, y el sweep eliminaba el dir recién creado del request actual con el fakeClock avanzado → fix sweep-antes-de-crear-dir.

Status: Review passed — 0 bloqueantes. Aprobado para merge por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
