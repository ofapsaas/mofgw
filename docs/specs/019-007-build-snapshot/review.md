# review.md — 019-007-build-snapshot (etapa 4, Review two-layer)

## Veredicto

**REQUEST_CHANGES + 2 bloqueantes** (B1: test sin commitear; B2: camino corrupto sin test). Ningún hallazgo invalida el diseño (fallback solo-por-ausencia, disco intacto, fail-soft). Ambos bloqueantes corregidos pre-merge (`190bd70`).

**Desviación estructural del ciclo (registrada en state):** RED y GREEN ejecutados por el ORQUESTADOR inline (runtime de sub-agentes inestable durante toda la etapa 3 — 8º incidente del epic). Compensaciones: test-audit commiteado ANTES del GREEN, diff RED→GREEN auditado (cero gaming), y este review con escepticismo máximo.

## Capa 1 — P1-P14 / I1-I7

| ID | Veredicto | Evidencia |
|---|---|---|
| P1 | PASS | `main.go:297-309` fallback solo-por-ausencia (stat + Available) + flag `fromSnapshot` propagado (F1) |
| P2 | PASS | fallback llama `modelsdev.ParseCatalog` (mismo entry point de 001) |
| P3 | PASS (F3 aplicado) | `Embedded()` + `meta.json` válido; B1 verifica SHA; F3: sha recomputado en runtime |
| P4 | PASS (B2 aplicado + F5) | `ok=false` en vacío/corrupto/mismatch; spec aclara que "meta ausente como archivo" = error de compilación (fail-loud en build) |
| P5 | PASS | presente-pero-corrupto → `stat` OK → sin fallback (discriminación por stat, no por clase de error) |
| P6 | PASS | rama fallback solo `ParseCatalog` en memoria; cero writes |
| P7 | PASS (F2) | Warn con `snapshot_fallback` + 4 attrs; append post-Merge con string canónico; orden del IR solo cosmético, Apply no serializa warnings |
| P8 | PASS | warn staleness solo age>30d; exit intacto |
| P9 | PASS | `main()` puebla desde `Embedded()`; vacío → zero-value = pre-007 |
| P10 | PASS | `upstream.New*Store` sin cambios |
| P11 | PASS | Available=false + sin cache → exit 1 (contrato 003/004 intacto) |
| P12 | PASS | heredoc +1 línea exacta; resto byte-intacto (harness C9) |
| P13 | PASS | install.sh sin writes a `mofgw.service.d/` (solo comentarios); override pre-sembrado byte-exacto (harness) |
| P14 | PASS (F4) | `set -euo pipefail`, curl fsSL + timeout, validación python, api byte-idéntico, meta con sha/RFC3339, inválido → die sin escribir; meta atómica (tmp+mv) |
| I1 | PASS | diff solo `cmd/mofgw-sync/*` + `scripts/*`; el server no ganó bytes |
| I2 | PASS | fetch-snapshot.sh no referenciado por build/tests Go |
| I3 | PASS | ningún path persiste el snapshot a disco |
| I4 | PASS | tiny inyectado; api.json real solo en canary opt-in |
| I5 | PASS | heredoc +1 línea, resto intacto |
| I6 | PASS | drop-ins jamás gestionados |
| I7 | PASS | staleness/ausencia solo warnings |

**Gaming audit:** RED→GREEN sin toques a tests para pasar; el único edit post-RED (B7 "snapshot stale" + helpers runReload/runM2 + B4 del ciclo anterior) fue de ejecutabilidad/contrato, documentado y commiteado (`2a6765c`).

## Capa 2 — Findings

**B1 — Bloqueante (RESUELTO):** `main_test.go` con edit sin commitear post-GREEN (B7 marker canónico) → commiteado, suite re-corrida.
**B2 — Bloqueante (RESUELTO):** camino corrupto sin cobertura determinista → `TestParseMeta_RejectsBad` (5 casos: JSON inválido, vacío, fecha mala, sha vacío, sin fetched_at) llamando al no-exportado in-package.
**F1 — Major (RESUELTO):** doble stat / TOCTOU entre loadSources y usedSnapshot → `loadSources` retorna flag `fromSnapshot`; call sites actualizados mecánicamente (`, _`).
**F2 — Minor (documentado):** append sintético rompe sorted+dedup del IR — cosmético (Apply no serializa warnings); aceptado como binario-added.
**F3 — Minor (RESUELTO):** `Embedded()` confiaba en el sha de meta → recomputa y falla cerrado ante mismatch (forense real).
**F4 — Minor (RESUELTO):** meta.json no atómico → tmp+mv (par crash-consistente).
**F5 — Minor (RESUELTO):** spec sobredicía "meta ausente" → aclarado (archivo ausente = error de compilación, fail-loud en build).
**F6 — Advisory (aceptado):** api.json 4.5 MB en git para siempre; regeneraciones suman historia (documentado; cadencia manual).
**F7 — Advisory:** `//go:embed` blank import — estándar, riesgo nulo.

**Pregunta lateral (sidecar):** sin hallazgo — el sidecar viejo no envenena (P6 no escribe; el próximo fetch real compara fetch-vs-sidecar y el mtime stale fuerza reintento — comportamiento deseado).

## Disclosure anti-bias

Reviewer: **GLM (Zhipu AI, glm-5.3-flash)** en sesión fresca. Implementer: orquestador inline (también GLM) → review mono-familia como única capa externa (degradación A2 del epic, registrada). Mitigaciones: test-audit previo al GREEN + audit de diff RED→GREEN + calibración de severidad sin teatro.

## Conclusión

Todos los findings aplicados pre-merge; suite **973/37 `-race` verde** + harness **48/48** (re-corridos por el orquestador). Última feature del epic cerrada con evidencia.

Status: Review **REQUEST_CHANGES** by cdad-reviewer (GLM/Z.ai) on 2026-09-17
Status: **Sign-off HITL** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — B1/B2 resueltos + F1 Major + F3/F4/F5 aplicados; F2/F6/F7 aceptados+documentados. Gate 4→5 DESBLOQUEADO → merge + memory bank (etapa 5)
