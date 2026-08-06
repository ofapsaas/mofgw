# Spec — 007-001-model-metadata: modelo de datos de capabilities por modelo

---
feature_id: 007-001-model-metadata
epic: mofgw-007-catalogo
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 006-001 (usage accounting), 006-002 (pricing ModelPricing)
paralelizable: no
---

## Contexto

El frente 3 de Pablo (06 Ago): "los runtimes no obtienen información de
capacidades de thinking, no pueden optimizar low/high/max para distintas
operaciones, no tienen datos de tokens y cache consumidos, no tienen info del
tamaño de ctx que cada modelo soporta". Verificado empíricamente: `opencode
models mofgw --verbose` muestra `reasoning:false, context:0, cost:0` para
TODOS los modelos del gateway → los runtimes toman malas decisiones (no
mandan reasoning_effort, compactan con default desconocido).

El discovery (`docs/research-token-efficiency.md` §3 y §4) estableció:

- opencode/openclaw leen de /v1/models: `context_length` (P0), `max_context_length`,
  `loaded_context_length`, `max_completion_tokens`, `capabilities.reasoning`,
  `supported_parameters` (P1-P2). Ver §3 para la matriz completa.
- Capacidades de thinking REALES por modelo (verificado con fuentes, §4):
  deepseek-v4-flash low/high/max; deepseek-v4-pro high/max (3-tier esperado);
  minimax-m3 adaptive/disabled; glm-5.2 7 niveles (max..none); kimi-k2.7-code
  SIEMPRE ON incontrolable; qwen3.7-* enable_thinking binario + budget.
- Ventanas de contexto reales: deepseek 1M/384K output; minimax 1M/512K;
  glm 1M/128K; kimi 262K/32K; qwen 1M/64K. Ver §4.

Esta feature define el **modelo de datos** (config) que alimentará el
catálogo: metadata por modelo, declarativa, verificable, sin hardcodear en
código. La feature 007-002 expondrá estos datos en /v1/models; 007-003
expondrá el usage.

## Postcondiciones

1. **P1 — Metadata por modelo en config:** el config YAML acepta un bloque
   `model_metadata` keyed por modelo con: `context_window` (int), `max_output`
   (int), `thinking` (niveles soportados: lista de strings, ej.
   `["low","high","max"]`; `["always"]` para kimi; vacío = no soporta), y
   `thinking_default` (nivel por defecto). La metadata es data, no código.

2. **P2 — Acceso programático:** el paquete config expone la metadata
   tipada (struct `ModelMetadata`) y el Server la recibe (inyección, mismo
   patrón que SetPricing de 006-002). Un modelo sin metadata → metadata
   vacía (context_window 0, sin thinking) — no error, backward compatible.

3. **P3 — Pricing reutilizado:** el catálogo combina la metadata con el
   pricing existente (006-002 ModelPricing) — un modelo con ambas es
   "completo"; con una sola, la otra queda vacía (0/ausente) sin error.

4. **P4 — Validación de metadata:** valores negativos o inconsistentes
   (thinking_default no incluido en thinking) se rechazan al cargar config
   con error claro. El load falla temprano (no en runtime).

5. **P5 — No cambia el comportamiento de ruteo:** la metadata es
   informativa — no afecta fallback, clamp, ni el flujo de requests. Un
   modelo con metadata y otro sin ella se comportan igual en el ruteo.

## Invariantes

- I1: La metadata es data (YAML), no código — se actualiza sin recompilar.
- I2: Sin metadata → comportamiento idéntico al actual (backward compatible,
  cardinalidad 0 no rompe nada).
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: Los datos reflejan las capacidades VERIFICADAS del research §4 — el
  spec documenta la fuente por modelo (para que el deploy cablee datos
  correctos, no inventados).

## Criterios de aceptación

- C1: Config con `model_metadata.deepseek-v4-flash: {context_window: 1000000,
  max_output: 384000, thinking: [low, high, max], thinking_default: high}` →
  se carga sin error y se accede tipado desde el Server.
- C2: Modelo sin metadata → metadata vacía, sin error, ruteo normal.
- C3: `thinking_default: xhigh` con `thinking: [low, max]` → error de
  validación al cargar (P4).
- C4: Metadata + pricing combinados: modelo con ambas → ambas accesibles;
  con una sola → la otra vacía.
- C5: Un request a un modelo con metadata responde igual que sin ella
  (P5 — test E2E con y sin metadata, mismo resultado).
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`: `ModelMetadata` struct + `model_metadata`
  map en `Config` + validación en `Load` (P4).
- `internal/proxy/proxy.go`: campo `modelMeta map[string]config.ModelMetadata`
  + `SetModelMetadata(...)` (patrón SetPricing 006-002).
- Tests: config_test.go (C1-C3), e2e (C5: request con/sin metadata idéntico).
- 007-002 consumirá esta metadata para /v1/models.

## Fuera de alcance

- Exposición en /v1/models — feature 007-002.
- Exposición de usage a clientes — feature 007-003.
- Enrutamiento por capacidades (elegir modelo según metadata) — futuro.
- Catálogo automático (descubrir capacidades desde el provider) — la
  metadata es declarativa por decisión (los providers no la exponen
  uniformemente; el research §3 confirma que el formato está fragmentado).
