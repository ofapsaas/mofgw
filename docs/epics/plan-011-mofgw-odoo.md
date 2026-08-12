# Epic 011: mofgw como proveedor de IA de Odoo (reemplazo total de OpenAI)

## Resumen

Odoo 19 enterprise habla con OpenAI vía el **Responses API** (`/v1/responses`, `/v1/embeddings`, `/v1/audio/*`, `/v1/realtime/*`). mofgw hoy solo expone `/v1/chat/completions`. Este epic hace que mofgw sea el único proveedor de IA de una instancia Odoo — reemplazo **total** de OpenAI para chat, tool calling, structured output, file attachments, web grounding y RAG — sin que Odoo se entere del cambio. mofgw es el gateway; el cliente nunca elige ni adivina.

## Scope

**In scope:**
- mofgw: endpoint `/v1/responses` (traducción Responses↔ChatCompletions, sync + streaming SSE).
- mofgw: structured output (`text.format.json_schema` → `response_format`).
- mofgw: tool calling (formato tools Responses ↔ chat-completions).
- mofgw: file attachments (input_file / input_image pasan intactos la traducción).
- mofgw: web grounding (`web_search_preview` resuelto con DDG del lado del gateway).
- mofgw: endpoint `/v1/embeddings` (forward a Ollama, modelo forzado por cliente, pricing incluido).
- Odoo: módulo que registra a mofgw como provider nativo del framework `ai`/`ai_app`.
- Odoo: setting de dimensión de embeddings (manual) + `Vector(size=...)` ajustado.

**Out of scope (documentado como gap de "full parity"):**
- Audio: `/v1/audio/transcriptions` (whisper) y `/v1/realtime/*` (sesiones de voz). No entran en este epic.
- Selección dinámica de modelo de embeddings por request: una instancia Odoo queda fijada a una dimensión (Odoo tiene UN `Vector` con UNA dimensión).
- Auto-sync de dimensión entre mofgw y Odoo: la dimensión se configura a mano en Odoo, sabiendo qué modelo/dimensión usa el cliente en mofgw.

## Decomposición en features

| # | Feature ID | Descripción (1 línea) | Dependencias | Paralelizable |
|---|-----------|----------------------|--------------|---------------|
| 1 | 011-001-responses-endpoint | `/v1/responses` en mofgw: traducción Responses↔ChatCompletions, sync + streaming SSE | — | No |
| 2 | 011-002-structured-output | `text.format.json_schema` → `response_format` (AI Field Fill) | 011-001 | No |
| 3 | 011-003-tool-calling | Traducción del formato tools Responses (required + `["type","null"]`) ↔ chat-completions | 011-001 | No |
| 4 | 011-004-file-attachments | Verificar/asegurar que input_file/input_image pasan intactos la traducción | 011-001 | Con 011-002, 011-003 |
| 5 | 011-005-web-search | Resolver `web_search_preview` con DDG del lado mofgw | 011-001 | No |
| 6 | 011-006-embeddings | `/v1/embeddings` forward a Ollama, modelo forzado por cliente, pricing | — | Sí (con 011-001) |
| 7 | 011-007-embedding-vector | Módulo Odoo: setting de dimensión de embeddings (manual) + `Vector(size=...)` | 011-006 | No |
| 8 | 011-008-odoo-provider | Módulo Odoo: registrar mofgw en `PROVIDERS` + `base_url`/key en `LLMApiService`, config por instancia | 011-001…011-007 | No |
| 9 | 011-009-staging-enterprise | Instalar enterprise + módulos AI en staging (staging) para E2E | — | Sí |

## Contratos cross-feature

### Traducción Responses → ChatCompletions (011-001, usado por 002, 003, 004, 005)

mofgw traduce el body del Responses API que manda Odoo al formato chat-completions que hablan los providers upstream, y la respuesta de vuelta. El body del request Responses viaja **crudo** por la cadena de providers (pattern existente de mofgw: transparente al contenido, no se pierden campos). La traducción vive en la capa del endpoint `/responses`, no en el router.

### Web search (011-005)

Odoo manda `{"type": "web_search_preview"}` como tool cuando `web_grounding=True`. mofgw la resuelve: ejecuta búsqueda DDG, inyecta resultados como contexto al modelo upstream, y devuelve respuesta sin que Odoo vea la mecánica. Fuera del epic queda mejorar la calidad del buscador.

### Embeddings (011-006, 011-007)

Odoo llama a `POST /v1/embeddings` con `model` + `dimensions`. mofgw **fuerza** el modelo de embeddings del cliente (ignora lo que mande Odoo, principio "el cliente nunca elige"), lo forwardea a Ollama con ese modelo, y devuelve el vector. `dimensions` que manda Odoo y el tamaño real del vector deben coincidir — se configura a mano en Odoo (011-007) según lo que el cliente tiene habilitado en mofgw. Cambiar de dimensión ⇒ reindexar sources.

## Criterios de aceptación del epic

- [ ] Las 9 features están done individualmente (cada una con su gate de Etapa 5 cerrado).
- [ ] E2E chat: un AI Agent en Odoo enterprise (staging) responde usando mofgw → modelo upstream, con tool calling funcionando.
- [ ] E2E structured output: AI Field Fill extrae datos estructurados (json_schema) vía mofgw.
- [ ] E2E RAG: un knowledge source se indexa con all-minilm (384) vía mofgw → Ollama, y el agent responde del source.
- [ ] E2E file attachment: un agent responde sobre una imagen adjunta.
- [ ] E2E web grounding: el agent responde con datos de búsqueda web vía DDG.
- [ ] mofgw fuerza el modelo de embeddings por cliente; Odoo nunca elige ni adivina.
- [ ] 0 llamadas salientes de Odoo a `api.openai.com` en los flujos probados.
- [ ] Suite mofgw completa verde con `-race` (sin regresiones en los 387 tests existentes).

## Riesgos / deuda esperada

- **Riesgo — dimensiones de embedding:** la dimensión se configura a mano en Odoo. Si un cliente cambia de modelo de embeddings en mofgw a uno de distinta dimensión, hay que reindexar sources en Odoo y ajustar el número. Deuda operativa documentada, no feature.
- **Deuda — audio:** whisper + realtime quedan fuera. Odoo con mofgw no tendrá voz (transcripción ni sesiones realtime) hasta un epic posterior.
- **Riesgo — web_search de baja calidad:** DDG es básico. Se itera a un buscador más potente en epic futuro si se necesita.
- **Riesgo — formatos Responses que cambian:** Odoo 19 usa `responses` API en estado "preview" para varias features; el módulo Odoo tiene que mantenerse alineado a lo que Odoo manda/espera. Mitigación: tests contra el código real de `llm_api_service.py` (disponible en `~/src/odoo/19/enterprise/ai`).

## Stakeholders

- **Aprobador del plan del epic**: Pablo
- **Aprobador de specs de features**: Pablo
- **Operador del resultado**: Ofap / mofgw (gateway), Odoo (instancia cliente)

## Cambios al plan

_Se actualiza durante el loop de features si el plan cambia. Inicialmente vacío._

---

Status: Approved by Pablo (delegado HITL a Ofap, 2026-08-12)
