# Test Audit — 016-001-config-renderer-core

**Rol:** test-writer, AUDIT mode (pre-RED) · **Feature:** `016-001-config-renderer-core` · **Proyecto:** mofgw · **Fecha audit:** 2026-08-22

## 1. Baseline green evidence

```
$ go test ./internal/proxy/... ./internal/config/... -count=1
Go test: 330 passed in 2 packages
```

`go` es ejecutable en el entorno; baseline **verde** antes de cualquier cambio de la feature.

## 2. Impact assessment — impacto sobre la suite existente

La feature es **puramente aditiva** en los tres frentes (ruta /v1/client-config, paquete internal/clientconfig, knob client_config). Ningún test existente requiere modificación.

- `Handler()` (~361-373): registra una ruta nueva `GET /v1/client-config`, sin colisión (patrón único, method-based).
- `publicPrefixes`: la ruta nueva NO entra; queda autenticada por defecto (no público → `Authenticate` → 401).
- Tests e2e de /v1/models, /v1/usage, /v1/context: inmutables (shapes no cambian; `handleModels` se reusa).
- `internal/config`: campo aditivo `ClientConfigConfig` con zero-value; ningún test hace DeepEqual de todo el Config.
- Knob nuevo ausente/vacío = off-safe válido (I3/I4).

**Bandera de nombres (advertencia GREEN):** `internal/config` ya tiene struct `ClientConfig` (cliente autenticado 001-007, L182). El knob nuevo debe llamarse **`ClientConfigConfig`**, NO `ClientConfig`. Usar el nombre exacto del spec para no compilar contra el struct equivocado.

**¿Genuinamente aditiva?** Sí. No existe ruta `GET /v1/client-config` hoy; ningún test la golpea; no se muta `publicPrefixes`; no se altera ruta existente. Auth automática por estar fuera de prefijos públicos.

## 3. Test ↔ postcondición mapping (feature NEW)

Todas las P son observables y testeables. Ninguna intraable.

| P | Assert (1 línea) | Ubicación esperada | Observable |
|---|---|---|---|
| P1 | sin Bearer → 401 + envelope `{"error":{"type":"invalid_api_key"}}` | internal/proxy (e2e) | ✔ |
| P2 | client ausente/vacío → 400 invalid_request_error "missing required query param: client" | internal/proxy | ✔ |
| P3 | client=noexiste + base_url seteado → 404 not_found_error, mensaje contiene "supported:" + lista del registry | internal/proxy + internal/clientconfig | ✔ |
| P4 | client válido pero base_url vacío → 503 server_error "client_config.base_url not set" (aun con renderer registrado) | internal/proxy | ✔ |
| P5 | Register("test",r) + base_url seteado → GET ?client=test → 200, body == bytes r.Render(ir) | internal/proxy (e2e, vía API pública) | ✔ |
| P6 | renderer captura ir; ir.Models == iteración deduplicada handleModels (misma lista+metadata que /v1/models; modelos sin metadata aparecen; mutar catálogo cambia IR) | internal/proxy (e2e comparando con /v1/models) | ✔ |
| P7 | ir.BaseURL == client_config.base_url; ir.KeyEnvRef == client_config.key_env (default "MOFGW_KEY"); cambiar knob cambia IR | internal/proxy + internal/config | ✔ |
| P8 | en 200/400/404/503 el body NUNCA contiene el secret; solo KeyEnvRef (grep negativo) | internal/proxy + internal/clientconfig | ✔ |
| P9 | errores usan envelope openAIError; suite /v1/* verde e intacta; /v1/models sin cambio de shape/cardinalidad | internal/proxy | ✔ |
| P10 | Register/SupportedList/Lookup operan sobre el mapa exportado Renderers; registrar/consultar es API pública | internal/clientconfig (unit) | ✔ |

## 4. Pre-existing tests que engañarán tras esta feature

**Ninguno.** No existe test verde que aserte el set completo de rutas ni que golpee `GET /v1/client-config` esperando 404 o enumerando `/v1/*`.

Único cuidado propio de RED: si se copia el patrón de features previas (011-001/011-006) de aserción "404 por ruta ausente", esos asserts RED son válidos SOLO en estado RED y deben transformarse a asserts de contrato real (P1..P5) en GREEN. No deben quedar asserts que esperen 404 para una ruta que tras GREEN devuelve 400/503.

**Conclusión:** feature aditiva sin regresión prevista; baseline verde (330 pass); tests P1-P10 en `internal/proxy` (e2e HTTP real + API pública `clientconfig.Register`) y `internal/clientconfig` (unit registry), defaults del knob en `internal/config`. Nombre exacto `ClientConfigConfig`.