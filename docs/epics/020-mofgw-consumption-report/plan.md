# Epic 020 — mofgw-consumption-report

**Fecha:** 2026-09-16 · **Estado:** Planificado (en cola) — se ejecuta cuando 019-003 cierre y libere el epic 019
**Aprobado por:** Ofap (agent-delegated HITL, pedido explícito de Pablo — documentar y encolar, 16 Sep 2026)

## Problema

mofgw loguea consumo a `registry.jsonl` (014-001) pero el registro no permite
responder "USD/tokens por modelo por día" sin joins frágiles, y no existe una
vista de consulta inmediata. El análisis post-mortem (Odoo o scripts) necesita
datos completos y trazables en el log.

## Evidencia de descubrimiento (verificada, 16 Sep 2026)

- Logs reales: `~/logs/mofgw-registry.jsonl` + 4 rotados (logrotate diario ~03:00 UTC).
  ~6.000 líneas/día, 257 B/línea, 6 clientes, 7 modelos, 1% errores. Procesamiento
  in-memory streaming: trivial (< 50 ms por día).
- **Gap confirmado:** `TerminalEvent` NO tiene `model` (keys reales: client,
  cost_usd, error_code, final_provider, outcome, request_id, status, stream,
  tokens, ts, type). "USD/día por modelo" hoy exige join `request_id ↔ attempt.model`.
- **Costo upstream verificado empíricamente:** OpenRouter entrega `usage.cost`
  (USD real cobrado) + `cost_details` breakdown con `"usage":{"include":true}` en
  el request; streaming lo incluye en el chunk final. Zen/Go (OpenAI-compatible
  estándar) NO reportan costo por request.
- Los precios upstream cambian: el costo debe **contabilizarse al momento del
  request** (mofgw ya lo hace con la tabla `pricing:`); la proveniencia de cada
  costo debe quedar registrada.

## Alcance

- **Dentro:** enriquecer TerminalEvent (model + costo trazable), capturar
  `usage.cost` de OpenRouter cuando exista, endpoint HTML de consulta inmediata
  por fecha sobre registry.jsonl + rotados.
- **Fuera (explícito):** rotación de logs (la hace logrotate), ingestión a
  Odoo/DB (fase 2, post-mortem al día siguiente, epic aparte), dimensión
  sesión/proyecto (telemetry.jsonl la cubre a futuro).

## Features (orden de ejecución)

| # | Feature | Resumen | Depende de |
|---|---|---|---|
| 020-001 | registry-cost-model | `model` + `cost_usd_src` + `cost_usd_up` (nullable) en TerminalEvent; captura `usage.cost` de OpenRouter; precedencia upstream > tabla > none | — |
| 020-002 | metrics-summary-html | `GET /v1/metrics/summary?date=YYYY-MM-DD` → HTML estático (tabla por modelo + cobertura de proveniencia + totales), streaming parse, tolerante a líneas corruptas | 001 |
