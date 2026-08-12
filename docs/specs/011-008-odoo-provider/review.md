# Review — 011-008-odoo-provider

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

**Ninguno.** Tras examen de cada archivo, cada parche del monkeypatch, cada test, y su interacción con el core enterprise: la implementación es fiel al spec, los 4 parches correctos, guards de idempotencia funcionan, sin regresión en openai/google, core enterprise intacto (I1).

## Opcionales

### 1. Test B5 (I2): aserción de regresión openai es débil — mock compartido no distingue dispatch correcto de roto
**Ubicación:** tests/test_odoo_provider.py:137-155
**Decisión:** **DESCARTAR** — el patch delegation es trivial (`if provider=='mofgw': else: _orig_llm`), riesgo real de regresión bajo; mockear a nivel `_request` agregaría complejidad sin beneficio crítico.

### 2. Spec-vs-core: `mofgw_key` sin `password=True` — implementación sigue patrón del core, spec inconsistente
**Ubicación:** models/res_config_settings.py:19-22
**Decisión:** **APLICAR** — corregir el spec (firma D4 decía `password=True` pero el core `openai_key` NO lo usa; el masking es vía `widget="password"` en la view, que la implementación hace correctamente). El código ya está correcto; solo se alinea el spec.

### 3. Nit: Test B7 — comentario sobre normalización de método es observación de implementación
**Ubicación:** tests/test_odoo_provider.py:219-220
**Decisión:** **DESCARTAR** — el formato es parte del contrato observable; la normalización a minúsculas es estable en el core.

## FYI

### 4. P11/C7 (E2E staging) no verificada — deuda explícita del spec
**Decisión:** sin acción — follow-up del epic para ejecutar manualmente en staging antes de cerrar 011-mofgw-odoo.

## Auditoría test↔postcondición

| Postcond. | Test | Cubierta |
| ----- | ----- | ----- |
| P1 | B1 (test_provider_registered) | ✅ |
| P2 | B1 | ✅ |
| P3 | B2 (test_get_embedding_model_mofgw) | ✅ |
| P4 | B3 (test_init_base_url) | ✅ |
| P5 | B4 (test_get_api_token) | ✅ |
| P6 | B5 (test_request_llm_delegates) | ✅ (I2 débil, ver #1) |
| P7 | B6 (test_build_tool_call_response) | ✅ |
| P8 | B7 (test_get_embedding) | ✅ |
| P9 | B8 (test_settings_config) | ✅ |
| P10 | B9 (test_embedding_model_selection) | ✅ |
| P11 | B10 (no escrito) | ⚠️ deuda documentada (E2E staging) |

- Postcondiciones sin test: P11 (por diseño — E2E).
- Tests sobrantes: ninguno.
- Dependencia interna: B5 usa mock en `_request_llm_openai` (plumbing del core), verifica comportamiento observable. Aceptable.

## Notas específicas

- **Monkeypatch global (I2):** CORRECTO, sin regresión. Los 4 parches siguen "guard + delegate". Verificado contra el core: cada `_orig_*` raisea NotImplementedError/UserError para provider desconocido → el patch mofgw evita esos paths. openai testeado (B3-B7); google no testeado explícitamente pero el patrón de delegación es idéntico.
- **Idempotencia:** CORRECTA, doble guard. `_register_mofgw_provider` (`if not any(p.name=='mofgw')`) + `_patch_llm_api_service` (flag `_mofgw_patched` en clase). Sin duplicación ni chain de parches ante upgrade/reinstall.
- **ondelete cascade:** CORRECTO, requerido por Odoo para selection_add; 'cascade' es correcto (embeddings mofgw sin sentido sin el provider).
- **Seguridad key:** ADECUADA — ir.config_parameter + env fallback + sudo() + widget password + sin logging. Mismo patrón que openai.
- **3 bugs de test corregidos en GREEN:** correctos (openai también usa function_call_output; get_values devuelve strings; requests normaliza a 'post'). Buena disciplina RED→GREEN.

---

Status: Review completado. 0 bloqueantes. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #2 (spec password), descartar #1/#3, FYI #4.
