# Review — 013-001-subprocess-core

**Reviewer model:** mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash — anti-confirmation-bias).
**Fecha:** 2026-08-12
**Commit revisado:** RED `7d6737d`, GREEN `d07de8c`, fixes bloqueantes `b9cbd33`.

## A) Contract correctness (P1–P13, I1–I6)

| Postcondición | Veredicto | Evidencia |
|---------------|-----------|-----------|
| P1 sesión determinística por clientID | PASS | `NewSessionKey` = sha256(clientID)[:16] hex; determinístico |
| P2 derivación client+model | PASS | seed clientID\|model; tests cubren 3 sub-casos |
| P3 multi-modelo + modelo per-request | PASS | `Serves` itera models; model de `req.Model` a `Backend.Args` |
| P4 prompt único = último user | PASS | `TranslateReq` extrae último user; stdin escribe una vez |
| P5 sin tools en prompt | PASS | TranslateReq solo devuelve content del último user; engine no pasa tools |
| P6 usage fabricado en Complete | PASS | `fillResponse` + `fabricateUsage` (Total=Prompt+Completion≥0) |
| P7 usage fabricado en Stream | PASS | chunk final `{"usage":{...}}` + [DONE], compatible con CaptureUsage |
| P8 serialización por sesión | PASS (post-fix) | map[ID]chan cap 1; lock sostenido todo el stream; release en todos los paths |
| P9 espera ctx-cancelable | PASS | select slot/ctx.Done → ErrUpstream, sesión reutilizable |
| P10 taxonomía de fallos | PASS | spawn→network, exit≠0→upstream_error, timeout→Type:timeout; nunca leak ctx crudo |
| P11 refusal→400 no-retryable | PASS (post-fix) | `Backend.IsRefusal` (frontera Backend) → invalid_request_error/400 |
| P12 sanitize | PASS | todos vía `NewErrUpstream`; mensajes controlados |
| P13 fail-closed clientID vacío | PASS | 400 antes de spawn |
| I1 Provider intacto | PASS | `var _ provider.Provider`; firmas coinciden |
| I2 frontera Backend | PASS (post-fix) | engine sin keywords hardcodeadas; `IsRefusal` en Backend |
| I3 hermeticidad | PASS | stub CLI + test-double; cero red |
| I4 sin tools en engine | PASS | engine no toca tools |
| I5 calidad Go | PASS (verificado empíricamente) | `go test ./... -race` 531/20, `go vet` limpio, gofmt limpio |
| I6 sin scoping | PASS | ningún test/código toca scoping |

## B) Test↔postcondition relevance audit (gate 3→4)

**PASS.** 19/19 tests presentes y correctamente mapeados a sus postcondiciones/criterios (tabla §4.2 del spec). 0 tests huérfanos. 0 postcondiciones sin test. Ningún test aserta estructura interna / mocks sobre plumbing — todos verifican comportamiento observable (session IDs, stdin, usage, error types, spawn counts).

## C) Hallazgos por severidad

### Bloqueantes (2 — RESUELTOS en commit b9cbd33)

1. **Goroutine leak + session lock starvation en Stream (P8)** — *Important.* Si el consumidor abandona el canal, los sends bloquean, `release()` no corre y el lock de sesión se cuelga para siempre; CLI huérfano. **Fix:** sends con `select ctx.Done` + `cmd.Process.Kill()` + `cmd.Wait()` (reap) + release por defer en todos los outcomes; scanner también con guard.
2. **`isRefusal` en el engine viola frontera Backend (P11/I2)** — *Important.* El motor interpretaba stderr con keywords hardcodeadas. **Fix:** `IsRefusal(stderr string) bool` agregado a la interfaz `Backend`; el engine delega la detección; test-double la implementa.

### No-bloqueantes (5)

- **Minor #3:** `fillResponse` sobreescribe usage real del backend si lo proveyera. Deuda para 013-004 (preservar si no-cero).
- **Minor #4:** scanner stdout pierde `sc.Err()` silenciosamente (línea >1MB). Aceptable; documentar en 013-004.
- **Nit #5:** mensaje "invalid backend output" con status 0/network (edge case poco probable).
- **FYI #6:** `cmd.Env = os.Environ()` expone todas las env al hijo. 013-004 podría allowlist.
- **FYI #7:** lock map nunca se limpia (slow leak). TTL de sesiones es 013-004.

## D) Conclusión

- **2 bloqueantes resueltos** (verificados empíricamente: suite verde tras el fix).
- **5 no-bloqueantes** — 3 deuda documentada para 013-004, 2 FYI.
- **Relevance audit: PASS** (19/19 tests ↔ postcondiciones).
- **Evidencia empírica:** `go build` OK, `go vet` limpio, `go test ./internal/subprocess/ -race` 19/19, `go test ./... -race` 531 tests en 20 paquetes.

Status: Review passed — 0 bloqueantes pendientes. Aprobado para merge por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
