# Process Log — Epic 019 provider-sync-automation

Registro continuo de encuentros, decisiones de orquestación, fricciones y mejoras
candidatas al proceso/herramientas (fuera de alcance del código). Se analiza al
cierre de la epic.

## 2026-09-11 — Bootstrap de la epic

- **Delegación HITL:** Pablo delegó explícitamente la aprobación HITL al orquestador
  (Ofap) en goal mode. Precedente ya existía en stage_history ("agent-delegated HITL,
  pedido explícito de Pablo"). Registrado como `Ofap (agent-delegated HITL, pedido
  explícito de Pablo — goal mode)`.
- **Renumeración de features:** en la conversación previa se esbozaron IDs 020-026;
  la convención del repo es `<epic>-<NN>` (013-001, 016-002, 018-001) → features
  renumeradas a 019-001..019-007 bajo epic 019. *Mejora candidata:* los skills
  cdad-epic deberían explicitar esta regla de numeración para evitar IDs huérfanos.
- **Convivencia con epic pausada:** 017-mofgw-client-hot-reload sigue pausada
  (017-001 en tdd-audit). El state file tiene un solo `active_epic`; al activar 019
  queda 017 referenciada solo en notes/epic_history. *Mejora candidata:* soportar
  epics paused/parked explícitamente en el schema del state file (lista, no campo único).
- **Riesgo mapeado pre-spec:** yaml.v3 no preserva comentarios en round-trip →
  decisión de 019-004 (regeneración acotada vs. edición estructural) adelantada al plan.
- **Digest/anti-rewrite:** lección externa (opencode PR #44282) incorporada al plan:
  comparar sha256 del body para no reescribir caches ni señalizar cambios byte-idénticos.

## 2026-09-11 — Discovery 019-001 (cdad-architect, ses_f6ef80bacffenVeaKRKGjl7Vfn)

- **Corrección al contexto recibido (importante):** `-check-config` NO existe en el
  binario mofgw — el skill `mofgw-provider-sync` lo anuncia aspiracionalmente (línea 72).
  Deuda registrada; la validación real hoy es `config.Parse` en proceso. *Mejora
  candidata:* auditar skills vs. binario real antes de especificar (los skills pueden
  documentar aspiraciones).
- **Hallazgo de datos:** `models.dev/api.json` real = 4.59 MB, 213 providers, 7.711
  modelos; `supported_parameters` y `variants` NO vienen (0 ocurrencias) — opencode los
  deriva mecánicamente de tool_call/structured_output/temperature/reasoning. La
  derivación queda para el spec de 019-003.
- **Delegación:** primer intento con `delegate` falló (cdad-architect es write-capable
  → requiere `task`). *Mejora candidata:* el contracto "read-only vía delegate" choca
  con cómo están definidos los perfiles instalados de cdad-architect; ajustar
  `references/opencode-delegation.md` o los perfiles de agente.
- **Decisiones HITL lockeadas (delegado):** D1 cache en `~/.cache/mofgw` vía
  os.UserCacheDir (env override `MOFGW_CACHE_DIR`); D2 binario nuevo `cmd/mofgw-sync`;
  D3 paquete `internal/modelsdev`; D4 fail-soft en runtime / exit-code al final; D5
  env `MOFGW_DISABLE_MODELS_FETCH` + flag `--no-fetch`; D6 digest sha256 sidecar
  `.sha256`; D7 logs slog con eventos fetch_ok/fetch_failed/cache_hit/skipped_identical;
  D8 TTL default 5m.

## 2026-09-11 — Etapa 2 (spec 019-001) + inicio TDD

- Spec draft materializado y committeado (9a4f1ee) desde output del arquitecto; aprobado
  como HITL delegado (837639a). 17 P / 10 I / 12 C; RED por compilación.
- **Hallazgo de routing (nuevo):** los perfiles instalados de agentes invierten el
  contrato CDAD: `cdad-architect` es write-capable (requiere task) y `cdad-test-writer`
  es read-only con bash DENIED (requiere delegate, no puede ni correr la suite).
  *Mejora candidata (importante):* alinear perfiles instalados con el contrato de roles
  (architect read-only; test-writer write en tests/** + bash para run de tests) o
  documentar en references/opencode-delegation.md la matriz real instalada. Workaround
  actual: role-work conductual + orquestador ejecuta las verificaciones (build/vet/test)
  y materializa artefactos.
- **Baseline verificado por orquestador (evidencia):** go build OK, go vet OK,
  `go test ./... -count=1` = 733 passed / 29 pkgs. AUDIT delegado (striped-black-bedbug).

## 2026-09-11 — RED + GREEN 019-001

- RED: f99c061 (19 tests B1-B11, RED por compilación, 0 sintaxis; desviación justificada:
  package modelsdev externo produce "no non-test Go files" que NO distingue símbolos →
  test file del propio paquete, convención del repo). Lección: la "RED por compilación"
  con paquete test-externo es imposible de distinguir de un paquete vacío — documentar
  esta trampa en odoo/test-writer references del framework.
- GREEN: cdad-implementer escribió el paquete (3 archivos, 596 líneas) pero su bash
  quedó bloqueado por el matcher de permisos del runtime (solo pasaba `pwd`). Gates
  2-5 ejecutados por el orquestador con evidencia (gofmt limpio, suite nueva 28 pasan,
  completa 761/30 -race verde, vet+build OK; commit 226728c). *Mejora candidata
  (crítica):* el permission-matching de bash en subagentes task es frágil (den `*`
  gana sobre allows); 018-001 ya había sufrido delegate roto. Priorizar fix de
  harness de delegación — es el 3er incidente de este tipo.
- Contador de tests del harness (rtk) parece contar subtests, no funciones (19 funcs
  → 28 en rtk). *Mejora candidata:* unificar métrica de conteo entre reportes de roles
  y verificación del orquestador.

## 2026-09-11 — Review 019-001 + loop de fixes

- Review REQUEST_CHANGES (premier-red-meerkat): 2 bloqueantes resueltos con loop
  disciplinado — test-writer RED discriminante (c0489bc: TestFetch_TimeoutBudget_NoRetry
  falla hits==2) → implementer fix (retryable timeout→false, sidecar-first persist,
  classifyTransport DeadlineExceeded/Canceled, fallback TempDir) → suite 763/30 -race
  verde verificada por orquestador (bash de subagentes bloqueado en ambos roles,
  2do y 3er incidente del mismo matcher).
- **Hallazgo de valor del reviewer:** el spec era internamente contradictorio (P4 vs
  D9) y el test del audit no discriminaba (falso negativo). La revisión independiente
  valió el costo — evitó perpetuar 2 intentos de 10s en producción. *Mejora candidata:*
  regla del framework — todo par "postcondición + decisión D" debe cruzarse explícito
  (matriz P↔D) en la auto-revisión del spec; la contradicción se detectó tarde.
- Anti-bias degradado (A2): reviewer corrió en deepseek-v4-flash, misma familia que el
  implementer. Capa 2 (HITL) validó los bloqueantes por inspección directa antes de
  decidir. *Mejora candidata:* el profile de cdad-reviewer no fija modelo distinto —
  revisar instalador/routing.

## 2026-09-11 — Ciclo 019-002 completo

- **Incidente de delegación (2do tipo):** el primer intento de RED volvió con resultado
  vacío (sesión fallida silenciosa, 0 archivos). Reintento inmediato OK. *Mejora
  candidata:* los task handlers deberían validar "output no vacío" y reintentar
  automático, o el orquestador verficar `git status` antes de asumir éxito/fallo.
- **Loop de GREEN (3 defectos encontrados):** (1) import `net/http` faltante en el
  motor — corrección mecánica aplicada por el orquestador con disclosure (código ya
  entregado por implementer; su bash bloqueado); (2) bug de fixture de TEST
  (writeCacheFile sin MkdirAll en subdir) → test-writer aislado (0ba81ab, AP-4);
  (3) defecto de implementación top_provider no hidratado (json tags) → implementer
  (mismo patrón pointer→value extendido). Suite final 806/32 -race verde.
- **Lección de review (capturada por scribe):** cuando el mecanismo se generaliza a N
  fuentes, el STRING de error deja de ser superficie de contrato — la identidad es
  errors.Is/As. El bloqueante cosmético se resolvió sin churn (aceptar+documentar).
- **Hallazgo empírico clave:** `supported_parameters` presente en los 443 modelos de
  OpenRouter vs 0 en models.dev — la decisión de 001 (no derivar lo que upstream no
  expone) queda validada y el gap se convirtió en work-item concreto (R5, 019-003).
- Bash de subagentes (implementer/reviewer/test-writer) bloqueado en TODAS las sesiones
  del ciclo; orquestador ejecutó gates+commits. Es el incidente más persistente del
  harness — *mejora candidata PRIORITARIA.*
