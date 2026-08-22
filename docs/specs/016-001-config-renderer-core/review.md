# Review — 016-001-config-renderer-core

**Reviewer:** CDAD reviewer (independent, anti-confirmation-bias) · **Stage:** GREEN (commit `3f28fd3`) · **Date:** 2026-08-22 · **Verdict: APPROVE**

## Resumen

El core del endpoint `GET /v1/client-config` + IR + registry de renderers implementa correctamente el contrato P1..P10; los invariantes I1..I4 se mantienen; suite completa verde (640 pass) y `go vet` limpio. **0 bloqueantes, 0 importantes.** Un hallazgo Menor de documentación (M1, prose spec §2.2) y 2 nits de limpieza.

## P1..P10 ↔ tests (contrato)

| P | Definición | Test(es) | Contrato |
|---|---|---|---|
| P1 | 401 sin auth, ruta bajo /v1 auth, no en publicPrefixes | `TestRED_ClientConfig401NoAuth_P1` | ✅ PASS |
| P2 | 400 sin `client`, `invalid_request_error` msg exacto | `TestRED_ClientConfig400NoClient_P2` | ✅ PASS |
| P3 | 404 cliente desconocido, msg con `supported:` + lista viva del registry | `TestRED_ClientConfig404UnknownClient_P3` | ✅ PASS |
| P4 | 503 base_url vacío, sin fallback, aun con renderer registrado | `TestRED_ClientConfig503NoBaseURL_P4` | ✅ PASS |
| P5 | 200, body == bytes de `Render(ir)` (path core vía `Register`) | `TestRED_ClientConfig200RegisteredRenderer_P5` | ✅ PASS |
| P6 | IR `Models` fiel al catálogo dedup de `/v1/models` (I2) | `TestRED_ClientConfigIRMatchesModelsAndKnobs_P6P7` | ✅ PASS |
| P7 | IR `BaseURL`/`KeyEnvRef` vivos (knob + default `MOFGW_KEY`) | `TestRED_ClientConfigIRMatchesModelsAndKnobs_P6P7`, `TestRED_ClientConfigDefaults_P7` | ✅ PASS |
| P8 | Key nunca en la respuesta; IR lleva `KeyEnvRef` no valor | `TestRED_ClientConfigNeverLeaksSecret_P8`, `TestRED_ConfigIRKeyEnvRefNuncaValor_P10`, `TestRED_ClientConfig_KeyEnvRefNoSeResuelve` | ✅ PASS |
| P9 | Envelope `openAIError` en errores; cero regresión `/v1/*`; `/v1/models` intacto | `TestRED_ClientConfigErrorEnvelopeAndNoRegression_P9` | ✅ PASS |
| P10 | Registry contrato público (`Register`/`SupportedList`/`Lookup` sobre mapa exportado) | `TestRED_RegisterSupportedListLookup_P10`, `TestRED_RenderersMapExportadoSePuebla_P10` | ✅ PASS |

## Checklist invariantes

- **I1 key nunca literal:** ✅ `ConfigIR.KeyEnvRef` es el único portador; sin resolución de env var en el path; grep de literales `sk-` en implementación sin resultado; ningún estado emite el valor.
- **I2 catálogo fuente de verdad viva:** ✅ `configCatalogModels()` reusa `modelCatalogEntry` (misma función, no copia) con idéntica iteración/`seen`-map que `handleModels`.
- **I3 aditivo cero regresión:** ✅ Única ruta nueva `GET /v1/client-config`; `publicPrefixes` intacto; ninguna ruta `/v1/*` alterada.
- **I4 base_url vacío → 503:** ✅ Check `base_url==""` → 503 sin fallback, antes de resolver renderer; `validate()` no falla con `BaseURL==""` (off-safe runtime).
- **P10 registry público:** ✅ mapa exportado mutable operado por Register/SupportedList/Lookup.
- **Ruta autenticada:** ✅ fuera de publicPrefixes → auth.Wrap → 401 invalid_api_key (emitido por Wrap, no el handler).
- **Seguridad:** ✅ output del renderer verbatim (aceptable); `client` interpolado en 404 va por `openAIError`→json (escapado, sin header-injection); cero llamadas salientes → sin SSRF; `ClientID` del contexto auth.
- **Relevance audit:** ✅ sin sobrantes; todos mapean a P1-P10 o a la postcondición de knob §2.3; sin mocks de plumbing (AP-14) — renderers de prueba vía API pública `Register`, harness real.

## Hallazgos

### Bloqueante — ninguno

### Importante — ninguno

### Menor
- **M1 — prose spec §2.2 lista 503-primero; el contrato runnable (y la impl) es 400-primero.** `TestRED_ClientConfig400NoClient_P2` golpea con client vacío Y base_url sin setear (harness default) esperando 400; P4 (base_url vacío + client `test`) pasa sólo con 400-antes-503. => orden correcto: 400 primero. **FIX aplicado:** prosa de spec §2.2 actualizada a 400-antes-503 (coherente con `handleClientConfig`). No bloqueaba (consistencia documento↔código).

### Nit
- **N1** `Content-Type: application/json` seteado dos veces en `handleClientConfig` (L46 y L85). Redundante, inofensivo. **Decisión orquestador: no aplicado** (inofensivo; evita churn post-aprobación).
- **N2** `configCatalogModels` duplica el bucle de iteración/`seen` de `handleModels` (~10 líneas). No viola I2 (la lógica de catálogo no se duplica). **Decisión orquestador: no aplicado** (FYI de refactor futuro; describe el patrón del spec §2.2).

### FYI
- Tests `TestRED_*` son permanentes de GREEN — convención heredada del pipeline, consistente con el repo.
- `Content-Type` quedará `application/json` para 200 aunque un futuro adapter quiera otro; el spec lo admite por convención. Los adapters 016-002/003/004 podrían ajustarlo.

## Evidence

```
$ go test ./... -count=1
Go test: 640 passed in 24 packages

$ go vet ./...
Go vet: No issues found
exit=0
```

Baseline pre-feature: 330 pass (2 paquetes) → GREEN: 640 pass (24 paquetes), cero regresión.

---

**Status: APPROVE — no se requieren cambios bloqueantes.** Fix M1 (prose spec §2.2) aplicado por el orquestador; nits N1/N2 no aplicados (decisión de scope).