# Review — 018-001-opencode-session-header

Fecha: 2026-09-03 · Sesión reviewer aislada (modelo general, familia distinta al implementer deepseek). Nota mecánica: `delegate` roto en el runtime (3 timeouts, 0 tokens — issue infra opencode), el reviewer corrió vía `task` con agente general instruido read-only. Garantías de fondo preservadas: sesión aislada de contexto fresco + familia de modelo distinta. Desviación documentada.

## Veredicto

**APPROVE_WITH_NITS** — bloqueantes: 0 · importantes: 0 · nits: 4.

## Hallazgos (resumen; reporte completo en sesión del reviewer)

### Correctness — sin problemas
- P1-P11 todos implementados y con test (evidencia archivo:línea verificada por reviewer).
- **I2 no-leak verificado exhaustivamente**: gate por knob en `provider.Client` (provider.go:486); paths auditados: chat stream/no-stream vía router, fallback a provider sin knob, `/v1/responses` (sin meta → zero-value → nunca inyecta), Health, embeddings. **Cero leak.**
- I3 body intacto; I6 stateless (RequestMeta context-key privada, inmutable); I4 sin impersonación (aseverado en test).
- Sanitización byte-wise sobre UTF-8 multibyte produce varios `_` por rune — consistente con D4, no viola spec.

### Readability / Architecture / Security / Performance — OK
- Convenciones del repo respetadas (español, SPDX, patrón de errores).
- `var Version` (no const) es decisión correcta para ldflags `-X`.
- Compat variádica: callsites previos compilan sin cambios.
- Session ids jamás en logs; header inyectado fuera del allowlist de telemetría (P8).
- O(1) por request, zero-alloc en paths sin knob, sin locks nuevos.

### Tests — relevancia OK
- Mapeo 1:1 B1-B9 → P1-P10; cero dependencia de estructura interna (config YAML + headers capturados).
- Fixes AP-4 (e5ccf1d) **no debilitan el contrato**: B3 sigue ejercitando sanitización+clamp completo; B7 solo vuelve elegible el body (condición 010-001/010-002) — sentido P7 íntegro.

### Empírico (corrido por el reviewer)
- `go vet ./...` limpio; `gofmt -l internal/`: 14 archivos preexistentes (016/017 — deuda fuera de alcance); `go test ./... -race` → **733/29 verde**.

## Nits y resolución

| # | Hallazgo | Resolución |
|---|----------|------------|
| 1 | proxy.go:546 comentario stale "NUNCA upstream" (008-003 I1) — con 018-001 el valor derivado SÍ sale a providers con knob | **APLICAR** (precisión documental) |
| 2 | `/v1/responses` hacia provider con knob dispara warn `opencode_session_header_omitido` por request (endpoint que por diseño no puebla meta — es el path principal de Odoo) → ruido de log en producción | **APLICAR** (silenciar warn cuando el context no porta meta; mantener warn cuando porta meta y el valor queda vacío) |
| 3 | Sin test para ">128 chars todos válidos" (clamp puro por longitud) — mismo code path que el subtest existente | Desestimado: path idéntico ya cubierto; cobertura marginal |
| 4 | `build.UserAgent` var mutable a runtime | Desestimado: riesgo teórico interno; función getter sería churn sin valor hoy |

Suite tras fixes debe seguir verde. Status: **Approved** — sin bloqueantes; gate 4→5 OK tras aplicar #1/#2.

Materializado por el orquestador desde el reporte del reviewer.
