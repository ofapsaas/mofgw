# EPIC-014 — mofgw-registro-unificado

> "Un solo registro de accounting/telemetría que permita calcular todo lo necesario por período."

## Resumen

Hoy hay **2 fuentes incompletas que no se cruzan**:

- **telemetry.jsonl** (sample 10%): timestamped, pero se loguea al **entrar** el request → **no tiene provider**, ni tokens, ni costo.
- **/metrics + state.json**: tiene provider/modelo/cliente + tokens + costo **exacto**, pero **acumulado desde arranque** (sin ventana temporal).

Consecuencia: no se puede responder "cuánto gastó cliente X en provider Y esta semana", ni fallos por provider, ni nada con resolución temporal.

**Alcance de ESTE epic:** emitir el **evento unificado por request** (con outcome/errores). Nada más dentro de mofgw.

**Fuera de alcance (epic aparte):** la consolidación diaria/semanal/mensual y las visualizaciones. Ese epic es QC **externo** — consume los datos que este epic deja en disco, **NO toca mofgw en sí**. Por eso este epic solo entrega el registro; quien lo quiera leer lo agrega por fuera.

## Objetivo

Un **evento único y completo por request**, emitido al completarse (ahí ya se conoce provider + tokens + costo + resultado), timestamped y append-only, que sea **fuente de verdad** para cualquier agregación por día/semana/mes × provider × modelo × cliente, incluida la estadística de fallos.

## Punto de enganche

Ya existe el hook perfecto: `recordCacheTokens()` en `internal/proxy/proxy.go` (y `responses.go`, `embeddings.go`) — **post-respuesta**, con `requestID, clientID, sessionID, providerID, model, usage`. Ahí se conoce provider + tokens + costo. Hoy solo acumula contadores; este epic lo convierte además en **emisor del evento unificado**.

Para el resultado a nivel **intento** (fallbacks/errores por provider), los puntos de emisión ya existen como debug: `provider_attempt`, `provider_fallback`, `retry_same_provider`, `cooldown_start` en `internal/router/router.go` — se elevan a emitir el evento.

## Features

| # | Feature | Qué entrega |
|---|---------|-------------|
| `014-001` | **Evento unificado de accounting + outcome** | Registro append-only (JSONL) por request con dos niveles de datos: evento por intento + evento terminal. |

**014-001 — detalle (la única feature de este epic):**

- **Evento por intento** (por cada provider probado en la cadena):
  `request_id, ts, client, provider, model, outcome(ok\|fallback), cause, status, attempt, retries`
- **Evento terminal del request:**
  `request_id, ts, client, outcome(success\|error), error_code, status, final_provider, tokens(prompt/completion/cache/reasoning), cost_usd, stream`

- El registro es **append-only**, no toca el request path (aditivo).
- **Privacidad:** solo metadata, **nunca** contenido de prompts/respuestas.

## Contrato cross-feature (el que consume el epic externo)

El **esquema del evento** (campos + tipos + significado) definido en `014-001` es el contrato. El epic externo de consolidación/visualización lee ese esquema → no necesita tocar mofgw.

Formato elegido: **JSONL** (consistente con el telemetry existente, append-only, readable por cualquier herramienta). La consolidación (epic externo) lo lee con Jobs/streaming.

## Criterios de aceptación del epic

1. Un request emite **eventos de intento + un evento terminal**, con outcome y clasificación de error (success/error + code/status, ok/fallback + cause).
2. El registro tiene `provider, model, client, tokens, cost` por request → permite después responder "tokens/cost de provider X modelo Y cliente Z <período>" con números **exactos**.
3. **Consolida fallos incluso cuando el request final fue exitoso** (nivel intento): muestra qué provider patinó y por qué.
4. Sobrevive restarts (append-only en disco) y **no depende del sample** del telemetry actual.
5. Suite completa `-race` verde; el evento **no altera el request path** (aditivo, reversible).

## Qué NO entra (anti-scope)

- ❌ No tocar ruteo/fallback/cache/sticky.
- ❌ No reemplazar el telemetry de descubrimiento (009-000) — conviven.
- ❌ No analizar contenido (nunca).
- ❌ **No** la consolidación diaria/semanal/mensual ni las visualizaciones → epic aparte, externo a mofgw.

## Ejecución

Se materializa como slices del worker `mofgw` (project-tracker, modo `iter`/`cdad`). El worker consume `projects/mofgw/task_plan.md`. Antes de lanzar: quitar el marcador "auto-queue: exhausted" del task_plan y `worker-manager.sh sync`.

---
Status: **Approved by Pablo on 2026-08-17** (Telegram, topic mofgw).