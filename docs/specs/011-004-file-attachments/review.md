# Review — 011-004-file-attachments

Reviewer model: mofgw/qwen3.7-plus (familia distinta al implementer deepseek-v4-flash)

Fecha: 2026-08-12
Priorización: Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12)

## Bloqueantes

**Ninguno.** Tras examen del diff contra P1-P9, I1-I5, D1-D3, CA-1..CA-8: la implementación cumple el spec. Verificación:
- I1 (ADR-004): grep confirma cero referencias a input_image/input_file/file_data en router/ ni provider/. ParseChatRequest no se invoca desde responses.go.
- D1 (decisión por mensaje): hasFilePart discrimina string-vs-array por item de input[], no por request.
- D2 (mapeo de parts): las 3 traducciones matchean byte-a-byte el spec.
- D3 (sin gating): rama P9 de 001 eliminada, sin filtro por modality.
- I2 (data-URI sin re-marshal): strings en map, sin decodificar.
- I3 (orden preservado): responsesContentArray itera parts en orden.

## Opcionales

### 1. Dead code: helper `inputPart` sin callers tras POST-AUDIT
**Ubicación:** e2e_011001_responses_test.go:596-603
**Problema:** inputPart (construía parts con file_id sin campos reales) queda sin callers tras POST-AUDIT. Compila (Go no rechaza funciones no-usadas) pero es dead code.
**Decisión:** **APLICAR** — eliminar inputPart. Si se necesita un helper genérico, re-introducir con campos reales.

### 2. POST-AUDIT M1/M2 aserciones más débiles — RETIRADO tras re-verificación
Las aserciones de M1/M2 sí verifican los valores tal cual; equivalentes a P1/P2. No es un issue.

### 3. Sin test para part de tipo desconocido en mensaje con file parts
**Ubicación:** e2e_011004_fileattachments_test.go — no existe test.
**Problema:** spec §Contexto técnico recomienda "mapear solo tipos conocidos preservando orden y dropear desconocidas". La implementación cumple (default implícito) pero no hay test.
**Decisión:** **APLICAR** — agregar subtest en Bloque B: input_text + input_image + `{"type":"input_unknown"}` → content-array con 2 items en orden, sin el unknown.

### 4. Nombre de test `TestE2E011003_P10_RechazosConservados` parcialmente desactualizado
**Ubicación:** e2e_011003_toolcalling_test.go:632
**Decisión:** **DESCARTAR** (Nit) — cosmético; los nombres de subtests individuales son correctos.

### 5. `responsesContentArray` usa map en vez de structs tipados
**Ubicación:** responses.go:144-167
**Decisión:** **DESCARTAR** (Optional) — funcionalmente correcto; los 12 tests cubren el wire format. Refactor de mantenibilidad sin beneficio inmediato.

### 6. Duplicación de aserciones entre POST-AUDIT M1-M4 y tests 004 (FYI)
**Decisión:** sin acción — intencional, valor documental (muestra conversión rechazo→traducción en contexto original).

## Auditoría test↔postcondición

| Postcond. | Test(s) | Cubierta |
| ----- | ----- | ----- |
| P1 | P1_ImageURLDetailPassthrough, P1_ImageURLDetailAusenteOmitEmpty + M2/M4 | ✅ |
| P2 | P2_FilePartTraduccion, P2_FileSin400 + M1/M3 | ✅ |
| P3 | P3_SoloInputTextString, P3_MixtoArrayOrden, P3_MensajesMixtosPorMensaje | ✅ |
| P4 | P4_MixtoCicloIntacto | ✅ |
| P5 | P5_BodyCoexisteConContentArray | ✅ |
| P6 | P6_FileNo400UpstreamOk, P6_Upstream4xxPropagado | ✅ |
| P7 | Tests untouched (001 F + 003 P10) | ✅ |
| P8 | P8_DataURIDentroVentanaPasa | ✅ |
| P9 | Tests untouched (001 C/H + 003 P7) | ✅ |

- **Postcondiciones sin test:** ninguna (P7/P9 por tests untouched).
- **Tests sobrantes:** ninguno.
- **Dependencia interna (AP-14):** ninguno.

**Verificaciones especiales:**
- **D1:** P3 cubre los 3 caminos (string, array, ambos en mismo input[]).
- **D2:** P1 verifica image_url.url + detail passthrough/omitempty; P2 verifica file.file_data/filename.
- **P4:** P4_MixtoCicloIntacto verifica items del ciclo intactos con message-array.
- **P6:** P6_FileNo400 + P6_Upstream4xx (status 400 propagado, callCount==1, wire con traducción).
- **I3:** P3_MixtoArrayOrden verifica el orden text→image_url→file.
- **POST-AUDIT M1-M4:** correctos, parts completas (no reutilizan inputPart).

---

Status: Review completado. 0 bloqueantes. Priorización validada por Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12): aplicar #1 (dead code) y #3 (test part desconocida), descartar #4/#5, FYI #6.
