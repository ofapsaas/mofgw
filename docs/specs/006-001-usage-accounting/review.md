# Review — 006-001-usage-accounting

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** revisión ejecutada INLINE por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído toda
> la sesión). Mismo agente que el implementer (deepseek-v4-flash), violando
> el invariante anti-bias de familia distinta. Mitigación: revisión
> adversarial + **validación externa independiente pendiente antes de PROD**
> (re-ejecutar `handoff-reviewer.md` adaptado a 006-001 cuando el runtime
> se recupere).

## Verdict: APPROVE con 1 minor (0 blockers, 0 majors)

La feature cumple P1-P6 y C1-C6. Suite completa verde con -race.

## Findings

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Decisión |
|---|-----------|--------------|----------|-----------|----------|
| H1 | Minor | naming | `recordCacheTokens` ahora acumula TAMBIÉN los tokens totales por cliente (006-001), pero conserva el nombre de 003-001. El nombre no refleja el alcance nuevo (debería ser `recordUsage` o similar). No afecta comportamiento. | proxy.go:218 | Aceptado como deuda cosmética (documentado en USER-GUIDE y comment). No justifica churn de renaming en el mismo epic. |
| H2 | Nit (verificado OK) | P1 | Los requests con error de provider absorbido (502) NO acumulan tokens — correcto: no hubo respuesta con usage, no hubo consumo. Verificado en el flujo: handleChainError retorna antes de recordCacheTokens. | proxy.go handleChat | Sin acción — comportamiento correcto. |
| H3 | Nit (verificado OK) | P2 | clientID vacío cae en label `unknown` (IncUsage guard). Cardinalidad acotada por clientes configurados. | metrics.go IncUsage | Sin acción. |
| H4 | Nit (verificado OK) | P5 | Los contadores de 003-001 (cache_hit/miss/reasoning) se mantienen intactos — TestRED_NoRompeCache003001 + suite 003-001 verde. | e2e_006001_test.go | Sin acción. |

## Positives

- **Un solo punto de acumulación:** `recordCacheTokens` (proxy) llama a
  IncCacheTokens + IncUsage juntas — el request acumula todo en un lugar,
  sin dispersión (consistente con el patrón de 003-001).
- **Cardinalidad acotada:** label `client|provider|model` — acotada por
  clientes configurados × providers × modelos; sin alto-cardinality (I4).
- **Robustez P1:** usage ausente → 0s, sin crashear (mismo patrón 003-001);
  verificado en TestRED_MetricsCacheTokensSinUsageSumaCero (0).
- **Privacidad P6:** labels usan el clientID (hasheado en disco, 001-007) —
  nunca contenido de prompt/respuesta.
- **Transparencia del cambio:** request_end agrega campos numéricos, no
  altera el contrato existente de 005-005 (status_code, duration_ms).
- **Commits granulares** RED (7abba8a) / GREEN (6db184f) separados; cambio
  de TestNamesSorted justificado en spec (11 contadores).

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN y
  post-gofmt).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: P1 no-stream (2400/160/2560 acumulados) ✓, P1 stream (500/20) ✓,
  P2 clientes separados (k1 vs k2 labels) ✓, P3 exposición ✓, P4 request_end
  ✓, P5 no rompe 003-001 ✓, P6 privacidad ✓.

## Pendiente de proceso

- [x] **Validación externa ejecutada (APPROVE (0 blockers, 0 majors))** — evidencias en `docs/specs/external-reviews/006-001-usage-accounting.resp.json` (revisado por qwen3.7-plus, familia distinta al implementer deepseek-v4-flash)
      handoff-reviewer.md a 006-001.
- [ ] 006-002 (costos) depende de estos contadores por cliente — listo para
      spec cuando se decida.

> ✅ **Actualización post-review externo:** la limitación de proceso declarada arriba
> (review inline por runtime de delegación caído) quedó RESUELTA — el runtime se
> recuperó y la validación externa independiente se ejecutó. Ver
> `docs/specs/external-reviews/006-001-usage-accounting.resp.json`.
