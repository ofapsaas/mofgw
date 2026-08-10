# TECHDEBT.md — Deuda técnica y funcional del programa mofgw

> Registro consolidado de deuda técnica/funcional. Cada entrada tiene:
> estado, prioridad, origen (spec/review/feature), y descripción.
> Convención: consolidar desde los `review.md` de cada feature y las
> decisiones operativas. Creado: 2026-08-06.

## Cómo se usa

- Las features CDAD registran su "Pendiente de proceso" en su `review.md`
  (fuente primaria). Este archivo es el **índice consolidado** para mirar
  todo de un vistazo.
- Al resolver una entrada: marcarla `[x]` y añadir la fecha + cómo.
- Las entradas "decisión operativa" (config de prod, budget) requieren
  juicio de Pablo — no se auto-resuelven.

## Índice

| # | Deuda | Estado | Prioridad | Origen | Descripción |
|---|-------|--------|-----------|--------|-------------|
| 1 | Validación externa anti-bias de reviews | ✅ CERRADA 06 Ago | — | 003-001→008-003 (todos los review.md) | El runtime de delegación se recuperó y la validación externa se ejecutó (sesión paralela, evidencias en `docs/specs/external-reviews/*.resp.json`): **7 APPROVE + 2 REQUEST CHANGES** (007-001 Major: thinking_default sin thinking; 008-003 Major: max_sessions_retained sin cablear). Ambos Majors corregidos (commit 082d10c). SEC-001 adicionalmente revisado con qwen3.7-plus (familia distinta). |
| 2 | Budget para cliente zot/OpenClaw | ⏳ PENDIENTE (decisión Pablo) | MEDIA | 008-002 + config prod | zot consume ~16.8M tokens/26min (~USD verificable en /metrics). Budget comentado en config (cost_usd_max/tokens_max) — activar cuando se decida el límite. |
| 3 | Pricing/metadata reales en config prod | ✅ HECHA 06 Ago | — | 006-002/007-001 + deploy | Se cableó pricing (tasas Zen) + model_metadata + context.margin 0.1 en `~/.config/mofgw/config.yaml`. Verificado: cost_usd_total vivo. |
| 4 | deepseek-v4-flash-0731 sin pricing | ✅ CERRADA 08 Ago (parcial — pricing verificado) | BAJA | config prod | **Pricing verificado 08 Ago 13:4x (API OpenRouter `openrouter.ai/api/v1/models`):** prompt $0.09/M, completion $0.18/M, input_cache_read $0.018/M, context 1M. Además: resultados ARC-AGI comparables a gpt-5.6-luna a ~1/4 del costo (thread HN 49214008, benchmark corrió en BaseTen). ⚠️ Matiz: el costo real de mis ciclos es el de la cadena go-1/2 (opencode.ai/zen/go/v1), no OpenRouter directo — verificar si la config de costos de rsi-metrics usa tasa Zen o necesita este dato. **Actualización 08 Ago 21:1x (pricing oficial DeepSeek, api-docs.deepseek.com/quick_start/pricing/):** flash-0731 directo = input cache hit $0.0028/M, cache miss $0.14/M, output $0.28/M, contexto 1M, max output 384K, concurrency 2500. ⚠️ **Anuncio oficial de suba "significativa" de precios** (textual en la página + Bloomberg 06 Ago) — vigilar impacto en rsi-metrics si se usa DeepSeek directo. |
| 5 | Clave de un provider con 401 upstream | ✅ VERIFICADA 06 Ago — keys todas válidas; 401 era stale | MEDIA | state file previo + deploy | **Verificación completa (06 Ago 16:2x):** las 5 keys devuelven 200 en `/models` y chat completions — NO hay ninguna key con 401. El hallazgo original era de un state file previo al replanteo. **Estado real:** GO_2 (acct2) y GO_3 (go-corp) devuelven **429 GoUsageLimitError** (monthly usage limit reached, resets en ~5 y ~14 días) — problema de cuota/balance, no de key. 429 es retryable en la cadena → el fallback circular lo absorbe (sin impacto operativo). Acción residual: monitorear cuota de GO_2/GO_3; no requiere rotación de keys. |
| 6 | X-Session-Id: ventana deslizante para budget | ⏳ FUTURO | BAJA | 008-002 spec I4 + 008-003 | El registro por sesión con timestamps habilita ventana deslizante exacta; el budget usa ventana simple (desde arranque). Aplicar si el operador lo pide. |
| 7 | Auth en /metrics y /healthz (bind ampliado) | ⏳ ACEPTADA (decisión Pablo) | BAJA | deploy VPN | El bind se amplió para acceso por red confiada (VPN). healthz/metrics (sin auth por diseño) quedan visibles para los nodos de esa red — hoy solo dispositivos propios, aceptado. Si entran nodos ajenos → volver a bind por IP o agregar auth. |
| 8 | Naming `recordCacheTokens` | ⏳ COSMÉTICA | BAJA | 006-001 review H1 | El nombre ya no refleja el alcance (acumula cache + usage + costo + sesión). Renombrar a `recordUsage` en un refactor. |
| 9 | Normalización DeepSeek directo (`prompt_cache_hit_tokens`) | ⏳ FUTURO | BAJA | 003-001 spec | Hoy vía Zen ya normaliza. Si algún día se apunta a DeepSeek directo, normalizar campos no estándar. |
| 10 | Cache explícito Alibaba (cache_control) | ⏳ FUTURO | BAJA | 003-001 spec | Implícito ya cubre (hit rate 94-99% medido). Evaluar explícito si el hit rate baja. |
| 11 | Budget global (suma de clientes) | ⏳ FUTURO | BAJA | 008-002 spec | Budget por cliente hoy; global no implementado. |
| 12 | Alertas/notificación al exceder budget | ⏳ FUTURO | BAJA | 008-002 spec | Solo rechazo 429 hoy; sin alerta. |
| 13 | Persistencia de accounting entre restarts | ✅ CERRADA 06 Ago | MEDIA | 006-001/006-002/008-003 | SaveState/LoadState a JSON atómico (temp+rename): contadores, rejected, cache, usage, cost, sesiones. Config `server.state_file` + `state_save_interval` (default deshabilitado). Restore rechaza versión >1; corrupto no bloquea arranque. 7 tests, -race OK, deploy con interval 10m (state.json 0600). Commit f90c0b7. |
| 14 | Tokenizer real vs estimación len/4 | ⏳ ACEPTADA | BAJA | 008-001 spec | Rechazo por ventana usa estimación rough con margen 0.1. Adecuado para el caso de uso (prompts gigantes). |
| 15 | Auth sin rate limiting/anti-brute-force | ⏳ ACEPTADA | BAJA | SEC-001 P5 | No hay backoff/bloqueo tras intentos fallidos con Bearer inválido. Aceptada: proxy interno, firewalld filtra internet (verificado), tailnet confiado, hash 256-bit hace la fuerza bruta impráctica. Si se expone fuera del tailnet → re-evaluar. |
| 16 | Redundancia `SetContextHistoryPerSession` en main.go:218 | ⏳ ACEPTADA (refactor futuro) | LOW | 009-001 review Optional #2 | `srv.SetContextAnalysis(...)` (main.go:217) ya propaga `historyPerSession` a metrics vía proxy.go:225; la línea 218 setea lo mismo dos veces. Aceptada sin cambio: es defensa en profundidad explícita en el wiring (setter del proxy y de metrics acoplados por contrato, no por efecto colateral); coste cero. Limpiar en un refactor de wiring si molesta. |
| 17 | Casing inconsistente JSON en `/v1/context`: entry-level PascalCase vs summary snake_case | ⏳ ACEPTADA | LOW | 009-001 review Optional #3 | `contextEntryToJSON` (proxy.go:899-914) expone `e.PartTypes` crudo → `PartType` serializa `Role/Type/Bytes/EstTokens` PascalCase, mientras `summary.part_types` es snake_case. El spec no fija el casing a nivel entry; consumidores propios no dependen. Si se expone públicamente: agregar JSON tags snake_case a `PartType` o una vista API dedicada. |
| 18 | `latest`/`history` siempre presentes en el JSON de `/v1/context` ("latest ausente" con `history_per_session=0`) | ⏳ ACEPTADA | FYI | 009-001 review Optional #4 | `latest: null` es representación válida de "ausente" en JSON; el spec lo declara "ausente" y el e2e test `TestE2E009001_DisabledByDefault` espera explícitamente `body["latest"] == nil`. Diferencia cosmética sin impacto en consumidores. |
| 19 | Ring buffer con prepend O(N) en `internal/metrics/context.go:171` | ⏳ ACEPTADA | FYI | 009-001 review Optional #5 | `rec.History = append([]ContextEntry{entry}, rec.History...)` → O(N) por request; costo cuadrático para `history_per_session` grande. Default 50 → despreciable. No se agrega tope superior a `history_per_session` ahora (spec P6 solo valida `< 0`); si un operador lo sube mucho es decisión consciente con costo documentado. Futuro: deque circular O(1). |
| 20 | gofmt sucio en `internal/proxy/e2e_009000_test.go` | ⏳ PENDIENTE | LOW | 009-001 review (preexistente en HEAD) | Deuda preexistente, NO introducida por 009-001 (el diff de la feature excluye el archivo). gofmt reporta diff en HEAD. Correr `gofmt -w` sobre el archivo la próxima vez que se toque (evita ruido de diff en reviews). |
| 21 | Concurrencia sticky sin test dedicado (C13) | ⏳ ACEPTADA (hardening post-cierre) | LOW | 009-002 review Optional #3 + test-audit §9.2 | No hay test que dispare requests concurrentes de la misma sesión con sticky on; la garantía es estructural (mutex de `AffinityStore`, spec P3) + `-race` sobre la suite. Análisis del reviewer: sin TOCTOU — Get/Set atómicas individualmente; la operación compuesta no requiere atomicidad cruzada (peor caso: ambos requests registran al mismo ganador, resultado correcto). Test concurrente dedicado en hardening futuro. |
| 22 | `X-Session-Affinity` irrelevante sin test dedicado (I3/D5) | ⏳ ACEPTADA (hardening post-cierre) | FYI | 009-002 test-audit §9.3 | Ningún test manda `X-Session-Affinity` y verifica que no influye en el keying; la garantía es por construcción (P2: el proxy solo lee `X-Session-Id`; D5) + guard existente `e2e_008003` untouched. Test dedicado que envíe el header y verifique keying inalterado → hardening post-cierre. |
| 23 | Margen de contexto subestima límite real upstream (1,048,660 req → 502) | ✅ CERRADA 09 Ago 05:15 | MEDIA | Observación prod 09 Ago 04:32 + recurrencia 04:49 | **Causa raíz (doble):** (1) metadata `context_window: 1,000,000` subestimaba el límite REAL 1,048,576 (2^20, verificado en 2 errores upstream independientes); (2) el chequeo 008-001 solo miraba el prompt estimado, no `max_tokens` — el upstream valida prompt+completion contra la ventana real, y `classify()` trata 4xx como no-reintentable → la cadena se corta en el primer provider → 502 al cliente (verificado: 1 intento, sin fallback). **Fix (commit `5a3d45f` + config):** window corregida a 1,048,576 (5 modelos) + `margin: 0` (rechazo en el techo real; cualquier margen >0 deja pasar requests que upstream rechaza) + chequeo incluye `max_tokens` del request (2 tests RED→green en e2e_008001). Suite completa -race 15/15 verde. Deploy: binario nuevo + restart 05:08. La cadena sigue siendo red de seguridad para 429/5xx/timeout. |
| 24 | `fallback.sticky_routing` NO habilitado en prod (decisión) | ✅ DECIDIDO 09 Ago (no se habilita; póliza default-off) | — | Decisión operativa 09 Ago | La feature 009-002 está implementada/testeada/mergeada pero queda default off por decisión del dueño del proceso: la data real muestra que no haría diferencia (failovers=0, cooldown_hits=0, cache hit 97.7% sin sticky; la cadena nunca rotó). Re-evaluar si aparecen cooldowns recurrentes (TECHDEBT #5). Ver activeContext.md 09 Ago. |

> Nota 009-002: de los 5 opcionales del review (APPROVE), 1 fue aplicado (Nit #5 — `max_sessions_retained`
> documentado en `config.example.yaml`), 2 quedan registrados arriba (#21, #22) y 2 son nits sin registro
> (idiom `evictLRU` con flag `first` — estilo correcto, refactor cosmético; `AffinityStore` creado
> incondicionalmente — consistente con `CooldownStore`, costo ~120 bytes). El FYI "Get refresca LastUsed
> siempre" es la semántica EXACTA del spec P3 (revisado "no cambiar") — no es deuda.

## Deuda de proceso del replanteo (no técnica)

- **Runtime de delegación caído** (delegate y task, "Failed to create
  delegation session", toda la sesión del 06 Ago): obligó a ejecutar todos
  los roles CDAD inline (opción 3 del contrato §4). La limitación anti-bias
  está declarada en cada review.md. Al recuperarse el runtime, re-ejecutar
  los reviews de forma independiente (ver #1).
- **Bug de memento-query** (MEMORY.md indexado sin chunking → todo score
  1.0): reportado para registro en el proyecto memento (decisión Pablo:
  lo reporta él en el proyecto correspondiente).
- **AUDIT previo al RED no ejecutado formalmente (feature 009-001, lección
  documentada en test-audit.md §2/§9.1):** el AUDIT post-hoc detectó que el
  bump `stateVersion` 1→2 (P5) cambia un literal de fixture (`"version":1`)
  que `TestPersistVersionReject` manipulaba → el replace era no-op y el
  test falló en GREEN por la razón equivocada. Corregido en sesión B
  (commit 6e28730) preservando la intención. Lección: el AUDIT de una
  feature con bump de schema debe escanear literales de fixture que el spec
  cambia, no solo call-sites de firma. (La deuda quedó cubierta por
  `TestPersistContextV2_*` — el riesgo real era que quedara oculto.)
- **AUDIT previo al RED no ejecutado formalmente (feature 009-002, lección
  documentada en test-audit.md §2/§5.2):** el AUDIT post-hoc detectó que 5 de los
  32 tests NUEVOS de la feature nacieron con bugs de test (detectados en GREEN: 3
  por el implementer, que respetó AP-4 y no tocó tests; 2 por el orquestador;
  corregidos en sesión B, commit 1a94eb9, sin relajar ningún assert). Lección
  extendida: además de escanear literales de fixture que el spec cambia (009-001),
  escanear que **los contadores/harness midan lo que el test afirma medir**:
  `fakeProvider.Stream` no incrementa el contador de Complete; el health check
  (`o.Health.CheckNow()`) sondea upstreams y contamina contadores; `upstreamOK`
  solo trae `total_tokens` → costo 0 → el budget nunca dispara. Los fixes quedan
  documentados, no ocultos.

## Cambios

- 2026-08-09: #24 agregada (decisión: sticky_routing NO se habilita en prod — póliza default-off; evidencia failovers=0/cooldown_hits=0/cache hit 97.7%).
- 2026-08-09: #23 agregada (margen de contexto subestima límite real upstream — 502 04:32, hallazgo de telemetría prod).
- 2026-08-09: #21-#22 agregadas (009-002-sticky-session, deuda aceptada del review APPROVE + test-audit — hardening post-cierre) + lección de proceso AUDIT post-hoc (test-audit.md §2/§5.2: contadores deben medir lo que el test afirma).
- 2026-08-09: #16-#20 agregadas (deuda opcional del review 009-001-context-composition, APPROVE 0 bloqueantes) + lección de proceso AUDIT post-hoc (test-audit.md §2).
- 2026-08-08 13:5x: #4 cerrada parcial — pricing verificado (OpenRouter API: $0.09/$0.18/$0.018, ctx 1M) + ARC-AGI comparable a Luna a 1/4 costo (HN 49214008). Matiz: costo real vía cadena Zen.
- 2026-08-06 16:35: #13 cerrada — persistencia implementada y deployada (commit f90c0b7).
- 2026-08-06 16:2x: #5 verificada — 401 era stale; keys todas válidas (200). GO_2/GO_3 en 429 por cuota mensual (resets 5/14 días), cubierto por fallback. Sin acción de rotación requerida.
- 2026-08-06 15:50: #1 completada de facto — el resp.json de 006-002 estaba truncado (finish=None, sin veredicto); hallazgo NaN/Inf de precios verificado REAL (yaml `.inf`/`.nan` pasaban `< 0`) y corregido: rechazo de precios no-finitos en config.go + test (commit de este ciclo). Review.md de las 9 features actualizados con estado de validación externa.
- 2026-08-06: creado con la consolidación del replanteo + deuda operativa.
