# review.md — 019-004-atomic-write-validate (etapa 4, Review two-layer)

## Veredicto

**REQUEST_CHANGES — 1 bloqueante** (B-1: flag `--once` del spec lockeado ausente). Los 3 Majors y los Advisories no bloquean. El núcleo (configsync, ParseForValidation, atomic write, skip) es sólido; el fix del bloqueante es trivial.

**Disclosure de verificación:** el matcher de permisos de bash bloqueó `go test`, `go vet` y varios `ls`/`wc` (incidente conocido del harness, 4º documentado en process-log). No se pudo correr la suite; los veredictos empíricos se apoyan en la corrida verificada por el orquestador (902/35, `-race`, build+vet OK) + lectura estática de código, diffs (`git show` de los 3 commits) y los 21 tests B1-B21. El canary `TestRoundTrip_LiveConfig` está en la corrida del orquestador y no puede fallar por construcción (hallazgo M-1).

## Capa 1 — Postcondiciones e invariantes

| # | Veredicto | Evidencia |
| --- | --- | --- |
| P1 | **PASS** | Bidireccional en `checkAssociation` (serialize.go:120-155, errores nombrando id en ambas direcciones + dup en raw). Un solo read del raw: main.go:93 → `Parse(raw)`:100 y `Apply(raw,…)` :119. Test B1. |
| P2 | **PASS** | Recorrido en orden del documento (serialize.go:80-88, D3); solo el nodo `models` se reemplaza (`replaceModels` :165-186); knobs/subprocess intactos. Tests B2 (discriminante orden alfabético del IR) y B3 (tabla de campos). |
| P3 | **PASS** | `pp.Models == nil → continue` (serialize.go:82-83). Test B4 con plan MUTANTE (go-cuenta-1 tocado) + oracle re-encode del subárbol — dientes suficientes: touch semántico (models null, valor, quoting, reorder, comentario perdido) cambia el encode → rojo. Sin hueco relevante; lo residual (columna de comentarios) es D2-aceptado por HITL. |
| P4 | **PASS** | `mergeKeyedSection` :356-418: (a) sobrescribe solo si `valuesEqual` difiere :402-414; (b) fields solo non-zero :290-324; (c) `thinking_default` jamás en `metadataFields` :306-324; (d) entry nueva alfabética solo campos provistos :376-387; (e) entries no cubiertas nunca visitadas; (f) no existe path de borrado. Tests B5 (6 subtests incl. key OR + corta coexistente) y B6 (thinking toggle + preserva). |
| P5 | **PASS** | Transferencia Head/Line/Foot al reemplazar seq (:195) y scalars (:409); keys jamás reemplazadas → sus comentarios viven; scalars de models reutilizados por valor (:196-208). Test B7 congela 7 marcadores incl. LineComment sobre scalar que CAMBIA. Ver M-5→A-5 (items de secuencias tocadas). |
| P6 | **PASS (con matiz)** | Shortcut no-op devuelve raw VERBATIM (serialize.go:102-104) → P6 satisfecho al máximo. Pero B8 queda tautológico (compara raw contra raw) — hallazgo M-1. |
| P7 | **PASS** | `ParseForValidation(candidate)` pre-commit (apply.go:49); test B10 verifica Config refleja plan y resto idéntico (DeepEqual por sección, APIKey vacía). |
| P8 | **PASS** | Todo error de Serialize/validación retorna ANTES de tocar disco (apply.go:45-51); ni config ni sidecar. Test B12: ambas ramas (YAML inválido + candidato roto `models: []`). |
| P9 | **PASS** | `writeAtomic` apply.go:82-108: temp en el mismo dir (:84), `Chmod` pre-rename (:94), `Sync` (:98), `Rename` (:105), `defer Remove` limpia. Mode del config vigente preservado (:63-66), default 0600 si no existe. Test B13 inyecta fallo real (dir read-only → CreateTemp en el MISMO dir falla — discriminante del spec) + mode 640. Rename-failure path no inyectable portable (test-audit lo declaró); estático OK (deferred Remove). Nota A-7: sin fsync del directorio (no exigido por el texto de P9). |
| P10 | **PASS** | Digest SOLO contra sidecar (:54-56); skip no toca nada (:57-61, mtime congelado por B14); sidecar-primero (:67-69); auto-cura contra candidato (B14 discriminante manual-edit + B15 discriminante espejo config==candidato/sidecar stale → escribe). Ver M-2 (skip-trap post-fallo). |
| P11 | **PASS** | Iteración siempre sortada (:254, :274, :344, `insertIndex` :467-474); warnings passthrough verbatim (:56, sin copia ni reorden). Test B11 congela bytes+digest. Ver A-8 (orden de fields DENTRO de entry nueva no alfabético — determinismo intacto). |
| P12 | **PASS** | Un `Warn` por warning ANTES de Apply (main.go:115-117); fail-soft (exit 0 con warnings, B16); Merge error → exit 1 sin escribir (:107-111, B17/B19). |
| P13 | **PASS** | Diff de 98d1826 verificado contra `6d63adf~1`: el cuerpo de `Parse` viejo es byte-idéntico a `parseCommon` (config.go:542-590) salvo `resolveKeys` movido a `Parse` (:519-528). `ParseForValidation` = `parseCommon` puro (:534-536). `Load`/`LoadFile` intactos. Tests B20/B21 (DeepEqual full-Config salvo APIKey). Suite config verde (gate del orquestador). |
| P14 | **PASS** | Stores con `Get` disk-only (modelscache/store.go:123-143); `Refresh` solo en modo fetch (:179-186), fail-soft `_, _ =`; --no-fetch = cero red. Todas-nil → Merge error → 1 (B17). |
| P15 | **PARCIAL** | Ciclo default, exit 0/1/2 y reporte final completos y testeados (B18/B19). **PERO el flag `--once` de D1.3/P15 no existe** — `mofgw-sync --once` → exit 2 (hallazgo B-1). |
| P16 | **PARCIAL** | Test existe, corre y pasa (orquestador) — pero vacuo: con `planNoOp` el shortcut devuelve raw verbatim → compara raw contra raw. La intención declarada del canary ("si yaml.v3 normalizara algo cosmético, el test lo DETECTA") no se ejercita — hallazgo M-1. |
| I1 | **PASS** | serialize.go: imports puros (bytes/sha256/hex/fmt/sort/strconv + catalogmerge/config/yaml). apply.go: `os.*` solo TIPOS de la interface FS — amendment HITL firmado (test-audit §4-A, documentado in-code :9-12). Sin red/log/reloj. El binario es el único I/O+log. |
| I2 | **PASS** | `configsync` importado solo por cmd/mofgw-sync (grep único hit: comentario en config.go). `Apply` escribe solo `configPath` + `configPath+".sha256"`; server sin cambios; clients.yaml jamás tocado. |
| I3 | **PASS** | Commits: 6d63adf solo tests+fixtures; 98d1826 = 3 archivos nuevos + config.go (aditivo, 23+/3-); eb29036 solo tests+fixture. CERO cambios en catalogmerge/modelsdev/upstream/modelscache/proxy/router/cmd/mofgw. |
| I4 | **PASS** | B3 congela "no inventar keys"; P4f sin borrado; provider nil no tocado. Margen teórico: sección nueva creada vacía (A-4). |
| I5 | **PASS** | `APIKey yaml:"-"` (config.go:165); `ParseForValidation` no consulta env; binario jamás resuelve keys (usa `Parse` del vigente para el plan, no para escribir). B10/B20 aserten APIKey vacía. |
| I6 | **PASS** | Fail-soft de fuentes (main.go:179-207, warnings) / fail-loud de datos (Apply P8, Merge-error exit 1). |

## Capa 2 — Hallazgos de calidad

**B-1 (Bloqueante) — Flag `--once` definido en D1.3 LOCKEADA y P15, no implementado.** `parseArgs` solo define `-config`/`--no-fetch` (main.go:75-84); el doc-comment del binario (:14) tampoco lo lista. `mofgw-sync --once` → `flag provided but not defined` → exit 2. Contradice spec.md:18 (D1.3) y :44 (P15 "--once (y default sin flag)"). El test-audit (B18) tampoco lo ejercita — el contrato RED heredó el gap, pero el test-audit no documenta decisión HITL de removerlo. Toca: D1.3/P15. Resolución trivial y a elección HITL: (i) alias no-op aceptado con subtest en parseArgs, o (ii) amend de spec documentando que 004 solo expone default/`--no-fetch` y `--once` nace en 006.

**M-1 (Major) — Oracles P6/P16 (B8/B9) estructuralmente vacíos por el shortcut no-op.** serialize.go:102-104: plan sin mutaciones → return raw verbatim. `planNoOp` (Models == declarados) jamás muta → B8 y B9 comparan raw contra raw: **pueden pasar para siempre, detecten o no normalización**. La letra de P6/P16 está satisfecha (de hecho la devolución verbatim es el comportamiento MÁS fiel posible), y el cubrimiento del encoder bajo plan mutante queda por-subárbol (B3/B4, buenos dientes) + marcadores (B7). Pero el propósito declarado de P16 como canary de D2 en el entorno real no se ejercita: el path `encodeNode` sobre documento completo con mutación no tiene ningún oracle de documento-entero. Toca: P6/P16 (letra ok, intención hueca), D8. No pide re-escritura del serializer (es el diseño correcto); pide decisión sobre oracle: un golden/materialización con plan mutante (o full-doc diff contra raw esperado) en un fix futuro o accept-and-document.

**M-2 (Major) — Skip-trap en el camino de fallo sidecar-primero.** apply.go:67-70: sidecar commiteado → write del config falla (disk full, EIO, perm-drift) → exit 1 (correcto ahí) pero estado en disco: sidecar=digest(candidato nuevo), config=viejo. Próxima corrida con mismas entradas → candidato idéntico → digest match → **SKIP con exit 0 y `Skipped=true`**, config stale silenciosamente, para siempre. El test-audit ya lo señalaba (apply_test.go:186-188). No viola postcondición escrita (P9 garantiza config intacto; P10 define digest-vs-candidato; sidecar-first es D6 LOCKEADA, espejo de modelscache persist). Es un riesgo operacional real que 005 no puede distinguir (recibe Skipped=true). Decisión HITL recomendada para 005/006 (p.ej. post-rename verify del digest, o alarma en "skip con config ≠ candidato" — esta última chocaría con el lock de P10); para 004: documentar el riesgo en el merge, no cambiar código.

**A-4 (Advisory) — Sección nueva creada vacía.** serialize.go:362-366 crea `pricing:`/`model_metadata:` antes del filtro de entries zero (:372-374); plan con solo entries zero-value sobre raw sin sección → agrega sección vacía (I4 marginal). No alcanzable con el IR real de 003. Caso sin test; riesgo teórico.

**A-5 (Advisory) — Comentarios de items dentro de secuencias tocadas se pierden.** Al sobrescribir `thinking:`/`supported_parameters:` el nodo nuevo es `strSeqScalar` (serialize.go:315, 408-413): solo Head/Line/Foot del nodo seq se transfieren; LineComment/FootComment de items individuales no. P5 no exige preservación a nivel item de secuencias tocadas → dentro de D2. La lista `models` sí está protegida (buildSequence reutiliza scalars por valor). Riesgo solo si el config vivo tiene comentarios por-item en esas listas.

**A-6 (Advisory) — Sin lock entre corridas concurrentes del binario.** modelscache usa flock; `Apply` no serializa dos `mofgw-sync` concurrentes. Con candidato determinístico el race es benigno, pero dos fetches concurrentes podrían intercalar sidecar/config de candidatos distintos. Relevante cuando 006 introduzca el timer.

**A-7 (Advisory) — Sin fsync del directorio tras rename.** writeAtomic fsyncea el temp pero no el dir (crash-consistency estricta). El diseño self-healing hace el riesgo benigno; fuera del texto de P9 y del patrón modelscache heredado. No requiere cambio.

**A-8 (Minor) — Orden de fields dentro de entries nuevas no alfabético.** `pricingFields`/`metadataFields` escriben en orden de declaración del struct, no alfabético (serialize.go:290-324); solo `mergeFields` (colisión de dos providers) ordena (:344). D9/P11 exigen alfabético para "keys nuevas de mapas" — la posición de la entry en el mapa sí es alfabética (testeada), el orden interno de fields queda sin congelar ni garantizado. Determinismo intacto; cosmético.

**A-9 (Advisory) — Interface FS más ancha que el uso.** `FS.WriteFile` y `FS.Chmod` sin callers en apply.go (el temp usa métodos de `*os.File`); vienen del contrato del test-writer (RED congelado) — no tocar en 004.

**Puntos evaluados sin hallazgo:** asociación 1:1 bidireccional sólida; transferencia de estilo flow/block correcta (Style transferido :195/:410-412); quoting de scalars supervivientes preservado por reutilización por valor; `valuesEqual` con dientes (Kind/Tag/Value recursivo); alias/anchors en entries tocadas → fail-loud descriptivo (I6); leaks de fds: cero (Close en todos los paths, defer Remove); precedencia de config path byte-idéntica a `config.Load` (comparado 497-516 vs 137-165); locking innecesario: no hay locks agregados.

## Disclosure anti-bias

Reviewer corriendo en **GLM — familia Z.ai (glm-5.3-flash, Zhipu/Z.ai)**. El process-log registra que en 019-001 el reviewer corrió en deepseek-v4-flash (misma familia que su implementer, bias degradado). Familia de esta review (GLM/Z.ai) vs implementer de 004 (deepseek-v4-flash): **distintas** — invariante anti-bias SATISFECHO en esta feature.

## Next steps recomendados

1. **Loop corto de fixes (no re-implementación):** resolver B-1 — la vía más barata es agregar `--once` como flag aceptado (no-op documentado: el ciclo YA es once) + 1 subtest en `TestRun_ExitCodes`/`parseArgs`; alternativa: amend HITL del spec. Re-corrida de suite por el orquestador (bash de subagentes bloqueado, incidente conocido).
2. **Decisiones HITL documentadas en el merge (sin código):** M-2 (skip-trap: riesgo operacional aceptado por D6 lockeada, anotar para 005), M-1 (vacuidad B8/B9: aceptar la devolución verbatim como comportamiento superior y opcionalmente agendar un oracle full-doc con plan mutante).
3. **Advisories A-4/A-5/A-8:** pasan a work-items del merge de documentación (o backlog de 005/006); ninguno justifica loop adicional.
4. **Merge directo de los 3 commits tras cerrar B-1** — el resto del contrato (P1-P14, I1-I6) está PASS con evidencia completa; suite 902/35 `-race` verificada por orquestador como gate.

Status: Review **REQUEST_CHANGES** by cdad-reviewer (GLM/Z.ai, familia distinta al implementer) on 2026-09-17

## Resolución del loop de fixes (2026-09-17)

- **B-1 — RESUELTO (decisión HITL: "corregir el origen del error").** El origen del hueco era el mapeo del test-audit (C16→B17/B18/B19 sin ejercitar `--once`): se corrigió a nivel contrato, no como parche. Mini-RED `6fed1d9` (test-writer: subtest `TestRun_ExitCodes/once_aceptado_y_redundante` congela `--once`/`-once` aceptados, redundantes con default D1.3, combinables con `-config`, jamás exit 2) → mini-GREEN `c3ea424` (implementer: flag no-op en parseArgs + doc-comment). Suite completa 903/35 `-race` verde (re-corrida por el orquestador). P15 → PASS completo.
- **M-1 — ACEPTADO Y DOCUMENTADO (HITL, recomendación del reviewer).** El shortcut no-op de Serialize (raw verbatim cuando nada cambia) es el comportamiento más fiel posible de P6/P16; los oracles B8/B9 tautológicos se aceptan como consecuencia (el cubrimiento del encoder bajo mutación queda en B3/B4/B7 por-subárbol). Oracle full-doc con plan mutante agendado como candidato a hardening posterior.
- **M-2 — DOCUMENTADO PARA 005/006 (sin código, D6 lockeada).** Skip-trap teórico (sidecar-primero + fallo de write → próximo run skipea config stale): riesgo operacional registrado para que 019-005 considere verificación post-rename o señal de "skip con config ≠ candidato" en su spec.
- **A-4/A-5/A-6/A-7/A-8/A-9** — pasan a backlog de hardening/005/006 según recomendación del reviewer.

Status: **Sign-off HITL** by Pablo/Ofap on 2026-09-17 — bloqueante B-1 resuelto, M-1/M-2 aceptados+documentados, gate 4→5 DESBLOQUEADO → merge + memory bank (etapa 5)
