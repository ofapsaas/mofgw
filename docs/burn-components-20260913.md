# Burn por componente — reporte Bet G (2026-09-13)

> **Fuente:** `hb/.cache/mofgw-burn/daily.jsonl` (deltas timestamped de `burn-daily.py`)
> + headers de ciclo (`hb/YYMMDD.md`). Herramienta: `scripts/burn-by-component.py`
> (commit 228f4b8). Read-only.

## Ventana analizada (real)

`2026-09-12T18:00 → 2026-09-13T12:00` (~18h) — primer día completo de
daily.jsonl. Ventanas: 16 · Total: **$4.80**

| Componente | USD | % |
|---|---|---|
| workers_opencode | $2.57 | 53.6% |
| heartbeat | $1.21 | 25.1% |
| crons | $0.39 | 8.2% |
| openclaw_otros | $0.36 | 7.4% |
| cliente_b | $0.25 | 5.2% |
| cliente_cliente-b-opencode | $0.02 | 0.4% |
| cliente_zot | $0.01 | 0.1% |

## Proyección semanal (extrapolación lineal — estimación mía, no medida)

Asumiendo perfil constante (workers rondas regulares + heartbeat a 60m):

| Componente | $/día | $/semana |
|---|---|---|
| workers_opencode | ~$3.4 | ~$24 |
| heartbeat (60m) | ~$1.6 | ~$11 |
| crons | ~$0.5 | ~$4 |
| openclaw_otros | ~$0.5 | ~$3 |
| clientes (cliente-b+zot) | ~$0.4 | ~$2.7 |
| **Total** | **~$6.4** | **~$45** |

⚠️ La ventana de 18h puede subestimar el día completo: el burn total de mofgw
medido por el análisis del 11 Sep fue ~$16.37/día, y el 12 Sep con día completo
dio $1.52/día mofgw-local + tráfico de clientes aparte. La discordancia
(FINDINGS §2026-09-12 Bet D) sigue sin resolver del todo: las fuentes
(`daily.jsonl` vs accounting mofgw) no cubren la misma superficie de clientes.

## Lecturas data-driven para la decisión

1. **workers_opencode domina (53.6%).** Cualquier recorte que NO toque rondas de
   workers deja >50% del burn intocado. La palanca de mayor impacto sigue siendo
   el paquete "pausa workers + Epic 019 caps" (gated Pablo, DM 12 Sep).
2. **Heartbeat a 60m cuesta ~$1.6/día proyectado.** Reducir cadencia más (ej. 2h)
   ahorraría ~$0.8/día — marginal frente a workers, y degrada el rescue path
   (F5/F6 dependen de heartbeats: incidente 26 Ago). **No recomendado** tocarlo
   sin datos de ≥1 semana completa.
3. **crons es barato ($0.39/18h)** pese a cadencias de 9:00-12:00 — los contratos
   de completitud + PASO 0 idempotencia (fix 11 Ago) ya contuvieron el costo.
   Sin acción.
4. **openclaw_otros ($0.36/18h)** = background poll/guardias/spills de ventana.
   Ruido estructural; monitorizar, no actuar.

## Recomendación

- **BET prioridad 1:** resolver el gate de workers (pausa + caps) — único recorte
  con impacto real.
- **NO tocar** cadencia de heartbeat ni crons hasta tener ≥7 días de
  daily.jsonl (próxima medición al cierre S43, 20 Sep).
- Investigar la discordancia de superficies de clientes (fuentes no comparables)
  como item de limpieza de datos en la próxima revisión semanal.

## Día completo 13 Sep (cierre 00:01, ventana 12 Sep 18:00 → 14 Sep 00:01)

17 ventanas · Total: **$10.38** (30h acumuladas; incluye la ventana parcial previa de $4.80)

| Componente | USD | % |
|---|---|---|
| workers_opencode | $6.95 | 66.9% |
| heartbeat | $1.21 | 11.6% |
| openclaw_otros | $1.09 | 10.5% |
| cliente_b | $0.53 | 5.1% |
| crons | $0.39 | 3.8% |
| cliente_cliente-b-opencode | $0.21 | 2.0% |
| cliente_zot | $0.01 | 0.1% |

**Delta 12 Sep 18:00 → 14 Sep 00:01 por cliente:** cliente-a-opencode $4.37 + $0.47 previo, cliente-a-openclaw $1.19, cliente-b $0.47, zot $0.0055.

### Hallazgo nuevo (00:01, HB)

- **Gap de captura 12:00→00:00:** los deltas de burn-daily solo se capturan cuando un ciclo HB los corre (no hay timer/cron dedicado). Con cadencia 60m los ciclos corrieron pero solo 2 ciclos capturaron delta hoy (12:00 y este). **Gap de datos estructural para Bet G** — candidato: cron liviano de captura (0 tokens, solo `burn-daily.py` cada 2h). Gated: crons nuevos = decisión Pablo (patrón HEARTBEAT "crons externos no tocar" aplica a los existentes; uno nuevo es config).
- **Worker mofgw re-check gastando en gate HITL:** handoff.json round 3/5 actualizado 23:48 — re-check del gate 4→5 de 019-003 (sigue bloqueado en sign-off Pablo). La ventana 12:00→00:01 concentró $4.37 de cliente-a-opencode (~66% del total del día). Bounded: máx 5 rondas del ciclo actual, luego se agota. Data point directo para la Bet D (pausa workers): el worker quemó ~$4 en 12h re-verificando un gate que no puede moverse sin Pablo.
