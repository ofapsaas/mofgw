# Spec — 016-002-adapter-opencode: renderer del fragmento config del provider mofgw para opencode.json

---
feature_id: 016-002-adapter-opencode
epic: mofgw-016-mofgw-client-config
status: approved
approved_by: Pablo (delegado a orquestador como HITL, 2026-08-22)
approved_at: 2026-08-22
created_at: 2026-08-22
depends_on: 016-001 (IR ConfigIR/ModelEntry + registry clientconfig.Register/SupportedList/Lookup)
paralelizable: no (es el primer adapter; depende del core 001)
---

## 1. Descripción

Un `clientconfig.Renderer` que serializa un `ConfigIR` en el **fragmento JSON del provider `mofgw`** listo para insertar bajo la clave `provider` de `opencode.json`. Apunta a `options.baseURL = IR.BaseURL` (OpenAI-compatible), referencia la API key por env-var, declara `npm: "@ai-sdk/openai-compatible"` y lista los modelos derivados de `IR.Models` (catálogo real, no hardcodeado). Salida: `[]byte` JSON válido.

Es **serialización pura** (sin I/O, sin mocks sobre plumbing): entrada `ConfigIR`, salida bytes. Es el primer adapter concreto del epic (016-001 definió el core; openclaw `003` y zot `004` siguen).

**Hallazgo crítico de research — sintaxis de env-ref en opencode:** la doc oficial usa template **STRING** `"{env:VARIABLE_NAME}"` (no un objeto `{"env": …}`), y también `"{file:path}"`.

> "Use `{env:VARIABLE_NAME}` to substitute environment variables: … `"apiKey": "{env:ANTHROPIC_API_KEY}"`" — https://opencode.ai/docs/config/ (Env vars)

Por tanto el renderer mapea `IR.KeyEnvRef` → `options.apiKey = "{env:<KeyEnvRef>}"`, nunca un objeto ni el valor literal.

**Fuera de alcance (anti-scope):**
- ❌ Adapters openclaw `003` / zot `004`.
- ❌ Escritura/parseo del `opencode.json` del host (el renderer devuelve el fragmento; el host lo inserta).
- ❌ Validación del config resultante contra el schema de opencode (sin fixture-drift mitigation, decisión del epic).
- ❌ Cambiar nada de 016-001 (IR, endpoint, registry) — solo agrega un Renderer nuevo.

## 2. Contrato

**Go type** (implementa `clientconfig.Renderer`):

```go
// internal/clientconfig/opencode/opencode.go
package opencode

type Renderer struct{}
func (Renderer) Render(ir clientconfig.ConfigIR) ([]byte, error)

// uso en wiring:
clientconfig.Register("opencode", opencode.Renderer{})
```

Registrado bajo el clientID `"opencode"` (aparecerá en `SupportedList()` → lista del 404 y del fragmento).

**Shape exacto de salida** (valor a insertar bajo `"provider"` de `opencode.json`):

```jsonc
{
  "mofgw": {
    "npm": "@ai-sdk/openai-compatible",
    "name": "mofgw",
    "options": {
      "baseURL": "https://mofgw.invalid/v1",   // ← IR.BaseURL
      "apiKey": "{env:MOFGW_KEY}"              // ← "{env:" + IR.KeyEnvRef + "}"
    },
    "models": {
      "<model.ID>": {
        "name": "<display name si Meta lo aporta, opcional>",
        "limit": {
          "context": <Meta.context_length | max_context_length>,
          "output":  <Meta.max_output_tokens | max_completion_tokens>
        }
      }
    }
  }
}
```

**Claves/valores EXACTOS:**

| Clave | Origen | Nota |
|---|---|---|
| `npm` | const `"@ai-sdk/openai-compatible"` | requerido en custom providers para `/v1/chat/completions` |
| `name` | `"mofgw"` | display name del provider |
| `options.baseURL` | `IR.BaseURL` (string) | endpoint API |
| `options.apiKey` | `"{env:" + IR.KeyEnvRef + "}"` | ref-env STRING; nunca literal ni objeto |
| `models.<id>` | `IR.Models[i].ID` | key del mapa de modelos |
| `models.<id>.name` | opcional; de `Meta` si hay display name útil | si ausente, se omite la clave |
| `models.<id>.limit.context` | `Meta["context_length"]` (fallback `max_context_length`) | solo si > 0 presente |
| `models.<id>.limit.output` | `Meta["max_output_tokens"]` (fallback `max_completion_tokens`) | solo si > 0 presente |

Fallback-rule tipo 010-001 (P2): campo emitido solo si su fuente declarada no es zero-value (ausente ≠ 0/[]/{}).

Fuentes del shape:
- Custom provider: `npm`, `name`, `options.baseURL`, `options.apiKey` opcional, `models` = mapa id → `{name, limit:{context,output}, options, variants}`. — https://opencode.ai/docs/providers/
- env-ref `{env:VAR}` string template, no objeto. — https://opencode.ai/docs/config/
- `models.<id>.limit` con `context`/`output`. — https://opencode.ai/docs/providers/
- Full model id = `provider_id/model_id`. — https://opencode.ai/docs/models/

## 3. Invariantes (heredadas del epic)

- **I1 — Key solo como env-ref:** output contiene `options.apiKey = "{env:<IR.KeyEnvRef>}"`. Jamás se emite el secret ni el valor fuera de env-template. `IR.KeyEnvRef` vacío → error (no emitir key a medias).
- **I2 — Modelos del catálogo:** `models.*` se construye exclusivamente desde `IR.Models`/`IR.Models[i].Meta` (fuente de verdad de `/v1/models`). Cero ids hardcodeados.
- **I3 — JSON sintácticamente válido:** salida parseable con `json.Unmarshal` (determinista, orden estable).
- **I4 — Fiel a `IR.BaseURL`:** `options.baseURL == IR.BaseURL`; no hay base_url hardcodeada.

## 4. Criterios (postcondiciones testeables)

Adapter = serialización pura ⇒ tests serializan un `ConfigIR` y recorren el JSON de salida. Sin mocks sobre plumbing.

- **P1** — Con `BaseURL="https://gw/v1"`, `KeyEnvRef="MOFGW_KEY"`, 2 modelos: `Render` devuelve bytes **JSON válido** (`json.Unmarshal` sin error), top-level con la clave `"mofgw"`.
- **P2** — `mofgw.options.baseURL == "https://gw/v1"` (fiel al IR; no hardcodeada).
- **P3** — `mofgw.options.apiKey == "{env:MOFGW_KEY}"`: ref-env STRING `{env:<key>}`; nunca objeto `{"env":…}`, nunca un string tipo key-legítima (`sk-…`) como valor. El IR no transporta el secret, así que el renderer no puede emitirlo.
- **P4** — `mofgw.npm == "@ai-sdk/openai-compatible"` y `mofgw.name == "mofgw"`.
- **P5** — Para cada `ir.Models[i]` existe `mofgw.models["<id>"]`; si `Meta["context_length"]` presente ⇒ `limit.context == ese valor`; si `Meta["max_output_tokens"]` presente ⇒ `limit.output == ese valor`.
- **P6** — `mofgw.models` no contiene ids fuera de `IR.Models` (deriva solo del catálogo).
- **P7** — `IR.Models` vacío ⇒ fragmento válido con `mofgw.models` objeto vacío `{}` (sin error).
- **P8** — Modelo sin metadata (`context_length`/`max_output_tokens` ausentes) ⇒ `models.<id>` objeto válido **sin** la clave `limit` (no emite ceros ni claves muertas).
- **P9** — `IR.BaseURL` vacío o `IR.KeyEnvRef` vacío ⇒ `Render` retorna error no-nil (nada de fragmento incompleto silencioso).
- **P10** — Compatibilidad con 001: `clientconfig.Register("opencode", r)` ⇒ `Lookup("opencode") != nil` y `"opencode" ∈ SupportedList()` (API pública del paquete, sin mock).
- **P11** — Orden determinista: dos llamadas con el mismo IR producen bytes idénticos (apto diff-check del host).

## 5. Mapeo técnico

| Touchpoint | Dónde | Qué |
|---|---|---|
| Renderer | Nuevo `internal/clientconfig/opencode/opencode.go` | struct `Renderer{}` implementando `Render(ir) ([]byte, error)`; construye el mapa de salida y `json.Marshal`. Reusa `internal/clientconfig` (ConfigIR, ModelEntry). |
| Modelo→limit mapping | helper puro en `opencode/` | leer `Meta["context_length"]`/`Meta["max_output_tokens"]` (fallback `max_context_length`/`max_completion_tokens`) con fallback-rule; solo emitir si presente. |
| Registro | wiring (main del worker o `init()` del subpkg) | `clientconfig.Register("opencode", opencode.Renderer{})`. Elegir patrón consistente con cómo 003/004 registrarán openclaw/zot. |
| Tests | Nuevo `internal/clientconfig/opencode/opencode_test.go` | unidad P1-P9/P11 (serializar IR arbitrario, recorrer JSON). E2E del endpoint (client=opencode) es del epic, no de esta feature. |
| Sin cambios | `internal/proxy/*`, `internal/config/*` | 001 ya define IR, endpoint y registry; 002 solo aporta un Renderer nuevo. |

---

**Status: Pending approval**