# review.md — 019-006-systemd-timer (etapa 4, Review two-layer)

## Veredicto

**REQUEST_CHANGES — 0 Bloqueantes · 2 Major · 3 Minor · 2 Advisory.**
P10 incumplida (documentación del operador) + trap del unit divergido (salva archivo, no comportamiento): uno es fix de 2 líneas, el otro decisión de alcance. El contrato riesgoso (backup-on-overwrite, orden, fail-loud) está implementado y testeado como el spec manda. Ningún hallazgo justifica REJECT.

**Desviación estructural del ciclo (registrada en el state):** RED ejecutado sin sesión aislada de test-writer (runtime de sub-agentes caído). El test-audit se commiteó ANTES del GREEN y el `git diff RED..GREEN` sobre el golden test es vacío (F7 confirmado) — el contrato lockeado sobrevivió.

## Capa 1 — Tabla P1–P12 + I1–I7

| ID | Veredicto | Evidencia |
|---|---|---|
| P1 | PASS | `scripts/systemd/mofgw-sync.service`: Type=oneshot, ExecStart del bin sync, EnvironmentFile tolerante con prefix -, journal; cero Restart/[Install]/TimeoutStartSec/RemainAfterExit (verificado por lectura directa + golden B1) |
| P2 | PASS | `scripts/systemd/mofgw-sync.timer`: OnBootSec=10min + OnUnitActiveSec=60min + WantedBy=timers.target; "Unit" solo en `[Unit]` y en comentario; cero OnCalendar/Persistent/AccuracySec (golden B2) |
| P3 | PASS | install_unit_file (ausente→cp, idéntico→no-op, difiere→backup+cp) usado por sync units; re-corrida idempotente sin backups nuevos (harness C4) |
| P4 | PASS (salvedad F2) | install_unit del server rutea por install_unit_file — trap corregido; harness pre-siembra server divergido y aserta backup byte-exacto + aviso de conciliación (F2 fix) |
| P5 | PASS | install_sync_binary: precedencia SYNC_BIN_SRC→prebuilt→go build; chmod 755; espejo de install_binary (harness C4/C5) |
| P6 | PASS | main: binary→config→unit→sync_binary→sync_units→start_service→start_sync_timer; set -e aborta antes del timer (harness C7 con fake systemctl + puerto ocupado: cero enable del timer) |
| P7 | PASS | dry-run loguea y retorna vía wrapper; todo el harness con SKIP=1 |
| P8 | PASS | cero escrituras al env file en install.sh (grep); prefix `-`; golden congela la referencia |
| P9 | PASS | --once queda no-op (cero diffs en main.go) |
| P10 | FAIL→RESUELTO (F1) | header documenta MOFGW_SYNC_VERIFY_KEY + assert `install.sh --help` en el harness (fix pre-merge) |
| P11 | PASS | uninstall: disable --now tolerante, borra units+bin sync, .bak.* inmortales (harness C8 con .bak sembrado) |
| P12 | PASS | resumen reporta timer + binario + comando de inspección (harness lo aserta por grep) |
| I1 | PASS | sin Restart en service; retry = próximo vencimiento |
| I2 | PASS | cero diffs en internal/* y cmd/mofgw* |
| I3 | PASS | todos los paths por install_unit_file; mv byte-exacto; uninstall no borra .bak |
| I4 | PASS | env file solo referenciado, jamás generado/tocado |
| I5 | PASS | NUEVOS: 2 units + unitfiles_test.go; MOD: install.sh + test-install.sh; audit-lock: diff RED→GREEN del golden vacío; el harness nuevo vino junto a su implementación (desviación blanqueada en test-audit §5) |
| I6 | PASS | dies claros (src ausente, build fallido, agenda no confirmada); C7 fail-loud |
| I7 | PASS | install.sh COPIA los commiteados; cmp byte-exacto en harness |

## Capa 2 — Findings

**F1 — Major (RESUELTO pre-merge):** P10 incumplida — `MOFGW_SYNC_VERIFY_KEY` sin documentar en header/README → F3 nunca se activaría (degradación perpetua, modo silencioso anti-I6). Fix: líneas de header + assert en harness. Sin cambio de comportamiento.
**F2 — Major follow-up (DECISIÓN HITL: warning ahora + absorción en 019-007):** el backup salva el ARCHIVO, no el COMPORTAMIENTO — el template del server sigue sin env-file/flags, así que re-instalar deja un server que arranca distinto. Resuelto ahora con aviso LOUD post-install cuando hubo divergencia del server ("conciliar manualmente env-file/flags/ customs"). La absorción de env+flags al template NO va en 006 (spec I5/P4 lockean el template byte-intacto) → scope de 019-007.
**F3 — Minor (RESUELTO):** `TestSyncUnitPairing` tautológico (literales comparados contra sí mismos) → reescrito: deriva el par del FS (mismo stem) + documenta que la garantía real del emparejamiento es B2 (ausencia de `Unit=`).
**F4 — Minor (RESUELTO):** granularidad de timestamp 1s (`date +%Y%m%d%H%M%S`) → `%N` en install_unit_file y uninstall (2 installs en el mismo segundo ya no pisan el backup intermedio).
**F5 — Minor (RESUELTO):** C7 asumía `python3` presente → guard con SKIP explícito (sin python3 el test mediría entorno, no regresión).
**F6 — Advisory (aceptado):** C9 tolera "is not executable" SOLO para mofgw-sync.service (binario %h ausente en sandbox; territorio del canary B4); filtro estrecho — cualquier otra línea ERROR/Failed sigue fallando.
**F7 — Advisory (registrado):** sin Persistent no hay catch-up post-downtime con OnUnitActiveSec puro — semántica HITL-aceptada (sync idempotente y convergente). `Type=oneshot` colapsa solapes; timeout de arranque infinito cubre el peor caso.
**Hallazgo adicional del loop (compgen + set -e):** `compgen -G` sin matches retorna 1 y con `pipefail` mataba install.sh en sandbox fresco → fix `|| true` documentado en el código. Preexistente en potencia; ahora blindado.

**Sin hallazgo:** emparejamiento por nombre (systemd.timer(5)/service(5)); flake `TestPostcondition9_MuestreoBanda` (estadístico preexistente, registrado junto a TTLExpiry).

## Disclosure anti-bias

Reviewer: **GLM (Zhipu AI, glm-5.3-flash)** en sesión fresca, sin participación en RED/GREEN (implementer: orquestador inline, también GLM) → review mono-familia como única capa externa (degradación A2 del epic, registrada). Mitigaciones: test-audit commiteado antes del GREEN + verificación evidencia-a-evidencia + escepticismo máximo declarado. Se ejecutó con diff de commits, greps de invariantes y lectura de artefactos (bash restringido, sin re-ejecución propia de suite — la del orquestador se audita por coherencia).

## Conclusión

F1/F2-warning/F3/F4/F5 aplicados pre-merge; suite **959/36 `-race` verde** + harness **40/40** (re-corridos por el orquestador). Advisories aceptadosdocumentados. El criterio del epic "Timer systemd activo con logs del sync" queda cerrado pendiente deploy real (canary B4 opt-in).

Status: Review **REQUEST_CHANGES** by cdad-reviewer (GLM/Z.ai) on 2026-09-17
Status: **Sign-off HITL** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-17 — findings resueltos pre-merge (F1 doc, F2 warning+scope 007, F3/F4/F5); F6/F7 aceptados+documentados. Gate 4→5 DESBLOQUEADO → merge + memory bank (etapa 5)
