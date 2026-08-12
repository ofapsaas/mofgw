# Test Audit Report — 011-008-odoo-provider

**Feature:** registro de mofgw como provider de IA de Odoo (módulo local `mofgw_ai`), última feature del epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-008-odoo-provider/spec.md` (aprobado por Ofap el 2026-08-12, P1-P11 + I1-I6).

## 1. Resumen del comportamiento que cambia

La feature **registra a mofgw como provider de IA de Odoo 19 enterprise** en el módulo local Python `mofgw_ai` (reemplazo total de OpenAI). Es **del lado Odoo**, como la 011-007. No toca **ningún archivo** del repo Go de mofgw.

Comportamiento observable nuevo:
1. `PROVIDERS` (módulo-global del core `ai`) gana un `Provider('mofgw', ...)` con `embedding_model='all-minilm'` y 5 llms — vía `PROVIDERS.append` (D1, monkeypatch in-place).
2. `LLMApiService` (clase base) tiene sus métodos `__init__`, `_get_api_token`, `_request_llm`, `_build_tool_call_response` **parcheados in-place** para `provider='mofgw'`; cada parche delega al original para `openai`/`google` (sin regresión, I2).
3. `ai.embedding.embedding_model` se re-declara con `selection_add=[('all-minilm','All-minilm')]` (D3).
4. Config por instancia: `ir.config_parameter` `ai.mofgw_url` + `ai.mofgw_key` (D4), `res.config.settings` hereda ambos, view heredada. Default URL D5.

**Impacto en suite Go:** NULO. La feature no toca `internal/` ni `cmd/`.

## 2. Tests modificados

**NINGUNO en el repo Go.** La feature opera 100% en el módulo Odoo `mofgw_ai`; mofgw (Go) ya sirve los contratos desde 011-001/011-006 y no cambian.

## 3. Tests nuevos (mapeo P1-P11 → tests de módulo Odoo, Python)

Los tests viven en **`mofgw_ai/tests/`** (módulo Odoo local, NO en el repo Go). 10 bloques:

| Bloque | Postcond. | Archivo propuesto | Verificación observable |
| ----- | ----- | ----- | ----- |
| B1 | P1, P2 | test_odoo_provider.py::test_provider_registered | PROVIDERS contiene 'mofgw' (5 llms, all-minilm); get_provider* resuelven; _get_llm_model_selection incluye los 5 |
| B2 | P3, I4 | ::test_get_embedding_model_mofgw | agent._get_embedding_model()=='all-minilm' |
| B3 | P4 | ::test_init_base_url | base_url == ai.mofgw_url o default D5; openai/google intactos |
| B4 | P5 | ::test_get_api_token | config → env → UserError; openai/google intactos |
| B5 | P6 | ::test_request_llm_delegates | _request_llm('mofgw') → _request_llm_openai (mock); openai/google intactos |
| B6 | P7 | ::test_build_tool_call_response | formato openai para 'mofgw'; openai/google intactos |
| B7 | P8 | ::test_get_embedding | POST <base>/embeddings con Bearer mofgw |
| B8 | P9 | ::test_settings_config | settings expone mofgw_url/key; view heredada |
| B9 | P10 | ::test_embedding_model_selection | Selection contiene ('all-minilm'), required=True |
| B10 | P11, C7 | test_odoo_provider_e2e.py::test_request_llm_e2e | E2E en staging contra mofgw real |

B1-B9 RED (fallan por AssertionError hasta implementar). B10 es E2E en staging.

## 4. Tests untouched (lista explícita)

**TODA la suite Go de mofgw** intacta por construcción. Grep en `internal/` y `cmd/` por `PROVIDERS|LLMApiService|mofgw_url|mofgw_key|monkeypatch|_get_embedding|get_provider` → 0 resultados. 55 archivos `*_test.go`, 19 paquetes, ~499 tests.

## 5. Regression risk assessment

- **Riesgo en repo Go: NO** (suite intacta).
- **Riesgo en lado Odoo: SÍ, moderado:**
  1. Monkeypatch global de LLMApiService — regresión en openai/google si un parche no delega. **Mitigado** por asserts I2 en B3-B7.
  2. **Idempotencia del registro/parche ante recarga del módulo** (upgrade/reinstall): el `PROVIDERS.append` + parches corren en el import; sin guard se duplicarían. **Pendiente de GREEN** — el append debe ser idempotente (`if not any(p.name=='mofgw' ...)`).
  3. EMBEDDING_MODELS_SELECTION snapshot — mitigado por override selection_add (B9).
  4. Desalineación de dimensión embeddings — mitigado por B1/B2 (string exacto).
  5. `ai.mofgw_key` vacía → UserError — comportamiento correcto per spec.

## 6. Gate — Test Audit checklist

- [x] Spec leído y aprobado.
- [x] Tests que validan comportamiento que cambia: NINGUNO en repo Go.
- [x] 0 tests modificados (verificado por grep 0 referencias).
- [x] Tests untouched listados explícitamente (toda la suite Go).
- [x] Tests nuevos mapeados P1-P11 → 10 bloques de tests de módulo Odoo.
- [x] Cada test mapea a postcondición; sin tests por completitud.
- [x] Sin tests dependientes de estructura interna.
- [x] Riesgo de regresión evaluado (Go: no; Odoo: mitigado).
- [x] **VERIFICATION gap:** suite Go intacta por construcción (0 archivos Go). Baseline build/vet validado en features previas.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
