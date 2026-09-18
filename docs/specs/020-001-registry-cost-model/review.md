# review.md — 020-001-registry-cost-model (etapa 4, Review two-layer)

## Veredicto

**APPROVE — 0 bloqueantes.** 1 Minor + 5 Advisory. Apto para merge directo.

## Auditoría RED→GREEN (desviación de ciclo)

`git show 7cbeaa9` confirma: el GREEN (commiteado por el orquestador) tocó **exactamente 5 archivos de implementación** (provider.go +20, proxy.go +35/-9, responses.go +1, registry.go +14/-11, singleflight.go +1) — **cero archivos de test**. El RED 0e48bc5 solo tocó `e2e_020001_registry_cost_test.go` (nuevo), `registry_cost_test.go` (nuevo) y la mod justificada P6 en `registry_test.go`. No hay manipulación de tests para pasar. Los tests B1-B9 del RED quedaron intactos y el GREEN los pasa por código real.

## Capa 1 — Verificación por postcondición

| Contrato | Veredicto | Evidencia |
|---|---|---|
| **P1** model en todo evento terminal | **PASS** | `emitTerminalSuccess` setea `Model` (proxy.go:984) y `emitTerminalError` (proxy.go:1030). Los 6 call-sites success pasan model: proxy.go:738 (stream), 776 (líder SF), 829 (follower SF), 849 (no-stream), responses.go:511, embeddings.go:143. Los 4 call-sites error pasan model vía `handleChainError(..., req.Model/rb.Model, ...)`: proxy.go:770, 843, 1157, responses.go:503. |
| **P2** precedencia upstream > table > none | **PASS** | proxy.go:964-975: `u.Cost != nil` → "upstream"; si no, `src` por **presencia de key** en `s.pricing` (proxy.go:970) — cumple la decisión vinculante "sin precio = ausencia de key". |
| **P3** `cost_usd_up` *float64 nullable, 0.0 ≠ null | **PASS** | `usageFloatPtr` (provider.go:167-176): `"0"` parsea a puntero no-nil; `null`/ausente/no-numérico → nil. Tag sin `omitempty` (registry.go:78) → nil serializa como `"cost_usd_up":null` explícito. Test B3 cubre el caso free. |
| **P4** upstream exacto, tabla NO participa | **PASS** | proxy.go:964-967: `costUSD = *u.Cost` directo, sin llamada a `estimateCost` ni a la tabla. Cubierto en stream Y no-stream. |
| **P5** reutiliza `estimateCost`; sin precio → 0+none | **PASS** | proxy.go:969 llama `s.estimateCost(...)` — **una sola fórmula, cero duplicación**. Sin precio: `estimateCost` retorna 0 (proxy.go:1044) + `src="none"` (proxy.go:973). |
| **P6** línea histórica → zero-values | **PASS** | registry.go:119-132: `json.Marshal` estándar, sin `omitempty`, sin unmarshal custom; línea histórica parsea con `model=""`, `cost_usd_src=""`, `cost_usd_up=nil`. Test B7. |
| **P7** privacidad metadata-only | **PASS** | Campos nuevos string/float64/pointer. Captura **solo-lectura** de `cres.Response.Usage.Cost`; envelope passthrough del raw. Test B7 con secret en prompt. |
| **P8** respuesta byte-idéntica | **PASS** | `setUsageHeaders` intocado; `Usage.Cost` con `json:"-"` (provider.go:131) → jamás se re-serializa al cliente; body es raw passthrough. Test B8: envelope con `"cost":0.0042` intacto + set de headers X-Usage-* congelado en 5. |
| **I1** append-only | **PASS** | Writer O_APPEND + mutex, sin truncate — intocado. |
| **I2** cost_usd siempre numérico | **PASS** | `CostUSD float64` sin omitempty → siempre número; `cost_usd_up: null` es el valor contractual del nullable. |
| **I3** best-effort | **PASS** | Guards `s.registry == nil` / `requestID == ""` (proxy.go:940-945, 1007-1012); captura jamás falla el request. |
| **I4** cero dependencia DB | **PASS** | Writer es solo `os.File` JSONL; sin dependencias nuevas. |

## Capa 2 — Findings de calidad

**MINOR-1 — Gap de cobertura: singleflight follower sin test propio de los campos nuevos.** proxy.go:796/825 copian `Cost` en ambas direcciones — el follower emite el mismo TerminalEvent que el líder (verificado por lectura), pero ningún test de 020-001 ejercita el path singleflight. Sugerencia no-bloqueante: agregar caso follower en próxima feature o consolidación.

**ADVISORY-1:** followers de vuelo fallido no emiten terminal error (pre-existente 014-001, fuera del alcance HITL).
**ADVISORY-2:** errores de embeddings sin terminal event (pre-existente, coherente con el alcance de 6+4 call-sites).
**ADVISORY-3:** stream interrumpido emite terminal success con fallback de tabla (semántica pre-existente; correcto que NO lee usage tras error de Copy).
**ADVISORY-4:** divergencia header vs registro con costo upstream (`X-Usage-Cost-USD` reporta estimado tabla, registro reporta upstream real — intencional por P8; anotar para consolidación).
**ADVISORY-5:** costo upstream negativo pasa sin clamp (riesgo ≈ nulo; consolidador podría sanear al leer).

## Disclosure anti-bias

Reviewer: **GLM (glm-5.3-flash, Z.ai)** — familia distinta de la del implementer según el contrato anti-confirmation-bias de la etapa 4. Implementer: cdad-implementer (deepseek-v4-flash) — **anti-bias SATISFECHO**. El orquestador commiteó el GREEN (implementer abortado a mitad de reporte, trabajo completo verificado). Los veredictos se basan exclusivamente en lectura de diff + código + tests RED.

**Disclosure de verificación empírica:** NO pude correr la suite (entorno de reviewer sin `go test`); me baso en la suite 987/37 `-race` VERDE verificada por el orquestador + lectura completa del diff y de los 9 tests B1-B9.

## Conclusión

**APPROVE con 0 bloqueantes.** Los 8 findings son no-bloqueantes; MINOR-1 es gap de cobertura, no de contrato. Merge directo.

Status: Review **APPROVE** by cdad-reviewer (GLM/Z.ai, familia distinta al implementer) on 2026-09-17
