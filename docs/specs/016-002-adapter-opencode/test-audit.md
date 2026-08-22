# Test Audit — 016-002-adapter-opencode

**Rol:** test-writer, AUDIT mode (pre-RED) · **Feature:** `016-002-adapter-opencode` · **Proyecto:** mofgw · **Fecha audit:** 2026-08-22

## 1. Baseline green evidence

```
$ go test ./... -count=1
Go test: 640 passed in 24 packages
```

(1 corrida mostró un flake estadístico aislado `TestPostcondition9_MuestreoBanda` — banda 40..60, 62 eventos con sample_rate 0.5; test del telemetry 009-000, no relacionado. En re-ejecución: 640/640 verde. Flake conocido del repo, no regresión.)

## 2. Impact assessment

La feature 016-002 es un **adapter puro de serialización** en paquete nuevo `internal/clientconfig/opencode/`. Impacto sobre suite existente: **nulo** (no toca internal/proxy/* ni internal/config/*; agrega un Renderer nuevo + su registro). Cero regresión prevista.

- Nuevo paquete `internal/clientconfig/opencode/` — no colisiona con nada.
- `clientconfig.Register("opencode", ...)` — opera sobre el mapa exportado existente (P10 de 001); no cambia su semántica.
- No toca ruta /v1, no toca config, no toca providers.

## 3. Test ↔ postcondición mapping (feature NEW)

| P | Assert (1 línea) | Ubicación esperada | Observable |
|---|---|---|---|
| P1 | Render(ir) con BaseURL+KeyEnvRef+2 modelos → JSON válido, top-level clave "mofgw" | internal/clientconfig/opencode (unit) | ✔ |
| P2 | mofgw.options.baseURL == ir.BaseURL | idem | ✔ |
| P3 | mofgw.options.apiKey == "{env:MOFGW_KEY}" (STRING, no objeto, no literal sk-) | idem | ✔ |
| P4 | mofgw.npm == "@ai-sdk/openai-compatible" && name == "mofgw" | idem | ✔ |
| P5 | por cada ir.Models[i]: models[id] existe; limit.context/output de Meta si presentes | idem | ✔ |
| P6 | models no contiene ids fuera de IR.Models | idem | ✔ |
| P7 | IR.Models vacío → models == {} (válido, sin error) | idem | ✔ |
| P8 | modelo sin metadata → sin clave limit (no cero/muerta) | idem | ✔ |
| P9 | BaseURL o KeyEnvRef vacío → Render error no-nil | idem | ✔ |
| P10 | Register("opencode",r) → Lookup != nil && "opencode" ∈ SupportedList | idem (+176 clientconfig) | ✔ |
| P11 | dos llamadas mismo IR → bytes idénticos (determinista) | idem | ✔ |

Todas observables, serialización pura, sin mocks de plumbing.

## 4. Pre-existing tests que engañarán tras esta feature

**Ninguno.** Paquete nuevo puro; no golpea rutas ni configuración existente. La prueba del endpoint E2E (client=opencode 200) se hace a nivel epic, no en esta feature.

**Conclusión:** feature de serialización pura, no-regresión confirmada (640 pass). Tests P1-P11 en `internal/clientconfig/opencode/opencode_test.go`. Nombre/registro del renderer: `clientconfig.Register("opencode", opencode.Renderer{})`.