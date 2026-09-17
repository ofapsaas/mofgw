# test-audit.md — 019-006-systemd-timer

**Feature:** 019-006-systemd-timer · **Etapa:** 3.0 (AUDIT) · **Fecha:** 2026-09-17
**Fuente única:** `docs/specs/019-006-systemd-timer/spec.md` (P1-P12, I1-I7, C1-C10) + scripts/test-install.sh (harness vigente).
**Desviación de proceso:** AUDIT inline por el orquestador (spawning de sub-agentes caído — 8º incidente del epic, disclosure en state). El alcance Go de la feature es SOLO tests (I5: cero cambios en `internal/*` y `cmd/mofgw-sync/main.go`), por lo que el audit es de superficie mínima.

## 1. Resumen ejecutivo

- **Veredicto global: 0 tests existentes requieren modificación.** I5 congela `internal/*` y `cmd/mofgw-sync/main.go` (y por extensión sus suites); `scripts/install.sh` y `scripts/test-install.sh` son shell (fuera de la suite Go) y se extienden aditivamente.
- **4 tests Go nuevos** (B1-B4) en `cmd/mofgw-sync/unitfiles_test.go` (NUEVO) mapean C1-C3 + C10; **5 tests bash nuevos** en `scripts/test-install.sh` mapean C4/C5/C7/C8/C9 (C6 es transversal al harness completo). 10/10 criterios cubiertos; 12/12 postcondiciones con test o verificación estática del harness.
- **RED por comportamiento sobre archivos ausentes** (lockeado por el spec): los golden tests fallan por `os.ReadFile` de `scripts/systemd/mofgw-sync.{service,timer}` inexistentes o por aserción de contenido.
- Baseline verificado por el orquestador: **953/36 `-race`** (post-005).

## 2. Inventario + veredicto

| Paquete/área | Tests | Veredicto |
| --- | --- | --- |
| `cmd/mofgw-sync` (main_test.go, reload_test.go, reload_live_test.go) | 22 top-level | UNTOUCHED (I5: cero cambios en main.go; los tests de 004/005 congelan su comportamiento) |
| `internal/*` (todo) | todo | UNTOUCHED (I5: cero diffs) |
| `scripts/install.sh`, `scripts/test-install.sh` | bash | Extensión ADITIVA (los 6 tests existentes del harness deben seguir pasando — gate) |

Lista explícita untouched en blast radius: los 22 tests top-level de cmd/mofgw-sync (incl. subtests reload) + 903 de post-005 restantes. La suite total post-GREEN se espera en ~957+3 top-level (golden ×3 + canary) con subtests adicionales.

## 3. Plan de tests nuevos (B1-B4 + B5-B9 bash)

**Contratos del test-writer:** los golden tests leen los archivos via `os.ReadFile` resolviento el repo root (subiendo hasta `go.mod`); validan contenido con aserciones de campo exacto + negativos (spec C1/C2: positivos Y negativos — nunca RED trivial).

- **B1 `TestSyncServiceUnit`** (C1, P1): existe + campos `Type=oneshot`, `ExecStart=%h/.local/bin/mofgw-sync`, `EnvironmentFile=-%h/.config/mofgw/env`, `StandardOutput=journal`, `StandardError=journal`; negativos: cero `Restart=`, cero `[Install]`, cero `TimeoutStartSec`, cero `RemainAfterExit`. RED: `os.ReadFile` error.
- **B2 `TestSyncTimerUnit`** (C2): existe + `OnBootSec=10min`, `OnUnitActiveSec=60min`, `WantedBy=timers.target`; negativos: cero `OnCalendar=`, `Persistent=`, `AccuracySec=`, `Unit=`. RED: ídem.
- **B3 `TestSyncUnitPairing`** (C3): ambos files tienen Description; el emparejamiento por nombre (mofgw-sync.timer ↔ mofgw-sync.service) se congela comparando el prefijo de nombre de archivo + descripciones presentes.
- **B4 `TestUnits_LiveEnvironment`** (C10, canary): triple guardia `MOFGW_SYNC_LIVE=1` + systemctl disponible + timer instalado en `~/.config/systemd/user`; asserts: `is-enabled` == enabled, `list-timers --no-legend` no-vacío con NEXT entre vencimientos. Skip silencioso en sandbox. RED: skip en RED (como B17 de 005).
- **B5-B9 (bash, scripts/test-install.sh — implementación de C4-C9):** `test_sync_creates_files` (+assert de salida P12), `test_sync_bin_permissions`, `test_sync_units_backup_on_overwrite` (P4: unit server divergido pre-sembrado → `.bak.<ts>` byte-exacto), `test_sync_idempotent` (2ª corrida sin backups nuevos), `test_sync_timer_requires_healthy_server` (C7), `test_sync_uninstall` (P11), extensión de `test_unit_passes_verify` (C9 — los 3 units).

## 4. Benefit-of-doubt

- **Resuelto:** el golden test NO usa go:embed (ruta `../` prohibida) ni duplica contenido — lee del repo root resuelto caminando hacia `go.mod` (spec D1.3). El RED del harness usa el guard existente (test-install.sh:31-37).
- **Resuelto:** `EnvironmentFile=-` con prefix `-` en el sync (tolerante a file ausente) vs sin prefix en el server — es DELIBERADO (D4), el golden lo congela con el prefix exacto.
- **Pendiente → resuelto por HITL:** ninguna (el spec cerró todas las decisiones en D1-D8).

## 5. Estrategia RED + discriminantes

RED = archivos ausentes (`os.ReadFile` error) + harness bash guard. **Discriminantes anti-"pasa por accidente":** el golden valida CONTENIDO (campos exactos + negativos explícitos) — un archivo presente pero incompleto falla; los tests bash pre-sembran units DIVERGIDOS y verifican `.bak.<ts>` byte-exacto (un mv sin backup o sin backup falla); `systemd-analyze verify` valida sintaxis real de los 3 units. Nivel de log del wrapper dry-run: presencia del texto dry-run en las líneas nuevas (no se congelan textos exactos del install.sh — el harness aserta existencia y exit codes).

**Commits RED:** B1-B3 (golden) → `test(mofgw): 019-006 — RED (golden units)`. El harness bash RED va con GREEN (el guard RED ya existe en el harness — los tests bash nuevos se agregan junto a su implementación para que el harness siga ejecutable; desviación documentada: RED bash parcial = guardes).

## 6. Fixtures

- Golden tests: repo root resuelto caminando hacia `go.mod` desde CWD del paquete; paths `scripts/systemd/…` relativos al root.
- Harness: sandbox `MOFGW_HOME=$(mktemp -d)` + `MOFGW_SKIP_SYSTEMCTL=1`; pre-siembra de units divergidos para C4; puerto 3369 ocupado para C7; `MOFGW_SYNC_BIN_SRC` para C5 (copia sin build).
- Canary B17-patrón: triple guardia (MOFGW_SYNC_LIVE=1 + systemctl disponible + timer instalado).

## 7. Gate checklist

- [x] Baseline 953/36 `-race` verificado por el orquestador (post-005).
- [x] Tests a modificar: **0** — justificado (I5; bash aditivo).
- [x] Toda postcondición P1-P12 tiene test o verificación estática del harness (tabla §3 + C-mapeo).
- [x] Todo test nuevo mapea a criterio C1-C10.
- [x] Ningún test depende de estructura interna (golden de archivos commiteados = contrato observable; negativos explícitos).
- [x] Benefit-of-doubt resuelto; riesgos: (R1) golden de contenido frágil a reformateo cosético de units — los asserts son por campo clave con greps de línea, no por bytes exactos del archivo completo; (R2) el harness bash corre en el orquestador (bash de subagentes bloqueado); (R3) canary opt-in nunca corre en CI.

---

**Resumen:** Tests a modificar: **0** · Tests nuevos: **4 Go (B1-B4) + 7 bash (C4-C9)** · Regression risks: 2 (golden por campos con negativos; harness corre en orquestador).

Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — gate 3.0 cerrado, arranca RED (3.1).
