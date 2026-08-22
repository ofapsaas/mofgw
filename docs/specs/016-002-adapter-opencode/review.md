# Review — 016-002-adapter-opencode

**Reviewer:** CDAD reviewer (independent, anti-confirmation-bias) · **Stage:** GREEN (commit `d5dcaa1`) + post-review fixes · **Date:** 2026-08-22

## Verdict

**REQUEST_CHANGES inicial → RESUELTO.** El núcleo (P1-P11, invariantes I1-I4) era correcto y verde; la review encontró 1 hallazgo Importante (emisión de `limit` con valor 0) alcanzable desde el catálogo real, un gap de test de fallback, una rama `name` inerte y un FYI de higiene de registry. Todos resueltos en sesión post-review con secuencia TDD (tests primero, luego impl). Suite completa **659 tests/25 paquetes** verde.

## P1..P11 ↔ tests (contrato)

| P | Test(s) | Contrato | Resultado |
|---|---|---|---|
| P1 | `TestRED_RenderJsonValidoTopLevelMofgw_P1` | JSON válido, top-level "mofgw" | ✅ PASS |
| P2 | `TestRED_RenderBaseURLFielAlIR_P2` | options.baseURL == IR.BaseURL | ✅ PASS |
| P3 | `TestRED_RenderApiKeyEnvRefString_P3` | apiKey string "{env:MOFGW_KEY}", no sk- | ✅ PASS |
| P4 | `TestRED_RenderNpmYName_P4` | npm/name | ✅ PASS |
| P5 | `TestRED_RenderModelosYLimitsDPMeta_P5` | modelos + limit de Meta | ✅ PASS |
| P6 | `TestRED_RenderModelosSinIdsExtraneos_P6` | sin ids fuera de IR.Models | ✅ PASS |
| P7 | `TestRED_RenderModelosVacioObjetoVacio_P7` | Models==nil → models=={} | ✅ PASS |
| P8 | `TestRED_RenderModeloSinMetaSinLimit_P8` | sin limit sin metadata | ✅ PASS |
| P9 | `..._BaseURVVacioError_P9` / `_KeyEnvRefVacioError_P9` | error no-nil | ✅ PASS |
| P10 | `TestRED_RegisterLookupSupportedList_P10` | registry real | ✅ PASS |
| P11 | `TestRED_RenderDeterministaMismosBytes_P11` | determinista | ✅ PASS |

## Invariantes

- **I1 key solo env-ref:** ✅ `"{env:"+ir.KeyEnvRef+"}"`; grep "sk-" en impl → 0. Sin secret en el IR.
- **I2 modelos solo de IR.Models:** ✅ cero ids hardcodeados.
- **I3 JSON siempre válido:** ✅ json.Marshal + P1/P7.
- **I4 fiel a BaseURL:** ✅ grep "https?://" en impl → 0.

## Hallazgos

### Importante
- **#1 — `limit` emitía 0 cuando la key estaba presente con 0.** `metaInt` devolvía ok por presencia; catálogo real (modelCatalogEntry) puede emitir context_length/max_completion_tokens=0 → `limit:{context:0}`. Contradecía la fallback-rule ">0/ausente≠0". **RESUELTO:** `modelFragment` trata presente-pero-≤0 como ausente (guarda > 0). Tests: `TestRED_Review_ContextLengthCeroNoEmiteLimitContext`, `TestRED_Review_OutputCeroNoEmiteLimitOutput` (RED primero).

### Menor
- **#2 — trigger del fallback alpha sin probar.** `else if` (max_context_length/max_completion_tokens) existía pero ningún test ejercitaba el trigger. **RESUELTO:** tests con solo la key fallback → limit del fallback. (Ya pasaban; cierran la rama.)
- **#3 — rama `name` inerte/desalineada.** Impl leía Meta["name"], fixture usaba display_name, catálogo no provee ninguno. **RESUELTO:** se alinea a `Meta["display_name"]` (convención del fixture, plausible catálogo), con test emitido/omitido. Rama viva y consistente.

### FYI
- **#4 — resetOpenCodeRegistry borraba todo el registry global.** **RESUELTO:** acotado a delete solo "opencode" (futuros 003/004 no se pisan).

## Relevance audit

Cada test mapea a P o variante documentada; sin sobrantes; el adapter es serialización pura (sin I/O, sin mocks); P10 usa el registry real de clientconfig. Tests post-review (6 nuevos) son cobertura requerida por hallazgos, no relleno.

## Evidence

```
$ go test ./internal/clientconfig/opencode/... -count=1
Go test: 19 passed in 1 packages

$ go test ./... -count=1
Go test: 659 passed in 25 packages

$ go vet ./internal/clientconfig/... 
Go vet: No issues found
```

## Nota

La sintaxis `"{env:VAR}"` (template STRING, no objeto) para apiKey de opencode es correcta per research (https://opencode.ai/docs/config/); no es defecto.

---

**Status: APPROVE — findings resueltos.**