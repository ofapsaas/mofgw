# mofgw — MOF Gateway

**Status:** ✅ MVP implementado (EPIC-001) + EPIC-002 (resiliencia) + EPIC-004 (escala) + EPIC-005 (ops) — desplegable como systemd user service con varios providers OpenAI-compatible. Suite de 83+ tests verde con `-race`.
**Go module:** `github.com/ofapsaas/mofgw`
**Puerto:** 3369 (default)
**Guía de usuario:** [`docs/USER-GUIDE.md`](docs/USER-GUIDE.md) — instalación, configuración, endpoints y uso práctico

## ¿Qué es?

Proxy de IA self-hosted, minimalista y en Go que expone **un endpoint OpenAI-compatible único** y hace **fallback transparente entre providers** (429, 5xx, timeout, 400 max_tokens). Cualquier cliente (OpenClaw, crons, sesiones aisladas) apunta a un solo endpoint; el proxy rota internamente entre los providers configurados, sin que el cliente se entere.

**Principio rector:** el cliente nunca se entera. El proxy salva todas las situaciones para que el agente cliente nunca sepa que un provider no respondió como debía.

## ¿Por qué?

- **omniroute** (Node, ~1GB RAM, 55s de arranque) no anda bien y es pesadísimo.
- **Fallback nativo de OpenClaw** no sirve para crons/hb: las sesiones aisladas mueren en la model-call antes de loguear.
- **Salida progresiva de Node** — kernel-ia será el SO de agentes; mofgw es un ladrillo de esa arquitectura.
- **Ligero, controlado, mantenible** — solo features con valor real.

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
| Eficiencia | Cache LLM + buenas prácticas de eficiencia (a investigar) |

## Epics

Ver [`docs/epics/plan.md`](docs/epics/plan.md) — 5 epics definidos con cdad-epic:

1. **EPIC-001 mofgw-core** — MVP: endpoint único, fallback en cadena **circular** (`max_retries` = tope de intentos totales, cooldown entre requests), clamp max_tokens, streaming, timeouts, config → ✅ **implementado**
2. **EPIC-002 mofgw-resiliencia** — Transparencia total: retry/backoff, health checks, absorción de errores, degradación controlada → ✅ **implementado**
3. **EPIC-003 mofgw-eficiencia** — Cache LLM + buenas prácticas (pendiente de investigación) → ⏳ pendiente
4. **EPIC-004 mofgw-escala** — Alta carga multi-agente: límites de concurrencia globales y aislamiento por cliente/agente → ✅ **implementado**
5. **EPIC-005 mofgw-ops** — Metrics, logging, systemd, config paths → ✅ **implementado**

## Estrategia

1. ✅ Investigación (competidores, features, arquitectura)
2. ✅ Diseño (SPEC.md)
3. 🛠️ Implementación feature por feature (cdad-cycle) — EPIC-001/002/004/005 completos, EPIC-003 pendiente

## Contexto

- Motivación: reemplazar un gateway previo en Node (pesado, ~1GB RAM) por un binario Go ligero
- Integración: los agentes cliente apuntan a un solo endpoint (`mofgw`), que rutea a los providers upstream en orden de config