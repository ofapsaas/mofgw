# Review — 008-002-budget-por-cliente

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** revisión ejecutada INLINE por el
> orquestador (opción 3 del contrato §4 — runtime de delegación caído toda
> la sesión). Mismo agente que el implementer (deepseek-v4-flash), violando
> el invariante anti-bias. Mitigación: revisión adversarial + **validación
> externa independiente pendiente antes de PROD**.

## Verdict: APPROVE (0 blockers, 0 majors, 0 minors — 1 bug real encontrado y corregido)

La feature cumple P1-P5 y C1-C6. Suite completa verde con -race.

## Findings

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Fix |
|---|-----------|--------------|----------|-----------|-----|
| H1 | **Bug real (Major→corregido)** | P3 (budget costo) | `SnapshotClient` sumaba el costo en `by_model` pero NUNCA en `snap.CostUSD` (totals.cost_usd quedaba en 0). Bug heredado de 007-003 (el endpoint /v1/usage mostraba totals.cost_usd 0). Detectado al fallar TestRED_BudgetCosto (el budget de costo nunca se excedía). | metrics.go SnapshotClient (loop de cost sin `snap.CostUSD += v`) | Corregido en b53bca0: `snap.CostUSD += v` en el loop. |
| H2 | Nit (verificado OK) | P5/I2 | El budget se evalúa ANTES del router (4.6) — un cliente sin budget configurado no matchea `s.budgets[clientID]` → sin 429. Aislamiento verificado (k2 sin budget → 200 tras k1 429). | proxy.go budgetExceeded | Sin acción. |
| H3 | Nit (verificado OK) | P2 | La ventana es simple (acumulado desde start) — documentado en spec I4 como aproximación. El window de config queda como documentación de intención (rotación = reinicio). | spec 008-002 I4 | Sin acción — decisión documentada. |

## Positives

- **El "diente" del sistema de medición:** con budget, el gasto por cliente
  queda acotado — 429 cuando se excede, sin tocar upstream (verificado:
  C5 gotBody vacío).
- **Doble dimensión (P3/P4):** costo USD y/o tokens — el operador elige.
  Límites opcionales (0 = sin límite para esa dimensión).
- **Aislamiento (P5):** cada cliente ve su budget; sin budget → sin límite
  (verificado C4).
- **La revisión adversarial encontró un bug real** (H1) que afectaba a
  /v1/usage desde 007-003 — valor concreto del proceso de revisión aunque
  sea inline.
- **Commits granulares** RED (31b9041) / GREEN (b53bca0) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN+fix).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 budget costo (429 en request 9) ✓, C2 budget tokens ✓, C3
  sin budget → 200 ✓, C4 aislamiento ✓, C5 429 rate_limit_exceeded ✓.

## Pendiente de proceso

- [ ] Validación externa independiente (anti-bias) antes de PROD.
- [ ] 008-003 (stats por sesión) — última feature del replanteo.
- [ ] El fix H1 también corrige /v1/usage (totals.cost_usd) — verificar en
      el deploy a PROD.
