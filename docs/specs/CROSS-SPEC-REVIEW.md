# Cross-Spec Review — EPIC-001 mofgw-core (001-001 → 001-006)

> Revisión de consistencia entre las 6 specs del EPIC-001 (04 Ago 2026, ciclo heartbeat 07:59).
> Método: lectura completa de las 6 specs + plan.md (contratos cross-feature) + research-architecture.md §6.
> Estado: 5 fixes aplicados (ver §4). Revisar de nuevo tras cambios sustanciales.

## 1. Resumen

| Feature | Spec | Consistencia | Notas |
|---|---|---|---|
| 001-001-endpoint | 228L | ✅ ok (1 wording) | "alias" sugiere mapping que no existe en MVP |
| 001-002-config | 241L | ⚠️ 2 majors | falta `fallback.timeout`; semántica 3-estados de timeout sin resolver; semaphore sin marcar |
| 001-003-fallback | 252L | ✅ ok (1 orden) | consume ErrTimeout definido en 001-006 — orden de implementación |
| 001-004-clamp | 126L | ✅ ok | re-clamp por provider consistente con retry pre-primer-byte |
| 001-005-streaming | ~110L | ✅ ok | completa; frontera primer-byte alineada con 001-003/001-006 |
| 001-006-timeouts | 147L | ⚠️ 1 major | contradicción interna "0 = sin timeout" vs "default 120s"; dep soft con 003 |

**Veredicto:** las 6 specs son implementables y coherentes en el 90% del contrato. Los 3 majors (M1-M3) son de schema/estructura, no de diseño — se corrigen en minutos y evitan rework en implementación.

## 2. Hallazgos MAJOR (corregir antes de implementar)

### M1 — `fallback.timeout` ausente en schema de 001-002, requerido por 001-006
- **Evidencia:** 001-006 `TimeoutFor()`: "per-provider > **global** > default (120s)"; consume `FallbackConfig.Timeout`. El schema YAML de 001-002 define `fallback:` solo con `max_retries`, `cooldown`, `cooldown_jitter` — no existe el tier "global".
- **Riesgo:** el tier global queda fantasmal; 001-006 se cubre con un hedge ("Si 001-002 aún no define el campo global..."), que es deuda técnica en un spec.
- **Fix (aplicado):** agregado `fallback.timeout: 120s` al schema de 001-002 + default documentado.

### M2 — Semántica de 3 estados para `timeout` (ausente vs 0 vs N) sin resolver
- **Evidencia:** 001-006 dice "Timeout 0 en config = sin timeout (default 120s si no se configura nada)" — contradicción interna: si 0 = desactivado, ¿qué significa "no se configura nada"? En YAML, campo ausente ≠ 0, pero `time.Duration` (no-puntero) colapsa ambos estados.
- **Riesgo:** un struct `time.Duration` no puede distinguir "usar default" de "desactivado explícitamente". Se necesita `*time.Duration` (nil = ausente → default; 0 = explícitamente sin timeout; N = N).
- **Fix (aplicado):** 001-002 documenta la semántica 3-estados para `timeout` (per-provider y fallback) con `*time.Duration`; 001-006 reescrito sin la contradicción. Nota: `cooldown` (001-003) usa semántica 2-estados (0 = usar global) — NO cambiar a 3 estados, está bien como está.

### M3 — Layout de paquetes desactualizado (research-architecture.md §6)
- **Evidencia:** el layout §6 define `cmd/mofgw` + `internal/{config,provider,router,proxy,metrics,logging}`. Las specs referencian 3 paquetes que NO están en el layout:
  - `internal/clamp/` (001-004, `go test ./internal/clamp/`)
  - `internal/timeouts/` (001-006, `go test ./internal/timeouts/`)
  - `internal/stream/` (001-005, `go test ./internal/stream/`)
- **Riesgo:** al implementar, alguien (humano o agente) ponga clamp/timeouts/stream dentro de proxy o router siguiendo el layout viejo, y los tests de las specs fallen por path.
- **Fix (pendiente):** actualizar research-architecture.md §6 con los 3 paquetes nuevos. clamp y timeouts como paquetes puros (cero red, testeables); stream como paquete propio (facilita el test del passthrough SSE sin acoplar a proxy).

## 3. Hallazgos MENOR

### m1 — 001-001 usa "alias" para el modelo (wording)
- 001-001: "Si no existe **alias** para el modelo pedido → 400". 001-003: match exacto en el MVP, alias mapping fuera de scope (EPIC-004). El wording de 001-001 sugiere que el alias existe en el MVP.
- **Fix (aplicado):** 001-001 ahora dice "si ningún provider sirve el modelo (match exacto en el MVP; alias mapping fuera de scope, EPIC-004)".

### m2 — Orden de implementación: ErrTimeout (001-006) consumido por 001-003
- 001-003 clasifica `ErrTimeout` como fallo retryable de provider, pero el tipo está definido en 001-006 (internal/timeouts). Si 003 se implementa antes que 006, el tipo no existe.
- **Fix (aplicado):** nota en 001-003 Integración: implementar el tipo `ErrTimeout` junto con 003 (o definirlo en un paquete de bajo nivel antes). La feature completa de 006 puede ir después; el TIPO no.

### m3 — `providers[].semaphore` sin consumidor en EPIC-001
- El schema de 001-002 incluye `semaphore: 8`, pero 001-003 excluye semáforos explícitamente ("→ 004-001-concurrencia"). Ninguna feature del MVP lo consume.
- **Fix (aplicado):** marcado como "reservado para 004-001-concurrencia (fuera del MVP EPIC-001)" para que el MVP no lo implemente ni lo valide de más.

### m4 — Dos fuentes de verdad para max_tokens de provider
- plan.md define `Provider.MaxTokens() int` en la interfaz; 001-004 clampea desde `ProviderConfig.MaxTokens` (config). Dos fuentes potencialmente divergentes.
- **Fix (recomendado, no aplicado):** config es la fuente de verdad (como ya hace 001-004). Documentar `MaxTokens()` como accessor conveniente que lee de config, o eliminarlo de la interfaz. Decidir en implementación — no bloquea.

## 4. Consistencia verificada ✅ (sin acción)

- **Frontera primer-byte:** 001-003, 001-005 y 001-006 coinciden: antes del primer byte → retry/fallback silencioso; después → stream comprometido, SSE error + [DONE]. Núcleo del diseño, alineado.
- **Formato de error SSE:** 001-005 cita exactamente el formato de 001-003 (`event: error` + `data: {"error":{"message":"<causa saneada>","type":"upstream_error"}}` + `[DONE]`).
- **Reescritura de `model`:** 001-001 (response JSON) y 001-005 (eventos SSE) — el cliente siempre ve el modelo que pidió.
- **`id` estable:** 001-005 extiende correctamente 001-001 (upstream si viene, sino `chatcmpl-mofgw-<hash>`), agregando estabilidad entre eventos del stream.
- **max_body_bytes:** 10MB → 413 (001-001) == `10485760` (001-002 schema).
- **Re-clamp en fallback:** 001-004 integración ("re-clampear con el límite de B") consistente con retry pre-primer-byte de 001-003/001-005.
- **Cooldown:** defaults 60s/5s idénticos en 001-002 y 001-003; override per-provider > global.
- **max_retries:** 001-002 ("hasta 3 providers") == 001-003 ("max_retries + 1 = 3 intentos").
- **Grafo de dependencias:** depends_on de cada spec == tabla del plan.md (001-002 raíz; 003→001+002; 005→001+003; 004/006→002).
- **MetricsSink:** no-op en MVP (001-003) consistente con EPIC-005 fuera de scope.
- **Semántica no-streaming:** buffer total + retry hasta agotar chain (001-003) sin frontera de primer byte — coherente con 001-005 §5.

## 5. Acciones pendientes

- [ ] M3: actualizar `research-architecture.md` §6 con `internal/clamp`, `internal/timeouts`, `internal/stream`.
- [ ] m4: decidir en implementación si `Provider.MaxTokens()` queda como accessor o se elimina.
- [ ] Re-verificar tras P1 approval (si Pablo pide cambios al plan, re-correr esta revisión).
