# ADR-007: Registro de un provider de IA en Odoo vía monkeypatch in-place (PROVIDERS.append + parches de LLMApiService)

- **Status**: Accepted (feature 011-008-odoo-provider, última del epic 011-mofgw-odoo)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Media

## Contexto

El epic 011 busca que Odoo 19 use mofgw como provider de IA (reemplazo total de OpenAI). El core `enterprise/ai` instancia la clase `LLMApiService` **por nombre** y comparte el objeto `PROVIDERS` (lista módulo-global) **por referencia**; `get_provider`, `get_provider_for_embedding_model` y `ai.agent._get_llm_model_selection` lo leen por referencia. Odoo no expone un mecanismo oficial para registrar un provider nuevo desde un módulo local: **no se puede subclasear** `LLMApiService` (el core instancia la clase base por nombre), y editar el core `enterprise/ai` está prohibido por el invariant I1 de la feature.

## Opciones consideradas

### Opción A: Subclasear `LLMApiService` (DESCARTADA)
Pros: tipado, sin tocar el core. Contras: el core instancia `LLMApiService` por nombre (`LLMApiService(env, provider=...)`) → una subclase nunca se instancia; el registro del provider no surte efecto. Estructuralmente inviable.

### Opción B: Editar el core `enterprise/ai` (DESCARTADA)
Pros: mecanismo "nativo". Contras: viola I1 (core intacto), rompe upgrades de Odoo y el versionado del enterprise. Inaceptable.

### Opción C: Monkeypatch in-place desde el módulo local `mofgw_ai` — ELEGIDA
`PROVIDERS.append(Provider('mofgw', ...))` (visión inmediata por referencia) + parchear in-place los métodos de `LLMApiService`: `__init__`, `_get_api_token`, `_request_llm`, `_build_tool_call_response`. Cada parche es "guard + delegate": solo agrega la rama `'mofgw'` y delega al método original para `openai`/`google` (sin regresión, I2). Doble guard de idempotencia (`if not any(p.name=='mofgw')` + flag `_mofgw_patched` en la clase).

## Decisión

Registrar un provider de IA de Odoo desde un **módulo local** mediante **monkeypatch in-place**:
- `PROVIDERS.append(...)` en el import del módulo (corre en install/upgrade).
- Parchear los 4 métodos de la clase base `LLMApiService` con patrón **guard + delegate al original**.
- **Doble guard de idempotencia** para soportar upgrade/reinstall sin duplicar ni encadenar parches.
- **Nunca** editar el core `enterprise/ai`.

## Razones

1. **El core instancia por nombre y comparte por referencia** → append + parche in-place son los únicos puntos de extensión que el core respeta sin editarse.
2. **Sin regresión garantizada por diseño**: cada parche delega al original para openai/google (I2), verificado por tests B3-B7.
3. **Idempotencia explícita** ante recarga del módulo (riesgo detectado en el Test Audit, mitigado en GREEN).
4. **Mismo módulo que ADR-006** (`mofgw_ai`), patrón reusable para futuras extensiones de provider de Odoo.

## Consecuencias

- **Acoplamiento a estructura interna del core enterprise** (atributo de clase, lista por referencia): si una futura versión de Odoo cambia `LLMApiService`/`PROVIDERS` (firma, mecanismo de instanciación, snapshot en lugar de referencia), los parches pueden romperse o quedar sin efecto → **debe re-evaluarse en cada upgrade mayor de Odoo**.
- `EMBEDDING_MODELS_SELECTION` es un snapshot separado → el override del Selection `ai.embedding.embedding_model` es obligatorio y se hace con `selection_add` + `ondelete='cascade'` (no lo cubre este ADR; es decisión de la misma feature, D3).
- El path `ir_actions_server.AI_PROVIDER` (server actions) NO se cubre con este registro → deuda documentada (D6) del epic.
- Conocimiento de Odoo 19 codificado: límites de extensión del core de IA (instanciación por nombre, PROVIDERS por referencia) — no redescubrir por prueba y error.
