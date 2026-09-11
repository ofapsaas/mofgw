# Test Audit — 019-003-merge-provider-catalog

**Fecha:** 2026-09-11 · **Auditor:** orquestador EN MODO ROL test-writer (delegación timeout — 3er incidente de harness; trade-off de aislamiento débil DISCLOSURE: inline = garantía menor) · **Materializado por:** orquestador
**Spec:** docs/specs/019-003-merge-provider-catalog/spec.md (aprobado 8539158)
**Baseline verificado:** 806/32 `-race` verde (orquestador).

## Tests a modificar: **0**

Cambios aditivos verificados: (a) `sync_source`/`sync_mirror` zero-value (`""`) en configs existentes → carga idéntica (validación solo rechaza valores NO vacíos inválidos); (b) `AliasTargetSlug: ""` en `upstream.OpenRouterModel` → zero-value, ningún test de 002 lo referencia (grep 0). Suites 001/002/003 = gates (I7).

## Tests nuevos — bloques B1..B13

Archivos: `internal/catalogmerge/catalogmerge_test.go` (package catalogmerge, PUREZA: fixtures inline, sin red/disco) + `internal/config/sync_source_test.go` (archivo NUEVO — no modificación — para P14).

| Bloque | P | Tests | Mecanismo |
|---|---|---|---|
| B1 | P14 | TestSyncSourceKnob | Valores válidos (zen/go/openrouter/modelsdev/"") parsean; `"bogus"` → error de carga claro; config sin knobs carga idéntico (deep-equal de Parse con/sin sección) |
| B2 | P1 | TestSourceAssociation | Tabla 5 casos: sync_source gana; auto match contra consts `upstream` (trim `/`, sin `/models`); sin match → modelsdev; subprocess → modelsdev. Usa CONSTS, nunca URLs hardcodeadas |
| B3 | P2 | TestMerge_ShortIDDirectMatch | Fixture models.dev recortado (2 providers: opencode espejo con cost/limit/modalities/reasoning_options, otro sin cost); listas zen/go reales recortadas → Models = lista de acceso completa (upstream manda) |
| B4 | P3 | TestMerge_OpenRouterVendorStrip | OR `z-ai/glm-5.3-flash` + models.dev key `glm-5.3-flash` → plan Models=[`z-ai/glm-5.3-flash`], Pricing/Metadata keyed por ID de acceso |
| B5 | P4 | TestMerge_OpenRouterAliasResolution, TestMerge_OpenRouterAliasWithoutTarget | RED híbrido: si GREEN no hidrata `AliasTargetSlug` (D3), el alias resuelve "" → AssertionError; sin target → omitir + warning |
| B6 | P5 | TestMerge_MissingIDSoftSkip | ID de acceso inexistente en models.dev → sin Pricing/Metadata + warning, sin error |
| B7 | P6 | TestMirror_KnobWins, TestMirror_DefaultsBySource, TestMirror_AlphaFallback, TestMirror_NoCostWarning | sync_mirror gana; defaults zen→opencode / go→opencode-go / openrouter+modelsdev→fallback; fallback = primer alfabético con cost + warning; sin cost → sin Pricing + warning |
| B8 | P7 | TestMerge_PricingPassthrough, TestMerge_PricingTiersDiscardWarning | cost 1:1 (in/out/cache_read); tiers+context_over_200k+cache_write → descartados + warning en ProviderPlan.Warnings |
| B9 | P8 | TestMerge_MetadataDerivation, TestMerge_ModalityFallback | ContextWindow/MaxOutput (+fallback top_provider OR); Modality Join("+")+"->"+Join("+") (+fallback architecture.modality; caso todo ausente → omitido) |
| B10 | P9 | TestMerge_ThinkingFromEffortValues | effort.values → Thinking tal cual; toggle/budget → sin levels; assert ThinkingDefault == "" (JAMÁS derivado) |
| B11 | P10 | TestMerge_SupportedParametersPriority | 3 ramas: OR presente → tal cual; OR nil/missing → derivación tool_call/structured_output/temperature/reasoning (ordenados); nada → omitido |
| B12 | P11 | TestMerge_Deterministic | 2 corridas → reflect.DeepEqual + asserts de orden explícitos (Providers por ID, Models alfabético, Warnings ordenado dedup) |
| B13 | P12 | TestMerge_FailSoftPerSource, TestMerge_NoSourcesError, TestMerge_SourcesUsed | Cada fuente nil por turno (provider afectado + resto intacto); todas nil → error; SourcesUsed solo no-nil |

Total: **17 tests nuevos** (B1=1 config + 16 catalogmerge). C13 pureza: implícito por firma congelada + review. C14: gate de suite.

## Riesgos RED

- (a) El RED de B2/B1 es AssertionError (no compilación) cuando el GREEN agrega knobs a config pero no la lógica — distinguir razón correcta del fallo en la validación RED.
- (b) Fixtures fieles mínimos: copiar shapes reales (cost con cache_read, limit con input opcional, reasoning_options array tipado, tiers) — no inventar campos.
- (c) P6 fallback alfabético: fixture con ≥3 providers que tengan el ID, cost presente solo en el 2º alfabético → el test discrimina que NO tomó el primero.
- (d) Sin flakiness esperado (paquete puro, sin tiempo/reloj).
- (e) Extensiones de contrato: ninguna (D8/D3 congelaron todo).

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11 — desbloquea RED (3.1).
