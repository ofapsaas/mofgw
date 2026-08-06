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

### Hallazgos clave

1. **DeepSeek V4 Pro: discrepancia 4×.** Zen cobra $1.74/$3.48 (list price); DeepSeek directo cobra $0.435/$0.87 (promo, "plans to raise pricing"). Para costos de provider-a/b/c/d → usar tasas Zen.
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

## 3. Consumo de /v1/models por runtimes

### Verificación empírica local (06 Ago 2026, opencode 1.18.4)

`opencode models mofgw --verbose` contra el provider mofgw (config opencode.jsonc) devuelve para CADA modelo (`deepseek-v4-flash`, `deepseek-v4-flash-0731`, `deepseek-v4-pro`, ...):

```json
{
  "capabilities": { "reasoning": false, "toolcall": true, "interleaved": { "field": "reasoning_content" } },
  "cost": { "input": 0, "output": 0, "cache": { "read": 0, "write": 0 } },
  "limit": { "context": 0, "output": 0 }
}
```

**Hallazgos:**
1. **`capabilities.reasoning: false` para TODOS los modelos de mofgw** — incluyendo deepseek-v4-flash que SÍ soporta thinking (ver §4). Consecuencia: opencode nunca va a mandar `reasoning_effort` → no puede optimizar low/high/max (frente 3 de Pablo confirmado con datos del sistema real).
2. **`limit.context: 0` y `limit.output: 0`** — opencode no conoce la ventana de contexto → decisiones de compactación con default desconocido.
3. **`cost: 0`** — opencode no puede estimar costo por modelo.
4. opencode obtiene metadata de **models.dev** (base pública) + config estática del provider en opencode.jsonc (`--refresh` refresca "the models cache from models.dev"). El comportamiento exacto de consulta a /v1/models lo confirma la delegación en curso (civic-turquoise-turkey).

*(Sección 3 en curso — delegación re-lanzada tras falla silenciosa de common-lavender-reptile.)*

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
*Actualizado: 2026-08-06T13:10-03:00 — §3 en curso (delegación re-lanzada). §1, §2, §4 verificados con fuentes.*
