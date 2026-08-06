# Spec — 006-002-costos: tabla de precios y costo estimado por request

---
feature_id: 006-002-costos
epic: mofgw-006-observabilidad
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 006-001-usage-accounting (contadores por client|provider|model)
paralelizable: no
---

## Contexto

006-001 ya contabiliza tokens (prompt/completion/total + cache) por
cliente|provider|modelo. El paso siguiente (decisión Pablo 06 Ago, frente 2:
"registrar y contabilizar no solo cantidad sino también costo de tokens por
proveedor y modelo") es traducir tokens a **dólares estimados**. Los precios
están verificados en `docs/research-token-efficiency.md` §1 (06 Ago, con
fuentes y fechas):

- Los 4 providers go-* pasan por **opencode.ai Zen** (reseller con sus propias
  tasas) → usar tasas Zen.
- El provider qwen pasa por **Alibaba bailian** directo → usar tasas Alibaba
  (implícito = 20% del input para cache hit).
- DeepSeek V4 Pro: discrepancia 4× entre Zen ($1.74/$3.48) y directo
  ($0.435/$0.87 promo) — la tabla debe reflejar el provider real que sirve,
  no el modelo genérico.

El costo es **estimado** (se calcula en el gateway desde los precios
configurados); la factura real la emite cada provider. El objetivo es
visibilidad de gasto por cliente, no contabilidad exacta.

## Postcondiciones

1. **P1 — Tabla de precios configurable:** el config YAML acepta un bloque
   `pricing` con precios por millón de tokens por modelo (input, output,
   cache-hit). Formato: keyed por modelo (los precios no varían por provider
   para el mismo modelo a través del mismo gateway, salvo el caso qwen/bailian
   vs Zen — ver decisión en Contexto; si un modelo aparece en la tabla, aplica
   a todos los providers que lo sirven). Sin entrada en la tabla → costo 0
   para ese modelo (no error).

2. **P2 — Cálculo por request:** cada request atendido (no-stream y stream)
   calcula `costo_usd = prompt_miss/1e6 × input_price + completion/1e6 ×
   output_price + cached/1e6 × cache_hit_price`. Los campos vienen del usage
   ya contabilizado (006-001): `miss = prompt - cached`. Un request sin usage
   → costo 0. Ausencia de precio → 0 para esa componente.

3. **P3 — Acumulación por cliente:** el costo se acumula por
   `client|provider|model` (misma key que 006-001) y se expone en /metrics
   como `mofgw_cost_usd_total` (agregado sin labels) + desagregado
   `mofgw_cost_usd{client,provider,model}` (formato Prometheus-style, mismo
   patrón que usage_tokens).

4. **P4 — Precisión y redondeo:** el costo se acumula en centavos (float64
   con 6 decimales de precisión mínima; se serializa con %f). No se usa
   money/currency lib — suma de float64 es suficiente para estimación.

5. **P5 — No bloquea el request:** el cálculo de costo es informativo — nunca
   afecta la respuesta, el fallback ni el streaming. Si falla el cálculo
   (dato raro), se registra 0 y se continúa.

## Invariantes

- I1: La tabla de precios es data (YAML), no código — se puede actualizar sin
  recompilar (nueva feature, nuevo precio, cambio de promo).
- I2: Costo 0 por defecto — un provider sin entrada en pricing no genera
  error ni bloquea.
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: Cardinalidad acotada (clientes × providers × modelos), mismo patrón que
  006-001.

## Criterios de aceptación

- C1: Config con `pricing: deepseek-v4-flash: {input: 0.14, output: 0.28,
  cache_hit: 0.028}` (tasas Zen). Request con prompt 1_000_000 miss + 1_000_000
  cached + 500_000 completion → costo = 1×0.14 + 0.5×0.28 + 1×0.028 = $0.308.
  Verificado en test con valores exactos.
- C2: Modelo sin entrada en pricing → costo 0, sin error.
- C3: /metrics expone `mofgw_cost_usd_total` y el desagregado por cliente con
  el acumulado correcto tras N requests.
- C4: request sin usage → costo 0.
- C5: Los contadores de 006-001 (prompt/completion/total) no se alteran.
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`: `Pricing map[string]PricingConfig` en
  `Config` (keyed por modelo) con `input_usd_per_m`, `output_usd_per_m`,
  `cache_hit_usd_per_m` (float64).
- `internal/metrics/metrics.go`: contador `costUSD` (map keyed
  `client|provider|model`, float64) + `IncCost(client, provider, model,
  costUsd float64)` + Render con total + desagregado. TestNamesSorted se
  ajusta (12 contadores) con justificación en spec.
- `internal/proxy/proxy.go`: `recordCacheTokens` → además de IncCacheTokens +
  IncUsage, calcular costo con la tabla (acceso vía config inyectado al
  Server) y llamar `IncCost`. El Server necesita la tabla de precios
  (`proxy.New` recibe config completa o la tabla).
- Tests: extender e2e_006001_test.go (o nuevo e2e_006002_test.go) con el caso
  C1 exacto y el C2 (sin pricing).

## Fuera de alcance

- Persistencia de costos entre restarts (en memoria, como 006-001).
- Facturación exacta (reconciliación con el bill del provider).
- Alertas por umbral de gasto — se evalúa con EPIC-008 (budget).
- Multi-divisa — solo USD (fuentes del research en USD).

## Cambios de tests justificados

- `metrics_test.go TestNamesSorted`: expectativa 11 → 12 contadores base. El
  contrato de /metrics cambió con P3 (cost_usd_total se emite siempre, en 0
  si no hubo datos). Justificado por P3.
- `e2e_test.go harness`: campo `proxySrv` agregado (exponer el proxy.Server
  para configurar features post-construcción, 006-002 SetPricing).
- `e2e_006001_test.go buildMultiClient`: setea `h.proxySrv` (soporte del
  harness para 006-002).
