# ADR-008: "El cliente nunca elige" — mofgw como gateway que decide el modelo y las capabilities

- **Status**: Accepted (decisión transversal del epic 011-mofgw-odoo)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Alta

## Contexto

El epic 011 busca que Odoo 19 use mofgw como único proveedor de IA (reemplazo total de OpenAI). El gateway es un proxy transparente: su principio rector original es "el cliente nunca se entera de que un provider falló". Este epic agrega una capa: **el gateway decide, el cliente no elige**. Odoo (vía su UI y su config de agentes) tendería a mandar el modelo de embeddings que "cree" usar (default `text-embedding-3-small` en `llm_providers.py`), pero mofgw debe forzar el modelo real que sirve cada cliente.

## Decisiones que este principio forzó (transversal del epic)

1. **Modelo de embeddings forzado por cliente (011-006):** `SetClientEmbeddingsModel(clientID, model)` — mofgw ignora el `model` que manda Odoo en `/v1/embeddings` y usa el configurado para ese cliente. El `model` del request de Odoo nunca llega a Ollama ni aparece en la respuesta.
2. **Dimensión única de Odoo fijada a la del modelo que mofgw fuerza (011-007/008):** como mofgw fuerza `all-minilm` (384 dims), Odoo debe declarar `Vector(size=384)` — la dimensión no es negociable por request, es la del modelo del cliente.
3. **Web search opt-in default-off (011-005):** `web_search.enabled: false` — la capability no se activa salvo que el operador la encienda explícitamente en config de mofgw; Odoo no la "descubre" sola.

## Decisión

**mofgw es la única fuente de verdad para modelo y capabilities de cada cliente.** El cliente (Odoo) manda un request con la forma que le convenga; mofgw resuelve el modelo real de ese cliente desde su config, fuerza la capability disponible, y devuelve la respuesta sin que el cliente sepa que se le ignoró su petición de modelo.

## Razones

1. **Evita que el cliente "adivine"** (principio de Make Illegal States Unrepresentable): si Odoo pudiera elegir un modelo que mofgw no sirve o que no matchea la dimensión, el `<=>` de similitud fallaría. Forzar el modelo del cliente garantiza consistencia.
2. **Un solo lugar de config** (mofgw): el operador decide qué modelo/capability tiene cada instancia Odoo, no el usuario final de Odoo.
3. **Transparencia operativa:** el cliente nunca elige ni adivina → no hay divergencia entre lo que el cliente pide y lo que el sistema realmente sirve.

## Consecuencias

- El modelo de embeddings de una instancia Odoo es **estático** (el del cliente); cambiarlo requiere reindexar sources (deuda operativa).
- La dimensión del vector en Odoo es **fija** por instancia y debe coincidir con la del modelo forzado por mofgw.
- Las capabilities (web search, etc.) son **opt-in** a nivel de config de mofgw, no descubiertas por el cliente.
- Este principio es el modelo mental del epic: el gateway decide, el cliente nunca adivina.
