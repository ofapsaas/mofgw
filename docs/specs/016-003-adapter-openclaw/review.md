# Review — 016-003-adapter-openclaw

**Reviewer:** CDAD reviewer (independent, anti-confirmation-bias) · **Stage:** GREEN (commit `8eb16e6`) · **Date:** 2026-08-22

## Verdict: **APPROVE**

El adapter `openclaw` replica fielmente el patrón review-cleaned de `opencode` (016-002) y lo adapta correctamente al shape de OpenClaw. **0 bloqueantes, 0 importantes.** Un único FYI no-bloqueante.

## P1..P12 ↔ tests (contrato)

| P | Test(s) | Resultado |
|---|---|---|
| P1 | `TestRED_RenderJsonValidoTopLevelModels_P1` | ✅ PASS |
| P2 | `TestRED_RenderBaseURLFielAlIR_P2` | ✅ PASS |
| P3 | `TestRED_RenderApiKeyEnvRefString_P3` | ✅ PASS |
| P4 | `TestRED_RenderApiYProviderMofgw_P4` | ✅ PASS |
| P5 | `TestRED_RenderModelosYLimitsDesdeMeta_P5` | ✅ PASS |
| P6 | `TestRED_RenderModelosSinIdsExtraneos_P6` | ✅ PASS |
| P7 | `TestRED_RenderModelosVacioArrayVacio_P7` | ✅ PASS |
| P8 | `TestRED_RenderModeloSinMetaSoloID_P8` | ✅ PASS |
| P9 | `..._BaseURVVacioError_P9` + `_KeyEnvRefVacioError_P9` | ✅ PASS |
| P10 | `TestRED_RegisterLookupSupportedList_P10` | ✅ PASS |
| P11 | `TestRED_RenderDeterministaMismosBytes_P11` | ✅ PASS |
| P12 | `_ContextLengthCeroNoEmiteContextWindow_P12a`, `_OutputCeroNoEmiteMaxTokens_P12c`, `_FallbackSoloMaxContextLength_P12b`, `_FallbackSoloMaxCompletionTokens_P12d` | ✅ PASS |

## Invariantes

- **I1 key solo env-ref:** `apiKey: "${" + ir.KeyEnvRef + "}"` (string template `${VAR}`), nunca literal ni SecretRef; grep "sk-" en impl → 0.
- **I2 modelos del catálogo:** array `models` desde `for _, m := range ir.Models`; cero ids hardcodeados.
- **I3 JSON válido:** json.Marshal; P1 valida.
- **I4 fiel a BaseURL:** `baseUrl: ir.BaseURL`; grep "https?://" en impl → 0.

## Verificaciones específicas

- `models[]` es ARRAY de objetos (opencode usa mapa keyed) — correcto per research. Orden preserva ir.Models para determinismo.
- P7 array vacío emite `[]` (no null).
- Guard `>0` + fallback-rule (lección de 002) aplicados en contextWindow y maxTokens; P12 escrito desde RED.
- `display_name` emitido solo si no vacío.
- P9 ambos casos vacíos → error.
- Registry cleanup scoped (borra solo "openclaw"), aplica lección de 002.
- Patrón fidelity vs opencode review-cleaned: divergencias intencionales y correctas (`${VAR}`, array, `baseUrl`/`contextWindow`/`maxTokens`/`api`).

## Findings

| Severidad | Hallazgo | Fix |
|---|---|---|
| FYI | `models.providers[].api` y key "mofgw" verificadas en P4; `models.mode=="merge"` NO tiene postcondición P ni test propio (el impl lo emite, L67-68) | Opcional: añadir aserción de `mode=="merge"` en un test. No bloquea (default de OpenClaw "merge" y el impl lo emite). |

## Relevance audit

Cada test mapea a P1..P12; sin sobrantes; sin mocks sobre plumbing (serialización pura, solo API pública). P12 = lección heredada de 002 escrita desde RED.

## Evidence

```
$ go test ./internal/clientconfig/openclaw/... -count=1
Go test: 16 passed in 1 packages

$ go vet ./internal/clientconfig/openclaw/...
Go vet: No issues found

$ go test ./... -count=1
Go test: 675 passed in 26 packages
```

---

**Status: APPROVE**