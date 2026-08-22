# Test Audit — 016-003-adapter-openclaw

**Rol:** test-writer, AUDIT mode (pre-RED) · **Feature:** `016-003-adapter-openclaw` · **Proyecto:** mofgw · **Fecha audit:** 2026-08-22

## 1. Baseline green evidence

```
$ go test ./... -count=1
Go test: 659 passed in 25 packages
```

Baseline verde (incluye 016-001 y 016-002 merged).

## 2. Impact assessment

Feature 016-003 es **adapter puro de serialización** en paquete nuevo `internal/clientconfig/openclaw/` — replica el patrón de 016-002 (opencode). Impacto sobre suite existente: **nulo** (no toca proxy/config; agrega un Renderer nuevo + su registro).

- Nuevo paquete `internal/clientconfig/openclaw/` — no colisiona.
- `clientconfig.Register("openclaw", ...)` — opera sobre el mapa exportado existente (P10 de 001); scoped en tests (lección de 002: borrar solo "openclaw", no todo el registry).
- No toca ruta /v1, no toca config, no toca providers.

## 3. Test ↔ postcondición mapping (feature NEW)

| P | Assert (1 línea) | Ubicación esperada | Observable |
|---|---|---|---|
| P1 | Render(ir) con BaseURL+KeyEnvRef+2 modelos → JSON válido, top-level "models" | internal/clientconfig/openclaw (unit) | ✔ |
| P2 | models.providers.mofgw.baseUrl == ir.BaseURL | idem | ✔ |
| P3 | models.providers.mofgw.apiKey == "${MOFGW_KEY}" (STRING, no SecretRef, no sk-) | idem | ✔ |
| P4 | api == "openai-completions" && key "mofgw" | idem | ✔ |
| P5 | por cada ir.Models[i]: models[].id existe; contextWindow/maxTokens de Meta si presentes | idem | ✔ |
| P6 | models no contiene ids fuera de IR.Models | idem | ✔ |
| P7 | IR.Models vacío → models == [] (válido, sin error) | idem | ✔ |
| P8 | modelo sin metadata → entrada solo id (sin contextWindow/maxTokens/name) | idem | ✔ |
| P9 | BaseURL o KeyEnvRef vacío → Render error no-nil | idem | ✔ |
| P10 | Register("openclaw",r) → Lookup != nil && "openclaw" ∈ SupportedList | idem | ✔ |
| P11 | dos llamadas mismo IR → bytes idénticos (determinista) | idem | ✔ |
| P12 | presente-pero-≤0 → sin contextWindow (context_length:0); fallback (max_context_length solo) → valor fallback; output análogo | idem | ✔ |

Todas observables, serialización pura, sin mocks de plumbing. P12 es la lección aprendida del hallazgo #1 de la review de 016-002 (se escribe desde RED).

## 4. Pre-existing tests que engañarán tras esta feature

**Ninguno.** Paquete nuevo puro. E2E del endpoint (client=openclaw 200) es del epic, no de esta feature.

**Conclusión:** feature de serialización pura, no-regresión confirmada (659 pass). Tests P1-P12 en `internal/clientconfig/openclaw/openclaw_test.go`. Nombre/registro: `clientconfig.Register("openclaw", openclaw.Renderer{})`. Env-ref openclaw = `${VAR}` (NO `{env:VAR}` de opencode). Formato JSON (JSON5-compatible).