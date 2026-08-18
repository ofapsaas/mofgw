# Epic 014 — Closure

Cerrado: 2026-08-18

## Resumen

El epic mofgw-014 entregó el **registro unificado de accounting + outcome** (única feature 014-001). Antes había 2 fuentes incompletas que no se cruzaban: **telemetry.jsonl** (sample 10%, timestamped pero logueado al **entrar** el request → sin provider, sin tokens, sin costo) y **/metrics + state.json** (provider/modelo/cliente + tokens + costo **exactos**, pero **acumulados desde arranque**, sin ventana temporal). Consecuencia: no se podía responder "cuánto gastó el cliente X en el provider Y esta semana" ni fallos por provider con resolución temporal.

Ahora hay un **registro append-only (JSONL) en disco** que emite, por cada request que llega a la cadena de routing, un **evento por intento** (cada visita a un provider: request_id, ts, client, provider, model, outcome ok|fallback, cause, status, attempt, retries) + un **evento terminal** (outcome success|error, error_code, status, final_provider, tokens prompt/completion/cache/reasoning, cost_usd, stream). Es la **fuente de verdad** para cualquier agregación por período × provider × modelo × cliente, incluida la estadística de fallos. El alcance de ESTE epic fue **solo emitir el registro**; la consolidación/visualización es un epic EXTERNO que consume el JSONL sin tocar mofgw.

## Features entregadas

| Feature                    | Descripción                                                                                | Cierre      |
| -------------------------- | ------------------------------------------------------------------------------------------ | ----------- |
| 014-001-registro-unificado | Registro append-only (JSONL) por request: evento por intento + evento terminal (accounting + outcome) | 18 Ago 2026 |

## Criterios de aceptación

- ✅ Un request emite **eventos de intento + un evento terminal**, con outcome y clasificación de error (success/error + code/status, ok/fallback + cause). Evidencia: suite 622/23 verde, `emitAttempt` (router) + `emitTerminalSuccess`/`emitTerminalError` (proxy).
- ✅ El registro tiene `provider, model, client, tokens, cost` por request → permite responder "tokens/cost de provider X modelo Y cliente Z <período>" con números **exactos**. Evidencia: esquema `attempt`/`terminal` + `cost_usd` = `estimateCost` (misma fórmula que /metrics).
- ✅ **Consolida fallos incluso cuando el request final fue exitoso** (nivel intento): muestra qué provider patinó y por qué. Evidencia: evento `attempt` con `outcome: fallback` + `cause` en cada visita, post-decisión.
- ✅ Sobrevive restarts (append-only en disco) y **no depende del sample** del telemetry actual. Evidencia: `NewWriter` O_APPEND|O_CREATE, escritor append-only; convive con 009-000 sin sustituirlo (P17).
- ✅ Suite completa `-race` verde (622/23); el evento **no altera el request path** (aditivo, reversible, off por default). Evidencia: review APPROVE 0 bloqueantes; aditividad off-default verificada con suite completa.

## Retrospectiva breve

### Lo que funcionó bien
- **El esquema del evento como contrato externo tipado (D1/D12):** el epic de consumo consolida/visualiza leyendo el JSONL sin tocar mofgw; la frontera quedó en el formato del evento, no en código.
- **Hooks existentes reutilizados sin refactor:** `recordCacheTokens` (proxy.go + responses.go + embeddings.go), `handleChainError`, `classify` y `ChainError.Code` se elevaron a emisores sin rediseñar la cadena.
- **Privacidad por construcción con grep negativo (C15):** el writer serializa SOLO el esquema; nunca contenido de prompts/respuestas/tools/headers/keys.
- **Aditividad off-default verificada con suite completa:** 622/23 `-race` verde, build/vet limpios, cero impacto en el request path.

### Lo que se complicó
- **El registro nació en medio de un ciclo largo** con mucho trabajo previo de re-priorización (014-001 estuvo BLOQUEADA en gate 2→3 esperando aprobación HITL mientras se priorizaban features standalone como 015-001).
- **Los 4 bugs de test detectados en GREEN** (keysIguales sort, MaxRetries 2→1, Cooldown time.Minute) fueron correcciones **legítimas** del test-writer, no relajación de la implementación (sin auto-satisfacción).
- **singleflight/stream interrumpido** requirieron decisiones D14/D15 para no perder eventos terminales (eventos por request físico; stream interrumpido = success con tokens capturados).

### Aprendizajes para futuros epics
- **El esquema como contrato externo tipado desacopla consumo de emisión:** definir el formato del evento como artefacto versionable permite que un epic externo lo consuma sin tocar mofgw.
- **La emisión post-decisión garantiza aditividad:** emitir al completar cada visita (con provider/tokens/costo ya conocidos) preserva el request path y da números exactos.
- **La privacidad se logra por construcción, no por filtrado:** serializar SOLO el esquema (y verificar con grep negativo) es más fuerte que depurar después lo que no debe salir.

## Deuda técnica que se llevó

- **Rotación/compresión/retention del JSONL no implementada** (append simple, igual que 009-000 — documentado como anti-scope de este epic; operativo a futuro).
- **O1 de la review:** tests dedicados para C17 (endpoints responses/embeddings) y P17 (convivencia con telemetry 009-000) como follow-up opcional, fuera del gate 4→5.
- **El epic EXTERNO de consolidación/visualización** (diario/semanal/mensual + visualizaciones) queda fuera de mofgw; consume el JSONL de este epic.

## Decisiones arquitectónicas tomadas

- **Sin ADR nuevo** (feature sin nueva llamada saliente ni frontera arquitectónica): la arquitectura es **aditiva** (writer de registro off-default + emisores post-decisión) y todas las decisiones D1-D15 están lockeadas en el spec `docs/specs/014-001-registro-unificado/spec.md`.
- **D1/D12** — writer dedicado tipado (sin slog) + esquema v1 extensible como contrato externo.
- **D2** — discriminador `type` (`attempt` | `terminal`) en cada evento.
- **D3** — `ts` en RFC3339 ms UTC.
- **D4/D5** — intento = visita concluida con `attempt`/`retries`; `cause` = typ de `classify`.
- **D7** — alcance only-routing.
- **D9** — `cost_usd` = `estimateCost(model, miss, completion, hit)` (misma fórmula que /metrics).
- **D14** — singleflight por request físico.
- **D15** — stream interrumpido = success con tokens capturados.
