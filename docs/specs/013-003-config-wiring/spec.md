---
feature_id: 013-003-config-wiring
feature_name: config-wiring
epic: 013-mofgw-cli-subprocess
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-12
created_at: 2026-08-12
depends_on: 013-001-subprocess-core, 013-002-adapter-claude
paralelizable: sí
---

# Spec — 013-003-config-wiring

Proyecto: `github.com/ofapsaas/mofgw` (Go)

## 1. Descripción

Cablea el provider `subprocess` (motor 013-001 + adapter claude 013-002) en el proxy en
ejecución: config `type: subprocess`, el factory de providers, el catálogo `/v1/models`
(que lista los modelos subprocess marcados SIN tools), el manejo de `tools` en el request
path para esos modelos (OMIT/STRIP), y la restricción "solo agentes del usuario". Es la
feature que hace el provider subprocess realmente usable por un cliente.

**Decisiones de diseño (ver §Cambios):**
- **Config en `ProviderConfig` (D1):** extiende el struct existente con `Type` (discriminador)
  + campos subprocess (`Backend, Command, SessionDir, BackendFlags, Clients`). `type` vacío o
  `"http"` = comportamiento actual (backward compatible, cero cambio). `type: subprocess`
  requiere `backend`; no requiere `base_url`/`api_key_env`.
- **Factory con switch (D2):** `buildProvider`/`buildBackend` en un nuevo paquete
  `internal/providerfactory` (importable, testeable con `command` inyectable) construyen
  `provider.NewClient(...)` para HTTP o `subprocess.NewProvider(..., claude.New(command), ...)`
  para subprocess. `buildBackend(backend, command)` = switch sobre `backend` (claude hoy;
  gemini/codex a futuro, motor intacto). `main.go` lo llama como adapter fino.
- **Catálogo sin tools (D3):** `internal/subprocess.Provider` gana un accessor
  `Models() []string` (aditivo); `handleModels` usa una type-assert de interfaz
  `interface{ Models() []string }` en lugar de `*provider.Client`. La metadata de los modelos
  subprocess (context_window, max_output, supported_parameters SIN "tools") es DECLARATIVA
  en `model_metadata` del config (fallback rule: nunca se adivina).
- **Tools OMIT (D4):** los `tools` de un request a un modelo subprocess se OMITEN (nunca
  llegan al CLI) — garantizado por `TranslateReq` del motor (D4/I4 de 001); el catálogo marca
  el modelo no-tools. No hay reject ni cambio en proxy/router para tools.
- **Restricción "solo agentes del usuario" (D5):** allowlist de `clientID` en
  `providers[].clients`, aplicada en el router excluyendo el provider subprocess de los
  candidatos cuando el `clientID` del request no está en la allowlist (así el fallback sigue
  funcionando para clientes generales). Allowlist vacía = sin restricción (warning en config).
- **Tests herméticos (D6):** stub CLI + factory con command inyectable, sin binario `claude`
  real, sin suscripción, cero red externa.

## 2. Contrato

### 2.1 Superficie

- **Config:** `ProviderConfig` gana `Type, Backend, Command, SessionDir, BackendFlags, Clients`
  (yaml). `ProviderSpec` gana `AllowedClients []string`.
- **Factory:** `buildProvider(pc config.ProviderConfig, logger *slog.Logger) (provider.Provider, error)`
  y `buildBackend(backend, command string) (subprocess.Backend, error)`.
- **Motor:** `(*subprocess.Provider).Models() []string` (nuevo accessor aditivo).
- **Proxy:** `handleModels` lista providers que implementan `Models()`.

### 2.2 Postcondiciones (P1..P10)

Cada postcondición es de comportamiento observable (por el config loader, el factory, el
catálogo HTTP, o el request path) e individualmente testeable.

- **P1 — Config `type: subprocess` válido.** Un YAML con
  `providers: [{id, type: subprocess, backend: claude, command, session_dir, models, max_tokens, backend_flags, clients}]`
  parsea y valida sin error. `backend` obligatorio; `command`/`session_dir` toman default
  (`claude` / `~/.config/mofgw/sessions`); `backend_flags` y `clients` opcionales.

- **P2 — Config subprocess NO exige clave ni base_url.** Un provider `type: subprocess` sin
  `base_url` ni `api_key_env` es válido (no requiere API key; claude usa OAuth/suscripción).

- **P3 — Config `type` desconocido → error.** `type: anything-else` (o `backend != "claude"`)
  falla la carga con error descriptivo (`unknown provider type` / `unknown backend`), sin
  arrancar.

- **P4 — Backward compat HTTP intacta.** Un provider sin `type` (o `type: http`) con
  `base_url`+`api_key_env` se valida y construye EXACTAMENTE como hoy (`provider.NewClient`),
  byte-idéntico: mismo `MaxTokens`, `Serves`, `Models`, health `Checker`. Cero cambio de
  comportamiento para configs existentes.

- **P5 — Factory construye el provider correcto por type.** `buildProvider` con
  `type: subprocess` devuelve un provider que implementa `provider.Provider`, cuyo
  `ID()==id`, `Serves()` es true para cada modelo de `models`, y `MaxTokens()==max_tokens`
  del config; y `buildBackend("claude", cmd)` devuelve un backend con `Name()=="claude"` y
  que compila contra `subprocess.Backend`. Con `type: http`/vacío devuelve `*provider.Client`.

- **P6 — Catálogo lista los modelos subprocess.** `GET /v1/models` (con providers HTTP +
  subprocess configurados) incluye cada modelo del `models` subprocess, deduplicado. Un modelo
  subprocess con `model_metadata.supported_parameters: ["reasoning"]` aparece con
  `supported_parameters` SIN `"tools"` (no-tools). Un modelo subprocess sin `model_metadata`
  aparece en formato mínimo (backward compatible).

- **P7 — Request con tools a modelo subprocess NO llega con tools al CLI.** Un request
  chat-completions con un arreglo `tools` no-vacío ruteado a un provider subprocess completa
  con éxito; el stub CLI (argv + stdin capturados) NO recibe ningún token `tools` ni el arreglo
  `tools` (ni en argv ni en el prompt traducido). (La traducción limpia D4 del motor lo
  garantiza; esta postcondición lo verifica en el request path.)

- **P8 — Restricción "solo agentes del usuario": cliente autorizado.** Con
  `providers[].clients: ["me"]`, un request autenticado con `clientID == "me"` para un modelo
  subprocess se rutea al provider subprocess y obtiene una completion. Un `clientID` que NO
  está en la allowlist NO recibe el modelo de ese provider: el router lo excluye de candidatos
  (no llega al CLI subprocess); si otro provider sirve el modelo, se le hace fallback; si
  ninguno lo sirve, el cliente recibe `404 model_not_found` (sin exponer detalles internos).

- **P9 — Allowlist vacía = sin restricción.** Con `clients` ausente/vacío, cualquier `clientID`
  autenticado puede usar el provider subprocess (backward compatible; se loguea un warning de
  "sin restricción" al arranque).

- **P10 — Hermeticidad.** La suite de 013-003 corre verde con `-race`, `go vet` y `gofmt`
  limpios, usando un **stub CLI** (script POSIX fake) + el adapter real `claude.Backend` (o un
  test-double Backend) inyectado en el factory con `command` apuntando al stub; sin binario
  `claude` real, sin suscripción, cero red externa.

## 3. Invariantes verificables

- **I1 — Provider intacto.** El `subprocess.Provider` sigue implementando `provider.Provider`
  con el contrato exacto (I1 de 001); `Models()` es un accessor aditivo, no un cambio de
  interfaz. Errores siempre `ErrUpstream`.
- **I2 — Motor intacto.** `engine.go` no se modifica en su lógica (solo se agrega el accessor
  `Models()`); la frontera `Backend` y la restricción de clientes viven fuera del motor.
- **I3 — Scoping fuera del motor.** El motor sigue agnóstico al scoping (I6 de 001); la
  restricción "solo agentes del usuario" la aplica el router.
- **I4 — Hermeticidad (D6).** Sin CLI real, sin suscripción, sin red externa en la suite.
- **I5 — Fallback preservado.** La exclusión de un provider subprocess para un cliente no
  autorizado NO aborta la cadena de fallback (se excluye de candidatos, no se produce un error
  no-retryable).
- **I6 — Calidad Go.** Suite verde con `-race`; `go vet`/`gofmt` limpios.

## 4. Criterios de aceptación

### 4.1 Criterios (C1..C10)

- **C1** — La suite hermética corre verde con `-race`, `go vet`, `gofmt` limpios, con stub CLI
  y sin red externa. (P10, I4, I6)
- **C2** — `config.Parse` de un bloque `type: subprocess` (backend claude, command, session_dir,
  models, max_tokens, backend_flags, clients) devuelve el `Config` tipado sin error. (P1)
- **C3** — Un `type: subprocess` sin `base_url`/`api_key_env` parsea sin error (no requiere key). (P2)
- **C4** — `type: bogus` y `backend: gemini` producen error de carga descriptivo. (P3)
- **C5** — Un config HTTP existente (sin `type`) se valida y construye idéntico a antes; la
  suite de 013-003 no rompe ningún test de backward compat. (P4)
- **C6** — `buildProvider` con type subprocess devuelve un `provider.Provider` con ID/models/
  MaxTokens correctos y `buildBackend("claude", cmd)` → `Name()=="claude"` (assert de
  compilación contra `subprocess.Backend`); type http/vacío → `*provider.Client`. (P5)
- **C7** — `GET /v1/models` incluye los modelos subprocess; uno con `model_metadata`
  `supported_parameters:["reasoning"]` aparece sin `"tools"`; uno sin metadata aparece mínimo. (P6)
- **C8** — Request con `tools` a un modelo subprocess completa y el stub CLI no recibe `tools`
  (ni argv ni stdin). (P7)
- **C9** — Cliente autorizado (en allowlist) usa el modelo subprocess; cliente no autorizado
  NO recibe el modelo (fallback a otro provider o 404), sin tocar el CLI subprocess. (P8)
- **C10** — `clients` vacío → sin restricción + warning de arranque. (P9)

### 4.2 Mapeo test ↔ postcondición

| Test ID                                 | Verifica | Criterio |
| --------------------------------------- | -------- | -------- |
| `T_config_subprocess_valid`               | P1       | C2       |
| `T_config_subprocess_no_key`              | P2       | C3       |
| `T_config_unknown_type_error`             | P3       | C4       |
| `T_config_unknown_backend_error`          | P3       | C4       |
| `T_config_http_backward_compat`           | P4       | C5       |
| `T_build_provider_subprocess`             | P5       | C6       |
| `T_build_backend_claude`                  | P5       | C6       |
| `T_build_provider_http_default`           | P5       | C6       |
| `T_catalog_lists_subprocess_models`       | P6       | C7       |
| `T_catalog_subprocess_no_tools`           | P6       | C7       |
| `T_catalog_subprocess_no_metadata`        | P6       | C7       |
| `T_request_tools_not_reach_cli`           | P7       | C8       |
| `T_restriction_allowed_client`            | P8       | C9       |
| `T_restriction_denied_client_fallback`    | P8       | C9       |
| `T_restriction_denied_client_404`         | P8       | C9       |
| `T_restriction_no_allowlist_unrestricted` | P9       | C10      |

> Invariantes verificados por la suite: I1/I2 por los tests de contrato (assert de
> implementación + `Models()` accessor); I3/I5 por los tests de restricción (denied client →
> fallback, no error no-retryable); I4/I6 por C1.

## Contexto técnico

**Archivos autoritativos:** `internal/config/config.go` (ProviderConfig, validate, resolveKeys),
`cmd/mofgw/main.go` (factory L85-101, health wiring L108), `internal/router/router.go`
(ProviderSpec L108, candidates L291, resolveReady L683, complete/stream not-retryable L825),
`internal/proxy/proxy.go` (handleModels L1063, modelCatalogEntry L1091, handleChat L484),
`internal/subprocess/engine.go` (Provider, Models accessor), `internal/subprocess/claude/claude.go`
(New, Backend), `internal/provider/provider.go` (Provider interface, Client.Models), y specs
013-001/013-002 (contratos cross-feature).

**Verificaciones pendientes / `VERIFICAR`:**
- Knob `session_key: client+model` (override de 013-001 P2): el motor `resolveSession` hardcodea
  `SessionKeyClient` y `NewProvider` no recibe el modo ⇒ exponerlo requeriría tocar el motor.
  **DECISIÓN del owner (12 Ago):** fuera de scope para 013-003 — se cablea solo el modo default
  `client`; `client+model` queda como follow-up que tocaría el motor.
- **DECISIÓN del owner (12 Ago) — validación de backend en tiempo de carga:** `config.validate`
  rechaza `backend != "claude"` con error descriptivo (`unknown backend`) al cargar, alineado con
  C4/P3 ("de carga"); `buildBackend` conserva el error defensivo por si se construye directo.
- **DECISIÓN del owner (12 Ago) — ubicación del factory:** `buildProvider`/`buildBackend` viven
  en un nuevo paquete importable **`internal/providerfactory`** (los unit tests los importan);
  `cmd/mofgw/main.go` llama al factory como adapter fino (patrón `buildTelemetryFromConfig`).

## Notas de implementación (no vinculantes)

- `ProviderConfig.Type` zero-value `""` ⇒ HTTP (única fuente de la backward compat; la validación
  existente de `base_url`/`api_key_env` se salta solo cuando `Type=="subprocess"`).
- `buildBackend` switch: `case "claude": return claude.New(command), nil` (command default
  `"claude"`); future `gemini`/`codex` agregan cases sin tocar el motor.
- `resolveSessionDir`: expandir `~` (via `os.UserHomeDir`) + default `DefaultSessionDir`.
- En main.go, el loop `hs.Register` (health) debe saltear providers subprocess (no implementan
  `health.Checker`); el health del CLI es 013-004.
- `candidates(model, clientID)`: excluir un spec cuando `len(spec.AllowedClients)>0 &&
  !contains(spec.AllowedClients, clientID)`; `clientID := auth.ClientIDFrom(ctx)` en
  `resolveReady`. El catálogo `/v1/models` NO filtra por cliente (global, igual que hoy).

## Out of scope (013-003)

- Errores finos del CLI, health check del subprocess, limpieza TTL de sesiones — **013-004**.
- Adapters `gemini`/`codex` — futuros.
- `session_key: client+model` (requiere tocar el motor) — follow-up.
- Rechazo 400 de `tools` (se eligió OMIT) y passthrough de `tool_calls` (deuda del epic).
- Catálogo filtrado por cliente y auto-generación de metadata de modelos subprocess.

## Cambios (decisiones de 013-003)

- **D1 — Config en ProviderConfig:** `type` discriminador + campos subprocess en el struct
  existente; backward compat por zero-value.
- **D2 — Factory con switch:** `buildProvider`/`buildBackend` extraídas y testeables.
- **D3 — Catálogo:** accessor `Models()` aditivo en el motor + type-assert de interfaz en
  handleModels; metadata declarativa en `model_metadata` (sin tools).
- **D4 — Tools OMIT:** el motor ya los omite (D4 de 001); sin cambio en proxy/router; catálogo
  marca no-tools.
- **D5 — Restricción en el router:** allowlist `clients` en `ProviderSpec` → exclusión de
  candidatos por `clientID` (fallback preservado).
- **D6 — Hermeticidad:** stub CLI + command inyectable en el factory.

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
