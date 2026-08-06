# Review — 007-002-models-endpoint

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
| H1 | Nit (verificado OK) | P4 | `created` se regenera en cada GET (time.Now().Unix()) — no es metadata persistente. Consistente con el comportamiento previo; los runtimes no dependen de created. | proxy.go modelCatalogEntry | Sin acción — comportamiento preexistente preservado. |
| H2 | Nit (verificado OK) | I2 | Los valores vienen de modelMeta + pricing (nunca hardcodeados acá) — la fuente de verdad es la config declarativa. Verificado: modelCatalogEntry solo lee de los dos maps. | proxy.go modelCatalogEntry | Sin acción. |
| H3 | Nit (verificado OK) | P1/C2 | Modelo sin metadata ni pricing → objeto mínimo idéntico al previo. El test TestE2EModelsEndpoint existente (sin setters) sigue pasando → backward compat verificado empíricamente. | e2e_test.go TestE2EModelsEndpoint + suite verde | Sin acción. |

## Positives

- **Valor real para los runtimes:** un cliente openclaw que consulte /v1/models
  ahora ve `context_length`, `max_completion_tokens`, `capabilities.reasoning`
  — corrige los defaults equivocados que el discovery detectó (research §3:
  openclaw default 128k/8k, opencode Go 4096).
- **Capacidades reales (I2):** `capabilities.reasoning` deriva de `thinking`
  (research §4 verificado) — deepseek-v4-flash → true (antes opencode veía
  false y nunca mandaba reasoning_effort). kimi ["always"] → true con levels
  honestos.
- **Pricing expuesto:** openclaw/otros pueden estimar costo por modelo desde
  el catálogo (antes cost:0).
- **Formato conservador:** campos extra no rompen el envelope mínimo — los
  clientes que ignoran campos extra siguen funcionando (C2 verificado).
- **Commits granulares** RED (e62fd4f) / GREEN (a6b37d8) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 campos metadata ✓, C2 mínimo sin metadata ✓, C3 pricing
  presente/ausente ✓, C4 kimi always ✓, C5 envelope + suite verde ✓.

## Pendiente de proceso

- [ ] Validación externa independiente (anti-bias) antes de PROD — aplicar
      handoff-reviewer.md adaptado a 007-002.
- [ ] Deploy a producción: rebuild + reinicio mofgw.service + verificación
      `curl /v1/models` con la key real (verificación empírica end-to-end
      con el runtime) — CONDICIONADO a la validación externa.
- [ ] 007-003 (usage para clientes) — siguiente feature del catálogo.
