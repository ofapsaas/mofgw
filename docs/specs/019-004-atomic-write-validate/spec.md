---
id: 019-004-atomic-write-validate
title: Write atómico de config.yaml — edición estructural Node + validación pre-commit + binario cmd/mofgw-sync
status: draft
epic: 019-provider-sync-automation
date: 2026-09-16
created: 2026-09-16
motivacion_externa: "Epic 019: 003 produce el IR (Plan) pero nadie materializa config.yaml; el workflow manual sigue siendo la única vía de escritura. 004 cierra el ciclo: IR → YAML editado estructuralmente (comentarios preservados) → validado → escrito atómicamente, con skip byte-idéntico para no reloadear en vano."
---

# 019-004 — Atomic write-validate: Plan → config.yaml editado estructuralmente

## Descripción

- **D1 — Componentes (LOCKEADA).** Tres piezas:
  1. Paquete puro **`internal/configsync`**: recibe el YAML crudo de config.yaml (`[]byte`) + el `Plan` de `catalogmerge` y produce el candidato serializado (`[]byte`, edición estructural `yaml.Node`) + reporte (`Applied bool`, `Warnings []string`). **Sin I/O, sin red, sin log** — todo inyectado (D9). Imports permitidos: `internal/catalogmerge`, `internal/config`, stdlib (`gopkg.in/yaml.v3`). JAMÁS `proxy`/`router`/`cmd`/`modelscache`.
  2. API aditiva en **`internal/config`**: `ParseForValidation(raw []byte) (*Config, error)` — idéntica a `Parse` (519-570) EXCEPTO `resolveKeys` (802-827): no exige env vars. Cambio estilo P14: `Parse` queda sin cambio de comportamiento; la lógica compartida se extrae a helper privado; suite de `internal/config` intacta y verde.
  3. Binario **`cmd/mofgw-sync/main.go`**: adapter fino que orquesta fetch (o cache) → `catalogmerge.Merge` → `configsync.Serialize` → `config.ParseForValidation` → atomic write + digest skip. Flags: `-config <path>` (precedencia idéntica a `config.Load`: flag > `~/.config/mofgw/config.yaml` > `/etc/mofgw/config.yaml`; escribe SIEMPRE el path que leyó), `--no-fetch` (cache-only), `--once` (ciclo completo; único modo de 004 — el timer/scheduling es 006).
- **D2 — Edición estructural yaml.Node (LOCKEADA, decisión HITL 2).** El candidato se produce decodificando el YAML vivo a `yaml.Node` y mutando ÚNICAMENTE los nodos alcanzables por el Plan: `providers[i].models` (reemplazo del nodo secuencia in-place) y entries de los mapas `pricing`/`model_metadata` (merge de scalars). Round-trip Node preserva comentarios (`HeadComment`/`LineComment`/`FootComment`). Normalización cosmética de estilo (indent/quoting) es aceptada por HITL; el contrato byte-fiel se define sobre el fixture normalizado (D8). NO se usa `yaml.Marshal` de struct (perdería comentarios).
- **D3 — Orden in-place (LOCKEADA, decisión HITL 1).** Los providers se recorren en el ORDEN del documento YAML vivo (que ES la cadena de fallback); el orden alfabético de `Plan.Providers` (catalogmerge.go:67-69) es determinismo del IR y NUNCA se traslada al archivo. Cada provider se localiza por match exacto de su `id` contra `ProviderPlan.ProviderID` y se edita in-place.
- **D4 — Merge-back de lo no derivable (LOCKEADA, decisión HITL 4).** Semántica por campo en entries de `pricing`/`model_metadata` TOCADAS: campo presente (non-zero) en el plan → sobrescribe; campo ausente/zero-value en el plan → PRESERVA el valor existente; `thinking_default` (jamás derivable, 003 P9) → PRESERVA siempre. Entries NO cubiertas por ningún plan (modelos manuales, claude-*, big-pickle, etc.): byte-inaltouchables. Keys existentes de pricing/metadata NUNCA se borran. Providers con `ProviderPlan.Models == nil` (fuente de acceso ausente, 003 P12a): el provider NO se toca (escribir nil borraría models y rompería `validate()`: "al menos un model es obligatorio").
- **D5 — Validación pre-commit fail-loud (LOCKEADA, decisión HITL 3).** El candidato se valida con `config.ParseForValidation` ANTES de cualquier escritura. Falla la validación → abort SIN escribir config.yaml NI sidecar (contrato epic: "005 solo corre si 004 validó; falla de validación = abort sin escribir").
- **D6 — Atomic write + digest skip (LOCKEADA, decisión HITL 5).** Escritura: temp file en el MISMO directorio + `fsync` + `os.Rename` sobre config.yaml (patrón `writeFileAtomic` de modelscache/store.go:202, reimplementado localmente — `writeFileAtomic` es unexported y modelscache no se importa). Skip byte-idéntico: digest sha256 hex del candidato vs sidecar `config.yaml.sha256` (junto al config) → idénticos → NI config NI sidecar se reescriben (mtime intacto → 005 no reloadée). Digest mismatch → reescribe sidecar PRIMERO y body después (auto-cura, patrón modelscache persist 145-175).
- **D7 — Dedupe de providers gemelos: FUERA de alcance (LOCKEADA, cierre del gap del discovery).** Los 8 providers go-* comparten base_url y lista pero NO identidad (keys/quotas distintas — son cuentas separadas, no redundancia). El dedupe automático cambiaría la cadena de fallback sin decisión operativa explícita → explícitamente fuera de 004; si algún día aplica, es feature propia con aprobación HITL.
- **D8 — Estrategia de fixture golden (LOCKEADA).** El config vivo NO está en el repo. Fixture: `internal/configsync/testdata/config-roundtrip.yaml` — seed SINTÉTICO exhaustivo (comentarios a todos los niveles: documento/section/key/line; flow-style `models: ["a","b"]`; block-style; scalars quoted/unquoted; durations; booleans; provider subprocess; knobs sync_source/sync_mirror; clients_file; entries pricing/metadata con y sin comentario). El golden se MATERIALIZA vía el propio round-trip del serializer (self-consistente): se commitea el output normalizado y el test aserta `RoundTrip(fixture) == fixture`. Test opt-in `TestRoundTrip_LiveConfig`: corre contra `~/.config/mofgw/config.yaml` real si existe, `t.Skip` si no (nunca depende de /home/ofap para CI).
- **D9 — Determinismo byte a byte (LOCKEADA).** Dos corridas con iguales entradas → bytes idénticos: keys nuevas de mapas en orden alfabético, iteración de mapas siempre sortada (herencia 003 P11), sin `range` de mapa sin sort en el serializer.

## Contrato (postcondiciones)

- **P1 — Asociación IR→YAML 1:1.** `Serialize(raw, plan)` recibe un Plan construido de LA MISMA lectura del raw (el binario lee config.yaml UNA vez: mismo `[]byte` alimenta `Parse`→providers→`Merge` y la edición). Todo `ProviderPlan.ProviderID` machea exactamente un nodo `providers[].id` del raw; provider en el YAML sin plan correspondiente → error descriptivo (invariante por construcción, test la congela).
- **P2 — Edición in-place con orden vivo.** El orden de los providers en el candidato es IDÉNTICO al del raw. Solo el nodo `models` de cada provider macheado se reemplaza (por `Plan.Models`, ordenado+dedup según 003); TODO otro campo del provider (base_url, api_key_env, max_tokens, thinking_path, opencode_session, sync_source/sync_mirror, cooldown/timeout/retry/health, type/backend/command/clients/backend_flags) queda byte-intacto.
- **P3 — Provider degradado no tocado.** `ProviderPlan.Models == nil` → el nodo del provider queda byte-idéntico al del raw (ni models ni nada).
- **P4 — Merge de pricing/model_metadata.** Por entry keyed por ID de acceso (el plan ya resuelve strip de vendor OR, 003 P3): (a) entry existente + campo derivable presente en el plan → sobrescribe ese campo; (b) entry existente + campo derivable ausente en el plan → preserva; (c) `thinking_default` existente → preserva SIEMPRE (aunque la entry sea tocada); (d) entry nueva (ID del plan sin entrada en el raw) → se agrega con SOLO los campos que el plan provee, en posición alfabética dentro del mapa; (e) entry existente NO cubierta por ningún plan → byte-intacta; (f) NINGUNA key se borra jamás.
- **P5 — Preservación de comentarios.** En el candidato: `HeadComment`/`LineComment`/`FootComment` de (a) nodos no tocados: byte-idénticos al raw; (b) nodos tocados: los comentarios asociados al mapping-key del provider y de las entries de pricing/metadata sobreviven (un comentario de línea sobre una key cuyo scalar cambia, persiste); (c) comentarios dentro de `providers[i]` (bloques de notas por provider) sobreviven aunque `models` cambie.
- **P6 — Secciones no tocadas byte-fiel (golden).** `RoundTrip(fixture, planNoOp)` == fixture byte a byte (plan no-op: Models idénticos a los declarados, sin cambios en pricing/metadata). Esto congela que server, fallback, registry, telemetry, context, embeddings, client_config, clients_file y cualquier sección futura pasan por el round-trip SIN mutación semántica ni cosmética.
- **P7 — Validación del candidato.** Tras serializar, `config.ParseForValidation(candidato)` debe exitar 0-error; el Config resultante refleja los cambios del plan (Models/Pricing/Metadata actualizados) y todo lo demás idéntico al Config del raw vigente.
- **P8 — Abort sin escribir (fail-loud).** Si `ParseForValidation` del candidato devuelve error → `Serialize`/`Apply` NO escribe config.yaml NI sidecar; el error sube descriptivo; el config vigente queda byte-intacto. Cubre también: YAML inválido en entrada (error, sin escribir).
- **P9 — Escritura atómica.** Escritura = temp en el mismo directorio del config + fsync + rename. Si el rename o la escritura del temp fallan, el config.yaml vigente queda byte-intacto (nunca truncado/parcial). Permisos del config se preservan (mode del archivo vigente).
- **P10 — Skip byte-idéntico.** sha256(candidato) == contenido del sidecar `<config>.sha256` (hex 64) → NO se escribe config NI sidecar; resultado `Applied=false, Skipped=true` con el digest en el reporte. Sidecar ausente/mismatch → escribe sidecar (sidecar-primero) y config. Sidecar presente pero config.yaml modificado a mano (digest no corresponde a NINGÚN output previo) → auto-cura: se escribe el candidato nuevo (el digest del sidecar solo compara contra el CANDIDATO actual, no contra el config vigente).
- **P11 — Determinismo.** Dos llamadas `Serialize(raw, plan)` → `bytes.Equal` true. Keys nuevas agregadas en orden alfabético; warnings del reporte = los del Plan, ordenados y dedup (passthrough fiel de 003, sin reordenar).
- **P12 — Warnings del Plan.** Todos los `Plan.Warnings` (+ per-provider) se retornan en el resultado y el binario los loguea (slog Info/Warn, uno por warning) ANTES de escribir. No afectan el exit code (fail-soft de 003), salvo error de Merge (todas las fuentes nil, 003 P12d) → exit 1 sin escribir.
- **P13 — API aditiva config (ParseForValidation).** (a) `ParseForValidation(raw)` = unmarshal + chequeo `clients:` inline + `clients_file` + `validate()` + defaults subprocess — SIN `resolveKeys` (no toca env). (b) Para un raw válido, `ParseForValidation(raw)` y `Parse(raw)` producen Configs idénticos salvo `APIKey` (vacía vs resuelta). (c) `Parse(raw)` sin cambio de comportamiento; suite `internal/config` intacta y verde.
- **P14 — `--no-fetch`.** Cada fuente se lee de su Store.Get() (`modelsdev.NewStore`/`upstream.NewZenStore`/`NewGoStore`/`NewOpenRouterStore` con sus Default*CachePath); cache ausente → fuente nil → fail-soft de 003 (degradación + warning); TODAS nil → error de Merge → exit 1, sin escribir. Con `--no-fetch` NO hay red (los Stores se construyen con el fetch deshabilitado o se usa Get puro).
- **P15 — Ciclo `--once` y exit codes.** `--once` (y default sin flag): load raw → Parse (fail-loud si el config VIGENTE es inválido) → fetch/merge → Serialize → ParseForValidation → skip/write → reporte final (applied/skipped, digest, warnings, sources used). Exit codes: **0** = escrito OK o skipped byte-idéntico; **1** = error fail-loud (config vigente inválido, validación del candidato falla, Merge sin fuentes, I/O, YAML inválido); **2** = flags/uso inválido. En CUALQUIER exit 1: config.yaml byte-intacto.
- **P16 — Round-trip del config vivo (opt-in).** `TestRoundTrip_LiveConfig`: round-trip sin mutación del config real == config real byte a byte (si yaml.v3 normalizara algo cosmético, el test lo DETECTA — es el canary de la decisión HITL 2 en el entorno real). Skip silencioso si el archivo no existe.

## Invariantes

- **I1 — Pureza de `internal/configsync`.** Sin I/O (no `os.*`), sin red, sin log, sin reloj. `raw []byte` + `Plan` entran, `[]byte` + reporte salen. El binario `cmd/mofgw-sync` es el ÚNICO que toca disco (lee config, escribe temp/rename/sidecar) y loguea.
- **I2 — Único escritor de config.yaml en el sync.** En todo el ciclo 004, solo `configsync` (via el binario) escribe config.yaml y su sidecar `.sha256`. El servidor mofgw JAMÁS escribe config.yaml (contrato epic cross-feature). El binario tampoco toca `clients.yaml` ni ningún otro archivo de config.
- **I3 — Cambios a features previas mínimos y aditivos.** `internal/config`: SOLO `ParseForValidation` + extracción privada compartida con `Parse` (cero cambio de comportamiento). `internal/catalogmerge`, `modelsdev`, `upstream`, `modelscache`: CERO cambios. Suites de 001/002/003 intactas y verdes (gates).
- **I4 — Preservar, nunca inventar ni borrar.** Lo no derivable se preserva (D4); lo no cubierto por el plan queda intocado; no se agregan providers ni models fuera del plan (hereda I2 de 003); ninguna key de pricing/metadata se elimina (P4f).
- **I5 — Sin secrets.** El candidato contiene solo referencias `api_key_env`; `APIKey` es `yaml:"-"`; `ParseForValidation` no lee env → el binario nunca resuelve keys para escribir (las keys jamás pasan por el sync).
- **I6 — Fail-soft de fuentes, fail-loud de datos.** Herencia 003: fuente ausente → degradación + warning; dato ilegal o validación fallida → error y abort sin escribir.

## Criterios de aceptación (con mapeo test)

| #   | Criterio                                                                                                                                  | Test                                                              |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| C1  | P1: asociación 1:1 por ProviderID; provider sin plan → error                                                                              | `TestSerialize_Association`                                         |
| C2  | P2: orden vivo preservado; solo `models` cambia; resto del provider byte-intacto (fixture con knobs y subprocess)                           | `TestSerialize_InPlaceOrder`, `TestSerialize_ProviderFieldsUntouched` |
| C3  | P3: provider degradado (Models nil) → byte-idéntico                                                                                       | `TestSerialize_DegradedProviderUntouched`                           |
| C4  | P4: (a)-(f) del merge de pricing/metadata, incl. keys por ID de acceso OR (`z-ai/glm-5.3-flash`) y preservación de keys cortas coexistentes | `TestSerialize_PricingMetadataMerge` (tabla de casos)               |
| C5  | P4c: thinking_default preservado en entries tocadas; niveles thinking: presente→sobrescribe, ausente→preserva (caso minimax toggle)       | `TestSerialize_ThinkingMergeBack`                                   |
| C6  | P5: comentarios Head/Line/Foot intactos en nodos no tocados y sobreviven en tocados                                                       | `TestSerialize_CommentsPreserved`                                   |
| C7  | P6: golden byte-fiel — RoundTrip(fixture, planNoOp) == fixture                                                                            | `TestRoundTrip_Golden`                                              |
| C8  | P16: round-trip del config vivo real (opt-in skip)                                                                                        | `TestRoundTrip_LiveConfig`                                          |
| C9  | P7: ParseForValidation del candidato refleja el plan; resto idéntico                                                                      | `TestSerialize_ValidatedCandidate`                                  |
| C10 | P8: validación falla (candidato roto forzado) → sin escribir, config intacto, error descriptivo                                           | `TestApply_AbortWithoutWrite`                                       |
| C11 | P9: escritura atómica — fallo de rename/permiso deja config byte-intacto; mode preservado                                                 | `TestApply_AtomicWrite`                                             |
| C12 | P10: skip byte-idéntico (mtime/sidecar intactos); mismatch → sidecar-primero + auto-cura                                                  | `TestApply_DigestSkip`, `TestApply_SidecarAutoHeal`                   |
| C13 | P11: dos corridas → bytes idénticos; keys nuevas alfabéticas                                                                              | `TestSerialize_Deterministic`                                       |
| C14 | P12: warnings passthrough + logueados por el binario (capturando el slog handler)                                                         | `TestRun_LogsWarnings`                                              |
| C15 | P13: ParseForValidation sin env; Config idéntico a Parse salvo APIKey; suite config verde (gate)                                          | `TestParseForValidation_NoEnv`, `TestParseForValidation_MatchesParse` |
| C16 | P14/P15: --no-fetch cache-only (todas-nil → exit 1 sin escribir); --once ciclo completo; exit codes 0/1/2                                 | `TestRun_NoFetchCacheOnly`, `TestRun_OnceCycle`, `TestRun_ExitCodes`    |

**Naturaleza del RED:** `internal/configsync` no existe → tests de C1-C8/C13 fallan por compilación (`undefined: configsync.*`); `config.ParseForValidation` no existe → compilación falla en C15; `cmd/mofgw-sync` no existe → C14/C16 fallan por compilación/build del binario (precedente RED-de-compilación de 001/002/003). Fixtures: seed sintético + golden materializado en `internal/configsync/testdata/` (D8).

## Fuera de alcance

- **019-005/006/007:** señalización de reload/restart y verificación `GET /v1/models`; unidad systemd timer + `OnBootSec`; snapshot embebido de build. 004 NO coordinación con 005: solo garantiza "escrito y validado" o "abort sin escribir".
- **clients_file / clients.yaml:** jamás leídos ni escritos por 004 (el path viaja dentro del config, `Parse` lo valida como siempre).
- **Dedupe de providers gemelos** (D7): explícitamente fuera.
- **Borrado de keys huérfanas** en pricing/model_metadata (P4f): nunca.
- **`config.example.yaml`:** su actualización documental no es código de esta feature (queda para la documentación del merge).
- **Cambios en** `internal/proxy`, `internal/router`, `cmd/mofgw`, `catalogmerge`, `modelsdev`, `upstream`, `modelscache` (I3).

## Contexto técnico verificado (Discovery 2026-09-16)

- `config.Parse` (config.go:519-570): unmarshal → anti-dual-source `clients:` (528-539) → `LoadClientsFile` (606-619) → `validate()` (634-800) → defaults subprocess (554-565) → `resolveKeys` (802-827, ÚNICO paso dependiente de env) — `ParseForValidation` = Parse sin ese último paso.
- `Plan` de 003 (catalogmerge.go:22-36): `Providers` sorted por ProviderID (67-69); `Models` = lista de acceso declarada (sorted+dedup); `Pricing`/`Metadata` keyed por ID de acceso (strip vendor OR ya resuelto, P3 de 003); `ThinkingDefault` JAMÁS presente (P9 de 003).
- Config vivo (`~/.config/mofgw/config.yaml`, 726 líneas, ~250 de comentarios): notas de incidentes/precios/verificaciones por provider y modelo; `models` en flow-style; comentarios dentro de la sección providers. `config.example.yaml` (243 líneas) como referencia de estilo documental.
- NO existe serialización YAML en el repo (0 hits de `yaml.Marshal`/`yaml.Encoder`); única dep YAML: `gopkg.in/yaml.v3 v3.0.1`.
- Patrón atomic-write + digest skip + flock ya demostrado in-repo: `modelscache/store.go:145-175` (persist), `writeFileAtomic` (202), sidecar `<path>.sha256` con auto-cura.
- Stores cache-only disponibles: `modelsdev.NewStore` (store.go:43), `upstream.NewZenStore/NewGoStore/NewOpenRouterStore` (zen.go:96, go.go:47, openrouter.go:228) sobre `*modelscache.Store[T]` (Get sin fetch, TTL por mtime).
- Baseline suite post-003: 836 tests / 33 pkgs, `-race` verde.

## Estado

Status: **Draft** — pendiente aprobación HITL (Pablo/Ofap)
Status: **Approved** by Pablo/Ofap (HITL — owner técnico mofgw) on 2026-09-16
