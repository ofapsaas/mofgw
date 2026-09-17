# test-audit.md — Feature 020-001-registry-cost-model (AUDIT, sub-fase 3.0)

## 1. Resumen del comportamiento que cambia

La feature enriquece `TerminalEvent` del registro unificado (014-001, `internal/registry`) con tres campos aditivos:

- `model` (string) — el modelo solicitado por el cliente (P1).
- `cost_usd_src` (string) — proveniencia del costo: `"upstream"` | `"table"` | `"none"` (P2).
- `cost_usd_up` (`*float64`, nullable) — costo reportado por el upstream cuando existe, incluyendo `0.0` legítimo; `null` cuando no hay dato (P3).

Precedencia (P4/P5): `usage.cost` upstream exacto (sin re-cálculo, la tabla NO participa) > `estimateCost` con tabla `pricing:` (`src="table"`) > `cost_usd=0` + `src="none"` si el modelo no está en la tabla (dato faltante, NO costo cero). Compatibilidad de lectura: líneas JSONL históricas sin los campos nuevos parsean con `model=""`, `cost_usd_src=""`, `cost_usd_up=null` (P6). Privacidad intacta (P7, metadata-only). Captura de `usage.cost` sin alterar el contrato de respuesta al cliente: envelope upstream + headers `X-Usage-*` byte-idénticos (P8). Cero cambio en el contrato de respuesta.

**Respuesta a la pregunta formal del HITL (¿existe `estimateCost`?): SÍ, EXISTE.** Ubicación exacta: `func (s *Server) estimateCost(model string, miss, completion, hit int64) float64` en `internal/proxy/proxy.go:1016`. Fórmula vigente verificada en `proxy.go:1021-1023`: `float64(miss)/1e6*p.InputUSDPerM + float64(completion)/1e6*p.OutputUSDPerM + float64(hit)/1e6*p.CacheHitUSDPerM`. Llamadores vigentes: `proxy.go:876` (recordCacheTokens), `proxy.go:907`, `proxy.go:969`. **Conclusión: P5 referencia una fórmula vigente y verificada; el RED NO necesita definir `estimateCost` como contrato nuevo — el implementer DEBE reutilizar `s.estimateCost`, no duplicar la fórmula.**

## 2. Inventario del blast radius + veredictos por test existente

### 2.1 `internal/registry/registry_test.go` — 7 tests (suite verificada: 7 passed)

| Test | Qué valida | Veredicto | Justificación |
|---|---|---|---|
| `Test014001_P3_NewWriterFailFast` | fail-fast ante path no abrible | UNTOUCHED | No toca el esquema. |
| `Test014001_P3_AppendOnly` | append preservado tras restart | UNTOUCHED | I1 se mantiene (campos nuevos solo en líneas nuevas). |
| `Test014001_P5_AttemptSchemaExact` | set EXACTO de keys de attempt | UNTOUCHED | AttemptEvent YA tiene `model` y no se le agrega nada (P1-P3 son solo de TerminalEvent). |
| `Test014001_P6_TerminalSchemaExact` | set EXACTO de keys de terminal (`type,request_id,ts,client,outcome,error_code,status,final_provider,tokens,cost_usd,stream`) | **MODIFICAR** — única modificación de todo el audit | P1/P2/P3 agregan `model`, `cost_usd_src`, `cost_usd_up`. El helper exige set exacto → FALLARÁ en GREEN si no se actualiza. Cambio: extender `want` con las 3 keys nuevas. Justificación: P1 (spec:26), P2 (spec:28-30), P3 (spec:32-35). |
| `Test014001_P3_JSONLValid` | líneas parsean como JSON | UNTOUCHED | Insensible a keys nuevas. |
| `Test014001_P4_ThreadSafety` | concurrencia | UNTOUCHED | Cuenta líneas; insensible al esquema. |
| `Test014001_P16_FallibleWriteNoPanic` | write sobre fd cerrado | UNTOUCHED | Best-effort; P1-P3 no lo tocan. |

### 2.2 `internal/proxy/e2e_014001_test.go` — 11 tests

Todos UNTOUCHED (verificado: ninguno usa assert de set exacto — solo campos puntuales + `cost_usd` tolerante/cero): `Test014001_P2_OffDefaultSinArchivo`, `Test014001_P7_HappyPathNoStream`, `Test014001_P8_P14_FallbackConsolidado`, `Test014001_P9_RetryMismoProvider`, `Test014001_P8_P10_P13_ChainAgotado`, `Test014001_P10_P13_ModelNotFound`, `Test014001_P7_P12_StreamingOK`, `Test014001_P10_P13_DegradedFastFail`, `Test014001_P11_CorrelacionYOrden`, `Test014001_P15_PrivacidadGrepNegativo` (re-correr en POST-AUDIT), `Test014001_P16_RequestContinua`.

### 2.3 Pricing / costos (tabla `pricing:`)

Tabla en dos niveles: `config.PricingConfig` → `proxy.ModelPricing` vía `SetPricing` pre-tráfico. Tests UNTOUCHED: `e2e_006002_test.go` (TestRED_CostoExacto y otros — verifican `/metrics`, no el registro), `e2e_007002_test.go` (TestRED_ModelsPricing), `e2e_007003_test.go` (headers `X-Usage-*` + costo — P8 los congela), `internal/config/external_review_fixes_test.go` (PricingNaNInf).

### 2.4 Parseo upstream / punto de captura de `usage.cost`

- `provider.Usage` (`internal/provider/provider.go:118-127`): Prompt/Completion/TotalTokens + Cached/CacheCreation/ReasoningTokens (con `json:"-"`). **NO existe campo `Cost`**: `usage.cost` hoy se descarta. La captura es contrato NUEVO a nivel de parseo — RED vía B2/B9 sin sobredeterminar el cómo (el implementer decide: campo `Cost` en `Usage` vs captura lateral).
- Puntos de emisión terminal success (reciben ya `model` + `*provider.Usage`): `proxy.go:738` (stream), `:776` (singleflight líder), `:827` (sf follower), `:847` (non-stream), `responses.go:511`, `embeddings.go:143`. Firma: `emitTerminalSuccess(requestID, clientID, providerID, model, u, stream)` (`proxy.go:937`); construye `registry.TerminalEvent` en `:955-956`. `emitTerminalError` (`proxy.go:982`, `:997`) NO recibe modelo → resuelto en §4 (decisión a: extender con `model`).
- `TerminalEvent` (`registry.go:65-76`): sin `model` (el `Model` de `:51` es de AttemptEvent). P1 es aditivo también a nivel de struct.

## 3. Plan de tests nuevos (B1-B9, C1-C2 del spec)

Archivos nuevos: `internal/proxy/e2e_020001_registry_cost_test.go` (harness 014-001 reutilizado) + `internal/registry/registry_cost_test.go` (B1-esquema + B7-compat a nivel writer).

| ID | Nombre | Postcondición(es) | Fixture | RED |
|---|---|---|---|---|
| B1 | `TestPostcondition1_TerminalIncluyeModel` | P1 | e2e no-stream, modelo `"m"`; terminal del JSONL con `model=="m"` | Compilación (`Model` inexistente en TerminalEvent) |
| B2 | `TestPostcondition2_4_UpstreamSrcYCostoExacto` | P2, P4 (C2) | upstream fake con `usage.cost=0.0042`; pricing CARGADO distinto a propósito; `src=="upstream"`, `cost_usd==0.0042` exacto | Compilación o AssertionError |
| B3 | `TestPostcondition3_CostoCeroUpstreamNoEsNull` | P3 | upstream fake con `cost=0.0` → `up!=nil && *up==0.0`, `src=="upstream"`; sin `cost` → `null` | AssertionError (conflación 0.0↔null) |
| B4 | (en B2) exactitud "tabla NO participa" | P4 | pricing deliberadamente distinto del upstream cost | AssertionError si re-calcula |
| B5 | `TestPostcondition5_TablaFallback` | P5 (rama table) | SIN `cost`; pricing {1.0,1.0,1.0}; usage prompt 1200/completion 80/cached 1000 (miss 200) → `0.00128`; `src=="table"`, `up==null` | AssertionError |
| B6 | `TestPostcondition5_SinPrecioEsNone` | P5 (rama none) | SIN `cost`; modelo SIN pricing → `cost_usd==0`, `src=="none"`, `up==null`; sub-caso terminal ERROR → `none` | AssertionError |
| B7 | `TestPostcondition6_LineaHistoricaParsea` | P6 | línea JSONL literal histórica (sin los 3 campos) → `model==""`, `src==""`, `up==null` | Compilación + AssertionError (nivel writer) |
| B8 | `TestPostcondition7_PrivacidadMetadataOnly` | P7 | tras B2/B5: tipos + grep negativo del prompt secreto | AssertionError |
| B9 | `TestPostcondition8_RespuestaByteIdentica` | P8 | mismo request CON vs SIN `cost`: cuerpo + headers `X-Usage-*` idénticos | AssertionError |

Cobertura C2 (upstream-con-costo / sin-costo+precio / sin-precio): B2 / B5 / B6. C1: todo bajo `-race`.

## 4. Benefit-of-doubt + decisiones HITL

### Resueltos (vinculantes para GREEN)

1. **`estimateCost` se reutiliza, no se duplica** (`proxy.go:1016`, fórmula `:1021-1023`).
2. **"Modelo sin precio" vs "precio cero"** por existencia de key (`p, ok := s.pricing[model]`); presente-con-ceros → `"table"`; ausente → `"none"`.
3. **Streaming**: captura solo-lectura del objeto ya parseado/chunk consumido (B9 lo congela).
4. **Emisión siempre presente, sin `omitempty`**: líneas nuevas emiten SIEMPRE los 3 campos (`up` null cuando aplique); P6 es propiedad de *lectura*.
5. **Precedencia total**: upstream > tabla > none (B2/B4 con pricing cargado a propósito).

### Preguntas → RESUELTAS POR HITL (sign-off 2026-09-17)

- **(a) Terminales de error: SÍ llevan `model`** — extender `emitTerminalError` con `model string` (P1: "TODO evento terminal"; default `""` si se desconoce).
- **(b) Upstream no-OpenRouter-shaped: TOLERANTE** — vale cualquier `cost` numérico presente en `usage` (contrato observable; sin shape-check).
- **(c) Alcance: LOS 6 call-sites success** (mismo `emitTerminalSuccess` compartido; el spec no excluye ninguno).

## 5. RED + discriminantes

- **RED por compilación**: B1, B2, B7 (campos inexistentes). **RED por AssertionError**: B3, B5, B6, B8, B9 (leen JSONL como map).
- **Discriminante B3-vs-B6**: `up` non-nil `0.0` (real upstream) vs `null` con `cost_usd==0` (faltante). B2/B4 con pricing cargado discriminan "usa upstream" de "re-calcula".

## 6. Fixtures

- Upstream fake con costo (`usage.cost=0.0042`), variante free (`0.0`), variante sin costo, variante stream (chunk final). Pricing `{1.0,1.0,1.0}` (canónico B5: `0.00128`). Línea histórica P6 literal. Harness 014-001 reutilizado sin cambios.

## 7. Gate checklist

- [x] Pregunta HITL respondida: `estimateCost` EXISTE (`proxy.go:1016`).
- [x] Inventario: 1 modificación justificada + ~25 untouched explícitos.
- [x] Plan B1-B9 con mapeo completo; 0 postcondiciones inventadas.
- [x] Contratos de firma + 3 preguntas resueltas por HITL.
- [x] RED discriminantes; riesgos bajos (set exacto ya contemplado; emitTerminalError; B9).
- [x] Baseline: tomar el verificado en HEAD al correr (discrepancia 836/33 vs 974/37 registrada — vigente el de HEAD).

---

**Resumen:** Tests a modificar: **1** (`Test014001_P6_TerminalSchemaExact`) · Tests untouched: **~25** · Tests nuevos: **9 (B1-B9)** · Regression risks: bajo.

Status: **Approved** by Ofap (agent-delegated HITL, goal mode) on 2026-09-17 — preguntas (a)(b)(c) resueltas (error con model; cost tolerante; 6 call-sites). Gate 3.0 cerrado, arranca RED (3.1).
