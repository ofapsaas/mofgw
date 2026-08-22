# Spec — 016-003-adapter-openclaw: renderer del fragmento config del provider mofgw para OpenClaw

---
feature_id: 016-003-adapter-openclaw
epic: mofgw-016-mofgw-client-config
status: approved
approved_by: Pablo (delegado a orquestador como HITL, 2026-08-22)
approved_at: 2026-08-22
created_at: 2026-08-22
depends_on: 016-001 (IR ConfigIR/ModelEntry + registry clientconfig.Register/SupportedList/Lookup); 016-002 (adapter-opencode, patrón a replicar)
paralelizable: sí (con 002/004)
---

## 1. Descripción

Un `clientconfig.Renderer` que serializa un `ConfigIR` en el **fragmento config del provider `mofgw`** para OpenClaw, siguiendo el patrón del adapter `opencode` (016-002): serialización pura, determinista, entrada `ConfigIR`, salida `[]byte`.

El fragmento se inserta en el archivo de config de OpenClaw bajo la rama `models.providers.mofgw`. Es **serialización pura** (sin I/O de filesystem, sin mocks sobre plumbing): el renderer NO lee/escribe `openclaw.json` del host — devuelve el fragmento.

**Corrección de investigación (importante):** el archivo de config de OpenClaw **no se llama `openclaw.conf` ni usa TOML/YAML**. Es **`~/.openclaw/openclaw.json`** (formato **JSON5**):

> "OpenClaw reads an optional **JSON5** config from `~/.openclaw/openclaw.json`." — https://docs.openclaw.ai/gateway/configuration

JSON5 es superconjunto de JSON: toda salida de `encoding/json` es un fragmento JSON5 válido. El renderer emite JSON estricto (JSON5-compatible), que el host inserta en su `openclaw.json`.

**Hallazgo crítico de research — sintaxis de env-ref en OpenClaw:** OpenClaw referencia variables de entorno en valores string de config con el template **STRING** `"${VAR_NAME}"` (no `{env:VAR}` de opencode, no objeto SecretRef):

> "Reference env vars in any config string value with `${VAR_NAME}`" / `"apiKey": "${CUSTOM_API_KEY}"` — https://docs.openclaw.ai/gateway/configuration
> "Env var substitution in config … `"apiKey": "${VERCEL_GATEWAY_API_KEY}"`" — https://docs.openclaw.ai/help/environment

El mapeo es `IR.KeyEnvRef` → `providers.mofgw.apiKey = "${<KeyEnvRef>}"`. Nótese que el código fuente de OpenClaw normaliza `${ENV_VAR}` y admite también el nombre desnudo de la env var como marcador; el patrón **documentado y canónico** es `${VAR}`. Se elige `${VAR}`.

**Fuera de alcance (anti-scope):**
- ❌ Escritura/parseo del `openclaw.json` del host (el renderer devuelve el fragmento; quien lo aplique lo aplica).
- ❌ La rama `agents.defaults.models` / `modelPolicy.allow` (restricción del usuario) — si el usuario restringe, añade refs `mofgw/<id>` manualmente, consecuencia de config, fuera del renderer.
- ❌ Adapters opencode `002` / zot `004`, y cambios a 016-001 (IR, registry, endpoint).
- ❌ Validación del fragmento contra el schema actual de OpenClaw vía fixture externo (sin fixture-drift mitigation, decisión del epic).

## 2. Contrato

**Go type** (implementa `clientconfig.Renderer`):

```go
// internal/clientconfig/openclaw/openclaw.go
package openclaw

type Renderer struct{}
func (Renderer) Render(ir clientconfig.ConfigIR) ([]byte, error)

// uso en wiring:
clientconfig.Register("openclaw", openclaw.Renderer{})
```

Registrado bajo el clientID `"openclaw"` (aparecerá en `SupportedList()` → lista del 404).

**Formato de salida:** JSON estricto (JSON5-compatible), fragmento a insertar/mergear bajo la rama `models` de `openclaw.json`.

**Shape exacto de salida:**

```json5
{
  "models": {
    "mode": "merge",                                // merge (default) | replace
    "providers": {
      "mofgw": {
        "baseUrl": "https://mofgw.invalid/v1",      // ← IR.BaseURL
        "apiKey": "${MOFGW_KEY}",                   // ← "${" + IR.KeyEnvRef + "}"
        "api": "openai-completions",                // OpenAI-compatible /v1/chat/completions
        "models": [
          {
            "id": "<model.ID>",                     // ← IR.Models[i].ID (obligatorio)
            "name": "<display_name si Meta lo aporta>",   // opcional
            "contextWindow": <Meta.context_length | max_context_length>,   // opcional
            "maxTokens":  <Meta.max_output_tokens | max_completion_tokens> // opcional
          }
        ]
      }
    }
  }
}
```

**Claves/valores EXACTOS:**

| Clave | Origen | Nota |
|---|---|---|
| `models.mode` | const `"merge"` | merge (default); evita que la rama reemplace el catálogo al mergear |
| `models.providers."mofgw"` | const `"mofgw"` | key del mapa de providers (ref = `mofgw/<id>`) |
| `baseUrl` | `IR.BaseURL` (string) | `baseUrl` canónico (OpenClaw acepta `baseURL` legacy, usa `baseUrl`) |
| `apiKey` | `"${" + IR.KeyEnvRef + "}"` | env-ref STRING `${VAR}`; nunca literal ni objeto SecretRef |
| `api` | const `"openai-completions"` | adapter para endpoints self-hosted `/v1/chat/completions` |
| `models[]` | `IR.Models` | array de entrada por modelo (NO objeto keyed) |
| `models[].id` | `IR.Models[i].ID` | requerido; debe coincidir con lo que la API espera en el body |
| `models[].name` | opcional; `Meta["display_name"]` | si ausente/vacío, se omite |
| `models[].contextWindow` | `Meta["context_length"]` (fallback `max_context_length`) | solo si > 0 presente; 'context' del opencode ↔ 'contextWindow' |
| `models[].maxTokens` | `Meta["max_output_tokens"]` (fallback `max_completion_tokens`) | solo si > 0 presente; 'output' del opencode ↔ 'maxTokens' |

Fallback-rule tipo 010-001: campo emitido solo si su fuente declarada no es zero-value (ausente ≠ 0/[]/{}).

**Campos OpenClaw-opcionales NO emitidos por defecto (VERIFICAR en implementación):** OpenClaw acepta por modelo `reasoning` (bool), `input` (`["text"]` default), `cost` (default `{input:0,output:0,cacheRead:0,cacheWrite:0}`). Como el default cubre su ausencia y el IR no garantiza esas claves de `Meta`, el renderer los **omite** (minimalismo fiel, sin inventar shape). Enriquecer `reasoning`/`input` desde `Meta` (modality/capabilities) queda como **VERIFICAR** contra datos reales del catálogo antes de añadirlo — deuda, no blocker.

Fuentes del shape:
- File/json5 config `~/.openclaw/openclaw.json`, env-ref `${VAR}` string template (no objeto) — https://docs.openclaw.ai/gateway/configuration
- `models.providers.*.{baseUrl, apiKey, api, models[]}` con `api: "openai-completions"`; modelo `{id, name, contextWindow, maxTokens}` — https://github.com/openclaw/openclaw/blob/main/docs/concepts/model-providers.md y https://github.com/openclaw/openclaw/blob/main/docs/gateway/config-tools.md
- `models.mode: "merge" | "replace"` — https://github.com/openclaw/openclaw/blob/main/docs/gateway/config-tools.md
- env-ref `${VAR}` substitution — https://docs.openclaw.ai/help/environment

## 3. Invariantes (heredadas del epic)

- **I1 — Key solo como env-ref:** `providers.mofgw.apiKey = "${<IR.KeyEnvRef>}"` (string template `${VAR}`). Jamás se emite el secret ni el valor fuera del template; nunca objeto SecretRef. `IR.KeyEnvRef` vacío → error.
- **I2 — Modelos del catálogo:** `models[]` se construye exclusivamente desde `IR.Models`/`IR.Models[i].Meta`. Cero ids hardcodeados.
- **I3 — Formato válido:** salida JSON estricto parseable con `json.Unmarshal` (JSON5-compatible; determinista, orden estable).
- **I4 — Fiel a `IR.BaseURL`:** `providers.mofgw.baseUrl == IR.BaseURL`; no hay base_url hardcodeada.

## 4. Criterios (postcondiciones testeables)

Adapter = serialización pura ⇒ tests serializan un `ConfigIR` y recorren el JSON de salida. Sin mocks sobre plumbing.

- **P1** — Con `BaseURL="https://gw/v1"`, `KeyEnvRef="MOFGW_KEY"`, 2 modelos: `Render` devuelve bytes **JSON válido** (`json.Unmarshal` sin error), top-level con la clave `"models"`.
- **P2** — `models.providers.mofgw.baseUrl == "https://gw/v1"` (fiel al IR).
- **P3** — `models.providers.mofgw.apiKey == "${MOFGW_KEY}"`: env-ref STRING `${<key>}`; nunca objeto SecretRef, nunca un string tipo key-legítima (`sk-…`) como valor.
- **P4** — `models.providers.mofgw.api == "openai-completions"` y key del provider `"mofgw"`.
- **P5** — Para cada `ir.Models[i]` existe `models.providers.mofgw.models[]` con `id == ir.Models[i].ID`; si `Meta["context_length"]` ⇒ `contextWindow == ese valor`; si `Meta["max_output_tokens"]` ⇒ `maxTokens == ese valor`.
- **P6** — `models` no contiene ids fuera de `IR.Models`.
- **P7** — `IR.Models` vacío ⇒ fragmento válido con `models.providers.mofgw.models` array vacío `[]`.
- **P8** — Modelo sin metadata (`context_length`/`max_output_tokens`/`display_name` ausentes) ⇒ entrada `models[]` válida solo con `id`.
- **P9** — `IR.BaseURL` vacío o `IR.KeyEnvRef` vacío ⇒ `Render` error no-nil.
- **P10** — `clientconfig.Register("openclaw", r)` ⇒ `Lookup("openclaw") != nil` y `"openclaw" ∈ SupportedList()` (API pública, sin mock).
- **P11** — Orden determinista: dos llamadas con el mismo IR producen bytes idénticos.
- **P12** — Presente-pero-≤0 (fallback-rule): `context_length:0` presente ⇒ sin `contextWindow`; `max_completion_tokens:0` con `max_output_tokens` ausente ⇒ sin `maxTokens`; key fallback (`max_context_length`/`max_completion_tokens`) sola ⇒ valor del fallback. (Lección aprendida del hallazgo #1 de la review de 002; se escribe desde RED.)

## 5. Mapeo técnico

| Touchpoint | Dónde | Qué |
|---|---|---|
| Renderer | Nuevo `internal/clientconfig/openclaw/openclaw.go` | struct `Renderer{}` implementando `Render(ir) ([]byte, error)`; construye `{models:{mode,providers:{mofgw:{...}}}}` y `json.Marshal`. Reusa `internal/clientconfig`. |
| Modelo→límites mapping | helper puro en `openclaw/` | leer `Meta["context_length"]`/`Meta["max_output_tokens"]` (fallback `max_context_length`/`max_completion_tokens`) → `contextWindow`/`maxTokens`, y `Meta["display_name"]` → `name`, con fallback-rule y guarda >0 (P12). |
| Registro | wiring | `clientconfig.Register("openclaw", openclaw.Renderer{})`, consistente con cómo 002/004 registran opencode/zot. |
| Tests | Nuevo `internal/clientconfig/openclaw/openclaw_test.go` | unidad P1-P12 (serializar IR, recorrer JSON, verificar `${MOFGW_KEY}`). E2E del endpoint es del epic. |
| Sin cambios | `internal/proxy/*`, `internal/config/*` | 001 ya define IR, endpoint y registry; 003 solo aporta un Renderer nuevo. |

---

**Status: Pending approval**