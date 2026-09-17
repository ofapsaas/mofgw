---
id: 020-001-registry-cost-model
epic: 020-mofgw-consumption-report
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode)
approved_at: 2026-09-17
---

# Feature 020-001 — registry-cost-model

> **DRAFT — Pendiente de aprobación del usuario (Pablo/Ofap HITL — indelegable).**
> Draft materializado por el orquestador desde el análisis validado del 16 Sep 2026
> (ver `docs/epics/020-mofgw-consumption-report/plan.md` § Evidencia).

## Descripción

Enriquecer el registro unificado de accounting (014-001) para que cada evento
terminal lleve la dimensión `model` y el costo contabilizado al momento del
request con su **proveniencia** explícita. Cuando el upstream reporta el costo
real facturado (OpenRouter `usage.cost`, verificado empíricamente), ese valor es
la fuente primaria; si no, se usa la tabla `pricing:` de config; si tampoco hay
precio cargado, se marca explícitamente como `none` (dato faltante, no costo cero).

## Contrato (postcondiciones numeradas)

- **P1.** `TerminalEvent` incluye `model` (string): el `model` solicitado por el
  cliente, poblado en TODO evento terminal (success y error) emitido por el proxy.
- **P2.** `TerminalEvent` incluye `cost_usd_src` (string): `"upstream"` si
  `cost_usd` proviene del costo reportado por el upstream; `"table"` si proviene
  del cálculo con la tabla `pricing:`; `"none"` si no hay costo upstream ni precio
  cargado para el modelo. Intentos (AttemptEvent) no cambian de esquema.
- **P3.** `TerminalEvent` incluye `cost_usd_up` (`*float64`, JSON): valor cuando el
  upstream reporta costo (incluyendo `0.0` legítimo de modelos free — nunca
  conflate con "sin dato"); `null` cuando el upstream no reporta costo o el modelo
  upstream no es OpenRouter-shaped.
- **P4.** Si el upstream reporta `usage.cost`: `cost_usd = usage.cost` (exacto, sin
  re-cálculo) y `cost_usd_src = "upstream"`. La fórmula de la tabla NO participa.
- **P5.** Si el upstream NO reporta costo: `cost_usd` = resultado de `estimateCost`
  con la tabla `pricing:` (fórmula vigente: miss/1M×input + completion/1M×output +
  hit/1M×cache_hit); `cost_usd_src = "table"` si el modelo tiene precio cargado;
  `cost_usd = 0` y `cost_usd_src = "none"` si el modelo NO está en la tabla.
- **P6.** Compatibilidad de lectura: una línea JSONL histórica (sin los campos
  nuevos) parsea con `model=""`, `cost_usd_src=""`, `cost_usd_up=null`. El writer
  existente no trunca ni reescribe líneas previas.
- **P7.** Privacidad: los campos nuevos son strings/números de metadata; ningún
  contenido de prompt/response ni header sensable entra al registro (I2 de 014-001 intacto).
- **P8.** La captura de `usage.cost` no altera el contrato de respuesta al cliente:
  headers X-Usage-* y el envelope upstream quedan idénticos (cero cambio observable
  fuera del registro).

## Invariantes

- **I1.** Append-only: el registro no reescribe líneas; los campos nuevos solo se
  agregan a eventos NUEVOS.
- **I2.** `cost_usd` siempre es numérico (agregable); `null` vive solo en
  `cost_usd_up`. Nunca se suma `cost_usd_up` como fuente de agregados.
- **I3.** Best-effort de emisión: un error al parsear/capturar el costo upstream
  jamás falla el request (degrada a `table`/`none`, nunca altera la respuesta).
- **I4.** Cero dependencia de DB/externo en el write path (mofgw solo loguea).

## Criterios de aceptación

- **C1.** `go test ./... -race` verde con tests que verifiquen P1-P8 (RED → GREEN).
- **C2.** E2E: un request a un upstream fake con `usage.cost` en la respuesta
  produce terminal con `src="upstream"` y cost exacto; upstream fake sin costo +
  modelo con precio → `src="table"`; modelo sin precio → `src="none"`.
- **C3.** Suite existente sin regresión (836/33 -race baseline vigente en HEAD).

## Fuera de alcance

Rotación (logrotate), ingestión Odoo, endpoint HTML (020-002), dimensiones de
sesión/proyecto, actualización de la tabla pricing (epic 019).

---

Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — gate 2→3 cerrado. Nota HITL: verificar en AUDIT que `estimateCost` existe en el código (P5 la referencia como "fórmula vigente"); si no existe, el test-writer lo reporta como pregunta antes del RED.
