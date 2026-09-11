# Review — 019-001-fetch-modelsdev

**Fecha:** 2026-09-11 · **Reviewer:** cdad-reviewer (premier-red-meerkat) · **Materializado por:** orquestador
**Veredicto original:** REQUEST_CHANGES — 2 bloqueantes, 5 opcionales, 4 abstenciones.

> **⚠️ A2 — Degradación anti-bias (registrar):** el arnés corrió al reviewer con
> `mofgw/deepseek-v4-flash` — **misma familia que el implementer**. La garantía de
> independencia estructural no se cumplió en este run. Capa 2 (HITL delegado)
> validó los bloqueantes por inspección directa del código antes de decidir
> (retryable en fetch.go: `case "timeout","network": return true` confirmado).
> *Mejora candidata:* routing de modelos por perfil de agente no confiable — el
> instalador debe garantizar familia distinta para cdad-reviewer.

## Bloqueantes (decisión HITL delegada — Ofap)

### B1 — P4: retry de timeout por presupuesto de intento (fetch.go:212) → **RESUELVER A FAVOR DE P4**

P4 manda: un intento colgado ya consumió su presupuesto; reintentarlo duplicaría la
latencia (2×10s). **Decisión:** `timeout` NUNCA es retryable. D9 se reinterpreta:
solo errores de transporte `network` (conn refused/reset/5xx) son transitorios.
Movimiento: (1) test-writer agrega el test discriminante del escenario P4 real
(upstream bloquea, ctx llamador vivo, assert hits==1) — el test actual no discrimina;
(2) implementer saca `"timeout"` de `retryable`.

### B2 — I6: par cache+sidecar no atómico (store.go:146-151) → **ADOPTAR sidecar-first**

**Decisión:** sidecar temp+rename PRIMERO, cache temp+rename ÚLTIMO (el cache es el
punto de commit). Fallo del sidecar → cache intacto + `(false, err)` (I6 consistente).
Fallo del cache tras sidecar OK → sidecar adelantado; el próximo refresh detecta
digest ≠ sidecar y reescribe ambos (auto-cura); interim: `Get()` sirve el cache viejo
(aceptable). Rationale documentado; el spec D6/I10 impone 2 archivos y la atomicidad
cross-file real quedaría fuera de alcance.

## Opcionales (aplicar/descartar con motivo)

- **#3 (aplicar, test-writer):** oráculo mtime en `TestRefresh_SkipsIdenticalBody`
  (byte-igualdad no prueba "no reescribe").
- **#4 (aplicar, implementer, sin test):** fallback de `DefaultCachePath` a
  `os.TempDir()` en vez de CWD cuando `os.UserCacheDir()` falla; el test del override
  `MOFGW_CACHE_DIR` queda como deuda documentada (simular HOME roto es frágil).
- **#5 (aplicar, implementer):** `classifyTransport` distingue `DeadlineExceeded`
  (timeout real) de `Canceled` (cancelación del llamador) para precisión semántica
  hacia 019-004.
- **#6 (aplicar, test-writer):** subtest env `""` en `TestFetchDisabled_Knob`.
- **#7 (desestimado con motivo):** `io.LimitReader` 64MiB — cota defensiva; payload
  real 4.59MB; clasificación del error como `parse` documentada en código.

## Abstenciones

A1 (P4-vs-D9 → resuelta arriba), A2 (anti-bias → ver nota), A3 (sin importlinter en
el repo — I1 verificado manualmente; deuda de tooling), A4 (bash denegado al reviewer;
evidencia de suite 761/30 -race aceptada del orquestador).

## Mapeo test ↔ postcondición

P1-P17: 16/17 PASS. **P4 FAIL parcial** (B1). Test sobrante: 0. Test faltante: 1
(escenario P4 con ctx llamador vivo — se agrega en el loop de fixes).

## Estado

Status: **Reviewed** — REQUEST_CHANGES; bloqueantes B1/B2 resueltos (c0489bc test discriminante + 65503d4 fix), suite 763/30 -race verde. Gate 4->5 cerrado el 2026-09-11.
