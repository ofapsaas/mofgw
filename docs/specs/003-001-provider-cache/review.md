# Review — 003-001-provider-cache

> Rol: reviewer (Etapa 4, two-layer). Fecha: 2026-08-06.
>
> **⚠️ LIMITACIÓN DE PROCESO DECLARADA:** esta revisión se ejecutó INLINE
> por el orquestador (opción 3 del contrato §4) porque el runtime de
> delegación estuvo caído toda la sesión (10+ fallos "Failed to create
> delegation session"). El invariante anti-bias exige reviewer de familia
> de modelo DISTINTA al implementer; acá ambos son el mismo agente
> (deepseek-v4-flash). Para mitigar: se aplicó revisión adversarial
> (búsqueda activa de fallas, no aprobación por defecto), se encontraron
> y corrigieron 2 hallazgos reales, y se exige **validación externa
> independiente antes del deploy a producción** (re-ejecutar el handoff
> en `handoff-reviewer.md` cuando el runtime se recupere).

## Verdict: APPROVE con fixes aplicados (0 blockers, 0 majors, 2 minors corregidos)

Tras aplicar los fixes de review, la feature cumple las postcondiciones
P1-P6 y los criterios C1-C6 del spec. Suite completa verde con `-race`.

## Findings (los 2 encontrados se corrigieron en commit 1bdc946)

| # | Severidad | Postcond/Sec | Hallazgo | Evidencia | Fix aplicado |
|---|-----------|--------------|----------|-----------|--------------|
| H1 | Minor→corregido | P4 / stream | **Race potencial en CaptureUsage:** si `sw.Copy` retorna temprano por error (stream interrumpido, ev.Err o error de escritura), el caller leía `events.Usage()` mientras la goroutine podía seguir escribiendo `cs.usage`. En path normal no hay race (happens-before por send FIFO + close del canal), pero el path de error sí. `-race` no lo detectó porque ningún test ejercita ese path. | stream.go:151-174 + proxy.go handleStream (pre-fix leía Usage() incondicionalmente) | proxy.go: solo leer `Usage()` cuando Copy retorna nil (path completo); en error, usage se deja nil (stream roto, sin valor). |
| H2 | Minor→corregido | P2 / streaming | **Desviación del spec:** si el cliente mandaba `stream_options` SIN `include_usage` (ej. solo `max_tokens`), el proxy respetaba el objeto tal cual y NO inyectaba include_usage. El spec P2 exige include_usage=true "cuando el cliente no lo especificó" — no especificarlo en un SO existente sigue siendo no especificarlo. | router.go ensureIncludeUsage (pre-fix: `if _, ok := m["stream_options"]; ok { return body }`) | router.go: parsear el SO existente; si no tiene `include_usage`, agregarlo preservando el resto; si lo tiene (true/false), respetarlo. Test nuevo TestRED_StreamCompletaIncludeUsageEnSOExistente. |

## Positives

- **Transparencia preservada:** ensureIncludeUsage usa el patrón clamp
  (json.RawMessage + SetEscapeHTML(false)) — el body del cliente solo se
  toca para lo necesario, ningún contenido de prompt/tools se altera (P5).
- **Parseo tolerante correcto:** usageInt maneja ausente/null/no-numérico
  sin crashear; la struct anidada con punteros (`*struct`) distingue
  "campo ausente" de "campo null" correctamente (P1, C1).
- **Robustez P6:** upstream sin usage → respuesta normal al cliente,
  contadores 0, sin 500. Criterio C5 cubierto por test E2E.
- **Cardinalidad de métricas acotada:** mapa keyed `provider|model` (igual
  patrón que rejected map) — cardinalidad = providers configurados ×
  modelos servidos, sin riesgo de explosión (P3).
- **Streaming transparente:** CaptureUsage reenvía eventos intactos; el
  cliente recibe el chunk de usage tal cual (con el id estable reescrito
  por Copy, comportamiento existente) (P2/P4).
- **Privacidad:** los campos nuevos de request_end son numéricos
  (cache_hit/miss/reasoning) — no se filtra contenido (P4 + regla 005-005).
- **Commits granulares** RED/GREEN separados; cambios de tests
  justificados en spec.md (TestNamesSorted 5→8, fixture malformado).

## Test suite verification

- `go test ./... -race`: 13 paquetes ok, 0 FAIL (verificado tras fixes).
- `go vet ./...`: limpio.
- `gofmt -l internal/`: limpio.
- Cobertura de postcondiciones: P1 (no-stream, ausentes, no-numérico) ✓;
  P2 (inyección, respeto explícito, SO existente, no-stream intacto) ✓;
  P3 (no-stream 3 req, sin-usage 0, stream 400/100) ✓; P4 (logs no-stream,
  metrics stream) ✓; P5 (TestRawBodyPreserved + NoStreamNoInyecta) ✓;
  P6 (no crashea, respuesta intacta) ✓.

## Pendiente de proceso (no bloqueante para la feature, bloqueante para PROD)

- [ ] Re-ejecutar review externo independiente (`handoff-reviewer.md`)
      cuando el runtime de delegación se recupere — validación anti-bias
      del invariante.
- [ ] Tras la validación externa: deploy a producción + smoke test real
      (mofgw.service, verificar /metrics con contadores de cache en
      tráfico real).
