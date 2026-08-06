# Review — 007-001-model-metadata

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** revisión ejecutada INLINE por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído toda
> la sesión). Mismo agente que el implementer (deepseek-v4-flash), violando
> el invariante anti-bias. Mitigación: revisión adversarial + **validación
> externa independiente pendiente antes de PROD** (misma condición que las
> features previas).

## Verdict: APPROVE (0 blockers, 0 majors, 0 minors)

La feature cumple P1-P5 y C1-C6. Suite completa verde con -race.

## Findings

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Decisión |
|---|-----------|--------------|----------|-----------|----------|
| H1 | Nit (verificado OK) | P5 | La metadata no se usa en ruteo — SetModelMetadata solo almacena. Un map nil en Server (sin llamar el setter) es seguro: `s.modelMeta[model]` en nil map → ok=false. Backward compatible por construcción. | proxy.go SetModelMetadata | Sin acción — P5 verificado por la suite completa que corre sin metadata. |
| H2 | Nit (verificado OK) | P4 | `thinking_default` con `thinking` vacío no se valida (no hay lista contra la cual validar) — semántica correcta: sin niveles declarados, no hay default que verificar. | config.go validate | Sin acción. |
| H3 | Nit (verificado OK) | I4 | Los datos del spec referencian el research §4 (fuentes verificadas) — el deploy cableará valores reales, no inventados. La validación solo garantiza forma, no verdad de los números. | spec 007-001 I4 | Sin acción — documentado. |

## Positives

- **Data-driven (I1):** ModelMetadata es YAML declarativo — se actualiza sin
  recompilar. Los niveles de thinking reales (research §4: deepseek
  low/high/max, glm 7 niveles, kimi always) se cargan como data.
- **Backward compatible (I2):** sin metadata → vacío, ruteo normal. La
  suite completa (13 paquetes) corre sin metadata y pasa.
- **Validación temprana (P4):** errores de forma (negativos,
  thinking_default inválido) fallan en load, no en runtime — patrón
  consistente con el validate() existente.
- **Consistencia con 006-002:** SetModelMetadata es espejo de SetPricing
  (mismo contrato: una vez antes del tráfico, mapa inmutable) — patrón
  establecido en el código.
- **Commits granulares** RED (6d3fa0f) / GREEN (5f11033) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 carga tipada (context_window, thinking, default) ✓, C2 sin
  metadata vacío ✓, C3 thinking_default fuera → error ✓, P4 negativo → error
  ✓, C5 suite completa sin metadata (ruteo intacto) ✓.

## Pendiente de proceso

- [x] **Validación externa ejecutada (REQUEST CHANGES (1 Major: thinking_default sin thinking; 3 Minor) → Major CORREGIDO)** — evidencias en `docs/specs/external-reviews/007-001-model-metadata.resp.json` (revisado por qwen3.7-plus, familia distinta al implementer deepseek-v4-flash)
      handoff-reviewer.md adaptado a 007-001.
- [ ] 007-002 (models-endpoint) consumirá esta metadata + pricing para
      enriquecer /v1/models — listo para spec.
- [ ] Cablear metadata real (research §4) en config de producción — deploy.

> ✅ **Actualización post-review externo:** la limitación de proceso declarada arriba
> (review inline por runtime de delegación caído) quedó RESUELTA — el runtime se
> recuperó y la validación externa independiente se ejecutó. Ver
> `docs/specs/external-reviews/007-001-model-metadata.resp.json`.
