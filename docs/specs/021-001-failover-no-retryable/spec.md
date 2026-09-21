---
context: >-
  Incidente 21-sep-2026: la cuenta OpenCode Go de acct-6 quedó sin suscripción y el upstream devolvió 403 "An active OpenCode Go subscription is required to use Go models" en cada chat. mofgw abortaba TODA la cadena ante un error no-retryable: 161 chain errors, 0 éxitos entre 16:10 y el fin del diagnóstico, sin probar acct-vx/acct-2 (sanos). Diagnóstico en logs del servicio local (journal mofgw.service PID 800 + registry.jsonl).
resolution: >-
  Corrección semántica del router — no-retryable (4xx: 400/403/404/413) es fatal para el PAR (provider, request): el provider se descarta del set de candidatos de ese request y la cadena continúa. Terminación solo por agotamiento total (exhaustedChain, que pasa el 4xx crudo) o tope maxAttempts. Sin cooldown en el skip (recordFailure no se invoca) y sin reintento del mismo provider.
postcondition: >-
  Un error no-retryable descarta SOLO al provider que lo devolvió; la cadena continúa; el cliente ve error solo si todos los candidatos fallaron.
verification: |
  - TestNoRetryable403Failover (router_test.go) → éxito vía p2, p1:1 p2:1 ✓
  - TestNoRetryable400Failover ✓
  - TestNoRetryableNoSameProviderRetry: p1:1 con RetryConfig.MaxAttempts=3 ✓
  - TestNoRetryableNotRevisited (2 subtests: éxito vía p3 / agotamiento con cada provider 1 vez) ✓
  - TestNoRetryableNoCooldown: cooldowns.IsCooling("p1")==false tras skip ✓
  - TestCircularNoRetryableFailsOver (REEMPLAZA TestCircularNoRetryableCorta que fijaba el bug) ✓
  - TestNoRetryableFailover_Stream (pre-primer-byte) ✓
  - TestNoRetryableFirstEventFailover_Stream (rama TTFB) ✓
  - TestNoRetryableFirstEventFailover_NoTTFB_Stream (rama timeout<=0, POST-AUDIT) ✓
  - POST-AUDIT: TestUpstream400StillNoFallback→TestUpstream400FailsOver, TestClientErrorNoFallback→TestClientErrorFailsOver, TestE2E010002_C11_400NoReintenta→TestE2E010002_C11_400Failover (mods justificadas: fijaban el abort viejo) ✓
  - Suite: go test ./... → 1007 passed / 0 failed / 37 packages; go build ./... OK; go vet OK ✓
---

# Spec — 021-001-failover-no-retryable

> No-retryable (4xx) es fatal para el **PAR (provider, request)**, no para la
> cadena: el provider se descarta de ese request y la cadena continúa con el
> siguiente candidato. Terminación solo por agotamiento total o tope
> `maxAttempts`.

## Decisions

| # | Decisión | Detalle |
|---|----------|---------|
| D1 | Skip-semantics | Ante un 4xx no-retryable (400/403/404/413) el intento se registra (`emitAttempt` "fallback") y el provider se **excluye** del set de candidatos de ese request; la cadena continúa con el siguiente. El abort inmediato de TODA la cadena (bug del incidente) desaparece. Terminación solo por: agotamiento total (todos los candidatos excluidos → `exhaustedChain`, que pasa el 4xx crudo del último error) o tope `maxAttempts`. |
| D2 | Sin cooldown | El skip **no invoca `recordFailure`**: el error es del PAR (provider, request), no del provider en sí. El provider no entra en cooldown global — no se castiga ante clientes con payload específico; la cuenta queda sana para requests siguientes. |
| D3 | Exclusión por request | `excluded map[int]bool` es variable local del loop de cada invocación (`complete`/`stream`), nunca estado global del Router. Sin reintento del mismo provider dentro del request (la exclusión lo saca de la rotación cíclica). |
| D4 | 4 call-sites corregidos (abort → skip) | `complete`: :977 → ~950 (skip en :1022-1030, `excluded[idx] = true` :1027). `stream`: :1139 → ~1205 (error de `Stream()`, pre-primer-byte); :1233 → ~1303 (`first.Err` rama TTFB, `excluded` :1306); :1280 → ~1357 (`first.Err` rama timeout≤0, `excluded` :1357). Refs POST-AUDIT; anchors exactos re-verificados contra `internal/router/router.go` al materializar el spec. |
| D5 | Helper `nextCyclicCandidate` | Selección cíclica pura (`router.go:885-908`) que salta candidatos excluidos escaneando desde la posición natural `attempts % len(ready)`. Con `excluded` vacío es idéntico al recorrido histórico `ready[attempts%len(ready)]` — cero regresión en el camino retryable. `found=false` → cadena agotada. |
| D6 | `break tries` en stream | El skip en stream usa labeled break del loop de tries del mismo provider: un `break` simple dentro del `select` solo saldría del select y caería en la lectura del branch timeout≤0 (doble consumo del canal). Aplica en las 3 ramas de stream (:1207, :1308, :1359). |

## Fuera de alcance

- ❌ **Circuit-breaker por cuenta** ante 401/402/403-subscription: el skip es por-request — un fallo estructural de la cuenta (sin suscripción) repite el 4xx en cada request nuevo hasta que la cuenta se repare o se quite del config. Un breaker global por cuenta es feature futura.
- ❌ **Etiqueta registry "fallback" en intentos terminales**: `emitAttempt` marca "fallback" por intento; distinguir fallback-real vs intento-terminal fallido en `registry.jsonl` queda para otro feature.
- ❌ **400 `external_directory` del payload de cliente**: 4xx del request del CLIENTE (payload malformado/traducción) es rama del proxy, no del router — fuera de este fix.
- ❌ **14 archivos gofmt pre-existentes**: deuda ajena al fix, no tocados.

## Notes

- **Incidente 21-sep-2026**: la cuenta OpenCode Go de `acct-6` quedó sin suscripción; el upstream devolvió 403 "An active OpenCode Go subscription is required to use Go models" en cada chat. El router abortaba TODA la cadena ante un error no-retryable: **161 chain errors, 0 éxitos** entre 16:10 y el fin del diagnóstico, sin probar `acct-vx`/`acct-2` (sanos). Diagnóstico en logs del servicio local (journal `mofgw.service`, PID 800) + `registry.jsonl`.
- **Precedente 401 del 17-sep-2026**: mismo patrón de abort de cadena ante no-retryable observado 4 días antes; inspiró directamente la semántica skip de este fix (descartar el PAR y continuar, terminación solo por agotamiento).
