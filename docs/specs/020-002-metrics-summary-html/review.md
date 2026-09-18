# review.md — 020-002-metrics-summary-html (etapa 4, Review two-layer)

## Veredicto

**NEEDS_FIX — 0 bloqueantes, 3 Majors (F1/F2/F3), 2 Minors, 6 Advisories.** Todos los Majors y Minors resueltos pre-merge (`8d79a98`). El GREEN es sólido en arquitectura, wiring, auth, read-only y determinismo.

## Capa 1 — P1-P17 + I1-I6

| # | Veredicto | Evidencia |
|---|---|---|
| P1 | PASS | `mux.HandleFunc("GET /v1/metrics/summary", ...)` proxy.go:385 — pattern con método → 405; única ruta nueva; B10 congela POST/DELETE/PUT→405 |
| P2 | PASS | `publicPrefixes` sin cambios; ruta no listada → Bearer; B3 aserta 401+`invalid_api_key` |
| P3 | PASS | `time.Parse("2006-01-02")` estricto; B2: 6 inválidos + 1 válido |
| P4 | PASS | `registryPath==""` → 503 "registry file not configured" — idéntico a clientconfig; main.go:242 incondicional (D3 literal) |
| P5 | PASS | MissingActive → aviso; B9; rotados se leen aunque el activo falte |
| P6 | PASS | `ev.Type != "terminal"` → skip sin agregar; B1 incluye attempt del día |
| P7 | PASS | `ts[:10]!=date` → skip; B5 con D-1/D+1 en activo y rotados, cero doble conteo |
| P8 | PASS | Loop `.1..4` con break; `.gz` nunca matchea; B5 con `.1.gz` |
| P9 | PASS | Agregados exactos; `cost_usd` suma solo non-nil; `cost_usd_up` NUNCA se suma; `0.0` cuenta presente |
| P10 | PASS (F5 endurecido) | Implementación correcta; B1 aserta valores EXACTOS de cobertura post-fix |
| P11 | PASS | `model==""` → fila "desconocido"; B1 la congela |
| P12 | PASS (F1/F2 aplicados) | ErrTooLong → descarta línea gigante y RE-ARRANCA el parse (el resto del archivo SÍ se parsea); objeto sin `type` y ts malformado cuentan como corruptas; attempt (evento válido) skip silencioso |
| P13 | PASS | Scanner línea a línea, buffer acotado; ningún archivo en memoria completa |
| P14 | PASS (F3 aplicado) | Content-Type correcto; CSS inline; fecha UTC; **html.EscapeString en el model** (XSS del propio registro cerrado) |
| P15 | PASS | Solo os.Stat/os.Open; B7 (bytes antes/después) |
| P16 | PASS (F12 congelado) | Determinismo: sort.Strings(models), floats formato fijo; **B1 ahora congela 2 requests → mismo HTML byte a byte** |
| P17 | PASS | stdlib only; go.mod/go.sum sin diffs |
| I1 | PASS | internal/registry/ sin diffs (verificado por diff-stat del GREEN) |
| I2 | PASS | Solo lectura; sin emisiones; no toca contadores de /metrics |
| I3 | PASS | stdlib only |
| I4 | PASS | Una ruta nueva; publicPrefixes intacto; config schema intacto; SetRegistryPath incondicional (D5 literal) |
| I5 | PASS | Todo el estado por request; registryPath inmutable post-arranque |
| I6 | PASS (F1 aplicado) | ErrTooLong manejado; corruptas contadas; ningún contenido aborta |

## Capa 2 — Findings

**F1 — Major (RESUELTO, `8d79a98`):** línea >1MiB → ErrTooLong abortaba el resto del archivo silenciosamente. Fix: ante ErrTooLong, descarta el resto de la línea gigante hasta `\n` (contada corrupta) y RE-ARRANCA el scanner — el resto del archivo SÍ se parsea. El contador sub-reportaba.
**F2 — Major (RESUELTO):** P12 lista 3 categorías de corruptas (JSON inválido, objeto sin `type`, ts malformado); el código solo contaba la primera. Fix: `Corruptas++` en objeto sin `type` y ts malformado (len<10). **El `attempt` (evento VÁLIDO del registro, P6) NO cuenta — skip silencioso.**
**F3 — Major (RESUELTO):** HTML escaping ausente — un `model` con contenido HTML/JS del write path se renderizaba raw (XSS del propio registro). Fix: `html.EscapeString(display)` en el render.
**F4 — Minor (RESUELTO):** aserciones no-op (`t.Logf` que nunca falla) sobre contador de corruptas y totales → aserciones EXACTAS (`"líneas corruptas: 2"`, `"terminales del día: 6"`).
**F5 — Minor (RESUELTO):** aserción de cobertura débil (solo palabras, no números) + comentario con aritmética mal → aserciones por fila EXACTAS (`"<tr><td>upstream</td><td>2</td>"`, etc.).
**F6 — Minor (disclosure, no bloqueante):** GREEN arrastró fixes de test (mixing de disciplina) — auditados ítem por ítem: fixes de compilación legítimos (import sin uso, firma inventada del RED), fixture hist→non-hist (MÁS fiel a 020-001), aserción cost más fuerte. **Ningún cambio debilita; dos endurecen.** Registrado como costo de la ejecución inline.
**F7 — Advisory:** stat-then-open race con rotación concurrente → fail-soft correcto; I5 lo tolera.
**F8 — Advisory (RESUELTO):** FilesRead post-Open exitoso.
**F9 — Advisory (RESUELTO):** bloque vacío muerto eliminado (refactor F1/F2 lo absorbió).
**F10 — Advisory:** referencias de spec erróneas en comentarios (cosmético).
**F11 — Advisory:** ts de otra TZ/malformado → F2 absorbe.
**F12 — Minor (RESUELTO):** determinismo P16 sin test → B1 congela 2 requests → mismo HTML.

## Disclosure anti-bias

Reviewer: **GLM (glm-5.3-flash, Z.ai)** en sesión fresca. Implementer: orquestador inline (también GLM) → review mono-familia (degradación A2 del epic, registrada). Mitigaciones: test-audit commiteado antes del GREEN + audit de diff RED→GREEN + calibración sin teatro. Suite no re-ejecutada por el reviewer (bash restringido); la 997/37 `-race` es del orquestador.

## Conclusión

F1/F2/F3/F4/F5/F8/F9/F12 aplicados pre-merge; suite **997/37 `-race` verde** + vet limpio (re-corridos por el orquestador post-fixes). El epic 020 cierra con esta feature.

Status: Review **NEEDS_FIX** by cdad-reviewer (GLM/Z.ai) on 2026-09-18
Status: **Sign-off HITL** by Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode) on 2026-09-18 — F1/F2/F3/F4/F5/F12 resueltos pre-merge; F6 disclosure; advisories aceptados. Gate 4→5 DESBLOQUEADO → merge + memory bank (etapa 5)
