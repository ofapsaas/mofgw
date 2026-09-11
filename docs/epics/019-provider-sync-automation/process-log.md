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
