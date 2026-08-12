# Test Audit Report — 011-007-embedding-vector

**Feature:** override de `ai.embedding.embedding_vector = Vector(size=384)` + migración explícita `pre_migrate` (DROP INDEX + ALTER TYPE vector(384)) en un módulo local de Odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-007-embedding-vector/spec.md` (aprobado por Ofap el 2026-08-12, P1-P10 + I1-I5).

## 1. Resumen del comportamiento que cambia

La feature **no cambia ningún comportamiento del repo Go de mofgw**. `internal/` y `cmd/` contienen **0 referencias** a los términos de esta feature (`embedding_vector`, `Vector(size`, `pre_migrate`, `addons_local`, `ai_embedding`). El cambio ocurre **íntegramente del lado Odoo** en un módulo local nuevo (`addons_local/<mod>/`):
1. Overridea el campo `ai.embedding.embedding_vector = Vector(size=384)` (reemplaza `Vector(size=1536)` hardcodeado en ai_embedding.py:32).
2. Declara migración explícita `migrations/<ver>/pre-migrate.py` (etapa `pre`): DROP INDEX + ALTER TYPE vector(384).
3. NO toca ningún archivo del core enterprise `ai` (I1/P3).

**Consecuencia:** ninguna suite Go valida comportamiento que cambie. 0 tests modificados.

## 2. Tests modificados

**NINGUNO (0)** en el repo Go. Grep verificado: 0 coincidencias de `embedding_vector|Vector(size|pre_migrate|addons_local|ai_embedding` en `internal/` y `cmd/`.

## 3. Tests nuevos (tests de módulo Odoo — Python, en addons_local/<mod>/tests/, en el servidor)

| # | Postcond. | Test Odoo (Python) planificado | Verificación observable |
| --- | ----- | ----- | ----- |
| B1 | P1 + P2 | test_get_dimensions_and_field_size.py | `_get_dimensions() == 384` y `field.size == 384` (C1) |
| B2 | P3 + I1 | test_core_untouched.py | `git diff` de los 4 archivos del core vacío tras instalar el módulo (C3) |
| B3 | P4 + P5 | test_pre_migrate.py | folder `migrations/<ver>/pre-migrate.py` existe con `pre_migrate`; SQL con DROP INDEX antes del ALTER (P4/P5) |
| B4 | P6 + P8 | test_column_vector384.py | `information_schema.columns`: `format_type == 'vector(384)'` (P6, P8a) |
| B5 | P7 + I4 | test_index_recreated.py | `pg_indexes`: existe `ai_embedding_embedding_vector_idx` con definición ivfflat (P7, P8b, C6) |
| B6 | P9 + C4 | test_migration_idempotent.py | re-upgrade en instancia ya en vector(384) sin error (P9) |
| B7 | P10 + C5 | test_operational_docs.py | README documenta purga de chunks 1536-dim antes de migrar (P10, D3) |

**Resumen:** 7 bloques de tests de módulo Odoo (Python) cubren P1-P10 completas + I1/I4. Cada bloque mapea a postcondición; ninguno sobra; ninguno depende de estructura interna del core (verifican field size, `_get_dimensions()`, schema real en PostgreSQL).

**NOTA de materialización:** `addons_local/` no existe en esta máquina (path del servidor remoto). Estos tests se materializan/ejecutan en el servidor (`mofgw-staging.example.com`) como parte de RED del módulo local.

## 4. Tests untouched (lista explícita)

**Toda la suite Go de mofgw queda untouched** — 55 archivos `*_test.go`, ~499 tests, 19 paquetes. Incluye explícitamente `e2e_011006_embeddings_test.go` (el forward `/v1/embeddings` Go no cambia — 011-007 solo alinea la dimensión declarada en Odoo). Paquetes: cmd/mofgw, internal/proxy, internal/provider, internal/config, internal/auth, internal/router, internal/limiter, internal/stream, internal/clamp, internal/respcache, internal/metrics, internal/health, internal/composition, internal/absorb, internal/logging, internal/timeouts, internal/singleflight.

## 5. Regression risk assessment

- **Repo Go (mofgw):** riesgo NO — 0 archivos Go tocados.
- **Lado Odoo:** riesgo SÍ (acotado):
  - R1 (P10/D3): chunks 1536-dim preexistentes → ALTER falla por pgvector. Mitigación: purga documentada; staging vacío sin impacto.
  - R2 (I4): si Odoo no recreara el índice tras ALTER, se pierde `<=>`. Mitigación: verificado que apply_to_database recrea índices; cubierto por B5/P7.
  - R3: comparte módulo con 011-008 (D4); la migración + override son independientes del provider → sin fricción cross-feature.

## 6. Gate — Test Audit checklist

- [x] Spec aprobado leído.
- [x] Comportamiento que cambia identificado: NINGUNO en repo Go; override + migración del lado Odoo.
- [x] Tests modificados: 0 (verificado empíricamente).
- [x] Tests nuevos mapeados: P1-P10 → 7 bloques de tests de módulo Odoo (Python).
- [x] Tests untouched listados explícitamente: toda la suite Go.
- [x] Regression risks evaluados (R1/R2 acotados; repo Go sin riesgo).
- [x] Criterio de aceptación = postcondición verificada por test de comportamiento.
- [x] Ningún test depende de estructura interna del core Odoo.
- [x] **VERIFICATION gap:** la suite Go queda intacta por construcción (0 archivos Go). Baseline build/vet validado en features previas del epic.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
