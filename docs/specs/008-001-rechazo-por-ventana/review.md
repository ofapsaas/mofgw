# Review — 008-001-rechazo-por-ventana

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
| H1 | Nit (verificado OK) | P2/I4 | Estimación `len(body)/4` es rough (incluye JSON overhead: keys, model, etc.) — sobre-estima el prompt real. El margen 0.1 default y la dirección del error (rechazar solo casos claros) cubren la imprecisión. Documentado en spec. | proxy.go estimatePromptTokens | Sin acción — estimación deliberada con margen. |
| H2 | Nit (verificado OK) | C5 | El mensaje 400 expone context_window del modelo — no es información sensible (los modelos son públicos). Formato OpenAI-compatible correcto. | proxy.go handleChat | Sin acción. |
| H3 | Nit (verificado OK) | P4 | Sin metadata → `exceedsContextWindow` false (map lookup ok=false) → sin rechazo. Backward compat verificado en TestRED_SinMetadataSinRechazo. | proxy.go exceedsContextWindow | Sin acción. |
| H4 | Nit (verificado OK) | P5 | El chequeo está DESPUÉS del parseo/auth (body ya validado) y ANTES del router — no altera el flujo normal (TestRED_RequestQueCabe: 200 + upstream). | proxy.go handleChat | Sin acción. |

## Positives

- **Ahorro real de intentos:** un prompt que excede la ventana se rechaza
  con 1 request (sin gastar max_retries × providers de la cadena). Verificado
  en TestRED_RechazoPorVentana: gotBody del upstream vacío = cero intentos.
- **Margen configurable (P3):** default 0.1, override vía SetContextMargin —
  el operador decide la agresividad del rechazo.
- **Backward compatible (I2):** sin metadata → comportamiento idéntico
  (TestRED_SinMetadataSinRechazo).
- **Estimación honesta (I4):** documentada como estimación, con margen de
  seguridad — no se presenta como conteo exacto.
- **Observabilidad:** IncRejected con reason "context_window" (cardinalidad
  acotada, patrón existente) + log debug.
- **Commits granulares** RED (beb7cc0) / GREEN (d050715) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 400 sin intentos ✓, C2 cabe → 200 + upstream ✓, C3 sin
  metadata → 200 ✓, C4 margen 0.5 ✓, C5 error OpenAI-compatible ✓.

## Pendiente de proceso

- [x] **Validación externa ejecutada (APPROVE (0 bloqueantes, 0 mayores, 5 menores, 2 nits))** — evidencias en `docs/specs/external-reviews/008-001-rechazo-por-ventana.resp.json` (revisado por qwen3.7-plus, familia distinta al implementer deepseek-v4-flash)
- [ ] 008-002 (budget por cliente) — siguiente feature de EPIC-008.
- [ ] Documentar el default de margin (0.1) en config.example.yaml y
      USER-GUIDE.

> ✅ **Actualización post-review externo:** la limitación de proceso declarada arriba
> (review inline por runtime de delegación caído) quedó RESUELTA — el runtime se
> recuperó y la validación externa independiente se ejecutó. Ver
> `docs/specs/external-reviews/008-001-rechazo-por-ventana.resp.json`.
