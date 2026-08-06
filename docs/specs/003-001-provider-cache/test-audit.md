# Test Audit — 003-001-provider-cache

> Auditoría de suite existente vs postcondiciones del spec (P1-P6).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline por el orquestador
> (opción 3 del contrato §4 — mecanismo de delegación caído; trade-off declarado:
> riesgo de sesgo mínimo porque el AUDIT es read-only y la feature no tiene
> implementación aún).

## Mapeo suite existente → postcondiciones

| Test existente | Qué verifica hoy | Cubre postcondición | Gaps |
|---|---|---|---|
| `provider_test.go:53 TestCompleteOK` | Complete parsea content + `Usage.TotalTokens` (L65-67); body crudo preservado (L69-71) | Parcial P1 (solo total_tokens) | No cached_tokens, no reasoning_tokens, no cache_creation_tokens |
| `provider_test.go:160 TestRawBodyPreserved` | Body con `tools`, `top_p`, `stream_options.include_usage` llega **intacto** al upstream (L173-179) | **P5** (transparencia) + parcial P2 (include_usage del cliente se respeta) | No cubre la INYECCIÓN de include_usage cuando el cliente no lo manda (P2) |
| `provider_test.go:123 TestStreamOK` | Stream SSE: eventos + `[DONE]` final (L140-142) | Parcial P2 (passthrough stream) | No cubre chunk final con `usage` + cached_tokens (P1 streaming) |
| `provider_test.go:74/92/107` (500/400/network) | ErrUpstream normalizado, sin filtrar keys | — | — |
| `metrics_test.go:12 TestCountersAndRender` | Contadores atómicos render Prometheus: requests/streams/errors/failovers/cooldown + last_provider (5 contadores) | Patrón P3 | No hay contadores de tokens (cache_hit/miss/reasoning) |
| `metrics_test.go:34 TestNamesSorted` | `Names()` ordenado | Patrón P3 | — |
| `e2e_test.go:TestE2EMetricsNoAuth` | GET /metrics sin auth → 200 con `mofgw_requests_total` | Patrón P3 (endpoint) | No verifica contadores de tokens |
| `e2e_test.go` harness (build/buildWithOptions) | Providers fake httptest, router, proxy completo | Infraestructura para C1-C6 | — |

## Gap analysis (cobertura faltante → tests RED)

| Postcondición | Cobertura actual | Test RED necesario |
|---|---|---|
| **P1** Parseo cached_tokens/cache_creation/reasoning (no-stream) | ❌ Ninguna | Complete con `prompt_tokens_details.cached_tokens`, `cache_creation_input_tokens`, `completion_tokens_details.reasoning_tokens` → campos parseados; ausentes → 0; no-numérico → 0 sin error |
| **P1** Parseo en streaming (chunk final con usage) | ❌ Ninguna | Stream con chunk final `usage.prompt_tokens_details.cached_tokens` → capturado |
| **P2** Inyección include_usage cuando el cliente no lo manda | ❌ Ninguna | Request stream sin stream_options → el body upstream tiene `stream_options.include_usage=true` |
| **P2** Respetar include_usage explícito del cliente (false) | Parcial (TestRawBodyPreserved: passthrough) | Request con `include_usage:false` → NO se pisa |
| **P3** Contadores de tokens en /metrics | ❌ Ninguna | 2 hit + 1 miss mismo provider+modelo → `mofgw_cache_hit_tokens_total`/`mofgw_cache_miss_tokens_total` correctos; reasoning acumulado |
| **P4** Logs estructurados con campos cache | ❌ Ninguna | Request con usage completo → evento de log con cache_hit/cache_miss/reasoning |
| **P5** Transparencia (sin alterar prompt/tools) | Parcial (TestRawBodyPreserved) | Reafirmar con la inyección de include_usage: tools/top_p siguen intactos |
| **P6** Robustez ante usage ausente | ❌ Ninguna | Upstream 200 sin `usage` → response normal, sin 500, campos 0 |

## Convenciones a seguir en RED (del patrón existente)

1. **Fake upstream en httptest** (`provider_test.go:19 fakeUpstream`, `e2e_test.go:66 upstream{status, body}`) — cero red externa.
2. **Provider fake** `NewClient("fake1", base, "sk-upstream", []string{...}, 4096, nil)` — inyectar base URL del httptest.
3. **Body de request** via `json.Marshal(map[string]any{...})` (`chatBody`, L45-51) o raw string con campos extra.
4. **Verificación de body upstream**: handler que captura `io.ReadAll(r.Body)` y lo inspecciona (patrón `TestRawBodyPreserved` L163-169) — clave para P2.
5. **Harness E2E** (`e2e_test.go build/buildWithOptions`) para tests que atraviesan proxy completo (P3 con /metrics, P6 con response normal).
6. **Assertions de metrics**: `strings.Contains(render, "mofgw_<name> <valor>")` (patrón `metrics_test.go:23-26`).
7. **Privacidad**: sanitize ya cubierto (TestSanitizeRedactsURLsAndKeys) — no repetir.

## Veredicto

P1 (parseo cache no-stream), P1-stream, P2 (inyección), P3 (contadores), P4 (logs), P6 (usage ausente) tienen **cero cobertura** → tests RED necesarios. P5 tiene cobertura parcial vía TestRawBodyPreserved → refuerzo en el test de inyección (asegurar tools/top_p intactos tras inyectar include_usage).
