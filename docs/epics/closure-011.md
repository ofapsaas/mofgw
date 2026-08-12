# Epic 011 — Closure

Cerrado: 2026-08-12

## Resumen

El epic mofgw-011 hizo de mofgw el **único proveedor de IA** de una instancia Odoo 19 enterprise (reemplazo total de OpenAI), sin que Odoo se entere del cambio. Del lado Go, mofgw expone el Responses API de Odoo (`/v1/responses` con traducción Responses↔ChatCompletions), structured output, tool-calling, file attachments, web search grounded y `/v1/embeddings`. Del lado Odoo (módulo local `mofgw_ai`), se alineó la dimensión del vector de embeddings y se registró mofgw como provider nativo. La integración E2E real se verificó en staging (staging-instance): `Odoo → mofgw → deepseek-v4-flash`, 0 llamadas a `api.openai.com`. Las 9 features están done e integradas. El epic cierra el objetivo central: Odoo habla exclusivamente con mofgw.

## Features entregadas

| Feature                    | Descripción                                                                    | Cierre      |
| -------------------------- | ------------------------------------------------------------------------------ | ----------- |
| 011-001-responses-endpoint | `/v1/responses`: traducción Responses↔ChatCompletions (tronco del epic, ADR-004) | 12 Ago 2026 |
| 011-002-structured-output  | `text.format.json_schema` → `response_format` (AI Field Fill)                      | 12 Ago 2026 |
| 011-003-tool-calling       | Ciclo tools ↔ function_call ↔ function_call_output de Odoo                     | 12 Ago 2026 |
| 011-004-file-attachments   | `input_file`/`input_image` → content-array                                         | 12 Ago 2026 |
| 011-005-web-search         | `web_search_preview` → grounded search DDG server-side (ADR-005)                 | 12 Ago 2026 |
| 011-006-embeddings         | `/v1/embeddings` forward a Ollama, modelo forzado por cliente                    | 12 Ago 2026 |
| 011-007-embedding-vector   | Vector(384) + redimensionado schema (ADR-006) — lado Odoo                      | 12 Ago 2026 |
| 011-008-odoo-provider      | Registro de mofgw como provider de Odoo, monkeypatch in-place (ADR-007)        | 12 Ago 2026 |
| 011-009-staging-enterprise | Instancia Odoo enterprise en staging (staging-vps/staging-instance)                      | 12 Ago 2026 |

## Criterios de aceptación

- ✅ Las 9 features done individualmente.
- ✅ E2E chat: AI Agent de Odoo responde vía mofgw → deepseek (`request_llm` → "PONG", curl /v1/responses 200).
- ✅ E2E structured output: `json_schema` traducido (test 011-002).
- ✅ E2E RAG: embeddings 384-dim alineados (vector(384) + modelo all-minilm).
- ✅ E2E file attachment: `input_file`/`input_image` → content-array (test 011-004).
- ✅ E2E web grounding: `web_search_preview` → DDG (test 011-005).
- ✅ mofgw fuerza el modelo de embeddings por cliente (Odoo nunca elige).
- ✅ 0 llamadas salientes a `api.openai.com`.
- ✅ Suite mofgw Go 499 tests `-race` verde + 14 tests del módulo Odoo.

## Retrospectiva breve

### Lo que funcionó bien
- **Contratos cross-feature alcanzaron sin refactor:** la frontera del epic (traducción SOLO en la capa del endpoint, ADR-004) permitió que 002-005 extendieran el tronco 001 sin pisarse; el body Responses viaja crudo por la cadena. Integración E2E real verificada de punta a punta.
- **Verificar contra el código real de Odoo, no contra la spec OpenAI:** alinear el contrato al parser `llm_api_service.py` real (ADR-009) evitó diseñar un Responses "canónico" que Odoo no hablara; la primera llamada E2E devolvió 200 sin ajustes de contrato.
- **Principio "el cliente nunca elige" sostenido (ADR-008):** modelo de embeddings forzado por cliente, web search opt-in, dimensión única en Odoo — el gateway decide, el cliente no adivina.
- **Decisiones de Odoo verificadas end-to-end en VPS** (vector 384, índice recreado, provider registrado) en vez de asumidas.

### Lo que se complicó
- **Ajuste de timing de hooks de Odoo (011-007):** el spec proponía `post_init_hook`; la implementación verificó contra el source que era incorrecto (no recrea índice, no corre en upgrade) → rediseño a `pre_init_hook` + `pre-migrate` (ADR-006). Requirió verificar contra `loading.py`/`migration.py` reales.
- **Monkeypatch del core de Odoo (011-008):** patrón no soportado oficialmente (instanciación por nombre, PROVIDERS por referencia) → acoplamiento a estructura interna documentado (ADR-007); requiere re-evaluación en cada upgrade mayor de Odoo.
- **Bugs de tests en GREEN (007, 008):** tests del módulo Odoo nacieron con bugs (formato de `function_call_output`, `get_values()` strings, normalización de `method`) y se corrigieron en GREEN; refuerza la lección de AUDIT formal previo al RED.

### Aprendizajes para futuros epics
- **El source del sistema cliente es la fuente de verdad del contrato** (código real de Odoo > spec del proveedor); verificar timing de hooks/migraciones contra el código real antes de fijar el diseño.
- **Los límites de extensión de Odoo** (instanciación por nombre, PROVIDERS por referencia, snapshots separados) se redescubren caro; quedan codificados en ADR-006/007 para no pagarlos dos veces.
- **La frontera de traducción en la capa de endpoint** (ADR-004) es un patrón de gateway que aísla features; sostenerlo evitó refactor cross-feature.

## Deuda técnica que se llevó

- **`ir_actions_server.AI_PROVIDER` (D6):** el path de server actions (`state='ai'`) sigue apuntando a openai/gpt-4.1; requiere parchear `ir_actions_server.py:26-27`. Fuera de scope de 008.
- **Embeddings reales sin verificación empírica contra Ollama desplegado:** el E2E de embeddings usó mock; falta verificar con un Ollama accesible desde el VPS.
- **Reindexación de sources:** si una instancia tenía chunks 1536-dim, reindexar tras el redimensionado (paso operativo en README de mofgw_ai).
- **Audio out (whisper/realtime):** fuera de scope del epic; Odoo con mofgw no tendrá voz hasta epic posterior.
- **Gaps de "full parity" documentados en plan.md:** selección dinámica de dimensión por request y auto-sync de dimensión — fuera de scope, deuda operativa.

## Decisiones arquitectónicas tomadas

- [ADR-004 — Traducción Responses en la capa del endpoint (tronco, feature 011-001)](docs/adr/ADR-004-traduccion-responses-en-capa-endpoint.md)
- [ADR-005 — Grounded search outbound a DDG (feature 011-005)](docs/adr/ADR-005-grounded-search-outbound-ddg.md)
- [ADR-006 — Redimensionar vector en Odoo vía pre_init_hook + pre-migrate (feature 011-007)](docs/adr/ADR-006-redimensionar-vector-odoo-pre-init-hook.md)
- [ADR-007 — Registro de provider de Odoo vía monkeypatch in-place (feature 011-008)](docs/adr/ADR-007-registro-provider-monkeypatch-in-place.md)
- [ADR-008 — "El cliente nunca elige": mofgw decide el modelo/capabilities (transversal del epic)](docs/adr/ADR-008-el-cliente-nunca-elige-gateway.md)
- [ADR-009 — Contrato Responses alineado al parser real de Odoo, no a la spec OpenAI (transversal del epic)](docs/adr/ADR-009-contrato-alineado-parser-real-odoo.md)
