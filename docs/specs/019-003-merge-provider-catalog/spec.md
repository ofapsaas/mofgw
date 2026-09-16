---
id: 019-003-merge-provider-catalog
title: Merge de catálogos upstream → IR de sync (paquete puro internal/catalogmerge)
status: approved
epic: 019-provider-sync-automation
date: 2026-09-11
created: 2026-09-11
motivacion_externa: "Epic 019: config.yaml se mantiene a mano y diverge del upstream (deepseek-v4-flash 0.22/0.66 manual vs 0.15/0.6 upstream — error humano verificado); el catálogo upstream manda y 019-004 necesita el IR tipado para escribirlo."
---

# 019-003 — Merge provider-catalog: catálogos upstream → IR de sync

## Descripción

- **D1 — IR de sync propio (LOCKEADA).** Paquete nuevo `internal/catalogmerge`, **puro** (sin red, sin disco, sin `config.Load` — D9): recibe los catálogos ya parseados de 001/002 (`modelsdev.Catalog`, `upstream.ModelList` zen, `upstream.ModelList` go, `upstream.OpenRouterCatalog` — cada uno `nil` = fuente ausente) + los providers ya parseados (`[]config.ProviderConfig`) y produce el IR que 019-004 escribirá en config.yaml:
  ```go
  type Plan struct {
      Providers   []ProviderPlan   // ordenado por ProviderID (D7)
      SourcesUsed map[string]bool  // "modelsdev"|"zen"|"go"|"openrouter" → usada
      Warnings    []string         // determinístico (ordenado, dedup)
  }
  type ProviderPlan struct {
      ProviderID string   // id del provider de config.yaml
      Source     string   // zen|go|openrouter|modelsdev
      Models     []string // lista FINAL de ids de modelo (ordenada, dedup)
      Pricing    map[string]config.PricingConfig   // keyed por id de modelo
      Metadata   map[string]config.ModelMetadata   // keyed por id de modelo
      Warnings   []string // ordenados (matching/espejo/descartes)
  }
  ```
  Imports permitidos: `internal/config` (SOLO tipos + los 2 knobs nuevos de D2), `internal/modelsdev`, `internal/upstream`. **JAMÁS** `proxy`/`router`/`cmd` ni `modelscache`. Salida pensada para que 004 serialice EXACTAMENTE los campos destino (ver Contexto técnico).
- **D2 — Asociación provider→fuente: knob declarativo + auto determinística (LOCKEADA).** Nuevos campos opcionales en `config.ProviderConfig`: `sync_source` (`""`|`zen`|`go`|`openrouter`|`modelsdev`; otro valor → error de carga, fail-fast) y `sync_mirror` (id de provider de models.dev para metadata/pricing; opcional). Convención del repo (I7): declarativo, JAMÁS inferido (`thinking_path` config.go:142, `opencode_session` config.go:151). Default `""` = **auto**: match EXACTO de `base_url` (trim del `/` final) contra las consts de `upstream` sin sufijo `/models`; sin match → `modelsdev`; `type: subprocess` → `modelsdev`. Verificado contra el config vivo: 8×go→go, 8×zen→zen, 1×openrouter→openrouter, qwen→modelsdev, claude-pro→modelsdev.
- **D3 — Reglas de matching (LOCKEADA).** (a) IDs cortos (zen/go y providers modelsdev): match directo contra las **keys** de `modelsdev.Catalog.Providers` (empírico hoy: 70/70 zen, 37/37 go). (b) OpenRouter `vendor/model` → **strip del prefijo vendor** (split en `/`) → key corta. (c) Alias `~vendor/model` → `alias_target.slug` → strip vendor → key corta (verificado con el caso vivo: `~deepseek/deepseek-v4-flash-latest` → `deepseek-v4-flash-0731`); alias sin `alias_target` → omitir + warning. (d) ID ausente en models.dev → **omitir del plan + warning** (fail-soft; nunca aborta). **Extensión aditiva requerida en `internal/upstream` (única, mecánica, mismo estilo que sync_source en config):** campo nuevo `AliasTargetSlug string` en `OpenRouterModel` (+ tag `alias_target.slug` en el raw, pointer→string tolerante). Suite 002 intacta y verde (gate, mismo estilo que 001).
- **D4 — Espejo de verdad de metadata/pricing (LOCKEADA).** La fuente de verdad de cost/limit/modalities/reasoning_options es el **provider-espejo de models.dev**: knob `sync_mirror` gana si está seteado y existe; defaults por fuente: `zen`→`opencode` (70/70 verificado), `go`→`opencode-go` (35/37; misses `deepseek-flash`/`hy3-preview`), `openrouter`→sin espejo (models.dev manda pricing/limit directamente: regla P6 con fallback), `modelsdev`/subprocess→sin default (fallback directo). **Fallback determinístico:** espejo ausente o sin el ID → primer provider de models.dev con ese ID **ordenado alfabéticamente** que tenga `cost` presente + warning; ninguno con cost → metadata sin pricing + warning.
- **D5 — Mapeo mecánico por campo (LOCKEADA).** `cost.input→InputUSDPerM`, `cost.output→OutputUSDPerM`, `cost.cache_read→CacheHitUSDPerM` — **passthrough directo** (unidades USD/M verificadas contra el config vivo: glm-5.2 y qwen3.7-plus idénticos al espejo). `cache_write`, `tiers`, `context_over_200k` **descartados con warning** si difieren del base (PricingConfig es plano; NUNCA inventar tiering). `limit.context→ContextWindow`; `limit.output→MaxOutput` (fallback: OpenRouter `top_provider.max_completion_tokens`); `Modality = Join(modalities.input,"+") + "->" + Join(modalities.output,"+")` (formato exacto que parsea `splitModality`, proxy.go:1290-1305; fallback: `architecture.modality` de OpenRouter — mismo formato literal); modalities ausente → campo omitido.
- **D6 — `supported_parameters` (LOCKEADA).** Prioridad: (1) OpenRouter **tal cual** (verificado 443/443); (2) fallback derivación de capabilities de models.dev: `tool_call→"tools"`, `structured_output→"structured_outputs"`, `temperature→"temperature"`, `reasoning→"reasoning"`; (3) nada → omitir el campo (P2 de 010-001). El vocabulario OpenRouter (22 params) difiere del manual corto actual — **cambio de catálogo /v1/models observable INTENCIONAL** (paridad con upstream manda).
- **D7 — Determinismo byte a byte (LOCKEADA).** Toda colección ordenada alfabéticamente; NINGÚN `range` de mapa sin sort; dos corridas con igual entrada → `Plan` deep-equal idéntico (precedente clientconfig P11).
- **D8 — Fail-soft por fuente (LOCKEADA).** Fuente de acceso ausente para un provider `zen`/`go` → ese provider con `Models: nil` + warning (los demás continúan); OpenRouter ausente → sin `SupportedParameters` de OR (paso 2 de D6); models.dev ausente → solo IDs de acceso sin pricing/metadata + warning; **sin fuente NINGUNA** → error (nada que mergear). El Plan expone `SourcesUsed`/`Warnings` para que 004 decida abortar o escribir degradado.
- **D9 — Pureza (LOCKEADA).** El paquete no fetchea, no lee archivos, no carga config: TODO inyectado como argumento. Ni siquiera instancia Stores. Testeable 100% offline.

## Contrato (postcondiciones)

- **P1 — Asociación de fuente.** (a) `sync_source` seteado → gana (sin importar base_url). (b) `sync_source: ""` → auto determinística: base_url (trim `/`) match exacto contra consts de `upstream` sin sufijo `/models` → esa fuente; sin match → `modelsdev`; `type: subprocess` → `modelsdev`. Casos del config vivo como test.
- **P2 — Matching corto directo.** Los IDs de la lista de acceso de zen/go se machean contra las keys de `modelsdev.Catalog.Providers`: presentes → entran al `ProviderPlan`; `Models` es **la lista de acceso completa** (zen/go) o **el subset declarado** (openrouter/modelsdev — nunca se agregan IDs). **Aclaración HITL 2026-09-16 (review S2/L2-P2):** la "lista de acceso" de zen/go es el `models[]` declarado en config.yaml (`prov.Models`) — los tests la congelan así; el merge **enriquece** pricing/metadata del subset declarado y **NO amplía el set a paridad upstream**. Si 019-004 requiere que el set crezca a paridad upstream, compete a su spec.
- **P3 — Strip de vendor OpenRouter.** Un modelo OR `z-ai/glm-5.3-flash` presente en models.dev como key `glm-5.3-flash` produce un plan cuyo `Models` contiene `z-ai/glm-5.3-flash` (el ID de ACCESO) y cuyo `Pricing`/`Metadata` están keyed por ese mismo ID (el strip es SOLO para encontrar la metadata, I2).
- **P4 — Alias de 2 saltos.** `~deepseek/deepseek-v4-flash-latest` con `alias_target.slug == "deepseek/deepseek-v4-flash-0731"` → strip vendor → `deepseek-v4-flash-0731` en models.dev (sin espejo → fallback D4) → pricing/metadata resueltos; el ID en el plan sigue siendo `~deepseek/deepseek-v4-flash-latest`. Alias sin `alias_target` → omitido + warning.
- **P5 — Ausencia en models.dev.** ID que no existe en ninguna key de models.dev → NO entra a `Pricing`/`Metadata`, con `Warning` del provider; el merge NO falla.
- **P6 — Espejo.** `sync_mirror` seteado y existente → ese provider de models.dev es la verdad. Sin knob: defaults por fuente (D4). Espejo sin el ID o ausente → primer provider alfabético con `cost` presente + warning; ninguno con cost → sin `Pricing` + warning (fail-soft).
- **P7 — Pricing passthrough fiel.** 1:1 sin conversión (unidades ya USD/M). `cache_write`/`tiers`/`context_over_200k` presentes y distintos del base → warning de descarte, valor base queda.
- **P8 — Metadata derivada.** `ContextWindow = limit.context`; `MaxOutput = limit.output` (fallback OpenRouter `top_provider.max_completion_tokens`); `Modality = Join(input,"+")+"->"+Join(output,"+")` (fallback `Architecture.Modality` de OR); sin ninguna → campo omitido.
- **P9 — Thinking solo derivable.** `Thinking` SOLO si `reasoning_options` trae `{type:"effort", values:[...]}` → values tal cual. `toggle`/`budget_tokens` sin values → sin levels (los niveles manuales no declarados upstream NO se derivan — pérdida observable documentada). `ThinkingDefault` **JAMÁS aparece en el IR**.
- **P10 — `SupportedParameters` prioridad.** (1) OpenRouter → tal cual; (2) derivación de models.dev: `tool_call→"tools"`, `structured_output→"structured_outputs"`, `temperature→"temperature"`, `reasoning→"reasoning"` (solo presentes, ordenados); (3) nada → omitir.
- **P11 — Determinismo.** Dos ejecuciones → `Plan` deep-equal byte a byte: Providers por ProviderID, Models ordenado con dedupe, mapas iterados en orden sortado, Warnings ordenado y dedup.
- **P12 — Fail-soft por fuente.** (a) fuente de acceso `nil` → provider con `Models: nil` + warning, resto intacto; (b) OR `nil` → pasos (2)/(3); (c) modelsdev `nil` → plan sin Pricing/Metadata + warning; (d) todas `nil` → error descriptivo. `SourcesUsed` refleja solo fuentes no-nil.
- **P13 — Pureza.** Sin sockets, sin archivos, sin `config.Load`: todo por parámetros, `nil` = ausente.
- **P14 — Compatibilidad config (cambio aditivo).** Configs SIN los knobs cargan idéntico; `sync_source: "bogus"` → error de carga claro; `sync_mirror` free-string (validación de existencia = warning del merge). Cambio a `internal/config`: SOLO los 2 campos + validación de `sync_source` — cero cambios de comportamiento existente.

## Invariantes

- **I1 — Sin imports prohibidos.** `catalogmerge` importa solo `internal/config` (tipos), `internal/modelsdev`, `internal/upstream`, stdlib. Jamás `proxy`/`router`/`cmd`/`modelscache`. El servidor no importa el paquete.
- **I2 — El upstream manda.** La lista de acceso define Models (zen/go); el subset declarado define Models (openrouter/modelsdev); el strip del vendor NO altera el ID del plan.
- **I3 — Declarativo, nunca inferido.** Knobs declarados; el auto es regla EXACTA publicada (comparación contra consts), no heurística.
- **I4 — Fail-soft de fuente, fail-loud de datos.** Fuente ausente → warning + plan parcial; entrada ilegal → error (los catálogos llegan ya parseados).
- **I5 — No inventar.** Solo se derivan campos con regla mecánica determinística; lo no derivable se omite o se preserva (ThinkingDefault → 004).
- **I6 — Sin mutación de entradas.** Catálogos/providers tratados como inmutables.
- **I7 — Cambios a 001/002 mínimos y aditivos.** `upstream`: SOLO `AliasTargetSlug`; `modelsdev`: **SOLO extensión aditiva zero-value** (`StructuredOutput`, `Temperature`, `ReasoningEffort` — campos ausentes → false/nil, sin cambio de comportamiento existente; enmienda HITL 2026-09-16 vía review S1/L2-I7, requerida por P9/P10); suites de ambas intactas y verdes (gates).

## Criterios de aceptación (con mapeo test)

| #   | Criterio                                                                                                                                                        | Test                                                                                                 |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| C1  | P14/P1: knobs parsean; valor inválido → error de carga; configs existentes idénticos; asociación knob-gana + auto determinística (5 casos)                      | `TestSyncSourceKnob`, `TestSourceAssociation`                                                            |
| C2  | P2: matching corto directo (fixtures zen/go + models.dev)                                                                                                       | `TestMerge_ShortIDDirectMatch`                                                                           |
| C3  | P3: strip de vendor — plan keyed por ID de acceso con metadata del ID corto                                                                                     | `TestMerge_OpenRouterVendorStrip`                                                                        |
| C4  | P4: alias 2 saltos + alias sin target → omitir + warning                                                                                                        | `TestMerge_OpenRouterAliasResolution`, `TestMerge_OpenRouterAliasWithoutTarget`                             |
| C5  | P5: ID ausente en models.dev → sin pricing/metadata + warning, sin error                                                                                        | `TestMerge_MissingIDSoftSkip`                                                                            |
| C6  | P6: espejo — knob gana; defaults; fallback alfabético-con-cost + warning; sin cost → sin pricing + warning                                                      | `TestMirror_KnobWins`, `TestMirror_DefaultsBySource`, `TestMirror_AlphaFallback`, `TestMirror_NoCostWarning` |
| C7  | P7: passthrough 1:1; tiers/cache_write/context_over_200k → descartados + warning                                                                                | `TestMerge_PricingPassthrough`, `TestMerge_PricingTiersDiscardWarning`                                     |
| C8  | P8: ContextWindow/MaxOutput (+fallback OR), Modality join (+fallback, caso ausente)                                                                             | `TestMerge_MetadataDerivation`, `TestMerge_ModalityFallback`                                               |
| C9  | P9: Thinking solo de effort.values; toggle/budget → sin levels; ThinkingDefault ausente                                                                          | `TestMerge_ThinkingFromEffortValues`                                                                     |
| C10 | P10: supported_parameters — OR > derivación > omitir (3 ramas)                                                                                                  | `TestMerge_SupportedParametersPriority`                                                                  |
| C11 | P11: dos corridas → Plan deep-equal; órdenes                                                                                                                    | `TestMerge_Deterministic`                                                                                |
| C12 | P12: fail-soft por fuente; todas nil → error; SourcesUsed correcto                                                                                              | `TestMerge_FailSoftPerSource`, `TestMerge_NoSourcesError`, `TestMerge_SourcesUsed`                          |
| C13 | P13: pureza — implícito por firma congelada + revisión                                                                                                          | —                                                                                                      |
| C14 | Suite completa verde; suites de 001/002 intactas (I7)                                                                                                           | suite + GREEN                                                                                            |

**Naturaleza del RED:** `internal/catalogmerge` no existe → tests fallan por compilación (`undefined: catalogmerge.*`). Los cambios a `internal/config` (P14) y `upstream` (`AliasTargetSlug`) SÍ compilan — sus asserts de comportamiento (P14 knobs, P4 alias) fallan por AssertionError si 003 no hace esas adiciones. Precedente 011-005/015-001/019-001/019-002. Fixtures: recortes reales capturados 2026-09-11.

## Fuera de alcance

- **019-004:** serialización/escritura de config.yaml; dedupe de providers gemelos; binario; flag `--no-fetch`.
- **019-005/006/007:** reload, timer, snapshot.
- **`ThinkingDefault`:** jamás derivado; preservación = 004.
- **Pricing de OpenRouter:** descartado como fuente (per-token; units distintas) — solo aporta supported_parameters y max_completion_tokens.
- **Fetch, disco, config.Load dentro del paquete** (D9/P13).
- Cambios en `cmd/`, `internal/proxy`, `internal/router`; y en `internal/config` más allá de los 2 knobs (P14).

## Alternativas desestimadas

- **Reusar `clientconfig.ConfigIR`:** shape de render por cliente (`Meta map[string]any`) — no transporta tipos de config.yaml; solo patrón de pureza/determinismo.
- **Asociación solo por base_url sin knob:** convención del repo es declarativo-jamás-inferir; el auto queda como default determinístico documentado y el knob gana (I3).
- **Maching OR por CanonicalSlug:** para aliases conserva el `~` (verificado) → strip de vendor sobre el ID es la regla uniforme.
- **Pricing de OpenRouter (×1e6):** models.dev espejo manda en USD/M; OR introduce conversión innecesaria.
- **Derivar `ThinkingDefault`:** no existe upstream; adivinarlo sería inventar (I5).
- **Reescribir subset declarado con catálogo completo:** para openrouter/modelsdev la lista de acceso NO es el catálogo completo; el merge valida/elimina, nunca agrega (P2).
- **Fallback de espejo por "más barato":** heurística de negocio; alfabético-primero es determinístico y `sync_mirror` permite pinning.

## Contexto técnico verificado (Discovery 2026-09-11)

### Campos destino (hooks exactos)
| Campo destino      | Struct (archivo:línea)                                                                                                | Consumo runtime                                               |
| ------------------ | --------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `providers[].models` | `config.ProviderConfig.Models` (config.go:118)                                                                          | key del mapa pricing/metadata = mismo string                  |
| `pricing` (plano)    | `PricingConfig{InputUSDPerM, OutputUSDPerM, CacheHitUSDPerM}` (config.go:212-216)                                       | main.go:244-252 `SetPricing`                                    |
| `model_metadata`     | `ModelMetadata{ContextWindow, MaxOutput, Thinking, ThinkingDefault, SupportedParameters, Modality}` (config.go:302-314) | main.go:254-258 `SetModelMetadata`; catálogo proxy.go:1233-1305 |

- `splitModality` (proxy.go:1290-1305): parsea `"inputs->outputs"` con separador `"+"` — formato EXACTO de `architecture.modality` de OpenRouter y derivable de `models.dev modalities` con Join("+").
- `SupportedParameters` va tal cual al catálogo (proxy.go:1246-1250).
- **`clientconfig.ConfigIR` NO reusable** (clientconfig.go:23-37): solo patrón de pureza/determinismo.
- **Precedente de knob declarativo:** `thinking_path` (config.go:142), `opencode_session` (config.go:151) — patrón para `sync_source`/`sync_mirror`.
- **Config vivo (leído sin exponer keys):** 8×go, 8×zen, 1×openrouter (3 ids, uno alias `~`), 1×qwen aliyuncs, 1×subprocess. `pricing` 32 keys / `metadata` 45 keys por ID corto; el openrouter vivo usa IDs con prefijo → keys de pricing no corresponden (gap que cierra 003).

### Hallazgos empíricos
- **Maching corto:** zen 70/70, go 37/37 vs models.dev (0 misses hoy).
- **Espejos:** `opencode` = 102 modelos (cubre 70/70 ZEN); `opencode-go` = 36 (cubre 35/37 GO; misses `deepseek-flash`→provider `deepseek`, `hy3-preview`→aihubmix).
- **Alias 2 saltos:** `~deepseek/deepseek-v4-flash-latest` → `deepseek/deepseek-v4-flash-0731` → strip → `deepseek-v4-flash-0731` (14 providers, sin espejo → fallback).
- **Unidades VERIFICADAS:** models.dev `cost` = USD/M (glm-5.2 espejo 1.4/4.4/0.26 == config vivo; qwen3.7-plus 0.4/1.6/0.04 == config vivo) → passthrough directo; OpenRouter = por token (NO se usa para pricing).
- **Schema variable:** `qwen3.7-plus` (opencode-go) trae `tiers[]` + `context_over_200k` → descartados con warning.
- **Thinking no derivable completo:** config manual `minimax-m3: ["adaptive","disabled"]` no existe como effort.values upstream; `thinking_default` inexistente upstream.
- **IDs sin espejo:** `deepseek-v4-flash-0731` (14), `deepseek-flash` (1), `hy3-preview` (2), claude ids (21/13/17; espejo natural `anthropic` declarable con sync_mirror).
- **`alias_target` NO tipado hoy en 002** → extensión aditiva `AliasTargetSlug` (D3, gate suite 002 verde).
- **Baseline:** suite 806/32 `-race` verde post-002; RED especial de compilación precedente.

## Estado

Status: **Approved** by Ofap (HITL delegado, pedido explícito de Pablo — goal mode) on 2026-09-11
