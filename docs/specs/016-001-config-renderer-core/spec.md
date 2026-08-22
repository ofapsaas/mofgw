# Spec — 016-001-config-renderer-core: endpoint `/v1/client-config` + IR + registry de renderers

---
feature_id: 016-001-config-renderer-core
epic: mofgw-016-mofgw-client-config
status: approved
approved_by: Pablo (delegado a orquestador como HITL, 2026-08-22)
approved_at: 2026-08-22
created_at: 2026-08-22
depends_on: 007-002 (/v1/models + modelCatalogEntry), 010-001 (catálogo fiel: supported_parameters/modality/architecture), 007-001 (ModelMetadata), 006-002 (pricing), 001-007 (auth /v1)
paralelizable: no (los adapters 016-002/003/004 dependen de este core)
---

## 1. Descripción

El epic 016 agrega un endpoint que devuelve el **fragmento de configuración del provider mofgw** listo para insertar en un cliente soportado (opencode.json, openclaw.conf, zot…). Hoy esa config se sincroniza a mano y se desincroniza cuando el catálogo de modelos cambia.

Esta feature (`016-001`) entrega **el core**, sin adapters: el endpoint `GET /v1/client-config?client=<id>`, la **representación intermedia (IR)** tipada que comparte metadata del catálogo real, el **registro de renderers** (vacío en esta feature — `map[clientID]Renderer` + interfaz `Renderer.Render`), la **auth** detrás de `/v1`, el **knob config** `client_config: {base_url, key_env}` (aditivo), y el manejo de errores OpenAI-compatible (401/404/503).

**Por qué el 200 del core se testea con un renderer registrado de prueba:** en esta feature el registro está vacío (los adapters opencode/openclaw/zot son 016-002/003/004). El registry ES el contrato público del paquete (`map[string]Renderer`/`Register`); registrar un renderer de prueba para ejercitar el path 200 no es mock sobre plumbing, es usar la API pública. Todo lo demás se testea contra el contrato observable del endpoint (404 lista vacía, 503 por base_url, 401 por auth).

**Fuera de alcance (anti-scope, del plan del epic):**
- ❌ Adapters opencode/openclaw/zot (016-002/003/004).
- ❌ Escritura/parseo de archivos de config del host (cero filesystem I/O del lado servidor).
- ❌ Validación sintáctica del fragmento contra el schema del cliente (sin fixture-drift mitigation, excluída por decisión de discovery D4).
- ❌ Bootstrap del cliente (instalar key, montar túnel).
- ❌ Cambiar `/v1/models` ni el enriquecimiento del catálogo (se consume tal cual).

## 2. Contrato

### 2.1 Paquete nuevo `internal/clientconfig` — IR tipado + interfaz Renderer + registry

Este IR es el **contrato compartido** por todos los renderers (001 lo define; 002/003/004 lo consumen en features posteriores).

```go
package clientconfig

// ConfigIR es la representación intermedia que recibe todo Renderer.
type ConfigIR struct {
    ClientID  string       // id del cliente autenticado (auth.ClientIDFrom)
    BaseURL   string       // knob client_config.base_url — vivo en el IR
    KeyEnvRef string       // p.ej. "MOFGW_KEY" — NUNCA el valor del secret
    Models    []ModelEntry // metadata COMPLETA del catálogo, no formato mínimo
}

// ModelEntry transporta la metadata del catálogo de 007-002/010-001,
// reusando modelCatalogEntry (misma fuente de verdad en memoria).
type ModelEntry struct {
    ID   string
    Meta map[string]any // objeto /v1/models enriquecido (id, object, created,
                        // owned_by, context_length, capabilities, thinking,
                        // supported_parameters, modality, architecture, pricing…)
}

// Renderer serializa el IR al formato del cliente.
type Renderer interface {
    Render(ir ConfigIR) ([]byte, error)
}
```

**Registry** (contrato público del paquete, testable sin mocks):

```go
// Renderers es el registry mutable de renderers por clientID.
// Inspectable/registrable por tests a través de la API pública.
var Renderers = map[string]Renderer{}

// Register asocia un renderer a un clientID.
func Register(clientID string, r Renderer)

// SupportedList devuelve los clientIDs registrados (para el 404).
func SupportedList() []string

// Lookup devuelve el renderer de un clientID, o nil.
func Lookup(clientID string) Renderer
```

### 2.2 Endpoint

| Aspecto       | Contrato                                                                                                                     |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| **Método + path** | `GET /v1/client-config`                                                                                                        |
| **Query params**  | `client=<id>` — obligatorio; ausente o vacío → `400 invalid_request_error` `"missing required query param: client"`                |
| **Auth**          | misma que /v1 (via `auth.Wrap`; la ruta queda autenticada por estar bajo `/v1/` y no en publicPrefixes). Sin Bearer válido → `401` |
| **200**           | body = bytes del renderer del cliente (Content-Type según renderer; `application/json` por convención)                         |
| **401**           | envelope OpenAI `invalid_api_key` (lo emite `auth.Wrap`, no el handler)                                                          |
| **404**           | cliente desconocido → `not_found_error` + lista de clientes soportados en el mensaje                                           |
| **503**           | `client_config.base_url` vacío → `server_error` `"client_config.base_url not set"`                                                 |
| **Additivo**      | cero regresión en rutas `/v1/*` existentes                                                                                     |

**Envelope de error (OpenAI-compatible, via `openAIError`):**
```json
{"error": {"message": "<msg>", "type": "<typ>", "code": null}}
```

**Mapeo de estados (orden de precedencia en el handler):**
1. `client` ausente/vacío → **400** `invalid_request_error` `"missing required query param: client"` (se evalúa PRIMERO: un request sin `client` es malformado independientemente de la config).
2. `base_url` vacío → **503** `server_error` `"client_config.base_url not set"` (sin fallback a otro base_url; se evalúa aún con un cliente registrado).
3. cliente no registrado → **404** `not_found_error` con mensaje `"unsupported client \"<id>\"; supported: <lista>"` (lista vacía en esta feature).
4. `Renderer.Render(ir)` error → **500** `server_error` (error de serialización del renderer).

**Contenido del IR en el handler `handleClientConfig`:**
- `ClientID` = `auth.ClientIDFrom(r.Context())`.
- `BaseURL` = knob `client_config.base_url` (seteado pre-tráfico, patrón `SetContextAnalysis`).
- `KeyEnvRef` = knob `client_config.key_env` (por defecto `"MOFGW_KEY"`).
- `Models` = iteración/dedupe idéntica a `handleModels` (mismo `seen` map y `modelCatalogEntry(model)`), construida sobre `s.providers` (`interface{ Models() []string }`, patrón 13-003 D3/P6). Sin duplicación de la lógica de catálogo.

### 2.3 Knob config (aditivo, off-safe)

```go
// internal/config/config.go
type ClientConfigConfig struct {
    BaseURL string `yaml:"base_url"` // vacío → endpoint 503 en runtime (NO fail-fast)
    KeyEnv  string `yaml:"key_env"`  // env-ref de la key; default "MOFGW_KEY"
}
```
- `Config` gana `ClientConfig ClientConfigConfig \`yaml:"client_config"\``.
- `defaults()`: `KeyEnv: "MOFGW_KEY"`, `BaseURL: ""` (por defecto → 503, coherente con I4).
- `validate()`: NO falla con `base_url` vacío (off-safe; la decisión es runtime 503, NO error de carga fail-fast — a diferencia de telemetry/registry que exigen `file`). Si se valida algo, solo no-negatividad/cero-size; `BaseURL` vacío es un estado válido y documentado.
- No se resuelve ninguna env var en `resolveKeys()`: `KeyEnvRef` viaja como **referencia**, nunca se lee el valor.

## 3. Invariantes

- **I1 — Key nunca literal:** el fragmento y la respuesta NUNCA contienen el valor del secret; solo `KeyEnvRef` (referencia a env var). El knob `key_env` es la ref; mofgw no lee ni emite el valor. El IR tiene `KeyEnvRef`, nunca el valor.
- **I2 — Catálogo es la fuente de verdad viva:** `Models` se construye reusando `modelCatalogEntry` sobre el catálogo en memoria (metadata 007-001 + pricing 006-002), con la misma iteración/dedupe que `handleModels`. Nunca hardcodeado en este código ni en el IR.
- **I3 — Aditivo, cero regresión:** la única ruta nueva es `GET /v1/client-config`; `/v1/models` y el resto de `/v1/*` quedan intactos. `client_config` bloque ausente/vacío en config no cambia ningún comportamiento existente (off-safe backward compatible).
- **I4 — base_url vacío → 503, no fallback silencioso:** sin `client_config.base_url` configurado el endpoint responde **503** `client_config.base_url not set`, explícito. NO se inventa un base_url, NO se cae a un default hardcodeado ni a otro host, NO se degrada silenciosamente. Esto es runtime, no fail-fast de arranque.

## 4. Criterios (postcondiciones testeables, P1..PN)

Cada postcondición mapea a comportamiento observable. Sin mocks sobre plumbing: el único registry poblado es la API pública `Register` (para ejercitar el path 200), y el resto se testea contra el contrato del endpoint con un cliente HTTP real. Sin targets de coverage.

- **P1 — 401 sin auth:** `GET /v1/client-config` sin `Authorization` válido → `401` con envelope `{"error":{"type":"invalid_api_key"}}`. (Cubre que la ruta nueva queda bajo `/v1` auth, no en publicPrefixes.)
- **P2 — 400 sin client param:** con auth válida y `client` ausente/vacío → `400` `invalid_request_error` `"missing required query param: client"`.
- **P3 — 404 cliente desconocido con lista:** con `client=noexiste` y `base_url` seteado → `404` `not_found_error` con el mensaje conteniendo `supported:` y la lista actual del registry (vacía en esta feature). Cada registro posterior vía `Register` cambia la lista observable en ese mensaje (el registry es la fuente).
- **P4 — 503 base_url vacío:** con `client` válido pero `client_config.base_url` no seteado (default) → `503` `server_error` `"client_config.base_url not set"`, independientemente del valor de `client` (inclusive un cliente registrado). No hay fallback silencioso a otro base_url.
- **P5 — 200 con renderer registrado (path core):** registrando un renderer de prueba vía `clientconfig.Register("test", r)` (API pública), con `base_url` seteado, `GET /v1/client-config?client=test` → `200` y el body es exactamente lo que `r.Render(ir)` devolvió.
- **P6 — IR fiel al catálogo (fuente de verdad viva, I2):** el renderer de prueba recibe un `ConfigIR` cuyo `Models` coincide con la iteración deduplicada de `handleModels`: la MISMA lista de modelos y, para cada modelo con metadata/pricing, la MISMA metadata que `/v1/models` emite (mismas keys/valores: context_length, capabilities, thinking, supported_parameters, modality, architecture, pricing…). Modelos sin metadata siguen apareciendo (mismo comportamiento que /v1/models). No hardcodeado: cambiar el catálogo cambia el IR.
- **P7 — IR con base_url y key_env vivos:** `ConfigIR.BaseURL == client_config.base_url` seteado y `ConfigIR.KeyEnvRef == client_config.key_env` (default `"MOFGW_KEY"`). Cambiar el knob cambia el IR.
- **P8 — Key nunca en la respuesta (I1):** en NINGÚN estado (200, 400, 404, 503) el body contiene el valor del secret ni la cadena de la env var resuelta; solo aparece `KeyEnvRef`. El IR transporta `KeyEnvRef`, nunca el valor; mofgw no resuelve ni emite el valor.
- **P9 — Envelope OpenAI-compatible, cero regresión:** los errores del endpoint usan el formato `openAIError` (`{"error":{message, type, code}}`). La suite `/v1/*` existente sigue pasando; `/v1/models` no cambia su shape ni su cardinalidad; los tests verdes existentes del proxy siguen verdes (sin red externa).
- **P10 — Registry es contrato público (no plumbing):** `clientconfig.Register`, `SupportedList` y `Lookup` operan sobre el mapa exportado; registrar/consultar es parte de la API del paquete, no un mock. Esto garantiza que el path de 200 y el 404 son testeables sin fixtures externos (decisión de discovery D4, sin fixture-drift mitigation).

## 5. Mapeo técnico

### Read-only (fuentes de verdad, no se tocan)
- `internal/proxy/proxy.go` — `handleModels` (~1167) y `modelCatalogEntry` (~1198): patrones de iteración/dedupe y de construir el objeto enriquecido por modelo (REUSAR, sin duplicar). `openAIError(w,status,msg,typ)` (~488): envelope de error. `Server` struct (rutas read-only para entender campos `providers`, `auth`, setters). `auth.Wrap` via `internal/auth/auth.go` (`Wrap` ~94): la ruta nueva queda autenticada por estar bajo `/v1/`.
- `internal/config/config.go` — `Config` (~295), `defaults()` (~363), `validate()` (~495); patrón knobs aditivos.
- `cmd/mofgw/main.go` — wiring pre-tráfico (`srv.SetPricing`, `srv.SetModelMetadata`, `srv.SetContextAnalysis` ~267, `srv.SetEmbeddings` ~299) → donde insertar `srv.SetClientConfig`.
- `config.example.yaml` — convenciones de documentación de knob.
- `docs/specs/010-001-catalogo-fiel/spec.md`, `docs/specs/007-002-models-endpoint/spec.md` — estilo casa y precedentes.

### Nuevo (a crear en la implementación)
- **`internal/clientconfig/clientconfig.go`** — `ConfigIR`, `ModelEntry`, `Renderer`, registry exportado (`Renderers`, `Register`, `SupportedList`, `Lookup`). Cero dependencias salientes nuevas (sin llamadas HTTP externas → ADR-005 no aplica).

### Hooks / funciones a tocar
- `internal/config/config.go`:
  - `Config` → campo nuevo `ClientConfig ClientConfigConfig \`yaml:"client_config"\``.
  - `ClientConfigConfig` struct nuevo (`base_url`, `key_env` con yaml tags).
  - `defaults()` → `KeyEnv: "MOFGW_KEY"`, `BaseURL: ""`.
  - `validate()` → (opcional) validación de no-negativos/cero; `BaseURL` vacío es estado válido (no fail-fast — es el 503 de runtime).
- `internal/proxy/proxy.go`:
  - `Server` → campo nuevo `clientConfigBaseURL string` + `clientConfigKeyEnv string` (inmutables post-setter, patrón `contextAnalysis` ~85).
  - Setter pre-tráfico `SetClientConfig(baseURL, keyEnv string)` (patrón `SetContextAnalysis` ~404).
  - `Handler()` (~361-373) → `mux.HandleFunc("GET /v1/client-config", s.handleClientConfig)`.
  - `handleClientConfig(w, r)` nuevo → 503/400/404/200 + construcción del IR reusando `modelCatalogEntry` + llamada a `clientconfig.Lookup`.
- `cmd/mofgw/main.go` → tras `srv.SetModelMetadata` (~248) o junto a los otros setters: `srv.SetClientConfig(cfg.ClientConfig.BaseURL, cfg.ClientConfig.KeyEnv)`.

### Tests (orientativo, no authoring)
- `internal/clientconfig`: contrato del paquete — `ConfigIR`/`ModelEntry`/`Renderer`, registro/lookup del registry (`P10`), y que el IR lleva `KeyEnvRef` nunca el valor (`P8`).
- `internal/proxy`: e2e del endpoint real — `401` (`P1`), `400` (`P2`), `404` lista (`P3`), `503` base_url vacío (`P4`), `200` con renderer de prueba registrado vía `clientconfig.Register` (`P5`), IR fiel al catálogo (`P6`), base_url/key_env vivos (`P7`), header/body sin secret (`P8`), envelope OpenAI-compatible + cero regresión en `/v1/*` (`P9`).
- `internal/config`: parseo del knob `client_config` con defaults y ausencia de fail-fast por base_url vacío.

---

**Status: Pending approval**