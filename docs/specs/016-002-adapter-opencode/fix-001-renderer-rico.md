# Fix 016-002-001 — renderer opencode rico (cost+capabilities)

- **Feature:** 016-002-adapter-opencode (follow-up)
- **Motivo:** cliente reporta sync idempotente pero pobre — renderer solo emite `limit`, ignora `pricing/capabilities/thinking` que `/v1/models` ya tiene.
- **Regla:** capability declarada por provider → se incluye (2026-08-30).

## Cambio

`internal/clientconfig/opencode/opencode.go:modelFragment` además de `limit`/`name` emite:

| Meta | → `models.<id>` |
|------|-----------------|
| `pricing.input_usd_per_m` + `output` + `cache_hit` | `cost: {input, output, cache_read}` (solo si >0, ausente si no declarado) |
| `supported_parameters` contiene `tools` | `tool_call: true` (ausente si no) |
| `thinking` no vacío o `capabilities.reasoning==true` | `reasoning: true` |
| `modality` con `image` | `modalities: ["text","image"]` (best-effort, solo si declara image) |

Guards `>0`/presencia idénticos a `limit`. Sin metadata → sin claves extra (P8 intacto).

## Tests

Extender `opencode_test.go`: nuevos tests P12-P15 para cost/reasoning/tool_call, sin romper P1-P11.

## Criterio aceptación

`GET /v1/client-config?client=opencode` con modelo que tiene pricing+tools+reasoning → fragmento contiene `cost`+`tool_call`+`reasoning`. Modelo sin pricing → sin `cost`.
