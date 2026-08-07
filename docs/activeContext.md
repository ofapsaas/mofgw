# activeContext.md — Contexto activo de mofgw

> Memory Bank: estado actual, decisiones recientes, próximos pasos, deuda conocida.
> Última actualización: 2026-08-07 (cdad-scribe, Memory Bank creation).

## Estado del programa

**✅ TODOS LOS EPICS IMPLEMENTADOS Y DESPLEGADOS.**

| Epic | Features | Status | Commit evidence |
|------|----------|--------|-----------------|
| EPIC-001 (core) | 7 features | ✅ DONE | e426f15d |
| EPIC-002 (resiliencia) | 4 features | ✅ DONE | 5966c5f4 |
| EPIC-003 (cache) | 1 feature | ✅ DONE | bb6367b→e5a9a16 |
| EPIC-004 (escala) | 2 features | ✅ DONE | (05 Ago) |
| EPIC-005 (ops) | 5 features | ✅ DONE | (05 Ago) |
| EPIC-006 (observabilidad) | 2 features | ✅ DONE | 119fe9d→2846c12 |
| EPIC-007 (catálogo) | 3 features | ✅ DONE | (06 Ago) |
| EPIC-008 (contexto) | 3 features | ✅ DONE | (06 Ago) |
| SEC-001 (hardening) | 4 fixes | ✅ DONE | b368656 |

**Suite:** 14 paquetes, 210+ tests verde con `-race`. vet + gofmt limpios.
**Deploy:** systemd user service activo en puerto 3369, providers reales (acct1, acct2, qwen/bailian, zen free).
**Verificación E2E:** smoke test con config real + cliente zot → happy path, streaming, fallback, auth, /healthz.

## Decisiones recientes (cronología inversa)

### 07 Ago 2026
- **Memory Bank creada** (cdad-scribe): `docs/projectbrief.md` + `docs/activeContext.md` — Etapa 5 del epic cycle completada.
- **GAP 1 resuelto:** `docs/specs/external-reviews/` ya existe con 9 `.resp.json` (evidencia de validación externa). El reporte del orchestrator era incorrecto.

### 06 Ago 2026
- **Replanteo de eficiencia completo:** EPIC-003/006/007/008 — 9 features, todas spec→audit→RED→GREEN→review.
- **SEC-001 security hardening:** 4 fixes de auditoría externa (SSE saneado, X-Agent-Id truncado+TTL, ReadHeaderTimeout, log 0640). Review con qwen3.7-plus (familia distinta).
- **Persistencia de accounting:** SaveState/LoadState JSON atómico (commit f90c0b7).
- **Validación externa:** 7 APPROVE + 2 REQUEST CHANGES. Ambos Majors corregidos (commit 082d10c).
- **Pricing/metadata reales** cableados en config de producción.

### 05 Ago 2026
- **EPIC-001 MVP completo** (commit e426f15d, 27 files, 3808 ins).
- **EPIC-002 completo** (commit 5966c5f4, 133 tests PASS).
- **EPIC-004/005 implementados y desplegados.**
- **Hotfix 001-001-endpoint-fix-content-array:** mofgw devolvía 400 a OpenClaw (content array vs string). Fix: `Messages` → `[]json.RawMessage`.
- **Feature 005-005-verbose** implementada (flags --verbose/--log-file, privacidad testeada).
- **Feature 001-003-fallback-v2** implementada (max_retries como tope de intentos totales, cooldown entre requests).

### 04 Ago 2026
- **Plan de epics aprobado** por Pablo (sesión Telegram) — con auth in-scope del MVP.
- **Todas las specs escritas** (EPIC-001 7/7, EPIC-002 4/4, EPIC-005 4/4).

### 03 Ago 2026
- **Decisiones fundacionales** (Pablo): nombre mofgw, transparencia total, puerto 3369, deploy systemd user, config paths.

## Próximos pasos

No hay features pendientes. El programa está completo. Posibles evoluciones futuras (no comprometidas):

1. **Criterios de aceptación del programa** (ver plan.md §criterios): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes.
2. **Deuda técnica** (ver `docs/TECHDEBT.md`): 15 entradas registradas, 3 cerradas, 12 abiertas (mayoría BAJA/FUTURO).
3. **Cablear pricing/metadata reales** de todos los modelos en config de producción (parcialmente hecho).
4. **Budget para cliente zot/OpenClaw** (decisión de Pablo pendiente).

## Conocimiento operativo clave

- **Config de producción:** `~/.config/mofgw/config.yaml` — providers reales, pricing Zen, context.margin 0.1.
- **State file:** `~/.config/mofgw/state.json` (0600) — contadores de accounting, persistidos cada 10min.
- **Logs:** `/home/<user>/clawd/projects/mofgw/logs/` (permisos 0640).
- **Service:** `systemctl --user status mofgw` — active + enabled.
- **RAM:** ~12MB en operación normal.
- **Cache hit rate:** 94-99% medido en /metrics.

## Archivos de referencia

| Archivo | Qué contiene |
|---------|-------------|
| `docs/projectbrief.md` | Identidad, decisiones irreversibles, epics |
| `docs/activeContext.md` | Este archivo — estado actual, decisiones recientes |
| `docs/epics/plan.md` | Plan completo de epics con decomposición |
| `docs/research-architecture.md` | Arquitectura recomendada (ReverseProxy + cooldown RAM) |
| `docs/research-token-efficiency.md` | Precios verificados, cache providers, thinking capabilities |
| `docs/research.md` | Competidores y decisiones iniciales |
| `docs/USER-GUIDE.md` | Guía de usuario (instalación, config, endpoints) |
| `docs/TECHDEBT.md` | Deuda técnica consolidada (15 entradas) |
| `docs/specs/` | Specs de cada feature (27 directorios) |
| `docs/specs/external-reviews/` | Evidencia de validación externa (9 .resp.json) |
| `docs/specs/CROSS-SPEC-REVIEW.md` | Revisión de consistencia entre specs EPIC-001 |
| `task_plan.md` | Plan de tareas con estado de cada feature |
| `.cdad-state.json` | Estado del ciclo CDAD |
