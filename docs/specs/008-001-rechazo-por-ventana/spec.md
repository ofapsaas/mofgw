# Spec — 008-001-rechazo-por-ventana: rechazo temprano por exceso de contexto

---
feature_id: 008-001-rechazo-por-ventana
epic: mofgw-008-contexto
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 007-001 (ModelMetadata context_window), 001-003 (routing)
paralelizable: no
---

## Contexto

Hoy un request cuyo prompt excede la ventana de contexto del modelo se va
upstream y el provider lo rechaza (o trunca) — quemando intentos de la
cadena de fallback (cada provider de la cadena que sirve el modelo puede
rechazar igual, hasta agotar `max_retries`). La metadata de 007-001 ya
expone `context_window` por modelo. Esta feature usa esa metadata para
**rechazar temprano** (400) antes de gastar intentos.

Detalle importante (research §4): las ventanas son del modelo, y los
providers tienen máximos de output distintos (max_output). El rechazo por
ventana debe considerar el input (messages del request), no el output.

Nota de precisión: el conteo de tokens del prompt en el gateway es una
**estimación** (los tokens reales los cuenta el tokenizer del provider).
El rechazo temprano usa un límite con margen (configurable) para no
rechazar requests que en realidad caben — el objetivo es evitar intentos
inútiles en casos CLAROS (prompt gigantes), no ser un contador exacto.

## Postcondiciones

1. **P1 — Rechazo temprano:** un request no-stream o stream cuyo prompt
   estimado excede `context_window` del modelo destino (con margen
   configurable) se rechaza con 400 `invalid_request_error` y mensaje claro
   ("estimated prompt tokens N exceed context window M for model X"), SIN
   intentar ningún provider. La metadata del modelo debe existir (007-001);
   sin metadata → sin rechazo (backward compatible).

2. **P2 — Estimación del prompt:** el tamaño del prompt se estima del body
   crudo del request (messages serializados). La estimación es rough:
   `len(body)/4` (promedio ~4 chars/token para texto) — documentado como
   estimación. El margen de seguridad (P3) cubre la imprecisión.

3. **P3 — Margen configurable:** `context_margin` (fracción 0-1, default
   0.1) en config: el rechazo ocurre cuando estimado > context_window ×
   (1 + margin). Con margin 0.1, un modelo de 1M rechaza prompts > 1.1M
   estimados (≈ 4.4M chars). Configurable por modelo (override en
   model_metadata) o global.

4. **P4 — Respeto al provider:** el rechazo temprano NO aplica si el
   provider destino puede manejar el request (no hay forma de saberlo sin
   preguntarle) — la decisión es del gateway con la metadata. El provider
   sigue siendo la autoridad final (si el gateway no rechaza, el provider
   decide como hoy).

5. **P5 — No rompe el flujo:** requests que caben (estimado ≤ límite) se
   procesan exactamente como hoy (sin cambio de comportamiento). Sin
   metadata → sin rechazo. El error 400 es OpenAI-compatible (mismo formato
   que los otros errores del proxy).

## Invariantes

- I1: El rechazo es un ahorro de intentos — nunca debe rechazar un request
  que el provider aceptaría en el caso normal (el margen lo cubre).
- I2: Sin metadata (007-001) → comportamiento idéntico al actual.
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: El conteo es estimado y documentado como tal — no se presenta como
  exacto.

## Criterios de aceptación

- C1: Modelo con context_window 1000, margin 0 → request con body de
  ~5000 chars (estimado ~1250 tokens) → 400 con mensaje de exceso, cero
  intentos a providers.
- C2: Mismo modelo, request con body de ~2000 chars (estimado ~500 tokens)
  → 200, procesado normal (un intento al provider).
- C3: Sin metadata para el modelo → request grande → 200 (sin rechazo,
  backward compatible).
- C4: Con margin 0.5 y context_window 1000 → request estimado 1200 tokens
  → 200 (1200 ≤ 1500).
- C5: El error 400 es OpenAI-compatible (formato `error.message`/`error.type`
  = invalid_request_error).
- C6: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación, no vinculante)

- `internal/config/config.go`: `context_margin` global en Config (default
  0.1) + override opcional en ModelMetadata.
- `internal/proxy/proxy.go handleChat`: tras ParseChatRequest y antes del
  router, estimar `len(body)/4` y comparar contra la metadata del modelo
  destino (con margen). Si excede → 400 OpenAI-compatible, sin tocar el
  router. El conteo usa el body crudo (que ya está leído).
- `internal/metrics/metrics.go`: contador de rechazos por contexto
  (`mofgw_context_rejected_total` — nuevo, o reutilizar IncRejected con
  reason "context_window").
- Tests: e2e_008001_test.go (C1-C5).

## Fuera de alcance

- Tokenizer real (contar tokens exactos) — estimación rough por diseño
  (el margen cubre).
- Rechazo por output (max_output) — el output lo decide el provider.
- Budget por cliente (tokens/costo acumulado) — feature 008-002.
- Compresión/truncamiento del prompt — vive en los runtimes.
