# 📦 Paquete de decisión — pausa de workers + Epic 019 caps (update 12 Sep 2026)

> **Para:** Pablo · **De:** Ofap (HB) · **Estado:** DRAFT listo para entrega horario hábil 13 Sep
> Reemplaza al DM 12 Sep 09:05 como base de decisión (ese pedía pausa urgente con burn estimado $16.37/día).

## Cambio material desde la propuesta original

| Dato | Valor | Fuente |
|------|-------|--------|
| Burn estimado (análisis 11 Sep) | ~$16.37/día | FINDINGS §2026-09-12 (contexto original) |
| Burn real día completo 12 Sep | **$1.52** (9 snapshots, cadencia ~30min) | `hb/.cache/mofgw-burn/daily.jsonl` |
| Run-rate anual proyectado | ~$555 (vs ~$6k de la estimación previa) | cálculo propio sobre $1.52/día |

**Causa probable de la brecha:** el análisis del 11 Sep computó una ventana que incluía caps del worker + crons nocturnos + ventanas upstream de ese día. El 12 Sep, con la cadena de providers fixeada (baseUrl, 11 Sep) y el worker mofgw en gate HITL 019-003, el burn fue un orden de magnitud menor.

## Implicancia

Si $1.52/día es el nuevo steady-state, **el costo podría NO justificar la pausa de workers**. El riesgo que motivó la propuesta (quema descontrolada durante ventanas upstream) está cubierto por: cadena de 3 eslabones vivos + guard-mofgw-quota (probe chat por cuenta, detecta 429 como señal de agotamiento) + sticky guard de worker-trigger (suprime re-fires hasta cambio real en task_plan).

## Opciones

1. **NO-BET pausa; mantener observación 3-7 días** (recomendada si la tendencia sostiene) — costo de medición: $0 (burn-daily manual por ciclo, cronización sigue gated). Criterio de re-evaluación: 3 días completos ≥ $8/día → reabrir la propuesta.
2. **Pausar workers + Epic 019 caps** — igual que la propuesta original. Sigue disponible si el burn remonta.
3. **Híbrido:** dejar workers, aplicar solo Epic 019 caps (per-agent) — reduce superficie sin frenar el trabajo.

## Nota de honestidad

- La estimación $16.37/día y el $1.52/día miden ventanas con condiciones distintas (11 Sep pre-fix + incidente; 12 Sep post-fix con worker gated). La comparación directa sesga a favor de "no pausar". Los días completos 13-15 Sep (sin gate en el worker si Pablo firma 019-003) darán el dato limpio bajo condiciones normales.
- La cronización de burn-daily sigue gated (regla "crons externos no tocar"); la captura es manual por ciclo HB.

## Anexos

- Reporte semanal: `projects/mofgw/docs/burn-weekly-20260912.md`
- FINDINGS: §2026-09-12 Bet D (línea 742)
- Worker mofgw blocked: gate HITL 019-003, paquete de sign-off en BLO_OFAP (msg 38976) — **independiente de esta decisión**, requiere sign-off S1/S2 para reanudar el worker.
