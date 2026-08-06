<!-- SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Plan de Epics — mofgw (MOF Gateway)

---
epic_program: mofgw
created_at: 2026-08-03
approved_by: Pablo (2026-08-04, sesión Telegram)
approved_at: 2026-08-04
---

# Programa mofgw: Proxy de IA con fallback transparente

## Resumen

Proxy self-hosted en Go que expone **un endpoint OpenAI-compatible único** y hace **fallback transparente entre providers** (cualquier proveedor OpenAI-compatible, configurable). El agente cliente (OpenClaw, crons, sesiones aisladas) **nunca se entera** de que un provider falló. Reemplaza a un gateway previo en Node (~1GB RAM, poco confiable) y es un ladrillo de la arquitectura de agentes autónomos.

**Principio rector (decisión Pablo):** el proxy salva todas las situaciones. El cliente nunca sabe que un provider no respondió como debía.

## Decisiones fijadas (Pablo, 03 Ago 2026)

| Decisión | Valor |
|----------|-------|
| Nombre | **mofgw** (MOF Gateway) |
| Transparencia | Total — el cliente nunca se entera de fallos de provider |
| Deploy | systemd user, básico, mínimo |
| Puerto | **3369** (estadísticamente el menos usado en 1025-4000: sin asignación IANA, no está en /etc/services, nada escuchando — centro del gap libre 3367-3371) |
| Config | `~/.config/mofgw/` (nivel home) o `/etc/mofgw/` (nivel sistema) |
| Orden post-MVP | Metrics primero |
| Carga | Debe soportar alta carga de agentes clientes, cada uno con sus asuntos |
| Eficiencia | Cache LLM + buenas prácticas de eficiencia (a investigar) |
| Agentes por usuario (04 Ago) | **Todos los agentes clientes privados apuntan a mofgw** — un mofgw por servidor. Keys solo en mofgw, nunca en los clientes. Deploy: systemd user por server. |
| Auth de clientes (04 Ago) | **In-scope del MVP** — API key por cliente (Bearer token), 401 `invalid_api_key` si falta/inválida. `/healthz` y `/metrics` sin auth (loopback). Keys hasheadas en disco. Multi-tenancy completo (tenants/roles) sigue fuera. |

## Status

**✅ APROBADO por Pablo el 2026-08-04** (sesión Telegram) — con auth in-scope del MVP (feature 001-007-auth).

**✅ EPIC-001 (core), EPIC-002 (resiliencia), EPIC-004 (escala), EPIC-005 (ops): DONE** — verificado 06 Ago 2026 (README + suite 83+ tests `-race` verde + servicios systemd desplegados).

**🔄 EPIC-003 (eficiencia): REPLANTEADO 06 Ago 2026.** El scope original ("cache LLM + buenas prácticas, pendiente de investigación") se expande en 4 epics con frentes independientes: EPIC-003 (cache de providers), EPIC-006 (medición tokens/costos), EPIC-007 (catálogo de capabilities), EPIC-008 (optimización de contexto). Decisión de Pablo 06 Ago: delegación total a Ofap (auto-aprobación GVR). Discovery en curso (4 investigaciones externas delegadas).

**Status: Approved by Ofap (auto-aprobación GVR, delegación Pablo 06 Ago 2026 — async no-blocking) on 2026-08-06.** Discovery del replanteo: 4 investigaciones externas completadas con fuentes (precios, cache providers, thinking capabilities) + verificación empírica opencode 1.18.4 (`reasoning:false`, `context:0` para todos los modelos mofgw) → `docs/research-token-efficiency.md`.

**✅ REPLANTEO COMPLETO + DEPLOYADO (06 Ago):** EPIC-003 (cache), EPIC-006 (medición), EPIC-007 (catálogo), EPIC-008 (contexto) — 9/9 features done (spec→audit→RED→GREEN→review cada una), suite 13 paquetes `-race` verde, deploy a producción verificado E2E (medición viva: cache_hit 96.7%). Pendientes: validación externa anti-bias (runtime de delegación estuvo caído toda la sesión) + cablear pricing/metadata reales en config de producción.

## Epics del programa

### EPIC-001: mofgw-core — MVP del proxy transparente
**Resumen:** El núcleo funcional: endpoint único, fallback en cadena, cooldown, clamp de max_tokens, streaming, timeouts, config file. Es el MVP que reemplaza a omniroute.

**In scope:** servidor HTTP OpenAI-compatible (`/v1/chat/completions`, `/v1/models`), fallback automático en cadena, cooldown por provider en memoria, clamp de max_tokens por provider, streaming SSE, timeouts configurables, config file YAML/TOML.
**Out of scope:** metrics, cache, multi-agente avanzado, load balancing, dashboard, persistencia, multi-tenancy completo (tenants/roles).

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 001-001-endpoint | Servidor HTTP OpenAI-compatible único | — | Sí |
| 2 | 001-002-config | Config file (providers, orden fallback, cooldowns, timeouts) | — | Sí |
| 3 | 001-003-fallback | Fallback en cadena con cooldown por provider | 001, 002 | No |
| 4 | 001-004-clamp | Clamp de max_tokens por provider | 002 | Sí |
| 5 | 001-005-streaming | Streaming SSE passthrough | 001, 003 | No |
| 6 | 001-006-timeouts | Timeout configurable por intento | 002 | Sí |
| 7 | 001-007-auth | API key por cliente (Bearer), 401, hash en disco | 001 | Sí |

**Contratos cross-feature:**
```go
// Provider: interfaz compartida por 003, 004, 005, 006
type Provider interface {
    ID() string
    Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error)
    MaxTokens() int
}
// Config: struct raíz, usada por 002-006
type Config struct {
    Providers []ProviderConfig `yaml:"providers"`
    Fallback  FallbackConfig   `yaml:"fallback"`
    Server    ServerConfig     `yaml:"server"`
}
```
Usado por: 001-003, 001-004, 001-005, 001-006.

**Criterios de aceptación del epic:**
- [ ] Las 6 features están done individualmente.
- [ ] Test E2E: request a `/v1/chat/completions` con provider A caído (429) → responde vía provider B. Cliente no ve error.
- [ ] Test E2E streaming: stream SSE se completa sin romperse con fallback.
- [ ] Test E2E clamp: request con max_tokens=131072 contra qwen → llega clampado a 65536, sin 400.
- [ ] Arranca como systemd user service en puerto 3369.

**Riesgos:** streaming fallback es lo más delicado (si el stream se corta a mitad, ¿qué hace el cliente?). Mitigación: investigar semántica (buffer hasta primer token vs fail) — ver research-architecture.md del subagente.

---

### EPIC-002: mofgw-resiliencia — Transparencia total
**Resumen:** El proxy salva TODAS las situaciones: retry con backoff, health checks, absorción de errores. El cliente nunca ve un error de provider.

**In scope:** retry con backoff exponencial, health checks periódicos de providers, absorción de errores (mapping a respuestas coherentes), cooldown inteligente (429 largo, timeout corto), estado "todos caídos" con degradación controlada.
**Out of scope:** cache, dashboard, persistencia.

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 002-001-retry | Retry con backoff exponencial por intento | 001-003 | No |
| 2 | 002-002-health | Health checks periódicos + endpoint /health | 001-003 | Sí |
| 3 | 002-003-absorcion | Absorción de errores: el cliente nunca ve fallo de provider | 002-001 | No | ✅ spec 04 Ago |
| 4 | 002-004-degradacion | Degradación controlada si todos los providers caen | 002-003 | No | ✅ spec 04 Ago |

**Criterios de aceptación:**
- [ ] Las 4 features done individualmente.
- [ ] E2E: los 5 providers caídos → el proxy responde error coherente (no crashea, no cuelga).
- [ ] E2E: provider con 429 → cooldown, no se reintenta hasta expirar.
- [ ] E2E: timeout de 859s no ocurre nunca (timeout máximo por intento).

---

### EPIC-003: mofgw-eficiencia — Cache de providers
**Resumen:** Habilitar y aprovechar el cache de prompt/contexto de los providers (cache KV automático por prefijo). El objetivo NO es construir cache propio sino: (a) garantizar que mofgw no rompe la cacheabilidad (ya verificado: clamp preserva el body byte a byte, no inyecta contenido dinámico), (b) verificar qué providers de la cadena reportan cache hits y en qué campo, (c) instrumentar hit/miss para saber cuánto ahorramos.

**In scope:** verificación de soporte de cache por provider (automático vs explícito tipo cache_control), forward correcto del request (no romper prefijo estable), reporte de cache hit/miss en usage/logs/metrics. **Out of scope:** semantic cache propio en mofgw, cache de respuestas.

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 003-001-provider-cache | Verificar soporte + instrumentar cache hit/miss por provider/modelo | investigación | Sí |

---

### EPIC-006: mofgw-observabilidad — Medición de tokens y costos
**Resumen:** Contabilizar no solo cantidad sino también costo de tokens por proveedor y modelo. Sin medición no hay optimización: esta es la base de los demás epics.

**In scope:** usage accounting (tokens por request desde el usage del provider, agregados por cliente/modelo/provider), tablas de precios por modelo (config YAML), cálculo de costo estimado por request y por período, exposición en /metrics. **Out of scope:** alerting, dashboard.

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 006-001-usage-accounting | Acumular tokens (prompt/completion/cache) por request, agregados por cliente/modelo/provider | investigación | No |
| 2 | 006-002-costos | Tabla de precios configurable + costo estimado por request/día | 006-001 | Sí |

---

### EPIC-007: mofgw-catalogo — Capabilities de modelos para clientes
**Resumen:** Enriquecer la info que los runtimes (opencode, openclaw) obtienen de mofgw: capacidades de thinking (low/high/max), tamaño de contexto por modelo, tokens/cache consumidos. Hoy /v1/models devuelve solo `{id, object, created, owned_by}` — los clientes no pueden elegir modelo ni optimizar reasoning effort.

**In scope:** metadata de modelo en config (context_window, max_output, thinking levels, pricing), /v1/models enriquecido, exposición de usage a clientes (headers/endpoint). **Out of scope:** policy de enrutamiento por modelo (futuro).

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 007-001-model-metadata | Modelo de datos de modelo (context_window, max_output, thinking, pricing) en config | 006-001 | No |
| 2 | 007-002-models-endpoint | /v1/models enriquecido compatible con lo que opencode/openclaw leen | 007-001 | No |
| 3 | 007-003-usage-para-clientes | Exponer tokens/costo/cache consumido por cliente (headers o endpoint) | 006-001 | Sí |

---

### EPIC-008: mofgw-contexto — Optimización de contexto
**Resumen:** Nivel más complejo de medición y optimización: budget de contexto por cliente, rechazo temprano de requests que excedan la ventana (hoy se van upstream y queman intentos de la cadena), stats por sesión. Depende de 006 (medición) y 007 (ventanas conocidas).

**In scope:** budget por cliente, rechazo temprano por ventana, stats por sesión. **Out of scope:** reescritura/compresión de prompts (vive en los runtimes).

**Decomposición en features:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 008-001-rechazo-por-ventana | Rechazo temprano (400) de requests que excedan la ventana del modelo destino | 007-001 | No |
| 2 | 008-002-budget-por-cliente | Límite de tokens/costo por cliente por período | 006-002 | No |
| 3 | 008-003-stats-por-sesion | Correlación de requests por sesión + stats de contexto | 006-001 | Sí |

---

### EPIC-004: mofgw-escala — Alta carga multi-agente
**Resumen:** Soportar muchos agentes clientes simultáneos pasando por el proxy, cada uno con sus asuntos, sin interferencia ni degradación.

**In scope:** concurrencia Go (goroutines, límites), aislamiento por agente cliente (identificación vía header — `X-Agent-Id`, ya autenticado por 001-007), límites de concurrencia por agente, backpressure. **Out of scope:** multi-tenancy completo (tenants, roles), rate limiting por facturación.

**Decomposición (draft, refinar en planning):**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 004-001-concurrencia | Manejo de alta concurrencia con límites | 001-001 | No |
| 2 | 004-002-aislamiento | Aislamiento por agente cliente (headers, límites por agente) | 004-001 | No |

---

### EPIC-005: mofgw-ops — Operación y observabilidad
**Resumen:** Métricas (Prometheus), logging estructurado, systemd user service, config paths estándar. Hacer el proxy operable y debugeable.

**In scope:** métricas Prometheus (`/metrics`), logging estructurado (JSON), unit systemd user, resolución de config (`~/.config/mofgw/` o `/etc/mofgw/`), flags CLI mínimos. **Out of scope:** dashboard, alerting, multi-host.

**Decomposición:**
| # | Feature ID | Descripción | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 005-001-metrics | Métricas Prometheus (requests, fallbacks, cooldowns, latencia) | 001-001 | No | ✅ spec 04 Ago |
| 2 | 005-002-logging | Logging estructurado JSON | — | Sí |
| 3 | 005-003-systemd | Unit systemd user + install script | 001-001 | No | ✅ spec 04 Ago |
| 4 | 005-004-config-paths | Resolución de config por nivel (home vs sistema) | 001-002 | Sí |

---

## Orden de ejecución

```
EPIC-001 (core/MVP) → EPIC-002 (resiliencia) → EPIC-005 (ops/metrics) → EPIC-004 (escala) → EPIC-003 (eficiencia)
```

**Actualizado 06 Ago 2026:** 001/002/004/005 done. El replanteo de eficiencia arranca en este orden:

```
EPIC-003 (cache providers — rápido, primero por decisión Pablo) → EPIC-006 (medición — la base real) → EPIC-007 (catálogo) → EPIC-008 (contexto — el más complejo, depende de todos)
```

Racional: cache primero porque es rápido y la decisión de Pablo es avanzar en ese orden. Medición segundo porque sin datos no hay optimización real. Catálogo tercero porque da valor a los runtimes (pueden elegir modelo). Contexto al final porque depende de medición (006), ventanas conocidas (007) y budget (006-002).

## Criterios de aceptación del programa completo

- [ ] Los 8 epics están done (001-008).
- [ ] E2E integral: OpenClaw (sesiones, crons, sesiones aisladas) corriendo contra mofgw en 3369, sin omniroute, sin fallbacks nativos de OpenClaw, durante 48h sin un solo error visible al cliente.
- [ ] omniroute.service deshabilitado, ~1GB RAM liberada.
- [ ] Métricas en Prometheus muestran fallbacks y cooldowns en acción.
- [ ] 10+ agentes clientes concurrentes sin degradación.
- [ ] (Nuevo 06 Ago) /metrics contabiliza tokens y costo por cliente/modelo/provider.
- [ ] (Nuevo 06 Ago) /v1/models expone capabilities (context window, max_output, thinking) y opencode/openclaw las consumen.
- [ ] (Nuevo 06 Ago) Requests que exceden la ventana de contexto se rechazan temprano sin quemar intentos de la cadena.

## Stakeholders

- **Aprobador del plan**: Pablo (autor del proyecto)
- **Aprobador de specs**: Pablo
- **Operador del resultado**: Ofap + futuros agentes autónomos

## Cambios al plan

- 2026-08-03: Plan inicial creado. Investigación de arquitectura en curso (subagente). La decomposición de EPIC-003 queda pendiente de research-architecture.md.
- 2026-08-06: EPIC-001/002/004/005 marcados DONE (verificado en README + suite). EPIC-003 replanteado: cache de providers (003-001) + nuevos EPIC-006 (medición), EPIC-007 (catálogo), EPIC-008 (contexto). Decisión Pablo: delegación total a Ofap (auto-aprobación GVR). Discovery en curso.
