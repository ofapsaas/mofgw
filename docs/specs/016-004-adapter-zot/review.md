# Review — 016-004-adapter-zot

**Reviewer:** CDAD reviewer (independent, anti-confirmation-bias) · **Stage:** GREEN (commit `8ea10ff`) · **Date:** 2026-08-22

## Verdict: **APPROVE**

**0 bloqueantes, 0 importantes.** Último adapter del epic 016; replica el patrón review-cleaned de 002/003 con la desviación I1 deliberada (sin campo apiKey). Un único hallazgo informativo/opcional no-bloqueante.

## P1..P12 ↔ tests (contrato)

| P | Test(s) | Resultado |
|---|---|---|
| P1 | `TestRED_RenderJsonValidoTopLevelProviders_P1` | ✅ PASS |
| P2 | `TestRED_RenderBaseURLFielAlIR_P2` | ✅ PASS |
| P3 | `TestRED_RenderSinApiKeyNiTemplate_P3` | ✅ PASS |
| P4 | `TestRED_RenderApiOpenaiYProviderMofgw_P4` | ✅ PASS |
| P5 | `TestRED_RenderModelosYLimitsDesdeMeta_P5` | ✅ PASS |
| P6 | `TestRED_RenderModelosSinIdsExtraneos_P6` | ✅ PASS |
| P7 | `TestRED_RenderModelosVacioArrayVacio_P7` | ✅ PASS |
| P8 | `TestRED_RenderModeloSinMetaSoloID_P8` | ✅ PASS |
| P9 | `_BaseURLVacioError_P9` + `_KeyEnvRefVacioNoError_P9` | ✅ PASS (desviación I1) |
| P10 | `TestRED_RegisterLookupSupportedList_P10` | ✅ PASS |
| P11 | `TestRED_RenderDeterministaMismosBytes_P11` | ✅ PASS |
| P12 | 4 tests `_P12a/b/c/d` | ✅ PASS |

## Invariantes

- **I1 forma zot (sin secret):** NO campo apiKey, NO template ({env:}/${}), NO valor. `IR.KeyEnvRef` no se emite y vacío no es error (desviación deliberada vs 002/003). Env derivada `MOFGW_API_KEY` documentada. Grep `apikey|{env:|${` en impl → solo comentarios (L12/L15).
- **I2 modelos solo del IR:** iteración exclusiva de ir.Models; sin ids/límites inventados (P6).
- **I3 JSON válido:** json.Marshal puro; error pre-marshal → no JSON parcial.
- **I4 baseUrl fiel:** grep "https?://" en impl → 0.

## Verificaciones específicas

- `models[]` ARRAY shape correcto; orden espeja ir.Models para determinismo; vacío → [].
- Guard >0 + fallback (P12) correctos (reuso del approach review-cleaned de 003).
- display_name solo si no vacío (guard `ok && name != ""`).
- P10 registration + cleanup scoped (borra solo "zot", no destruye 002/003).
- Patrón fidelity vs openclaw: modelFragment/metaString/metaInt verbatim; divergencia solo en shape (`providers`, `api:"openai"`, sin apiKey). Sin colisión de helpers (paquetes separados).
- Hygiene: SPDX + package comment (documenta desviación I1).

## Findings

| Severidad | Hallazgo | Decisión |
|---|---|---|
| Informativo/Opcional | Test gap menor: `display_name` presente-pero-vacío (`""`) sin caso directo (behavior cubierto en P5 present-non-empty y P8 absent). El impl guarda `!= ""` verbatim de 003 (review-cleaned), riesgo nulo. | **No aplicado** (churn marginal, copia verbatim del patrón ya validado). |

## Relevance audit

Cada test mapea 1:1 a P1-P12; sin sobrantes; registry via API pública (sin mock); serialización pura. P3/P9 ejercitan la desviación diferencial vs 002/003.

## Evidence

```
$ go test ./internal/clientconfig/zot/... -count=1
Go test: 16 passed in 1 packages

$ go vet ./internal/clientconfig/zot/...
Go vet: No issues found

$ go test ./... -count=1
Go test: 691 passed in 27 packages
```

Delta vs baseline 675/26: +16 tests, +1 paquete (exactamente zot), cero regresión.

---

**Status: APPROVE**