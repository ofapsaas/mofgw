---
feature_id: 013-001-subprocess-core
feature_name: subprocess-core
epic: 013-mofgw-cli-subprocess
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-12
created_at: 2026-08-12
updated_at: 2026-08-12
depends_on: —
paralelizable: sí
---

# Spec — 013-001-subprocess-core

Proyecto: `github.com/ofapsaas/mofgw` (Go)

---

## 1. Descripción

Motor `subprocess` genérico para providers que ejecutan el CLI de un backend de IA como subproceso (en lugar de hablar HTTP con un endpoint). Un provider `subprocess` sirve uno o más modelos (router elige por nombre vía `Serves`); la conversación se mantiene en una **sesión estable por cliente** que vive en el proceso del CLI (el CLI acumula el historial entre llamadas, como una sesión CLI normal).

Este feature (013-001) entrega **solo el motor genérico + la interfaz `Backend`**. El adapter `claude` concreto es 013-002; el cableado config/factory/catálogo es 013-003; la resiliencia fina (stderr backend-específico, health, limpieza TTL) es 013-004.

**Arquitectura (D1):** el motor (`internal/subprocess`) posee exec, resolución de sesión por cliente, serialización, captura de stdout/exit, fabricación de usage y normalización de errores → `provider.ErrUpstream`. El `Backend` posee argv, traducción de formato y parseo. **El motor NO conoce strings backend-específicos**: no hay `"claude"`, no interpreta flags; `backend_flags` se pasan opacos.

**Decisiones clave lockeadas (ver §Cambios):**
- **Sesión por CLIENTE (default)** (D2): `Session.ID` se deriva determinísticamente solo de `clientID`; el modelo se pasa por request (`Backend.Args(s, model, flags)` → CLI `--model`), NO participa en la key de sesión en modo default. Override opcional `session_key: client+model` (ID = hash(`clientID|model`)) se implementa en 001 y se expone desde 003.
- **Historial en la sesión, prompt único** (D3): mofgw envía SOLO el último mensaje `user` (no reconstruye ni re-envía el historial completo).
- **Traducción limpia, sin tools** (D4): `TranslateReq` devuelve texto natural limpio, sin `tool_calls`/resultados de tool/marcadores internos; el modelo se trata como sin-tools desde mofgw.
- **Sin fallback a nivel backend** (D5): hay exactamente UN backend subprocess (claude); una negativa/falla de claude NO se auto-rutea a otro backend subprocess (limitación del modelo comercial). El motor normaliza TODOS los fallos a `ErrUpstream`; el fallback cross-provider genérico de mofgw aplica si otro provider sirve el mismo modelo.
- **Usage fabricado por el motor** (D7) en Complete y Stream.
- **Serialización por sesión** (D8): a lo sumo un proceso CLI en vuelo por sesión, lock por sesión para todo el request, espera ctx-cancelable.
- **Errores vía `provider.NewErrUpstream`** (D9): sanitizado, sin leaks, `ctx.Err()`/`DeadlineExceeded` → `ErrUpstream{Type:"timeout"}`.
- **Tests herméticos** (D6): stub CLI + test-double Backend, sin binario `claude` real ni suscripción, cero red externa.

---

## 2. Contrato

### 2.1 `Provider` (interfaz existente compartida, sin cambios)

`internal/provider.Provider` — el provider subprocess la implementa intacta (I1):

```go
type Provider interface {
    ID() string
    Complete(ctx context.Context, body []byte) (*CompleteResult, error)
    Stream(ctx context.Context, body []byte) (<-chan StreamEvent, error)
    MaxTokens() int
    Serves(model string) bool
}
```

- `Complete`/`Stream` reciben el **body crudo** OpenAI chat-completions (`[]byte`) y devuelven los mismos tipos que el `provider.Client` HTTP: `*CompleteResult` (con `Response *ChatResponse` y `Raw []byte`) y un canal `<-chan StreamEvent` (`Data`/`Err`), respectivamente.
- `MaxTokens()` y `Serves(model)` cumplen exactamente el contrato existente (usados por el router y el clamp).
- Errores: SIEMPRE `*provider.ErrUpstream`, nunca el error crudo del CLI ni un `context` error.

### 2.2 `Backend` (interfaz nueva, definida por 001, implementada por 002)

```go
type Backend interface {
    Name() string
    // Args arma el argv del CLI para un request (cwd = Session.Dir).
    // El modelo se pasa per-request; NO participa de la key de sesión (default).
    Args(s *Session, model string, flags []string) []string
    // TranslateReq traduce el body chat-completions a UN prompt único de texto
    // natural limpio (D3/D4). No recibe flags; recibe el body crudo completo.
    TranslateReq(body []byte) (string, error)
    // TranslateOut traduce stdout (modo no-stream) a ChatResponse OpenAI.
    TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
    // TranslateStreamOut traduce líneas del stdout (streaming) a eventos SSE.
    TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string)
}
```

**Semántica exacta de `TranslateReq` (D3/D4):**
- Devuelve el **contenido del ÚLTIMO mensaje `role=="user"`** del `messages[]` como un único prompt de texto limpio.
- Es texto natural limpio: **NO** contiene `tool_calls`, bloques de resultado de tool (`role=="tool"`), marcadores internos, ni estructura de system-prompt de orquestación. Cualquier resto OpenAI-interno que pudiera alcanzar el CLI se elimina aquí.
- Este prompt es el **único** input que el motor envía al CLI vía **stdin** (una sola escritura). El motor NO reconstruye ni re-envía el historial completo de mensajes (D3): el CLI mantiene la conversación en su sesión estable por cliente.

### 2.3 `Session` (struct)

- `ID string` — derivado **determinísticamente** de `clientID` (default: `hash(clientID)`), o de `hash(clientID|model)` en modo `client+model`. (Ver P1/P2.)
- `Dir string` — directorio base de la sesión del cliente.
- El motor mantiene un mapa `Session.ID → chan struct{}` (capacidad 1) para serialización.

### 2.4 Postcondiciones (P1..P13)

Cada postcondición es de **comportamiento** (lo que observa un consumidor externo: el router, el proxy, el cliente) e individualmente testeable con un test de comportamiento (no un mock sobre plumbing).

- **P1 — Sesión por cliente, determinística (D2).** Para un provider dado, dos requests del MISMO `clientID` (resuelto vía `auth.ClientIDFrom(ctx)`) resuelven el MISMO `Session.ID`; dos requests de clientIDs DISTINTOS resuelven IDs DISTINTOS. El ID se deriva solo de `clientID` (default), de forma determinista: mismo `clientID` ⇒ mismo ID en llamadas sucesivas.

- **P2 — Derive `client+model` implementada (D2).** El provider implementa AMBAS derivaciones de key de sesión: (a) default por `clientID`; (b) override `client+model` con `ID = hash(clientID|model)`. En modo (b), mismo cliente + mismo modelo ⇒ mismo ID; mismo cliente + modelos distintos ⇒ IDs distintos; y el ID difiere del modo (a) para el mismo cliente. (El knob de selección lo expone 013-003; 001 implementa y testeable ambas derivaciones.)

- **P3 — Multi-modelo por request, misma sesión (D2).** Una sola instancia de provider sirve TODOS los modelos de su lista configurada (`Serves(model)` es true para cada uno). Dos requests del MISMO cliente, MISMA sesión, con modelos DISTINTOS: (i) el modelo se pasa a `Backend.Args` per-request (llega al CLI vía `--model`), y (ii) en modo default (key por cliente) ambos resuelven la MISMA sesión — el modelo NO altera la key de sesión. La respuesta completa (non-stream) lleva el modelo correcto.

- **P4 — Prompt único = último mensaje user (D3).** En cada request el motor escribe UN solo prompt al stdin del CLI: el contenido del ÚLTIMO mensaje `user` extraído por `TranslateReq` del body. El motor NO reconstruye ni re-envía el historial completo de mensajes. (Comportamiento observable: el stdin del proceso CLI de una request contiene solo el texto del último mensaje `user`, no el arreglo `messages` completo.)

- **P5 — Traducción limpia, sin tools (D4).** El prompt que llega al CLI (stdin) es texto natural limpio: sin `tool_calls`, sin bloques de resultado de tool (`role=="tool"`), sin marcadores internos ni system-prompt de orquestación. Ningún arreglo `tools` llega al CLI desde mofgw. (El CLI puede usar sus PROPIAS tools internas como agente; eso es invisible para mofgw y no lo testea el motor.)

- **P6 — Usage fabricado en Complete (D7).** Tras un `Complete` exitoso, `CompleteResult.Response.Usage` es no-nil y no degenerado: `PromptTokens`, `CompletionTokens` y `TotalTokens` presentes, con `TotalTokens >= 0`. Si el CLI no emite conteos de tokens, el motor los fabrica (de propiedad del motor) de modo que usage headers/metrics/accounting no degeneren. (El test solo verifica no-degeneración y consistencia, no valores exactos.)

- **P7 — Usage fabricado en Stream (D7).** Un stream del provider emite un chunk SSE final con objeto `usage` OpenAI-compatible (`stream_options.include_usage`), de modo que `stream.CaptureUsage`/`Copy` puedan extraerlo. Campos no degenerados (mismos criterios que P6). (Drenar el canal del stream hasta su cierre produce un evento final con objeto `usage`.)

- **P8 — Serialización por sesión (D8).** A lo sumo UN proceso CLI en vuelo por `Session.ID`. Dos requests concurrentes de la MISMA sesión se serializan: el segundo espera a que el primero termine (incluido un stream largo) antes de spawnear. El lock por sesión se mantiene durante TODO el request, incluida la duración del stream. (Comportamiento: nunca hay dos spawns simultáneos para la misma sesión; el conteo máximo de procesos en vuelo por sesión es 1.)

- **P9 — Espera ctx-cancelable (D8).** Un request en cola esperando el lock de su sesión es cancelable por su `context`: si se cancela antes de adquirir el lock, la espera aborta y el request falla con un `*ErrUpstream` normalizado (nunca con el error crudo de contexto). (Comportamiento: cancelar un request en cola retorna rápido con un `ErrUpstream`, y el lock queda intacto para el siguiente request.)

- **P10 — Taxonomía completa de fallos → ErrUpstream (D9).** Todo fallo del motor/CLI se normaliza a `*provider.ErrUpstream` para que `router.classify` lo clasifique correctamente y se muestre un error limpio. Modos cubiertos: (a) fallo de spawn/exec → `ErrUpstream{Type:"network"}`; (b) exit no-cero con stdout → `ErrUpstream` con el mensaje del stderr/stdout saneado; (c) timeout del proceso / deadline del contexto → `ErrUpstream{Type:"timeout"}`. Específicamente: `ctx.Err()`/`context.DeadlineExceeded` se convierten en `ErrUpstream{Type:"timeout"}` (retryable por el router) y NUNCA se filtra el error crudo de contexto (que el router clasificaría como no-retryable por caer en el default).

- **P11 — Negativa de seguridad/refusal normalizada (D5).** Si el CLI devuelve una **negativa de policy / safety block** (el CLI se niega a responder), el motor la normaliza a `ErrUpstream{Type:"invalid_request_error", StatusCode:400}` — **NO retryable**. Justificación: un refusal de policy es una decisión determinista sobre el contenido del prompt (problema de cliente), no una falla transitoria de infraestructura; marcarlo retryable quemaría cuota y rotaría providers sin chance de éxito. Al ser 4xx de cliente, `router.classify` lo trata como no-retryable (no dispara cooldown/retry/fallback) y se muestra un error limpio. Este caso NO debe quedar misclasificado como retryable.

- **P12 — Sanitize en todos los errores (D9).** Todo `ErrUpstream` producido por el motor se construye vía `provider.NewErrUpstream` (su `sanitize` corre): el `Message` visible al router/cliente no contiene keys, paths internos ni argv; y tiene longitud ≤ 300 caracteres.

- **P13 — Identidad de cliente no resoluble → fail-closed (D9).** El motor resuelve la sesión vía `auth.ClientIDFrom(ctx)`. Si devuelve `""` (request no autenticado), el request falla con un `ErrUpstream` **no-retryable** en lugar de compartir una sesión de otro cliente. (Las sesiones subprocess son por cliente y no se pueden keyear sin identidad; no hay sesión compartida por defecto.)

---

## 3. Invariantes verificables

- **I1 — Provider intacto.** El `subprocess.Provider` implementa `provider.Provider` con el contrato exacto existente (firmas, tipos `CompleteResult`/`StreamEvent`, `MaxTokens()`, `Serves`). Ningún string backend-específico ("claude", flags) aparece en la superficie de la interfaz `Provider`. Errores siempre `ErrUpstream`.

- **I2 — Frontera Backend.** El motor NO conoce strings backend-específicos: no interpreta `backend_flags`, no hardcodea nombres de binarios, no parsea formatos. Todo lo específico (argv, traducción, parseo) vive en el `Backend`. `backend_flags` viajan opacos de config → `Args`.

- **I3 — Hermeticidad (D6).** La suite de tests NO depende del binario `claude` real ni de una suscripción; usa un stub CLI ejecutable (script fake) + un test-double Backend. Cero red externa. (El smoke test real contra el CLI autenticado es manual, fuera de la suite.)

- **I4 — Sin-tools en el motor (D4).** La traducción del motor se mantiene limpia (sin tools) de modo que no requiera cambio del motor cuando 013-003 marque el modelo como sin-tools en el request path. El motor trata el modelo como texto.

- **I5 — Calidad Go.** Suite completa verde con `-race`; `go vet` y `gofmt` limpios.

- **I6 — Sin scoping de clientes generales (013-003).** 013-001 NO implementa la restricción "solo agentes del usuario" ni el request-path marking de catálogo; eso es de 013-003. El motor es agnóstico al scoping.

---

## 4. Criterios de aceptación

Medibles y verificables. Cada criterio mapea a una o más postcondiciones (tabla en §4.2).

### 4.1 Criterios (C1..C14)

- **C1** — La suite hermética corre verde con `-race`, `go vet` y `gofmt` limpios, usando stub CLI + test-double Backend y sin red externa. (I3, I5)
- **C2** — Con un stub Backend y dos clientIDs distintos: el mismo clientID resuelve el mismo `Session.ID` en dos requests; clientIDs distintos resuelven IDs distintos. (P1)
- **C3** — Con la derivación `client+model` activada: mismo (client, model) ⇒ mismo ID; modelos distintos para el mismo cliente ⇒ IDs distintos; y este ID difiere del default para el mismo cliente. (P2)
- **C4** — Un provider con 2+ modelos: `Serves` es true para ambos; dos requests del mismo cliente con modelos distintos pasan cada modelo por `Args` (el stub Backend registra el `--model`) Y resuelven la misma sesión (modo default); la respuesta non-stream lleva el modelo correcto. (P3)
- **C5** — El stdin del proceso CLI contiene SOLO el contenido del último mensaje `user` del body (no el arreglo `messages` completo ni el historial). (P4)
- **C6** — El prompt que llega al CLI no contiene `tool_calls`, bloques de resultado de tool, ni marcadores internos/orquestación; y ningún arreglo `tools` llega al CLI. (P5)
- **C7** — Tras un `Complete` exitoso con un CLI que NO emite conteos: `Usage` no-nil, `PromptTokens`/`CompletionTokens`/`TotalTokens` presentes, `TotalTokens >= 0`. (P6)
- **C8** — Drenar el canal de un stream hasta el cierre produce un evento final con objeto `usage` extraíble (compatible con `stream.CaptureUsage`). (P7)
- **C9** — Dos requests concurrentes de la misma sesión: máximo 1 proceso CLI en vuelo para esa sesión (el stub Backend/CLI cuenta spawns simultáneos); el segundo espera al primero. (P8)
- **C10** — Cancelar el context de un request en cola (esperando el lock) aborta la espera y devuelve un `ErrUpstream` (no un error crudo de contexto); un request posterior de la misma sesión adquiere el lock y completa. (P9)
- **C11** — Cada modo de fallo produce el `ErrUpstream` esperado: spawn fallido → `Type:"network"`; exit no-cero → `ErrUpstream` saneado; timeout/deadline → `Type:"timeout"` (retryable por `router.classify`). Ningún error crudo de contexto (que sería no-retryable) se filtra al router. (P10)
- **C12** — Una negativa/refusal de policy del CLI produce `ErrUpstream{Type:"invalid_request_error", StatusCode:400}`, clasificado **no-retryable** por `router.classify` (sin cooldown/retry/fallback del backend). (P11)
- **C13** — Ningún `ErrUpstream.Message` producido por el motor contiene keys, paths internos ni argv; longitud ≤ 300 caracteres. (P12)
- **C14** — Un request sin `clientID` resoluble (`auth.ClientIDFrom` = "") falla con un `ErrUpstream` no-retryable (fail-closed), sin compartir sesión. (P13)

### 4.2 Mapeo test ↔ postcondición

| Test ID                      | Verifica postcondición(es) | Criterio(s) de aceptación |
| ---------------------------- | -------------------------- | ------------------------- |
| `T_session_same_client`        | P1                         | C2                        |
| `T_session_diff_client`        | P1                         | C2                        |
| `T_session_key_client_model`   | P2                         | C3                        |
| `T_multi_model_serves`         | P3                         | C4                        |
| `T_multi_model_same_session`   | P3                         | C4                        |
| `T_model_passed_per_request`   | P3                         | C4                        |
| `T_single_last_user_prompt`    | P4                         | C5                        |
| `T_prompt_clean_no_tools`      | P5                         | C6                        |
| `T_usage_fabricated_complete`  | P6                         | C7                        |
| `T_usage_fabricated_stream`    | P7                         | C8                        |
| `T_serialize_same_session`     | P8                         | C9                        |
| `T_wait_cancellable`           | P9                         | C10                       |
| `T_err_spawn_network`          | P10                        | C11                       |
| `T_err_nonezero_exit`          | P10                        | C11                       |
| `T_err_timeout_retryable`      | P10                        | C11                       |
| `T_never_leak_ctx_err`         | P10                        | C11                       |
| `T_refusal_not_retryable`      | P11                        | C12                       |
| `T_sanitize_no_leak`           | P12                        | C13                       |
| `T_empty_clientid_fail_closed` | P13                        | C14                       |

> Invariantes verificados por suite completa: I3/I5 por C1; I1/I2 por los tests de contrato (`Provider` implementa la interfaz, `Backend` doble de test en todos los casos); I4 por C6; I6 por ausencia (ningún test de 013-001 toca scoping — verificado por el auditor en el Gate 3→4).

---

## Contexto técnico

**Archivos/hooks relevantes (autoritativos):**
- `internal/provider/provider.go` — interfaz `Provider` (ID/Complete/Stream/MaxTokens/Serves), `ChatRequest`/`ParseChatRequest`, `CompleteResult`, `StreamEvent`, `ErrUpstream` (StatusCode/Type/Message/RetryAfter), `NewErrUpstream` (aplica `sanitize`), `sanitize` (trim, ≤300 chars, redacta `http(s)://`, `localhost`, `127.0.0.1`, `Bearer `), tipos `Usage`/`ChatResponse`/`Choice`.
- `internal/auth/auth.go` — `ClientIDFrom(ctx) string` (identidad de cliente, resuelve la sesión; `""` si no pasó por `Wrap`).
- `internal/router/router.go` — `classify(err)` (retryable: 429, ≥500, `timeout`/`network`; no-retryable: 4xx de cliente), `MaxTokens`, `Serves`, cadena de fallback, `ProviderSpec`.
- `internal/stream/stream.go` — `CaptureUsage` (extrae usage del chunk final SSE) y `Copy` (consumidor del canal). La fabricación de usage del stream (P7) debe ser compatible con este contrato.
- `internal/proxy/responses.go` — patrón de capa de traducción (el motor subprocess usa el mismo principio: traducción aislada en el Backend, la pipeline router/provider intacta).
- `cmd/mofgw/main.go` (~l.86) — factory de providers en `router.ProviderSpec` (013-003 agrega el caso `type: subprocess`; 001 no toca este factory).
- `internal/timeouts/timeouts.go` — `Attempt`/`Classify` (`DeadlineExceeded → ErrTimeout`); referencia para la conversión de P10 (timeout → `ErrUpstream{Type:"timeout"}`).
- `docs/epics/013-mofgw-cli-subprocess/plan.md` — contrato cross-feature `Backend` y descomposición del epic.

**Convenciones aplicables:** body crudo `[]byte` por la pipeline (transparencia OpenAI); errores siempre `*ErrUpstream` normalizados; cliente se identifica por `auth.ClientIDFrom`; el router nunca ve errores crudos de contexto (P10); tests herméticos con stub/test-double (D6).

**Verificaciones pendientes / `VERIFICAR`:** detalle exacto del algoritmo de fabricación de usage (heurística engine-owned; el spec solo exige no-degeneración, no valores). El `session_dir` y formato de `backend_flags` se materializan en 013-003; 001 implementa derivación de `Session.ID` y directorio por cliente pero no el parsing de config.

---

## Notas de implementación (no vinculantes)

- Resolución de sesión: `Session.ID = hash(clientID)` (default) o `hash(clientID|model)` (override); el directorio por cliente bajo `session_dir` (path lo provee config en 013-003).
- Serialización: mapa `Session.ID → chan struct{}` (cap 1); `select` sobre `ctx.Done()` + el canal del lock para la espera cancelable (P8/P9). Integra con el limiter global/singleflight existentes (sin tocarlos).
- Spawn: `exec.CommandContext` con el ctx del request; argv = `Backend.Args(s, model, flags)` (cwd = `Session.Dir`); stdin = prompt único de P4/P5; stdout leído por líneas (para stream) o completo (no-stream).
- Timeout: medir y convertir `ctx.Err()/DeadlineExceeded` → `ErrUpstream{Type:"timeout"}` (P10), nunca exponer el error crudo.
- Usage: fabricar `Usage` en el motor tras el request (no-stream) o inyectando un chunk `usage` final en el stream (compatible con `CaptureUsage`).
- Refusal de policy: detectar la señal de negativa del CLI y mapear a `invalid_request_error`/400 (P11). La señal concreta la conoce el Backend (su responsabilidad de detección); el motor solo recibe el error normalizado.

---

## Out of scope (013-001)

- Adapter `claude` (argv `-p --session-id`, traducción OpenAI↔claude, parseo) — **013-002**.
- Config `type: subprocess` + `backend` + factory en router + catálogo `/v1/models` sin-tools + request path omite/rechaza tools + restricción "solo agentes del usuario" — **013-003**.
- Resiliencia fina: stderr backend-específico, health check, limpieza TTL de sesiones en disco — **013-004**.
- Adapters `gemini` y `codex` (futuros) — fuera del epic (plan.md §Cambios).
- Smoke test real contra el CLI `claude` autenticado — **manual**, fuera de la suite (D6).
- Passthrough de `tool_calls` OpenAI ↔ CLI y structured output nativo vía CLI (deuda esperada del epic, plan.md §Riesgos).

---

## Cambios (consolidación de decisiones lockeadas)

- **D3 — Revierte el framing "prompt-cache full-context"** del primer draft/plan: en lugar de re-enviar el prompt completo con session-id para que el CLI cachee el prefijo, mofgw envía **SOLO el último mensaje `user`** (prompt único); la sesión estable del CLI acumula el historial como una sesión CLI normal. `TranslateReq` = "extraer el último mensaje `user` como prompt único".
- **D5 — Agrega no-fallback/special-case y normalización de refusal**: hay un único backend subprocess (claude) → sin fallback entre backends subprocess (limitación del modelo comercial); el fallback cross-provider genérico aplica si otro provider sirve el mismo modelo. Nueva postcondición P11: una negativa/refusal de policy se normaliza a `invalid_request_error`/400 (no-retryable), con justificación, para no misclasificarla.
- **D2 — Confirma sesión por cliente (default) + multi-modelo por request**: `Session.ID` derivado solo de `clientID`; el modelo se pasa por request (Backend.Args → `--model`) sin participar en la key de sesión; override `client+model` implementado en 001 y expuesto por 003.
- **D1/D7/D8/D9/D6** — confirmadas de drafts previos (motor genérico + `Backend`; usage fabricado por el motor; serialización por sesión con espera cancelable; errores vía `NewErrUpstream` sanitizado sin leaks de ctx; tests herméticos con stub/test-double).

---

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
