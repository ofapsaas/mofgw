# TECHDEBT.md — Deuda técnica y funcional del programa mofgw

> Registro consolidado de deuda técnica/funcional. Cada entrada tiene:
> estado, prioridad, origen (spec/review/feature), y descripción.
> Convención: consolidar desde los `review.md` de cada feature y las
> decisiones operativas. Creado: 2026-08-06.

## Cómo se usa

- Las features CDAD registran su "Pendiente de proceso" en su `review.md`
  (fuente primaria). Este archivo es el **índice consolidado** para mirar
  todo de un vistazo.
- Al resolver una entrada: marcarla `[x]` y añadir la fecha + cómo.
- Las entradas "decisión operativa" (config de prod, budget) requieren
  juicio de Pablo — no se auto-resuelven.

## Índice

| # | Deuda | Estado | Prioridad | Origen | Descripción |
|---|-------|--------|-----------|--------|-------------|
| 1 | Validación externa anti-bias de reviews | ✅ CERRADA 06 Ago | — | 003-001→008-003 (todos los review.md) | El runtime de delegación se recuperó y la validación externa se ejecutó (sesión paralela, evidencias en `docs/specs/external-reviews/*.resp.json`): **7 APPROVE + 2 REQUEST CHANGES** (007-001 Major: thinking_default sin thinking; 008-003 Major: max_sessions_retained sin cablear). Ambos Majors corregidos (commit 082d10c). SEC-001 adicionalmente revisado con qwen3.7-plus (familia distinta). |
| 2 | Budget para cliente zot/OpenClaw | ⏳ PENDIENTE (decisión Pablo) | MEDIA | 008-002 + config prod | zot consume ~16.8M tokens/26min (~USD verificable en /metrics). Budget comentado en config (cost_usd_max/tokens_max) — activar cuando se decida el límite. |
| 3 | Pricing/metadata reales en config prod | ✅ HECHA 06 Ago | — | 006-002/007-001 + deploy | Se cableó pricing (tasas Zen) + model_metadata + context.margin 0.1 en `~/.config/mofgw/config.yaml`. Verificado: cost_usd_total vivo. |
| 4 | deepseek-v4-flash-0731 sin pricing | ⏳ PENDIENTE (observar) | BAJA | config prod | Modelo solo en bailian directo; sin precio verificado en research → costo 0. Revisar si aparece en tráfico real. |
| 5 | Clave acct2 con 401 upstream | ⏳ PENDIENTE (validar/rotar) | MEDIA | state file previo + deploy | Hallazgo pre-existente (no del replanteo): la key del provider acct2 devuelve 401. No bloquea (fallback cubre) pero hay que validarla. |
| 6 | X-Session-Id: ventana deslizante para budget | ⏳ FUTURO | BAJA | 008-002 spec I4 + 008-003 | El registro por sesión con timestamps habilita ventana deslizante exacta; el budget usa ventana simple (desde arranque). Aplicar si el operador lo pide. |
| 7 | Auth en /metrics y /healthz (Tailscale) | ⏳ ACEPTADA (decisión Pablo) | BAJA | deploy VPN | Con bind 0.0.0.0, healthz/metrics (sin auth por diseño) visibles para nodos del tailnet. Hoy solo dispositivos propios — aceptado. Si entran nodos ajenos → bind por IP Tailscale o auth. |
| 8 | Naming `recordCacheTokens` | ⏳ COSMÉTICA | BAJA | 006-001 review H1 | El nombre ya no refleja el alcance (acumula cache + usage + costo + sesión). Renombrar a `recordUsage` en un refactor. |
| 9 | Normalización DeepSeek directo (`prompt_cache_hit_tokens`) | ⏳ FUTURO | BAJA | 003-001 spec | Hoy vía Zen ya normaliza. Si algún día se apunta a DeepSeek directo, normalizar campos no estándar. |
| 10 | Cache explícito Alibaba (cache_control) | ⏳ FUTURO | BAJA | 003-001 spec | Implícito ya cubre (hit rate 94-99% medido). Evaluar explícito si el hit rate baja. |
| 11 | Budget global (suma de clientes) | ⏳ FUTURO | BAJA | 008-002 spec | Budget por cliente hoy; global no implementado. |
| 12 | Alertas/notificación al exceder budget | ⏳ FUTURO | BAJA | 008-002 spec | Solo rechazo 429 hoy; sin alerta. |
| 13 | Persistencia de accounting entre restarts | ⏳ FUTURO | MEDIA | 006-001/006-002/008-003 | Todo en memoria — un restart pierde el histórico de sesiones/costos. Evaluar persistencia si se necesita reporting histórico. |
| 14 | Tokenizer real vs estimación len/4 | ⏳ ACEPTADA | BAJA | 008-001 spec | Rechazo por ventana usa estimación rough con margen 0.1. Adecuado para el caso de uso (prompts gigantes). |
| 15 | Auth sin rate limiting/anti-brute-force | ⏳ ACEPTADA | BAJA | SEC-001 P5 | No hay backoff/bloqueo tras intentos fallidos con Bearer inválido. Aceptada: proxy interno, firewalld filtra internet (verificado), tailnet confiado, hash 256-bit hace la fuerza bruta impráctica. Si se expone fuera del tailnet → re-evaluar. |

## Deuda de proceso del replanteo (no técnica)

- **Runtime de delegación caído** (delegate y task, "Failed to create
  delegation session", toda la sesión del 06 Ago): obligó a ejecutar todos
  los roles CDAD inline (opción 3 del contrato §4). La limitación anti-bias
  está declarada en cada review.md. Al recuperarse el runtime, re-ejecutar
  los reviews de forma independiente (ver #1).
- **Bug de memento-query** (MEMORY.md indexado sin chunking → todo score
  1.0): reportado para registro en el proyecto memento (decisión Pablo:
  lo reporta él en el proyecto correspondiente).

## Cambios

- 2026-08-06 15:50: #1 completada de facto — el resp.json de 006-002 estaba truncado (finish=None, sin veredicto); hallazgo NaN/Inf de precios verificado REAL (yaml `.inf`/`.nan` pasaban `< 0`) y corregido: rechazo de precios no-finitos en config.go + test (commit de este ciclo). Review.md de las 9 features actualizados con estado de validación externa.
- 2026-08-06: creado con la consolidación del replanteo + deuda operativa.
