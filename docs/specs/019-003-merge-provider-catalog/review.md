---
feature: 019-003-merge-provider-catalog
veredicto: APPROVE
bloqueantes: 0
revisor: cdad-reviewer (worker round — deepseek-v4-flash, MISMA familia que implementer; disclosure anti-bias abajo)
fecha: 2026-09-12
spec: docs/specs/019-003-merge-provider-catalog/spec.md
red: b73694a
green: 0b9d9fc
---

# Review — 019-003 merge-provider-catalog

**Veredicto: APPROVE — 0 bloqueantes. 2 findings NO-bloqueantes requieren sign-off/documentedación explícita del HITL antes de merge (I7 enmienda y P2 claridad).**

## Evidencia (gate 3→4, verificada empíricamente — no asumida)

- HEAD = `0b9d9fc` `feat(mofgw): 019-003-merge-provider-catalog — GREEN`, padre inmediato = RED `b73694a`. Commits granulares RED→GREEN separados.
- `go build ./...` OK · `go vet ./...` limpio · `gofmt -l` limpio en los archivos del feature.
- `go test ./... -race` **836/33 paquetes verdes, 0 FAIL**.
- Suites gates 001 (modelsdev) 30/30 y 002 (upstream) 43/43 **verdes, sin regresión** (I7).

## Disclosure anti-bias (limitación del harness)

El reviewer corrió en la **misma familia de modelo (deepseek-v4-flash) que el implementer**. El invariante anti-bias "reviewer ≠ implementer en familia" NO está garantizado por modelo en este round. Mitigaciones estructurales sí presentes: sesión aislada, revisión sobre diff real + fixture, validación empírica `-race`, y auditoría test↔postcondición mecanizada. Precedente documentado en el proyecto (019-001 review: igual disclosura). Validación externa independiente (modelo/distinto) queda recomendada antes de deploy, siguiendo el precedente del proyecto.

## Capa 1 — Revisión de la spec

Spec formal CDAD completa: Descripción (D1-D9 lockeadas), Contrato P1-P14, Invariantes I1-I7, Criterios C1-C14 con mapeo test. Gate 2→3: spec aprobada + test-audit aprobado (0 mods, 17 nuevos B1-B13). Sin hallazgos de forma.

**S1 — Inconsistencia interna spec P9/P10 vs I7 (necesita enmienda).**
P9 exige derivar `Thinking` de `reasoning_options[effort].values` y P10 exige derivación de `structured_output`; pero `modelsdev.Model` NO exponía esos datos y **I7 lockea `modelsdev: sin cambios`**. La única forma de satisfacer P9/P10 es la extensión aditiva de modelsdev que el GREEN materializó (ver L2-I7). La spec es internamente inconsistente entre postcondición y invariante; requiere **enmienda de I7 vía HITL** declarando additive (zero-value) la extensión de modelsdev. NO bloquea (la implementación es correcta y aditiva), pero el texto del contrato debe reflejarlo.

**S2 — P2 "lista de acceso completa (zen/go)" ambigua (clarity, no defecto).**
El término "lista de acceso" es interpretable: (a) el set autorizado upstream (args `zen`/`goList` de `Merge`) o (b) el `models[]` declarado del provider en config.yaml. Los tests congelaron (b) (`prov.Models` como access list). Ver L2-P2. NO es defecto de implementación (coincide con los tests); es claridad de wording del spec que 019-004 debe resolver (¿la escritura debe hacer crecer el set a paridad upstream?). Recomendado: docs/añadir aclaración en P2 o en el spec de 004.

## Capa 2 — Revisión de la implementación (paquete puro `internal/catalogmerge`)

Paquete nuevo puro (solo `internal/config` tipos+knobs, `internal/modelsdev`, `internal/upstream`, stdlib — verificado, **cero imports prohibidos** I1, P13). Estructura correcta: `Merge` (orquestación + SourcesUsed + fail-soft + orden), `buildProvider`, `sourceForProvider`, `mirrorFor`, `lookupModel`, `buildMetadata`, `buildSupported`. Determinismo por construcción (`sortedDedup`, `sortedProviderIDs`). Sin mutación de entradas (I6). Sin `config.Load`, sin I/O, sin red (P13).

### Verificación por postcondición
- **P1 (asociación fuente)** ✅ knob gana (`sync_source != ""`); auto por `base_url` trim `/` y sufijo `/models` contra consts `upstream`; `subprocess`→modelsdev. TestSourceAssociation (7 casos) verde.
- **P2 (matching corto)** ✅ — ver finding L2-P2. `Models` = `sortedDedup(accessList)`; `accessList` = `prov.Models` (salvo openrouter). TestMerge_ShortIDDirectMatch verde.
- **P3 (strip vendor OR)** ✅ `orShortKey` strip vendor solo para lookup; `Models`/`Pricing`/`Metadata` keyed por ID de acceso (I2). Test verificado.
- **P4 (alias 2 saltos)** ✅ `resolveOpenRouterAccess` omite alias sin `alias_target` + warning; `orShortKey` usa `AliasTargetSlug` (hidratación D3 vía `internal/upstream` `AliasTargetSlug`, pointer→string tolerante). Tests alias-resolution/without-target verdes.
- **P5 (ausencia en models.dev)** ✅ ID ausente NO entra a Pricing/Metadata + warning, sin error; **ID permanece en `Models`** (interpretación B6/P2-I2, ver L2-P5). fail-soft.
- **P6 (espejo)** ✅ `mirrorFor` knob gana → defaults zen→opencode / go→opencode-go → `""`; `lookupModel` fallback al primer provider alfabético con cost + warning; sin cost → metadata sin pricing + warning. Tests knob-wins/defaults/alpha-fallback/no-cost verdes.
- **P7 (pricing passthrough)** ✅ passthrough 1:1 input/output/cache_read; `cache_write≠0` → warning de descarte; valor base queda. Test passthrough + tiers-discard verde (vía cache_write). Ver finding L2-P7.
- **P8 (metadata)** ✅ ContextWindow/MaxOutput (fallback `top_provider.max_completion_tokens`); Modality `Join("+")+"->"+Join("+")` (fallback `architecture.modality`; ausente→omitido). Tests verdes.
- **P9 (thinking)** ✅ `Thinking` = `ReasoningEffort` (extensión modelsdev); toggle/budget→sin levels; `ThinkingDefault` JAMÁS (siempre vacío). Test verificado.
- **P10 (supported_parameters)** ✅ OR→tal cual; derivación desde `StructuredOutput/Temperature/Reasoning/ToolCall` (orden fijo); nada→nil (omitir). Test 3 ramas verde.
- **P11 (determinismo)** ✅ 2 corridas `deep-equal`; Providers por ID, Models ordenado+dedup, Warnings ordenado+dedup. Test verde.
- **P12 (fail-soft)** ✅ fuente zen/go nil → `Models` degradado + warning, resto intacto; OR nil → sin SupportedParameters de OR + pricing por espejo; modelsdev nil → sin pricing/metadata + warning; todas nil → error descriptivo. `SourcesUsed` solo no-nil. Tests verdes.
- **P13 (pureza)** ✅ implícito por firma congelada + imports revisados.
- **P14 (config aditivo)** ✅ knobs parsean; `"bogus"`→error de carga claro; sin knobs→carga idéntica (deep-equal normalizado); `sync_mirror` free-string. TestSyncSourceKnob (5 subtests) verde.

### Findings detallados

**L2-I7 (Major, NO-bloqueante — requiere sign-off/documentación HITL): extensión aditiva de modelsdev vs I7 "sin cambios".**
GREEN agregó a `modelsdev.Model`: `StructuredOutput`, `Temperature`, `ReasoningEffort` + parse (`toModel`, `extractEffortValues`). Es una extensión `zero-value` (campos ausentes→false/nil; suite 001 verde 30/30, SIN cambios de comportamiento existing). REQUERIDA por POSTCONDICIONES LOCKED P9/P10 (test-audit B10/B11 la anticipan explícitamente como "extensión aditiva o enmienda I7 vía HITL"). La spec I7 solo auto-autorizó la extensión `upstream` (D3). **Acción:** HITL debe aceptar+documentar la enmienda de I7 (modelsdev: extensión aditiva zero-value permitida) O rechazar y re-planificar P9/P10. Por se aditiva-con-suite-verde, el reviewer la acepta; solo falta el sign-off formal del contrato.

**L2-P2 (Major, NO-bloqueante — claridad de intención, requiere confirmación HITL): uso de `prov.Models` como access list para zen/go.**
`Merge` recibe `zen *upstream.ModelList` y `goList`, pero en `buildProvider` solo se usan para el nil-check (P12a) y `SourcesUsed`; `Models` se deriva de `prov.Models` (config-declarado), no del set autorizado upstream. Coincide con el contrato CONGELADO por los tests (B3/Deterministic usan `prov.Models`), así que NO es defecto de implementación. Pero si la intención del epic ("catálogo upstream manda", corregir divergencia) es que la lista de modelos crezca a paridad upstream para zen/go, entonces 019-004 hereda un IR que NO amplía el set (solo enriquece pricing/metadata del subset declarado). **Acción:** HITL/architect confirma si Models(zen/go) = declarado-enriquecido (actual) o = unión-con-upstream (requeriría cambio en 003 + tests discriminantes nuevos). Recomendado: documentar en P2 (spec) que la paridad de ID set compete a 004.

**L2-P7 (Minor/advisory, NO-bloqueante): warning de descarte tiers/context_over_200k solo parcialmente satisfacible.**
`modelsdev.Cost` expone `input/output/cache_read/cache_write`; `tiers`/`context_over_200k` NO están tipados y encoding/json los descarta silenciosamente ANTES de llegar a catalogmerge. El warning P7 se dispara solo vía `cache_write≠0`. Si un modelo trae `tiers`/`context_over_200k` pero `cache_write==0`, no hay warning (los datos ya se perdieron en parse). El VALOR de comportamiento es correcto (base preservada, no se inventa tiering), pero el warning puntual por tiers/context_over_200k no es alcanzable sin extender modelsdev para tiparlos. **Recomendado:** aceptar como limitación documentada (el parseo tolerante de 001 ya ignora estos campos) o extender modelsdev aditivamente (mismo patrón I7) en 019-hardening.

**L2-P5 (reconciliación cerrada, NO-bloqueante): ID ausente permanece en `Models`.**
El test freeze (B6) interpreta D3(d) "omitir del plan" como omisión de pricing/metadata (P5) manteniendo el ID en `Models` (P2/I2: la lista de acceso manda). La implementación coincide. El reviewer **CIERRA** la reconciliación: P2/I2 (lock) dominan sobre el wording de D3(d); el ID ausente queda visible en `Models` con warning, dando observabilidad a 004 para decidir. Aceptado.

## Anti-fraude TDD (no auto-satisfacción)

- RED `b73694a` → GREEN `0b9d9fc` sin tocar tests (commit parent confirmado).
- Los 17 tests RED (16 catalogmerge + 1 config P14) pasan en GREEN con la implementación; no se relajó ningún assert para hacer verde.
- GREEN es el commit HEAD; suite entera 836/33 `-race` verde incluye los tests nuevos (gate suite).
- Suites 001/002 intactas (gates I7) — extensión modelsdev/upstream demostrada como no-regresiva.

## Auditoría test↔postcondición (mapeo C1-C14)

Toda postcondición P1-P14 tiene al menos un test (C1-C13); C13 pureza implícito por firma+imports; C14 = gate suite. Sin tests por completitud (cada bloque B1-B13 señala su P). Sin mocks sobre plumbing (paquete puro, fixtures inline).

## Hallazgos de severidad consolidados

| ID | Severidad | Tipo | Acción |
|---|---|---|---|
| S1/L2-I7 | No-bloqueante (Major) | Spec desviación (enmienda I7) | HITL: aceptar+documentar extensión aditiva modelsdev para P9/P10 |
| S2/L2-P2 | No-bloqueante (Major) | Spec claridad (P2 wording) | HITL/architect: confirmar si Models(zen/go)=declarado o =upstream; documentar en spec |
| L2-P7 | No-bloqueante (Minor/advisory) | Limite de detección | Aceptar limitación documentada o extender modelsdev en hardening |
| L2-P5 | Cerrado | Reconciliación | Aceptado (P2/I2 dominan; warning preserva observabilidad) |

## Conclusión

Implementación de alta calidad, determinística, pura, aditiva y no-regresiva; suite completa verde con evidencia. **APPROVE con 0 bloqueantes.** Los 2 findings S1 (I7 enmienda) y S2 (P2 claridad) requieren sign-off/documentedación explícita del HITL antes de merge, y el reviewer recomienda registro en `.cdad-state.json`. Próximo paso: merge + memory bank (etapa 5, cdad-scribe).

Status: Review **APPROVE** by cdad-reviewer on 2026-09-12