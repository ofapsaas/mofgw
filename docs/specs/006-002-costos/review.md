# Review — 006-002-costos

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** revisión ejecutada INLINE por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído toda
> la sesión). Mismo agente que el implementer (deepseek-v4-flash), violando
> el invariante anti-bias. Mitigación: revisión adversarial + **validación
> externa independiente pendiente antes de PROD** (misma condición que
> 003-001/006-001 — aplicar `handoff-reviewer.md` adaptado).

## Verdict: APPROVE (0 blockers, 0 majors, 0 minors)

La feature cumple P1-P5 y C1-C6. Suite completa verde con -race.

## Findings

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Decisión |
|---|-----------|--------------|----------|-----------|----------|
| H1 | Nit (verificado OK) | P1/P2 | `s.pricing[model]` en map nil → zero value con ok=false → costo 0. Nil-safe por construcción de Go. El contrato exige SetPricing antes del tráfico; si se llamara concurrente habría race — documentado en el setter y verificado por -race en los tests (que llaman SetPricing antes de los requests). | proxy.go estimateCost + SetPricing | Sin acción — contrato documentado. |
| H2 | Nit (verificado OK) | P4 | float64 acumulado con %.6f de precisión — suficiente para estimación (spec P4 lo define explícitamente). | metrics.go Render cost | Sin acción. |
| H3 | Nit (verificado OK) | P5 | El cálculo de costo está en recordCacheTokens (post-respuesta) — nunca afecta el flujo del request. Si estimateCost fallara (imposible: solo aritmética), no hay path de error que bloquee. | proxy.go recordCacheTokens | Sin acción. |
| H4 | Nit (verificado OK) | C5 | IncCacheTokens + IncUsage intactos (006-001/003-001 no se tocan); TestRED_NoRompeCache003001 + suite completa verde. | e2e_006001_test.go | Sin acción. |

## Positives

- **Tabla de precios data-driven (I1):** ModelPricing + SetPricing — el
  pricing se puede actualizar sin recompilar (config se cablea en main).
  Los precios verificados del research (§1) se cargan como data.
- **Costo 0 por defecto (I2):** modelo sin pricing → 0, sin error; el
  upgrade de config no rompe providers sin precio conocido.
- **Cálculo transparente:** miss = prompt − cached (consistente con
  003-001), cached facturado a tarifa de cache-hit — refleja la realidad de
  los providers (cache hit es más barato).
- **Cardinalidad acotada (I4):** misma key `client|provider|model` que
  006-001 — sin dimensiones nuevas de alto cardinality.
- **Commits granulares** RED (802d7f4) / GREEN (32752e0) separados; cambio
  de TestNamesSorted justificado (12 contadores).

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN y
  post-gofmt).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 cálculo exacto (0.308) ✓, C2 sin pricing → 0 ✓, C3
  acumulación por cliente (0.00256 × 2 req) + desagregado ✓, C4 sin usage →
  0 ✓, C5 no rompe 006-001 ✓.

## Pendiente de proceso

- [ ] Validación externa independiente (anti-bias) antes de PROD — aplicar
      handoff-reviewer.md adaptado a 006-002.
- [ ] Cablear el pricing real (research §1: tasas Zen para provider-a/b/c/d,
      Alibaba para qwen) en la config de producción — feature de deploy,
      fuera de esta feature (el mecanismo está listo).
- [ ] 007-001 (model-metadata) puede reutilizar ModelPricing para el
      catálogo.
