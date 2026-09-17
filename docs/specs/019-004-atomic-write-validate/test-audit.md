# test-audit.md — 019-004-atomic-write-validate

**Feature:** 019-004-atomic-write-validate · **Etapa:** 3.0 (AUDIT) · **Fecha:** 2026-09-17
**Fuente única:** `docs/specs/019-004-atomic-write-validate/spec.md` (P1-P16, I1-I6, C1-C16) + `docs/systemPatterns.md`.
**Evidencia de baseline (verificada empíricamente, no asumida):** `rtk go test -race -count=1 ./...` → **836 passed in 33 packages** (coincide con el baseline post-003 declarado); `rtk go test -race ./internal/config/... ./internal/catalogmerge/... ./internal/upstream/... ./internal/modelsdev/... ./internal/modelscache/...` → **172 passed in 5 packages**.

---

## 1. Resumen ejecutivo

- **Veredicto global: 0 tests existentes requieren modificación.** La feature es aditiva (paquete nuevo `internal/configsync`, API aditiva `config.ParseForValidation`, binario nuevo `cmd/mofgw-sync`). P13c congela la suite de `internal/config`; I3 congela 001/002/003. Ninguna expectativa de ningún test existente cambia bajo el spec.
- **21 tests nuevos (B1-B21)** mapean 1:1 a los 16 criterios C1-C16 y cubren las 16 postcondiciones P1-P16 (verificación exhaustiva en §3). Ningún test queda sin postcondición; ninguna postcondición queda sin test.
- **RED por compilación** (precedente 001/002/003, registrado en el spec): los tests de `internal/configsync` fallarán por `undefined: Serialize/Apply`, los de `internal/config` por `undefined: config.ParseForValidation`, los de `cmd/mofgw-sync` por paquete/binario inexistente. Desviación documentada del gate genérico "AssertionError", ya lockeada por el spec.
- **Inventario confirma el discovery:** 0 hits de `yaml.Marshal`/`yaml.Encoder` en todo el repo (única dep YAML `gopkg.in/yaml.v3 v3.0.1`); 0 tests referencian `configsync`, `mofgw-sync` o `ParseForValidation`; NO existe ningún directorio `testdata/` en el repo — `internal/configsync/testdata/` será el primero, y la convención vigente de fixtures es **inline** (constantes `validYAML` + `writeTemp` en `config_test.go`).
- **2 preguntas formales al orquestador** (§4): ubicación/inyección de `Apply` (tensión I1↔P8-P10) y mecanismo de "candidato roto forzado" de C10.

## 2. Inventario de suite existente + veredicto por test

### 2.1 Alcance del blast-radius

Áreas del spec: `internal/config` (ParseForValidation), `internal/configsync` (nuevo, 0 tests existentes), `cmd/mofgw-sync` (nuevo, 0 tests existentes), y suites de 001/002/003 (modelsdev/upstream/catalogmerge) prometidas intactas por I3. Todo lo demás (proxy e2e, subprocess, metrics, router…) está fuera del blast radius del spec (secciones "Fuera de alcance" + I3) y se valida implícitamente con el gate de suite completa.

### 2.2 Veredicto por paquete afectado

| Paquete | Archivos test | Tests (func Test) | Veredicto |
| --- | --- | --- | --- |
| `internal/config` | 13 (`config_test.go`, `clients_source_red_test.go`, `telemetry_red_test.go`, `metadata_red_test.go`, `clientconfig_conf_test.go`, `sticky_red_test.go`, `registry_red_test.go`, `external_review_fixes_test.go`, `inter_attempt_delay_red_test.go`, `opencode_session_red_test.go`, `context_analysis_test.go`, `sync_source_test.go`) | 56 | **UNTOUCHED** (P13c) |
| `internal/catalogmerge` (003) | 1 (`catalogmerge_test.go`) | 20 | **UNTOUCHED** (I3) |
| `internal/upstream` (002) | 1 (`upstream_test.go`) | 16 | **UNTOUCHED** (I3) |
| `internal/modelsdev` (001) | 1 (`modelsdev_test.go`) | 20 | **UNTOUCHED** (I3) |
| `internal/modelscache` | **0** (sin test file propio; su patrón atomic-write/digest se ejercita vía modelsdev/upstream) | 0 | **UNTOUCHED** (I3: cero cambios) |

**Lista explícita de tests untouched en el blast radius (112):**

- **`internal/config` (56, todos UNTOUCHED — expectativa P13c: suite intacta y verde):** `TestLoadValid`, `TestLoadMissingEnv`, `TestLoadInvalidYAML`, `TestLoadMissingProviderFields`, `TestLoadInvalidClientHash`, `TestLoadEmptyProviders`, `Test010001_P1_CargaConClavesNuevas`, `Test010001_P1_BackwardCompat`, `Test029001_DefaultFirstTokenTimeout`, `Test029001_ExplicitFirstTokenTimeout`, `Test029001_NegativeFirstTokenTimeoutRejected`, `Test029001_ProviderOverride`, `TestT_ConfigSubprocessValid`, `TestT_ConfigSubprocessNoKey`, `TestT_ConfigUnknownTypeError`, `TestT_ConfigUnknownBackendError`, `TestT_ConfigHTTPBackwardCompat`, `TestT_ConfigDefaultsInParse`, `TestSyncSourceKnob`, `Test017001_C1_InlineSinClientsFileErrorMigracion`, `Test017001_C2_ClientsFileCargaYValida`, `Test017001_C3_ClientsFileFailFast`, `Test017001_C4_InlineMasClientsFileDualSource`, `Test017001_C9_LoadClientsFileAceptaHashKey`, `TestContextAnalysisConfig_Defaults`, `TestContextAnalysisConfig_ValidateNegative`, `TestContextAnalysisConfig_ValidateZero`, `TestPostcondition1_StickyEnabledTrue`, `TestPostcondition1_StickyAusenteDefaults`, `TestPostcondition1_StickyNoBoolError`, `Test014001_P1_DefaultsRegistry`, `Test014001_P1_BlockValidoSeCarga`, `Test014001_P1_EnabledConFileVacio`, `TestExternalReview_ThinkingDefaultConThinkingVacio`, `TestExternalReview_MaxSessionsRetainedEnConfig`, `TestExternalReview_MaxSessionsRetainedDefault`, `TestExternalReview_MaxSessionsRetainedNegativo`, `TestExternalReview_PricingNaNInfRechazado`, `Test015001_P1_DefaultInterAttemptDelayZero`, `Test015001_P1_ExplicitInterAttemptDelay`, `Test015001_P1_NegativeInterAttemptDelayRejected`, `TestRED_ClientConfigDefaults_P7`, `TestRED_ClientConfig_ExplicitFields`, `TestRED_ClientConfigEmptyBlockOffSafe`, `TestRED_ClientConfig_KeyEnvRefNoSeResuelve`, `TestPostcondition8_DefaultsTelemetry`, `TestPostcondition8_BlockValidoSeCarga`, `TestPostcondition8_SinBloqueDefaults`, `TestPostcondition8_SampleRateInvalido`, `TestPostcondition8_EnabledConFileVacio`, `TestRED_LoadMetadataValida`, `TestRED_LoadSinMetadataVacia`, `TestRED_ThinkingDefaultFueraDeLista`, `TestRED_ContextWindowNegativo`, `TestPostcondition10_ConfigWiring`, `TestPostcondition10_ConfigDefaultFalse`.
- **`internal/catalogmerge` (20, todos UNTOUCHED — I3):** `TestSourceAssociation`, `TestMerge_ShortIDDirectMatch`, `TestMerge_OpenRouterVendorStrip`, `TestMerge_OpenRouterAliasResolution`, `TestMerge_OpenRouterAliasWithoutTarget`, `TestMerge_MissingIDSoftSkip`, `TestMirror_KnobWins`, `TestMirror_DefaultsBySource`, `TestMirror_AlphaFallback`, `TestMirror_NoCostWarning`, `TestMerge_PricingPassthrough`, `TestMerge_PricingTiersDiscardWarning`, `TestMerge_MetadataDerivation`, `TestMerge_ModalityFallback`, `TestMerge_ThinkingFromEffortValues`, `TestMerge_SupportedParametersPriority`, `TestMerge_Deterministic`, `TestMerge_FailSoftPerSource`, `TestMerge_NoSourcesError`, `TestMerge_SourcesUsed`.
- **`internal/upstream` (16, todos UNTOUCHED — I3):** `TestFetchZen_FaithfulList`, `TestFetchGo_FaithfulList`, `TestParseModelList_Tolerant`, `TestParseOpenRouterCatalog_PricingStrings`, `TestParseOpenRouterCatalog_FaithfulFields`, `TestParseOpenRouterCatalog_TolerantShape`, `TestFetchOpenRouter_AuthConditional`, `TestOpenRouter_KeyNeverLogged`, `TestUpstreamKnob_DisablesSources`, `TestUpstreamKnob_Isolation`, `TestDefaultCachePaths_PerSource`, `TestStores_Isolation`, `TestEngineLog_SourceAttribute`, `TestZenStore_Wiring`, `TestGoStore_Wiring`, `TestOpenRouterStore_Wiring`.
- **`internal/modelsdev` (20, todos UNTOUCHED — I3):** `TestGet_ServesCacheWithoutNetwork`, `TestGet_ServesStaleCache`, `TestFetch_CatalogRawAndUserAgent`, `TestFetch_Timeout`, `TestFetch_TimeoutBudget_NoRetry`, `TestFetch_RetryTransient`, `TestFetch_NoRetryOn4xx`, `TestRefresh_FreshSkips`, `TestRefresh_StaleRefetches`, `TestRefresh_Force`, `TestRefresh_SkipsIdenticalBody`, `TestRefresh_LockBusyFailsFast`, `TestRefresh_NoLockMode`, `TestRefresh_FailSoftWithCache`, `TestRefresh_FailLoudWithoutCache`, `TestFetchDisabled_Knob`, `TestParseCatalog_TolerantShape`, `TestParseCatalog_FaithfulFields`, `TestRefresh_AtomicWrite`, `TestLogEvents`.

### 2.3 Veredictos detallados (justificación de 0 mods)

- **`TestLoadValid` / `TestLoadMissingEnv`** (config_test.go:75, 120): congelan que `Parse` resuelve `APIKey` y falla nombrando la env ausente (líneas 519-570/802-827 del código actual). El spec P13a/P13c lockea que `Parse` queda **sin cambio de comportamiento**; la lógica compartida se extrae a helper privado → estas expectativas **no cambian**. UNTOUCHED.
- **`TestT_ConfigDefaultsInParse`** (config_test.go:556): congelará los defaults subprocess en Parse — P13a los hereda explícitamente en `ParseForValidation`. UNTOUCHED.
- **`TestExternalReview_PricingNaNInfRechazado`** (external_review_fixes_test.go:79): valida que `validate()` rechaza pricing NaN/Inf — dato que se reutiliza como mecanismo de inyección en C10 (§4-B). UNTOUCHED.
- **`TestMerge_*` (003)**: congelan el IR (`Plan.Providers` sorted por ProviderID, `Models` sorted+dedup, `Pricing/Metadata` keyed por ID de acceso, `ThinkingDefault` JAMÁS presente — catalogmerge_test.go:812). `configsync.Serialize` consume ese IR; ningún cambio en 003. UNTOUCHED.
- **`TestRefresh_AtomicWrite` / `TestRefresh_SkipsIdenticalBody`** (modelsdev_test.go:868, 511): son el precedente in-repo del patrón temp+rename+digest sidecar (store.go:145-202). No cubren `configsync` (código nuevo, prueba nueva). UNTOUCHED.
- **Resto de la suite (836 − 112 = 724 en 28 paquetes)**: sin relación con las áreas de la feature (I3 promete cero cambios en sus dependencias). UNTOUCHED implícito, verificado por el run completo del baseline.

### 2.4 Verificación de la premisa de discovery

- Serialización YAML existente: **0** (grep `yaml\.(Marshal|Encoder)` → solo menciones en el spec). Confirmado.
- Tests que referencien componentes nuevos: **0** (grep `configsync|mofgw-sync|ParseForValidation` en `*_test.go` → 0). Confirmado.
- `os.WriteFile` en tests: solo fixtures de setup (raw strings a `t.TempDir()` — config_test.go:56, modelsdev_test.go:166, etc.). Ningún test testa escritura YAML estructural. Confirmado.

## 3. Plan de tests nuevos (B1-B21) con mapeo postcondición

Convención de nombres: se adoptan los nombres LOCKEADOS por la tabla C1-C16 del spec. Cada test lleva en su doc-comment la(s) postcondición(es) numeradas que congela (identidad contractual). Ubicación por componente:

| Archivo | Paquete | Tests |
| --- | --- | --- |
| `internal/configsync/serialize_test.go` | `configsync` (in-package, convención del repo) | B1-B8, B10, B11 |
| `internal/configsync/apply_test.go` | `configsync` | B12-B15 |
| `internal/configsync/testdata/config-roundtrip.yaml` | — (fixture golden, §6) | materializa B8 |
| `internal/config/p13_parseforvalidation_test.go` | `config` (reusa `validYAML`/`writeTemp` del paquete) | B20, B21 |
| `cmd/mofgw-sync/main_test.go` | `main` (precedente `cmd/mofgw/*_red_test.go`) | B16-B19 |

**Contratos de firma definidos por el test-writer** (precedente in-repo: `newHTTPServer`/`buildTelemetryFromConfig` — "el implementer DEBE respetar esta firma"):

```go
// internal/configsync (nuevo):
func Serialize(raw []byte, plan catalogmerge.Plan) ([]byte, Report, error)
type Report struct {
    Applied  bool     // P8/P10
    Skipped  bool     // P10 (skip byte-idéntico)
    Digest   string   // P10 (sha256 hex del candidato)
    Warnings []string // P12 (passthrough fiel del Plan)
}
func Apply(raw []byte, plan catalogmerge.Plan, configPath string, fs FS) (Report, error) // ⚠ sujeta a §4-A
type FS interface { /* CreateTemp/Rename/Stat/ReadFile/WriteFile/Remove/Chmod mínimos */ } // ⚠ sujeta a §4-A

// cmd/mofgw-sync/main.go (nuevo):
func run(opts runOpts, logger *slog.Logger) int // exit codes 0/1/2 (P15)
type runOpts struct {
    ConfigPath string    // -config (precedencia idéntica a config.Load)
    NoFetch    bool      // --no-fetch (cache-only, P14)
    CachePaths cachePaths // inyección de <path> por fuente (modelsdev/zen/go/openrouter)
}
```

### B1 — `TestSerialize_Association` (C1, P1) — `internal/configsync/serialize_test.go`
- **Congela:** P1. Tabla: (a) asociación 1:1 completa OK (todo `ProviderPlan.ProviderID` machea exactamente un `providers[].id`); (b) provider en el YAML SIN plan correspondiente → error descriptivo; (c) `ProviderID` del plan sin nodo en el raw → error (violación de "machea exactamente uno", lectura bidireccional declarada en §4); (d) id duplicado en raw → error.
- **Fixture:** `config-roundtrip.yaml` (§6).
- **RED:** compilación — `undefined: Serialize` (paquete `configsync` no existe).

### B2 — `TestSerialize_InPlaceOrder` (C2, P2+D3) — `serialize_test.go`
- **Congela:** P2 (orden vivo) + D3. El fixture usa un orden de providers NO alfabético (`go-cuenta-1` → `auto-sin-match` → `sub-acc` → `zen-cuenta-1`); el `Plan.Providers` se construye en orden alfabético (determinismo del IR). Assert: el orden de `providers[]` en el candidato == orden del raw, NO el del plan. **Discriminante:** un serializer que itere el plan (orden alfabético) falla.
- **RED:** compilación.

### B3 — `TestSerialize_ProviderFieldsUntouched` (C2, P2) — `serialize_test.go`
- **Congela:** P2 ("todo otro campo byte-intacto"). Tabla sobre campos: `base_url`, `api_key_env`, `max_tokens`, `thinking_path`, `opencode_session`, `sync_source`/`sync_mirror` (knobs), `cooldown`/`timeout`/`retry`/`health`, y en el provider subprocess: `type`/`backend`/`command`/`clients`/`backend_flags`. Assert: tras reemplazar SOLO `models`, cada uno de esos scalars conserva su valor byte-intacto (comparación nodo a nodo del subdocumento del provider, excluyendo el nodo secuencia `models`).
- **RED:** compilación.

### B4 — `TestSerialize_DegradedProviderUntouched` (C3, P3) — `serialize_test.go`
- **Congela:** P3. Provider con `ProviderPlan.Models == nil` en un plan NO vacío (con warnings y otros providers tocados) → el nodo del provider es byte-idéntico al raw. **Discriminante:** si el serializer "tocara por las dudas", el byte-diff del subdocumento del provider falla. Un naive `models: null` (borrar models) rompería `validate()` ("al menos un model es obligatorio", D4) y el test lo congela.
- **RED:** compilación.

### B5 — `TestSerialize_PricingMetadataMerge` (C4, P4 a/b/d/e/f) — `serialize_test.go`
- **Congela:** P4(a-f), tabla de casos sobre `pricing` y `model_metadata`: (a) entry existente + campo derivable presente en plan → sobrescribe; (b) entry existente + campo derivable ausente (zero-value) en plan → preserva; (d) entry nueva (ID del plan sin entrada en raw) → agregada con SOLO campos provistos, **posición alfabética** dentro del mapa; (e) entry existente NO cubierta por plan → byte-intacta (valores exactos asertados); (f) NINGUNA key borrada: assert de conteo de keys antes/después + keys exactas preservadas.
- **Incluye C4 explícito:** keys por ID de acceso OR (`z-ai/glm-5.3-flash`) coexistiendo con keys cortas (`glm-5.2`), ambas en fixture y en plan.
- **RED:** compilación.

### B6 — `TestSerialize_ThinkingMergeBack` (C5, P4c) — `serialize_test.go`
- **Congela:** P4(c) + caso "minimax toggle" del criterio. Entry tocada con `thinking: ["low","high"]` + `thinking_default: "low"` en el raw; plan con `Metadata["glm-5.2"].Thinking = ["medium","max"]` → `thinking` sobrescribe, `thinking_default: "low"` PRESERVA (jamás derivable, 003 P9). Caso b: plan sin `Thinking` → preserva. **Discriminante:** solo un merge-back por campo implementa esto; un reemplazo de entry completa borra `thinking_default` → test rojo.
- **RED:** compilación.

### B7 — `TestSerialize_CommentsPreserved` (C6, P5 a/b/c) — `serialize_test.go`
- **Congela:** P5. (a) comentarios de nodos no tocados byte-idénticos al raw (Head/Line/Foot en document, sections, entries); (b) LineComment sobre la key de una entry de pricing cuyo scalar CAMBIA sobrevive; LineComment sobre `models` de un provider tocado sobrevive; (c) bloques de notas (HeadComments) dentro de `providers[i]` sobreviven aunque `models` cambie.
- **RED:** compilación. **Discriminante:** un `yaml.Marshal` de struct pierde TODO comentario → cualquier desvío es visible por bytes.

### B8 — `TestRoundTrip_Golden` (C7, P6+D8) — `serialize_test.go` + `testdata/config-roundtrip.yaml`
- **Congela:** P6 — `RoundTrip(fixture, planNoOp) == fixture` **byte a byte**, donde `planNoOp` se construye in-test desde el parse del fixture (Models == declarados, sin cambios pricing/metadata). Congela que server, fallback, registry, telemetry, context, embeddings, client_config, clients_file y cualquier sección futura pasan por el round-trip sin mutación semántica ni cosmética.
- **Golden self-consistente (D8):** flag `-update` del test materializa el golden vía el round-trip del propio serializer. Ver protocolo §6 y discriminante §5.
- **RED:** compilación (y ausencia del golden).

### B9 — `TestRoundTrip_LiveConfig` (C8, P16) — `serialize_test.go`
- **Congela:** P16 — canary HITL en entorno real. Lee `~/.config/mofgw/config.yaml` (vía `os.UserHomeDir()` en el TEST — el paquete puro jamás hace I/O, I1); round-trip sin mutación == archivo real byte a byte. `t.Skip` si no existe (nunca depende de `/home/ofap` para CI).
- **RED:** compilación.

### B10 — `TestSerialize_ValidatedCandidate` (C9, P7) — `serialize_test.go`
- **Congela:** P7. Fixture inline VÁLIDO sin `clients_file` (ver decisión §4-C — el golden fixture NO sirve aquí porque `ParseForValidation` abriría el clients file). Assert: `ParseForValidation(candidato)` exita 0-error; el Config refleja Models/Pricing/Metadata del plan; todo lo demás (server, fallback, providers fields, knobs) idéntico al Config del raw.
- **RED:** compilación — doble: `configsync` y `config.ParseForValidation` inexistentes.

### B11 — `TestSerialize_Deterministic` (C13, P11+D9) — `serialize_test.go`
- **Congela:** P11. (a) Dos llamadas `Serialize(raw, plan)` → `bytes.Equal`; (b) keys NUEVAS de mapas agregadas en posición alfabética (case (d) de P4 asertado por posición); (c) `Report.Warnings == plan.Warnings` verbatim (passthrough fiel de 003 — los warnings ya llegan sorted+dedup del IR; el serializer NO reordena; lectura de P11 declarada en §4).
- **RED:** compilación.

### B12 — `TestApply_AbortWithoutWrite` (C10, P8+D5) — `apply_test.go`
- **Congela:** P8. (a) raw YAML inválido en entrada → error descriptivo, SIN escribir config.yaml NI sidecar, config vigente byte-intacto; (b) candidato roto forzado (mecanismo sujeto a §4-B, propuesta: plan con `Models` non-nil vacía para un provider → `models: []` → `validate()` rechaza "al menos un model es obligatorio", congelado por `TestLoadMissingProviderFields` caso "models vacio") → abort sin escribir; error sube descriptivo.
- **RED:** compilación.

### B13 — `TestApply_AtomicWrite` (C11, P9+D6) — `apply_test.go`
- **Congela:** P9. (a) directorio del config read-only (`chmod 0o555` tras crear el config) → la creación del temp EN ESE directorio falla → error, config vigente byte-intacto (nunca truncado/parcial), sin sidecar huérfano. Este caso DISCRIMINA el "temp en el mismo directorio". (b) preservación de permisos: config pre-existente con mode `0o640` → tras write exitoso, `os.Stat` verifica mode `0o640`.
- **RED:** compilación.

### B14 — `TestApply_DigestSkip` (C12, P10) — `apply_test.go`
- **Congela:** P10 rama skip. Tabla:
  1. sidecar `config.yaml.sha256` == sha256(candidato) → NI config NI sidecar reescritos; `Applied=false, Skipped=true`; digest en el reporte. Assert de **mtime intacto** vía `os.Chtimes` pre-fijado + `os.Stat` post (discriminante de reloj, no de suspensión).
  2. **Discriminante de semántica P10:** sidecar == sha256(candidato) PERO config.yaml en disco fue modificado a mano (bytes ≠ candidato) → AUN ASÍ skip. Congela "el digest del sidecar solo compara contra el CANDIDATO actual, no contra el config vigente".
  3. sidecar ausente → escribe sidecar + config (reporte `Applied=true, Skipped=false`).
- **Nota §4-D:** el ORDEN sidecar-primero no es observable black-box (escritura síncrona in-proceso); el resultado final sí (B15).
- **RED:** compilación.

### B15 — `TestApply_SidecarAutoHeal` (C12, P10 auto-cura) — `apply_test.go`
- **Congela:** P10 rama mismatch/auto-cura. Config en disco contiene A (modificado a mano); sidecar contiene `sha256(B)` de un candidato previo; candidato actual C ≠ B → Apply escribe C (sobrescribe el hand-edit), sidecar actualizado a `sha256(C)`. **Discriminante espejo de B14-2:** config en disco == candidato C pero sidecar stale (`sha256(B)`, B≠C) → DEBE escribir (un impl que comparara contra el config vivo en lugar del sidecar skipearía erróneamente).
- **RED:** compilación.

### B16 — `TestRun_LogsWarnings` (C14, P12) — `cmd/mofgw-sync/main_test.go`
- **Congela:** P12. `run()` con logger inyectado (handler captor, patrón `slog.New(slog.NewJSONHandler(&buf,...))`): (a) cada `Plan.Warnings` (+ per-provider) logueado UNA vez (Info/Warn — nivel NO congelado, §4-E); (b) warnings logueados ANTES de escribir — proxy observable: con escritura abortada (dir read-only) los warnings YA están en el buffer, y con skip byte-idéntico también se loguean; (c) exit 0 con warnings presentes (fail-soft, salvo error de Merge → cubierto en B17/B19).
- **RED:** compilación — binario `cmd/mofgw-sync` inexistente.

### B17 — `TestRun_NoFetchCacheOnly` (C16, P14) — `main_test.go`
- **Congela:** P14. Con `--no-fetch`: caches seedeadas en `CachePaths` inyectados (helper `writeCache` + sidecar sha256, replicando el patrón de `modelsdev_test.go:166-176`) → fuentes usadas, sin red; cache de una fuente ausente → fail-soft (degradación + warning, escritura continúa); TODAS nil → error de Merge (003 P12d) → exit 1, sin escribir.
- **RED:** compilación.

### B18 — `TestRun_OnceCycle` (C16, P15+D1.3) — `main_test.go`
- **Congela:** P15 ciclo `--once`. Con config tmp válido + cache seedeada cuyo catálogo agrega un model nuevo: run → exit 0, config.yaml reescrito (models actualizado, comentarios intactos), sidecar escrito, reporte final con applied/digest/warnings/sources used; segunda corrida → skip (exit 0). **Setup completo con fixtures reales, sin mocks** (criterio E2E del flujo cross-componente). Es además el E2E de la sub-fase 3.5 (§4-G).
- **RED:** compilación.

### B19 — `TestRun_ExitCodes` (C16, P15) — `main_test.go`
- **Congela:** P15 mapa de exit codes. Tabla: **0** = escrito OK y skipped byte-idéntico (2 casos); **1** = config vigente inválido; flag `-config` a path inexistente (fail-loud I/O) → 1 con config no-creado; **2** = flag desconocido (parse de flags). La rama "validación del candidato falla → 1" queda cubierta a nivel `Apply` (B12) — alcance declarado en §4-F.
- **RED:** compilación.

### B20 — `TestParseForValidation_NoEnv` (C15, P13a) — `internal/config/p13_parseforvalidation_test.go`
- **Congela:** P13a. Raw válido SIN env vars resolvibles (nombres de env no seteados, patrón `TestLoadMissingEnv`): `Parse(raw)` falla nombrando la var; `ParseForValidation(raw)` exita con TODAS las `APIKey` vacías. Caso adicional: anti-dual-source (`clients:` inline + `clients_file`) → error también en `ParseForValidation` (es parte del "unmarshal + chequeo clients: inline" de P13a).
- **RED:** compilación — `undefined: ParseForValidation`.

### B21 — `TestParseForValidation_MatchesParse` (C15, P13b) — `p13_parseforvalidation_test.go`
- **Congela:** P13b. Con env SET (patrón `t.Setenv` del repo): `Parse(raw)` y `ParseForValidation(raw)` producen Configs `reflect.DeepEqual` tras zero-ear `APIKey` en ambos (uno resuelto, otro vacío). Tabla con raw: http clásico (`validYAML`), subprocess con defaults (`TestT_ConfigDefaultsInParse`), knobs heredados.
- **RED:** compilación.

### Cobertura completa (verificación del mapeo)

| Postcondición | Test(s) | | Postcondición | Test(s) |
| --- | --- | --- | --- | --- |
| P1 | B1 | | P9 | B13 |
| P2 | B2, B3 | | P10 | B14, B15 |
| P3 | B4 | | P11 | B11 |
| P4 | B5, B6 | | P12 | B16, B19 |
| P5 | B7 | | P13 | B20, B21 (+ gate suite intacta) |
| P6 | B8 | | P14 | B17 |
| P7 | B10 | | P15 | B18, B19 |
| P8 | B12 | | P16 | B9 |

16/16 postcondiciones cubiertas, 0 tests huérfanos. **I1-I6 no generan tests RED** (convención del framework: test sin postcondición sobra): I1/I2/I3 son invariantes arquitecturales verificables en Etapa 4 (imports de `configsync` permitidos solo `catalogmerge`/`config`/stdlib-yaml; único escritor de disco en el binario; suites intactas) + I3 queda además evidenciado por el gate de suite verde; I4 queda congelado por B5(e/f), B3, B4; I5 por B20/B21 (`APIKey` vacía en validación, keys jamás resueltas en el sync); I6 por B17/B12.

## 4. Benefit-of-doubt — resuelto / pendiente

### Resueltos (lectura inequívoca del spec, documentada para GREEN)

- **C. Golden fixture y `clients_file` (P6/D8):** el seed sintético INCLUYE la key `clients_file` (string inerte — el round-trip de `Serialize` nunca abre el archivo), pero los tests basados en `ParseForValidation` (B10, B20, B21) usan fixture inline SIN `clients_file`, porque `LoadClientsFile` haría fail-fast sobre un path inexistente. El round-trip de `clients_file` del mundo real queda cubierto por B9 (opt-in sobre el config vivo).
- **D. P10 auto-cura exacta:** resuelta por el texto lockeado — la comparación del sidecar es SOLO contra el sha256 del CANDIDATO (nunca contra el config vigente). Dos casos discriminantes lo congelan (B14-2, B15).
- **E. P15 exit codes:** mapa completo 0/1/2 testeable in-process vía `run()`. Flag desconocido → 2 (parse propio con `ContinueOnError`, no `ExitOnError` de stdlib). `-config` vs `--config`: idénticos para `flag` de Go. Path de config ausente en las 3 ubicaciones → 1 (fail-loud, categoría "I/O" del P15).
- **F. P12 warnings:** el serializer hace passthrough VERBATIM de `Plan.Warnings` (ya sorted+dedup por el IR de 003); "ordenados y dedup" de P11 describe la precondición del Plan, no una re-normalización del serializer. Nivel de log (Info vs Warn) NO se congelan — assert de presencia y unicidad.
- **G. B18 como E2E de la sub-fase 3.5:** `TestRun_OnceCycle` YA es el test E2E cross-componente del ciclo completo (criterio de aceptación C16). No se requieren E2E adicionales.

### Pendientes (2 preguntas formales al orquestador — no bloquean 13/21 tests)

1. **§4-A (PREGUNTA 1): ubicación de `Apply` (tensión I1 ↔ P8-P10).** Propuesta del test-writer (recomendada): `Apply` vive en `internal/configsync` pero recibe un filesystem INYECTADO (interfaz `FS` mínima definida en el paquete, implementación os-backed cableada por el binario) — I1 se respeta al pie de la letra ("no os.*" en el paquete), los tests C10-C12 viven en package `configsync` con wrapper FS real sobre `t.TempDir()` + wrappers de fallo (read-only, rename-error), y el "único que toca disco" se sostiene a nivel runtime. **Alternativa:** `Apply` en package `main` de `cmd/mofgw-sync` (I1 literal perfecto, pero separaría la lógica del paquete prometido en D1). Los tests B12-B15 se escriben idénticos bajo ambas — solo cambia el paquete del archivo. **Decisión requerida antes de RED** porque fija la API que el implementer debe respetar.
2. **§4-B (PREGUNTA 2): mecanismo de "candidato roto forzado" (C10).** Propuesta: plan con `ProviderPlan.Models` **non-nil vacía** para un provider → el candidato lleva `models: []` → `validate()` rechaza ("al menos un model es obligatorio" — congelado por `TestLoadMissingProviderFields`). Estado sintético que 003 nunca produce (Models nil = degradado es el caso real), pero es el oráculo determinista más limpio sin hooks.

## 5. Estrategia RED + discriminantes

### Naturaleza del RED (declarada por el spec, precedente del repo)

El spec lockea RED por compilación: `internal/configsync` inexistente → `undefined: configsync.*`; `config.ParseForValidation` inexistente → compilación falla en C15; `cmd/mofgw-sync` inexistente → build del binario falla (precedente 001/002/003). Desviación documentada del gate genérico "AssertionError", lockeada por el spec. **Verificación RED:** `go build ./internal/configsync/...` falla, `go test` de los paquetes afectados reporta build failure, `go build ./cmd/mofgw-sync` falla. Los tests NO pasan "por accidente": un test que no compila no puede reportar success.

### Discriminantes RED→GREEN (contra "pasa por accidente")

| Riesgo | Discriminante que solo un serializer real produce |
| --- | --- |
| Golden trivial (B8) | Fixture con comentarios a 4 niveles, flow+block style, quoted/unquoted: un `yaml.Marshal` de struct produce bytes SIN comentarios, reordenados y reindentados → falla por bytes. Fixture hand-authored ANTES de GREEN → oráculo independiente. |
| Orden del plan vs orden vivo (B2) | Fixture NO alfabético + Plan alfabético: iterar el plan produce otro orden → bytes distintos. |
| Degradado tocado por las dudas (B4) | Plan no vacío y subdocumento del degradado byte-idéntico. |
| Merge-back (B5/B6) | Entry manual con valores exactos preservados + `thinking_default` preservado en entry tocada: un reemplazo de entry completa o Marshal de struct pierde ambos. |
| Skip del digest (B14/B15) | Casos espejo: sidecar==candidato con config hand-editado → skip; config==candidato con sidecar stale → write. Solo la semántica "compara candidato vs sidecar" pasa ambos. |
| Atomicity real (B13) | Directorio read-only → fallo del temp EN ese dir; mode `0o640` preservado verificado por `os.Stat`. |
| Sin env (B20/B21) | `Parse` falla nombrando la var; `ParseForValidation` no la consulta jamás — un impl que reusara `Parse` completo falla. |
| Determinismo (B11) | Keys nuevas en posición alfabética exacta + dos corridas `bytes.Equal` con mapas Go: un impl con `range` sin sort falla intermitentemente. |

### Protocolo de materialización del golden (D8) — GUARDIA contra AP-4

El golden `testdata/config-roundtrip.yaml` es **self-consistente**: se commitea el output normalizado del round-trip. Secuencia pactada:
1. **RED (3.1):** el test-writer commitea el seed sintético hand-authored + el test con flag `-update` (fail por compilación — no se puede materializar sin serializer).
2. **GREEN (3.2), primera corrida:** si el round-trip produce diferencias COSMÉTICAS aceptadas por HITL (D2: indent/quoting normalizados), el test falla por bytes-diff y el implementer **NO puede tocarlo**. Reporta al orquestador → materialización del golden vía `go test ./internal/configsync -run TestRoundTrip_Golden -update` con **aprobación del usuario (HITL)** y commit del orquestador sobre el artefacto del test-writer. No es "ajustar el test para que pase" (AP-4): es el mecanismo D8 lockeado por el spec; la dif aceptada debe limitarse a normalización cosmética — cualquier pérdida de comentario/valor es un defecto real del serializer.
3. Los tests de postcondición estructural (B3, B5-B7) usan el fixture RAW (no el golden materializado), por lo que su RED/GREEN no depende del protocolo.

### Commits RED (uno por postcondición, en orden de dependencia)

1. `test: add failing test for postcondition 1` (B1) — `serialize_test.go` + seed `testdata/config-roundtrip.yaml`
2. B2/B3 → P2 · B4 → P3 · B5+B6 → P4 · B7 → P5 · B8+B9 → P6/P16 · B10 → P7 · B11 → P11 · B12 → P8 · B13 → P9 · B14+B15 → P10 · B20+B21 → P13 · B16 → P12 · B17 → P14 · B18+B19 → P15

## 6. Fixtures

### 6.1 Seed sintético `internal/configsync/testdata/config-roundtrip.yaml` (D8, LOCKEADA)

Composición mínima-exhaustiva (cada elemento existe para congelar una postcondición concreta):

```yaml
# HeadComment nivel documento (P5a) — nota de operación de la feature 019
server:                                  # LineComment nivel section
  addr: "127.0.0.1:3369"                 # scalar quoted (P6)
  max_body_bytes: 10485760               # int unquoted
  read_timeout: 120s                     # duration (D8)
  write_timeout: 300s
fallback:
  max_retries: 2
  cooldown: 60s
  cooldown_jitter: 5s
  timeout: 120s
  first_token_timeout: 75s               # override explícito (no default)
telemetry:                               # sección P6, block con bool (D8)
  enabled: true
  file: "telemetry.jsonl"
client_config:
  base_url: ""                           # empty-state válido (off-safe)
  key_env: MOFGW_KEY                     # scalar unquoted
clients_file: "config-roundtrip-clients.yaml"   # string INERTE (nunca abierta por round-trip; §4-C)
providers:                               # orden NO alfabético — congela D3/P2
  - id: go-cuenta-1                      # provider http con knobs completos
    base_url: "https://opencode.go/v1"
    api_key_env: "MOFGW_GO_1_KEY"
    models: ["glm-5.2", "deepseek-flash"]        # flow-style + LineComment
    max_tokens: 8192
    thinking_path: "reasoning"           # knob D8
    opencode_session: true               # knob D8, bool
    sync_source: "zen"                   # knob D8
    sync_mirror: "opencode-go"           # knob D8
  - id: auto-sin-match                   # base_url NO machea fuente → plan Models nil en runtime
    base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"   # quoted largo
    api_key_env: "MOFGW_DASH_KEY"
    # comentario intermedio DENTRO del provider (P5c)
    models:                              # block-style multi-línea
      - "glm-5.2"
      - "minimax-m3"
    max_tokens: 16384
  - id: sub-acc                          # provider subprocess (C2/D8)
    type: subprocess
    backend: claude
    command: "claude"
    backend_flags: ["--dangerously-skip-permissions"]
    clients: ["me"]
    session_dir: "/home/ofap/.local/share/mofgw/sessions"
    models: ["claude-sonnet-4"]
pricing:                                 # mezcla keys OR + cortas (C4)
  glm-5.2:                               # entry tocada por el plan (P4a/b)
    input: 1.4                           # LineComment sobre scalar que CAMBIA (P5b)
    output: 4.4
    cache_hit: 0.26
  minimax-m3:                            # entry tocada parcialmente (P4b: campo ausente preserva)
    input: 0.4
    output: 1.6
    thinking: ["low", "high"]            # niveles tocados por el plan (toggle, B6)
    thinking_default: "low"              # JAMÁS derivable — preserva SIEMPRE (P4c)
  z-ai/glm-5.3-flash:                    # key OR de acceso, NO cubierta por plan (P4e) + HeadComment
    # nota de verificación de precio (bloque de notas — P5a)
    input: 0.2
    output: 1.1
    cache_hit: 0.02
  qwen3.7-plus:                          # entry manual, byte-intacta (P4e)
    input: 0.4
    output: 1.6
model_metadata:
  glm-5.2:
    context_window: 200000
    max_output: 131072
    thinking_default: "low"              # tocada → preserva (P4c)
  z-ai/glm-5.3-flash:                    # entry con comentario, untouched (P5a)
    context_window: 204800
    max_output: 131072
  claude-sonnet-4:                       # entry manual sin comentario, intacta (P4e)
    context_window: 200000
    max_output: 64000
  # FootComment a nivel map (P5a)
```

Reglas del seed: scalars quoted/unquoted mezclados; flow vs block style en `models`; durations + booleans; knobs sync_source/sync_mirror; subprocess completo; entries con y sin comentario; keys OR + cortas coexistiendo; provider degradado-candidato (`auto-sin-match`) presente aunque su degradación la decide el PLAN en runtime (B4), no el fixture. IDs de providers en orden NO alfabético, deliberadamente distinto del orden alfabético del IR.

### 6.2 Golden self-consistente (D8)

- El archivo commiteado en `testdata/` **es** el golden: output normalizado del round-trip del propio serializer (self-consistente).
- Materialización: flag `-update` en `TestRoundTrip_Golden` (B8), primera corrida en GREEN con aprobación HITL (protocolo §5). El seed hand-authored de RED puede normalizarse cosméticamente en esa materialización — aprobado por HITL (D2).
- `TestRoundTrip_LiveConfig` (B9) corre contra el config vivo real si existe (`t.Skip` si no) — canary de D2 en el entorno real (726 líneas, ~250 de comentarios, `models` en flow-style).

### 6.3 Fixtures de tests de binario (B16-B19)

- Config mínimo válido inline (patrón `validYAML`/`subprocessYAML` del repo) con un provider http cuyo `models` difiere del catálogo cacheado → cycle aplicado.
- Caches seedeadas con helper `writeCache(t, dir, body)` + sidecar sha256 (replica `modelsdev_test.go:166-176`, permitido: es un test file). El formato exacto del body de cache se resuelve en RED leyendo los fixtures ya existentes de `modelsdev_test.go` (permitido: son tests).
- Directorios: `t.TempDir()` para config, sidecar y caches; NUNCA paths de `/home/ofap` ni `~/.config` reales en los tests deterministas (solo B9, opt-in skip).

## 7. Gate checklist (salida 3.0 → 3.1)

- [x] Suite baseline verde con `-race`: 836/33 (evidencia pegada §1).
- [x] Suites de áreas afectadas verdes: 172/5 (evidencia pegada §1).
- [x] Tests modificados: **0** — justificación: feature aditiva; P13c/I3 lockean las suites existentes (§2).
- [x] Tests untouched listados EXPLÍCITAMENTE: 112 en el blast radius (§2.2), resto por I3 + run completo.
- [x] Toda postcondición P1-P16 tiene ≥1 test (tabla §3).
- [x] Todo test nuevo mapea a postcondición(es) del spec — 0 tests por completitud.
- [x] Ningún test depende de estructura interna: asserts sobre contrato observable (bytes del candidato, `ParseForValidation`, archivo+sidecar+mtime+mode, exit codes, logs capturados); el único contrato de firma (`Report`, `run`, `Apply+FS`) sigue el precedente in-repo de helper-definido-por-test.
- [x] Benefit-of-doubt: 7 resueltos documentados, 2 preguntas formales al orquestador (§4-A y §4-B — ninguna bloquea B1-B11/B16-B21).
- [x] Riesgos de regresión identificados: (R1) extracción helper en `internal/config` → mitigado por suite intacta P13c como gate post-GREEN; (R2) golden cosmético → protocolo D8 con aprobación HITL (§5); (R3) `Parse` sin cambio de comportamiento → `TestLoadValid`/`TestLoadMissingEnv` intactos; (R4) suites 001/002/003 → CERO cambios en esos paquetes (I3), verificado por run completo post-GREEN; (R5) contrato de firmas nuevas (`Serialize`, `Apply`, `run`) → definido en §3, el implementer debe respetarlo.

---

**Resumen:**
- Tests a modificar: **0**
- Tests untouched: **836** (baseline completo, verde con `-race`; 112 listados explícitamente en el blast radius — §2)
- Tests nuevos: **21** (B1-B21, mapeo completo P1-P16 → C1-C16 — §3)
- Regression risks: **5** — extracción de helper en `internal/config` (gate P13c), materialización cosmética del golden (protocolo D8/HITL), contrato de firmas nuevas que el implementer debe respetar, suites 001/002/003 bajo I3 con CERO cambios, `TestRoundTrip_LiveConfig` opt-in nunca dependiente de `/home/ofap` para CI. (Detalle §7.)
