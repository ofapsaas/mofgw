# EPIC-012-mofgw-odoo-component — Closure

**Estado:** CERRADO (12 Ago 2026)
**Commit:** `8884036`
**Aprobado por:** Pablo (12 Ago 2026)

## Objetivo

Incorporar el módulo Odoo `mofgw_ai` (epic 011, features 011-007 embedding-vector
+ 011-008 odoo-provider) al repo de mofgw bajo `odoo/`, como **componente opcional**
para quien use Odoo 19 con mofgw como proxy de IA. Hasta ahora el módulo solo existía
como copia instalada en el VPS de staging (`addons_local/`), sin versionar en ningún git.

## Hallazgos del discovery

- El fuente del módulo existía localmente y completo en
  `$HOME/domains/mofgw-staging.example.com/addons_local/mofgw_ai/` — esta máquina es el
  host de dominios, así que `mofgw-staging.example.com` y el workstation comparten filesystem.
- Inventario verificado: `__manifest__.py`, `__init__.py` (hook + monkeypatch provider),
  `models/{ai_embedding, ir_actions_server, res_config_settings}.py`,
  `views/res_config_settings_views.xml`, `migrations/1.0/pre-migrate.py`,
  `tests/{test_embedding_vector, test_odoo_provider, test_odoo_provider_e2e}.py`,
  `README.md`.
- Todos los archivos con SPDX headers (Pablo Manuel Rizzo, GPL-3.0-or-later).

## Features entregadas (3/3)

| Feature | Descripción | Estado |
|---------|-------------|--------|
| 012-001-module-versioning | Copia fiel del fuente a `odoo/mofgw_ai/` (sin `__pycache__`/`.pyc`) | ✅ |
| 012-002-component-docs | README del componente + referencias en README raíz y activeContext | ✅ |
| 012-003-distribution-wiring | Distribución opción A (versionar en repo; cada instancia copia a su addons_path) | ✅ |

## Criterios de aceptación verificados

- ✅ `odoo/mofgw_ai/` presente con los 13 archivos fuente + 3 tests, sin pyc
  (verificado con `diff -r` contra el origen: copia fiel).
- ✅ README del componente completo y referenciado desde README raíz (sección
  "Componentes opcionales") y docs/activeContext.
- ✅ **0 cambios** en `internal/`/`cmd/` — suite Go intacta.
- ✅ Build OK, vet OK, **512 tests verdes con `-race`** en 19 paquetes.
- ✅ `.gitignore` ya cubría `__pycache__/` y `*.pyc`.

## Lecciones

- **La copia "instalada en el servidor" puede ser local:** el epic 011 asumió que
  `addons_local/` "solo existía en mofgw-staging.example.com". Al rastrear la sesión de desarrollo
  se descubrió que esa ruta existe localmente (esta máquina es el host de dominios). El
  fuente del módulo nunca estuvo perdido; simplemente no estaba versionado.
- **Rastrear el historial de la sesión** (delegations tree) fue la clave para recuperar el
  path exacto del fuente sin búsquedas amplias ni acceso al VPS real.

## Deuda / pasos siguientes

- **Validación en staging:** los 14 tests del módulo Odoo requieren instancia Odoo 19;
  se validan en una instancia que instale el componente (`odoo-bin -i mofgw_ai`).
- **Distribución opcional (no en scope):** script de instalación que copie el addon a un
  `addons_path` objetivo (opción B del plan, descartada por ahora).

## Referencias

- Commit: `8884036` (`feat(odoo): versionar mofgw_ai como componente Odoo distribuible`)
- Módulo: [`odoo/mofgw_ai/`](../../odoo/mofgw_ai/)
- Epics 011: `docs/epics/closure-011.md`, ADRs 004-009.
