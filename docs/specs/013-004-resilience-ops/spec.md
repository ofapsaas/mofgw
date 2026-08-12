---
feature_id: 013-004-resilience-ops
feature_name: resilience-ops
epic: 013-mofgw-cli-subprocess
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-12
created_at: 2026-08-12
depends_on: 013-001, 013-002
paralelizable: no
---

# Spec — 013-004-resilience-ops

Proyecto: `github.com/ofapsaas/mofgw` (Go)

## 1. Descripción

Hardening de resiliencia y operaciones del provider subprocess (feature final
del epic, previa a la integración E3). Consume la deuda acumulada en las
revisiones de 013-001/002/003 más la limpieza de sesiones (TTL) del plan del
epic. Alcance: TTL de sesiones en disco + eviction del lock map en memoria,
preservación del usage real del backend, manejo de `sc.Err()` en el scanner
de streaming, allowlist de variables de entorno del hijo, guard de contexto en
la frontera `Backend.TranslateStreamOut` (cambio de interfaz), defaults de
config fuera de `validate()`, y refinamiento de los markers `IsRefusal`.

Quedan explícitamente fuera de alcance (deferidos con rationale en §6):
health check proactivo para subprocess, mapeo específico rate-limit/auth desde
stderr, calibración total de markers IsRefusal con el CLI real, y allowlist de
env configurable.

**Decisiones clave (owner 12 Ago):**
- **TTL oportunista throttled (D1):** sweep desde `resolveSession`, a lo sumo
  una vez por intervalo (default `sessionTTL/2`). Sin goroutine de background
  (el Provider no tiene ciclo de vida). Un dir/lock en uso (in-flight) jamás
  se elimina. **Default `DefaultSessionTTL = 7 * 24h`** (sesiones largas para
  prompt-cache; limpieza acotada).
- **Guard de ctx por interfaz (D2):** `Backend.TranslateStreamOut(ctx, ...)`.
  El adapter guarda su send con `select ctx.Done`. El kill+reap del proceso
  sigue en el engine. Aceptado como el fix limpio del IMPORTANT de 013-002.
- **Env allowlist fija (D3):** `PATH` + `HOME` + variables `STUB_*` (hook de
  tests herméticos). En producción = `PATH`+`HOME`. Mitiga el sombreado de
  `ANTHROPIC_API_KEY` sobre OAuth.
- **Usage preservado (D4):** `fillResponse` solo fabrica cuando el backend no
  proveyó un `TotalTokens` no-cero.
- **IsRefusal sin falso positivo ancho (D5):** se remueve el marker `"cannot"`.

## 2. Contrato

### 2.1 Interfaz modificada (`internal/subprocess/engine.go`)

La frontera `Backend` cambia un método (D2). Es el único cambio de contrato:

```go
type Backend interface {
    Name() string
    Args(s *Session, model string, flags []string) []string
    TranslateReq(body []byte) (string, error)
    TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
    // TranslateStreamOut traduce líneas del stdout a eventos SSE. Recibe ctx
    // (D2): todo send a ch debe guardarse con select sobre ctx.Done() y
    // retornar si el ctx se cancela, para no bloquear ni colgar el lock.
    TranslateStreamOut(ctx context.Context, lines <-chan string, ch chan<- provider.StreamEvent, model string)
    IsRefusal(stderr string) bool
}
```

### 2.2 `Provider` (nuevos campos)

- `sessionTTL time.Duration` (default `DefaultSessionTTL = 7 * 24 * time.Hour`).
- `now func() time.Time` (inyectable, default `time.Now`; precedente `health.Store`).
- `locks map[string]lockSlot` donde `lockSlot{ slot chan struct{}; lastUsed time.Time }`.
- Accessor de test `lockCount() int` (precedente `health.Store.Len()`).

### 2.3 `config` (cambio interno)

- `validate()` deja de mutar `Command`/`SessionDir`; los defaults se aplican en
  `Parse()` (o quedan delegados al factory, que ya los resuelve aguas abajo).

### 2.4 Postcondiciones (P1..P8)

- **P1 — Usage real preservado (D4, Minor#3).** Tras un `Complete` exitoso donde
  el backend provee `Usage.TotalTokens > 0`, la respuesta final conserva
  `PromptTokens`/`CompletionTokens`/`TotalTokens` tal cual los entregó el
  backend. La fabricación del motor SOLO ocurre cuando el backend no proveyó un
  `TotalTokens` no-cero (0 o ausente), y en ese caso el usage fabricado es no
  degenerado y consistente (Total ≥ Prompt y Total ≥ Completion).

- **P2 — `sc.Err()` del scanner no trunca en silencio (Minor#4).** Si el scanner
  de stdout del stream encuentra una línea que excede su límite de buffer
  (`sc.Err() != nil`, p.ej. línea > 1MB), el stream NO termina como un stream
  limpio truncado: el consumidor recibe un `StreamEvent.Err` normalizado
  (`*ErrUpstream`, `Type:"upstream_error"`) y el canal se cierra, sin el finish
  fabricado (usage + `[DONE]`).

- **P3 — Env del hijo acotado (FYI#6, D3).** El proceso CLI hijo recibe un env
  estricto: `PATH`, `HOME`, y toda variable cuyo nombre comience con `STUB_`.
  No recibe ninguna otra variable del entorno del proceso mofgw (una variable
  centinela arbitraria seteada en el entorno del proceso NO llega al hijo).

- **P4 — Stream abandonado libera el lock (D2, IMPORTANT 013-002).** Si el
  consumidor abandona un stream (el contexto del request se cancela mientras el
  stream está activo), el send del adapter deja de bloquear y retorna por
  `ctx.Done()`; el engine hace kill+reap y **libera el lock de la sesión**. Un
  request posterior de la MISMA sesión adquiere el lock y completa dentro de un
  plazo acotado (la sesión NO queda inanicionada). A nivel del adapter, un
  `TranslateStreamOut` con ctx ya cancelado y canal sin consumidor retorna en
  un plazo acotado (no bloquea).

- **P5 — Sesiones viejas en disco limpiadas (TTL, FYI#7).** Tras un sweep, todo
  dir de cliente bajo `sessionDir/clients/<clientID>/` cuya actividad más
  reciente predate el TTL es eliminado. Un dir con un request in-flight (lock
  sostenido) NO se elimina aunque sea viejo.

- **P6 — Lock map acotado (FYI#7).** El mapa de locks en memoria no crece sin
  límite: tras un sweep, las entradas de sesiones idle más allá del TTL y
  libres son evictadas (verificado con el accessor de test `lockCount()`).
  La eviction NO afecta la serialización de 013-001 (P8): sesiones con request
  en vuelo quedan intactas y una sesión evictada se re-crea al volver a pedir.

- **P7 — Defaults de config fuera de `validate()` (Minor 013-003).** Al parsear
  un provider `type: subprocess` SIN `command` ni `session_dir`, el `Config`
  resultante lee `Command == "claude"` y `SessionDir == DefaultSessionDir`
  (defaults aplicados en Parse/factory). `validate()` no produce esa mutación
  como efecto secundario.

- **P8 — IsRefusal sin falso positivo genérico (D5, Important-2 013-002).**
  `claude.Backend.IsRefusal` NO marca como negativa un error de sistema genérico
  (p.ej. `"cannot open file"`), y SÍ marca un texto de policy específico
  (p.ej. `"refused due to responsible use policy"`).

## 3. Invariantes verificables

- **I1 — Frontera Backend intacta (I2 de 001).** El motor sigue sin conocer
  strings backend-específicos. El cambio de interfaz (ctx) preserva la frontera:
  detección de refusal y ahora el guard de ctx del stream son responsabilidad
  del Backend; el engine conserva exec/kill/reap/errors.
- **I2 — Hermeticidad (I3 de 001).** Todos los tests nuevos usan stub CLI +
  variables `STUB_*` (incluido el nuevo `STUB_ENV_FILE` para P3). Cero red
  externa, cero binario `claude` real.
- **I3 — Serialización por sesión preservada (P8 de 001).** La eviction del lock
  map (P6) solo toca entradas idle y libres; nunca se elimina una entrada con
  request en vuelo; el máximo de procesos en vuelo por sesión sigue siendo 1.
- **I4 — Calidad Go.** Suite completa verde con `-race`; `go vet` y `gofmt`
  limpios.
- **I5 — Sin regresión en config/HTTP.** El cambio de `validate()` no altera la
  validación de providers HTTP ni la backward compatibility (P4 de 013-003).

## 4. Criterios de aceptación

### 4.1 Criterios (C1..C8)

- **C1** — Suite hermética verde con `-race`, `go vet` y `gofmt` limpios; sin
  red externa. (I2, I4)
- **C2** — Complete con backend que emite `TotalTokens` no-cero ⇒ la respuesta
  preserva ese usage; con backend que emite `TotalTokens` 0/ausente ⇒ usage
  fabricado no degenerado y consistente. (P1)
- **C3** — Un stream cuyo stdout contiene una línea > 1MB termina con
  `StreamEvent.Err` (`upstream_error`) y sin finish fabricado (`[DONE]`). (P2)
- **C4** — El env del hijo contiene `PATH`, `HOME` y las `STUB_*`, y NO contiene
  una variable centinela arbitraria del proceso mofgw. (P3)
- **C5** — Abandonar un stream (cancelar ctx) y luego emitir un `Complete` de la
  misma sesión: completa dentro de un plazo acotado (lock liberado); y un
  `TranslateStreamOut` con ctx cancelado retorna en plazo acotado. (P4)
- **C6** — Un dir de cliente con actividad predating el TTL se elimina tras el
  sweep; un dir con request in-flight no se elimina. (P5)
- **C7** — Tras el sweep, `lockCount()` no supera las entradas activas (idle
  evictadas), y una sesión evictada se re-crea y funciona al volver a pedir. (P6)
- **C8** — `Parse` de un subprocess sin `command`/`session_dir` produce
  `Command=="claude"`, `SessionDir==DefaultSessionDir`; y
  `claude.IsRefusal("cannot open file")==false` mientras
  `claude.IsRefusal("refused due to responsible use policy")==true`. (P7, P8)

### 4.2 Mapeo test ↔ postcondición

| Test ID                               | Verifica | Criterio |
| ------------------------------------- | -------- | -------- |
| `T_usage_preserved_nonzero`             | P1       | C2       |
| `T_usage_fabricated_when_zero`          | P1       | C2       |
| `T_stream_toolongline_surfaces_err`     | P2       | C3       |
| `T_child_env_allowlist`                 | P3       | C4       |
| `T_abandoned_stream_releases_lock`      | P4       | C5       |
| `T_adapter_send_ctx_guarded`            | P4       | C5       |
| `T_ttl_stale_dir_removed`               | P5       | C6       |
| `T_ttl_active_dir_kept`                 | P5       | C6       |
| `T_lock_map_bounded`                    | P6       | C7       |
| `T_evicted_session_reusable`            | P6       | C7       |
| `T_config_defaults_in_parse`            | P7       | C8       |
| `T_isrefusal_no_generic_false_positive` | P8       | C8       |

> **Nota de tests modificados (gate 3→4):** los tests existentes
> `T_usage_fabricated_complete`/`_stream` (013-001) usan el stub por default que
> emite usage `{1,1,2}`; tras P1 dejarán de ejercitar la *fabricación* (ahora
> ejercitan preservación). Para mantener cubierta la fabricación, se agrega
> `T_usage_fabricated_when_zero` con un stub de usage cero/ausente. No se borran
> los tests existentes (siguen validando no-degeneración del camino preservado).

## 5. Notas de implementación (no vinculantes)

- TTL: `DefaultSessionTTL = 7 * 24h`; `now` inyectable (patrón `health.Store`).
  Sweep en `resolveSession`, throttled a `sessionTTL/2`.
- Env: `childEnv()` filtra `os.Environ()` por `PATH`, `HOME`, prefijo `STUB_`.
- `fillResponse`: `if resp.Usage.TotalTokens <= 0 { resp.Usage = fabricateUsage(...) }`.
- Scanner: canal `scanErr` cap 1; `if err := sc.Err(); err != nil { kill+reap; scanErr <- ...; return }`; el goroutine principal chequea `scanErr` tras `TranslateStreamOut` antes del finish.
- `TranslateStreamOut(ctx, ...)`: guard `select { ch<-ev; case <-ctx.Done(): return }`.
- `IsRefusal`: remover `"cannot"`, conservar el vocabulario específico.
- Stub harness: agregar `STUB_ENV_FILE` (dump del env del hijo).

## 6. Out of scope (deferidos, con rationale)

- **Health check proactivo subprocess (item 1).** Las señales relevantes (OAuth,
  suscripción) solo aparecen en un request real que quema cuota; un probe barato
  tiene señal nula y el router ya absorbe los fallos reales vía P10+fallback.
  Diferido. main.go sigue correcto (los subprocess no se registran como health.Checker).
- **Mapeo específico rate-limit→429 / auth→401/403.** Requiere detección
  backend-específica con datos del CLI real (riesgo de falso positivo igual a
  IsRefusal). P10 ya normaliza la taxonomía (todo exit≠0→retryable 500) y el
  fallback absorbe; diferido hasta smoke real.
- **Calibración total de markers IsRefusal con smoke real.** Solo se remueve el
  falso positivo más ancho ("cannot"); el resto se documenta para el smoke real
  (B-Q7 del spec de 002).
- **Allowlist de env configurable.** Se deja fija `PATH+HOME+STUB_*`; una
  extensión vía config (p.ej. para `ANTHROPIC_API_KEY` en modo API-key) queda
  diferida (la actual mitiga el riesgo de sombreado de OAuth, plan §Riesgos #7).

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
