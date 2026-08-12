# Review — 011-007-embedding-vector

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

### 1. post_init_hook corre DESPUÉS del schema sync → el índice ivfflat queda caído
**Ubicación:** mofgw_ai/__init__.py (hook) + spec.md:43
**Problema:** Verificado en source Odoo 19 (loading.py:186→235): init_models (schema sync, recrea índice) corre ANTES del post_init_hook. El hook dropea el índice y nada lo recrea → toda query <=> hace sequential scan (I4 roto).
**Decisión:** **APLICADO** — cambiar `post_init_hook` → `pre_init_hook`. pre_init_hook corre ANTES de registry.load()/init_models() (loading.py:172-186), así que tras el DROP+ALTER, init_models recrea el índice. Verificado end-to-end: tras install, columna vector(384) + índice recreado automáticamente.

### 2. post_init_hook NO corre en upgrade → columna queda en vector(1536)
**Ubicación:** mofgw_ai/__init__.py + spec.md:42
**Problema:** Verificado (loading.py:231-238): post_init_hook solo corre si `update_operation == 'install'`. En upgrade no corre → el override cambia el size en ORM pero la columna PG queda 1536 (P6 roto).
**Decisión:** **APLICADO** — agregar `migrations/1.0/pre-migrate.py` con la misma lógica (pre-migrate corre en loading.py:166, antes de init_models, en upgrade). pre_init_hook cubre install; pre-migrate cubre upgrade. Mutuamente excluyentes.

### 3. El ALTER no es idempotente con guarda
**Ubicación:** mofgw_ai/__init__.py + migrations/1.0/pre-migrate.py
**Problema:** P9 exige idempotencia explícita; sin guarda se depende del cast same-dimension de pgvector (detalle de implementación).
**Decisión:** **APLICADO** — agregar guarda: `SELECT format_type(...)` → si ya es `vector(384)` return (skip). En ambos (hook y pre-migrate).

## Opcionales

### 4. Tests incompletos: 3 de 7 bloques (B2/B3/B6/B7 faltantes)
**Decisión:** **PARCIAL — B6 APLICADO** (test_redimension_idempotente, verifica P9 idempotencia). B2 (core untouched) y B3 (hook mechanism) son structural de menor valor — descartados (el comportamiento está cubierto por B1/B4/B5/B6). B7 (docs operativos) → cubierto por README (#5).

### 5. Sin README — P10/C5 no verificados
**Decisión:** **APLICADO** — README.md con prerequisito operativo de purga de chunks 1536-dim.

### 6. Directorio migrations/1.0/ vacío (dead structure)
**Decisión:** **RESUELTO** — migrations/1.0/pre-migrate.py creado (bloqueante #2).

### 7. Typo "embedd_vector" en comentarios
**Decisión:** **APLICADO** — corregido a "embedding_vector".

### 8. Sin test de "core untouched" (P3/I1)
**Decisión:** **DESCARTAR** (FYI) — verificado manualmente: el módulo no toca archivos del core enterprise/ai. Riesgo bajo.

## Auditoría test↔postcondición

| Postcond. | Test | Estado |
| ----- | ----- | ----- |
| P1 (dim=384) | B1 | ✅ |
| P2 (_get_dimensions=384) | B1 | ✅ |
| P3 (core untouched) | — | ✅ manual (ver #8) |
| P4 (hook mechanism) | — | ✅ manual |
| P5 (DROP antes ALTER) | — | ✅ manual |
| P6 (columna vector(384)) | B4 | ✅ |
| P7 (índice recreado) | B5 | ✅ |
| P8 (schema verified) | B4+B5 | ✅ |
| P9 (idempotente) | B6 | ✅ |
| P10 (docs operativos) | — | ✅ README (#5) |
| I1 (core intacto) | — | ✅ manual |
| I4 (índice se conserva) | B5 | ✅ |

**Nota sobre B5 y bloqueante #1:** confirmado — el índice solo aparecía tras el upgrade manual en la verificación anterior; con pre_init_hook se recrea automáticamente en install.

## Correcciones de diseño evaluadas
- Corrección 1 (post_init_hook → pre_init_hook): **correcta** tras el fix de los bloqueantes.
- Corrección 2 (firma del hook — UN arg `env`): **correcta** (loading.py:177/235).
- Corrección 3 (función en __init__.py): **correcta**.

---

Status: Review completado. 3 bloqueantes resueltos (pre_init_hook, pre-migrate.py, guarda de idempotencia) + opcionales #5/#6/#7 aplicados, #4 parcial, #8 descartado. Verificado end-to-end: 4 tests del módulo pasan, columna vector(384), índice recreado automáticamente en install.
