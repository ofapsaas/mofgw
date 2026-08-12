# Test Audit Report — 011-006-embeddings

**Feature:** `POST /v1/embeddings` (forward a Ollama, modelo forzado por cliente), epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-006-embeddings/spec.md` (aprobado por Ofap el 2026-08-12, P1-P10 + I1-I7).

## 1. Resumen del comportamiento que cambia

La feature 011-006 **agrega un endpoint NUEVO** `POST /v1/embeddings` (primera llamada saliente dedicada a Ollama, patrón `websearch.Client` de 005, ADR-005). No modifica el comportamiento de ningún endpoint existente:
- Sin cambio en `/v1/chat/completions` ni `/v1/responses` (I1).
- Sin cambio en providers/router: `provider.Client` NO se toca (I7).
- Sin cambio en auth: ruta bajo `s.auth.Wrap` (I6).
- Cambio de infra aditivo: `EmbeddingsConfig{BaseURL, APIKeyEnv}` global + `clients[].embeddings.model` por cliente + setter `SetEmbeddings`. Opt-in; `embeddings.model` opcional a nivel config, 400 de P4 a nivel request (fail-fast).

Todos los tests de la feature son **NUEVOS**. No se modifica ninguno existente.

## 2. Tests modificados

**NINGUNO (esperado: 0).** La feature agrega ruta y config nuevas sin alterar contrato de ningún endpoint existente.

## 3. Tests untouched (lista EXPLÍCITA)

Verificado por grep: ningún `*_test.go` referencia `embeddings`/`Embeddings`; sin test que enumere rutas del mux ni que espere 404 en `/v1/embeddings`; `config.go:376` usa `yaml.Unmarshal` lenient. La suite existente (~481 tests, 18 pkgs) queda **intacta**. Grupos de scope explícitos:
1. `internal/config/config_test.go` — campo nuevo aditivo/opcional; configs existentes cargan igual.
2. `internal/proxy/e2e_test.go:206-234` `TestE2EAuthRequired` — auth de chat intacta (I6).
3. `e2e_011001/011002/011003/011004/011005` — flujos de `/v1/responses` intactos.
4. `e2e_008002/009002/009000` — tests de budget de chat/responses intactos.
5. `e2e_006002/007003` — pricing/usage headers intactos (P7 reutiliza sin cambiar).
6. `internal/provider/*_test.go` — provider.Client intacto (I7).
7. `internal/router/*`, `internal/auth/*` — sin cambios.

## 4. Tests nuevos (mapeo P1-P10 → 10 bloques, archivo nuevo `internal/proxy/e2e_011006_embeddings_test.go`)

**Decisión de RED (tomada por el orquestador):** usar el patrón 001 (RED por 404 — la ruta `/v1/embeddings` no existe → 404/400 AssertionError), NO el patrón 005 (RED por compilación). Esto mantiene el gate RED limpio (falla por AssertionError, no por compile error). Los setters `SetEmbeddings`/`SetClientEmbeddingsModel` son el contrato que el implementer agrega en GREEN.

**Contrato de setters (implementer los implementa en GREEN, patrón `SetWebSearch`/`SetBudget`):**
- `func (s *Server) SetEmbeddings(baseURL, apiKey string)` — global.
- setter/mapa por cliente `clientID→model`, p.ej. `SetClientEmbeddingsModel(clientID, model)`.

| Bloque | Postcond. | Verificación observable |
| ----- | ----- | ----- |
| A — Auth (P1) | P1 | sin Bearer → 401; con Bearer + modelo → handler responde |
| B — Body inválido (P2) | P2 | not-json → 400; sin input → 400; con envelope error |
| C — Modelo forzado (P3) | P3 | body Odoo model:"x", cliente embeddings.model=="m" → upstream model=="m", envelope model=="m"; model:"x" no llega |
| D — Sin modelo (P4) | P4 | cliente sin embeddings.model → 400 "no embeddings model configured for client" |
| E — Input/dimensions passthrough (P5) | P5 | input byte-idéntico; dimensions passthrough; encoding_format passthrough |
| F — Respuesta OpenAI-compatible (P6) | P6 | shape list/data/embedding/index preservado; model forzado; vector nativo 384 verbatim |
| G — Pricing/usage (P7) | P7 | prompt_tokens incrementan usage; X-Usage-Prompt-Tokens; costo 0 sin pricing |
| H — Budget 429 (P8) | P8 | budget excedido → 429 rate_limit_exceeded |
| I — Error upstream 502 (P9) | P9 | Ollama 500 → 502; 429 no-reintentable → 502 |
| J — Envelope uniforme (P10) | P10 | 400/401/429/502 con envelope error y status correcto |

**Invariantes cross-feature:** I1/I7 (CA-9) request a `/embeddings` no a `/chat/completions`; I4 (CA-5) vector nativo verbatim.

**Total:** 13 tests nuevos en 10 bloques (A-J). Cada test mapea a postcondición.

## 5. Regression risk assessment

- **Bajo — validación de config:** si el implementer valida `embeddings.model` como requerido a nivel de carga (rompería config_test y configs existentes sin embeddings:). Spec P4 lo prescribe a nivel request → implementación esperada no rompe config tests.
- **Medio — gap de infraestructura:** el harness no inyecta base_url de Ollama ni modelo por cliente; requiere `SetEmbeddings` + setter por cliente inyectables (patrón SetBudget).
- **Medio — fallo RED por compilación:** mitigado por la decisión del orquestador de usar patrón 001 (RED por 404), no referenciar setters en el test.

## 6. Gate de Test Audit checklist

- [x] Comportamiento que cambia identificado (endpoint NUEVO; cero cambio en chat/responses/providers/auth).
- [x] Tests existentes auditados (grep completo: 0 referencias a embeddings; config lenient).
- [x] **0 tests modificados** (endpoint nuevo no toca código existente, I1/I7/I6).
- [x] Tests untouched listados explícitamente (7 grupos / ~481 tests).
- [x] Postcondiciones P1-P10 mapeadas a bloques (10 bloques, 13 tests) con mock Ollama + config embeddings.model.
- [x] Invariantes cross-feature I1/I4/I7 cubiertos.
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de RED.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
