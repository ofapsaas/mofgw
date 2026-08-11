# Spec — 010-001-catalogo-fiel: catálogo completo, veraz y prescriptivo en /v1/models

---
feature_id: 010-001-catalogo-fiel
epic: mofgw-010-catalogo-fiel
status: approved
approved_by: Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo 10 Ago 2026)
approved_at: 2026-08-11
created_at: 2026-08-10
updated_at: 2026-08-11
depends_on: 007-001 (ModelMetadata en config), 007-002 (/v1/models enriquecido), 006-002 (ModelPricing — sin cambios)
paralelizable: no
---

## Contexto

`GET /v1/models` (007-002) ya expone `context_length`, `max_completion_tokens`,
`capabilities.reasoning` y `thinking` desde la metadata declarativa (007-001).
Quedan huecos de fidelidad: los runtimes leen más campos que hoy no existen
(`supported_parameters`, `modality`, `top_provider.*`, `max_output_tokens`), y
algunos datos configurados no son veraces (qwen3.7-plus max_output).

### Hallazgos verificados (fecha de acceso 2026-08-10; delegación ref:brainy-white-narwhal y ref:desirable-tan-seahorse — fuentes de código fuente de openclaw/openrouter)

- **D1 — Lecturas de OpenClaw (path OpenRouter):** `top_provider.context_length`
  (1º) / `context_length` (2º, fallback 128k); `top_provider.max_completion_tokens`
  (1º) / `max_completion_tokens` (2º) / `max_output_tokens` (3º, fallback 8192);
  `supported_parameters` ([]string, busca `includes("tools")` e
  `includes("reasoning")`); `modality` string `"inputs->outputs"` parseada vía
  `split("->")[0].includes("image")`. Path LM Studio: `context_length` →
  `context_window` → `context_size` → `meta.n_ctx_train`; `capabilities.reasoning`
  boolean.
- **D2 — Tools:** los 6 modelos verificados con function calling:
  deepseek-v4-flash/pro, minimax-m3, glm-5.2, kimi-k2.7-code, qwen3.7-plus.
- **D3 — Vision:** minimax-m3 SÍ (text+image+**video**, vía `image_url`/`video_url`),
  kimi-k2.7-code SÍ (text+image+**video**, base64/upload, png/jpeg/webp/gif + mp4/mov),
  qwen3.7-plus SÍ (text+image+**video**, hasta 2048 imágenes / 64 videos);
  deepseek-v4-flash/pro NO documentado; glm-5.2 NO (text-only; la visión es el
  modelo aparte GLM-5V-Turbo).
- **D4 — deepseek-v4-flash-0731:** context 1,048,576, max_output 384,000,
  thinking levels [low, high, max], tools sí. Default prescriptivo: low (misma
  familia que deepseek-v4-flash). Solo lo sirve el provider `qwen` (bailian).
- **D5 — Formato canónico de modality:** top-level `modality` string
  `"inputs->outputs"` (lo parsea OpenClaw) + `architecture.input_modalities`
  (array estructurado canónico). Entradas: text, image, file, audio, video.
  Salidas: text, image, embeddings, audio, video, rerank, speech, transcription.
- **D8 (ref:desirable-tan-seahorse) — Corrección de datos:** qwen3.7-plus
  `max_output` es **131,072** (128K), NO 65,536 como está configurado y como
  dice research §4. Context 1M. El catálogo debe reflejar 131072.

### Decisiones de diseño (cerradas, no reabrir)

1. **Declarativo:** se extiende `ModelMetadata` con `supported_parameters
   []string` y `modality string`. Zero-value → campo omitido del catálogo.
   Backward compatible (yaml.v2 ignora claves desconocidas).
2. **thinking_default prescriptivo:** el campo documenta el effort que los
   agentes DEBEN enviar (puede diferir del default nativo del modelo). El
   ADR-003 registra la semántica. Valores en prod (sin cambio):
   deepseek-v4-flash low, deepseek-v4-pro high, minimax-m3 adaptive, glm-5.2
   medium, kimi-k2.7-code always, qwen3.7-plus high.
3. **Fallback rule:** capabilities no verificadas desde docs oficiales se
   OMITEN, nunca se adivinan (documentado en TECHDEBT).
4. **Backward compatibility:** envelope `{object:list, data:[...]}` intacto;
   modelo sin metadata → entrada mínima; pricing y auth intactos.

### Decisiones de datos (elección explícita)

- **Modality honesta negativa:** deepseek-v4-flash/pro, deepseek-v4-flash-0731 y
  glm-5.2 declaran `"text->text"`. La cadena declara SOLO las modalidades
  verificadas; la ausencia de "image" en un lado significa "no documentada como
  soportada" (negativa honesta), no una conjetura. Es consistente con la fallback
  rule porque cada lado solo contiene modalidades verificadas. Para minimax-m3,
  kimi-k2.7-code y qwen3.7-plus se declara `"text+image+video->text"` (D3
  verifica video en los tres; la cadena completa evita sub-declarar).
- **Coherencia de datos (check de deploy):** `"reasoning" ∈ supported_parameters`
  ⇔ `thinking` no vacío (D2 + research §4: los 6 modelos tienen thinking).

## Postcondiciones

1. **P1 — Config extensible y backward compatible:** el config YAML acepta
   `model_metadata.<model>.supported_parameters` (lista de strings) y
   `model_metadata.<model>.modality` (string), accesibles tipado desde
   `config.ModelMetadata`. Un config SIN esas claves carga sin error con
   zero-values (`supported_parameters` nil, `modality` ""). Un config existente
   de 007-001 (sin las claves nuevas) carga exactamente como antes, sin
   validaciones nuevas.

2. **P2 — Zero-value → campo ausente:** para un modelo CON metadata,
   `supported_parameters` se emite solo si la lista configurada no está vacía;
   `modality`, `architecture`, `top_provider` y `max_output_tokens` se emiten
   solo si sus valores fuente son no-vacíos/no-cero. Modelo con metadata pero sin
   esos valores → campos AUSENTES (nunca `[]`, `{}`, `0` ni `false`).

3. **P3 — supported_parameters fiel:** modelo con
   `supported_parameters: ["tools","reasoning"]` → el objeto del catálogo emite
   `supported_parameters` con EXACTAMENTE esos strings y en ese orden
   (`["tools","reasoning"]`). No se ordena, no se deduplica, no se agrega ningún
   parámetro no declarado en config.

4. **P4 — modality y architecture derivada:** modelo con
   `modality: "text+image+video->text"` → emite `modality: "text+image+video->text"`,
   `architecture.modality: "text+image+video->text"`,
   `architecture.input_modalities: ["text","image","video"]`,
   `architecture.output_modalities: ["text"]`. Derivación mecánica: inputs =
   lado izquierdo de `->` partido por `+`; outputs = lado derecho partido por
   `+`; cada lado no vacío. Para `"text->text"` → `input_modalities: ["text"]`,
   `output_modalities: ["text"]`.

5. **P5 — modality no parseable → architecture omitida:** si `modality` no
   contiene `->` o algún lado queda vacío (ej. `"text"`, `"text->"`), el catálogo
   emite `modality` con el string declarado (passthrough de dato declarado) pero
   NO emite `architecture` (la derivación no es posible; ausente ≠ adivinado).

6. **P6 — top_provider y max_output_tokens:** modelo con metadata con
   `context_window > 0` y `max_output > 0` → emite
   `top_provider.context_length == context_window`,
   `top_provider.max_completion_tokens == max_output` y
   `max_output_tokens == max_output`. Si `context_window == 0` → se omite
   `top_provider.context_length`; si `max_output == 0` → se omiten
   `top_provider.max_completion_tokens` y `max_output_tokens`.

7. **P7 — thinking_default prescriptivo:** modelo con `thinking_default`
   configurado → `thinking.default` es EXACTAMENTE el string configurado, aunque
   difiera del default nativo del modelo (documenta el effort que el agente
   DEBE enviar). Ej.: `deepseek-v4-flash` con `thinking_default: "low"` →
   `thinking.default == "low"` (el nativo del modelo es high). Sin
   `thinking_default` → `thinking.default` ausente.

8. **P8 — Envelope, mínimos, pricing y auth intactos:** `GET /v1/models`
   responde `{"object":"list","data":[...]}`; cada ítem tiene `id`,
   `object:"model"`, `created`, `owned_by`; modelo SIN metadata → solo esos 4
   campos (sin campos nuevos); modelo con pricing (006-002) → `pricing` con
   `input_usd_per_m`, `output_usd_per_m`, `cache_hit_usd_per_m` (sin cambios
   respecto de 007-002); el endpoint sigue exigiendo Bearer válido (401 sin
   auth).

## Invariantes

- I1: Backward compatible — un cliente que solo lee `id` sigue funcionando;
  envelope y campos mínimos intactos (reafirma 007-002 I1).
- I2: Los valores provienen de la metadata declarativa (007-001) — nunca
  hardcodeados en este código (extiende 007-001 I2).
- I3: Fallback rule — una capability no declarada/verificada se omite;
  ausente ≠ 0/[]/{}; ninguna derivación inventa capabilities.
- I4: La derivación de `architecture` es puramente mecánica desde la cadena
  `modality` — sin semántica externa ni fuentes adicionales.
- I5: La cardinalidad del endpoint es la misma (modelos configurados).
- I6: Suite completa `go test ./... -race` verde, cero red externa.

## Criterios de aceptación

- C1: Modelo con metadata completa (context_window 1000000, max_output 384000,
  thinking [low,high,max], thinking_default low, supported_parameters
  [tools,reasoning], modality "text+image+video->text") → el objeto JSON emite,
  con valores exactos: context_length 1000000, max_completion_tokens 384000,
  max_output_tokens 384000, capabilities.reasoning true, thinking.levels
  [low,high,max], thinking.default "low", supported_parameters [tools,reasoning],
  modality "text+image+video->text", architecture {modality "text+image+video->text",
  input_modalities [text,image,video], output_modalities [text]}, top_provider
  {context_length 1000000, max_completion_tokens 384000}.
- C2: Modelo sin metadata → objeto mínimo (id/object/created/owned_by), sin
  campos nuevos.
- C3: Modelo con metadata pero SIN supported_parameters/modality declarados →
  supported_parameters, modality y architecture (derivado directo de modality)
  AUSENTES (fallback rule). top_provider y max_output_tokens NO son derivados
  de las claves nuevas: son alias derivados de context_window/max_output
  (007-002) y se emiten cuando esos valores son > 0 (P6).
- C4: Modelo con `modality: "text"` (no parseable) → `modality` presente con
  valor "text", `architecture` ausente.
- C5: Modelo con `thinking_default: "low"` → `thinking.default == "low"` aunque
  el modelo tenga thinking nativo distinto.
- C6: Envelope, auth y pricing intactos (reafirmación de 007-002 C5 + auth
  Bearer 401 sin key).
- C7: `config.example.yaml` actualizado: documenta `supported_parameters` y
  `modality` en `model_metadata`, refleja los thinking_default prescriptivos
  (sección "Datos esperados" abajo) y qwen3.7-plus `max_output: 131072` (D8).
- C8: Suite completa verde: `go test ./... -race`, `go vet`, gofmt limpios.

### Datos esperados (Phase 4 — deploy / config.example.yaml)

| Modelo                 | supported_parameters | modality             | thinking_default (prescriptivo) | max_output   |
| ---------------------- | -------------------- | -------------------- | ------------------------------- | ------------ |
| deepseek-v4-flash      | [tools, reasoning]   | text->text           | low                             | 384000       |
| deepseek-v4-pro        | [tools, reasoning]   | text->text           | high                            | 384000       |
| deepseek-v4-flash-0731 | [tools, reasoning]   | text->text           | low                             | 384000       |
| minimax-m3             | [tools, reasoning]   | text+image+video->text | adaptive                      | (sin cambio) |
| glm-5.2                | [tools, reasoning]   | text->text           | medium                          | (sin cambio) |
| kimi-k2.7-code         | [tools, reasoning]   | text+image+video->text | always                        | (sin cambio) |
| qwen3.7-plus           | [tools, reasoning]   | text+image+video->text | high                          | **131072** (D8)  |

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`: `ModelMetadata` +=
  `SupportedParameters []string \`yaml:"supported_parameters"\`` y
  `Modality string \`yaml:"modality"\``. Sin validación nueva (P1 — configs
  007-001 existentes intactos).
- `internal/proxy/proxy.go modelCatalogEntry`: cuando hay metadata, emitir
  adicionalmente (regla zero→omitido):
  - `supported_parameters`: passthrough si `len > 0`.
  - `modality`: passthrough si `!= ""`.
  - `architecture`: `{modality: <mismo string>, input_modalities: [...],
    output_modalities: [...]}` solo si la cadena es parseable (split `->`, lados
    no vacíos; split `+`).
  - `top_provider`: `{context_length, max_completion_tokens}` con campos
    emitidos solo si la fuente > 0.
  - `max_output_tokens`: si `MaxOutput > 0`.
- `config.example.yaml`: documentar campos nuevos + tabla de datos esperados.
- Tests: `e2e_010001_test.go` (patrón `e2e_007002_test.go`: harness +
  SetModelMetadata/SetPricing + GET /v1/models + asserts JSON) para C1-C6;
  `config_test.go` para P1. Nota: las aserciones negativas de P2/P5 y el
  passthrough de P7 pueden ya estar verdes por 007-002 — el AUDIT (3.0) lo
  confirma; el RED de la feature viene de las positivas P3/P4/P6.

## Fuera de alcance

- Clamp / window-check / inyección de parámetros — feature 010-002 (existe
  `e2e_010002_test.go`; no tocar).
- Config del cliente opencode (v2 usa models.dev, no lee /v1/models).
- Cambios de `context_window` para minimax/glm/qwen (1,000,000 vs 1,048,576 —
  verificación empírica pendiente; NO cambiar valores).
- Descubrimiento automático de capabilities desde el provider — declarativo por
  decisión (007-001).
- ADR-003 (se redacta aparte; este spec documenta la semántica prescriptiva).
- Pricing y auth — sin cambios.

## Cambios
- 2026-08-11: C3 corregido (decisión del dueño, GVR): top_provider/max_output_tokens se removieron de la lista de campos ausentes — son alias de context_window/max_output (P6), no capacidades nuevas sujetas a fallback rule. Contradicción interna C3-vs-P6 detectada en GREEN por el implementer (AP-4 respetado, no tocó tests).

Status: Approved by Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo) on 2026-08-11.
