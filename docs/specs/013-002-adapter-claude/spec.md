---
feature_id: 013-002-adapter-claude
feature_name: adapter-claude
epic: 013-mofgw-cli-subprocess
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-12
created_at: 2026-08-12
depends_on: 013-001-subprocess-core
paralelizable: no
---

# Spec — 013-002-adapter-claude

Proyecto: `github.com/ofapsaas/mofgw` (Go)

## 1. Descripción

Implementa el **adapter `claude`** de la interfaz `subprocess.Backend` definida por 013-001: una capa delgada que arma el argv del CLI de claude, traduce el body chat-completions a un prompt único limpio, parsea la salida de claude (no-stream y stream) a los tipos OpenAI de `provider`, y detecta la señal de negativa de policy en stderr. **NO modifica el motor** (`internal/subprocess/engine.go` y `Provider` intactos); solo implementa el contrato `Backend` que el motor ya invoca.

**Decisiones lockeadas del epic que este adapter respeta:** sesión por cliente (`Session.ID`), prompt único = último mensaje `user` (D3), traducción limpia sin tools (D4), sin fallback de backend (D5), usage fabricado por el motor (D7), errores vía `ErrUpstream` (D9), tests herméticos (D6).

**Decisiones de diseño propias de 013-002 (ver §Cambios):**
- **Wire-format único stream-json (C1):** `Args` incluye SIEMPRE `--output-format stream-json` (y opcionalmente `--include-partial-messages`), porque la firma `Backend.Args` no distingue stream de no-stream. Consecuencia: `TranslateOut` (Complete, stdout completo) parsea el stream-json agregado; `TranslateStreamOut` (Stream, por líneas) parsea el mismo formato incrementalmente. Un solo formato, motor intacto.
- **Prompt vía stdin, no en argv (C3):** el motor escribe el prompt traducido por stdin en ambas vías; `Args` NO agrega prompt posicional.
- **Text-only (C5/I4):** solo los blocks `type=="text"` se surfacean; `thinking` y `tool_use` se descartan.
- **`stop_reason`→`finish_reason` (B-Q2):** `end_turn`→`stop`, `stop_sequence`→`stop`, `max_tokens`→`length`, otro→`stop`.
- **Usage de claude se descarta (C4):** el motor fabrica/sobreescribe usage (P6/P7 de 001).
- **Binario como campo del backend (A.2):** `New(bin string)`; el argv[0] sale del campo `bin` (config `command`, default `"claude"`).

## 2. Contrato

La superficie de contrato de 013-002 es la **interfaz `subprocess.Backend` existente** (definida por 013-001, engine.go), implementada por `claude.Backend`. El motor no cambia. El comportamiento específico de claude:

```go
type Backend struct {
    bin string // ruta del binario (config `command`; default "claude")
}
func New(bin string) *Backend

// implementa subprocess.Backend:
//   Name() string
//   Args(s *subprocess.Session, model string, flags []string) []string
//   TranslateReq(body []byte) (string, error)
//   TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
//   TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string)
//   IsRefusal(stderr string) bool
```

### Postcondiciones (P1..P11)

Cada postcondición es de **comportamiento** observable (por el motor, los tests o el consumidor) e individualmente testeable.

- **P1 — Nombre.** `Name()` devuelve exactamente `"claude"`.

- **P2 — Construcción del argv.** `Args(s, model, flags)` devuelve, en orden, `[bin, "-p", "--session-id", s.ID, "--model", model, ...flags]` donde `bin` es el campo del backend y `flags` se concatenan opacos **después** de `--model` (I2 de 001). El argv incluye los flags de stream-json (`--output-format stream-json`, y `--include-partial-messages` si se adopta) de forma incondicional (C1). El orden de los flags de stream-json es consistente (antes o después de `--model`, fijado e igual en todas las llamadas).

- **P3 — Sin leak de prompt en argv.** El argv NO contiene el texto del prompt traducido, ni ningún `content` de los `messages[]` del request, ni el arreglo `tools`. El prompt viaja exclusivamente por stdin (el motor lo escribe). (Seguridad/privacidad.)

- **P4 — Último mensaje user limpio.** `TranslateReq(body)` devuelve el contenido del ÚLTIMO mensaje `role=="user"` como un único string: si `content` es string, se usa directo; si es array de partes, se concatenan las partes `type=="text"` en orden (se descartan partes no-texto). Mensajes `assistant`, `tool`, `system` y anteriores `user` se ignoran. Un body sin mensaje `user` devuelve error.

- **P5 — Traducción sin tools.** El string devuelto por `TranslateReq` es texto natural limpio: sin `tool_calls`, sin resultados de tool (`role=="tool"`), sin marcadores internos ni estructura de system-prompt de orquestación; ningún arreglo `tools` del request se incluye. (D3/D4.)

- **P6 — Mapeo Complete (no-stream).** `TranslateOut(raw, model)` parsea el stream-json agregado del stdout de claude y devuelve `*provider.ChatResponse` con `choices[0]`: `message.role=="assistant"`, `message.content` = concatenación de los `text` de los `content_block_delta` con `delta.type=="text_delta"`, `finish_reason` mapeado de `stop_reason` del `message_delta` (`end_turn`→`stop`, `stop_sequence`→`stop`, `max_tokens`→`length`, otro→`stop`); `model` = parámetro `model`. Devuelve error no-nil si el JSON no es parseable o no contiene contenido de texto. (El motor sobreescribe `ID/Object/Created/Model/Usage` vía `fillResponse` — P6 de 001.)

- **P7 — Mapeo Stream (text deltas).** `TranslateStreamOut` emite UN `provider.StreamEvent{Data}` por cada `content_block_delta` con `delta.type=="text_delta"`, con payload JSON OpenAI: `{"id":...,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"<text>"},"finish_reason":null}]}`. No emite usage ni `[DONE]` (los agrega el motor). Devuelve cuando `lines` se cierra; no emite nada sobre un stream vacío.

- **P8 — Filtrado de eventos de control.** `TranslateStreamOut` NO genera eventos con contenido para: `message_start`, `content_block_start`, `content_block_stop`, `message_delta`, `message_stop`, ni deltas de `thinking`/`tool_use`. Solo los text deltas producen eventos de datos.

- **P9 — Detección de refusal.** `IsRefusal(stderr)` devuelve `true` si `stderr` contiene algún marker de negativa de policy/safety de claude (set case-insensitive definido en el adapter: raíces `"refus"`, `"cannot"`, `"not able to"`, `"policy"`, `"responsible use"`, `"safety"`); `false` en caso contrario. Solo se evalúa sobre stderr (salida de error del CLI, no la respuesta), minimizando falsos positivos.

- **P10 — Composición sin tocar el motor.** `claude.Backend` implementa `subprocess.Backend` (assert de compilación). Se cablea vía `subprocess.NewProvider(..., claude.New(bin), ...)`. `internal/subprocess/engine.go` y `Provider` NO se modifican.

- **P11 — Hermeticidad.** La suite de 013-002 usa un **stub CLI de claude** (script POSIX que emite JSON de mensaje/stream-json de claude) + el adapter real `claude.Backend` contra el `Provider` de 013-001 (patrón harness de `harness_test.go`). Sin binario `claude` real, sin suscripción, cero red externa.

## 3. Invariantes verificables

- **I1 — Motor intacto.** `internal/subprocess/engine.go` y `Provider` no se modifican; el adapter solo implementa la `Backend` existente.
- **I2 — Frontera backend.** Todo string específico de claude (`"claude"`, `"--session-id"`, `"--model"`, `"--output-format stream-json"`, `"stop_reason"`, markers de refusal) vive SOLO en `internal/subprocess/claude/`; `engine.go` sigue backend-agnóstico.
- **I3 — Hermeticidad.** Sin CLI real, sin suscripción, sin red externa en la suite.
- **I4 — Text-only sin tools.** `thinking`/`tool_use` se descartan; solo se surfacea texto. (D4.)
- **I5 — Calidad Go.** Suite verde con `-race`; `go vet` y `gofmt` limpios.
- **I6 — Fidelidad de contrato.** El adapter satisface exactamente lo que el motor espera: prompt por stdin (sin prompt posicional), motor dueño de usage/`[DONE]` (stream) y de `ID/Model/Usage` (Complete).

## 4. Criterios de aceptación

### 4.1 Criterios (C1..C11)

- **C1** — La suite hermética corre verde con `-race`, `go vet` y `gofmt` limpios, con stub CLI de claude y sin red externa. (P11, I3, I5)
- **C2** — `Name()` == `"claude"`. (P1)
- **C3** — Con un `Session{ID:"sess-1", Dir:"..."}` y `model:"claude-sonnet-4-6"`: `Args` devuelve el argv exacto `[bin, "-p", "--session-id", "sess-1", "--model", "claude-sonnet-4-6", <flags de stream-json>, ...backendFlags]` en orden; los `backendFlags` opacos van después de `--model`. (P2)
- **C4** — El argv de C3 NO contiene el prompt del request ni ningún `content`/`tools` (no hay leak de prompt en argv). (P3)
- **C5** — `TranslateReq` de un body con varios mensajes devuelve SOLO el contenido del último `user`; con `content` array devuelve la concatenación de las partes `text`. (P4)
- **C6** — El resultado de `TranslateReq` no contiene `tool_calls`, resultados de tool, marcadores internos ni el arreglo `tools`. (P5)
- **C7** — `TranslateOut` de un stream-json agregado de claude devuelve `ChatResponse` con `choices[0].message.content` = texto concatenado y `finish_reason` mapeado correctamente para `end_turn`→`stop` y `max_tokens`→`length`. Un stdout no parseable devuelve error. (P6)
- **C8** — Drenar el canal de `TranslateStreamOut` con líneas de stream-json de claude produce un evento Data con `delta.content` por cada `text_delta`, y NINGÚN evento para los eventos de control. (P7, P8)
- **C9** — El canal de `TranslateStreamOut` no contiene un chunk con `usage` ni el literal `[DONE]` (el motor los agrega). (P7)
- **C10** — `IsRefusal("...cannot respond to that...")` == `true`; `IsRefusal("server started")` == `false`. (P9)
- **C11** — `claude.Backend` compila contra `subprocess.Backend` y, usando el stub CLI de claude a través de `subprocess.NewProvider`, un `Complete` devuelve la respuesta traducida y un `Stream` emite los text deltas; `engine.go` no fue modificado. (P10, P11)

### 4.2 Mapeo test ↔ postcondición

| Test ID                          | Verifica   | Criterio |
| -------------------------------- | ---------- | -------- |
| `T_name`                           | P1         | C2       |
| `T_argv_build`                     | P2         | C3       |
| `T_argv_order_flags`               | P2         | C3       |
| `T_argv_no_prompt_leak`            | P3         | C4       |
| `T_translate_req_last_user`        | P4         | C5       |
| `T_translate_req_text_array`       | P4         | C5       |
| `T_translate_req_no_user_error`    | P4         | C5       |
| `T_translate_req_clean_no_tools`   | P5         | C6       |
| `T_translate_out_text_content`     | P6         | C7       |
| `T_translate_out_stop_reason_map`  | P6         | C7       |
| `T_translate_out_invalid_json`     | P6         | C7       |
| `T_translate_stream_text_deltas`   | P7         | C8       |
| `T_translate_stream_skip_control`  | P8         | C8       |
| `T_translate_stream_no_usage_done` | P7         | C9       |
| `T_translate_stream_empty`         | P7         | C8       |
| `T_is_refusal_true` / `_false`       | P9         | C10      |
| `T_implements_backend` (assert)    | P10        | C11      |
| `T_adapter_complete_stub_cli`      | P6,P10,P11 | C7,C11   |
| `T_adapter_stream_stub_cli`        | P7,P10,P11 | C8,C11   |

> **Nota de diseño de tests:** a diferencia de 013-001 (que usaba `stubBackend` como test-double), 013-002 testea el **adapter real `claude.Backend`** contra un **stub CLI de claude** que emite JSON de claude. Reutiliza el patrón del harness de 013-001 (`buildStubCLI` → script que emite claude-shaped output, `clientCtx` para el clientID, `chatBody`, wiring con `subprocess.NewProvider`). Las funciones puras (`TranslateReq`, `TranslateOut`, `TranslateStreamOut`, `IsRefusal`) se testean unitariamente con JSON canned sin spawn.

## Contexto técnico

**Archivos autoritativos:**
- `internal/subprocess/engine.go` — interfaz `Backend` y cómo el motor la invoca: prompt por stdin, `TranslateOut` recibe stdout completo, `TranslateStreamOut` recibe líneas y escribe a `ch`, motor dueño de usage/`[DONE]`/`fillResponse`.
- `internal/subprocess/harness_test.go` — patrón hermético (stub CLI POSIX, `clientCtx`, `chatBody`, wiring `NewProvider`), forma exacta del contrato `Backend` (via `stubBackend`).
- `internal/provider/provider.go` — `ChatResponse`/`Choice`/`ChatMessage`/`Usage`/`StreamEvent` (shapes OpenAI a producir).
- `internal/stream/stream.go` — `CaptureUsage`/`Copy`: el stream debe emitir chunks OpenAI y el motor el chunk final `{"choices":[],"usage":{...}}` + `[DONE]`.
- `docs/epics/013-mofgw-claude-subprocess/plan.md` — decisiones lockeadas (D1–D9) y contrato `Backend`.
- `docs/specs/013-001-subprocess-core/spec.md` — contrato `Backend` (§2.2), `Session` (§2.3), P1–P13, I2 (frontera backend).

**Convenciones aplicables:** cuerpo crudo `[]byte` por la pipeline; el motor fabrica usage (P6/P7); errores siempre `ErrUpstream`; tests herméticos (D6); el adapter posee argv/traducción/parseo/refusal (I2).

**Verificaciones pendientes / `VERIFICAR`:**
- Formato de `--session-id` que acepta el CLI de claude (sha256 hex del motor vs UUID). (B-Q5)
- Confirmar que `claude -p` lee el prompt de stdin sin prompt posicional. (B-Q6)
- Comportamiento de `--include-partial-messages` (eventos `message_partial`): decidir si se adopta; si no, no incluir el flag. (B-Q1)
- Set final de markers de `IsRefusal` ajustado con datos del smoke real. (B-Q7)

## Notas de implementación (no vinculantes)

- `New(bin string)` con default `"claude"`; `bin` es campo del struct (como `scriptPath` en `stubBackend`).
- `Args` arma `[bin, "-p", "--session-id", s.ID, "--model", model, "--output-format", "stream-json", ...flags]` (incluir `--include-partial-messages` solo si se adopta, ver B-Q1).
- `TranslateReq`: unmarshal a `{Messages []{Role, Content json.RawMessage}}`; iterar del último al primero por `role=="user"`; si `Content` es string usar; si es array, concatenar `type=="text"`.
- `TranslateOut`: escanear líneas del stdout, acumular texto de `content_block_delta.text_delta`, tomar `stop_reason` del `message_delta`, construir `ChatResponse`. Tolerante a líneas no-JSON.
- `TranslateStreamOut`: `for ln := range lines`, unmarshal; solo `content_block_delta` con `delta.type=="text_delta"` → emitir chunk OpenAI. Devolver al cerrar `lines`.
- `IsRefusal`: `strings.ToLower(stderr)` + contains sobre el set de markers.
- Assert de compilación `var _ subprocess.Backend = (*Backend)(nil)`.

## Out of scope (013-002)

- Cableado config/factory/catálogo sin-tools/request-path — **013-003**.
- Resiliencia fina (stderr backend, health, limpieza TTL) — **013-004**.
- Smoke test real contra el CLI `claude` autenticado — manual, fuera de la suite (D6).
- Exponer `thinking`/reasoning o passthrough de `tool_calls` — deuda del epic.
- Cambiar el motor o el contrato `Backend`.

## Cambios (decisiones de 013-002)

- **C1 — Wire-format único stream-json:** `Args` no distingue stream ⇒ flags de stream-json SIEMPRE; `TranslateOut` parsea el stream-json agregado. Un formato para Complete y Stream, motor intacto.
- **C3 — Prompt vía stdin:** el motor siempre escribe el prompt a stdin; el adapter no pone prompt posicional en argv.
- **C5 — Text-only:** solo blocks `text` se surfacean; `thinking`/`tool_use` descartados (D4).
- **B-Q2 — stop_reason:** `end_turn`/`stop_sequence`→`stop`, `max_tokens`→`length`, otro→`stop`.
- **C4 — Usage de claude se descarta** (motor lo fabrica/sobreescribe, P6/P7 de 001).
- **A.2 — Binario como campo del backend** (`New(bin)`), habilitando el stub CLI hermético y la config `command` (013-003).

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
