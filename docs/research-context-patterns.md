# Research — Patrones de contexto y sesión en el tráfico real de mofgw

> Análisis de la telemetría de descubrimiento (009-000). Documento vivo: se
> actualiza a medida que la captura (24-48h) acumula data.
> Fecha de inicio: 2026-08-09. Fuente: `/home/<user>/logs/mofgw-telemetry.jsonl`.

## Hallazgo principal (preliminar — muestra chica, ver §Metodología)

**El id de sesión vive en el HEADER, no en la metadata del body.** Con la
muestra inicial (13 eventos, ~2 min de captura), los tres runtimes del tráfico
real son:

| Runtime (User-Agent) | ¿Manda X-Session-Id? | ¿Ids en metadata del body (detected_ids)? |
|---|---|---|
| `opencode/1.18.10 ai-sdk/provider-utils` | ✅ SIEMPRE (header `X-Session-Id` + `X-Session-Affinity`) | No (0 detectados) |
| `OpenAI/JS 6.45.0` (zot u otro vía SDK JS) | ❌ NO | No (0 detectados) |
| `node` (openclaw u otro cliente Node) | ❌ NO | No (0 detectados) |

Consistente con la investigación estática previa (`ref:naughty-black-trout`,
`ref:philosophical-lime-gopher`): solo opencode serializa el session id, y va
en header, no en el body.

## Implicación para el diseño de 009-001 (composición)

El criterio de cierre del epic (spec 009-000 §Fuera de alcance, m4 del audit)
se perfila así:

- **opencode** → composición keyed por `X-Session-Id` (sesiones reales).
- **openclaw / zot** → si tras la captura completa siguen sin sesión ni
  anidada en metadata → composición keyed por `client_id` (agregado, sin
  aislamiento por sesión).

Esta es la decisión que se confirma al final de la captura. La evidencia
preliminar apunta a que **no hay session id en metadata del ctx** (la hipótesis
de Pablo no se confirma con la muestra actual), pero la muestra es insuficiente
para descartarla: solo 13 eventos y los runtimes sin sesión podrían mandar
metadata solo en ciertos request (p.ej. subagentes).

## Sesiones observadas (opencode)

- `ses_01bfb2356ffeUgpQY65U91BDyf` — sesión opencode (fork #1 / otra conversación)
- `ses_01d155e10ffe0Vc5XJUo1BaUOh` — sesión opencode (la conversación de este epic)

Ambas aparecen con `X-Session-Id` == `X-Session-Affinity` (mismo valor, como
documentó el research de opencode).

## Metodología y límites

- Captura con `telemetry.enabled: true, sample_rate: 1` (captura completa).
- Muestra inicial: 13 eventos en ~2 min (2026-08-09 01:08-01:10 ART).
- **Límite crítico:** la muestra inicial está dominada por opencode (mi propia
  sesión y una paralela). openclaw/zot tienen 2 eventos. Los agregados por
  runtime se completan con la captura de 24-48h.
- `detected_ids` = 0 en todos los eventos iniciales, pero la hipótesis
  "metadata de sesión en el ctx" solo se descarta con muestra representativa
  de openclaw/zot (heartbeats, crons, Telegram).

## Pendiente (al completar la captura)

1. Agregados por runtime: volumen, % con session id, % con detected_ids.
2. Correlación de session ids con opencode.log / openclaw.log (por valor +
   timestamp, M2 del audit).
3. Confirmar o descartar la hipótesis de metadata anidada.
4. Decisión formal: composición keyed por session (opencode) vs client_id
   (openclaw/zot) → input de la spec de 009-001.
