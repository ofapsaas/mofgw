<!-- SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# mofgw — MOF Gateway

**Status:** ✅ Programa completo implementado y desplegado — 10 epics (001-010) + hardening de seguridad (SEC-001). Suite **17 paquetes, 387 tests verde con `-race`**, vet + gofmt limpios.
**Go module:** `github.com/ofapsaas/mofgw`
**Puerto:** 3369 (default)
**Guía de usuario:** [`docs/USER-GUIDE.md`](docs/USER-GUIDE.md) — instalación, configuración, endpoints y uso práctico
**Deuda técnica:** [`docs/TECHDEBT.md`](docs/TECHDEBT.md)

## ¿Qué es?

Proxy de IA self-hosted, minimalista y en Go que expone **un endpoint OpenAI-compatible único** y hace **fallback transparente entre providers** (429, 5xx, timeout, 400 max_tokens). Cualquier cliente (OpenClaw, crons, sesiones aisladas) apunta a un solo endpoint; el proxy rota internamente entre los providers configurados, sin que el cliente se entere.

**Principio rector:** el cliente nunca se entera. El proxy salva todas las situaciones para que el agente cliente nunca sepa que un provider no respondió como debía.

## ¿Por qué?

- **omniroute** (Node, ~1GB RAM, 55s de arranque) no anda bien y es pesadísimo.
- **Fallback nativo de OpenClaw** no sirve para crons/hb: las sesiones aisladas mueren en la model-call antes de loguear.
- **Salida progresiva de Node** — kernel-ia será el SO de agentes; mofgw es un ladrillo de esa arquitectura.
- **Ligero, controlado, mantenible** — solo features con valor real (~10-12MB RAM).

## Decisiones fijadas (Pablo, 03 Ago 2026)

| Decisión | Valor |
|----------|-------|
| Nombre | mofgw (MOF Gateway) |
| Transparencia | Total — el cliente nunca sabe que un provider falló |
| Deploy | systemd user, básico, mínimo |
| Puerto | **3369** — estadísticamente el menos usado del rango 1025-4000 (sin asignación IANA, no está en /etc/services, nada escuchando; centro del hueco libre 3367-3371) |
| Config | `~/.config/mofgw/` (nivel home) o `/etc/mofgw/` (nivel sistema) |
| Orden post-MVP | Metrics primero |
| Carga | Alta carga de agentes clientes, cada uno con sus asuntos |
| Eficiencia | Cache LLM + medición + catálogo + contexto (implementado 06 Ago) |

## Epics

Ver [`docs/epics/plan.md`](docs/epics/plan.md) — 10 epics (5 originales + replanteo de eficiencia + contexto-análisis + catálogo-fiel):

1. **EPIC-001 mofgw-core** — MVP: endpoint único, fallback en cadena **circular** (`max_retries` = tope de intentos totales, cooldown entre requests), clamp max_tokens, streaming, timeouts, config → ✅ **implementado**
2. **EPIC-002 mofgw-resiliencia** — Transparencia total: retry/backoff, health checks, absorción de errores, degradación controlada → ✅ **implementado**
3. **EPIC-003 mofgw-eficiencia** — Cache de providers: instrumentación de cache hit/miss (los providers ya cachean por prefijo automáticamente; mofgw lo expone) → ✅ **implementado**
4. **EPIC-004 mofgw-escala** — Alta carga multi-agente: límites de concurrencia globales y aislamiento por cliente/agente → ✅ **implementado**
5. **EPIC-005 mofgw-ops** — Metrics, logging, systemd, config paths → ✅ **implementado**
6. **EPIC-006 mofgw-observabilidad** — Medición de tokens (prompt/completion/cache por cliente) + costos USD con tabla de precios configurable → ✅ **implementado**
7. **EPIC-007 mofgw-catalogo** — Capabilities de modelos para clientes: `/v1/models` enriquecido (context window, max output, thinking levels, pricing) + headers `X-Usage-*` + endpoint `/v1/usage` → ✅ **implementado**
8. **EPIC-008 mofgw-contexto** — Rechazo temprano por ventana de contexto, budget por cliente, stats por sesión → ✅ **implementado**
9. **EPIC-009 mofgw-contexto-analisis** — Telemetría de descubrimiento, composición de contexto `/v1/context`, sticky routing por sesión → ✅ **implementado**
10. **EPIC-010 mofgw-catalogo-fiel** — Catálogo `/v1/models` completo/veraz/prescriptivo (`supported_parameters`, `modality`/`architecture`, `top_provider`, `max_output_tokens`, `thinking_default` prescriptivo) + request path que respeta el `max_output` por modelo (clamp) e inyecta el `thinking_default` per-attempt (knob `providers[].thinking_path`) → ✅ **implementado + deployado**

Más **SEC-001-security-hardening** (auditoría externa): SSE saneado, X-Agent-Id acotado, ReadHeaderTimeout, permisos de log → ✅ **implementado**

## Componentes opcionales

- **`odoo/mofgw_ai/`** — módulo Odoo 19 enterprise que hace de mofgw el único
  proveedor de IA de una instancia Odoo (reemplazo total de OpenAI): alinea el
  vector de embeddings a 384-dim y registra mofgw como provider nativo. Para
  quien use Odoo 19 con mofgw como proxy de IA: copiar el addon al `addons_path`
  de la instancia e instalar con `odoo-bin -i mofgw_ai`. Ver
  [`odoo/mofgw_ai/README.md`](odoo/mofgw_ai/README.md). (Epic 011/012.)

## Estrategia

1. ✅ Investigación (competidores, features, arquitectura, precios, capacidades)
2. ✅ Diseño (SPEC.md + research-token-efficiency.md)
3. ✅ Implementación feature por feature (cdad-cycle, 9 features + hardening, cada una spec→RED→GREEN→review)
4. ✅ Deploy a producción como systemd user service

## Capacidades clave

- **Fallback transparente** en cadena circular con cooldown, retry con backoff, health checks y degradación controlada.
- **Medición real:** contadores de tokens y costo estimado por cliente/provider/modelo en `/metrics`, expuestos al cliente vía headers `X-Usage-*` y endpoint `GET /v1/usage` (con stats por sesión via `X-Session-Id`).
- **Catálogo de modelos enriquecido y fiel:** `/v1/models` devuelve `context_length`, `max_completion_tokens`, `max_output_tokens`, `capabilities.reasoning`, `thinking.levels/default` (prescriptivo), `supported_parameters` (tools/reasoning), `modality`/`architecture` (visión) y `pricing` — para que los runtimes (opencode, openclaw) elijan modelo y optimicen reasoning effort. **Fallback rule:** capability no verificada se OMITE del catálogo (nunca se adivina; ausente ≠ 0/[]/false).
- **Request path fiel:** clamp de `max_tokens` al `max_output` real del modelo (elimina el 400→502 en modelos de salida acotada), window-check fiel (usa el `max_tokens` efectivo post-clamp), e **inyección del `thinking_default` prescriptivo** por-attempt y provider-aware vía knob `providers[].thinking_path` — providers con passthrough de `reasoning_effort` vs providers que requieren `enable_thinking`; modelos de thinking siempre-activo o con default nativo == prescriptivo nunca se inyectan.
- **Cache de providers visible:** `mofgw_cache_hit_tokens_total` en `/metrics` (el cache de prompt es automático del lado del provider; mofgw lo instrumenta y expone — hit rates típicos 94-99%).
- **Control de contexto:** rechazo temprano de prompts que exceden la ventana (ahorra intentos de la cadena), budget por cliente (429 al exceder), sesiones correlacionadas.
- **Seguridad:** auth Bearer con hash SHA-256 + comparación constant-time, errores upstream absorbidos (nunca filtran detalles internos al cliente), keys fuera del YAML.

## Contexto

- Motivación: reemplazar un gateway previo en Node (pesado, ~1GB RAM) por un binario Go ligero
- Integración: los agentes cliente apuntan a un solo endpoint (`mofgw`), que rutea a los providers upstream en orden de config

## Licencia

GPL-3.0-or-later. Ver [`LICENSE`](LICENSE). Copyright © 2026 Pablo Manuel Rizzo.
