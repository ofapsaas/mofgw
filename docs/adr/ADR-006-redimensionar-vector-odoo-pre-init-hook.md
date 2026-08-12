# ADR-006: Redimensionar una columna vector en Odoo 19 vía pre_init_hook (install) + migrations pre-migrate (upgrade)

- **Status**: Accepted (feature 011-007-embedding-vector, corrección de diseño del spec)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Alta (verificado contra source Odoo 19 + end-to-end en VPS staging-vps/staging-instance)

## Contexto

Odoo 19 NO migra una columna pgvector automáticamente cuando cambia su dimensión: `update_db_column` compara la columna solo por `udt_name == 'vector'` (sin considerar `size`), y el índice ivfflat existente exige DROP antes de un `ALTER ... TYPE vector(N)`. La feature 011-007 necesita redimensionar `ai.embedding.embedding_vector` de `vector(1536)` a `vector(384)` (dimensión nativa del modelo all-minilm que mofgw fuerza). El spec original proponía un `post_init_hook`; la implementación verificó que esa elección era incorrecta.

## Opciones consideradas

### Opción A: post_init_hook (estrategia original del spec — DESCARTADA)
Pros: corre en install y upgrade. Contras (verificadas en loading.py): corre DESPUÉS del schema sync (`init_models`) → dropea el índice y nada lo recrea (I4 roto); y NO corre en upgrade (`update_operation != 'install'`) → la columna PG queda en 1536 aunque el ORM diga 384 (P6 roto).

### Opción B: solo migraciones `migrations/<ver>/pre-migrate.py` (DESCARTADA para install)
Pros: corre en la etapa 'pre', antes de init_models. Contras (verificado en migration.py:151): `migrate_module` retorna temprano si `load_state != 'to upgrade'` → **no corre en instalación fresca**. El caso real (instalar `mofgw_ai` sobre un `ai` ya instalado) es install → la migración no se ejecuta.

### Opción C: pre_init_hook (install) + migrations/1.0/pre-migrate.py (upgrade) — ELEGIDA
Pros: pre_init_hook corre ANTES de `registry.load()`/`init_models()` (loading.py:172-186) → tras el DROP+ALTER, Odoo recrea el índice vía `apply_to_database`/`check_indexes` automáticamente; pre-migrate cubre el upgrade (corre antes de init_models, loading.py:166). Ambos caminos cubiertos, mutuamente excluyentes. Verificado end-to-end: columna vector(384) + índice recreado automáticamente en install, 4 tests del módulo pasan.

## Decisión

Redimensionar una columna `vector` en Odoo 19 con un **módulo local** usando:
- **`pre_init_hook`** en `__init__.py` (firma `def redimension_embedding_vector(env)` — Odoo 19 lo invoca con UN argumento) → cubre **install**.
- **`migrations/<ver>/pre-migrate.py`** → cubre **upgrade**.
- Ambos emiten `DROP INDEX IF EXISTS <idx>` + `ALTER TABLE ... TYPE vector(N)`, con **guarda de idempotencia** (consultar `format_type` → skip si ya es `vector(N)`).
- **NO** agregar la recreación del índice en el hook: Odoo la hace solo vía `apply_to_database`/`check_indexes` al cargar modelos tras el hook.

## Razones

1. **El índice ivfflat exige DROP antes del ALTER** (verificado: nombre `{tabla}_{key}` = `ai_embedding_embedding_vector_idx`, table_objects.py:135).
2. **post_init_hook es el timing incorrecto** — corre tras el schema sync (índice no se recrea) y no corre en upgrade.
3. **solo migrations no cubre install** (solo 'to upgrade').
4. **La combinación pre_init_hook + pre-migrate cubre install y upgrade** con el mismo código y mutuamente excluyentes, y el índice se recrea automáticamente.

## Consecuencias

- La dimensión del vector se alinea con la del modelo real de embeddings (all-minilm = 384) que mofgw fuerza; el core `enterprise/ai` queda intacto (solo se overridea el field).
- **Paso operativo documentado (no automático):** si la tabla tiene chunks 1536-dim, el `ALTER` falla por incompatibilidad pgvector → purgar antes de migrar (los chunks se regeneran por el cron).
- Este patrón es **reusable** por 011-008 (mismo módulo `mofgw_ai`) y por cualquier futuro cambio de schema vectorial en Odoo.
- Conocimiento de Odoo 19 codificado: orden de hooks vs schema sync y alcance de `migrations/` — no redescubrir por prueba y error.
