# Review — 010-001-catalogo-fiel
Status: APPROVE WITH COMMENTS — bloqueante C7 resuelto 2026-08-11 (config.example.yaml actualizado); 2 nits/FYI sin acción. Reviewer: qwen3.7-plus (familia distinta al implementer).

# I now have all the information...

I now have all the information needed. Let me produce the review.

---

# Review — 010-001-catalogo-fiel

**Reviewer model:** mofgw/qwen3.7-plus (fami...

**ID:** loyal-chocolate-aphid
**Agent:** cdad-reviewer
**Status:** complete
**Session:** ses_011b82484ffeB3Hs38U3HkVmql
**Started:** 2026-08-11T00:45:14.502Z
**Completed:** 2026-08-11T00:47:50.302Z

---

I now have all the information needed. Let me produce the review.

---

# Review — 010-001-catalogo-fiel

**Reviewer model:** mofgw/qwen3.7-plus (familia DISTINCTA al implementer deepseek-v4-flash)

## Verdict: APPROVE WITH COMMENTS

La implementación de código satisface las 8 postcondiciones (P1-P8) y los 6 invariantes (I1-I6). Los tests verifican el contrato observable (JSON de `/v1/models`), no detalles internos. Hay **un bloqueante**: C7 (config.example.yaml) no fue actualizado.

---

## Bloqueantes (Critical + Required)

### 1. C7 no satisfecho — config.example.yaml sin actualizar

**Ubicación:** `config.example.yaml` (líneas 78-88)

**Problema:** El criterio de aceptación C7 requiere explícitamente:
1. Documentar `supported_parameters` y `modality` en `model_metadata` → **no aparecen en ningún YAML del proyecto** (confirmado vía grep: 0 matches para `supported_parameters|modality` en `*.yaml`).
2. Reflejar los `thinking_default` prescriptivos (spec §"Datos esperados") → **deepseek-v4-flash tiene `thinking_default: "high"` (línea 83) cuando el spec exige `"low"`** (tabla Datos esperados + decisión de diseño #2).
3. qwen3.7-plus `max_output: 131072` (D8) → **qwen3.7-plus no está en config.example.yaml**, y el valor 131072 no aparece en ningún YAML.

El test-audit (§ Riesgos, punto 4) documenta explícitamente que C7 "se verifica en review/deploy, no en `go test`" — esta review es el gate. La spec dice "Changes de código esperados: config.example.yaml: documentar campos nuevos + tabla de datos esperados".

**Sugerencia:** Actualizar `config.example.yaml`:
- Agregar `supported_parameters` y `modality` al bloque `deepseek-v4-flash`.
- Corregir `thinking_default: "high"` → `"low"` para deepseek-v4-flash.
- Agregar al menos una entrada con modality multimodal (ej. kimi-k2.7-code con `modality: "text+image+video->text"`) como documentación viva de los campos nuevos.
- Considerar agregar qwen3.7-plus con `max_output: 131072` (D8).

**Severidad:** Required (criterio de aceptación explícito, verificable en este gate)

---

## Opcionales (Optional + Nit + FYI)

### 2. Nit: `time.Now().Unix()` en el path de respuesta del catálogo

**Ubicación:** `internal/proxy/proxy.go:965`

**Problema:** Cada llamada a `/v1/models` genera un `created` distinto. No es un regression de 010-001 (viene de 007-002), pero es una inconsistencia semántica: `created` sugiere un timestamp de creación del modelo, no de la respuesta.

**Severidad:** Nit (preexistente, fuera de scope de esta feature)

### 3. FYI: `top_provider` como nombre de key

**Ubicación:** `internal/proxy/proxy.go:1012`

**Problema:** El nombre `top_provider` es un artefacto del formato OpenRouter (D1). En mofgw no hay concepto de "top provider" — es un eco del formato que leen los clientes. La implementación es fiel al spec (que adopta explícitamente esa convención). Sin acción necesaria; documentado para contexto futuro.

**Severidad:** FYI

---

## Verificación postcondición por postcondición

| Postcondición | Satisfecha | Evidencia en código | Test |
|---|---|---|---|
| **P1** — Config extensible + backward compat | ✅ | `config.go:189,192` (campos con yaml tags, sin validación nueva) | `Test010001_P1_CargaConClavesNuevas`, `Test010001_P1_BackwardCompat` |
| **P2** — Zero-value → ausente | ✅ | `proxy.go:989` (`len>0`), `:994` (`!=""`), `:1004` (`cw>0 \|\| MaxOutput>0`), `:1014` (`MaxOutput>0`) | `TestE2E010001_P2_MetadataSinClavesNuevas` |
| **P3** — supported_parameters fiel | ✅ | `proxy.go:990-992` (copy exacto, sin sort/dedup/aggregate) | `TestE2E010001_P3_SupportedParameters` |
| **P4** — modality + architecture derivada | ✅ | `proxy.go:994-1002` + `splitModality:1033-1048` (split mecánico `->` y `+`) | `TestE2E010001_P4_ModalityYArchitecture` (multimodal + text->text) |
| **P5** — modality no parseable → architecture ausente | ✅ | `splitModality:1035` (guard: `len(sides)!=2 \|\| sides[0]=="" \|\| sides[1]==""`) | `TestE2E010001_P5_ModalityNoParseable` ("text" + "text->") |
| **P6** — top_provider + max_output_tokens | ✅ | `proxy.go:1004-1016` (guards individuales por campo) | `TestE2E010001_P6_TopProviderYMaxOutput` (full + ctxonly) |
| **P7** — thinking_default prescriptivo | ✅ | `proxy.go:982-984` (passthrough del valor configurado) | `TestE2E010001_P7_ThinkingDefaultPrescriptivo` |
| **P8** — Envelope, mínimos, pricing, auth | ✅ | `proxy.go:962-967` (4 campos mínimos), `:1018-1024` (pricing sin cambios) | `TestE2E010001_C2_SinMetadataMinimo`, `TestE2E010001_C6_EnvelopeYAuth` |
<!-- table not formatted: invalid structure -->

## Verificación de invariantes

| Invariante                           | Satisfecho | Evidencia                                                                              |
| ------------------------------------ | ---------- | -------------------------------------------------------------------------------------- |
| **I1** — Backward compat                 | ✅         | Campos nuevos son aditivos; modelo sin metadata → 4 campos (`len(item) != 4` en C2 test) |
| **I2** — Valores de metadata declarativa | ✅         | Zero hardcodeo; todo viene de `s.modelMeta[model]`                                       |
| **I3** — Fallback rule                   | ✅         | `wantAbsent` en tests verifica ausente ≠ 0/[]/{}                                         |
| **I4** — Derivación mecánica             | ✅         | `splitModality` es pura (sin side effects, sin fuentes externas)                         |
| **I5** — Cardinalidad                    | ✅         | Mismo loop de modelos (proxy.go:920-944); no se agregan ni quitan entradas             |
| **I6** — Suite verde                     | ✅         | Verificado empíricamente (17 paquetes PASS con -race)                                  |

## Verificación de puntos críticos solicitados

### splitModality edge cases
| Input                     | `strings.Split("->")`             | Guard `:1035`          | Resultado                                                           |
| ------------------------- | ------------------------------- | -------------------- | ------------------------------------------------------------------- |
| `"text"`                    | `["text"]` (len 1)                | `len!=2` → false       | architecture ausente ✅                                             |
| `"text->"`                  | `["text",""]`                     | `sides[1]==""` → false | architecture ausente ✅                                             |
| `"->text"`                  | `["","text"]`                     | `sides[0]==""` → false | architecture ausente ✅                                             |
| `""`                        | `[""]` (len 1)                    | `len!=2` → false       | architecture ausente ✅ (+ modality también ausente por guard `:994`) |
| `"text+image->video+audio"` | `["text+image","video+audio"]`    | pasa guard           | `["text","image"]`, `["video","audio"]` ✅                              |
| `"text->text->extra"`       | `["text","text","extra"]` (len 3) | `len!=2` → false       | architecture ausente ✅                                             |

### copy() de SupportedParameters (no aliasing)
`proxy.go:990-991`: `params := make([]string, len(md.SupportedParameters))` + `copy(params, md.SupportedParameters)` — slice independiente; el caller no puede mutar la config. ✅ Mismo patrón para `Thinking` levels (`:979-980`).

### top_provider empty-map guard
`proxy.go:1004`: `if cw > 0 || md.MaxOutput > 0` — el bloque `top_provider` solo se crea si al menos un campo será emitido. Imposible emitir `{}`. ✅ Verificado por el caso `ctxonly` del test P6.

## Observaciones positivas

1. **Fidelidad al spec:** La implementación es un mapeo casi literal de las postcondiciones a código. Cada guard del spec tiene un guard en el código en el mismo orden lógico.
2. **Tests verifican contrato, no implementación:** Todos los tests van vía `GET /v1/models` + deserialización JSON. Ningún test toca `splitModality` directamente ni depende de estructura interna.
3. **Anti-compile-error strategy en tests RED:** Los tests fueron escritos referenciando campos que no existían aún (strategy documentada en comentarios del archivo). Cuando el implementer agregó los campos, los tests pasaron sin modificación — evidencia de que el contrato estaba bien definido.
4. **Copy defensivo:** El implementer copió tanto `SupportedParameters` como `Thinking` antes de emitirlos, previniendo aliasing de memoria de config hacia la respuesta HTTP.
5. **C3 enmendado respetado:** El test P2 (línea 275-277) documenta explícitamente la enmienda de C3 y no exige ausencia de `top_provider`/`max_output_tokens` cuando las fuentes son no-cero.

---

**LISTO. Resumen: 1 bloqueante (C7 config.example.yaml), 0 opcionales de código, 2 nits/FYI.**

Revisión independiente — familia de modelo distinta al implementer (reviewer: qwen3.7-plus, implementer: deepseek-v4-flash).
