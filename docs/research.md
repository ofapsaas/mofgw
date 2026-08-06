<!-- SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Investigación: AI Gateway Proxy

**Estado:** 🔬 Borrador inicial — iterar con Pablo
**Objetivo:** Definir el feature set mínimo viable que realmente aporte valor, antes de escribir una línea de código Go.

---

## 1. Problema

### 1.1 Qué falla hoy

| Síntoma | Causa | Impacto |
|---------|-------|---------|
| Heartbeat muerto por horas | El provider principal alcanza su límite (429), el fallback nativo no retransparenta | 101 errores en 12h, bitácora vacía |
| 400 max_tokens | El cliente envía 131072, un provider acepta ≤ 65536 | El ciclo de sesiones aisladas muere en la model-call |
| 401 expired key | Cuentas free de providers expiran; el fallback nativo no reintenta bien | Sesiones aisladas mueren sin log |
| 180-859s timeouts | Timeout del provider; el cliente no aborta rápido | 3-5 errores/hora, proceso colgado |

### 1.2 Por qué un proxy externo

OpenClaw implementa fallback a nivel de **config** (primary → fallback#1 → fallback#2), pero:
- Si el primary falla **durante** una request, la sesión aislada (cron, hb) muere antes de alcanzar el fallback.
- No hay cooldown: si el provider principal está en 429, cada ciclo intenta primary → timeout → recién ahí fallback.
- El fallback no es transparente: el cliente (OpenClaw) sabe que cambió de provider, y en sesiones aisladas eso equivale a perder la request.

Un proxy resuelve esto: el cliente ve **un endpoint que siempre responde**. El proxy internamente reintenta con otro provider, aplica cooldown, clamp de max_tokens, etc.

---

## 2. Competidores

### 2.1 omniroute (v3.8.48, Node)

**Lo que hace bien:**
- Routing entre 160+ providers con auto-fallback
- Compresión RTK+Caveman (ahorro 15-95% tokens)
- Dashboard web
- MCP/A2A support

**Lo que hace mal:**
- **~1GB RAM** — 55s de arranque, proceso Node monstruoso
- **DB SQLite** para config — estado persistente innecesario para routing
- **250 providers** que no usamos — 99% del código es ruido para nosotros
- **Tiene bugs** — 400 max_tokens no clampado, middlewares no cargan en REST path
- **Node** — ecosistema pesado para lo que necesitamos

**Lecciones:** No necesitamos 160 providers. Necesitamos 5, bien configurados. No necesitamos DB. No necesitamos dashboard.

### 2.2 LiteLLM (Python, open source, MIT)

**Lo que hace bien:**
- **Fallback transparente** con cooldown, retry, load balancing — probado en producción en cientos de equipos
- Config YAML declarativa, fácil de versionar
- OpenAI-compatible completo (streaming, tools, vision, etc.)
- Health checks, rate limit tracking, cooldown automático por provider
- Docker / pip / systemd

**Lo que hace mal:**
- **Python** — runtime pesado, dependencias (openai, anthropic, httpx, etc.)
- **Muchas features** que no usamos (100+ providers, spending, caching, etc.)
- Documentación extensa pero no siempre clara en edge cases

**Lecciones:** LiteLLM resuelve exactamente el problema. Pero es Python. Si vamos a salir de Node, no nos casemos con Python. Go es la apuesta correcta a largo plazo (kernel-ia). Pero LiteLLM es la referencia funcional — nuestro diseño debería imitar sus mecanismos de fallback/cooldown.

### 2.3 OpenRouter (hosted)

**Pros:** No hay que mantenerlo, funciona, maneja fallback y rate limits.
**Cons:** Dependencia externa, datos pasan por terceros, cuesta plata, no tenemos control de cooldowns ni providers.

**Veredicto:** No. Queremos self-hosted, controlado, parte de nuestra infra.

### 2.4 ClawRouter (hosted, OpenClaw)

**Pros:** Nativo de OpenClaw, modelo catalog scoped, quotas, bundled.
**Cons:** Requiere credential de admin ClawRouter (no tenemos), es hosted, no self-hosted, no controlamos fallback.

**Veredicto:** No aplica.

---

## 3. Feature Set

### 3.1 Must-Have (MVP — sin esto no sirve)

| Feature | Justificación | Referencia |
|---------|---------------|------------|
| **Endpoint OpenAI-compatible único** | `/v1/chat/completions`, `/v1/models` — OpenClaw apunta a 1 endpoint | omniroute, LiteLLM |
| **Fallback automático en cadena** | Si provider A falla (429/5xx/timeout), reintentar con B, C, D... | LiteLLM `fallbacks` |
| **Cooldown por provider** | Si un provider da 429, no reintentarlo por N minutos | LiteLLM `cooldown_time` |
| **Streaming SSE passthrough** | Las respuestas streaming deben pasar sin romperse | omniroute, LiteLLM |
| **Clamp de max_tokens por provider** | provider-c max 65536, provider-a 384k — el proxy clamp automáticamente | Fix manual en omniroute DB |
| **Timeout configurable por intento** | No esperar 859s por un timeout | LiteLLM `timeout` |
| **Config file (YAML/TOML)** | Lista de providers, orden de fallback, cooldowns, timeouts — versionable | LiteLLM |

### 3.2 Nice-to-Have (post-MVP)

| Feature | Valor | Complejidad |
|---------|-------|-------------|
| **Load balancing** (round-robin entre providers sanos) | Media — evita que un provider siempre sea el primero | Media |
| **Métricas Prometheus** | Alta — visibility para crons/hb debug | Media |
| **Health check endpoint** | Media — para systemd healthchecks | Baja |
| **Retry con backoff** | Media — para timeouts transitorios | Baja |
| **Logging estructurado** (JSON) | Alta — debug de fallas | Baja |
| **API key validation** | Media — seguridad básica | Baja |

### 3.3 Out of Scope (por ahora)

| Feature | Por qué no |
|---------|------------|
| Caching / KV storage | No lo necesitamos para fallback; agrega estado y complejidad |
| Dashboard web | No lo necesitamos; logs + métricas alcanzan |
| 160+ providers | Usamos 5; agregar más es mantener lo que no usamos |
| Compresión (RTK/Caveman) | No es el problema; el problema es que el fallback no funciona |
| MCP / A2A | No es relevante para este proxy |
| Multi-usuario | Somos nosotros |
| Persistencia (DB) | El estado es efímero (cooldowns en memoria) |

---

## 4. Consideraciones de Diseño

### 4.1 Arquitectura

```
OpenClaw / curl / cualquier cliente
        │
        ▼
 ┌─────────────────┐
 │  AI Gateway      │  Go binary, stateless (excepto cooldowns en RAM)
 │  :4000/v1        │  Config file: providers.yaml
 │  Fallback chain  │
 └────────┬────────┘
          │
     ┌─────┼──────┬──────────┐
     ▼     ▼      ▼          ▼
 provider-a provider-b provider-c provider-d
```

**Decisiones:**
- **Sin DB** — cooldowns en memoria, se pierden al reiniciar (aceptable)
- **Sin estado** — el proxy no trackea sesiones, solo reenvía requests
- **Config file estático** — YAML/TOML, recargable con SIGHUP
- **Concurrencia** — Go goroutines, un pool por provider, semáforos de cooldown

### 4.2 Manejo de Streaming

El streaming es la parte más delicada. El proxy debe:
1. Abrir SSE con provider A
2. Si la conexión se cae antes de recibir `[DONE]`, reconectar con provider B
3. Esto es complejo — el cliente ya recibió tokens parciales de A. ¿Cómo manejar la transición?
   - Opción 1: **No hacer fallback en streaming**. Si streaming falla, error al cliente. Solo fallback en requests no-stream.
   - Opción 2: **Buffer completo**. No streamear hasta tener respuesta completa del provider. Más latencia pero fallback real.
   - Opción 3: **Fallback parcial**. Si cae antes del primer token, reintentar. Si ya entregó tokens, error.

**Pregunta para definir:** ¿Qué hacemos con streaming fallback? La opción 1 (no fallback en streaming) es la más simple y la que usa LiteLLM por defecto para requests con streaming habilitado. Pero OpenClaw usa streaming para todo. La opción 3 (fallback hasta el primer token) es un buen balance.

### 4.3 Cooldown Strategy

- Cuando un provider responde 429, 503, timeout, o error 5xx → marcar como "en cooldown" por N segundos
- Configurable por provider (ej: opencode 429 → 300s cooldown, timeout → 60s)
- Cooldown en memoria, no persistente
- Si todos los providers están en cooldown → esperar hasta que el primero se libere, o devolver error

### 4.4 max_tokens Clamping

Problema: OpenClaw envía max_tokens=131072. Alibaba/qwen solo acepta ≤ 65536 y devuelve 400.

Solución: el proxy conoce el límite de cada provider y clamp automáticamente:
- `qwen3.6-flash` → max_tokens = min(req.max_tokens, 65536)
- `deepseek-v4-flash` → max_tokens = min(req.max_tokens, 384000)
- Si el valor clampado es menor al mínimo del provider, usar el mínimo del provider

Esto ya se configuró manualmente en omniroute DB; el proxy lo hace nativamente.

---

## 5. Decisiones tomadas (Pablo, 03 Ago 2026)

| # | Decisión | Valor |
|---|----------|-------|
| 1 | **Nombre** | `mofgw` (MOF Gateway) |
| 2 | **Transparencia** | Total — el cliente nunca se entera. El proxy salva todas las situaciones para que el agente cliente nunca sepa que un provider no respondió como debía |
| 3 | **Deploy** | systemd user, básico, mínimo |
| 4 | **Puerto** | **3369** — estadísticamente el menos usado de 1025-4000: sin asignación IANA, ausente en /etc/services, nada escuchando. Centro del hueco libre 3367-3371 (el más largo del rango) |
| 5 | **Config** | `~/.config/mofgw/` si es a nivel home; `/etc/mofgw/` si es a nivel sistema |
| 6 | **Feature order** | Metrics primero (después del MVP) |
| 7 | **Alta carga** | Debe soportar alta carga de agentes clientes pasando por el proxy, cada uno con sus asuntos |
| 8 | **Eficiencia** | Optimizar al menos el cache de LLM + buenas prácticas de eficiencia básica — a investigar bien |

**Implicaciones nuevas vs. borrador original:**
- **Streaming fallback** (pregunta 2): sin decisión explícita aún — lo define la investigación de arquitectura. Principio rector: el cliente nunca se entera, así que la opción de "error al cliente" queda descartada salvo evidencia en contra.
- **Alta carga multi-agente** pasa de nice-to-have a requisito explícito → nuevo EPIC-004.
- **Cache LLM + eficiencia** pasa de out-of-scope a requisito explícito → nuevo EPIC-003 (requiere investigación).
- **Metrics** confirmado como primer feature post-MVP.

---

## 6. Próximos Pasos

1. ✅ Proyecto creado (`projects/mofgw/`)
2. ✅ Nombre definido: mofgw
3. ✅ Decisiones registradas (tabla §5)
4. ✅ Epics definidos (`docs/epics/plan.md` — 5 epics con cdad-epic)
5. 🔄 Investigación de arquitectura (subagente en curso → `docs/research-architecture.md`)
6. □ Refinar epics con resultados de investigación
7. □ Escribir SPEC.md con diseño detallado
8. □ Implementar MVP (endpoint único + fallback + cooldown + streaming)
9. □ Deploy como systemd user service en :3369
10. □ Configurar OpenClaw para apuntar al proxy
11. □ Deshabilitar omniroute.service (liberar ~1GB RAM)