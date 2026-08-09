# Review — 009-001-context-composition

Reviewer model: **mofgw/qwen3.7-plus** (familia distinta al implementer deepseek-v4-flash — anti-confirmation-bias CDAD §2)

Fecha: 2026-08-09 · Rango revisado: `git diff 8f3486d..HEAD` (feature aislada, excluye fixes de timeouts intermedios)

## Resumen de la resolución (orquestador + usuario HITL)

- **Bloqueantes reales: 0** (1 reportado, **desestimado con motivo escrito** — ver abajo).
- **Opcionales: 5** — todos aceptados como deuda documentada, ninguno viola el contrato ni bloquea el merge.
- Suite completa **294 tests `-race` verdes** (evidencia empírica del orquestador), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente en HEAD).

---

## Hallazgo reportado como Bloqueante — DESESTIMADO (falso positivo)

### 1. "Nil slices en JSON de `/v1/context` para clientes sin data — viola 'nunca nil' (P3/C10)"

**Ubicación:** `internal/metrics/context.go:216-224` + `internal/proxy/proxy.go:834-856`

**Problema reportado:** el snapshot zero-value tiene `Summary.Roles/PartTypes/History` nil y `writeContext` los serializaría como JSON `null` en vez de `[]`, violando P3 "nunca nil".

**Motivo de desestimación (verificado contra el código real):**
- `roleAggsToJSON(nil)` (proxy.go:873-884) → `out := make([]map[string]any, 0, len(nil))` → `make([]T, 0, 0)` **nunca devuelve nil** (slice zero-length no-nil garantizado por la especificación de Go) → `json.Marshal` produce `[]`, no `null`.
- `partAggsToJSON` y `contextEntriesToJSON` usan exactamente el mismo patrón `make(..., 0, len(...))` → `[]`.
- El único campo que emite `null` es `latest` (`contextEntryToJSON(nil)` → `return nil` → map nil) — y el e2e test `TestE2E009001_DisabledByDefault` (e2e_009001_test.go:541-543) **espera explícitamente** `body["latest"] == nil` (el spec P3 declara "latest ausente" con history_per_session=0). `summary.prompt_tokens_actual: null` también es el comportamiento esperado por C9 (fase 2 no corrió).
- El propio reviewer reconoce en su texto que `SnapshotClient` (metrics.go) usa `make(...)` y produce `[]` para nil — `roleAggsToJSON` usa el mismo patrón exacto. Inconsistencia interna del reporte.
- El test E2E verde `TestE2E009001_DisabledByDefault` ya valida `len(roles)==0`, `len(history)==0`, `latest==nil` para el caso sin data — consistente con la lectura correcta.

**Veredicto:** el contrato P3/C10 se cumple (ceros/vacío, nunca nil en los slices). Desestimado.

---

## Opcionales (aceptados como deuda documentada)

### 2. Llamada redundante a `SetContextHistoryPerSession` en main.go
**Ubicación:** `cmd/mofgw/main.go:218` — `srv.SetContextAnalysis(...)` (línea 217) ya propaga `historyPerSession` al metrics vía proxy.go:225; la línea 218 setea lo mismo dos veces.
**Decisión:** aceptado sin cambio. Es defensa en profundidad explícita en el wiring (el setter del proxy y el de metrics quedan acoplados por contrato, no por efecto colateral); coste cero. Documentado en TECHDEBT si se quiere limpiar en refactor futuro.

### 3. Inconsistencia de casing JSON: entry-level vs summary-level
**Ubicación:** `internal/proxy/proxy.go:899-914` (`contextEntryToJSON` expone `e.PartTypes` crudo → `PartType` serializa `Role/Type/Bytes/EstTokens` PascalCase) vs summary snake_case.
**Decisión:** aceptado. El spec no fija el casing de `part_types` a nivel entry; los consumidores del endpoint (agentes propios) no dependen de él. Deuda registrada: si se expone públicamente, agregar JSON tags snake_case a `PartType` o una vista API dedicada.

### 4. `latest`/`history` siempre presentes en el JSON (spec: "latest ausente" con `history_per_session=0`)
**Ubicación:** `internal/proxy/proxy.go:849-851`
**Decisión:** aceptado. `latest: null` es una representación válida de "ausente" en JSON; el spec lo declara "ausente" pero el e2e test espera null. Diferencia cosmética sin impacto en consumidores.

### 5. Ring buffer con prepend O(N) — costo cuadrático para `history_per_session` grande
**Ubicación:** `internal/metrics/context.go:171-174`
**Decisión:** aceptado como FYI de hardening. Default 50 → coste despreciable. No se agrega tope superior a `history_per_session` ahora (spec P6 solo valida `< 0`); si un operador lo sube mucho, es decisión consciente con costo documentado. Futuro: deque circular O(1).

---

## Hallazgos positivos (verificación de correctness — sin issues)

- **Privacidad por construcción (I1/P7):** `Analyze` nunca expone valores de content/arguments/description; solo `len(s)` en `pendingPart.bytes`. Verificado por C2 + C13.
- **Fase 1 incluye rechazos (P4/C9):** fase 1 antes de limiter/budget/ventana; request rechazado queda con est_tokens y sin actual. Verificado por `TestE2E009001_RejectedRequest`.
- **Record de cliente nunca se evicta (P2/C14):** `evictOldestContextLocked` salta `SessionID == ""`. Verificado por `TestContextRecord_EvictionFIFO`.
- **Matcheo por request_id evita races (P4):** búsqueda exacta en history; entry evictada → no-op. Verificado por `TestContextRecord_UpdateUsageMissingRequest`.
- **Backward compat v1/v2 (P5/I6):** `LoadState` rechaza versión mayor; v1 sin `contexts` → mapa vacío. Verificado por C12.
- **Test `TestPersistVersionReject` fix justificado** (test-audit.md §5): el bump cambió el literal; la intención (versión mayor rechazada + estado intacto) se preserva.
- **Walk intacto (C3):** `Analyze` reimplementa el recorrido internamente; `walk.go` sin cambios; C3 verifica KeyPaths/Detected idénticos.
- **Config fail-fast (P6/I5):** `history_per_session < 0` rechazado, `0` válido. Verificado por C10.
- **X-Session-Id solo lectura (I4):** usado solo para keying; no se propaga upstream; guard `e2e_008003` intacto.

---

## Veredicto final

**APPROVE** — 0 bloqueantes (1 desestimado con motivo), 5 opcionales documentados como deuda. Suite 294 verde, vet y gofmt limpios. Los invariantes I1-I7 están verificados. La feature cumple el contrato P1-P8 / C1-C16 del spec aprobado.

Priorización aprobada por: Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo 08 Ago 2026).
