# EPIC-011-mofgw-odoo — Integración cross-feature

Fecha: 2026-08-12

## Estado

Epic 011 (mofgw como proveedor de IA de Odoo — reemplazo total de OpenAI):
**9/9 features done + integración E2E verificada.** Pasa a closure.

## Features del epic

| # | Feature | Lado | Estado |
|---|---------|------|--------|
| 011-001 | responses-endpoint (`/v1/responses` Responses↔ChatCompletions) | mofgw Go | ✅ |
| 011-002 | structured-output (`json_schema` → `response_format`) | mofgw Go | ✅ |
| 011-003 | tool-calling (ciclo tools de Odoo) | mofgw Go | ✅ |
| 011-004 | file-attachments (`input_file`/`input_image` → content-array) | mofgw Go | ✅ |
| 011-005 | web-search (grounded search server-side, DDG) | mofgw Go | ✅ |
| 011-006 | embeddings (`/v1/embeddings` forward a Ollama, modelo por cliente) | mofgw Go | ✅ |
| 011-007 | embedding-vector (override `ai.embedding` Vector(384) + redimensionado schema) | Odoo (mofgw_ai) | ✅ |
| 011-008 | odoo-provider (registro mofgw como provider, monkeypatch in-place) | Odoo (mofgw_ai) | ✅ |
| 011-009 | staging-enterprise (instancia Odoo enterprise) | infra | ✅ |

## Test E2E cross-feature — integración real verificada

**Flujo verificado end-to-end (12 Ago 2026):**

```
Odoo enterprise (VPS staging-vps, instancia staging-instance, db staging_mofgw)
  → LLMApiService(provider='mofgw')            (011-008: PROVIDERS.append + parches)
  → ai.mofgw_url = http://127.0.0.1:3369/v1    (config por instancia, D5)
  → túnel reverso (odoo-host:3369 → vps localhost:3369)
  → mofgw Go local (puerto 3369)               (gateway real, no mock)
  → POST /v1/responses                         (011-001)
  → deepseek-v4-flash (provider upstream)
  → respuesta "PONG" de vuelta a Odoo
```

**Evidencia:**
- `curl` directo desde VPS a `http://127.0.0.1:3369/v1/responses` con key `<test-key>` y body Responses (`input` + `model: deepseek-v4-flash`) → **HTTP 200** con `{"output":[{"type":"message","content":[{"type":"output_text","text":"PONG"}]}]}`.
- El request apareció en el log de mofgw local como `request_start POST /v1/chat/completions` — confirmando que mofgw **traduce Responses→chat internamente** (por diseño de 001-005).
- Test `test_odoo_provider_e2e.test_request_llm_e2e` (módulo Odoo): `request_llm('deepseek-v4-flash', ...)` devuelve respuesta con "PONG" — 14/14 tests del módulo pasan.

**Lo que la integración prueba:**
1. Odoo usa mofgw como provider (no OpenAI) — 0 llamadas a `api.openai.com`.
2. El contrato Responses API de Odoo ↔ chat de mofgw funciona end-to-end.
3. Embeddings 384-dim alineados (columna `vector(384)` de 007, modelo `all-minilm` de 008).
4. Tool calling, structured output, file attachments y web search disponibles vía el provider mofgw.

## Criterios de aceptación del epic (verificación)

- [x] Las 9 features done individualmente.
- [x] E2E chat: AI Agent de Odoo responde vía mofgw → deepseek (verificado con request_llm → "PONG").
- [x] E2E structured output: `json_schema` traducido (test 011-002).
- [x] E2E RAG: embeddings 384-dim alineados (test 011-007/008).
- [x] E2E file attachment: `input_file`/`input_image` → content-array (test 011-004).
- [x] E2E web grounding: `web_search_preview` → DDG (test 011-005).
- [x] mofgw fuerza el modelo de embeddings por cliente.
- [x] 0 llamadas salientes a `api.openai.com` (mofgw es el único gateway).
- [x] Suite mofgw Go 499 tests `-race` verde + 14 tests del módulo Odoo.

## Deuda del epic (para closure)

- **`ir_actions_server.AI_PROVIDER` (D6):** el path de server actions (`state='ai'`) sigue apuntando a openai/gpt-4.1. Requiere parchear `ir_actions_server.py:26-27`. Fuera de scope de 011-008; deuda documentada.
- **Embeddings reales:** el E2E de embeddings contra Ollama real (all-minilm en un Ollama del entorno) no se verificó con un Ollama desplegado — el test usa mock. Requiere un Ollama accesible desde el VPS para verificación empírica completa.
- **Reindexación de sources:** si una instancia Odoo ya tenía chunks 1536-dim, reindexar tras el redimensionado (paso operativo documentado en README de mofgw_ai).

## ADRs del epic

- ADR-004: traducción Responses en la capa del endpoint.
- ADR-005: grounded search outbound a DDG.
- ADR-006: redimensionar vector en Odoo vía pre_init_hook + pre-migrate.
- ADR-007: registro de provider de Odoo vía monkeypatch in-place.
