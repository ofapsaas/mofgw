# Spec — 016-004-adapter-zot: renderer del fragmento config del provider mofgw para zot

---
feature_id: 016-004-adapter-zot
epic: mofgw-016-mofgw-client-config
status: approved
approved_by: Pablo (delegado a orquestador como HITL, 2026-08-22)
approved_at: 2026-08-22
created_at: 2026-08-22
depends_on: 016-001 (IR ConfigIR/ModelEntry + registry clientconfig.Register/SupportedList/Lookup); 016-002/016-003 (patrón de adapters a replicar)
paralelizable: sí (último adapter; independiente de 002/003)
---

## 1. Descripción

`internal/clientconfig/zot` es un adapter concreto del endpoint `/v1/client-config` (epic mofgw-client-config). Implementa un `clientconfig.Renderer` que serializa un `clientconfig.ConfigIR` en el fragmento JSON del provider "mofgw" listo para insertar en el archivo de modelos de **zot** (`patriceckhart/zot`, "Yet another coding agent harness", CLI coding-agent Go agrupado con opencode/openclaw). zot define providers AI personalizados en `$ZOT_HOME/models.json` cuya estructura central es `UserModelsFile`.

Es serialización pura (entrada `ConfigIR`, salida `[]byte` JSON válido y determinista): apunta `providers.mofgw.baseUrl` al IR, fija `api: "openai"` (Chat Completions compatible OpenAI), y lista los modelos como un **ARRAY** de objetos `{id, name?, contextWindow?, maxTokens?}` derivados exclusivamente del IR.

**Decisión clave de diseño (I1):** el formato de zot **no tiene campo `apiKey` en absoluto** — `models.json` jamás almacena secrets. zot resuelve la key en runtime vía flag `--api-key`, una env var **derivada** `UPPER_SNAKE(provider)_API_KEY`, o `auth.json`. Para el provider "mofgw" la env derivada es `MOFGW_API_KEY`. Por tanto este adapter **NO emite template alguno** (`{env:VAR}`/`${VAR}`): emite **cero** campo de key. Esto es una **desviación deliberada** frente a los adapters 002/003 (que sí emiten un template): aquí `IR.KeyEnvRef` **no es representable, no se emite y no es obligatorio** (vacío NO es error). Para alinear con el knob `client_config.key_env`, el operador exporta/define la env que zot ya deriva (`MOFGW_API_KEY`).

**Fuera de alcance (anti-scope):**
- ❌ Escribir/parsear el `models.json` del host (el renderer devuelve el fragmento; quien lo aplica lo aplica). Sin I/O de filesystem.
- ❌ Elegir/operar la key (login, `--api-key`) — resolución de credenciales de zot, fuera del renderer.
- ❌ Providers integrados/catálogo built-in de zot; solo se emite el provider custom `mofgw`.
- ❌ Cambios a 016-001 (IR, endpoint, registry), ni a los adapters 002/003.

## 2. Contrato

### 2.1 Go type (firma pública)

```go
// Renderer serializa un ConfigIR al fragmento UserModelsFile del provider zot.
type Renderer struct{}

func (Renderer) Render(ir clientconfig.ConfigIR) ([]byte, error)
```

Implementa `clientconfig.Renderer` (idéntico a 002/003).

### 2.2 Registro

```go
clientconfig.Register("zot", zot.Renderer{})
```

Registrado bajo el clientID `"zot"` (aparece en `SupportedList()`).

### 2.3 Output JSON exacto (UserModelsFile)

```json
{
  "providers": {
    "mofgw": {
      "baseUrl": "<IR.BaseURL>",
      "api": "openai",
      "models": [
        { "id": "<m0.id>", "name": "<meta display_name>", "contextWindow": N, "maxTokens": N },
        { "id": "<m1.id>" }
      ]
    }
  }
}
```

**Difiere de openclaw (003):** top-level **`"providers"`** (no `"models"`), **sin** clave `mode`, `api: "openai"` (no `"openai-completions"`), y **NO** hay campo `apiKey`.

### 2.4 Ejemplo completo

```go
clientconfig.ConfigIR{
	ClientID: "zot",
	BaseURL:  "https://llm.example.com/v1",
	Models: []clientconfig.ModelEntry{
		{ ID: "my-model-a", Meta: map[string]any{"display_name":"My Model A","context_length":32000,"max_output_tokens":8192} },
		{ ID: "my-model-b", Meta: map[string]any{} },
	},
}
```

Salida determinista:
```json
{"providers":{"mofgw":{"api":"openai","baseUrl":"https://llm.example.com/v1","models":[{"contextWindow":32000,"id":"my-model-a","maxTokens":8192,"name":"My Model A"},{"id":"my-model-b"}]}}}
```

## 3. Invariantes

- **I1 — forma zot, sin secret:** el fragmento NO emite campo `apiKey` de ninguna clase; no hay template (`{env:VAR}`/`${VAR}`) ni valor de secret. La key se resuelve por env **derivada** `UPPER_SNAKE(provider)_API_KEY` (= `MOFGW_API_KEY`). `IR.KeyEnvRef` **no es representable**: no se emite y vacío **no es error**. **Desviación documentada** frente a 002/003 (más fuerte en I1). Para alinear el knob `key_env`, el operador setea `MOFGW_API_KEY`.
- **I2 — modelos del IR:** cada elemento de `models[]` y sus metadatos se derivan **exclusivamente** de `ir.Models`; jamás se inventan ids ni límites.
- **I3 — JSON válido:** salida siempre serializable; fallo antes del marshal → error no-nil, nunca JSON parcial.
- **I4 — baseUrl fiel:** `providers.mofgw.baseUrl == ir.BaseURL`; si `ir.BaseURL` vacío → error no-nil.

## 4. Criterios (postcondiciones testeables)

- **P1** — `Render` produce `[]byte` JSON válido con top-level `{"providers": {...}}`.
- **P2** — `providers.mofgw.baseUrl == ir.BaseURL` (fiel, sin transformación).
- **P3** — el output NO contiene la clave `"apiKey"` ni `"key"`, ni strings template (`${...}`/`{env:...}`). La key se resuelve por env derivada `MOFGW_API_KEY` (I1, P9-dependent).
- **P4** — `providers.mofgw.api == "openai"` y provider accesible bajo clave `"mofgw"` vía registro `Register("zot", ...)`.
- **P5** — para cada `j`, `providers.mofgw.models[j].id == ir.Models[j].ID`; `contextWindow`/`maxTokens` de Meta (fallback) solo si presentes; `name` de `Meta["display_name"]` solo si string no vacío.
- **P6** — el conjunto de `models[*].id` es exactamente `{m.ID : m ∈ ir.Models}`; sin extras.
- **P7** — `ir.Models` vacío ⇒ `providers.mofgw.models == []` (array, nunca null).
- **P8** — modelo sin metadata ⇒ `models[j] == {"id":"<ID>"}` (solo id).
- **P9** — `ir.BaseURL == ""` ⇒ error no-nil; **`ir.KeyEnvRef == ""` NO es error** (desviación I1, P9-dependent documentado en el test).
- **P10** — `clientconfig.Register("zot", r)` ⇒ `Lookup("zot") != nil`; cleanup scoped (restaura solo "zot").
- **P11** — determinista: dos llamadas mismo IR ⇒ bytes idénticos.
- **P12** — guard `>0` + fallback (lección 003): `contextWindow` de `context_length` fallback `max_context_length`; `maxTokens` de `max_output_tokens` fallback `max_completion_tokens`; se emiten SOLO si presentes Y > 0; presente-pero-≤0 tratado como ausente.

## 5. Mapeo técnico

```
internal/clientconfig/zot/zot.go       # adapter (Renderer + helpers)
internal/clientconfig/zot/zot_test.go  # tests P1..P12
```

**Estructura de `zot.go`** (espeja los helpers de `openclaw.go`; difiere solo en el shape de salida):
- consts: `providerKey="mofgw"`, `apiAdapter="openai"`, `limitKeyContext="context_length"`/`limitKeyContextF="max_context_length"`, `limitKeyOutput="max_output_tokens"`/`limitKeyOutputF="max_completion_tokens"`, `nameKey="display_name"`.
- `type Renderer struct{}` + `Render(ir)`:
  1. Guard P9: `if ir.BaseURL == "" { return error }` — **sin** guard de KeyEnvRef (desviación I1).
  2. `models := make([]any, 0, len(ir.Models))`; por cada m append `modelFragment(m)` (array).
  3. `mofgw := map[string]any{"baseUrl": ir.BaseURL, "api": apiAdapter, "models": models}` — sin `apiKey`.
  4. `json.Marshal(map[string]any{"providers": map[string]any{providerKey: mofgw}})`.
- `modelFragment(m)` — `"id"`; `name` de metaString(display_name) si no vacío; `contextWindow`/`maxTokens` de metaInt con fallback y guard >0 (P12).
- `metaString`/`metaInt` — copia de helpers de openclaw (acepta float64/int/int64/json.Number).

**Deuda documentada (VERIFICAR):** campos opcionales por-modelo de zot (`reasoning`, `reasoningLevelMap`, `priceInput…CacheWrite`, `input`) NO emitidos por defecto (defaults los cubren); enriquecer desde `Meta.modality`/`capabilities` difiere hasta verificar contra catálogo real.

**Registro:** `clientconfig.Register("zot", zot.Renderer{})` idempotente.

---

**Status: Pending approval**