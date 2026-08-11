# Test Audit — 010-002-request-path-fiel

Status: Approved by Ofap (usuario delegado HITL — GVR) on 2026-08-11

### Resumen del comportamiento que cambia

La feature 010-002 (spec draft, artifact `printed-lavender-bee.md`) cambia tres comportamientos del request path, todos observables por el body que recibe un upstream fake, el status HTTP o el conteo de intentos:

1. **Clamp por modelo pre-routing (P1/P2):** `max_tokens`/`max_completion_tokens` del cliente > `model_metadata.<model>.max_output` se reescriben a `max_output` antes del routing. Efectivo = `min(max_output_modelo, MaxTokens_provider)` (convive con 001-004; un provider sin límite no relaja el clamp por modelo).
2. **Window-check fiel (P3):** el rechazo 008-001 pasa a usar el max_tokens **EFECTIVO** post-clamp por modelo: `promptTokens + min(client_max_tokens, max_output_modelo) > context_window × (1+margin)`. Sin metadata (o `max_output == 0`) → raw (comportamiento actual intacto, TECHDEBT #23 no se reabre).
3. **Inyección per-attempt provider-aware (P4-P7):** modelo con `thinking_default` no vacío y par (path, modelo) con parámetro de activación verificado (§5.4) → cada intento de la cadena inyecta el parámetro con valor `EXACTAMENTE thinking_default`, SOLO si el body del cliente no trae ninguno de `{reasoning_effort, thinking, enable_thinking}`. kimi-k2.7-code NUNCA inyecta; minimax-m3 no inyecta (nativo adaptive == prescriptivo). Stream y no-stream cubiertos; `stream_options.include_usage` (003-001) intacto.

**Diseño ADOPTADO (inline contract):** clamp en proxy (donde vive `modelMeta`), window-check con valor efectivo, inyección en router vía knob declarativo `providers[].thinking_path` (`""` | `"zen"` | `"bailian"`, default `""` = sin inyección).

### Harness helpers — verificación (leídos solo de `*_test.go`)

| Helper                                                                                 | Disponible                                              | Evidencia                                |
| -------------------------------------------------------------------------------------- | ------------------------------------------------------- | ---------------------------------------- |
| `upstream.gotBody` / `gotHeaders`                                                          | ✅                                                      | `e2e_test.go:35-36,46-47`                  |
| `h.proxySrv.SetModelMetadata(map[string]config.ModelMetadata)`                           | ✅                                                      | `e2e_008001_test.go:39,68,108`             |
| `h.proxySrv.SetContextMargin(float64)`                                                   | ✅                                                      | `e2e_008001_test.go:107,128,153`           |
| `buildMultiClient(t, ups, keys...)` + `h.chatAs(t, key, body)`                             | ✅                                                      | `e2e_006001_test.go:37,65`                 |
| `build` / `buildWithLogger` / `buildWithOptions` (cadenas, cooldown 0, maxRetries 2)         | ✅                                                      | `e2e_test.go:91,98,121`                    |
| Fakes `upstreamOK`, `upstreamStreamOK`, `upstreamFail(status)`, `bigBody(n)`                   | ✅                                                      | `e2e_test.go:67-77`, `e2e_008001_test.go:26` |
| `provider.NewClient(id, url, key, models, maxTokens, nil)` — ID y MaxTokens controlables | ✅ (pero los builders estándar hardcodean `up1`/`"m"`/`4096`) | `e2e_test.go:106`                          |
| `router.New(specs, 2, 0, 0, 30s, nil)` — cadena de fallback = orden de `ups`               | ✅                                                      | `e2e_test.go:110`                          |
| Knob per-provider de path (`thinking_path`) / MaxTokens por provider en harness          | ❌ **NO existe**                                            | —                                        |

**Gap de interfaz (único):** para los tests de inyección (C2/C3/C7/C9a/C10) el router necesita saber el path de cada provider por intento. Hoy no hay knob en el código (verificación pendiente del spec §"Verificaciones pendientes"). Para mantener **"compilable contra código actual; RED por aserción"**, el test fija el path **por construcción** usando el ID del provider (`provider.NewClient("zen", ...)` / `"bailian"`), que es exactamente el set de valores del knob ADOPTADO (`""|"zen"|"bailian"`). En prod el path viene de `providers[].thinking_path`; en el harness, el ID del provider ES el label declarativo. Si el usuario/orquestador prefiere un campo `ProviderSpec.ThinkingPath`, el cambio es **un solo punto**: el helper local `buildRP` del archivo nuevo (documentado en el header del test). Esto se reporta como riesgo de interfaz, no bloquea el contrato observable.

### Tests existentes a MODIFICAR (con justificación y ref al spec)

| Test                             | Archivo                               | Comportamiento que cambia                                                                                                                                                                                                                                           | Justificación (spec)                                                                                                                                                                                                                                                                                                                                                                                            | Acción                                                                                                                                                             |
| -------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `TestRED_MaxTokensCuentaEnVentana` | `internal/proxy/e2e_008001_test.go:103` | Hoy: `window 1000, MaxOutput 100, max_tokens 400, margin 0, prompt ~750` → espera **400** + 0 intentos. Con la regla fiel: `750 + min(400,100) = 850 ≤ 1000` → **200** y upstream recibe `max_tokens == 100`. El test queda rojo bajo la implementación nueva si no se actualiza. | Spec draft: P3 (artifact líneas ~179-188: "modelo context_window: 1000, max_output: 100, margin 0, prompt ~750 tokens, max_tokens: 400 → 200 (750+100 = 850 ≤ 1000), y el upstream recibe max_tokens == 100") + sección "Impacto cross-feature verificado — e2e_008001" (líneas ~95-102: "DEBE actualizarse; su premisa — el max_tokens crudo cuenta contra la ventana — queda reemplazada por la regla fiel"). | **MODIFICAR (mandatorio)** en POST-AUDIT: expectativa 400 → 200 + assert `upstream.gotBody.max_tokens == 100`.                                                           |
| `TestRED_MaxTokensQueCabe`         | `internal/proxy/e2e_008001_test.go:125` | El wire value cambia: upstream recibe `max_tokens == 100` (clamp por modelo) en vez de `200`. El test hoy solo aserta status 200 + `gotBody != ""` → **sigue verde**; no observa el valor.                                                                                    | Spec draft líneas ~100-102: "TestRED_MaxTokensQueCabe sigue 200 (el upstream ahora recibe max_tokens: 100 en vez de 200)".                                                                                                                                                                                                                                                                                      | **HARDENING OPCIONAL** en POST-AUDIT: agregar `wantBodyMaxTokens(t, ups.gotBody, 100)` para fijar el contrato nuevo. No es obligatorio (sus asserts actuales no fallan). |

Ningún otro test existente usa `max_tokens` con metadata que se vea afectado (verificado: `TestE2EClampMaxTokens` de e2e_test.go:338 es SIN metadata → clamp por provider intacto, no cambia).

### Tests nuevos a escribir (archivo `internal/proxy/e2e_010002_requestpath_test.go` — el nombre `e2e_010002_test.go` está OCUPADO por response-cache)

| Criterio | Postcondición                       | Test propuesto                                         | RED hoy                                                              |
| -------- | ----------------------------------- | ------------------------------------------------------ | -------------------------------------------------------------------- |
| C1       | P1 (clamp kimi)                     | `TestE2E010002_C1_KimiClampPorModelo`                    | ✅ `max_tokens = 65536, want 32768`                                    |
| C2       | P4a (flash/zen)                     | `TestE2E010002_C2_FlashZenInyectaLow`                    | ✅ falta `reasoning_effort`                                            |
| C3       | P4b (flash/bailian)                 | `TestE2E010002_C3_FlashBailianInyectaLowYEnable`         | ✅ faltan ambos campos                                               |
| C4       | P5 (respeto explícito)              | `TestE2E010002_C4_EffortExplicitoSinOverride`            | guard (verde hoy)                                                    |
| C5       | P6a (kimi nunca)                    | `TestE2E010002_C5_KimiSinInyeccion`                      | guard (verde hoy)                                                    |
| C6       | P6b (minimax nunca)                 | `TestE2E010002_C6_MinimaxSinInyeccion`                   | guard (verde hoy)                                                    |
| C7       | P4c (qwen/bailian)                  | `TestE2E010002_C7_QwenBailianEnableThinking`             | ✅ falta `enable_thinking`                                             |
| C8       | P3 (window fiel)                    | `TestE2E010002_C8_WindowFiel` (3 subtests)               | (a) ✅ 400→200; (b),(c) guards                                       |
| C9       | P7 (stream)                         | `TestE2E010002_C9_StreamCubierto` (2 subtests)           | (a) ✅ falta `reasoning_effort`; (b) ✅ `max_tokens = 65536, want 32768` |
| C10      | P4+P9 (cadena 429)                  | `TestE2E010002_C10_CadenaZenBailian`                     | ✅ faltan campos en ambos intentos                                   |
| C11      | P9 (400 no-reintentable)            | `TestE2E010002_C11_400NoReintenta`                       | guard (verde hoy)                                                    |
| C12      | P2+P8 (sin metadata / min efectivo) | `TestE2E010002_C12_SinMetadataYMinEfectivo` (4 subtests) | guards (verde hoy)                                                   |

Coverage por postcondición: P1→C1/C9b; P2→C1/C12c/C12d; P3→C8; P4→C2/C3/C7/C10; P5→C4; P6→C5/C6; P7→C9; P8→C8c/C12a/C12b; P9→C10/C11. **Todas cubiertas.**

### Tests untouched (lista explícita)

- **008-001** `internal/proxy/e2e_008001_test.go`: `TestRED_RechazoPorVentana`, `TestRED_RequestQueCabe`, `TestRED_SinMetadataSinRechazo`, `TestRED_MargenRespetado` — no usan `max_tokens` con metadata afectable; la regla fiel no cambia su resultado (prompt sin max_tokens → efectivo = crudo = 0/ausente). Spec draft líneas ~102: "Los demás tests de 008-001 no usan max_tokens → no cambian".
- **010-002-response-cache** `internal/proxy/e2e_010002_test.go`: TODO el archivo (327 líneas, `TestE2E010002_*` de X-Mofgw-Cache). Feature distinta con numeración previa; cache keyea el body crudo del cliente y el clamp no altera ese contrato (spec §Cambios: "el cache exact-match sigue keyeando sobre el body crudo"). **NO TOCAR** — por eso el archivo nuevo es `e2e_010002_requestpath_test.go`.
- **e2e_test.go** (`TestE2EClampMaxTokens`, `TestE2EPreservesExtraFields`, `TestE2EFallbackTransparent`, `TestE2EAllProvidersFail502`, `TestE2EStreamingFallback`, resto) — sin metadata → comportamiento 001-004/001-003 intacto.
- **e2e_003001_test.go** (include_usage) — sin metadata → inyección de usage intacta (P7 la exige).
- **e2e_006001/007002/008002/008003/009000/009001/009002/009000/limiter**, **router_test.go**, **router_sticky_test.go**, **provider_test.go**, **clamp_test.go**, **config_test.go** — no ejercen metadata en el request path ni el window check; no cambian. (Cuidado: `router_sticky_test.go` es "RED por compilación" y ya está en verde con APIs de 009-002 — sin relación.)

### Evaluación de riesgo de regresión

- **ALTO (mitigado):** el flip 400→200 de `TestRED_MaxTokensCuentaEnVentana` deja roja la suite 008-001 si no se actualiza en POST-AUDIT antes del GREEN. Es el único test existente que cambia de contrato; ya identificado y justificado arriba.
- **MEDIO:** si `provider.NewClient(..., 0, nil)` defaulteara `MaxTokens` a 4096 (no verificable desde tests; `provider_test.go:238` solo prueba 4096 explícito), los subtests C12(b)/(d) "provider sin límite" fallarían por motivo ajeno al spec. El orquestador debe confirmar la semántica de 0 en el client real al materializar (POST-AUDIT ajusta si hace falta).
- **BAJO:** la inyección agrega campos por intento en el router; sin metadata (nil-safe P8) no toca nada → router_test.go y el resto de la suite no se ven afectados. El clamp por modelo corre en el proxy donde ya vive `modelMeta`; el cache 010-002 keyea raw → sin impacto.
- **BAJO:** la coexistencia con 001-004 (efectivo = min) está guardada por C12(c)/(d) y `TestE2EClampMaxTokens`.

### Gate checklist (3.0 → 3.1)

- [x] Test Audit producido; modificación existente justificada por spec (P3 + sección cross-feature del artifact).
- [x] Toda postcondición P1-P9 tiene al menos un test (mapeo arriba).
- [x] Ningún test depende de estructura interna (solo `gotBody`, status, conteo de intentos — contrato observable).
- [x] Tests RED nuevos fallan por AssertionError (campos ausentes/incorrectos), no por compilación — ver sección 3.
- [x] Colisión de nombre resuelta: archivo nuevo `e2e_010002_requestpath_test.go`; `e2e_010002_test.go` untouched.
- [ ] **Pendiente:** aprobación del usuario del audit antes de pasar a RED/POST-AUDIT.
