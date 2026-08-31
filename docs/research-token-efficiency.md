# Token Efficiency — Research Notes (EPIC-003/006/007/008)

> Discovery del replanteo de eficiencia (06 Ago 2026). Fuentes verificadas con fecha de acceso. Nombres de providers sanitizados (provider-a/b/c/d) por repo público.

## 1. Precios por modelo (verificado 06 Ago 2026)

**Tabla oficial de cada proveedor (USD por 1M tokens):**

| Modelo | Proveedor | Input $/M | Output $/M | Cache-hit $/M | Fuente |
|---|---|---|---|---|---|
| deepseek-v4-flash | DeepSeek | $0.14 | $0.28 | $0.0028 | api-docs.deepseek.com/quick_start/pricing/ |
| deepseek-v4-pro | DeepSeek (promo actual) | $0.435 | $0.87 | $0.003625 | api-docs.deepseek.com/quick_start/pricing/ |
| minimax-m3 (≤512K in) | MiniMax | $0.30 | $1.20 | $0.06 | platform.minimax.io/docs/guides/pricing-paygo |
| minimax-m3 (>512K in) | MiniMax | $0.60 | $2.40 | $0.12 | idem |
| glm-5.2 | Zhipu (Z.AI) | $1.40 | $4.40 | $0.26 | docs.z.ai/guides/overview/pricing |
| kimi-k2.7-code | Moonshot | $0.95 | $4.00 | $0.19 | platform.kimi.ai/docs/pricing/chat-k27-code |
| qwen3.7-plus (≤256K, intl) | Alibaba | $0.40 | $1.60 | $0.08 implicit / $0.04 explicit | alibabacloud.com/help/en/model-studio/model-pricing |
| qwen3.7-max (intl) | Alibaba | $2.50 | $7.50 | $0.50 implicit / $0.25 explicit | idem |
| qwen3.7-flash (≤32K) | Alibaba | $0.03 | $0.13 | $0.006 implicit / $0.003 explicit | idem |

**Tabla opencode.ai Zen (reseller — ES LA QUE APLICA a provider-a/b/c/d, que pasan por zen/go/v1):**

| Modelo | Input $/M | Output $/M | Cached Read $/M | Cached Write $/M |
|---|---|---|---|---|
| deepseek-v4-flash | $0.14 | $0.28 | $0.028 | — |
| deepseek-v4-flash-free | Free | Free | Free | — |
| deepseek-v4-pro | $1.74 | $3.48 | $0.145 | — |
| minimax-m3 | $0.30 | $1.20 | $0.06 | — |
| glm-5.2 | $1.40 | $4.40 | $0.26 | — |
| kimi-k2.7-code | $0.95 | $4.00 | $0.19 | — |
| qwen3.7-max | $2.50 | $7.50 | $0.50 | $3.125 |
| qwen3.7-plus | $0.40 | $1.60 | $0.04 | $0.50 |

Fuente: opencode.ai/docs/zen — accedido 06 Ago 2026.

**Tabla opencode.ai Go (APLICA a los providers go-*; acceso 30 Ago 2026):**

| Modelo | Input $/M | Output $/M | Cached Read $/M | Fuente |
|---|---|---|---|---|
| glm-5.3-flash | $0.15 | $0.50 | $0.03 | opencode.ai/docs/go (acceso 30 Ago) |

Nota: los 8 modelos agregados al catálogo el 31 Ago (§6.1) no tienen tasa Go publicada en models.dev (provider `opencode-go` sin pricing) → sin entrada en el pricing de mofgw (costo 0, regla declarativa).

### Hallazgos clave

1. **DeepSeek V4 Pro: discrepancia 4×.** Zen cobra $1.74/$3.48 (list price); DeepSeek directo cobra $0.435/$0.87 (promo, "plans to raise pricing"). **CORRECCIÓN 11 Ago 2026:** los providers go-* apuntan a `opencode.ai/zen/go/v1` = el **servicio GO de opencode** (no Zen), que cobra las tasas Go: deepseek-v4-pro **$0.435/$0.87** (cached $0.003625) y deepseek-v4-flash cache **$0.0028**. Verificado en models.dev opencode-go + opencode.ai/docs/go. Para costos de provider-a/b/c/d (go-*) → usar tasas **Go**, no Zen.
2. **Alibaba cache explícito vs implícito:** implícito (automático) = 20% del input; explícito (con cache_control) = 10% read / 125% write. Los "Cached Read" de Zen matchean el explícito.
3. **MiniMax M3 tiered:** 2× si input > 512K.
4. **Qwen3.7-flash tiered por input:** ≤32K: $0.03/$0.13; 32-256K: $0.10/$0.40; 256K-1M: $0.20/$0.80. No aparece en Zen.
5. **No verificado:** qwen3.7-flash en Zen (no listado); fecha efectiva del aumento de DeepSeek.

## 2. Cache de providers — soporte y reporting (verificado 06 Ago 2026)

| Provider / Modelo | Cache | Detección (campos exactos) | Gateway debe hacer |
|---|---|---|---|
| DeepSeek (v4-flash/pro) | **Automático** (siempre on, sin opt-in) | `usage.prompt_cache_hit_tokens` + `prompt_cache_miss_tokens` (NO estándar); `prompt_tokens == hit + miss` | Normalizar a `prompt_tokens_details.cached_tokens` si se apunta directo |
| Alibaba bailian (qwen3.7-*, deepseek, glm vía bailian) | **Implícito (automático, default) + explícito** (`cache_control: {type:"ephemeral"}`) | `usage.prompt_tokens_details.cached_tokens` + `cache_creation_input_tokens` (formato OpenAI-compatible nativo) | Para streaming: `stream_options.include_usage=true` nosotros (bailian NO lo inyecta) |
| MiniMax (m3) | **Automático** (implícito) vía OpenAI-compatible | `usage.prompt_tokens_details.cached_tokens` | nada (implícito) |
| GLM/Zhipu (glm-5.2) | **Automático** (implícito) | `usage.prompt_tokens_details.cached_tokens` | nada |
| Kimi (kimi-k2.7-code) | **Automático** para k2.* | `usage.cached_tokens` (top-level, Moonshot) Y `prompt_tokens_details.cached_tokens` (ambos poblados) | leer cualquiera; forward opcional `prompt_cache_key` |
| **opencode.ai zen** (provider-a/b/c/d) | **Pass-through** — cachea upstream y normaliza | **Ya devuelve formato estándar**: `usage.prompt_tokens_details.cached_tokens` (confirmado en source code `normalizeUsage` + docs Bifrost) | **Nada — ya inyecta `stream_options.include_usage` automáticamente** en streaming |

### Hallazgos clave

1. **Cache de providers: ya activo en TODOS.** DeepSeek automático (mín. 64 tokens, prefijo desde token 0), Alibaba implícito+explícito, MiniMax/GLM/Kimi automáticos. **Ningún provider usa headers HTTP** para cache — todo va en el body `usage`.
2. **Zen normaliza todo por nosotros:** `normalizeUsage` lee `cached_tokens` y `prompt_tokens_details.cached_tokens` upstream y devuelve formato OpenAI estándar al cliente; además inyecta `stream_options.include_usage` en streaming. **Conclusión para 003-001: para los 4 providers go-* no hay NADA que habilitar — el trabajo es instrumentar la lectura del usage que ya llega.**
3. **Único provider que requiere acción nuestra:** Alibaba bailian directo (provider qwen) — hay que inyectar `stream_options.include_usage=true` en streaming nosotros (bailian NO lo hace). Cache explícito con `cache_control` queda fuera de scope inicial (implícito ya cubre).
4. **Única inconsistencia de campo:** DeepSeek directo usa `prompt_cache_hit_tokens` (no estándar) — pero vía Zen ya está normalizado. Si algún día apuntamos a DeepSeek directo, normalizar.
5. **Riesgo conocido upstream:** bug #14795 de zen — `usage` puede ser `undefined` y causar 500. Relevante para 006-001: el accounting no debe crashear si usage falta.

## 3. Consumo de /v1/models por runtimes (verificado 06 Ago 2026)

### Hallazgos de fuente (opencode Go source + openclaw TypeScript source)

**OpenCode (Go):** consulta `/v1/models` SOLO para providers locales con env `LOCAL_ENDPOINT`; para providers configurados usa catálogo estático. Lee `max_context_length`/`loaded_context_length` (NO `context_window`), usa `loaded_context_length` como context window con **fallback 4096** si ausente → compactación agresiva. **Hardcodea `CanReason: true` para todos los modelos locales** → siempre manda `reasoning_effort` (low/medium/high) y `max_completion_tokens`. Defaults: contexto 4096, effort medium.

**OpenClaw:** consulta `/v1/models` en discovery/setup de providers self-hosted (GET {baseUrl}/models). Lee en orden: `context_length` → `context_window` → `context_size` → `meta.n_ctx_train`, con **fallback 128,000**; max tokens fallback 8,192. **Reasoning por heurística de nombre** (regex `/r1|reasoning|think|reason/i`). Como gateway propio, sirve /v1/models con solo los 4 campos estándar.

**Convenciones de campo del ecosistema:**

| Campo | Quién lo lee | Prioridad |
|---|---|---|
| `context_length` | OpenClaw (1º), OpenRouter clients | **P0** |
| `max_context_length` | OpenCode Go (local) | **P0** |
| `loaded_context_length` | OpenCode Go (1º para local) | **P0** |
| `max_completion_tokens` | OpenClaw (max output) | P1 |
| `capabilities.reasoning` | OpenClaw (LM Studio path) | P1 |
| `supported_parameters` | OpenClaw (OpenRouter path, busca "tools"/"reasoning") | P2 |
| `context_window` | OpenClaw (2º fallback) | P2 |
| `modality`, `top_provider.context_length` | OpenClaw | P3 |

### Implicaciones para mofgw (EPIC-007)

1. **Exponer `context_length` + `max_context_length` + `loaded_context_length` + `max_completion_tokens` + `capabilities.reasoning`** en /v1/models corrige los defaults equivocados de ambos runtimes (4096 en OpenCode Go, 128k/8k en OpenClaw).
2. **OpenCode Go manda `reasoning_effort` siempre** (CanReason hardcodeado) → mofgw debe pasarlo transparente (hoy lo es: body crudo) y declarar en el catálogo qué niveles soporta cada modelo (ver §4: deepseek-v4-flash low/high/max, kimi-k2.7-code rechaza `disabled`).
3. Verificación empírica local (opencode 1.18.4): todos los modelos mofgw salen con `reasoning:false`, `context:0`, `cost:0` → confirma que hoy los runtimes no tienen metadata → compactación con default desconocido.

*(§3 completada tras re-lanzamiento de delegación — common-lavender-reptile falló silenciosamente, civic-turquoise-turkey entregó el reporte completo con fuentes de código fuente.)*

## 4. Capacidades de thinking por modelo (verificado 06 Ago 2026)
| Modelo | Thinking | Niveles | Activación (OpenAI-compatible) | Default | Contexto / Max output | Fuente |
|---|---|---|---|---|---|---|
| deepseek-v4-flash | ✅ | `low`, `high`, `max` | `extra_body={"thinking":{"type":"enabled"}}` + `reasoning_effort=low/high/max` | ON, effort=high | 1M / 384K | api-docs.deepseek.com/guides/thinking_mode/ |
| deepseek-v4-pro | ✅ | `high`, `max` (3-tier esperado early Ago 2026) | idem | ON, effort=high | 1M / 384K | api-docs.deepseek.com/api/create-chat-completion |
| minimax-m3 | ✅ | `adaptive` (default), `disabled` | `extra_body={"thinking":{"type":"adaptive"}}` + `reasoning_split=True/False` | ON (adaptive) | 1M / 512K (128K rec.) | platform.minimax.io/docs/api-reference/text-openai-api |
| glm-5.2 | ✅ | `max`(def), `xhigh`, `high`, `medium`, `low`, `minimal`, `none` | `extra_body={"thinking":{"type":"enabled"}}` + `reasoning_effort=` | ON (dynamic), effort=max | 1M / 128K | docs.z.ai/guides/capabilities/thinking |
| kimi-k2.7-code | ✅ SIEMPRE ON | — (no desactivable, no controlable) | Nada; `disabled` da error; `thinking.keep` siempre `"all"`; temperature fija 1.0 | ALWAYS ON | 262K / 32K | platform.kimi.ai/docs/guide/use-kimi-k2.7-code-quickstart |
| qwen3.7-plus/max/flash | ✅ (hybrid) | on/off binario + `thinking_budget` + `reasoning_effort` | `extra_body={"enable_thinking":true}` + `thinking_budget=<int>` | ON | 1M / 64K (thinking), CoT máx 256K | help.aliyun.com/en/model-studio/deep-thinking |

### Hallazgos clave

1. **Parámetros no estándar** (`thinking`, `enable_thinking`, `reasoning_effort`, `thinking_budget`) van por `extra_body` — mofgw debe pasarlos transparentes (hoy lo es) y el catálogo (EPIC-007) debe declarar qué soporta cada modelo.
2. **Reasoning tokens se facturan como output** en todas las plataformas → el accounting (006-001) debe contarlos bien.
3. **Effort mapping varía por modelo:** deepseek-v4-pro mapea low→high, xhigh→max; glm-5.2 mapea low/medium→high, xhigh→max. Un `reasoning_effort` pedido puede resolverse distinto. El catálogo debe declarar los niveles REALES por modelo, no asumidos (decisión discovery).
4. **kimi-k2.7-code es un caso aparte:** thinking incontrolable + temperature fija + razonamiento preservado obligatorio en multi-turno. No aplica "optimizar low/high/max" — va siempre ON.
5. **Multi-turno:** la mayoría exige preservar `reasoning_content` histórico (especialmente Kimi y GLM Preserved Thinking). Es un riesgo de interop para el catálogo.

---
*Actualizado: 2026-08-06T13:25-03:00 — §1 (precios), §2 (cache providers), §3 (consumo /v1/models), §4 (thinking) TODOS verificados con fuentes. Discovery del replanteo de eficiencia COMPLETO.*

## 5. Verificación EPIC-010 — catálogo fiel (10 Ago 2026)

> Consolidación de la auditoría del 10 Ago (delegaciones `ref:brainy-white-narwhal` D1-D5 y `ref:desirable-tan-seahorse` D6-D8, acceso 2026-08-10). Corrige §3/§4 donde quedaron desactualizados.

### 5.1 Consumo de /v1/models por OpenClaw — detalle exacto (verificado en source, acceso 10 Ago)

**Path OpenRouter** (`src/agents/embedded-agent-runner/openrouter-model-capabilities.ts`):

| Campo | Prioridad | Default si ausente |
|---|---|---|
| `top_provider.context_length` | **1º** | 128,000 |
| `context_length` | 2º | 128,000 |
| `top_provider.max_completion_tokens` | **1º** | 8,192 |
| `max_completion_tokens` | 2º | 8,192 |
| `max_output_tokens` | 3º | 8,192 |
| `supported_parameters` ([]string) | `includes("tools")` → supportsTools; `includes("reasoning")` → reasoning | `[]` |
| `modality` (string `"inputs->outputs"`) | `split("->")[0].includes("image")` → input+image | `""` → text-only |

**Path LM Studio** (self-hosted): `context_length` → `context_window` → `context_size` → `meta.n_ctx_train` (fallback 128k); max fallback 8,192; `capabilities.reasoning` boolean.

**Implicación mofgw (EPIC-010):** emitir `top_provider` (context_length + max_completion_tokens), `max_output_tokens`, `supported_parameters` y `modality` (string) — ver spec 010-001 P1-P8.

### 5.2 Capacidades por modelo — tools y visión (verificado 10 Ago)

| Modelo | Tools | Visión | Fuente (acceso 10 Ago) |
|---|---|---|---|
| deepseek-v4-flash | ✅ (≤128 funciones, parallel, strict) | ❌ no documentado | api-docs.deepseek.com (function_calling, thinking_mode) |
| deepseek-v4-pro | ✅ | ❌ no documentado | api-docs.deepseek.com |
| deepseek-v4-flash-0731 | ✅ | ❌ no documentado | api-docs.deepseek.com + HF DeepSeek-V4-Flash-0731 |
| minimax-m3 | ✅ (native tool use + interleaved thinking) | ✅ text+image+**video** (`image_url`/`video_url`) | platform.minimax.io/docs/guides/text-m3-function-call |
| glm-5.2 | ✅ (tool calls + `tool_stream=true`) | ❌ text-only (la visión es GLM-5V-Turbo, modelo aparte) | docs.z.ai/guides/llm/glm-5.2 |
| kimi-k2.7-code | ✅ (multi-step + `tool_choice`) | ✅ text+image+**video** (base64/upload; png/jpeg/webp/gif + mp4/mov) | platform.kimi.ai/docs (use-kimi-vision-model) |
| qwen3.7-plus | ✅ (function calling + built-in tools) | ✅ text+image+**video** (≤2048 imgs, 64 videos, 2h) | alibabacloud.com/help/en/model-studio/vision-model |

### 5.3 Correcciones de datos vs §4 (verificado 10 Ago)

| Ítem | §4 (06 Ago) | Verificado 10 Ago | Fuente |
|---|---|---|---|
| qwen3.7-plus max_output | 64K | **131,072 (128K)** — la página del modelo dice max output 131072 con y sin thinking; el 64K era la tabla de vision-models (max `max_tokens` user-settable). CoT max 262,144. | help.aliyun.com/zh/model-studio/qwen3-7-plus + qwencloud.com/models/qwen3.7-plus |
| deepseek en bailian: thinking default | "hybrid ON" | **OFF por default** (bailian requiere `enable_thinking: true`); directo DeepSeek API = ON. El default depende del endpoint. | help.aliyun.com/en/model-studio/deep-thinking (sección DeepSeek) |
| context windows minimax/glm/qwen | 1M | Docs oficiales dicen **1,000,000** (no 1,048,576); deepseek 1,048,576 verificado por errores upstream (TECHDEBT #23). Verificación empírica del techo real pendiente (Phase 4, no cambiar sin evidencia). | docs de cada provider + TECHDEBT #23 |

### 5.4 Mapa de activación de thinking por (modelo × provider-path) — base de la inyección 010-002 (verificado 10 Ago)

| Modelo | Path | Parámetro a inyectar | Default nativo |
|---|---|---|---|
| deepseek-v4-flash/pro | zen/go (go-*) | `reasoning_effort` (pass-through) | high (directo) / **OFF (bailian)** |
| deepseek-v4-flash/pro | bailian (qwen) | `reasoning_effort` + `enable_thinking: true` | OFF |
| glm-5.2 | zen/go | `reasoning_effort` (pass-through) | max |
| glm-5.2 | bailian | `reasoning_effort` (high/max only) + `enable_thinking: true` | ON (dynamic) |
| glm-5.3 | zen/go (go-*) | `reasoning_effort` (pass-through) | high (prescriptivo); **rechaza `medium`** [1210] (verificado 16 Ago) |
| glm-5.3-flash | zen/go (go-*) | `reasoning_effort` (pass-through) | high (prescriptivo); **rechaza `medium`/`none`** [1210] (verificado 30 Ago) |
| minimax-m3 | zen/go (Anthropic-compat) | zen convierte; thinking type adaptive | adaptive |
| kimi-k2.7-code | cualquier | **NUNCA inyectar** (`disabled` → ERROR) | always-on |
| qwen3.7-plus | bailian | `enable_thinking: true` (thinking_budget opcional) | ON |

Regla general: parámetros no soportados se ignoran silenciosamente (OpenAI-compat); única excepción kimi (`disabled` → error). → Decisión 1.3: inyección per-attempt (router, provider-aware).

## 6. Sync catálogo opencode — declaración de endpoints (31 Ago 2026)

> Sincronización del catálogo de mofgw con lo que DECLARAN los endpoints de opencode
> (acceso 31 Ago 2026): `https://opencode.ai/zen/go/v1/models` (33 modelos) y
> `https://opencode.ai/zen/v1/models` (catálogo completo; solo `-free` + `big-pickle` gratis).
> context_window / max_output / modality salen de models.dev/api.json (provider
> `opencode-go` para GO, `opencode` para free; `hy3-preview` solo presente en `tencent-tokenhub`).

### 6.1 Providers go-*: 33 modelos, todo lo declarado

Los 7 providers `go-*` pasan de 26 a **33 modelos** (listas idénticas; los errores
transitorios por chat los absorbe el fallback):

- **+8 verificados por chat (200, 31 Ago):** `longcat-2.0`, `deepseek-v4-flash-vision-exp`,
  `qwen3.7-max` (re-ingreso — excluido 16 Ago por inestable), `qwen3.8-max`,
  `qwen3.8-flash`, `qwen3.6-plus` (re-ingreso), `qwen3.5-plus` (re-ingreso), `hy4-preview`.
- **+6 por declaración:** `grok-4.5` (solo `/responses`), `grok-4.6` (solo `/responses`),
  `mimo-v2-pro`, `mimo-v2-omni`, `hy3-preview`, `muse-spark-1.2-contributor` (errores
  transitorios por chat el 31 Ago: 400/401/500 — quedan listados por declaración).

| Modelo | ctx | max_output | input modalities |
|---|---|---|---|
| longcat-2.0 | 1,000,000 | 131,072 | text |
| deepseek-v4-flash-vision-exp | 1,000,000 | 384,000 | text+image |
| qwen3.7-max | 1,000,000 | 65,536 | text |
| qwen3.8-max | 1,000,000 | 131,072 | text+image+video+pdf |
| qwen3.8-flash | 1,000,000 | 131,072 | text+image+video |
| qwen3.6-plus | 1,000,000 | 65,536 | text+image+video |
| qwen3.5-plus | 1,000,000 | 65,536 | text+image+video |
| hy4-preview | 1,024,000 | 64,000 | text |
| grok-4.5 | 500,000 | 500,000 | text+image |
| grok-4.6 | 500,000 | 500,000 | text+image |
| mimo-v2-pro | 1,048,576 | 128,000 | text |
| mimo-v2-omni | 262,144 | 128,000 | text+image+audio+pdf |
| hy3-preview | 256,000 | 64,000 | text |
| muse-spark-1.2-contributor | 1,048,576 | 131,072 | text+image+video+pdf+audio |

> Nota longcat-2.0: models.dev declara valores distintos según provider — nano-gpt (1,048,756 / 262,144) vs **opencode-go (1,000,000 / 131,072)**. Como los go-* apuntan al servicio opencode-go, en config/mofgw consta **1,000,000 / 131,072** (31 Ago).

**Thinking:** no declarado por los endpoints → **omitido en la metadata** (fallback rule:
no se adivina). **Pricing:** sin tasa publicada → **sin entrada (costo 0)**.

### 6.2 Tier free: 8 modelos en los 7 providers go-*-free

`big-pickle` (202,752 / 32,768) + los 7 `-free` de `/zen/v1`:
`deepseek-v4-flash-free` (200K / 128K), `muse-spark-1.2-contributor-free` (1M / 131K),
`mimo-v2.5-free` (200K / 32K), `ling-3.0-flash-fin-free` (262K / 32K),
`nemotron-3-ultra-free` (1M / 128K), `nemotron-3.5-lightning-free` (262K / 262K),
`laguna-s-2.1-free` (256K / 32K).

⚠️ Verificado 31 Ago: las keys actuales responden `401 "Model is disabled"` para los 7
`-free` (el plan free depende de la cuenta/workspace) → quedan declarados; el fallback
los absorbe. Se habilitan solos cuando una cuenta tenga plan free.

### 6.3 GLM-5.3-Flash (ficha completa, verificado 30-31 Ago)

- **Precio Go:** $0.15 / $0.50 input/output, cache-hit $0.03 por 1M (opencode.ai/docs/go, 30 Ago).
- **Thinking (probe 30 Ago):** acepta `low`/`high`/`max`; **rechaza `medium` y `none`**
  con error `[1210] "This model always engages in thinking and cannot be disabled; please
  use low, high, or max"`. En metadata: `thinking: [low, high, max]`, `thinking_default: high`
  (prescriptivo, ADR-003).
- **Especificación:** Z.ai blog 26 Ago — 320B params / 18B activos, multimodal nativo, 1M ctx,
  alias `ox-alpha`. models.dev familia glm-5.3: ctx 1M / max_output 131,072 (confirmar con el
  primer response grande).
- **En catálogo:** 7 providers `go-*` + pricing + metadata.

---
*Actualizado: 2026-08-31 — §6 (sync catálogo opencode: 33 GO + 8 free declarados 31 Ago; GLM-5.3-Flash pricing + thinking verificado) agregado; §1 (tasa Go glm-5.3-flash) y §5.4 (glm-5.3/glm-5.3-flash) complementados.*
