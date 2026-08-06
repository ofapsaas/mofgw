# Test Audit — 007-002-models-endpoint

> Auditoría de suite vs postcondiciones del spec (P1-P5).
> Rol: test-writer (AUDIT). Fecha: 2026-08-06. Ejecutado inline (opción 3
> declarada).

## Mapeo suite existente → postcondiciones

| Test existente | Qué cubre | Cubre postcondición | Gaps |
|---|---|---|---|
| `e2e_test.go TestE2EModelsEndpoint` | GET /v1/models con auth → 200, lista el modelo | Patrón P1/P4 (envelope + auth) | No campos enriquecidos |
| `e2e_test.go harness` | providers con Models() | — | — |
| `config/metadata_red_test.go` (007-001) | ModelMetadata cargada | Base P1 (datos disponibles) | — |

## Gap analysis (→ tests RED)

| Postcondición | Cobertura | Test RED necesario |
|---|---|---|
| **P1** campos enriquecidos (context_length, max_context_length, loaded_context_length, max_completion_tokens, capabilities.reasoning) | ❌ | GET /v1/models con metadata seteada → assert campos (C1) |
| **P1/C2** modelo sin metadata → mínimo | Parcial (TestE2EModelsEndpoint) | Reforzar: sin metadata → sin campos extra |
| **P2/C3** pricing presente/ausente | ❌ | Modelo con pricing → pricing en objeto; sin → ausente |
| **P3/C4** thinking levels + kimi ["always"] | ❌ | thinking.levels reflejado; ["always"] → capabilities.reasoning true |
| **P4/C5** envelope intacto + suite verde | Parcial | Envelope `{"object":"list","data":[...]}` verificado |

## Convenciones

1. `build` + GET /v1/models con Bearer (patrón TestE2EModelsEndpoint).
2. `buildMultiClient`/`h.proxySrv.SetModelMetadata(...)` + `SetPricing(...)`
   para inyectar metadata/pricing antes del GET.
3. Assert campos con `map[string]any` parseado (json.Unmarshal).
4. Zero red externa; suite completa -race.

## Veredicto

P1, P2, P3 sin cobertura → RED. P4/P5 se re-verifican con suite + assert de
envelope.
