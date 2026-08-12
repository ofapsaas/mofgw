# ADR-004: La traducción Responses↔ChatCompletions vive en la capa del endpoint, no en el router ni en los providers

- **Status**: Accepted (spec 011-001-responses-endpoint I1 la codifica; contrato cross-feature plan-011:42)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Alta (contrato cross-feature explícito del plan-011 y verificada empíricamente: el diff de 011-001 no toca `ParseChatRequest`, router ni providers — 0 tests existentes modificados, suite 413 `-race` verde)

## Contexto

Odoo 19 enterprise habla con su proveedor de IA vía el Responses API (`POST /v1/responses`, body con `input`), mientras los providers upstream de mofgw hablan chat-completions (`messages`). mofgw debe traducir en ambos sentidos (Responses→ChatCompletions al salir, chat→`output[]` al volver). El body Responses viaja **crudo** por la cadena (pattern mofgw de transparencia: no se pierden campos).

La pregunta: ¿dónde vive esa traducción? Las opciones son ponerla en el router/providers (capas compartidas por todos los endpoints) o en la capa del endpoint `/responses` (handler propio).

## Opciones consideradas

### Opción A: Traducción en la capa del endpoint `/responses` (ELEGIDA)
- Pros: el router y los providers quedan intactos y hablando UN solo dialecto (chat-completions); `ParseChatRequest`, ruteo, fallback, cooldown y cache no se bifurcan por endpoint; la separación es extensible (002-005 solo agrandan `responses.go` sin tocar plumbing); el riesgo de regresión en la suite existente es mínimo (0 tests existentes modificados).
- Contras: duplica lógica de handler a nivel de endpoint (patrón paralelo a `handleChat`); el endpoint responde por el contrato de Odoo, no por el contrato interno de mofgw.

### Opción B: Traducción en el router/providers (capas compartidas)
- Pros: un solo punto de traducción si mañana hubiera varios consumidores del dialecto Responses.
- Contras: contamina el corazón del proxy (ruteo/fallback/cooldown/cache) con un dialecto de un solo consumidor (Odoo); rompe I1 y el principio de transparencia; riesgos de regresión altos en la suite existente; el router debería conocer `input` y `messages` y bifurcar su lógica interna (AP de acoplamiento).

### Opción C: Dos dialectos nativos en providers
- Pros: cada provider habla lo que el cliente pidió.
- Contras: duplica la superficie de contratos upstream (cada provider tendría que implementar Responses); el resto de la cadena (cache, limiter, telemetría, clamp, usage) operaría sobre dos formatos de body distintos — complejidad sin beneficio real (el único consumidor Responses es Odoo).

## Decisión

**La traducción Responses↔ChatCompletions vive en la capa del endpoint `/responses`** (`internal/proxy/responses.go`). `handleResponses` traduce `input`→`messages`, delega en la MISMA pipeline de chat (`router.Complete` con body chat-completions) y traduce la respuesta a `output[]`. El router y los providers no conocen el dialecto Responses: hablan chat-completions intactos (I1).

## Razones

1. **Aislamiento del core:** el router/providers son el activo más riesgoso de tocar (ruteo, fallback, cooldown, cache, accounting). Mantenerlos en UN dialecto minimiza regresiones (test-audit: 0 tests existentes modificados) y preserva I1.
2. **Extensibilidad del epic:** las features 002-005 (structured output, tool calling, file attachments, web search) extienden SOLO `responses.go` — reemplazan los rechazos P6-P9 por traducción real sin tocar plumbing. La frontera del ADR es el punto de crecimiento.
3. **Coherencia con el patrón existente:** el body viaja crudo y la transformación es responsabilidad del borde (endpoint), igual que la reescritura de modelo a nivel raíz.
4. **Riesgo acotado y verificable:** la feature 011-001 la implementa y la suite completa 413 tests `-race` la confirma; el reviewer (qwen3.7-plus, familia distinta) verificó I1 sin hallazgos.

## Consecuencias

- Spec 011-001 I1: `ParseChatRequest` y el ruteo de providers no cambian; todo cambio Responses vive en `responses.go` (testeable).
- Features 011-002/003/004/005 reemplazan los rechazos P8/P7/P9/P6 manteniendo la traducción en la capa del endpoint.
- Streaming SSE del Responses API (deuda del epic) seguirá el mismo patrón: traducción en el endpoint cuando se implemente.
- Nuevos consumidores de un dialecto distinto a chat-completions deben seguir el mismo patrón (traducción en su capa de endpoint), no bifurcar el router.
