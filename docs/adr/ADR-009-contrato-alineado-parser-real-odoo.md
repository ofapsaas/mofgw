# ADR-009: Contrato Responses alineado al parser real de Odoo (`llm_api_service.py`), no a la spec OpenAI

- **Status**: Accepted (decisión transversal del epic 011-mofgw-odoo)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Media

## Contexto

Odoo 19 enterprise usa el Responses API de OpenAI para hablar con su proveedor de IA. La documentación oficial del Responses API (spec OpenAI) es amplia y describe un protocolo "canónico" (items `web_search_call`, `function_call_output`, `url_citation`, etc.). Durante el epic 011 se detectó que **lo que Odoo realmente habla/lee difiere de la spec canónica**: `_request_llm_openai_helper` (llm_api_service.py:364-397) solo lee `output[].content[].text`, no maneja `web_search_call`/`url_citation`, y los `arguments` de tool calls deben ser string JSON.

## Decisiones que esto forzó (transversal del epic)

1. **Forma del `output[]` (011-001):** mofgw produce `{"type":"message","content":[{"type":"output_text","text":...}]}` — el shape exacto que `_request_llm_openai_helper` parsea, no el Responses canónico.
2. **Solo-texto de web search (011-005):** como Odoo no maneja `web_search_call`, mofgw emula el grounded search server-side e inyecta los resultados como contexto; no emite items `web_search_call`/`url_citation` (que Odoo ignoraría).
3. **Tool call arguments (011-003):** los `arguments` de `function_call` deben ser string JSON parseable por `json.loads` de Odoo.
4. **Los tests se validaron contra el código real de Odoo** (`llm_api_service.py`), no contra la doc del Responses API.

## Decisión

**El contrato de la integración es lo que Odoo realmente lee/parsea (`llm_api_service.py`), no la spec canónica del Responses API de OpenAI.** mofgw produce el dialecto que Odoo entiende, verificado contra el código real del cliente, no contra la documentación del proveedor.

## Razones

1. **El código real es la fuente de verdad** del contrato del cliente: diseñar contra la spec OpenAI habría producido un Responses "canónico" que Odoo no habría parseado correctamente.
2. **Evita features inútiles:** `web_search_call`/`url_citation`/`structured_output` canónicos son features que Odoo no consume → no se implementan (se documenta el gap).
3. **La verificación E2E fue limpia:** la primera llamada E2E devolvió 200 sin ajustes de contrato, porque el contrato se alineó al parser real desde el inicio.

## Consecuencias

- mofgw habla "el dialecto que Odoo entiende", que puede divergir del Responses API canónico de OpenAI.
- Los tests de contrato se escriben contra `llm_api_service.py` (código real de Odoo), no contra la doc del Responses API.
- Si Odoo cambia su parser en una versión futura, hay que revalidar el contrato (mismo riesgo que ADR-007 con el core enterprise).
- Este es un patrón de integración: el código real del sistema cliente es la fuente de verdad del contrato, no la documentación del proveedor.
