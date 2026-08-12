# Review — 011-001-responses-endpoint

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

### 1. Test G (P12) no ejercita la cache — pasa por razón incorrecta
**Ubicación:** `internal/proxy/e2e_011001_responses_test.go:492-546`

**Problema:** El test G pretende verificar P12 ("cache separada por endpoint"), pero ninguna de las dos requests es elegible para cache. `responsesBody()` no setea `temperature`, por lo que `rb.Temperature == nil` en responses.go:200 → `isDeterministic = false` → la cache se salta por completo. Ambas requests son MISS obligatorios que llaman al upstream (calls=2), independientemente de si existe separación de cache o no. Además, incluso con temperature, los bodies canónicos son estructuralmente distintos (`input` vs `messages`) → hashes distintos sin necesidad del namespace. El test no puede distinguir "namespace separado" de "bodies distintos producen keys distintas".

**Decisión (priorización):** **APLICAR** — remedio mínimo. Agregar `temperature: 0` a ambos bodies del test G para ejercitar el camino de cache (HIT/MISS real). El namespace `"responses"` en `responsesCacheKey` es correcto y defensivo y se mantiene; el test verifica el comportamiento observable de P12 (un endpoint nunca se sirve desde la cache del otro). Se documenta que la separación de namespace por endpoint es un invariante estructural garantizado por el hash del body wire responses (que nunca coincide con el body wire chat), no end-to-end demostrable por el test.

## Opcionales

### 2. Pipeline incompleta: faltan IncRequests, emitRequestTelemetry, contextAnalysis, emitRequestEnd
**Ubicación:** `internal/proxy/responses.go:100-299`

**Problema:** P4 dice "MISMA pipeline". `handleResponses` omite `s.metrics.IncRequests()`, `s.emitRequestTelemetry(...)`, `composition.Analyze(...)` y `defer s.emitRequestEnd(...)` con `statusRecorder`, que sí están en `handleChat` (proxy.go:407-744). La telemetría de descubrimiento y análisis de contexto no ve tráfico de responses.

**Decisión:** **APLICAR** — replicar las cuatro llamadas en el mismo orden que handleChat, para que la observabilidad no quede silenciosamente fuera de un endpoint que comparte la pipeline.

### 3. Falta rate limiting a nivel de agente
**Ubicación:** `internal/proxy/responses.go:188-196`

**Problema:** handleChat verifica `s.keyedLimiter.AcquireAgent(clientID, agentID)` (proxy.go:486-496). handleResponses solo hace `AcquireClient` — un agente puede exceder su límite individual vía /v1/responses.

**Decisión:** **APLICAR** — replicar el bloque `AcquireAgent` de handleChat.

### 4. Falta singleflight (coalescing de requests idénticos)
**Ubicación:** `internal/proxy/responses.go` (ausente)

**Decisión:** **DESCARTAR como deuda documentada.** No está en el spec; el coalescing de requests de /v1/responses es una feature propia para un epic/fase posterior. Queda en deuda técnica.

### 5. Doble JSON marshaling innecesario
**Ubicación:** `internal/proxy/responses.go:160-168, 67-83`

**Decisión:** **DESCARTAR** (nit). Overhead mínimo en requests no-cache-eligible; los cache-eligible hacen el parseo una sola vez. No vale el riesgo para una optimización menor.

### 6. model del envelope usa rb.Model (modelo pedido), no modelo resuelto por router
**Ubicación:** `internal/proxy/responses.go:293`

**Decisión:** **NO APLICAR** (FYI). Patrón consistente con handleChat (proxy.go:672/737 usan req.Model igual). Sin aliasing de modelo en el router, `rb.Model == modelo resuelto`. El test C confirma que el model del envelope == modelo del cliente.

### 7. Sin validación de role en items de input[]
**Ubicación:** `internal/proxy/responses.go:150-159`

**Decisión:** **DESCARTAR** (nit). El upstream rechaza roles inválidos; el error será menos claro pero funcional. No vale agregar validación duplicada del upstream.

## Auditoría test↔postcondición

- **Postcondiciones cubiertas:** P1 (bloque A), P2 (B), P3 (C), P4 (E), P5 (C), P6 (F), P7 (F), P8 (F), P9 (F), P10 (C), P11 (D), P13 (H), P14 (I).
- **Postcondiciones sin test efectivo:** P12 (bloque G) — existe pero no ejercita la cache (fix en bloqueante #1).
- **Tests sin postcondición (sobrantes):** ninguno.
- **Tests con dependencia de estructura interna (AP-14):** ninguno. Todos usan HTTP público contra `Server.Handler()`, status, envelopes de error wire, bodies de respuesta. `countingUpstream011` cuenta llamadas a un upstream fake (observable externo).
- **Nota P12:** el test G no verificaba la separación de cache empíricamente (ver bloqueante #1). Fix aplicado.

## Invariantes del spec

- **I1 (router/providers intactos):** ✓ Verificado. `ParseChatRequest`, router y providers sin cambios. Traducción vive solo en `responses.go`.
- **I2 (arguments como string JSON):** N/A en 001 (tools se rechaza en P7).
- **I3 (zero llamadas a OpenAI):** ✓ `grep "api.openai.com"` → cero coincidencias. Todo request va por `s.router.Complete()` a providers configurados.
- **I4 (envelope de error consistente):** ✓ Todos usan `openAIError()` (proxy.go:399), misma función que handleChat.
- **I5 (suite verde -race, cero red):** ✓ `go build`/`go vet` exit 0; tests feature verdes.
- **I6 (auth sin regresión):** ✓ Ruta bajo `s.auth.Wrap`. Test A lo confirma.

---

Status: Review completado. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12). Bloqueante #1 y opcionales #2, #3 a aplicar; #4, #5, #6, #7 desestimados con motivo.
