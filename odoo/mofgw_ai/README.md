# mofgw_ai

**Componente opcional de mofgw** para integración con Odoo 19 enterprise.
Módulo Odoo que hace de mofgw el proveedor de IA de una instancia Odoo
(reemplazo total de OpenAI). Versionado en el repo de mofgw bajo `odoo/`;
cada instancia Odoo que use mofgw como proxy de IA copia este addon a su
`addons_path` y lo instala con `odoo-bin -i mofgw_ai`.

Origen: epic 011-mofgw-odoo del proyecto mofgw (features 011-007 + 011-008).

- **011-007-embedding-vector**: override de `ai.embedding.embedding_vector` a
  `Vector(size=384)` (dimensión nativa de all-minilm que mofgw fuerza por
  cliente) + redimensionado del schema vía `pre_init_hook` (install) y
  `migrations/1.0/pre-migrate.py` (upgrade).
- **011-008-odoo-provider**: registro del provider mofgw/Ollama (feature
  hermana, mismo módulo).

## Requisitos

- Odoo 19 enterprise con los módulos `ai`, `ai_app`, `ai_fields`.
- Extensión PostgreSQL `vector` (pgvector) instalada en la DB.
- mofgw desplegado y accesible (endpoints `/v1/responses` y `/v1/embeddings`).

## Instalación

```bash
# como usuario del servidor Odoo
odoo-bin -c odoo.conf -d <db> -i mofgw_ai --stop-after-init
```

El `pre_init_hook` ejecuta `DROP INDEX` + `ALTER TYPE vector(384)` ANTES del
schema sync, y Odoo recrea el índice ivfflat sobre la columna redimensionada.

## ⚠️ Prerequisito operativo: instancias con chunks de embeddings preexistentes

Si la tabla `ai_embedding` ya contiene chunks embebidos con la dimensión
**1536** (el valor anterior del core `ai`), el `ALTER TABLE ... TYPE
vector(384)` **falla** por incompatibilidad de tipo pgvector (los vectores
existentes son de 384-dim, no caben en la columna redimensionada a 384 si
tienen 1536, o viceversa — el ALTER a menor dimensión no puede convertir
vectores de mayor dimensión).

**Paso requerido antes de instalar/actualizar el módulo en una instancia con
datos:** purgar los chunks existentes de `ai_embedding` (se regeneran por el
cron `ai.ir_cron_generate_embedding`):

```sql
-- OPCIONAL: purgar solo si hay chunks 1536-dim preexistentes
DELETE FROM ai_embedding;
```

En instancias nuevas (sin datos en `ai_embedding`) no aplica: el upgrade pasa
limpio.

> Verificado end-to-end en staging (staging-instance / staging-vps): columna
> `vector(384)`, índice ivfflat recreado, 3 tests del módulo pasan.

## Tests

```bash
odoo-bin -c odoo.conf -d <db> --test-enable --workers 0 --test-tags mofgw_ai
```
