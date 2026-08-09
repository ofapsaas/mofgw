# Research: Gateway-Level Response Cache — EPIC-003 (eficiencia)

> S37 Apuesta A (09 Ago 2026). Investigación de la semántica de hits de cache
> (prompt-prefix vs exact-match vs embedding) + decisión de arquitectura
> (dónde cachear: gateway vs provider). Complementa `research-token-efficiency.md`
> (cache de providers, ya instrumentado en 003-001).
> Estado: **DISEÑO — implementación NO aprobada aún** (appetite = investigación + diseño).

## 0. Contexto: qué ya está cacheado y funcionando

Antes de diseñar cache a nivel gateway hay que registrar lo que **ya existe** (verificado en prod, 09 Ago 04:44 — BACKLOG S37):

| Capa | Estado | Evidencia |
|---|---|---|
| Cache de providers (prefix/KV automático) | ✅ ACTIVO en los 5 providers (DeepSeek automático, Alibaba implícito, MiniMax/GLM/Kimi automáticos, Zen pass-through) | `research-token-efficiency.md` §2 |
| Instrumentación gateway (003-001) | ✅ contadores cache_hit/miss + reasoning + headers `X-Usage-Cache-Hit-Tokens` | proxy.go:497-514 |
| **Hit rate efectivo en producción** | **~97.8%** (hit 2,498,998,864 / miss 57,116,068, deepseek-v4-flash en go-1: 98.2%) | telemetría prod 09 Ago 04:44 |
| Trafico de referencia | 2,802 requests / 7.4h (~6.3 req/min), 2559 flash + 243 qwen3.7-plus, 61-65% con X-Session-Id | telemetría 09 Ago 05:31 |

**Conclusión inmediata:** el cache de **prefijo** (el que domina el ahorro en workloads agénticos — prompts gigantes con system prompt estable) ya está activo y rindiendo. El gateway NO tiene palanca sobre su tasa de hit: el prefijo lo construye el runtime cliente (OpenClaw/OpenCode); reordenar mensajes en el gateway cambiaría la semántica. **Cualquier cache nuevo a nivel gateway solo puede atacar requests DUPLICADOS completos, no prefijos.**

## 1. Taxonomía de semántica de hits

Tres familias, con riesgos y casos de uso distintos:

### 1.1 Exact-match (determinístico)
- **Key:** hash canónico de TODO el request (model + messages + sampling params + tools + max_tokens + cliente).
- **Sirve solo requests byte-idénticos.** Cero riesgo de respuesta stale a prompt "similar".
- **Referencia:** GPTCache modo exact (`similarity_threshold` = exacto), ver §3.
- **Uso en mofgw:** retries del mismo request, llamadas paralelas duplicadas, re-envíos idempotentes.
- **Limitación:** en workloads agénticos la tasa esperada de duplicados exactos es **baja** (cada turno muta el historial) — estimación propia, sin benchmark (ver §5).

### 1.2 Prompt-prefix (KV reuse)
- **Qué es:** el provider cachea el estado de atención del prefijo compartido (system prompt + primeros turnos). NO requiere requests idénticos.
- **Dónde vive:** exclusivamente en el provider (ya activo, §0). Un gateway NO puede "cachear por prefijo" respuestas completas: un prefijo compartido no determina el completion.
- **Palanca real del gateway:** ninguna (la construcción del prefijo es del cliente). Documentar y cerrar.

### 1.3 Semantic/embedding (similarity)
- **Qué es:** embed el request, servir la respuesta almacenada del vecino más cercano sobre un umbral (GPTCache, Redis LangCache).
- **Referencia principal:** GPTCache (Zilliz, MIT) — "semantic cache for LLMs", integrado con LangChain/llama_index (github.com/zilliztech/GPTCache, accedido 09 Ago 2026).
- **Riesgos para un gateway agéntico:**
  1. **Staleness:** un request "similar" puede requerir datos actuales (tool results, estado del sistema) — servir una respuesta vieja es un error de negocio silencioso.
  2. **Transparencia rota:** contradice el principio rector (el cliente nunca se entera / respuesta indistinguible de upstream). Un hit semántico devuelve contenido que NINGÚN provider generó para ese request.
  3. **Complejidad:** embedding runtime + índice vectorial en un gateway de 12MB RAM.
- **Veredicto:** **FUERA DE SCOPE** para mofgw. Anti-scope explícito de la apuesta ("sin romper transparencia total").

## 2. Análisis de workload (qué pide realmente mofgw)

Datos de telemetría prod (09 Ago, verificado):
- **Streaming-dominante:** OpenClaw/OpenCode usan streaming con `include_usage`. Un cache de respuestas debe decidir cómo servir un HIT a un request streaming (ver §4.3).
- **Prompts únicos por turno:** el historial muta en cada request → duplicados exactos son la excepción, no la regla.
- **Determinismo parcial:** los runtimes mandan `temperature` (a menudo 0 en coding/agentes) pero no siempre `seed`.
- **Concurrencia baja:** ~6.3 req/min en el pico observado. El costo dominante NO es el número de requests, es el tamaño de prompt (y eso ya lo cubre el cache de providers).

**Implicación:** el caso de uso que SÍ tiene valor a nivel gateway no es "repetir requests idénticos a lo largo del tiempo" (poco frecuente), sino **requests idénticos en una ventana corta** — retries en paralelo, re-envíos por timeout del cliente, dos agentes con el mismo prompt semilla. Eso se ataca con *single-flight / request coalescing* (dedupe en vuelo), no con un cache persistente.

## 3. Referencias verificadas

| Fuente | Qué aporta | Fecha acceso |
|---|---|---|
| github.com/zilliztech/GPTCache | Referencia canónica de semantic cache (exact + similar matching), MIT, integrado LangChain/llama_index | 09 Ago 2026 |
| api-docs.deepseek.com/quick_start/pricing/ | Cache automático DeepSeek (min. 64 tokens, prefijo desde token 0) — citado en research-token-efficiency.md | 06 Ago 2026 |
| opencode.ai/docs/zen | Zen pass-through: ya normaliza cached_tokens + inyecta include_usage — citado en research-token-efficiency.md | 06 Ago 2026 |
| Telemetría prod mofgw (009-000) | Hit rate 97.8%, 2,802 req/7.4h, 61-65% X-Session-Id | 09 Ago 2026 |

## 4. Decisión de arquitectura (¿dónde cachear?)

### 4.1 Opciones evaluadas

| Opción | Beneficio | Costo | Riesgo | Veredicto |
|---|---|---|---|---|
| A. Cache persistente exact-match (TTL) | Sirve duplicados espaciados en el tiempo | RAM (entries), invalidation | Stale solo si el mundo cambió entre hits idénticos (bajo, pero existe) | **P1 opt-in** |
| B. Single-flight / coalescing (ventana ~segundos) | Mata duplicados concurrentes sin almacenar nada durable | ~nada (mapa in-flight) | Ninguno (requests idénticos → misma respuesta esperada) | **P0 si se implementa** |
| C. Semantic cache (embedding) | Altísimo hit rate teórico | Embedding + índice + storage | Staleness, transparencia rota, complejidad | **NO** (anti-scope) |
| D. Prefix normalization gateway | Nada real: el prefijo lo construye el cliente | — | — | **DESCARTADA** (§1.2) |

### 4.2 Diseño propuesto (si se implementa — decisión de Pablo/operador)

**Cache exact-match opt-in (feature `010-001-response-cache`), OFF por defecto:**

1. **Elegibilidad:** solo requests **no-streaming** con `temperature == 0` (o `seed` presente). Streaming y temperature>0 → pasan directo (el determinismo es el contrato del cache).
2. **Key:** `sha256(canonical JSON)` de `{client_id, model, messages, params completos, tools, max_tokens}`. Canonical = orden estable de claves (json.Marshal de struct es estable en Go). **client_id en la key** = partición por cliente (cero riesgo de fuga entre clientes, aunque prompts idénticos).
3. **Entry:** `{response JSON completo (incl. usage), created_at, ttl}`. El usage se replica del original (el accounting no se pierde).
4. **Storage:** LRU en memoria, `max_entries` configurable (default modesto, p.ej. 512 — respeta el principio de 12MB RAM), TTL default 5 min.
5. **Transparencia:** headers `X-Mofgw-Cache: HIT|MISS` + `X-Mofgw-Cache-Age`; el hit se registra en telemetría/metrics (`cache_resp_hit/miss` + razón). El cliente SIEMPRE puede ver que vino del cache — principio rector intacto.
6. **Posición en handleChat:** después de telemetría/análisis de contexto (el discovery ve TODO el tráfico, R4) y después de límites de concurrencia, ANTES del router/chain. El chequeo de ventana de contexto (008-001) se saltea en HIT (la respuesta ya existe y fue generada válidamente).
7. **Interacción con fallback/sticky:** el cache vive ANTES de la cadena → un HIT no consume provider ni toca cooldowns. Un MISS sigue el flujo actual intacto (anti-scope: no tocar la cadena).

**Single-flight (P0, barato):** mapa `key → chan` con ventana ~5s. Si un request idéntico está en vuelo, el segundo espera la misma respuesta. Sin storage durable, sin TTL, cero riesgo. Este es el que ataca el caso real detectado en §2.

### 4.3 Streaming (por qué no se cachea)
Un HIT a un request streaming requiere (a) bufferar el completion completo antes de responder (mata la primera-latencia de token, el beneficio central del streaming) o (b) re-play de chunks almacenados (complejidad de timing/backpressure alta). **Ninguno justifica el beneficio** para el workload observado (~6 req/min). Los duplicados concurrentes streaming los cubre el single-flight (misma respuesta, un solo upstream, streaming real para todos los que esperan).

## 5. Expectativa de beneficio (estimación, NO benchmark)

- **Exact-match persistente:** hit rate esperado <1% del tráfico (prompts agénticos mutan por turno; duplicados espaciados son raros). Ahorro marginal; valor principal = idempotencia de retries.
- **Single-flight:** ataca el caso real (retries/paralelos), costo ~cero, sin riesgo.
- **El ahorro dominante ya está capturado:** 97.8% hit rate de providers (verificado).

⚠️ Estas son estimaciones propias basadas en la telemetría disponible (2,802 requests), no benchmarks medidos. Si se implementa, la fase de validación (feature + ventana de observación) dirá la verdad — el diseño lo permite medir (contadores separados).

## 6. Recomendación (para el task_plan / Pablo)

1. **NO** construir semantic cache. Documentar como anti-scope permanente.
2. **SÍ** single-flight (P0, ~1 feature chica, riesgo ~nulo) si el operador quiere eficiencia real de gateway.
3. **SÍ** response-cache exact-match (P1, opt-in, OFF default) si se quiere idempotencia de retries — con headers de transparencia.
4. Cierre formal del epic al implementar 1 y/o 2 + actualizar TECHDEBT #8.

## 7. Historial

- **09 Ago 05:5x** — Creación (S37 Apuesta A, ciclo 1: investigación + diseño). Fuentes verificadas en §3.
