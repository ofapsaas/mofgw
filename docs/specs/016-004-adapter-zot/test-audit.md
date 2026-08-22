# Test Audit — 016-004-adapter-zot

**Rol:** test-writer, AUDIT mode (pre-RED) · **Feature:** `016-004-adapter-zot` · **Proyecto:** mofgw · **Fecha audit:** 2026-08-22

## 1. Baseline green evidence

```
$ go test ./... -count=1
Go test: 675 passed in 26 packages
```

Baseline verde (incluye 016-001/002/003 merged).

## 2. Impact assessment

Feature 016-004 es **adapter puro de serialización** en paquete nuevo `internal/clientconfig/zot/` — replica el patrón de 002/003. Impacto sobre suite existente: **nulo** (no toca proxy/config; agrega un Renderer nuevo + su registro).

- Nuevo paquete `internal/clientconfig/zot/` — no colisiona.
- `clientconfig.Register("zot", ...)` — opera sobre el mapa exportado; cleanup scoped (solo "zot").
- Desviación I1: NO hay campo apiKey; `IR.KeyEnvRef` no se emite ni es obligatorio (P9-dependent). Diferente de 002/003 — clave en los tests.

## 3. Test ↔ postcondición mapping (feature NEW)

| P | Assert (1 línea) | Ubicación esperada | Observable |
|---|---|---|---|
| P1 | Render(ir) → JSON válido, top-level "providers" | internal/clientconfig/zot (unit) | ✔ |
| P2 | providers.mofgw.baseUrl == ir.BaseURL | idem | ✔ |
| P3 | NO hay clave "apiKey"/"key" ni strings template (${}/{env:}) en el output | idem | ✔ |
| P4 | api == "openai" && key "mofgw" | idem | ✔ |
| P5 | por cada ir.Models[i]: models[j].id existe; contextWindow/maxTokens de Meta si presentes; name de display_name si no vacío | idem | ✔ |
| P6 | models ids exactamente == IR.Models ids (sin extras) | idem | ✔ |
| P7 | IR.Models vacío → models == [] (no null) | idem | ✔ |
| P8 | modelo sin metadata → entrada solo id | idem | ✔ |
| P9 | BaseURL vacío → error; KeyEnvRef vacío → NO error (desviación I1) | idem | ✔ |
| P10 | Register("zot",r) → Lookup != nil && "zot" ∈ SupportedList; cleanup scoped | idem | ✔ |
| P11 | dos llamadas mismo IR → bytes idénticos | idem | ✔ |
| P12 | contexto presente-pero-≤0 → omitido; fallback key solo → valor fallback | idem | ✔ |

Todas observables, serialización pura, sin mocks. P3 y P9 son la desviación I1 (diferencial de este adapter vs opencode/openclaw).

## 4. Pre-existing tests que engañarán tras esta feature

**Ninguno.** Paquete nuevo puro. E2E del endpoint (client=zot 200) es del epic, no de esta feature.

**Conclusión:** feature de serialización pura, no-regresión confirmada (675 pass). Tests P1-P12 en `internal/clientconfig/zot/zot_test.go`. Registro: `clientconfig.Register("zot", zot.Renderer{})`. Shape: top-level "providers", api "openai", sin apiKey (env derivada MOFGW_API_KEY).