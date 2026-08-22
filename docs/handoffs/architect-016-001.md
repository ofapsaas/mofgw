# Handoff packet — rol: architect (etapa 1+2) para feature 016-001-config-renderer-core

Pegá este packet en un chat NUEVO (opencode o el LLM que use rol architect, modelo deepseek-v4-pro). Trabajo en `/home/ofap/clawd/projects/mofgw` (Go, gateway modelo IA OpenAI-compatible).

## Rol y constraints (strict)

Sos el **architect** del ciclo CDAD. Etapas 1 (Descubrimiento) y 2 (Especificación). **READ-ONLY**: leés todo, escribís nada. NO escribís el archivo spec por vos — tu output es el draft de spec como mensaje final de texto. NO aprobás el spec (lo aprueba el operador). NO implementás/testeás/revisás código. NO corrés go test.

## Contexto — EPIC 016 (mofgw-client-config), plan aprobado

Leé completo: `/home/ofap/clawd/projects/mofgw/docs/epics/016-mofgw-client-config/plan.md`.

El epic agrega un endpoint que devuelve el fragmento de configuración del provider mofgw listo para insertar por cliente (opencode.json, openclaw.conf, zot). Decisiones confirmadas:
- **Catálogo completo** por modelo (no formato mínimo) — reusa la metadata que ya emite `modelCatalogEntry`.
- **base_url configurable** vía knob `client_config.base_url`.
- **Key SOLO como referencia a env var**, nunca el secret literal.
- Sin mitigación de test-fixture por template (excluida explícitamente).

## Feature 016-001 = core únicamente

- Paquete nuevo `internal/clientconfig`.
- IR tipado: `ClientID`, `BaseURL`, `KeyEnvRef`, `Models []ModelEntry` (ModelEntry lleva la metadata del catálogo).
- Registry de renderers + interfaz `Renderer.Render(ir ConfigIR) ([]byte, error)`; cliente desconocido → 404 con lista de clientes soportados.
- Endpoint detrás de la misma auth de /v1 (clientID autenticado, 401).
- Knob config `client_config: {base_url, key_env}`, aditivo; base_url vacío → 503.
- La key nunca viaja en la respuesta, solo env-ref.

## Código a leer (overview)

- `internal/proxy/proxy.go` — `handleModels` (~1167) + `modelCatalogEntry` (~1198): fuente de verdad de metadata de modelos a reusar.
- `config.example.yaml` — convenciones de knob (el bloque `registry:` es buen template de knob aditivo enable/off).
- `internal/auth/` — cómo funciona la auth de /v1 (clientID autenticado, auth.Wrap).
- `cmd/mofgw/main.go` — patrón de wiring config → server → handlers (setter pre-tráfico).
- `docs/specs/010-001-catalogo-fiel/spec.md` y `docs/specs/007-002-models-endpoint/spec.md` — estilo de spec casa y precedentes del catálogo/endpoint de models.

## Mapeo técnico ya derivado (podés confiar, verificarlo con los reads)

- `internal/config/config.go`: `Config` gana `ClientConfigConfig{base_url, key_env}` (knob aditivo, yaml tags, defaults, validación). base_url vacío → 503 en runtime, NO error de carga fail-fast.
- `internal/proxy/proxy.go`: `Server` gana campo + setter pre-tráfico (patrón `SetContextAnalysis`); `Handler()` (~361-373) gana ruta `GET /v1/client-config` → `handleClientConfig`; reusar `modelCatalogEntry` para armar el IR Models (sin duplicación); usar `handleModels` (~1167) como patrón de iteración/dedupe de modelos; errores vía `openAIError` (404 `not_found_error`, 503 `server_error`) en envelope OpenAI-compatible.
- `internal/auth/`: `auth.Wrap` cubre /v1 automáticamente (la ruta nueva queda autenticada por estar bajo /v1/ y no en publicPrefixes).
- `cmd/mofgw/main.go`: después del setter de metadata existente, `srv.SetClientConfig(cfg.ClientConfig.BaseURL, cfg.ClientConfig.KeyEnv)`.
- NUEVO: `internal/clientconfig` — ConfigIR, ModelEntry, Renderer, registry.

**Nota clave sobre tests:** en esta feature el registry está VACÍO (los adapters opencode/openclaw/zot son features 016-002/003/004, fuera de alcance) → cualquier client da 404 con lista vacía. Para testear el path 200 del core, el test registra un renderer de prueba en el registry público — el registry ES el contrato público del paquete (`map[string]Renderer`/`Register`), así que eso NO es mock sobre plumbing.

## Output (mensaje final de texto)

Draft de spec completo en markdown para la feature 016-001, con:
1. **Descripción** — qué entrega y por qué.
2. **Contrato** — tipos Go del IR + interfaz Renderer + contrato del endpoint (path, query params, estados 200/401/404/503, envelopes de error OpenAI-compatible).
3. **Invariantes** — no negociables (key nunca literal; catálogo como fuente de verdad viva, no hardcodeado; aditivo a /v1/models sin regresión; base_url vacío → 503, no fallback silencioso).
4. **Criterios** — postcondiciones numeradas P1..PN testeables, cada una mapeando a comportamiento observable (contrato observable, sin mocks sobre plumbing, sin targets de coverage).
5. **Mapeo técnico** — archivos/hooks/funciones a tocar (marcando read-only vs nuevo).
6. Terminar con `Status: Pending approval`.

NO inventes adapters de cliente (opencode/openclaw/zot son 016-002/003/004, fuera de alcance). Solo el core + registry.