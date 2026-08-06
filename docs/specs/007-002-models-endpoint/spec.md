# Spec — 007-002-models-endpoint: /v1/models enriquecido con capabilities

---
feature_id: 007-002-models-endpoint
epic: mofgw-007-catalogo
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 007-001 (ModelMetadata en config), 006-002 (ModelPricing)
paralelizable: no
---

## Contexto

Hoy `GET /v1/models` devuelve solo `{id, object, created, owned_by}` — los
runtimes no saben nada de las capacidades. Verificado empíricamente:
`opencode models mofgw --verbose` muestra `reasoning:false, context:0,
cost:0` → los runtimes no mandan reasoning_effort (deepseek-v4-flash lo
soporta) y compactan con default desconocido.

El discovery (research §3, con source de opencode Go + openclaw TS) definió
qué campos leen los runtimes y con qué prioridad:

- **P0** `context_length` (openclaw 1º), `max_context_length` +
  `loaded_context_length` (opencode Go para locales).
- **P1** `max_completion_tokens` (openclaw), `capabilities.reasoning`
  (openclaw LM Studio path).
- **P2** `supported_parameters` (openclaw OpenRouter path, busca "tools" y
  "reasoning"), `context_window` (openclaw 2º fallback).
- **P3** `modality`, `top_provider.context_length`.

Nota: opencode v2 (TS) usa models.dev, NO consulta /v1/models — el valor de
este endpoint es para openclaw (que sí lo consulta en discovery) y para
cualquier cliente que lea la convención OpenRouter. La metadata declarativa
de 007-001 (verificada en research §4) es la fuente de verdad.

## Postcondiciones

1. **P1 — /v1/models enriquecido:** cada modelo del catálogo incluye, cuando
   la metadata existe (007-001): `context_length` (= context_window),
   `context_window` (alias), `max_context_length` (= context_window),
   `loaded_context_length` (= context_window), `max_completion_tokens` (=
   max_output), y `capabilities.reasoning` (derivado de `thinking`:
   true si thinking no vacío). Modelos sin metadata conservan el formato
   mínimo actual (backward compatible).

2. **P2 — Pricing expuesto:** cuando el modelo tiene pricing (006-002), el
   objeto incluye `pricing` con `input_usd_per_m`, `output_usd_per_m`,
   `cache_hit_usd_per_m`. Sin pricing → campo ausente (no 0).

3. **P3 — Thinking explícito:** cuando `thinking` no es vacío, el objeto
   incluye `thinking: {levels: [...], default: "..."}` (niveles reales del
   research §4 — para que un runtime pueda elegir low/high/max). Para
   `["always"]` (kimi-k2.7-code), levels refleja que es fijo.

4. **P4 — Formato estándar respetado:** el envelope sigue siendo
   `{"object":"list","data":[...]}` con cada ítem `object:"model"`,
   `created`, `owned_by` — los campos extra NO rompen el formato mínimo
   (los clientes que ignoran campos extra siguen funcionando).

5. **P5 — Sin auth extra:** /v1/models sigue exigiendo auth Bearer (como
   hoy). La metadata no agrega superficie nueva.

## Invariantes

- I1: Backward compatible — un cliente que solo lee id sigue funcionando.
- I2: Los valores provienen de la metadata declarativa (007-001) y el
  pricing (006-002) — nunca hardcodeados en este código.
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: La cardinalidad del endpoint es la misma (modelos configurados).

## Criterios de aceptación

- C1: Modelo con metadata (context_window 1000000, max_output 384000,
  thinking [low,high,max]) → el objeto JSON tiene context_length 1000000,
  max_context_length 1000000, loaded_context_length 1000000,
  max_completion_tokens 384000, capabilities.reasoning true,
  thinking.levels [low,high,max].
- C2: Modelo sin metadata → objeto mínimo (id/object/created/owned_by),
  sin campos extra.
- C3: Modelo con pricing → pricing presente con los 3 valores; sin pricing
  → ausente.
- C4: kimi con thinking ["always"] → capabilities.reasoning true y
  thinking.levels ["always"].
- C5: Envelope `{"object":"list","data":[...]}` intacto; suite completa
  verde.

## Cambios de código esperados (orientación, no vinculante)

- `internal/proxy/proxy.go handleModels`: enriquecer el map del modelo con
  los campos derivados de `s.modelMeta` (007-001) y `s.pricing` (006-002).
  La metadata/pricing ya están en el Server (setters).
- Tests: e2e_test.go o nuevo e2e_007002_test.go con C1-C5 (GET /v1/models
  con auth, assert campos).

## Fuera de alcance

- Exposición de usage a clientes (headers/endpoint) — feature 007-003.
- Descubrimiento automático de capacidades desde el provider — declarativo
  por decisión (research §3: formato fragmentado).
- opencode v2 (usa models.dev, no lee esto) — fuera de nuestro control.
