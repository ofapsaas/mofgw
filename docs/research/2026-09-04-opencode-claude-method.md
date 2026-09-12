# Estudio: método de opencode-claude y portabilidad a mofgw

**Fecha:** 2026-09-04 · **Fuente:** github.com/openchamber/opencode-claude @ main (v0.14.0, MIT)
**Objetivo:** resolver la deuda del epic 013 (ADR-010 D4: sin passthrough de `tool_calls`).

## 1. Arquitectura

Plugin OpenCode (Bun) que levanta `Bun.serve` en puerto efímero (`startProxy`, proxy.ts:213,
con retry si ADDR-IN-USE y health-check `isProxyHealthyAt`:187). OpenCode rutea el provider
`claude-code` a ese baseURL. Cadena: `OpenCode → /v1/chat/completions (HTTP) → proxy → Agent SDK
query() → claude CLI (OAuth suscripción)`. El plugin nunca toca credenciales.

## 2. Mapeo del protocolo OpenAI ↔ SDK

- `handleChatCompletions` (proxy.ts:350): `conversationKey = requestKeyNamespace(metaKind) +
  (session-header || hash)`. El hash (`conversationKeyFromMessages`, session-store.ts:79) usa los
  **primeros 200 chars del PRIMER mensaje user** — deliberadamente NO la cantidad de mensajes
  (cambia por turno y rompía el resume).
- Multi-turno: NO reenvía historial si hay resume; envía solo el último prompt user
  (`latestUserPrompt`, prompt.ts:396). Si el resume no es posible, serializa el historial previo
  en el prompt (`buildConversationTranscript` con tope `historyMaxChars`, prompt.ts:534/460).
- Meta-requests (título/resumen de OpenCode, `request-kind.ts`): sin tools, sin resume,
  `maxTurns: 1`, thinking disabled (proxy.ts:611-613).

## 3. Park/resume de tool_calls (el mecanismo crítico)

**Registro de tools** (`buildOpenCodeMcpServer`, proxy.ts:800): cada tool OpenAI del body se
convierte en tool MCP **in-process** del SDK (`createSdkMcpServer` + `tool()` con JSON Schema →
zod). Los built-ins del CLI se apagan (`tools: []`, query.ts:291) y se aliasan
(`bash→mcp__opencode__bash`, etc., proxy.ts:554-574) con `allowedTools` y
`permissionMode: "bypassPermissions"` + system prompt preset `claude_code` con append
("use only mcp__opencode__* tools").

**El park** (proxy.ts:863-883): el handler de cada tool genera `call_<uuid>`, registra una
`ParkedToolCall {resolve, reject}` en `pendingTools` del bridge, llama `onPark()` y **await a la
promesa**. El turno del SDK NUNCA muere: queda suspendido dentro del handler esperando.

**Emisión al cliente:** `onPark` despierta la carrera `Promise.race([nextEvent, parkPromise,
stallPromise])` del loop `consumeStream` (proxy.ts:663-745) → el stream emite `__park__` →
`mapSdkEvent` (proxy.ts:1496) → chunks OpenAI con `tool_calls` y `finish_reason: "tool_calls"`
(proxy.ts:1009 buffered / :1411 stream). La respuesta HTTP termina ahí.

**El resume:** el siguiente request del cliente trae mensajes `role:"tool"` → `collectToolResults`
(proxy.ts:326) → match del bridge por conversationKey, con fallback por `tool_call_id`
(proxy.ts:368-376) → `tool.resolve(result)` despierta al handler MCP → devuelve `{content:[text]}`
al SDK → Claude continúa el mismo turno → `bridge.continueStream()` (proxy.ts:767) reabre el
generador y la nueva respuesta HTTP streamea la continuación. Resultados parciales → re-emite
los tool_calls pendientes (proxy.ts:407-419). Un solo bridge por conversación: el nuevo turno
supersede al viejo (reject + close, bridge-pool.ts:29-46).

## 4. Sesión sticky

`system/init` del SDK trae `session_id` → `setForeignSessionId` persiste en
`~/.local/share/opencode-claude/sessions.json` (session-store.ts:17-64). Resume via opción
`resume` del SDK. Pre-check defensivo: si el transcript `~/.claude/projects/<slug>/<id>.jsonl`
no existe, el resume produce sesión sin contexto → borra binding e **inyecta historial serializado
en el prompt** (session-store.ts:101-119, prompt.ts:567). `forgetDeadSession` limpia al morir.

## 5. Effort variants

Catálogo: aliases `fable/opus/sonnet/haiku` + pines (models.ts:45-56). Variantes
`low/medium/high/xhigh/max` via header `x-opencode-claude-effort` (proxy.ts:341) → opción
`effort` del SDK + `thinking: {type:"adaptive"}` por defecto (query.ts:228-236).

## 6. Rate limit

Tres fuentes capturadas (rate-limit.ts): (1) `rate_limit_event` del SDK con
`rate_limit_info` estructurado (proxy.ts:1504-1512); (2) assistant sintético `error:"rate_limit"`
(proxy.ts:1563-1576); (3) texto del result/exception parseado
(`isClaudeRateLimitText`/`parseResetTimeFromText`, rate-limit.ts:124/138). Estado: `limited`,
`limitedUntil`, `resetsAt` → snapshot para `GET /v1/rate-limit` y `/health`. **Gate:** con límite
confirmado, `rateLimitGate` (rate-limit.ts:297) hace fail-fast 429 + `Retry-After` +
`x-claude-rate-limit-reset` SIN spawnear CLI condenado (proxy.ts:1132-1183).

## 7. Robustez (oro para copiar)

- **probeTurnEvents** (proxy.ts:1095): retiene el head HTTP hasta el primer contenido real;
  si el turno muere antes → error HTTP veraz, **nunca fake-200**. Motivación con evidencia:
  "ese doom loop quemó ~4% de una cuota semanal el 2026-08-11" (proxy.ts:777).
- **Stall watchdog** (proxy.ts:686): silencio total del CLI por N segundos mata el turno con
  error claro (cualquier evento o park resetea el reloj).
- **SSE heartbeat** `: ping` cada pocos segundos (proxy.ts:1230) para hops que matan SSE silente.
- **Usage dedup** por `message.id` (`seenAssistantUsageIds`) — el SDK emite usage repetido.
- Cancel del stream → kill del process tree del CLI (query.ts:87-120); sin eso, CLI huérfano +
  bridge eterno (proxy.ts:1213).

## 8. Clasificación de portabilidad a mofgw

**ACTUALIZADO 2026-09-04 (tarde):** Pablo fijó constraint: mofgw es **un único binario real Go,
sin Node/Bun** — la ruta sidecar (B) queda descartada. Se validó empíricamente una ruta Go puro
que NO requiere reverse-engineering del control protocol SDK↔CLI: **park a nivel MCP**.

### Ruta A validada empíricamente (CLI 2.1.260, test en ~/tmp/mcp-park-test/)

Diseño: mofgw genera un `mcp.json` efímero cuya única entrada es un **servidor MCP stdio
servido por el propio mofgw** (JSON-RPC over stdio, sin deps — ~100 líneas de Go), exponiendo
las tools OpenAI del request como `mcp__mofgw__<name>`. Spawn:

```
claude -p <prompt> --output-format stream-json --verbose --include-partial-messages \
  --mcp-config <generado> --allowedTools mcp__mofgw__* \
  --dangerously-skip-permissions --effort <level> --session-id <uuid>
```

Ciclo verificado en vivo (logs en el dir de test):
1. `assistant` con bloque `tool_use` llega a stdout **mientras la tool MCP sigue estacionada**
   (26s de park en el test, sin timeout) → mofgw emite `tool_calls` OpenAI y termina la
   respuesta HTTP; el proceso CLI queda vivo esperando la respuesta JSON-RPC.
2. Siguiente request del cliente con `role:"tool"` → mofgw resuelve el `tools/call` MCP
   (channel de Go) → el CLI continúa SOLO: `user/tool_result` → texto → `result` final con
   `session_id`, `num_turns` y **usage real** (input/cache_read/output).
3. **Bonus verificado:** `rate_limit_event` aparece en el stream crudo del CLI (sin SDK) —
   el gate de rate-limit es portable tal cual.
4. Detalle menor: el CLI hizo un `ToolSearch` interno antes de llamar la tool MCP
   (deferred tool loading); transparente para mofgw, suma un turno.

Knobs: `MCP_TOOL_TIMEOUT` (hard wall-clock por call; subir al spawn, p.ej. 900000) y
`MCP_TIMEOUT` (startup) — verificados como env vars del binario.

| Mecanismo | Plugin (proxy.ts) | Ruta Go puro (validada) |
|---|---|---|
| Park/resume tools | MCP in-process SDK + promesas | MCP stdio de mofgw + channel Go, park en `tools/call` |
| Sesión sticky | conversationKey→resume SDK | proceso vivo = sesión; `--session-id`+`--resume` para crash-recovery |
| Effort/thinking | opción SDK | flag CLI `--effort` (existe en 2.1.260) |
| Rate limit | `rate_limit_event` SDK | `rate_limit_event` en stream-json crudo ✓ |
| Anti fake-200 / watchdog / heartbeat | probeTurnEvents etc. | re-implementar en Go (lógica clara, ~200 líneas) |
| Historial sin resume | prompt.ts serializa | misma técnica, prompt único |

## 9. Recomendación (revisada)

**Ruta A (Go puro, park a nivel MCP)** — cumple el constraint de binario único, usa solo
superficie documentada del CLI (`--mcp-config`, `--input/output-format stream-json`), y el
mecanismo crítico quedó verificado empíricamente. El riesgo que yo había asignado a la ruta A
(hablar el control protocol del SDK) **desaparece**: no hacemos de SDK, hacemos de MCP server.

Cambios en mofgw:
1. `internal/subprocess/claude`: extender el adapter — argv con `--mcp-config` generado +
   `--include-partial-messages` + `--effort`; motor subprocess ahora sostiene el proceso vivo
   entre requests (park) en vez de exec-per-request con lock.
2. Nuevo `internal/mcpserver`: servidor MCP stdio con tools dinámicas por request (map
   `tool_call_id → chan result`), generado del `body.tools` del cliente.
3. `TranslateOut/TranslateStreamOut`: emitir `tool_calls` OpenAI desde bloques `tool_use`,
   consumir `role:"tool"` como release del park; usage real desde `assistant`/`result`.
4. Gate rate-limit (Go, lógica de rate-limit.ts portada directo).
5. vendor: nada. Attribution conceptual al plugin en el ADR (MIT, estudio, no código).

Licencia MIT del plugin: solo referenciamos el método (ingeniería inversa de conceptos
observables), no copiamos código → sin obligación de vendor, crédito en el ADR igual.
