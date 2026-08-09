# activeContext.md — Contexto activo de mofgw

> Memory Bank: estado actual, decisiones recientes, próximos pasos, deuda conocida.
> Última actualización: 2026-08-09 (cdad-scribe, feature 009-002 closed — EPIC-009 3/3 done).

## Estado del programa

**🔜 EPIC-009: 3/3 features DONE (009-000 / 009-001 / 009-002). Pendiente: integración cross-feature (E3) + closure del epic (E4). Resto de epics DONE.**

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
| EPIC-009 (contexto-análisis) | 3 features | 🔜 3/3 DONE — pendiente integración (E3) + closure (E4) | 009-000 ✅ / 009-001 ✅ / 009-002 ✅ done |

**Suite:** 14 paquetes, 326 tests verde con `-race`. vet + gofmt limpios (salvo e2e_009000_test.go, TECHDEBT #20).
**Deploy:** systemd user service activo en puerto 3369, providers reales (acct1, acct2, qwen/bailian, zen free).
**Verificación E2E:** smoke test con config real + cliente zot → happy path, streaming, fallback, auth, /healthz.

## Decisiones recientes (cronología inversa)

### 09 Ago 2026 — Feature: 009-002-sticky-session (cierre de ciclo — última del epic)

## 2026-08-09 — Feature: 009-002-sticky-session

### Decisiones relevantes

- **Afinidad como reordenamiento post-filtro (I2/P9) — el corazón de la feature:** `applyStickyReorder` actúa SOLO sobre el slice `ready` post-`resolveReady` (ya post-cooldown/health/Serves): si el preferido está en `ready`, se mueve al frente preservando el orden relativo del resto; si NO está (cooldown, unhealthy, ya no sirve el modelo, clave ausente/evictada, stickyKey vacío) → slice intacto, comportamiento IDÉNTICO a legacy. NUNCA fuerza un provider que el filtro descartó, nunca cambia `maxAttempts` ni la clasificación de errores. Garantiza el criterio del epic (plan.md:215, "respeta cooldown/health/cadena") y la transparencia total (P7/I1/D6): el cliente nunca percibe el ruteo; única diferencia observable es el log debug `sticky_applied`. → **ADR-002**.
- **Keying `client|session` + `client|` (P2, consume ADR-001):** misma fuente de sesión que 009-001 — `X-Session-Id` solo lectura, nunca upstream (I4). opencode → `clientID|sessionID` (afinidad por sesión real); openclaw/zot sin sesión → `clientID|` (afinidad por cliente, comportamiento deseado según captura 009-000). `X-Session-Affinity` deliberadamente NO se usa (D5/I3: en opencode es idéntico a `X-Session-Id`). `clientID` nunca vacío (token autenticado) → clave nunca `""`.
- **`AffinityStore` efímero LRU (P3/D3/D4):** store en memoria (patrón `CooldownStore`; restart → frío; sin bump de `stateVersion`), evicción LRU por last-used (Set y Get refrescan — un cliente activo no se evicta, C12), cap = `Options.StickyMaxEntries` REUTILIZANDO el MISMO knob `server.max_sessions_retained` (default 100): D1 limita la config a `enabled`, el operador gobierna retención de sesiones y afinidad con un solo knob. Claves `client|` cuentan contra el mismo tope; pérdida benigna (re-registra en frío).
- **`CompleteFor`/`StreamFor` con backward compat total (P8/D2):** refactor del loop de `Complete`/`Stream` a helpers compartidos (`complete`/`stream` + `applyStickyReorder`); `Complete`/`Stream`/`New` legacy intactos (stickyKey `""` → retorno inmediato); `StickyMaxEntries` nuevo en Options (0 = default). Con sticky off (default) el proxy llama legacy → cero diferencias observables (C11), e2e existentes pasan sin cambios.
- **Registro post-éxito del GANADOR (P6):** en `recordCacheTokens` (punto único post-éxito con providerID para stream y no-stream), gated por `s.stickyRouting` (off → store vacío, cero memoria). Registra el provider que RESPONDIÓ/arrancó el stream (no necesariamente el preferido). Fallo total (502) o rechazos pre-router (400/413/limiter/budget/ventana) NO tocan la afinidad (C9); stream arrancado que muere a mitad SÍ registra (cache calentado). Best-effort: error de registro jamás falla el request.
- **No invasivo + bounds (P9):** sin cambios en limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta)/telemetría; sin métricas nuevas (solo log debug); cardinalidad acotada (LRU) + mutex, seguro concurrente. Limitación aceptada (D4): afinidad por clave, no por `(clave, modelo)` — el óptimo de cache del segundo modelo puede no alcanzarse; corrección siempre garantizada vía `Serves`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes, 5 opcionales (4 aceptados como deuda #21-#22 + nits sin registro; 1 aplicado — Nit #5: `max_sessions_retained` documentado en `config.example.yaml`). Suite completa 326 tests `-race` verde (294 + 32 nuevos), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Feature aditiva (test-audit):** 0 tests existentes modificados, 38 archivos untouched, 32 tests nuevos (3 config + 18 router + 11 e2e) que cubren P1-P9 / C1-C13; 5 fixes de tests NUEVOS documentados (§5.2) con lección: **los contadores deben medir lo que el test afirma** (`fakeProvider.Stream` no cuenta en `callCount`; el health check de `buildSticky` contamina contadores; `upstreamOK` no produce costo → budget nunca dispara).
- **Operativo:** la feature cierra el ciclo (merge) y el EPIC-009 (3/3 done). Habilitar `fallback.sticky_routing.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo) — junto con `context.analysis.enabled` de 009-001.

### Deuda técnica detectada

- **TECHDEBT #21** — concurrencia sticky sin test dedicado (C13): garantía estructural (mutex de `AffinityStore`) + `-race` en suite; sin TOCTOU. Test concurrente dedicado → hardening post-cierre.
- **TECHDEBT #22** — `X-Session-Affinity` irrelevante sin test dedicado (I3/D5): garantía por construcción (proxy solo lee `X-Session-Id`) + guard `e2e_008003` untouched. Test dedicado → hardening post-cierre.
- **Nits de la review sin registro** (aceptados, sin acción de mejora): `evictLRU` con flag `first` (idiom Go correcto, refactor cosmético opcional); `AffinityStore` creado incondicionalmente (costo ~120 bytes, consistente con `CooldownStore`); `Get` refresca siempre `LastUsed` (semántica EXACTA del spec P3, revisado "no cambiar").
- **Lección de proceso (test-audit §2/§5.2):** AUDIT previo al RED no ejecutado como sesión formal → 5 de los 32 tests nuevos nacieron con bugs de test (detectados en GREEN: 3 por el implementer respetando AP-4, 2 por el orquestador). Lección: además de escanear fixtures (009-001), escanear que los contadores/harness midan lo que el test afirma medir. Ver entrada de proceso en TECHDEBT.

### Próxima feature en cola

- **Ninguna del epic 009 — las 3 features están done.** Siguiente paso: **integración cross-feature (E3) y closure del epic (E4)** vía `cdad-epic` (chat nuevo): verificar los criterios de aceptación del epic (telemetry.jsonl capturando; `docs/research-context-patterns.md`; `/v1/context?session=<id>`; sticky routing opcional por config transparente que respeta cooldown/health/cadena — este último cubierto por 009-002), habilitar las features en prod y decidir el cierre formal.

### 09 Ago 2026 — Feature: 009-001-context-composition (cierre de ciclo)

## 2026-08-09 — Feature: 009-001-context-composition

### Decisiones relevantes

- **Dual-keying confirmado por captura (~103 eventos) — decisión de cierre del epic resuelta:** opencode manda `X-Session-Id` en header → composición keyed `client|session`; openclaw/zot NO mandan sesión (ni header ni metadata del body, `detected_ids` = 0 en todos los eventos) → composición keyed `client|` (agregado por client_id, fallback diseñado). Ambos keyings conviven simultáneamente en runtime (P2/C15). Fuente: `docs/research-context-patterns.md`. → **ADR-001**.
- **Endpoint `GET /v1/context` con aislamiento por clientID** (auth Bearer sobre `/v1/*`): `?session=<id>` → `scope:"session"` con 404 si la sesión no existe para el cliente (consistente 008-003 P5); sin session → `scope:"client"` con el agregado `client|` (incluye TODO el tráfico del cliente, con y sin sesión). Shape `{client, scope, session?, requests, summary, latest, history}`, ceros/vacío nunca nil (P3).
- **Parser estructural en UN solo recorrido (`composition.Analyze`):** superset de `Walk` (absorbe el refactor del audit 009-000 R1); `Walk` intacto y verificado semánticamente idéntico (C3, schema lock R10 de telemetría verde); privacidad por construcción — solo metadata estructural, nunca contenido (P7/I1).
- **`prompt_tokens_actual` en DOS fases:** fase 1 pre-request (mismo gate que telemetría: body válido, stream y no-stream, incluidos los rechazos por limiter/budget/ventana/upstream — P4/C9); fase 2 post-response con matcheo exacto por `request_id` (evita races entre handlers concurrentes de la misma sesión). Best-effort: error de análisis jamás falla el request (I7).
- **stateVersion 1→2 con backward compat (P5/I6):** `persistedState` gana `Contexts` (`omitempty`, nil-safe para v1); binario nuevo lee snapshot v1 sin error; binario viejo rechaza v2 con el check existente (persist.go:243); round-trip preserva contexts con history.
- **Config `context.analysis` aditiva (P6):** `enabled=false` default (sin records ni memoria; endpoint 200 con ceros/vacío), `history_per_session=50` (0 = sin history, solo agregados), valida `< 0`; INDEPENDIENTE de `telemetry`. Wiring pre-tráfico `srv.SetContextAnalysis(...)` + `m.SetContextHistoryPerSession(...)` (patrón SetContextMargin/SetMaxSessionsRetained, main.go:217-218).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes (1 reportado desestimado con motivo escrito — nil-slices falso positivo: `make(..., 0, len(...))` nunca emite `null`, `[]` garantizado), 5 opcionales aceptados como deuda documentada (#16-#20). Suite completa 294 tests `-race` verde, vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Operativo:** la feature cierra el ciclo (merge). Habilitar `context.analysis.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo).

### Deuda técnica detectada

- **TECHDEBT #16** — redundancia `SetContextHistoryPerSession` en main.go:218 (defensa en profundidad; limpiar en refactor futuro).
- **TECHDEBT #17** — casing PascalCase de `PartType` a nivel entry del endpoint (vs snake_case del summary; consumidores propios no dependen).
- **TECHDEBT #18** — `latest: null` vs "ausente" con `history_per_session=0` (cosmética; representación JSON válida, e2e test la espera).
- **TECHDEBT #19** — ring buffer con prepend O(N) en context.go:171 (FYI de hardening; default 50 → despreciable; deque circular O(1) futuro).
- **TECHDEBT #20** — gofmt preexistente en `e2e_009000_test.go` (preexistente en HEAD, fuera del diff de esta feature; correr gofmt cuando se toque el archivo).
- **Lección de proceso (test-audit §2/§9.1):** AUDIT previo al RED no ejecutado como sesión formal → `TestPersistVersionReject` falló en GREEN porque el bump P5 cambia un literal de fixture (`"version":1` → `"version":2`) y el replace era no-op. Corregido en sesión B (6e28730) preservando la intención. Lección: el AUDIT de una feature con bump de schema debe escanear literales de fixture que el spec cambia, no solo call-sites de firma.
- (menor, test-audit §9.3) acceso a campo interno en `TestPersistContextV2_BackwardCompatV1` (persist_context_test.go:39-44) — alternativa observable (`ContextSnapshot(client, "")`) recomendada para hardening futuro.

### Próxima feature en cola

- **009-002-sticky-session** (pendiente del epic mofgw-009): afinidad de provider por sesión para maximizar cache hit. Consume la composición de 009-001 y la fuente de sesión confirmada (`X-Session-Id` de opencode; client_id para openclaw/zot). Coordinar vía `cdad-epic` (chat nuevo) — el orquestador sugiere volver al epic al cerrar esta feature.

### 09 Ago 2026
- **Feature 009-000-request-telemetry DESPLEGADA en producción:** telemetría de requests activa (`telemetry.enabled: true, sample_rate: 1`), archivo `/home/<user>/logs/mofgw-telemetry.jsonl` (0640). Ciclo CDAD completo: spec → audit → RED → GREEN → review (APPROVE qwen3.7-plus, 0 bloqueantes). Suite 265 tests -race verde.
- **Hallazgo de infraestructura (causa raíz artifacts vacíos):** `fallback.timeout: 120s` cortaba streams largos de sub-agentes (el timeout aplica a todo el intento, no solo TTFB — discrepancia contrato-vs-impl). Fix en prod: 120s→300s. Documentado en `docs/hallazgos/2026-08-09-timeout-streams-largos.md`. Fix de diseño (TTFB-only vs stream_timeout) pendiente en backlog.
- **Descubrimiento en curso (24-48h):** telemetría capturando tráfico real. Primeros datos confirman la investigación — opencode manda X-Session-Id en header; el runtime OpenAI/JS (zot/openclaw) NO manda sesión ni en headers ni en metadata del body.

### 08 Ago 2026
- **Cierre formal del replanteo de eficiencia:** P4 CERRADO — Memory Bank aprobada por Pablo, `.cdad-state.json` marcado epic-closed (006/007/008 done). Validación externa CERRADA (7 APPROVE + 2 REQUEST CHANGES corregidos).
- **EPIC-009 planificado y aprobado:** telemetría de tráfico (009-000) → composición de contexto (009-001) + sticky routing por sesión (009-002). Fase 1 = telemetría: loguear headers/metadata del body del tráfico real para descubrir dónde viven los ids de sesión (investigación confirmó: solo opencode manda X-Session-Id; openclaw y zot no). Destino: telemetry.jsonl dedicado.

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

1. **EPIC-009: 3/3 features DONE** (009-000 desplegada; 009-001 y 009-002 con ciclo cerrado, default off en prod). Siguiente: **integración cross-feature (E3)** — verificar criterios de aceptación del epic (plan.md:211-215) y habilitar `context.analysis` + `sticky_routing` en config de prod — y **closure del epic (E4)**. Volver a `cdad-epic` (chat nuevo) para coordinar.
2. **Criterios de aceptación del programa** (ver plan.md §criterios): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes.
3. **Deuda técnica** (ver `docs/TECHDEBT.md`): 22 entradas registradas (#21-22 = deuda 009-002), varias cerradas, resto BAJA/FUTURO.
4. **Budget para cliente zot/OpenClaw** (decisión de Pablo pendiente — evaluado y diferido 08 Ago: el gasto es intencional, no se limita).

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
