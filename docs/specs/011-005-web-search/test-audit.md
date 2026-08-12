# Test Audit Report — 011-005-web-search

**Feature:** grounded search server-side (web_search_preview → DDG → inyección → item message) en `/v1/responses`, epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-005-web-search/spec.md` (aprobado por Ofap el 2026-08-12).

## 1. Resumen del comportamiento que cambia

La feature 011-005 reemplaza el rechazo incondicional D3 de 003 (`responses.go:227-230`: cualquier tool `type != "function"` → HTTP 400) por un **grounded search server-side** para el caso específico `web_search_preview`:

- **D5 gating (`web_search.enabled`):** con `enabled:false` (default) el 400 de D3 de 003 se **conserva intacto** (sin regresión). Solo con `enabled:true` el flujo web search se activa para `web_search_preview`.
- La rama cambia de "400 incondicional" a un despacho de 3 vías: (a) `web_search_preview` + `enabled:true` → flujo web search (P2-P7); (b) `web_search_preview` + `enabled:false` → **conserva el 400** (P1); (c) cualquier `type != "function"` no-web-search (p.ej. `code_interpreter`) → **conserva el 400 en ambos modos** (P1).
- Cambios de infra: nuevo campo `WebSearch` en `Config` (default `false`, opt-in) + nuevo cliente DDG inyectado en `Server`.

## 2. Tests modificados

**NINGUNO (esperado: 0).** Los tests existentes que ejercitan `web_search_preview` esperan HTTP 400, y **siguen verdes** porque corren contra la config default (`web_search.enabled:false`), que el spec conserva (P1, D5). Ninguno cambia de contrato.

## 3. Tests untouched (lista EXPLÍCITA)

Verificado por grep (`web_search_preview` / `tool calling not yet supported` / `web_search` en `*_test.go`):

1. **`internal/proxy/e2e_011001_responses_test.go:485-490`** — subtest `web_search_preview` del bloque F: `tools:[{"type":"web_search_preview"}]` → espera 400. **Untouched** (P1, CA-1).
2. **`internal/proxy/e2e_011003_toolcalling_test.go:220-245`** — `TestE2E011003_P2_WebSearchPreview400`:
   - subtest `web_search_preview` (224-229): 400. **Untouched** (P1, CA-1).
   - subtest `tipo_no_function` (230-235, `code_interpreter`): 400. **Untouched** (no-function-no-web-search → 400 en ambos modos, CA-9).
   - subtest `mixto_function_y_no_function` (236-244): 400. **Untouched** (con enabled:false default, P1 gana y produce 400).

Referencia de no-afectación: subtest `tools` de 001 (traducción function) no cambia — 005 no toca la traducción de function tools (P5 remite a 003 P1).

## 4. Tests nuevos (mapeo P1-P8 → 8 bloques, archivo nuevo `internal/proxy/e2e_011005_websearch_test.go`)

Requieren **mock del cliente DDG** (I6) y config con `web_search.enabled:true`. El harness actual (`build()`) no expone inyección de config/cliente DDG → archivo nuevo necesita helper `buildWithWebSearch(...)` (config enabled:true + cliente DDG fake).

| Bloque | Postcond. | Verificación observable |
| ----- | ----- | ----- |
| A — Gating (P1) | P1 | enabled:true + web_search_preview → no 400; code_interpreter → 400 |
| B — Query (P2) | P2 | query == concat último item user; DDG mock recibió esa query; determinismo |
| C — DDG (P3) | P3 | mock >N resultados → top-N consumido; fallback Instant Answer |
| D — Inyección (P4) | P4 | wire messages[0] role:"system" con header + exactamente N items; sin url_citation |
| E — Remoción (P5) | P5 | wire tools sin web_search_preview; function tools traducidos; si no queda → tools ausente |
| F — Salida (P6) | P6 | output[] con un solo item message; sin function_call por web search |
| G — Fallo best-effort (P7) | P7 | DDG falla → no 400, sin system message, web_search_preview removido, ungrounded; sin user message → sin grounding |
| H — Pipeline/cache (P8) | P8 | cache excluido: mismo body + DDG mock A → A; mock B → B (nunca cacheado A) |

**Total:** 10 tests nuevos (8 bloques). CA-9 (rechazos conservados con enabled:false) ya cubierto por tests untouched — no se escribe test redundante.

## 5. Regression risk assessment

- **Medio — regresión del 400 en modo default:** si el implementer no preserva la rama 400 con enabled:false, los tests untouched 1 y 2 se ponen rojos. **Mitigación:** re-corridos en POST-AUDIT. Es el riesgo principal.
- **Medio — gap de infraestructura de test:** el harness `build()` no inyecta config web_search ni cliente DDG. Si el cliente DDG no se expone como interfaz inyectable, los tests del flujo enabled:true no se pueden mockear. **Dependencia a verificar en RED** (spec §251-259 lo prescribe).
- **Bajo — campo nuevo en Config:** agregar WebSearch no rompe tests (yaml.Unmarshal lenient); config.example.yaml aditivo.
- **Bajo — test infraestructura:** buildWithWebSearch debe respetar el patrón (httptest, t.Cleanup, cero red I6).

## 6. Gate de Test Audit checklist

- [x] Comportamiento que cambia identificado (D3 → despacho 3 vías con gating enabled).
- [x] Tests afectados auditados (grep completo: solo 001 subtest + 003 P2).
- [x] 0 tests modificados — verificado que los 400 de web_search_preview siguen verdes con enabled:false default (P1, D5).
- [x] Tests untouched listados explícitamente (2 bloques / 4 subtests).
- [x] Postcondiciones P1-P8 mapeadas a bloques (8 bloques, 10 tests) con mock DDG.
- [x] Riesgos de regresión evaluados.
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de RED.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
