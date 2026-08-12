# Spec — 011-007-embedding-vector: dimensión 384 del embedding_vector en Odoo

---
feature_id: 011-007-embedding-vector
feature_name: embedding-vector
epic: 011-mofgw-odoo
status: draft
created_at: 2026-08-12
approved_by: pendiente
depends_on: 011-006-embeddings
---

## Descripción funcional

Hacer que el campo `ai.embedding.embedding_vector` de Odoo 19 enterprise refleje
la dimensión real del modelo de embeddings que mofgw fuerza (all-minilm = 384),
en lugar del `Vector(size=1536)` hardcodeado en `ai_embedding.py:32`.

La alineación de dimensión entre mofgw y Odoo es **config manual por instancia**
(plan-011:50, spec 011-006 D1/I4): mofgw entrega la dimensión nativa del modelo
del cliente, y Odoo debe declarar esa misma dimensión en su columna `vector`.
Como la selección de modelo de embeddings es estática por instancia, la
dimensión se fija en **384** en el override local (all-minilm = 384, plan-011).

**Hallazgo crítico (descubrimiento, verificado):** Odoo NO migra la columna
vector automáticamente. `update_db_column` compara la columna solo por su
`udt_name == 'vector'` (sin considerar la dimensión), por lo que un cambio de
`vector(1536)` a `vector(384)` es un **no-op** del lado del ORM. Y el índice
ivfflat existente (`ai_embedding_embedding_vector_idx`, verificado) exige
**DROP antes** de cualquier `ALTER TABLE ... TYPE vector(384)`. Por eso la
feature necesita una **migración explícita** en un módulo local.

**Estrategia (D1, D2, D4):** un único módulo local de Odoo que hereda
`ai.embedding`:
- **Override del campo** `embedding_vector = Vector(size=384)` (dimensión fija,
  config manual por instancia). `_get_dimensions()` ya lee `size` en runtime
  (ai_embedding.py:37), así que 384 fluye automático a los 2 usos
  (`ai_embedding.py:112` cron, `ai_agent.py:597` RAG).
- **Redimensionado del schema vía `post_init_hook`** (D2, corregido durante
  implementación): `DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx` +
  `ALTER TABLE ai_embedding ALTER COLUMN embedding_vector TYPE vector(384)`.
  El hook corre SIEMPRE al instalar el módulo (independiente de install/
  upgrade). El índice lo recrea Odoo tras el ALTER (`apply_to_database`,
  registry.py:805 + table_objects.py:154-182).

> **Corrección de D2 (verificado en implementación):** inicialmente el spec
> proponía una migración `migrations/<ver>/pre-migrate.py`. Se verificó en el
> código real (migration.py:151) que `migrate_module` retorna temprano si
> `load_state != 'to upgrade'` — es decir, **las migraciones NO corren en
> instalación fresca** de `mofgw_ai`. Como el caso real es instalar `mofgw_ai`
> sobre un `ai` ya instalado (fresh install de mofgw_ai), la migración pre no
> se ejecutaría. Se reemplaza por `post_init_hook` (corre en install y
> upgrade), mecanismo robusto y verificado end-to-end.

**Comparte módulo con 011-008:** 011-007 entrega el override del campo + la
migración; 011-008 (odoo-provider) agrega al MISMO módulo el registro del
provider mofgw/Ollama. Comparten instalación y migración (D4). Este spec cubre
SOLO la parte de 011-007.

**Datos existentes (D3):** si la tabla `ai_embedding` tiene chunks de 1536-dim
ya escritos, el `ALTER ... TYPE vector(384)` falla por incompatibilidad de tipo
pgvector. Paso requerido previo a migrar: purgar esos chunks (se regeneran por
el cron `_cron_generate_embedding`). En staging vacío no es problema. Es un paso
operativo documentado, no una feature.

**Sin setting dinámico:** `Vector(size=...)` es un atributo de **clase**, no por
registro; la columna no se puede desacoplar de un setting dinámico (no
recomendado). 384 fijo en el override.

## Contrato

### Firma (override del campo en el módulo local)

```python
# en el módulo local (hereda ai.embedding)
from odoo.addons.ai.orm.field_vector import Vector

class AIEmbedding(models.Model):
    _inherit = 'ai.embedding'
    embedding_vector = Vector(size=384)
```

### Firma (migración explícita)

```python
# migrations/<version>/pre-migrate.py  (o migrations.py con hook)
def pre_migrate(cr, version):
    cr.execute("DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx;")
    cr.execute("ALTER TABLE ai_embedding ALTER COLUMN embedding_vector TYPE vector(384);")
```

### Postcondiciones

**Override del campo (D1)**

1. **P1 — Dimensión declarada en 384:** el modelo `ai.embedding` del módulo
   local declara `embedding_vector = Vector(size=384)`. El `size` del field
   `embedding_vector` resuelto en runtime es el entero **384**.

2. **P2 — `_get_dimensions()` devuelve 384:** `ai.embedding._get_dimensions()`
   (que lee `self._fields['embedding_vector'].size`, ai_embedding.py:37)
   retorna **384** con el override cargado. Los 2 usos que la consumen
   (`ai_embedding.py:112` cron, `ai_agent.py:597` RAG) reciben 384
   automáticamente, sin cambios en esos archivos.

3. **P3 — El core de `ai` no se modifica:** el override vive en el módulo
   local; no se toca `ai_embedding.py`, `field_vector.py`, `ai_agent.py` ni
   `ai_agent_source.py` del core enterprise.

**Redimensionado del schema (D2, corregido)**

4. **P4 — El redimensionado corre vía `post_init_hook` (instalación/upgrade):**
   el `DROP INDEX + ALTER TYPE vector(384)` se ejecuta en un `post_init_hook`
   del módulo `mofgw_ai` (`redimension_embedding_vector`), que Odoo invoca al
   instalar/actualizar el módulo — tanto en instalación fresca como en upgrade
   (verificado en implementación: las migraciones `migrations/` solo corren en
   'to upgrade', por eso el hook las reemplaza).

5. **P5 — El índice se dropea antes del ALTER:** el hook emite
   `DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx` (verificado:
   nombre `{tabla}_{key}` con `key=_embedding_vector_idx`, table_objects.py:135)
   antes del `ALTER TABLE`. El `DROP IF EXISTS` es idempotente: no falla si el
   índice ya no existe.

6. **P6 — La columna queda en `vector(384)`:** tras el hook, el `ALTER
   TABLE ai_embedding ALTER COLUMN embedding_vector TYPE vector(384)` deja la
   columna con `udt_name='vector'` y dimensión **384**. Verificable consultando
   `information_schema.columns` / `format_type` de la columna
   `ai_embedding.embedding_vector`.

7. **P7 — El índice ivfflat se recrea:** tras el ALTER, Odoo recrea
   `ai_embedding_embedding_vector_idx` vía `apply_to_database`
   (registry.py:805 + table_objects.py:154-182), con la misma definición
   `USING ivfflat (embedding_vector vector_cosine_ops)` heredada del core.

**Verificación de schema (post-condición observable)**

8. **P8 — Schema verificado contra PostgreSQL:** después de aplicar el módulo
   en una instancia (staging vacío, sin datos 1536-dim), se verifica
   empíricamente que: (a) `ai_embedding.embedding_vector` tiene tipo
   `vector(384)`, y (b) existe el índice `ai_embedding_embedding_vector_idx`.

**Idempotencia y datos (D3)**

9. **P9 — El redimensionado es idempotente en schema:** el `DROP IF EXISTS` +
   el `ALTER ... TYPE vector(384)` (en el post_init_hook) son re-ejecutables sin
   error en una instancia que ya tenga la columna en `vector(384)` y el índice
   recreado.

10. **P10 — Datos incompatibles documentados como requisito operativo (D3):**
    el spec documenta explícitamente que una `ai_embedding` con chunks
    1536-dim existentes hace fallar el `ALTER`; el paso requerido es purgarlos
    antes de migrar (los chunks se regeneran por el cron). En staging vacío no
    aplica. (Pendiente de verificación empírica en staging con datos reales —
    ver "Verificaciones pendientes".)

## Invariantes verificables

- **I1 — El core de `ai` enterprise queda intacto:** no se edita
  `ai_embedding.py`, `field_vector.py`, `ai_agent.py` ni `ai_agent_source.py`
  (P3). El contrato del core se preserva; solo se overridea el field.
- **I2 — `_get_dimensions()` sigue siendo la única fuente de dimensión:** los
  llamadores (`ai_embedding.py:112`, `ai_agent.py:597`) leen la dimensión desde
  `_fields['embedding_vector'].size`; no hay un número hardcodeado nuevo en
  esos llamadores.
- **I3 — La columna y su dimensión son consistentes con PostgreSQL:** lo que
  Odoo declara (`Vector(size=384)`) == lo que la DB tiene (`vector(384)`)
  (P1 ⇔ P6).
- **I4 — El índice ivfflat se conserva tras la migración:** no se pierde la
  capacidad de búsqueda de similitud (`<=>` en ai_embedding.py:52); el índice
  se dropea solo temporalmente durante la migración y se recrea (P5 ⇔ P7).
- **I5 — Dimensión fija, sin setting dinámico (decisión del dueño):** la
  dimensión es un atributo de clase (384 fijo en el override); NO se desacopla
  la columna de un setting dinámico.

## Criterios de aceptación

- **C1 (P1, P2):** con el módulo local instalado, `env['ai.embedding']
  ._get_dimensions() == 384`, y `env['ai.embedding']._fields['embedding_vector']
  .size == 384`. Verificable con un test de módulo Odoo.
- **C2 (P4-P7):** al instalar/actualizar el módulo en una DB limpia, la
  migración `pre` corre y: `ai_embedding.embedding_vector` queda en
  `vector(384)` y el índice `ai_embedding_embedding_vector_idx` existe con la
  definición ivfflat. Verificable consultando el schema después del upgrade.
- **C3 (P3, I1):** `git diff` del módulo local no toca ningún archivo del core
  `enterprise/ai` (solo agrega el módulo nuevo).
- **C4 (P9):** re-ejecutar la migración (re-upgrade) no lanza error: el `DROP
  IF EXISTS` y el `ALTER ... TYPE vector(384)` son idempotentes.
- **C5 (P10, D3):** el spec/README del módulo documenta el paso operativo de
  purgar chunks 1536-dim antes de migrar en una instancia con datos; en staging
  vacío el upgrade pasa limpio.
- **C6 (I4):** después del upgrade, una query de similitud (`<=>`) sobre
  `ai_embedding` usa el índice recreado (o al menos el índice existe en
  `pg_indexes` con la definición esperada).

### Mapeo de pruebas de contrato (Etapa 3)

Como es Odoo (Python + SQL + schema), los tests verifican el contrato
observable: override del field, `_get_dimensions()`, y el schema real en
PostgreSQL. No dependen de estructura interna del core.

| Postcondición | Verificación observable                                                             |
| ------------- | ----------------------------------------------------------------------------------- |
| P1, P2        | test de módulo Odoo: `_get_dimensions() == 384`, `field.size == 384`                    |
| P3, I1        | `git diff` del módulo local sin tocar archivos del core `ai`                            |
| P4            | el módulo define `post_init_hook: redimension_embedding_vector` |
| P5            | el hook contiene `DROP INDEX IF EXISTS ai_embedding_embedding_vector_idx` |
| P6, P8        | consulta a `information_schema.columns`: `format_type == vector(384)`                   |
| P7, I4        | consulta a `pg_indexes`: `ai_embedding_embedding_vector_idx` existe                     |
| P9            | re-ejecución de la migración sin error (idempotente)                                |
| P10, D3       | paso operativo de purga documentado; upgrade limpio en staging vacío                |

## Contexto técnico

**Modelos/entidades tocadas:**
- `ai.embedding` (`enterprise/ai/models/ai_embedding.py`): modelo overrideado.
  Campo `embedding_vector` (ai_embedding.py:32), índice
  `_embedding_vector_idx` (ai_embedding.py:33), `_get_dimensions()`
  (ai_embedding.py:36-37), usos del campo en `:52` (query `<=>`), `:97`
  (filtro), `:115` (escritura), `:112` (`_get_dimensions()` en cron). No se
  modifican; se overridean.
- `enterprise/ai/orm/field_vector.py`: `Vector` field (15-26), `_column_type` →
  `pg_vector(self.size)` (24-26). Se usa tal cual en el override; no se toca.
- `ai_agent.py:597`: segundo uso de `_get_dimensions()` (RAG). No se toca.
- `ai_agent_source.py:197`: lectura de `embedding_vector` (filtro). No se toca.

**Hooks/extensión disponibles:**
- `_inherit = 'ai.embedding'` en un módulo local → override del field
  `embedding_vector` (mecanismo estándar Odoo de override de campos heredados).
- Migración explícita: `migrations/<version>/pre-migrate.py` con
  `def pre_migrate(cr)`. Odoo ejecuta `migrate_module(package, 'pre')` en
  loading.py:166, ANTES del schema fix de modelos (loading.py:468/551). Etapa
  'pre' garantiza que el `DROP INDEX` + `ALTER` corren antes del load del
  modelo `ai.embedding`.
- Recreación de índices `models.Index`: `apply_to_database`
  (table_objects.py:154-182) + `check_indexes` (registry.py:805) recrean el
  índice tras el ALTER, con nombre `{tabla}_{key}` = `ai_embedding_embedding_vector_idx`
  (table_objects.py:135).

**Convenciones aplicables:**
- Override de campos `Vector` por `_inherit` + re-declaración: patrón estándar
  Odoo para re-dimensionar un campo heredado.
- Migraciones en `migrations/<version>/` siguiendo la convención
  `migrate(cr, installed_version)` (migration.py:238-257).
- Módulo local compartido con 011-008 (D4): 011-007 agrega override+migración;
  011-008 agrega el provider al mismo módulo. Comparten instalación.

**Verificaciones pendientes (deuda documentada, no bloqueante):**
- VERIFICAR: comportamiento real del `ALTER ... TYPE vector(384)` en staging con
  datos 1536-dim existentes (P10, D3) — en esta máquina no existe la instancia
  (`addons_local` solo en el servidor `mofgw-staging.example.com`). Se documenta el paso
  operativo de purga como requisito; la verificación empírica es E2E en staging.
- VERIFICAR: confirmar en el código real de `update_db_column` del ORM que el
  cambio `vector(1536)`→`vector(384)` es no-op (ya citado en el descubrimiento);
  la migración explícita no depende de eso para su correctitud.
- VERIFICAR: la versión exacta del folder `migrations/<ver>/` según la
  `version` del `__manifest__.py` del módulo local.

## Notas de implementación (orientación, no vinculante)

- Crear el módulo local (carpeta addons_local, p.ej. en el servidor
  `mofgw-staging.example.com`) con `_inherit = 'ai.embedding'` y
  `embedding_vector = Vector(size=384)`.
- Redimensionado del schema en `post_init_hook` del manifest
  (`redimension_embedding_vector`): `DROP INDEX IF EXISTS
  ai_embedding_embedding_vector_idx;` + `ALTER TABLE ai_embedding ALTER COLUMN
  embedding_vector TYPE vector(384);`. El hook se define en `mofgw_ai/__init__.py`
  (no en el manifest — Odoo lo resuelve al importar el paquete), con la firma
  `def redimension_embedding_vector(env)` (Odoo 19 lo invoca con UN argumento:
  `getattr(py_module, post_init)(env)`, loading.py:241). NO usar `migrations/`
  porque solo corren en 'to upgrade' (migration.py:151).
- NO agregar la recreación del índice en el hook: Odoo la hace solo vía
  `apply_to_database`/`check_indexes` al cargar los modelos tras el hook.
- Documentar el paso operativo de purga de chunks 1536-dim (D3) en el
  README/notas del módulo.

## Out of scope

- **Registro del provider mofgw/Ollama** — feature 011-008 (mismo módulo local,
  D4); este spec cubre solo override del campo + migración.
- **Setting dinámico de dimensión** — la columna es de clase, no por registro;
  no se desacopla de un setting (decisión del dueño, "Sin setting dinámico").
- **Ajuste de `_get_dimensions()` o de los llamadores** (`ai_embedding.py:112`,
  `ai_agent.py:597`) — ya leen `size` en runtime; no requieren cambios (P2).
- **Edición del core `enterprise/ai`** — I1/P3: ningún archivo del core se toca.
- **Auto-sync de dimensión mofgw↔Odoo** — configuración a mano (plan-011:22).
- **Cambio de modelo de embeddings** — si un cliente cambia a un modelo de
  distinta dimensión, hay que reindexar sources y ajustar el número; deuda
  operativa (plan-011:66), no feature.

## Cambios

- 2026-08-12: draft inicial (architect, Etapa 2). Decisiones D1-D4 resueltas por
  el dueño delegado. Hallazgo crítico verificado: Odoo no migra la columna
  vector automáticamente; el índice ivfflat exige DROP antes del ALTER. Índice
  real `ai_embedding_embedding_vector_idx` (table_objects.py:135).

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
