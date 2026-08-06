# Spec — 003-001-provider-cache: instrumentar cache de providers (hit/miss)

---
feature_id: 003-001-provider-cache
epic: mofgw-003-eficiencia
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 001-001-endpoint (proxy HTTP), 001-003-fallback (routing), 005-001-metrics, 005-002-logging
paralelizable: no
---

## Contexto

El cache de prompt/contexto de los providers **ya está activo en todos los
upstreams** (verificado en `docs/research-token-efficiency.md` §2): DeepSeek
automático, Alibaba implícito+explícito, MiniMax/GLM/Kimi automáticos. El
gateway opencode.ai zen (providers provider-a/b/c/d) ya normaliza los campos
de cache a formato OpenAI estándar (`usage.prompt_tokens_details.cached_tokens`)
y ya inyecta `stream_options.include_usage=true` en streaming. El único
provider que requiere acción nuestra es Alibaba bailian directo (provider
qwen), que NO inyecta `include_usage` — hay que agregarlo desde mofgw.

**Conclusión del discovery: no hay nada que "habilitar" en los providers. El
trabajo real es instrumentar la lectura del usage que ya llega**, para que
mofgw (a) sepa cuánto cache hit/miss hay por request, (b) lo exponga en logs
estructurados y /metrics, y (c) no pierda el dato en streaming (donde solo
viene en el chunk final si `include_usage` está presente).

Estado actual del código (verificado):
- `internal/provider/provider.go`: `ChatResponse.Usage` captura solo
  `prompt_tokens`, `completion_tokens`, `total_tokens`. NO captura
  `prompt_tokens_details.cached_tokens`, `cache_creation_input_tokens` ni
  `completion_tokens_details.reasoning_tokens`.
- `internal/stream/stream.go` `Copy()`: passthrough SSE que reescribe
  model/id pero preserva el payload completo (incluido `usage` del chunk
  final si upstream lo manda).
- `internal/router/router.go`: ata `res.ProviderID` a cada resultado — punto
  natural de instrumentación.
- `internal/metrics/metrics.go`: contadores atómicos sin accounting de
  tokens.

## Postcondiciones

1. **P1 — Parseo de campos de cache:** todo response no-streaming parseado
   por `provider.Complete()` expone en su `Usage` los campos
   `cached_tokens` (de `prompt_tokens_details.cached_tokens`), `cache_creation_tokens`
   (de `prompt_tokens_details.cache_creation_input_tokens`) y
   `reasoning_tokens` (de `completion_tokens_details.reasoning_tokens`),
   con valor 0 cuando el upstream no los reporta. El parseo es tolerante:
   campos ausentes, null o no numéricos → 0, sin error.

2. **P2 — Streaming con include_usage garantizado:** todo request streaming
   que mofgw envía a un upstream incluye `stream_options.include_usage=true`
   cuando el cliente no lo especificó. Si el cliente ya lo mandó, se respeta
   (sin duplicación). El chunk final SSE con `usage` se pasa al cliente
   intacto (sin alteración de los campos de cache).

3. **P3 — Métricas de cache:** /metrics expone contadores por
   provider+modelo: `mofgw_cache_hit_tokens_total`, `mofgw_cache_miss_tokens_total`
   y `mofgw_reasoning_tokens_total`, acumulados por request (no-stream y
   stream). Un request sin campos de cache suma 0 (no crashea). La cardinalidad
   queda acotada por providers configurados × modelos servidos.

4. **P4 — Logs estructurados:** cada request (no-stream y stream) emite un
   evento de log con los campos `cache_hit_tokens`, `cache_miss_tokens`,
   `reasoning_tokens` (además de los ya existentes prompt/completion/total),
   por provider y modelo. Ausencia de usage → 0s, sin error.

5. **P5 — Transparencia preservada:** el body del request hacia upstream
   solo se modifica para (a) clamp de max_tokens (comportamiento existente) y
   (b) inyección de `stream_options.include_usage=true` en streaming. Ningún
   contenido del prompt, tools ni parámetros del cliente se altera.

6. **P6 — Robustez ante usage ausente:** si un upstream responde sin objeto
   `usage` (caso documentado: bug zen #14795 puede devolver usage undefined),
   mofgw responde normal al cliente, registra los campos como 0 y no genera
   error interno.

## Invariantes

- I1: El proxy sigue siendo transparente: el cliente nunca ve error de
  provider por problemas de instrumentación de cache.
- I2: Los campos de cache son informativos — mofgw no altera el comportamiento
  de cacheado del upstream (no inyecta ni remueve `cache_control`).
- I3: Los contadores de /metrics mantienen el estilo existente: atómicos, sin
  dependencias externas, serialización Prometheus text (EPIC-005 001-001).
- I4: La suite completa pasa con `go test -race` (cero red externa — providers
  fake en httptest).

## Criterios de aceptación

- C1: Test P1: response no-stream con `prompt_tokens_details.cached_tokens:
  100` → `Usage.CachedTokens == 100`; sin el campo → 0; `cached_tokens: "x"`
  (no numérico) → 0 sin error.
- C2: Test P2: request stream sin `stream_options` → el body que recibe el
  upstream tiene `stream_options.include_usage=true`; request con
  `include_usage:false` explícito → se respeta (no se pisa).
- C3: Test P3: 3 requests (2 hit, 1 miss) al mismo provider+modelo →
  contador `mofgw_cache_hit_tokens_total` = suma de hits, miss = suma de
  misses.
- C4: Test P4: request con usage completo → log contiene los campos
  cache_hit/cache_miss/reasoning con los valores correctos.
- C5: Test P6: upstream responde 200 sin `usage` → cliente recibe la respuesta
  normal, no hay 500, logs con 0s.
- C6: Suite completa `go test ./... -race` verde, cero red externa.

## Fuera de alcance

- Semantic cache propio en mofgw (cache de respuestas completas) — evaluación
  futura.
- Inyección de `cache_control` markers (cache explícito Alibaba) — el
  implícito ya cubre; se evaluará si los datos de hit rate lo justifican.
- Tabla de costos y accounting monetario — feature 006-002 (EPIC-006).
- Exposición de capabilities en /v1/models — feature 007-001/007-002 (EPIC-007).
- Normalización de campos DeepSeek directos (`prompt_cache_hit_tokens`) —
  irrelevante hoy (vamos vía Zen); se cubre si algún día apuntamos directo.
