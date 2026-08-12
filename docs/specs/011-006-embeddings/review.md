# Review — 011-006-embeddings

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

**Ninguno.** Tras examen del diff contra P1-P10, I1-I7: sin hallazgos ≥80% de confianza. Verificación:
- I1: router/provider sin cambios; ruta agregada en mux de proxy.go:359 sin tocar rutas existentes.
- I2: `embeddings.Ollama.Embed` hace POST baseURL+"/embeddings", nunca api.openai.com.
- I7: provider.go solo tiene /chat/completions en líneas pre-existentes 260/295.
- P3: `embeddingsModelFor(clientID)` + `upMap["model"]=model` (model de Odoo nunca copia) + `rewriteResponseModel` fuerza envelope.
- P10: todos los paths de error por `openAIError` (envelope uniforme).

## Opcionales

### 1. Sin validación startup: cliente con embeddings.model pero sin base_url global
**Ubicación:** cmd/mofgw/main.go:256-263
**Decisión:** **DESCARTAR** — el spec no lo prescribe (P4 fail-fast a nivel request); la config.example documenta la dependencia. Fallo se descubre en prod, aceptable.

### 2. `parseEmbeddingsUsage` define struct local en vez de reutilizar `provider.Usage`
**Ubicación:** internal/proxy/embeddings.go:156-172
**Decisión:** **APLICAR** — usar `json.Unmarshal(raw, &resp)` con campo `Usage provider.Usage` directo, eliminando el struct duplicado y la conversión manual. Evita drift si provider.Usage agrega campos.

### 3. Comentarios del archivo de tests aún hablan de "RED"
**Ubicación:** e2e_011006_embeddings_test.go:5-38, 122-138
**Decisión:** **DESCARTAR** (Nit) — documentación histórica correcta; no afecta.

### 4. `rewriteResponseModel` hace JSON round-trip completo sobre la respuesta de embeddings
**Ubicación:** internal/proxy/embeddings.go:146 → proxy.go:1412-1427
**Decisión:** **DESCARTAR** (Optional) — los vectores se preservan (RawMessage); el spec no prescribe byte-exactitud del envelope. Optimización de performance sin beneficio crítico.

### 5. Límite de respuesta upstream de 4MB sin documentación de contract (FYI)
**Decisión:** documentar el límite en el comment del método `Embed` (nota de implementación).

### 6. Frontmatter del spec no actualizado
**Ubicación:** docs/specs/011-006-embeddings/spec.md:8 (`approved_by: pendiente`)
**Decisión:** **APLICAR** — actualizar frontmatter a `approved_by: "Ofap (agent-delegated)"` para consistencia con la línea de Status.

## Auditoría test↔postcondición

| Test | Postcondición | Cubierta |
| ----- | ----- | ----- |
| A (Auth) | P1 | ✅ |
| B (BodyInvalido) | P2 | ✅ |
| C (ModeloForzado) | P3 | ✅ |
| D (ClienteSinModelo) | P4 | ✅ |
| E (InputDimensionsPassthrough) | P5 | ✅ |
| F (RespuestaOpenAICompatible) | P6 | ✅ |
| G (PricingUsage) | P7 | ✅ |
| H (Budget) | P8 | ✅ |
| I (ErrorUpstream) | P9 | ✅ |
| J (EnvelopeUniforme) | P10 | ✅ |

- **Postcondiciones sin test:** ninguna (10/10).
- **Tests sobrantes:** ninguno.
- **Dependencia interna (AP-14):** no detectada (mock upstream = boundary externo inyectado).

**Verificaciones especiales:**
- P3: test C exhaustivo (upstream model "m" no "x"; assert negativo de "model":"x"; envelope "m").
- P4: test D (buildMultiClient sin setter → 400 + message exacto).
- P5: test E (input verbatim, dimensions 1536, encoding_format float).
- P6: test F (vector 384-dim elemento-a-elemento, shape, model, usage).
- P7: test G (assertUsageHeaders 10/0/10/0/"0" + métrica).
- P8: test H (budget 15, 2 requests 20 ≥ 15 → 3er 429).
- I1/I7 (CA-9): test C verifica path /embeddings, no /chat/completions.

---

Status: Review completado. 0 bloqueantes. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #2 y #6, descartar #1/#3/#4, FYI #5 documentado.
