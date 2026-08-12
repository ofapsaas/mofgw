# Review — 011-003-tool-calling

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

**Ninguno.** Tras examen exhaustivo de P1-P10, I1-I6, D1-D3, decisiones y código: sin divergencias funcionales, sin violaciones de boundaries, sin riesgos de seguridad, sin bugs de correctness.

Verificación por invariante:
- **I1:** provider.go solo agrega type definitions (ChatToolCall, ToolCalls en ChatMessage). No modifica ParseChatRequest, router ni providers. ADR-004 respetado.
- **I2:** arguments viaja como string crudo, sin re-marshal.
- **I3:** Parameters json.RawMessage preserva bytes; tests P1 y POST-AUDIT lo verifican.
- **I4:** call_id output = eco del id upstream; tool_call_id del role:"tool" = call_id del function_call_output. Test P4 verifica ambos matchean "call_abc".
- **I5:** zero llamadas OpenAI.
- **I6:** suite verde verificada por el orquestador (`go test ./... -race` → 452 passed).

## Opcionales

### 1. Adjacent function_call items generan mensajes assistant separados (no agrupados)
**Ubicación:** responses.go:238-256
**Problema:** cada item function_call genera un assistant message independiente con un solo tool_call. Con parallel tool calls, Odoo podría mandar varios adyacentes → N mensajes assistant separados en vez de 1 con N tool_calls. Spec-compliant (documentado como out-of-scope), funcional para Odoo.
**Decisión:** **DESCARTAR** — spec-compliant, documentado como no-exigido, funcional para Odoo (parsea tool_calls uno a uno). Hardening futuro, no de esta feature.

### 2. Subtest tool_calls_vacio no ejercita `tool_calls: []` (sino ausente)
**Ubicación:** e2e_011003_toolcalling_test.go:449-471
**Problema:** usa upstreamOK("m","hola") que no incluye tool_calls; el nombre promete array vacío. Funcionalmente equivalente (len 0 trata nil y empty igual en `len(toolCalls) > 0`).
**Decisión:** **DESCARTAR** (Nit) — cobertura funcional equivalente; el comentario del test ya lo aclara.

### 3. Error message genérico para tool JSON malformado
**Ubicación:** responses.go:188-189
**Problema:** unmarshal fallido retorna "tool calling not yet supported" (mismo que type!=function). Un tool con JSON inválido no es "no soportado" — es malformado.
**Decisión:** **APLICAR** — separar ramas: unmarshal fallido → "invalid tool definition"; type != function → "tool calling not yet supported" (P2/D3). Mejora diagnóstico sin complejidad.

### 4. Todos los items function_call comparten el mismo id estable (FYI)
**Decisión:** sin acción — diseño intencional verificado contra Odoo (distingue por call_id).

### 5. responsesInputItem struct flat (FYI)
**Decisión:** sin acción — pragmático, consistente con responsesRequestBody.

## Auditoría test↔postcondición

| Test | Postcondición | Invariante/Decisión | Contrato observable |
| ---- | ------------- | ------------------- | ------------------- |
| P1_ToolsFunctionAChatAnidado | P1 | I3 | ✅ wire tools anidados, parameters byte-idénticos, strict/parallel passthrough |
| P2_WebSearchPreview400 | P2 | D3 | ✅ 3 subtests (incl. mixto function+no-function) |
| P3_FunctionCallItemAAssistantToolCalls | P3 | D1 | ✅ tool_call.id==call_id, fallback a id, arguments string |
| P4_FunctionCallOutputItemAToolRole | P4 | I4, D1 | ✅ role:"tool" + round-trip combinado |
| P5_MessageItemsSinRegresion | P5 | — | ✅ 4 mensajes en orden de aparición |
| P6_ToolCallsASoloFunctionCallItems | P6 | D1, D2, I2 | ✅ N function_call, cero message, call_id eco, arguments parseable |
| P7_SinToolCallsItemMessage | P7 | — | ✅ item message con content[0].text |
| P8_IdEstableDeterministico | P8 | D1 | ✅ id determinístico, id ≠ call_id, call_id eco |
| P9_PipelineCompartidaYPassthrough | P9 | I2, I3 | ✅ 3 subtests (thinking, cache, ventana) |
| P10_RechazosConservados | P10 | — | ✅ 8 subtests (incl. json_schema coexiste con tools) |
| POST-AUDIT subtest tools (001) | P1 | I3 | ✅ conversión rechazo→traducción |

- **Postcondiciones sin test:** 0 (10/10 cubiertas).
- **Tests sobrantes:** 0.
- **Tests con dependencia interna (AP-14):** 0.

**Verificaciones específicas:**
- **D1:** verificada en ambas direcciones (P6/P8 salida: call_id eco upstream + id ≠ call_id; P4 entrada: tool_call.id == call_id).
- **D2:** P6 verifica exactamente N function_call, cero message.
- **I4:** P4 round-trip combinado (assistant tool_call.id == tool.tool_call_id == call_abc).
- **P2/D3 mixto:** P2 subtest mixto function+web_search verifica que el 400 gana.
- **POST-AUDIT:** coherente; el 400 de tools function se reemplaza por traducción verificada, el 400 de no-function sigue cubierto por web_search_preview.

---

Status: Review completado. 0 bloqueantes. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #3, descartar #1/#2, FYI #4/#5 sin acción.
