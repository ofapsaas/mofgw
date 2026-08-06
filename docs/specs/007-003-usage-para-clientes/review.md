# Review — 007-003-usage-para-clientes

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
| H1 | Nit (verificado OK) | P3/I2 | Aislamiento del snapshot por prefix `client|`: un client "a" no matchea "ab|" porque el delimiter `|` separa — no hay colisión de prefijos. clientID sale del token autenticado (001-007), nunca del body. | metrics.go SnapshotClient | Sin acción — aislamiento correcto. |
| H2 | Nit (verificado OK) | P4 | Headers de stream seteados antes del primer byte con usage nil → 0. El usage real del chunk final va en el body SSE (003-001 ya lo pasa). El contrato P4 exige exactamente esto (los headers no se pueden cambiar post-flush). | proxy.go handleStream | Sin acción. |
| H3 | Nit (verificado OK) | P1 | Formato del costo en header: %.6f (0.001280). El test normaliza con TrimRight — la implementación tiene más precisión que el assert, correcto. | proxy.go setUsageHeaders | Sin acción. |
| H4 | Nit (verificado OK) | P5/I4 | /v1/usage solo expone números agregados (nunca contenido). 401 sin auth (el wrap de auth cubre /v1/* — verificado en TestRED_UsageEndpoint401). | proxy.go handleUsage | Sin acción. |

## Positives

- **Visibilidad real para el runtime (frente 3 de Pablo):** cada respuesta
  trae el consumo del request en headers (sin scrapear /metrics), y el
  cliente puede consultar su acumulado en /v1/usage.
- **Aislamiento por cliente (I2):** cada token ve solo sus agregados —
  multi-cliente sin fuga entre tenants. Verificado con 2 clientes distintos.
- **Streaming respetado (P4):** headers antes del primer byte, usage real en
  el body SSE — no rompe 003-001 (verificado: chunk final con cached_tokens).
- **Consistencia de fuentes (P2):** headers y /v1/usage usan los mismos
  contadores que /metrics (IncUsage/IncCost) — el cliente ve lo mismo que
  acumula el gateway.
- **Privacidad (I4):** solo números — consistente con la regla dura de
  005-005.
- **Commits granulares** RED (0f7dfc8) / GREEN (2ce9a2a) separados.

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras GREEN).
- `go vet ./...`: limpio. `gofmt -l internal/`: limpio.
- Cobertura: C1 headers no-stream ✓, C2 sin usage → 0 ✓, C3 /v1/usage +
  aislamiento ✓, C4 401 sin auth ✓, C5 stream headers + body intacto ✓.

## Pendiente de proceso

- [ ] Validación externa independiente (anti-bias) antes de PROD.
- [ ] Deploy a producción (rebuild + reinicio + verificación end-to-end)
      — condicionado a la validación externa.
- [ ] EPIC-007 (catálogo) COMPLETO con esto. Siguiente: EPIC-008-contexto
      (rechazo por ventana, budget por cliente, stats por sesión).
