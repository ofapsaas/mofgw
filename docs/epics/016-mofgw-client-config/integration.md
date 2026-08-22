# Integración cross-feature — Epic 016-mofgw-client-config

> E3 · Integración verificada 2026-08-22 · Todas las gates E2→E3 cumplidas (4/4 features done, cada Memory Bank commiteado, ninguno in-progress/blocked).

## Flujo E2E cross-feature verificado

`GET /v1/client-config?client=<id>` con los **3 adapters registrados en producción** (wiring `a8af57e`) devuelve fragmentos reales y fieles al catálogo:

| Cliente | Fragmento | Assert clave |
|---|---|---|
| **opencode** | top-level `mofgw` | `options.baseURL == base_url`; `options.apiKey == "{env:MOFGW_KEY}"`; `npm == "@ai-sdk/openai-compatible"`; `models["m"].limit.context == ContextWindow(1000000)` |
| **openclaw** | top-level `models` (+ `mode=="merge"`) | `providers.mofgw.baseUrl == base_url`; `apiKey == "${MOFGW_KEY}"`; `api == "openai-completions"`; `models[]` contiene `m` con `contextWindow == 1000000` |
| **zot** | top-level `providers` | `providers.mofgw.baseUrl == base_url`; **sin** `apiKey`/`${`/`{env:` en el body (I1 dev); `api == "openai"`; `models[]` contiene `m` con `contextWindow == 1000000` |
| **regresión /v1/models** | — | sigue 200 `object=="list"` listando `m` (catálogo intacto) |
| **cliente desconocido** | — | 404 `not_found_error` con la lista de los 3 ids soportados |

Test: `internal/proxy/e2e_clientconfig_integration_test.go` → `TestE2E_ClientConfigIntegration` (5 subtests: opencode / openclaw / zot / no_regression_models / unknown_client_404). Registra los adapters reales con scoped cleanup (solo esos 3 ids), harness con `SetModelMetadata`+`SetPricing`+`SetClientConfig`.

## Evidencia

```
$ go test ./internal/proxy/ -run TestE2E_ClientConfigIntegration -count=1 -v
--- PASS: TestE2E_ClientConfigIntegration (5 subtests all PASS)

$ go test ./... -count=1
Go test: 697 passed in 27 packages
```

Suite completa **697 tests `-race` en 27 paquetes** verde (691 features + 6 tests de integración E2E). Cero regresión.

## Gate E3→E4

- [x] Tests E2E cross-feature existen y pasan (`TestE2E_ClientConfigIntegration`).
- [x] Suite completa verde (697/27).
- [x] Los 3 adapters registrados en producción (`cmd/mofgw/main.go` wiring `a8af57e`).
- [x] Catálogo /v1/models sin regresión (verificado en el mismo E2E).

---