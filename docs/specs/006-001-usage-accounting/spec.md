# Spec — 006-001-usage-accounting: contabilizar tokens por cliente

---
feature_id: 006-001-usage-accounting
epic: mofgw-006-observabilidad
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 001-007-auth (clientID en contexto), 003-001-provider-cache (Usage con cached/reasoning, patrón IncCacheTokens)
paralelizable: no
---

## Contexto

EPIC-006 es la base de la optimización de tokens: **sin medición no hay
optimización**. La feature 003-001 ya instrumentó los campos de cache del
usage upstream (cached_tokens, reasoning_tokens) y los acumula por
provider+modelo en /metrics. Lo que falta para cerrar el accounting es:

1. Contabilizar **todos** los tokens (prompt, completion, total) por request,
   no solo los de cache.
2. Agregarlos por **cliente autenticado** (el clientID ya está en el context
   vía auth — `auth.ClientIDFrom(ctx)` en proxy.go:138), además de
   provider+modelo. Esto habilita la feature 006-002 (costos por cliente) y
   el EPIC-008 (budget por cliente).

Estado actual del código (verificado):
- `internal/provider/provider.go`: `Usage` expone PromptTokens, CompletionTokens,
  TotalTokens, CachedTokens, CacheCreationTokens, ReasoningTokens (parseo
  tolerante de 003-001).
- `internal/metrics/metrics.go`: `IncCacheTokens(provider, model, hit, miss,
  reasoning)` acumula SOLO los campos de cache, en un mapa keyed
  `provider|model`. No hay contador de prompt/completion/total, ni dimensión
  de cliente.
- `internal/proxy/proxy.go`: `recordCacheTokens(providerID, model, u)` llama a
  IncCacheTokens; `clientID := auth.ClientIDFrom(r.Context())` ya está
  disponible en handleChat (línea 138) y en el request_end (emitRequestEnd
  recibe el logger con el contexto).

## Postcondiciones

1. **P1 — Accounting de tokens por request:** todo request atendido (no-stream
   y stream, exitoso o con error de provider absorbido) acumula en /metrics
   los tokens `prompt_tokens`, `completion_tokens`, `total_tokens` del usage
   upstream. Un request sin usage (o con error antes del primer byte)
   contribuye 0 (no crashea). La acumulación incluye los campos de cache ya
   existentes (no se pierde lo de 003-001).

2. **P2 — Dimensión de cliente:** los contadores se acumulan por
   `client|provider|model` además de por `provider|model`. El clientID sale
   del context autenticado (`auth.ClientIDFrom`). Un clientID vacío (request
   sin auth — no debería pasar, pero robustez) cae en el label `unknown`.

3. **P3 — Exposición en /metrics:** /metrics expone contadores acumulados de
   prompt/completion/total por cliente (formato Prometheus-style, mismo
   estilo que 003-001: totales sin labels + desagregado con labels). La
   cardinalidad queda acotada por clientes configurados × providers ×
   modelos.

4. **P4 — Logs request_end:** el evento request_end (005-005) incluye
   `prompt_tokens`, `completion_tokens`, `total_tokens` además de los campos
   de cache ya agregados en 003-001. Ausencia de usage → 0s, sin error.

5. **P5 — No rompe 003-001:** los contadores de cache existentes
   (`mofgw_cache_hit_tokens_total` etc.) y su desagregado por provider+modelo
   se mantienen exactamente como están (compatibilidad de contrato).

6. **P6 — Privacidad:** los labels de cliente usan el clientID (ya hasheado
   en disco, 001-007) — nunca contenido de prompt/respuesta.

## Invariantes

- I1: El accounting es informativo: nunca bloquea ni altera el request (sin
  side-effects en el flujo de respuesta).
- I2: Contadores atómicos con mutex (patrón existente metrics.go), cero
  dependencias externas, serialización Prometheus text.
- I3: Suite completa `go test ./... -race` verde (cero red externa — providers
  fake en httptest).
- I4: La cardinalidad de los labels queda acotada (clientes × providers ×
  modelos configurados) — sin dimensiones de alto cardinality (nada de
  request-id, timestamps, etc.).

## Criterios de aceptación

- C1: 2 requests del mismo cliente al mismo provider+modelo con usage
  `prompt=100, completion=50` → contador de prompt para ese
  client|provider|model = 200, completion = 100, total = 300.
- C2: request sin usage → contadores contribuyen 0 (se exponen en 0, no
  crashean).
- C3: 2 clientes distintos (claves distintas) al mismo modelo → agregados
  separados por cliente (cada cliente ve/consume su propio acumulado).
- C4: /metrics incluye las líneas de 003-001 sin cambios (cache_hit_total,
  cache_miss_total, reasoning_total y el desagregado mofgw_cache_tokens).
- C5: request_end loguea prompt/completion/total.
- C6: Suite completa verde con -race, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/metrics/metrics.go`: extender el mapa de tokens con las
  dimensiones de cliente; agregar contadores prompt/completion/total por
  `client|provider|model` (y totales agregados sin labels). Mantener la
  firma `IncCacheTokens` existente o reemplazarla por un método más general
  `IncUsage(client, provider, model string, u *provider.Usage)` que acumule
  todo (cache + prompt/completion/total) en un solo punto.
- `internal/proxy/proxy.go`: en handleChat, pasar `clientID` a
  `recordCacheTokens` (hoy solo recibe providerID/model) → generalizar a
  `recordUsage(clientID, providerID, model, u)`.
- `internal/proxy/proxy.go emitRequestEnd`: agregar campos
  prompt/completion/total (con el mismo helper de 0-tolerancia de 003-001).
- Tests: extender e2e_003001_test.go con tests de agregación por cliente y
  totales; ajustar TestNamesSorted (nuevos contadores) con justificación en
  spec.

## Fuera de alcance

- Costos monetarios (tabla de precios + $/request) — feature 006-002.
- Persistencia de agregados entre restarts — el accounting es en memoria
  (patrón actual); persistencia se evalúa con 006-002 si los costos lo
  justifican.
- Exposición de usage a clientes (headers/endpoint) — feature 007-003.
- Budget/límites por cliente — EPIC-008.

## Cambios de tests justificados

- `metrics_test.go TestNamesSorted`: expectativa 8 → 11 contadores base. El
  contrato de /metrics cambió con P3 (totales prompt/completion/total se
  emiten siempre, en 0 si no hubo datos). Justificado por P3.
