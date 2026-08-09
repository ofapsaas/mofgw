# Epic 009 — Closure

Cerrado: 2026-08-09

## Resumen

El epic mofgw-009-contexto-analisis descubrió y optimizó la composición del contexto del tráfico real: la telemetría de descubrimiento (009-000) reveló dónde viven los ids de sesión (solo opencode manda `X-Session-Id`; openclaw/zot no), y sobre esa evidencia se diseñaron la composición de contexto con endpoint `/v1/context` (009-001) y el sticky routing por sesión para maximizar cache hit (009-002). Las 3 features están done e integradas juntas: 332 tests `-race`, E2E cross-feature verde, sin bugs cross-feature. El epic queda cerrado con las features desplegables vía config (default off).

## Features entregadas

| Feature | Descripción | Cierre |
|---|---|---|
| 009-000-request-telemetry | Telemetría de metadata de requests (allowlist headers + key paths + conteos) a `telemetry.jsonl` — desplegada en prod | 09 Ago 2026 |
| 009-001-context-composition | Composición estructural del contexto + endpoint `GET /v1/context?session=<id>` (dual-keying, ADR-001) | 09 Ago 2026 |
| 009-002-sticky-session | Afinidad de provider por sesión/cliente para cache hit (reordenamiento post-filtro, ADR-002) | 09 Ago 2026 |

## Criterios de aceptación

- ✅ **C1** — `telemetry.jsonl` captura headers + paths de claves del tráfico real sin contenido ni keys (privacidad 005-005): subtest `privacidad_grep_negativo` + grep negativo en E2E.
- ✅ **C2** — `docs/research-context-patterns.md` con hallazgos por runtime (dónde vive la sesión): captura ~103 eventos; opencode → `X-Session-Id` en header; openclaw/zot → sin sesión (`detected_ids` = 0 en todos).
- ✅ **C3** — `/v1/context?session=<id>` devuelve composición (roles, part types, tamaños, evolución) con aislamiento por cliente: subtests `request1_*`/`request2_*`/`request3_*` + `aislamiento_otro_cliente_misma_sesion`.
- ✅ **C4** — Sticky routing opcional por config (`fallback.sticky_routing`), transparente, respeta cooldown/health/cadena: subtests `request2_sticky_mismo_provider` + `request3_sin_sesion_openclaw_zot`.

## Retrospectiva breve

### Lo que funcionó bien
- **Telemetría primero / observar antes de diseñar:** la hipótesis de Pablo ("session id en metadata del ctx") se descartó con data real (~103 eventos); 009-001/009-002 se diseñaron con la fuente de sesión confirmada, no con conjetura. El costo de observar (una feature chica, 009-000) pagó la corrección de las dos siguientes.
- **Contratos cross-feature alcanzaron:** integración E3 sin refactor de features done y sin bugs cross-feature; el dual-keying (ADR-001) se comportó exactamente como diseñado en el flujo real, con y sin sesión.

### Lo que se complicó
- **Bugs de tests en GREEN en 2 features del epic (009-001 y 009-002):** el AUDIT previo al RED no se ejecutó como sesión formal → tests nuevos nacieron rotos y se detectaron en GREEN (el implementer respetó AP-4 y no tocó tests; los fixes fueron en sesión B, sin relajar asserts). Lección de contadores (009-002) y de fixtures (009-001) pagada dos veces en el mismo epic.

### Aprendizajes para futuros epics
- **AUDIT formal antes del RED, siempre** — no solo para fixtures existentes, también para los tests nuevos que el RED va a escribir.
- **Los contadores deben medir lo que el test afirma** (`fakeProvider.Stream` no cuenta en `callCount`; el health check contamina contadores; `upstreamOK` no produce costo → el budget nunca dispara).
- El AUDIT de una feature con bump de schema debe escanear literales de fixture que el spec cambia, no solo call-sites de firma.

## Deuda técnica que se llevó

- **TECHDEBT #20** — gofmt preexistente en `e2e_009000_test.go` (LOW; correr `gofmt -w` cuando se toque el archivo).
- **TECHDEBT #21** — concurrencia sticky sin test dedicado (LOW; garantía estructural mutex + `-race`; test dedicado → hardening post-cierre).
- **TECHDEBT #22** — `X-Session-Affinity` irrelevante sin test dedicado (FYI; garantía por construcción; test dedicado → hardening post-cierre).
- Nits sin registro de la review 009-002 (idiom `evictLRU`; `AffinityStore` incondicional) — aceptados, sin acción de mejora.
- Las dos lecciones de proceso (AUDIT post-hoc 009-001/009-002) quedan documentadas en TECHDEBT §Deuda de proceso, no como deuda técnica.

## Decisiones arquitectónicas tomadas

- [ADR-001 — Composición de contexto keyed por sesión (opencode) y por client_id (openclaw/zot): dual-keying simultáneo](docs/adr/ADR-001-dual-keying-composicion-contexto.md) — creado en la feature 009-001.
- [ADR-002 — Afinidad sticky como reordenamiento post-filtro (nunca fuerza cooldown/health/cadena)](docs/adr/ADR-002-sticky-afinidad-reordenamiento-post-filtro.md) — creado en la feature 009-002.
