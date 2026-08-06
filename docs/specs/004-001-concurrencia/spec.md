# Spec — 004-001-concurrencia + 004-002-aislamiento: Alta carga multi-agente

---
feature_id: 004-001-concurrencia
feature_id_2: 004-002-aislamiento
epic: mofgw-004-escala
status: draft (pendiente aprobación Pablo; cdad-cycle heartbeat 05 Ago 2026)
created_at: 2026-08-05
updated_at: 2026-08-05
depends_on: 001-001-endpoint, 001-007-auth, 002-004-degradacion
paralelizable: no (004-002 depende de 004-001)
---

```yaml
context: >
  mofgw es el gateway por el que pasan TODOS los agentes clientes
  (OpenClaw, crons, hb, futuros agentes clientes — decisión Pablo 04 Ago:
  "todos los agentes privados apuntan a mofgw"). Hoy el proxy no tiene
  ningún límite de concurrencia: cada request es una goroutine del
  http.Server y el número de requests simultáneos queda a merced del
  tráfico entrante. Un solo agente con un loop de retry agresivo (o un
  cliente con N agentes) puede saturar el proxy y degradar a TODOS los
  demás. El criterio de aceptación del programa completo dice: "10+
  agentes clientes concurrentes sin degradación". Para eso se necesitan
  (a) límites de concurrencia globales con backpressure controlada
  (004-001) y (b) aislamiento por cliente/agente: que un cliente que
  consume su propio budget no afecte a los otros (004-002).
resolution: >
  004-001 (concurrencia global): semáforo global de requests en vuelo
  (buffered channel), configurable con `server.max_concurrent_requests`
  (default 0 = ilimitado, para no cambiar comportamiento de deploys
  existentes; el deploy de producción lo setea explícito).
  Cuando el semáforo está saturado, el request ESPERA hasta
  `server.backpressure_timeout` (default 10s) por un slot; si el timeout
  expira sin slot → 429 `rate_limit_exceeded` (formato OpenAI-compatible,
  misma función openAIError). No hay cola ilimitada: esperar con timeout
  ES la backpressure — acota la latencia extra del cliente sin acumular
  requests indefinidamente ni crashear el proxy. La espera respeta el
  context del cliente: si el cliente cancela, se aborta la espera.
  El semáforo se adquiere DESPUÉS de auth y parsing de body (un request
  no autenticado no debe consumir slots) pero ANTES de tocar el router
  (un request que va a gastar upstream debe pasar por el límite).

  004-002 (aislamiento por cliente/agente): además del semáforo global,
  cada cliente autenticado (001-007) tiene su propio semáforo
  `clients[].max_concurrent_requests` (default 0 = ilimitado). La
  identidad del agente se toma del header `X-Agent-Id` (opcional; si
  viene, se usa para accounting y para `clients[].max_concurrent_per_agent`
  — límite por (cliente, agente), default 0 = ilimitado). El semáforo
  del cliente se adquiere después del global; si el del cliente está
  saturado → 429 inmediato (sin espera: un cliente que excede su budget
  no debe hacer esperar a los demás; el 429 es el mecanismo de
  aislamiento — el cliente recibe backpressure clara y puede hacer retry
  con backoff propio). El `X-Agent-Id` es de SOLO lectura: se usa para
  accounting/límites pero NUNCA se reenvía upstream (el proxy es
  transparente con el body, pero los headers internos de ruteo no son
  parte del contrato OpenAI-compatible; provider.setHeaders no lo copia).
postcondition: >
  Con `server.max_concurrent_requests: N` el proxy nunca tiene más de N
  requests en vuelo hacia upstream: el resto espera (hasta
  backpressure_timeout) o recibe 429. Con `clients[].max_concurrent_requests`
  un cliente no puede saturar el proxy por encima de su budget: sus
  requests extra reciben 429 inmediato SIN afectar a otros clientes. El
  `X-Agent-Id` permite límites finos por agente sin exponer nada
  upstream. Los 429 son formato OpenAI-compatible, con métrica
  `mofgw_rejected_requests_total{client,reason}` (reason=global_timeout |
  client_limit | agent_limit). Comportamiento default (todo en 0) =
  idéntico al actual (sin límites) — backward compatible. Todos los
  tests pasan con `go test -race` (cero red externa).
```

## Postcondición (resumen verificable)

1. `server.max_concurrent_requests > 0` → a lo sumo N handlers en vuelo hacia el router; requests extra esperan ≤ `backpressure_timeout` y luego 429 `rate_limit_exceeded`.
2. `clients[].max_concurrent_requests > 0` → un cliente que excede su budget recibe 429 inmediato (sin espera); los demás clientes no se ven afectados.
3. `clients[].max_concurrent_per_agent > 0` → límite por (cliente, X-Agent-Id); sin header, el límite aplica al cliente como agente único.
4. `X-Agent-Id` nunca llega upstream (verificado por test de header).
5. Defaults en 0 → sin límites, comportamiento actual intacto (test de regresión).
6. 429 en formato OpenAI-compatible (`error.type = "rate_limit_exceeded"`).
7. Suite completa verde con `-race`.

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`:
  - `ServerConfig`: + `MaxConcurrentRequests int` (default 0), + `BackpressureTimeout time.Duration` (default 10s).
  - `ClientConfig`: + `MaxConcurrentRequests int`, + `MaxConcurrentPerAgent int` (ambos default 0).
  - Validaciones: valores ≥ 0; `backpressure_timeout` ≥ 0.
- `internal/limiter/limiter.go` (nuevo paquete):
  - `type Limiter struct { slots chan struct{} }` con `New(n int)`, `Acquire(ctx) bool` (no-blocking si n==0), `Release()`.
  - `type Keyed struct { mu sync.Mutex; limits map[string]*Limiter; max int; perAgent int }` con `Acquire(clientID, agentID string, ctx) (release func(), ok bool)` — implementa la semántica: global-timeout para el global; fast-fail para client/agent.
- `internal/proxy/proxy.go`:
  - `Server` + campos `globalLimiter *limiter.Limiter`, `keyed *limiter.Keyed`, `backpressureTimeout time.Duration`.
  - `handleChat`: después de auth (el clientID sale de `s.auth.Authenticate`) y parsing → adquirir global (con timeout) → adquirir client (fast-fail) → adquirir agent (fast-fail) → router. Release en defer.
  - El `X-Agent-Id` se lee del header del request entrante (solo lectura).
- `internal/metrics/metrics.go`:
  - + `IncRejected(client, reason string)` → `mofgw_rejected_requests_total{client,reason}`.
- `cmd/mofgw/main.go`: wire del limiter con los valores del config.

## Criterios de aceptación

- [ ] Config acepta los 3 campos nuevos con defaults backward-compatible (0 = ilimitado, 10s backpressure).
- [ ] Test: con max_concurrent_requests=1 y 2 requests simultáneos (handler de upstream que duerme), el segundo espera y completa OK (no 429) si el primero termina dentro del timeout.
- [ ] Test: con max_concurrent_requests=1, upstream que nunca responde (context cancelado manualmente o timeout corto), el segundo request recibe 429 `rate_limit_exceeded` al vencer backpressure_timeout.
- [ ] Test: con clients[].max_concurrent_requests=1, dos requests del MISMO cliente → el segundo 429 inmediato; dos requests de clientes DISTINTOS → ambos OK.
- [ ] Test: con max_concurrent_per_agent=1, dos requests del mismo (client, X-Agent-Id) → segundo 429; distinto X-Agent-Id → OK.
- [ ] Test: `X-Agent-Id` no aparece en los headers del request upstream.
- [ ] Test: defaults (todo 0) → N requests simultáneos pasan sin límite (regresión).
- [ ] Test: 429 con formato OpenAI-compatible y métrica `mofgw_rejected_requests_total` incrementada.
- [ ] Suite completa `go test -race -count=1` verde.

## Riesgos / notas

- El semáforo global con espera puede aumentar la latencia p99 bajo saturación (diseñado: backpressure > error inmediato para tráfico legítimo). El 429 solo aparece si la saturación persiste > backpressure_timeout.
- Un request que espera en el semáforo global retiene su conexión HTTP: con backpressure_timeout default 10s y conexiones limitadas del cliente, esto es aceptable (el cliente típico es un agente con timeout propio > 10s).
- No hay prioridad entre clientes (fairness FIFO del channel). Multi-tenancy con prioridades sigue fuera de scope (decisión plan).
- El límite por agente usa el header X-Agent-Id tal cual viene: un cliente puede falsear su propio X-Agent-Id para eludir SU límite (no es un límite de seguridad, es de cortesía/aislamiento). La autenticación real sigue siendo la API key.
