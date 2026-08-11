# Review — 010-002-request-path-fiel

Status: APPROVE WITH COMMENTS — 0 bloqueantes; Major #1 (config.example.yaml qwen3.7-plus sin `thinking`) RESUELTO 2026-08-11; Minor #2 (thinkingProfile fragilidad) registrado en TECHDEBT #25; Minor #3 (thinking_path sin documentar) RESUELTO 2026-08-11. Reviewer: qwen3.7-plus (familia distinta al implementer deepseek-v4-flash).

---

# Review — 010-002-request-path-fiel

**Reviewer model:** mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash — anti-confirmation-bias estructural)

## Veredicto: APPROVE WITH COMMENTS

La implementación satisface todas las postcondiciones P1-P9 y los invariantes I1-I8 del spec. Cero issues de correctness, security ni performance que bloqueen el merge.

## Bloqueantes

**Ninguno.**

## Major

### 1. config.example.yaml: qwen3.7-plus tenía `thinking_default: "high"` sin `thinking` → validation fail en arranque — RESUELTO

- **Ubicación:** `config.example.yaml` (entrada qwen3.7-plus)
- **Problema:** `thinking_default: "high"` sin `thinking: [...]` viola la validación `config.go:462-465` (`thinking_default` pero `thinking` vacío → error de arranque). Un usuario que copiara el ejemplo no podría arrancar el gateway.
- **Resolución (2026-08-11, dueño):** se agregó `context_window: 1000000` y `thinking: ["high"]` a la entrada de qwen3.7-plus.

## Minor

### 2. `thinkingProfile` — clasificación por niveles de Thinking es frágil ante cambios de config (orden-dependiente) — TECHDEBT #25

- **Ubicación:** `internal/router/router.go` (thinkingProfile)
- **Problema:** la familia del modelo se deriva iterando `md.Thinking` (["always"]→kimi NUNCA, ["adaptive"]→minimax NUNCA, low/medium/max→reasoning, ["high"] exacto→qwen). Orden-dependiente y shadowing entre familias si la config evoluciona. Correcto para los 6 modelos actuales (niveles verificados en research §4/§5.4).
- **Decisión del dueño (2026-08-11):** fragilidad ACEPTADA y registrada en TECHDEBT #25. Sin cambio estructural ahora (los 6 modelos están verificados; `config.validate()` protege `thinking_default ∈ thinking`). Si el catálogo evoluciona: agregar campo declarativo `ThinkingFamily` ("reasoning"|"qwen"|"") o unit test que fije la semántica.

### 3. `thinking_path` no documentado en config.example.yaml — RESUELTO

- **Ubicación:** `config.example.yaml` providers section
- **Resolución (2026-08-11, dueño):** comentario documentando el knob agregado en provider-a (`thinking_path: "zen"` con valores `"" | "zen" | "bailian"` y nota de kimi/minimax).

## Opcionales / Nit / FYI

- **Nit 4 — re-encode JSON múltiple por request** (proxy clamp + router clamp + inject + ensureIncludeUsage): overhead sub-milisegundo, no es hot path de latencia. Aceptado.
- **Nit 5 — `bodyHasExplicitEffort` re-parsea el body** (podría extraerse en `ChatRequest`): optimización menor; registrado como candidato TECHDEBT.
- **FYI 6 — C8c asume provider 4096 en `buildMultiClient`** (400 < 4096 → sobrevive): correcto; comentario explícito.

## Positivos

1. **Posición del clamp por modelo** (proxy.go, tras ParseChatRequest y antes del window-check): exactamente donde el spec pide; el cache (2.6) keyea el body crudo y no se afecta.
2. **Window-check fiel**: efectivo `min(raw, MaxOutput)` inline sin cambiar la firma de `exceedsContextWindow`.
3. **Inyección per-attempt**: `explicitEffort` decidido UNA vez sobre el body original; cada intento recibe la inyección de SU path (C10 verificado).
4. **`thinkingInjectionFor` como tabla de datos pura** (§5.4 codificada; fallback a no-op = fallback rule P8).
5. **Nil-safety (P8) consistente** en cada punto de entrada.
6. **Tests E2E exhaustivos C1-C12** con guards de no-cambio desde el día 1.
7. **Suite completa verde** (387 tests, 17 paquetes, `-race`); vet + gofmt limpios.

---

*Materializado por el orquestador desde el reporte del reviewer (ref:armed-blue-snipe). Decisión del dueño sobre cada hallazgo documentada arriba.*
