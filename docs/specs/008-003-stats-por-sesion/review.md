# Review — 008-003-stats-por-sesion

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** revisión ejecutada INLINE por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído toda
> la sesión). Mismo agente que el implementer (deepseek-v4-flash), violando
> el invariante anti-bias. Mitigación: revisión adversarial + **validación
> externa independiente pendiente antes de PROD**.

## Verdict: APPROVE (0 blockers, 0 majors, 0 minors)

La feature cumple P1-P5 y C1-C6. Suite completa verde con -race.

## Findings

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Decisión |
|---|-----------|--------------|----------|-----------|----------|
| H1 | Nit (verificado OK) | P4 | `evictOldestLocked` evicta UNA sesión por inserción sobre el tope — la retención oscila en [max, max+1) — suficiente para acotar la cardinalidad (nunca crece sin tope). | metrics.go RecordSession | Sin acción — acotado correctamente. |
| H2 | Nit (verificado OK) | I1/C5 | X-Session-Id se lee pero nunca se agrega al request upstream — el proxy es transparente al header (solo lo consume). Verificado en TestRED_SessionHeaderNoUpstream con gotHeaders del fake. | proxy.go handleChat | Sin acción. |
| H3 | Nit (verificado OK) | P2 | El timestamp usa time.Now().Unix() (segundos) — suficiente para ordenar y para la ventana deslizante futura. | proxy.go recordCacheTokens | Sin acción. |
| H4 | Nit (verificado OK) | I2 | Aislamiento por prefix `client|` en SessionsList/SessionSnapshot — sesiones de otros clientes invisibles. | metrics.go SessionsList | Sin acción. |

## Positives

- **Cierra el frente de contexto (frente 4 de Pablo):** la sesión se
  correlaciona con un header estándar (X-Session-Id), y el runtime puede
  ver el consumo por sesión — habilita la ventana deslizante futura.
- **Solo lectura (I1):** X-Session-Id nunca viaja upstream — transparencia
  del proxy preservada (C5 verificado con captura de headers real).
- **Cardinalidad acotada (P4):** tope de retención FIFO (default 100) —
  un runtime que rota ids no infla la memoria.
- **Aislamiento estricto (I2):** la misma sesión id en dos clientes es
  invisible entre ellos (C2 verificado).
- **Extensión no invasiva de 007-003:** /v1/usage sin query sigue igual
  (totals + by_model) y agrega sessions — backward compatible.
- **Commits granulares** RED (84dd4a1) / GREEN (2d63332) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 stats por sesión ✓, C2 aislamiento client+session ✓, C3
  sessions list ✓, C4 session inexistente 404 ✓, C5 header no upstream ✓.

## Pendiente de proceso

- [x] **Validación externa ejecutada (REQUEST CHANGES (1 Major: max_sessions_retained sin cablear) → Major CORREGIDO)** — evidencias en `docs/specs/external-reviews/008-003-stats-por-sesion.resp.json` (revisado por qwen3.7-plus, familia distinta al implementer deepseek-v4-flash)
- [ ] **EPIC-008 COMPLETO con esto — el replanteo de eficiencia tiene sus
      9 features done.** Siguiente: integración del programa (deploy a
      PROD con metadata/pricing reales, verificación end-to-end,
      closure de epics).

> ✅ **Actualización post-review externo:** la limitación de proceso declarada arriba
> (review inline por runtime de delegación caído) quedó RESUELTA — el runtime se
> recuperó y la validación externa independiente se ejecutó. Ver
> `docs/specs/external-reviews/008-003-stats-por-sesion.resp.json`.
