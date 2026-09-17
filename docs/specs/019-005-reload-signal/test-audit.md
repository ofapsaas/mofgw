# test-audit.md — 019-005-reload-signal

**Feature:** 019-005-reload-signal · **Etapa:** 3.0 (AUDIT) · **Fecha:** 2026-09-17
**Fuente única:** `docs/specs/019-005-reload-signal/spec.md` (P1-P15, I1-I7, C1-C14) + `docs/systemPatterns.md` + test-audit de 004 (convenciones del epic).
**Evidencia de baseline (verificada empíricamente, no asumida):** `rtk go test -race -count=1 ./...` → **903 passed in 35 packages** (coincide con el baseline post-004 declarado en el spec). Confirmado además: `internal/reloadsig` no existe (grep `reloadsig|SystemdCtl|MOFGW_SYNC_VERIFY_KEY|no-reload` en `*_test.go` → 0 hits fuera del spec), y el único paquete de tests existente en el blast radius es `cmd/mofgw-sync` (4 tests, B16-B19 de 004).

## 1. Resumen ejecutivo

- **Veredicto global: 0 tests existentes requieren modificación.** La extensión de `cmd/mofgw-sync` es ADITIVA (campos nuevos en `runOpts` + flag nuevo en `parseArgs` + fase post-004 condicionada). El mecanismo de convivencia diseñado (§2.4): **campos cero-valor de `runOpts` = fase de reload DESHABILITADA** — los tests B16-B19 de 004 no setean los campos nuevos, por lo que su corrida congela EXACTAMENTE la fase 004 (jamás toca `systemctl`, ni siquiera en el path de skip, donde correría el M-2) y sus exit codes 0/1/2 quedan intactos. Ninguna expectativa existente cambia.
- **17 tests nuevos (B1-B17)** mapean los 14 criterios C1-C14 (verificación exhaustiva en §3): 2 en `cmd/mofgw-sync/reload_test.go` (NUEVO — trigger y matriz de exit codes, nivel binario) + 14 en `internal/reloadsig/reloadsig_test.go` (NUEVO) + 1 canary live (C14). Ningún test queda sin postcondición; ninguna postcondición queda sin test.
- **RED por compilación** (precedente 001/002/003/004, lockeado por la sección "Naturaleza del RED" del spec): los tests de `internal/reloadsig` fallan por `undefined: reloadsig.*`; los tests nuevos de `cmd/mofgw-sync` fallan por compilación (`opts.Reload` / `runOpts.NoReload` no existen en `runOpts`).
- **1 decisión de diseño del test contract documentada** (§2.4: nil-hooks-disabled) + **4 preguntas formales al orquestador** (§4; ninguna bloquea 15/17 tests — resueltas por HITL en el sign-off).
- **Advertencia operativa RED→GREEN:** los tests inyectan fakes; un `run()` que IGNORARA la inyección y cableara systemctl real reiniciaría `mofgw.service` de verdad durante `go test` (el host deploy tiene systemd user). El fake SystemdCtl registra llamadas y la ausencia de llamadas al fake es observable → discriminante R4 (§5).

## 2. Inventario de suite existente + veredicto por test existente

### 2.1 Alcance del blast-radius

Áreas del spec: `internal/reloadsig` (paquete NUEVO, 0 tests existentes), `cmd/mofgw-sync/main.go` (extensión; su suite es `main_test.go`, 4 tests de 004). Todo lo demás está explícitamente fuera por I1 ("CERO cambios en internal/configsync, catalogmerge, modelsdev, upstream, modelscache, config, proxy, router, cmd/mofgw") y se valida con el gate de suite completa.

### 2.2 Veredicto por test existente

| Paquete | Tests (func Test) | Veredicto |
| --- | --- | --- |
| `cmd/mofgw-sync` (`main_test.go`) | 4: `TestRun_LogsWarnings`, `TestRun_NoFetchCacheOnly`, `TestRun_OnceCycle`, `TestRun_ExitCodes` (con 5 subtests + subtest `once_aceptado_y_redundante`) | **UNTOUCHED — 0 modificaciones** (justificación por test abajo) |
| `internal/configsync` | 15 (B1-B15 de 004) | **UNTOUCHED** (I1: cero cambios en configsync; su Report queda intocado — reloadsig lo consume como primitivas `Applied/Skipped/Digest`, D1) |
| `internal/config` (incl. `p13_parseforvalidation_test.go`) | 56+2 | **UNTOUCHED** (I1: cero cambios en config; reloadsig solo lo CONSUME vía `ParseForValidation`) |
| `internal/catalogmerge` / `upstream` / `modelsdev` / `modelscache` | 20/16/20/0 | **UNTOUCHED** (I1, precedentes I3 de 004) |
| Resto de la suite (903 − 21 explícitos) | 882 | **UNTOUCHED implícito**, verificado por el run completo del baseline |

**Lista explícita de tests untouched en el blast radius directo (21):** los 4 de `cmd/mofgw-sync` (analizados uno a uno abajo) + los 15 de `internal/configsync` + los 2 de `internal/config` (`TestParseForValidation_NoEnv`, `TestParseForValidation_MatchesParse`). Estos últimos son el contrato que 005 CONSUME (D1 permite solo `ParseForValidation`; I1 congela su paquete) — siguen siendo el oráculo de que nada cambió bajo 005.

### 2.3 Veredicto detallado por test de cmd/mofgw-sync

**Mecanismo de convivencia (decisión de diseño del test contract, resuelve la pregunta del orquestador):** la fase de reload corre post-004 dentro del mismo `run(opts, logger)`. Los tests de 004 NO la corren porque **los campos nuevos de `runOpts` quedan en zero-value y zero-value = fase deshabilitada**:

```go
// extensión ADITIVA de runOpts (cmd/mofgw-sync/main.go — la extiende el implementer):
type runOpts struct {
	ConfigPath string
	NoFetch    bool
	CachePaths cachePaths
	// ---- 005 ----
	NoReload bool            // --no-reload (D12); parseArgs lo parsea (P11: exit 2 si malformado)
	Reload   reloadHooks     // inyección de la fase reload (tests); zero-value = DESHABILITADA
}
type reloadHooks struct {
	Systemd   reloadsig.SystemdCtl // nil ⇒ NINGUNA fase de reload/verificación/rollback corre
	Prober    reloadsig.Prober     //       (ni siquiera el chequeo M-2): exit = exit de 004 (P1)
	Clock     reloadsig.Clock
	VerifyKey string               // "" = unset (P9); el binario la resuelve con os.LookupEnv
	FS        reloadsig.FS         // nil ⇒ osFS{} real (los tests que rollean back la inyectan)
}
```

Semántica congelada por el contrato de tests (HEAD del nuevo `reload_test.go`): **`Reload.Systemd == nil` ⇒ run() jamás invoca `reloadsig.Run` (ni M-2, ni restart, ni rollback) y retorna el exit de la fase 004.** Es el equivalente observacional exacto de `--no-reload` (P1), pero como convención de inyección de tests — análoga a `CachePaths` de 004. `main()` (no testeado, glue de 3 líneas) SIEMPRE cablea hooks no-nil en producción.

- **`TestRun_LogsWarnings`**: fase disabled → expectativas intactas. UNTOUCHED.
- **`TestRun_NoFetchCacheOnly`**: 004 aborta PRE-reporte → P1 de 005 garantiza que la fase de reload no corre. UNTOUCHED.
- **`TestRun_OnceCycle`**: hooks nil ⇒ ni siquiera el M-2 corre → exit 0, mtime intacto. UNTOUCHED.
- **`TestRun_ExitCodes`**: con hooks nil ningún caso llega a exit 3; los asserts de 004 se MANTIENEN tal cual (P11 de 005 es extensión: 0/1/2 conservan semántica; 3 solo en path `Applied && reload falló`). UNTOUCHED.

**Por qué no se usa `NoReload:true` en los tests de 004** (alternativa descartada): mezclaría el flag REAL con el scaffolding de tests, obligaría a tocar 6 call-sites sin ganancia de postcondición. La inyección nil mantiene B16-B19 byte-untouched.

## 3. Plan de tests nuevos (B1-B17) con mapeo postcondición

Convención: nombres LOCKEADOS por la tabla C1-C14 del spec. Ubicación: `cmd/mofgw-sync/reload_test.go` (NUEVO — B1, B2) y `internal/reloadsig/reloadsig_test.go` (NUEVO — B3-B16) + canary live (B17). Cada test lleva en su doc-comment la(s) postcondición(es) que congela.

**Contratos de firma definidos por el test-writer (el implementer DEBE respetarlos):**

```go
// internal/reloadsig (nuevo):
func Run(in Input) int
type Input struct {
	Stash      []byte        // bytes previos del config (D9 rollback)
	ConfigPath string
	Applied    bool          // reporte de 004
	Skipped    bool
	Digest     string        // sha256 hex del candidato (defensa M-2)
	Unit       string        // "mofgw.service" (D4)
	VerifyKey  string        // "" = unset (P9)
	Systemd    SystemdCtl
	Prober     Prober
	FS         FS
	Clock      Clock
	Logger     *slog.Logger  // nil → slog.Default() (R3)
}
type SystemdCtl interface {
	Available() error                                  // P10: detecta systemd ausente (antes de P3)
	Restart(unit string) error                         // timeout de comando 30s (wiring real)
	IsActive(unit string) bool                         // F1; fake scriptable
}
type Prober interface {
	Get(url string, bearer string) (status int, body []byte, err error) // healthz bearer=="", /v1/models bearer==VerifyKey
}
type FS interface { /* CreateTemp/Rename/Stat/ReadFile/WriteFile/Remove/Chmod — réplica de la FS de 004 */ }
type Clock interface { Now() time.Time; Sleep(d time.Duration) }  // fake: Sleep avanza el reloj, jamás duerme (R1)

// cmd/mofgw-sync/main.go (extensión aditiva): runOpts.NoReload + runOpts.Reload (reloadHooks, §2.4)
// Q3 ratificado: tipos reales (execSystemdCtl, httpProber) exportables en main.go para B17.
```

### B1 — `TestRun_ReloadTrigger` (C1, P1+D3+D12+P15) — cmd/mofgw-sync/reload_test.go
Tabla: (a) applied + hooks → Restart("mofgw.service") EXACTAMENTE 1 vez, exit 0; (b) skipped + hooks → cero Restart/IsActive, M-2 corre (m2_check en log), exit 0; (c) applied + NoReload → cero llamadas SystemdCtl/Prober, exit de 004; (d) 004 falla + hooks → cero llamadas, exit 1; (e) unit == "mofgw.service" en todos los registros. RED: compilación.

### B2 — `TestRun_ExitCodesReload` (C8, P11) — extensión de TestRun_ExitCodes
Matriz 0/1/2/3 sobre combinaciones reales: (a) applied+reload OK+key unset → 0 con warning degradación; (b) applied+reload OK+key set+paridad OK → 0; (c) skip+M-2 match → 0; (d) skip+M-2 mismatch → 1 sin restart (FS con ReadFile tamper); (e/f) --no-reload + applied/skipped → 0; (g) --no-reload=x → parseArgs error → 2; (h) applied+restart cmd error → rollback completo → **exit 3**, config restaurado == bytes previos; (i) applied+systemd unavailable → 3 con log declarando estado NUEVO; (j) exit 1 de 004 con hooks → cero llamadas. RED: compilación.

### B3 — `TestRun_M2Defense` (C2, P2+D8)
(a) digest disco == Digest → exit 0, log `skip verificado`; (b) mismatch → exit 1, AMBOS digests en log, cero Restart/IsActive/Prober. RED: compilación.

### B4 — `TestRun_M2ReadOnly` (C13, P2)
Spy FS: tras skip (match), WriteFile/Rename/Remove/Chmod/CreateTemp = cero llamadas. RED: compilación.

### B5 — `TestRun_ReloadPhases` (C3, P3/P4/P5)
Fakes OK, key unset: Restart ×1; IsActive poll (fake no-activo×2 → activo) con sleeps 1s registrados (Clock); URLs EXACTAS y ordenadas `http://127.0.0.1:4444/healthz` (addr NO-default — discriminante derivación D5) sin Bearer; healthz script 500×2 → 2xx; exit 0; cero llamadas /v1/models. RED: compilación.

### B6 — `TestRun_SettleTimeoutRollback` (C3, P4/P5 → rollback)
(a) is-active false siempre → ventana agotada (~31 polls fake, cero sleeps reales) → restore + re-restart + F1/F2 → exit 3, log rollback_*; (b) healthz nunca 2xx → idem. Re-restart == 2 totales, config == stash. RED: compilación.

### B7 — `TestRun_Parity` (C4, P6+D6)
Key seteada, server listo: (a) set == esperado con `created` distinto por entry y campos extra → exit 0; (b) id faltante → rollback exit 3; (c) id extra → rollback; (d) object != "list" → rollback; (e) JSON inválido → rollback; (f) 401 → rollback; (g) orden permutado → exit 0 (SET). F3 == 1 request, bearer == VerifyKey, URL http://127.0.0.1:4444/v1/models. RED: compilación.

### B8 — `TestRun_ParityKeyDegradation` (C4, P9+D7)
(a) key unset + F1/F2 ok → cero /v1/models, warning con sufijo `verify_key` ×1, exit 0; (b) key seteada + 401 → rollback (NO degradación) → exit 3. RED: compilación.

### B9 — `TestRun_RollbackRestoreOnly` (C5, P7/P8+D9)
Fallo en F3; stash arbitrario NO-YAML: (a) config post-rollback == stash byte-a-byte; (b) mode 0o640 preservado; (c) patrón atómico (discriminante de B10); (d) re-restart ×1 adicional + F1/F2 ok → **exit 3 JAMÁS 0**; (e) log declara rollback completo. RED: compilación.

### B10 — `TestRun_RollbackWriteFail` (C6, P7/D10)
chmod 0o555 al dir → CreateTemp en ESE dir falla → exit 3 inmediato, log con error I/O, cero re-restart (Restart total == 1), config vigente intacto. RED: compilación.

### B11 — `TestRun_RollbackExhausted` (C5/P8, D10)
Restore OK pero: (a) re-restart falla → exit 3, log rollback_restart; (b) F1 timeout re-verify → exit 3, log rollback_verify; (c) F2 fail → idem. Exit 3 SIEMPRE. RED: compilación.

### B12 — `TestRun_RollbackSidecarUntouched` (C11, P14)
Sidecar con digest del candidato (mtime congelado Chtimes) → rollback exitoso: sidecar byte-intacto + mtime intacto, config == stash, clients.yaml inerte intacto. RED: compilación.

### B13 — `TestRun_SystemdUnavailable` (C7, P10)
Available() error → exit 3 descriptivo, cero Restart/IsActive/Prober/escrituras, orden de llamadas: solo Available. RED: compilación.

### B14 — `TestRun_PhaseLogging` (C9, P12)
Happy path con key → 1 record por fase {restart, is_active, healthz, parity} en orden real; degradado → {restart, is_active, healthz, degraded}; fallo → rollback_* con nivel Error; M-2 → m2_check. Niveles Info/Warn/Error congelados; campos: unit en restart, url en healthz/parity, ambos digests en M-2. RED: compilación.

### B15 — `TestRun_NoSecretsInLogs` (C10, P13+I6)
Key "s3cr3t-k3y", 401 forzado con rollback completo → valor JAMÁS en el log (ni truncado), sin headers Authorization logueados; degradación sin key → warning sin valor. RED: compilación.

### B16 — `TestRun_ExpectedModelSet` (C12, D6)
Config con http+subprocess+dedup cross-provider → set esperado = unión dedup; tabla: set exacto → 0; sin claude-sonnet-4 → rollback (subprocess PARTICIPA); id extra → rollback. "Providers degradados sin models" = contribuyen CERO ids (R5: no construibles post-004, congelado por TestLoadMissingProviderFields). RED: compilación.

### B17 — `TestReloadSig_LiveEnvironment` (C14, D4/D5/D7) — canary opt-in
Entorno real: systemctl real + restart real + /healthz + /v1/models con key real; implementaciones REALES construidas in-test desde los tipos de main.go (Q3). Doble guardia: service existe && key seteada && `MOFGW_SYNC_LIVE=1` (Q2 ratificada por HITL: reinicia producción — opt-in obligatorio). Skip silencioso si falta cualquiera. RED: skip en sandbox.

### Cobertura completa

| P | Test(s) | | P | Test(s) |
| --- | --- | --- | --- | --- |
| P1 | B1, B2(e,f,j) | | P9 | B8 (+ B2a) |
| P2 | B3, B4 (+ B2c,d) | | P10 | B13 (+ B2i) |
| P3 | B5 (+ B2h) | | P11 | B2 (matriz) |
| P4 | B5, B6, B11 | | P12 | B14 |
| P5 | B5, B6 | | P13 | B15 |
| P6 | B7 | | P14 | B12 |
| P7 | B9, B10 | | P15 | B1, B9, B5 |
| P8 | B9, B11 | | C1-C14 | todos con test lockeado |

15/15 postcondiciones cubiertas, 0 tests huérfanos. I1-I7 sin tests RED: arquitecturales (I1/I2/I4 etapa 4), I3 por B9/B10/B12 + gate, I5 por contrato de interfaz congelada (no existe método de señal) + review, I6 por B15, I7 por B3/B11/B13/B14.

## 4. Benefit-of-doubt — resueltos / pendientes

### Resueltos
- **R1. Reloj:** Clock inyectado — Sleep(1s) del fake avanza reloj lógico y cuenta polls; ventana = Now()-phaseStart < 30s; determinístico, cero esperas reales. Un impl con time.Sleep real HANG el test (señal R4).
- **R2. Prober fake:** UN método Get(url, bearer) → (status, body, err); parseo JSON en reloadsig; bodies crudos del shape real.
- **R3. Logger:** `*slog.Logger` (nil → slog.Default()) — precedente run(opts, logger) de 004; compatible con P12.
- **R4. Semántica hooks nil:** §2.4 — Reload.Systemd == nil ⇒ fase disabled, exit = 004, jamás M-2. main() SIEMPRE cablea hooks reales (riesgo R3 de wiring → review).
- **R5. Degradados sin models:** contribuyen CERO ids; no construibles post-004 (congelado por TestLoadMissingProviderFields).
- **R6. Literal del warning:** assert del sufijo común `verify_key` con conteo exacto 1; literal canónico para review.
- **R7. VerifyKey="" :** tratado como unset (hardening posterior).

### Pendientes → RESUELTAS POR HITL (sign-off 2026-09-17)
1. **Q1 RATIFICADA:** tests nuevos de cmd en `cmd/mofgw-sync/reload_test.go` (archivo NUEVO); `main_test.go` byte-intacto. I1 rige el código de implementación; los tests son dominio del test-writer (contrato de roles CDAD).
2. **Q2 RATIFICADA con endurecimiento:** canary C14 con triple guardia — service existe && key seteada && **opt-in explícito `MOFGW_SYNC_LIVE=1`** (un test que reinicia producción exige opt-in; sin la var, skip siempre).
3. **Q3 RATIFICADA:** implementaciones reales (exec-backed SystemdCtl, net/http Prober) como tipos en main.go; B17 las construye in-package.
4. **Q4 RATIFICADA:** firma `Run(in Input) int` (struct agregada) — 11 parámetros posicionales son propensos a error; el implementer DEBE respetarla.

## 5. Estrategia RED + discriminantes

**Naturaleza del RED (lockeada por el spec):** `internal/reloadsig` inexistente → `undefined: reloadsig.*` en B3-B16; `runOpts` sin campos nuevos → compilación falla en B1/B2. Verificación: build failure de los paquetes afectados listando símbolos; `go build ./...` Success. Desviación documentada del gate genérico AssertionError, lockeada por el spec.

**Discriminantes RED→GREEN:** restart fantasma (B1a/B1b conteos), rollback con bytes no exactos (B9 stash arbitrario no-YAML vs re-serialización), restore no-atómico (B10 read-only dir), rollback que reporta éxito (B9d/B11 exit 3 SIEMPRE), sidecar tocado (B12 mtime Chtimes), poll sin reloj (B6/B11 hang = señal), paridad por secuencia vs set (B7g permuted → 0), created/extra participando (B7a), URL hardcodeada (B5/B7 addr 4444), M-2 que escribe (B4 spy), M-2 con digest equivocado (B3b ambos digests), secrets en logs (B15), systemd-unavailable que restartea (B13), reload corriendo con hooks nil (gate B16-B19 intacto 903-verde), impl que ignora inyección (R4).

**Commits RED (uno por postcondición):** B1 → P1 · B2 → P11 · B3+B4 → P2 · B5+B6 → P3/P4/P5 · B7+B8 → P6/P9 · B9+B10 → P7 · B11 → P8/D10 · B12 → P14 · B13 → P10 · B14 → P12 · B15 → P13 · B16 → D6 · B17 → C14.

## 6. Fixtures / inyecciones

- **fakeSystemd**: AvailableErr/RestartErr, IsActives []bool (script; false-forever), registro Calls {Op, Unit}.
- **fakeProber**: scripts por path (Healthz []resp, Models resp), registro (url, bearer), bodies crudos del shape real.
- **fakeClock**: Now() base configurable; Sleep(d) avanza reloj + contador; jamás duerme.
- **realFS** (réplica apply_test.go:45-55 de 004) + **spyFS** (wrapper contable B4/B13).
- **Inyección de fallos SIEMPRE con sistema real** (chmod 0o555, os.Chtimes, bytes tamperados) — cero mocks de plumbing (AP-14).
- **Config fixture reloadsig** (`reloadConfigFixture`): addr `127.0.0.1:4444` NO-default (discriminante D5/D6); http+subprocess+dedup cross-provider; válido para ParseForValidation sin env. Stash arbitrario no-YAML en la mayoría (discrimina bytes-leídos-jamás-derivados); B5/B7/B16 usan el config fixture como stash.
- **Fixtures cmd (B1/B2):** los de 004 (`syncConfigTemplate`, `seedZenCache`, `captureLogs`) — fase 004 con fixtures reales, solo el layer reload es fake. `t.TempDir()` SIEMPRE (solo B17 toca entorno real, opt-in).

## 7. Gate checklist (salida 3.0 → 3.1)

- [x] Suite baseline verde con `-race`: **903/35** (evidencia §1, corrida por el test-writer).
- [x] Tests modificados: **0** — mecanismo de convivencia 004/005 documentado (§2.4).
- [x] Tests untouched listados EXPLÍCITAMENTE: 21 en el blast radius (§2.2); resto por I1 + run completo.
- [x] Toda postcondición P1-P15 tiene ≥1 test (tabla §3).
- [x] Todo test nuevo mapea a postcondición/criterio — 17/17 con C-mapeo lockeado; 0 tests por completitud.
- [x] Ningún test depende de estructura interna: contrato observable (llamadas registradas, bytes+sidecar+mtime+mode, exit codes, records slog con `phase`); las llamadas systemctl/HTTP son el CONTRATO de coordinación de fases de 005 (el fake registra QUÉ, no CÓMO); flags `--user` y timeouts reales (30s/2s/5s) congelados en review + C14.
- [x] Benefit-of-doubt: 7 resueltos + 4 preguntas resueltas por HITL (§4).
- [x] Riesgos de regresión: (R1) exit semantics 004 con hooks nil — gate B16-B19 intacto post-GREEN; (R2) wiring real solo en review/C14; (R3) nil-hooks — producción depende de main() cableando (compile-time + review); (R4) sleeps reales → hang detectable; (R5) suite esperada post-GREEN: **920/36**.

---

**Resumen:** Tests a modificar: **0** · Tests untouched: **903** (21 explícitos en blast radius) · Tests nuevos: **17** (B1-B17, C1-C14 mapeo 1:1) · Regression risks: **5** (detalle §7).

Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — Q1-Q4 ratificadas (reload_test.go nuevo archivo; canary con opt-in MOFGW_SYNC_LIVE=1; tipos reales en main.go; firma Run(in Input) int). Gate 3.0 cerrado, arranca RED (3.1).
