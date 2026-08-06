# Spec — 008-003-stats-por-sesion: correlacionar requests por sesión

---
feature_id: 008-003-stats-por-sesion
epic: mofgw-008-contexto
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 006-001 (IncUsage), 001-007-auth (clientID)
paralelizable: no
---

## Contexto

El frente 4 de Pablo: "nivel de medición y optimización más complejo... depende
de lo anterior para hacerlo bien". Las features 006-001/006-002 ya acumulan
por cliente; 008-002 puso budget (ventana simple). Lo que falta para cerrar
el frente de contexto es la **correlación por sesión**: agrupar los requests
de una sesión lógica (un thread de conversación de un runtime) y exponer sus
stats.

La sesión se identifica por un header opcional: `X-Session-Id` (lo mandan
los runtimes que tienen sesiones — openclaw/openode tienen ids de sesión).
Sin el header, cada request es su propia sesión (agrupación trivial, sin
costo extra).

Esta feature habilita la **ventana deslizante** para budget (008-002 lo dejó
pendiente): con timestamps por request, un budget por sesión o por ventana
de tiempo se puede evaluar con precisión. Decisión de alcance: acá se
registran los timestamps y se exponen las stats por sesión; el budget
deslizante se aplica en un ajuste posterior si el operador lo pide (fuera de
alcance de esta feature — ver §Fuera de alcance).

## Postcondiciones

1. **P1 — Sesión identificada:** el header opcional `X-Session-Id` (de
   solo lectura, NUNCA forwardeado upstream — como X-Agent-Id en 004-002)
   identifica la sesión del request. Requests sin el header → sesión
   implícita por request (agrupación trivial).

2. **P2 — Registro con timestamp:** cada request atendido (no-stream y
   stream, exitoso) registra su consumo (tokens/costo, los mismos de
   006-001/006-002) asociado a (clientID, sessionID, timestamp). El
   registro habilita stats por sesión y, en el futuro, ventana deslizante.

3. **P3 — Stats por sesión:** `GET /v1/usage?session=<id>` (con auth del
   cliente) devuelve las stats de UNA sesión del cliente:
   `{session, client, requests: N, prompt_tokens, completion_tokens,
   total_tokens, cost_usd, first_request_at, last_request_at}`.
   `GET /v1/usage` (sin query) sigue devolviendo los totals del cliente
   (007-003 intacto) + una lista de sesiones recientes (`sessions: [{id,
   requests, total_tokens, cost_usd, last_request_at}]`). Solo las sesiones
   del cliente autenticado (aislamiento).

4. **P4 — Cardinalidad acotada:** el registro de sesiones tiene tope
   configurable (max_sessions_retained, default 100) — las sesiones más
   viejas se descartan (FIFO por last_request_at). Un runtime que rota ids
   de sesión no puede inflar la memoria indefinidamente.

5. **P5 — Robustez:** session vacío/ausente → sesión implícita; el endpoint
   con session inexistente → 404 o sesión con ceros (decisión: 404 para
   session desconocida del cliente, 200 con ceros para session que existe
   sin requests todavía — no hay caso real, ambos válidos; elegir 404 para
   session desconocida, consistente con "no existe").

## Invariantes

- I1: X-Session-Id es de solo lectura — se usa para correlacionar, nunca se
  envía upstream ni se loguea contenido.
- I2: Aislamiento por cliente: las sesiones de un cliente no son visibles
  para otro (mismo clientID del token).
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: El registro es best-effort (en memoria) — un crash pierde el histórico
  (consistente con el resto del accounting).

## Criterios de aceptación

- C1: 3 requests con X-Session-Id "s1" del mismo cliente → GET
  /v1/usage?session=s1 devuelve requests 3 y tokens/costo acumulados.
- C2: Requests de 2 clientes distintos con el MISMO session id "s1" → cada
  cliente ve su propia sesión s1 (aislamiento por clientID+session).
- C3: Request sin X-Session-Id → GET /v1/usage (sin query) lista sesiones
  sin incluir un grupo espurio (sesión implícita por request, o no listada).
- C4: GET /v1/usage?session=inexistente → 404.
- C5: El header X-Session-Id NO llega upstream (gotBody sin el header).
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/metrics/metrics.go`: registro por sesión (map
  clientID|sessionID → {requests, tokens, cost, timestamps}) con
  max_sessions_retained. Métodos `RecordSession(client, session, usage,
  cost, now)` y `SessionSnapshot(client, session)` / `SessionsList(client)`.
- `internal/proxy/proxy.go handleChat`: leer X-Session-Id (solo lectura),
  pasarlo a recordCacheTokens/IncUsage (o un RecordSession separado),
  handler /v1/usage?session= (extender handleUsage).
- `internal/config/config.go`: `max_sessions_retained` (default 100).
- Tests: e2e_008003_test.go (C1-C6).

## Fuera de alcance

- Budget deslizante por ventana (aplicar límites sobre la ventana con
  timestamps) — el registro lo habilita; la aplicación se decide aparte.
- Persistencia de sesiones entre restarts — en memoria, consistente.
- Análisis de contenido de sesión (nunca — privacidad).
