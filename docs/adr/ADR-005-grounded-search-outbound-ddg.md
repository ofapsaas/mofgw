# ADR-005: mofgw emula el grounded search server-side con una llamada saliente best-effort a DuckDuckGo

- **Status**: Accepted (feature 011-005-web-search D1/D3, spec 011-005)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Media (parte reutiliza ADR-004; lo nuevo es la primera llamada saliente del proxy a un servicio no-provider)

## Contexto

Odoo 19 manda `{"type":"web_search_preview"}` en `tools` del Responses API y NO maneja el ciclo `web_search_call` (0 matches en `llm_api_service.py`; solo lee `text` del item message). El contrato observable para Odoo es un item `message`. mofgw debe resolver el grounding: o expone un ciclo de web search (que Odoo no consume) o lo emula server-side haciendo la búsqueda por su cuenta.

## Opciones consideradas

### Opción A: mofgw emula grounded search server-side con DDG (ELEGIDA)
Pros: cumple el contrato observable de Odoo (item message); sin ciclo no consumido; extensible al patrón de ADR-004 (todo en `responses.go`). Contras: **primera llamada saliente del proxy a un servicio externo fuera de los providers configurados**; dependencia best-effort (disponibilidad/rate limits de DDG).

### Opción B: exponer el ciclo `web_search_call`
Pros: delegar la búsqueda al modelo/proveedor. Contras: Odoo no lo maneja (conclusión verificada) → el contrato observable no se cumple; complejidad sin beneficio real.

### Opción C: rechazar web_search_preview (status quo D3 de 003)
Pros: sin dependencias nuevas. Contras: la feature web search de Odoo sigue muerta; no resuelve el caso real.

## Decisión

mofgw emula el grounded search server-side: detectar `web_search_preview` → derivar la query (último mensaje user) → buscar en DDG (cliente propio `internal/websearch`, sin dependencias de terceros) → inyectar resultados como system message prependido → remover la tool del upstream → item message normal. Gating `web_search.enabled:false` default (opt-in). Fallo de DDG = best-effort sin grounding (nunca un 4xx/5xx al cliente).

## Razones

1. **El contrato observable de Odoo es un item message** (verificado contra `llm_api_service.py`); el ciclo `web_search_call` no lo consume nadie.
2. **Cumple ADR-004:** la traducción/emulación vive en la capa del endpoint (`responses.go`); router y providers intactos (I1).
3. **Sin errores nuevos (I2):** fallo de DDG degrada a ungrounded, nunca a 4xx/5xx; consistente con el espíritu de D5 de 002.
4. **Best-effort acotado:** query solo como query parameter, URLs DDG fijas (sin SSRF), body limitado a 4MB, timeout configurable.

## Consecuencias

- mofgw hace su **primera llamada saliente** a un servicio externo no-provider (DDG) en el path del request `/responses`; features futuras que agreguen outbound (006 embeddings vía Ollama, 008 odoo-provider) deben evaluar el mismo patrón best-effort.
- `web_search.enabled` queda `false` por default (opt-in); habilitarla es decisión operativa de config.
- El response cache **excluye** los requests web search activos (P8): nunca resultados obsoletos.
- El contrato `websearch.Client` desacopla el parser del handler: permite cambiar/agregar proveedor de búsqueda sin tocar `responses.go` (extensión futura, e.g. Brave/SerpAPI, hoy out of scope D3).
