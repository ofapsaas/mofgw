# mofgw — Investigación y Arquitectura

**Fecha:** 2026-08-03 · **Estado:** Propuesta MVP · **Ámbito:** proxy IA en Go, liviano y minimalista.
**Objetivo:** reemplazar un gateway previo en Node (~1GB RAM, poco confiable) con un proxy Go que expone UN endpoint OpenAI-compatible y rota entre varios providers upstream (OpenAI-compatible, configurables) con **transparencia total**: el cliente nunca debe enterarse de un fallo.

---

## 1. Arquitectura Go recomendada

**Patrón: servidor HTTP estándar + reverse proxy con `httputil.ReverseProxy`.**

- El servidor expone `POST /v1/chat/completions` (stream y no-stream), `GET /v1/models`, `GET /healthz`, `GET /metrics`. `http.ServeMux` de Go 1.22+ alcanza (patterns con método y wildcards, sin dependencias) — https://go.dev/blog/routing-enhancements.
- **Abstracción de provider:** una interfaz mínima con 1 método (`Complete(ctx, req)`) y un cliente OpenAI-compatible (OpenAI-compatible = JSON + SSE, todos nuestros upstreams lo hablan). El mapping modelo→provider vive en config (alias: lo que pide el cliente → modelo real por provider). El proxy reescribe el campo `model` en request y response (el cliente siempre ve el modelo que pidió).
- **Estado de cooldown en memoria:** `map[string]cooldownEntry` (proveedor → `until time.Time`, contador de fallos) protegido por `sync.Mutex` (5 providers: un mutex alcanza, no hace falta `sync.Map` ni sharding). Un `time.Ticker` (1 min) limpia entradas expiradas. Sin Redis, sin DB: el estado es epímero por diseño; al reiniciar, todos los providers empiezan fríos (aceptable).
- **Modelo de concurrencia:** goroutine-por-request (es el modelo del runtime Go; el servidor HTTP ya escala). Lo que hay que proteger son los providers débiles:
  - **Semáforo de in-flight por provider** (`chan struct{}`, ~8-16) para que un provider lento no acumule goroutines ni abra conexiones sin límite.
  - **Un solo `http.Transport` compartido** con connection pooling (ver §4).
- **Connection pooling/keep-alive:** un `http.Transport` global reutilizado por todos los providers, con `MaxIdleConnsPerHost ≥ 8`, `IdleConnTimeout: 90s`, `TLSHandshakeTimeout: 10s`, `ResponseHeaderTimeout: 60s` (TTFB), `DialContext` con timeout 10s, y **`ForceAttemptHTTP2: true`** (customizar Transport desactiva HTTP/2 por defecto — https://pkg.go.dev/net/http#Transport).
- **`httputil.ReverseProxy`** ya maneja streaming SSE, flush y cancelación si se configura bien (`FlushInterval: -1` = flush inmediato tras cada write — https://pkg.go.dev/net/http/httputil#ReverseProxy). No hay que reimplementar el passthrough.
- **Deadline global por request:** `context.WithTimeout` (p. ej. 10 min streaming, 180s no-stream). Cuando el cliente se desconecta, cancelar el contexto aguas arriba para liberar el stream.

## 2. Librerías Go (con justificación)

| Necesidad | Elección | Por qué |
|---|---|---|
| Router HTTP | **`net/http` stdlib** | Go 1.22+ tiene method+path patterns; 4 rutas no justifican chi/echo. Cero deps. |
| Reverse proxy / SSE | **`net/http/httputil` stdlib** | Streaming, flush y upgrades ya resueltos. |
| Cliente HTTP | **`net/http.Transport` stdlib** | Con pooling configurado (§1) es superior a fasthttp (que rompe la ergonomía de streaming/context). |
| Logging | **`log/slog` stdlib** (Go 1.21+) | Suficiente para MVP; JSON handler para journald. zerolog solo si el throughput lo exige (no lo va a exigir). |
| Config YAML | **`gopkg.in/yaml.v3`** | Estándar de facto para YAML en Go — https://pkg.go.dev/gopkg.in/yaml.v3. Alternativa `sigs.k8s.io/yaml` (usa tags JSON) solo si se mezcla con k8s. |
| Métricas | **`github.com/prometheus/client_golang`** | Es el cliente oficial (https://prometheus.io/docs/guides/go-application/); `promhttp.Handler()` en `/metrics`. |
| SSE | **Ninguna** | SSE es HTTP plano (`text/event-stream`); se usa `http.Flusher`. La spec: https://html.spec.whatwg.org/multipage/server-sent-events.html. |

Regla: stdlib hasta que duela. Este proyecto debería quedar en ~3-5 dependencias.

## 3. Semántica de fallback en streaming SSE

**El punto crítico:** una vez emitido el primer token, el stream está *comprometido*. No se puede conmutar de provider a mitad de respuesta: los continuations de modelos distintos son incompatibles (tokenizers, IDs, estilos) y el cliente recibiría basura. Opciones:

- **A) Buffer hasta el primer token y reintentar** — el proxy espera el primer byte del upstream con un timeout TTFB por intento (default 60s). Si el intento falla *antes* del primer byte (429/5xx/timeout/400-max_tokens), se descarta y se prueba el siguiente provider. El cliente solo ve el primer intento que produce tokens. **Coste:** latencia percibida = TTFB del intento fallido + reintento (enmascarable con timeout TTFB agresivo).
- **B) Error al cliente si el stream se corta a mitad** — imposible retractar tokens ya enviados; lo único honesto es mandar un evento SSE de error y cerrar. El cliente ve un stream truncado (OpenClaw lo reportaría como fallo).
- **C) "Splice" de streams entre providers** — descartado: inviable técnicamente (modelos distintos) y complejidad sin retorno.

**Recomendación: A para la fase pre-primer-byte + B para fallos mid-stream.**
1. Reintentar solo hasta el primer byte (esto es exactamente lo que LiteLLM hace: los retries aplican a la fase de conexión/respuesta, no a un stream ya iniciado — ver §5).
2. Con el primer byte emitido, el fallback termina: si el stream muere a mitad, propagar un evento SSE de error limpio (con `retry`/`[DONE]` bien formado) y registrar métrica. **Nunca** reconectar a otro provider.
3. Detalles de passthrough: reenviar headers relevantes (`Authorization` se reemplaza por la key del provider elegido, `Accept`, `Content-Type`), propagar el `id` del upstream (o reescribirlo estable) y reescribir `model` al nombre que pidió el cliente.
4. En **no-streaming** el buffer es total (respuesta JSON completa) → ahí sí se puede reintentar sin límite de "primer byte", hasta agotar el chain.

Clasificación de reintentables: 408, 429, 5xx, errores de red (timeout, conn refused, TLS) y **400 cuyo body indique `max_tokens`/`context_length_exceeded`** (caso listado en los requisitos: saltar a un provider con más contexto). No reintentar 400 de payload inválido ni 401/403.

## 4. Prompt caching + eficiencia

**Caching del lado provider (lo que de verdad da plata):**
- **OpenAI:** prefix caching automático desde sept 2024 — prompts ≥1024 tokens, cache hit por prefijo exacto, TTL de inactividad ~5-10 min, sin cambios de API (https://platform.openai.com/docs/guides/prompt-caching). **OpenRouter:** idem, automático (https://openrouter.ai/docs/features/prompt-caching). **Qwen/Bailian (DashScope):** context cache automática de prefijos idénticos (https://help.aliyun.com/zh/model-studio/context-cache).
- **Lo que el gateway puede hacer para maximizar hits:** (1) mantener el system prompt y las definiciones de tools **al inicio del array de messages y byte-idénticos entre requests** (nada de timestamps/IDs inyectados en el system prompt); (2) no reordenar mensajes; (3) no tocar el prefix de cada cliente.
- **Anthropic** usa `cache_control` explícito (https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching); solo relevante si algún upstream lo requiere vía headers — el proxy puede passthrough de headers de cache sin interpretarlos.

**Cache en el gateway (fase 2, NO en MVP):** un cache de respuestas exact-match (hash del request normalizado → respuesta completa) solo tiene sentido si hay requests repetidos idénticos (tests, retries del cliente). Es una optimización barata de implementar (mapa + TTL) pero con hit-rate incierto en workloads de agentes conversacionales. **Decisión:** medirlo con métricas primero; si el hit-rate no justifica, no implementar. El cache de prompts del lado provider es el que da plata real y es gratis (solo requiere no romper prefijos).

## 5. Referencia funcional: cómo resuelve LiteLLM fallback/cooldown

LiteLLM es la referencia funcional del dominio (no vamos a copiarlo, pero su modelo mental es correcto). Docs: https://docs.litellm.ai/docs/routing y https://docs.litellm.ai/docs/proxy/reliability.

- **`model_list`** — lista de "deployments": cada entrada mapea un `model_name` (virtual, lo que ve el cliente) a un `litellm_params` (modelo real, `api_base`, `api_key`). Varias entradas con el mismo `model_name` = grupo de fallback/load-balance. → Equivalente mofgw: aliases en config.
- **`router_settings`**:
  - `num_retries` (int) — reintentos por request fallida.
  - `cooldown_time` (float, segundos) — cuánto tiempo un modelo queda "frío" tras fallar.
  - `allowed_fails` (int) — número de fallos consecutivos antes de entrar en cooldown.
  - `retry_after` (float) — espera entre reintentos.
  - `fallbacks` — lista de modelos alternativos por modelo primario.
  - Estrategias de routing: `simple-shuffle`, `least-busy`, `usage-based-routing`, `latency-based-routing`. Para MVP: orden fijo (la chain es explícita en config: provider-a → provider-b → provider-c → provider-d).
- **Health checks:** endpoints `/health/liveliness` y `/health/readiness` en el proxy, más `health_check_interval` para sondear upstreams en background (https://docs.litellm.ai/docs/proxy/health). → mofgw: `/healthz` + health checks periódicos (EPIC-002).
- **Mecánica de cooldown (la que nos interesa):** cuando un deployment supera `allowed_fails` fallos, se marca en cooldown por `cooldown_time`; el router no lo usa hasta que expira. Los fallos que cuentan son los reintentables (timeouts, 429, 5xx). Un 200 resetea el contador.
- **Lección clave para mofgw:** LiteLLM reintenta en la fase de *conexión/respuesta*, no en un stream ya emitido — valida nuestra decisión §3-A/B. Y su fallback es **a nivel de request**, no de conversación: el cliente no sabe que cambió de upstream porque el contrato OpenAI-compatible es el mismo.

## 6. Layout de módulos sugerido (MVP)

```
mofgw/
├── cmd/mofgw/main.go          — entrypoint: flags, carga de config, bootstrap
├── internal/config/           — carga/validación YAML (~/.config/mofgw/config.yaml o /etc/mofgw/)
├── internal/provider/         — interfaz Provider + cliente OpenAI-compatible por upstream
├── internal/router/           — chain de fallback, estado de cooldown, semáforos in-flight
├── internal/clamp/             — reescritura max_tokens/max_completion_tokens (001-004, paquete puro)
├── internal/timeouts/          — TimeoutFor/AttemptContext/ErrTimeout (001-006, paquete puro)
├── internal/stream/            — passthrough SSE con frontera primer-byte (001-005)
├── internal/proxy/            — wiring httputil.ReverseProxy, reescritura model, SSE
├── internal/metrics/          — counters/histograms Prometheus
├── internal/logging/          — setup slog (JSON)
├── config.example.yaml        — providers, orden de fallback, cooldowns, timeouts
└── docs/                      — research, spec, epics
```

Nota (cross-spec review 04 Ago): `internal/clamp`, `internal/timeouts` e `internal/stream` fueron agregados a este layout porque las specs 001-004/001-006/001-005 los referencian (go test ./internal/clamp/ etc.). clamp y timeouts son paquetes puros (cero red, fake clock inyectable); stream es paquete propio para aislar el test del passthrough SSE sin acoplar a proxy.

Criterio: cada paquete con una responsabilidad, sin dependencias circulares, ~5 archivos de lógica por paquete como máximo. El router y el proxy son los dos paquetes que concentran la complejidad; el resto es glue.

## 7. Recomendación final

**La arquitectura más simple, escalable y resiliente para mofgw es: un reverse proxy stateless con estado de cooldown en RAM.**

1. **Simple:** stdlib Go (net/http + httputil.ReverseProxy + slog + yaml.v3 + client_golang). 3-5 dependencias. Sin DB, sin colas, sin framework. Config YAML explícita.
2. **Escalable:** goroutine-por-request + un solo http.Transport con pooling + semáforo in-flight por provider (8-16). El proxy no guarda estado de conversación → se pueden correr N instancias detrás de un socket systemd o un balanceador si algún día hace falta (no va a hacer falta: 1 instancia soporta decenas de agentes).
3. **Resiliente:** fallback en cadena explícito (config), cooldown por provider con contador de fallos, retry hasta el primer byte en streaming, timeout global por request (mata los 859s de omniroute), clasificación de errores reintentables (429/5xx/408/timeout/400-max_tokens).

**MVP (EPIC-001) en una frase:** `httputil.ReverseProxy` + interfaz Provider + mapa de cooldown con mutex + reescritura de `model` + config YAML, corriendo en :3369 como systemd user service.

**Fuera de scope deliberado:** DB, Redis, dashboard, 160 providers, compresión de tokens, cache de respuestas (medir primero), load-balancing sofisticado (orden fijo alcanza), multi-tenancy completo (tenants/roles).

**Nota (04 Ago):** auth básico de clientes (API key por cliente, Bearer) pasó a in-scope del MVP — feature 001-007-auth — porque los agentes clientes apuntan a mofgw y las keys de providers viven solo en el proxy. El endpoint no puede quedar abierto sin identificación.
