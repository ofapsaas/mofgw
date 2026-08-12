# Review — 013-003-config-wiring

**Reviewer model:** mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash).
**Fecha:** 2026-08-12
**Commits revisados:** RED `5e14760`, GREEN `fe9dfe5`, fix bloqueante `e21fa77`.

## A) Contract correctness (P1–P10, I1–I6)

| Postcondición | Veredicto |
|---------------|-----------|
| P1 config type:subprocess válido + defaults | PASS |
| P2 subprocess sin key/base_url | PASS |
| P3 type/backend desconocido → error al cargar | PASS |
| P4 backward compat HTTP | PASS (factory byte-idéntico) |
| P5 factory construye provider/backend correcto | PASS |
| P6 catálogo lista subprocess + no-tools + minimal | PASS |
| P7 tools OMIT (nunca llegan al CLI) | PASS |
| P8 restricción "solo agentes del usuario" | PASS (tras fix bloqueante) |
| P9 allowlist vacía sin restricción + warning | PASS |
| P10 hermeticidad | PASS |
| I1-I6 | PASS (I5 fallback preservado: denied → 404/fallback, no 403) |

## B) Test↔postcondition relevance audit

**PASS.** 16/16 tests presentes y correctamente mapeados (tabla §4.2) + 1 test de regresión post-review (shortestCooldown). 0 huérfanos, 0 gaps. Todos verifican comportamiento observable, sin mocks sobre plumbing.

## C) Hallazgos por severidad

**1 bloqueante RESUELTO (`e21fa77`):**
1. `shortestCooldown` no filtraba `AllowedClients` → un cliente denegado en el degradation-grace path esperaba el cooldown de un provider subprocess que no puede usar (wait espurio / fast-fail incorrecto). **Fix:** `shortestCooldown(model, clientID)` aplica el mismo filtro de allowlist. Test de regresión `TestT_RestrictionDeniedCooldownGraceNoWait` verifica fast-fail inmediato. Corregido un bug del test (variable `http` sombreaba `net/http`).

**No-bloqueantes (documentados):**
- Minor: `validate()` muta los defaults de Command/SessionDir (efecto secundario; mover a Parse/factory en refactor futuro).
- Nit: `ResolveSessionDir` no expande `session_dir: "~"` sin trailing slash.
- Nit: helper `contains` manual vs `slices.Contains` (Go 1.21+).
- FYI: handleModels interface assert — cualquier provider con `Models()` aparece (diseño actual, sin opt-out).
- FYI: health skip de subprocess implícito (no implementa `health.Checker`), correcto.

## D) Conclusión

- **0 bloqueantes** (1 resuelto en `e21fa77`). → merge aprobado.
- **Relevance audit: PASS** (16/16 + 1 regresión).
- **Evidencia empírica:** `go build` OK, `go vet` limpio, `go test ./... -race` **569 tests en 22 paquetes** (el único fallo visto fue un flake estadístico preexistente de muestreo telemetría, no relacionado; pasó al re-correr).

Status: Review passed — 0 bloqueantes. Aprobado para merge por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
